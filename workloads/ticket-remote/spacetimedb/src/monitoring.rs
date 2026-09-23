//! Monitoring is observation only: it never creates phone action authority.
use super::*;

const CHECK_MS: i64 = 5 * 60_000;
const CLAIM_MS: i64 = 60_000;
const MAX_ATTEMPTS: u32 = 3;

#[spacetimedb::table(accessor = ticketremote_monitoring_config, public)]
#[derive(Clone)]
pub struct TicketremoteMonitoringConfig {
    #[primary_key]
    pub id: String,
    pub ticketId: String,
    pub backendId: String,
    pub enabled: bool,
    pub epoch: String,
}

#[spacetimedb::table(accessor = ticketremote_monitoring_health)]
#[derive(Clone, Debug)]
pub struct TicketremoteMonitoringHealth {
    #[primary_key]
    pub id: String,
    pub ticketId: String,
    pub backendId: String,
    pub enabled: bool,
    pub epoch: String,
    pub status: String,
    pub reason: String,
    pub enabledAtMs: i64,
    pub lastCheckedAtMs: i64,
    pub problemStartedAtMs: i64,
    pub incidentId: String,
    pub alertSent: bool,
    pub sessionId: String,
    pub sequence: u64,
}

#[spacetimedb::table(accessor = ticketremote_push_subscription,
    index(accessor = ticketEmail, btree(columns = [ticketId, email])))]
#[derive(Clone)]
pub struct TicketremotePushSubscription {
    #[primary_key]
    pub id: String,
    pub ticketId: String,
    pub email: String,
    pub endpoint: String,
    pub p256dh: String,
    pub auth: String,
}

// A sent problem is the receipt needed for a recovery. Remove the row after
// recovery; this table is bounded delivery state, not another event history.
#[spacetimedb::table(accessor = ticketremote_push_delivery,
    index(accessor = ticketId, btree(columns = [ticketId])),
    index(accessor = subscriptionId, btree(columns = [subscriptionId])))]
#[derive(Clone)]
pub struct TicketremotePushDelivery {
    #[primary_key]
    pub id: String,
    pub ticketId: String,
    pub subscriptionId: String,
    pub incidentId: String,
    pub kind: String,
    pub reason: String,
    pub status: String,
    pub attempts: u32,
    pub nextAttemptAtMs: i64,
    pub claimId: String,
    pub claimUntilMs: i64,
    pub expiresAtMs: i64,
}

#[spacetimedb::view(accessor = ticketremote_service_monitoring_health, public, primary_key = id)]
pub fn service_monitoring_health(ctx: &ViewContext) -> Vec<TicketremoteMonitoringHealth> {
    let Some(ticket) = service_ticket_id_for_viewer(ctx) else {
        return vec![];
    };
    ctx.db
        .ticketremote_monitoring_health()
        .id()
        .find(phone_row_id(&ticket, "pixel"))
        .into_iter()
        .collect()
}

#[spacetimedb::view(accessor = ticketremote_service_push_subscription, public, primary_key = id)]
pub fn service_push_subscription(ctx: &ViewContext) -> Vec<TicketremotePushSubscription> {
    let Some(ticket) = service_ticket_id_for_viewer(ctx) else {
        return vec![];
    };
    ctx.db
        .ticketremote_push_subscription()
        .ticketEmail()
        .filter((&ticket,))
        .collect()
}

#[spacetimedb::view(accessor = ticketremote_service_push_delivery, public, primary_key = id)]
pub fn service_push_delivery(ctx: &ViewContext) -> Vec<TicketremotePushDelivery> {
    let Some(ticket) = service_ticket_id_for_viewer(ctx) else {
        return vec![];
    };
    ctx.db
        .ticketremote_push_delivery()
        .ticketId()
        .filter(&ticket)
        .collect()
}

fn empty_health(ticket: &str, clock: i64) -> TicketremoteMonitoringHealth {
    TicketremoteMonitoringHealth {
        id: phone_row_id(ticket, "pixel"),
        ticketId: ticket.into(),
        backendId: "pixel".into(),
        enabled: false,
        epoch: clock.to_string(),
        status: "disabled".into(),
        reason: String::new(),
        enabledAtMs: clock,
        lastCheckedAtMs: 0,
        problemStartedAtMs: 0,
        incidentId: String::new(),
        alertSent: false,
        sessionId: String::new(),
        sequence: 0,
    }
}

#[spacetimedb::reducer]
pub fn ticketremote_set_monitoring(
    ctx: &ReducerContext,
    ticketId: String,
    actorEmail: String,
    enabled: bool,
) -> Result<(), String> {
    require_service(ctx)?;
    let ticket = clean_ticket_id(&ticketId);
    require_owner(ctx, &ticket, &clean_email(&actorEmail))?;
    let clock = ctx.timestamp.to_micros_since_unix_epoch() / 1_000;
    let mut health = empty_health(&ticket, clock);
    if let Some(previous) = ctx
        .db
        .ticketremote_monitoring_health()
        .id()
        .find(&health.id)
    {
        if previous.enabled == enabled {
            return Ok(());
        }
        // Microsecond revision prevents toggle ABA within the same millisecond.
        health.epoch = ctx.timestamp.to_micros_since_unix_epoch().to_string();
    }
    health.enabled = enabled;
    health.status = if enabled { "checking" } else { "disabled" }.into();
    for delivery in ctx
        .db
        .ticketremote_push_delivery()
        .ticketId()
        .filter(&ticket)
    {
        ctx.db.ticketremote_push_delivery().id().delete(delivery.id);
    }
    let config = TicketremoteMonitoringConfig {
        id: health.id.clone(),
        ticketId: ticket,
        backendId: "pixel".into(),
        enabled,
        epoch: health.epoch.clone(),
    };
    let table = ctx.db.ticketremote_monitoring_config();
    if table.id().find(&config.id).is_some() {
        table.id().update(config);
    } else {
        table.insert(config);
    }
    store_health(ctx, health);
    Ok(())
}

fn store_health(ctx: &ReducerContext, row: TicketremoteMonitoringHealth) {
    let table = ctx.db.ticketremote_monitoring_health();
    if table.id().find(&row.id).is_some() {
        table.id().update(row);
    } else {
        table.insert(row);
    }
}

fn valid_observation(status: &str, reason: &str) -> bool {
    match status {
        "ready" => matches!(reason, "ticket_detail_activated" | "ticket_detail_unused"),
        "not_ready" => matches!(
            reason,
            "login" | "ticket_list" | "blocked" | "unknown" | "busy"
        ),
        "unavailable" => reason == "capture_unavailable",
        _ => false,
    }
}

fn observe(row: &mut TicketremoteMonitoringHealth, status: &str, reason: &str, clock: i64) {
    row.lastCheckedAtMs = clock;
    row.status = status.into();
    row.reason = reason.into();
    if status == "ready" {
        row.problemStartedAtMs = 0;
        row.incidentId.clear();
        row.alertSent = false;
    } else {
        if row.problemStartedAtMs == 0 {
            row.problemStartedAtMs = clock;
        }
        if row.incidentId.is_empty() && clock.saturating_sub(row.problemStartedAtMs) >= CHECK_MS {
            row.incidentId = format!("{}:{}", row.epoch, row.problemStartedAtMs);
        }
    }
}

fn accept_observation(
    row: &TicketremoteMonitoringHealth,
    epoch: &str,
    session: &str,
    active_session: &str,
    sequence: u64,
) -> Result<bool, String> {
    if !row.enabled || row.epoch != epoch {
        return Err("monitoring_epoch_changed".into());
    }
    if session.is_empty() || session != active_session {
        return Err("phone_control_session_changed".into());
    }
    if sequence == 0 {
        return Err("invalid_monitoring_observation".into());
    }
    Ok(row.sessionId != session || sequence > row.sequence)
}

fn fresh_observation(row: &TicketremoteMonitoringHealth, observed: i64, clock: i64) -> bool {
    // Phone timestamps are conservative server-clock estimates. Renewing their
    // anchor may move the estimate backwards by a network round trip; epoch and
    // sequence still fence old publication, and the stored check never regresses.
    observed >= row.enabledAtMs.saturating_sub(5_000)
        && observed >= row.lastCheckedAtMs.saturating_sub(5_000)
        && observed <= clock.saturating_add(5_000)
        && clock.saturating_sub(observed) <= 30_000
}

fn observation_time(row: &TicketremoteMonitoringHealth, observed: i64, clock: i64) -> i64 {
    observed
        .max(row.enabledAtMs)
        .max(row.lastCheckedAtMs)
        .min(clock)
}

#[spacetimedb::reducer]
pub fn ticketremote_report_monitoring(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    epoch: String,
    sessionId: String,
    sequence: u64,
    status: String,
    reason: String,
    observedAtMs: i64,
) -> Result<(), String> {
    require_service(ctx)?;
    let id = phone_row_id(&ticketId, &backendId);
    let mut row = ctx
        .db
        .ticketremote_monitoring_health()
        .id()
        .find(&id)
        .ok_or("monitoring_disabled")?;
    let active_session = ctx
        .db
        .ticketremote_phone_control_state()
        .id()
        .find(&id)
        .map(|phone| phone.sessionId)
        .unwrap_or_default();
    if !valid_observation(&status, &reason) {
        return Err("invalid_monitoring_observation".into());
    }
    if !accept_observation(&row, &epoch, &sessionId, &active_session, sequence)? {
        return Ok(());
    }
    let clock = ctx.timestamp.to_micros_since_unix_epoch() / 1_000;
    if !fresh_observation(&row, observedAtMs, clock) {
        return Err("monitoring_observation_stale".into());
    }
    row.sessionId = sessionId;
    row.sequence = sequence;
    let observed = observation_time(&row, observedAtMs, clock);
    observe(&mut row, &status, &reason, observed);
    reconcile_deliveries(ctx, &row, clock);
    store_health(ctx, row);
    Ok(())
}

fn subscription_id(ticket: &str, email: &str, endpoint: &str) -> String {
    format!(
        "{}:{}:{:x}",
        ticket,
        account_scope_id(email),
        Sha256::digest(endpoint.as_bytes())
    )
}

fn delete_subscription(ctx: &ReducerContext, id: &str) {
    for row in ctx
        .db
        .ticketremote_push_delivery()
        .subscriptionId()
        .filter(id)
    {
        ctx.db.ticketremote_push_delivery().id().delete(row.id);
    }
    ctx.db
        .ticketremote_push_subscription()
        .id()
        .delete(id.to_string());
}

#[spacetimedb::reducer]
pub fn ticketremote_set_push_subscription(
    ctx: &ReducerContext,
    ticketId: String,
    email: String,
    endpoint: String,
    p256dh: String,
    auth: String,
    enabled: bool,
) -> Result<(), String> {
    require_service(ctx)?;
    let ticket = clean_ticket_id(&ticketId);
    let email = clean_email(&email);
    require_admin(ctx, &ticket, &email)?;
    if !endpoint.starts_with("https://")
        || endpoint.len() > 2048
        || endpoint
            .bytes()
            .any(|b| b.is_ascii_whitespace() || b.is_ascii_control())
    {
        return Err("invalid_push_subscription".into());
    }
    let id = subscription_id(&ticket, &email, &endpoint);
    if !enabled {
        delete_subscription(ctx, &id);
        return Ok(());
    }
    let valid_key = |value: &str, min, max| {
        value.len() >= min
            && value.len() <= max
            && value
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'-' | b'_' | b'='))
    };
    if !valid_key(&p256dh, 80, 100) || !valid_key(&auth, 20, 30) {
        return Err("invalid_push_subscription".into());
    }
    let table = ctx.db.ticketremote_push_subscription();
    if table.id().find(&id).is_none() && table.ticketEmail().filter((&ticket, &email)).count() >= 10
    {
        return Err("push_device_limit".into());
    }
    let row = TicketremotePushSubscription {
        id,
        ticketId: ticket.clone(),
        email,
        endpoint,
        p256dh,
        auth,
    };
    if table.id().find(&row.id).is_some() {
        table.id().update(row);
    } else {
        table.insert(row);
    }
    if let Some(health) = ctx
        .db
        .ticketremote_monitoring_health()
        .id()
        .find(phone_row_id(&ticket, "pixel"))
    {
        if health.enabled && !health.incidentId.is_empty() {
            reconcile_deliveries(
                ctx,
                &health,
                ctx.timestamp.to_micros_since_unix_epoch() / 1_000,
            );
        }
    }
    Ok(())
}

fn queue_recovery(row: &mut TicketremotePushDelivery, clock: i64) {
    row.kind = "recovery".into();
    row.reason = "ticket_ready".into();
    row.status = "pending".into();
    row.attempts = 0;
    row.nextAttemptAtMs = clock;
    row.claimId.clear();
    row.claimUntilMs = 0;
    row.expiresAtMs = clock + HISTORY_TTL_MS;
}

fn reconcile_deliveries(ctx: &ReducerContext, health: &TicketremoteMonitoringHealth, clock: i64) {
    let table = ctx.db.ticketremote_push_delivery();
    for mut row in table.ticketId().filter(&health.ticketId) {
        if row.kind == "recovery" && health.status != "ready" && row.status != "sending" {
            table.id().delete(row.id);
            continue;
        }
        if row.kind != "problem" {
            continue;
        }
        if health.status == "ready" {
            if row.status == "sent" {
                queue_recovery(&mut row, clock);
                table.id().update(row);
            } else if row.status != "sending" {
                table.id().delete(row.id);
            }
        }
    }
    if health.incidentId.is_empty() {
        return;
    }
    for subscription in ctx
        .db
        .ticketremote_push_subscription()
        .ticketEmail()
        .filter((&health.ticketId,))
    {
        if !is_admin(ctx, &health.ticketId, &subscription.email) {
            delete_subscription(ctx, &subscription.id);
            continue;
        }
        let id = format!("{}:{}", subscription.id, health.incidentId);
        if table.id().find(&id).is_some() {
            continue;
        }
        table.insert(TicketremotePushDelivery {
            id,
            ticketId: health.ticketId.clone(),
            subscriptionId: subscription.id,
            incidentId: health.incidentId.clone(),
            kind: "problem".into(),
            reason: health.reason.clone(),
            status: "pending".into(),
            attempts: 0,
            nextAttemptAtMs: clock,
            claimId: String::new(),
            claimUntilMs: 0,
            expiresAtMs: clock + HISTORY_TTL_MS,
        });
    }
}

fn delivery_claimable(row: &TicketremotePushDelivery, clock: i64) -> bool {
    (row.status == "pending" && row.nextAttemptAtMs <= clock
        || row.status == "sending" && row.claimUntilMs <= clock)
        && row.attempts < MAX_ATTEMPTS
}

fn delivery_current(row: &TicketremotePushDelivery, health: &TicketremoteMonitoringHealth) -> bool {
    health.enabled
        && match row.kind.as_str() {
            "problem" => health.incidentId == row.incidentId && health.status != "ready",
            "recovery" => health.status == "ready",
            _ => false,
        }
}

#[spacetimedb::reducer]
pub fn ticketremote_claim_push_delivery(
    ctx: &ReducerContext,
    ticketId: String,
    deliveryId: String,
    claimId: String,
) -> Result<(), String> {
    require_service(ctx)?;
    if claimId.is_empty() || claimId.len() > 100 {
        return Err("invalid_push_claim".into());
    }
    let table = ctx.db.ticketremote_push_delivery();
    let mut row = table
        .id()
        .find(&deliveryId)
        .ok_or("push_delivery_unavailable")?;
    if row.ticketId != clean_ticket_id(&ticketId) {
        return Err("push_delivery_unavailable".into());
    }
    let subscription = ctx
        .db
        .ticketremote_push_subscription()
        .id()
        .find(&row.subscriptionId)
        .ok_or("push_subscription_unavailable")?;
    if !is_admin(ctx, &row.ticketId, &subscription.email) {
        delete_subscription(ctx, &subscription.id);
        return Ok(());
    }
    let health = ctx
        .db
        .ticketremote_monitoring_health()
        .id()
        .find(phone_row_id(&row.ticketId, "pixel"))
        .ok_or("monitoring_disabled")?;
    if !delivery_current(&row, &health) {
        table.id().delete(&row.id);
        return Ok(());
    }
    let clock = ctx.timestamp.to_micros_since_unix_epoch() / 1_000;
    if row.status == "sending" && row.claimId == claimId && row.claimUntilMs > clock {
        return Ok(());
    }
    if !delivery_claimable(&row, clock) {
        return Err("push_delivery_not_due".into());
    }
    row.status = "sending".into();
    row.attempts += 1;
    row.claimId = claimId;
    row.claimUntilMs = clock + CLAIM_MS;
    table.id().update(row);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_finish_push_delivery(
    ctx: &ReducerContext,
    ticketId: String,
    deliveryId: String,
    claimId: String,
    outcome: String,
) -> Result<(), String> {
    require_service(ctx)?;
    if !matches!(outcome.as_str(), "sent" | "retry" | "invalid" | "failed") {
        return Err("invalid_push_outcome".into());
    }
    let table = ctx.db.ticketremote_push_delivery();
    let Some(mut row) = table.id().find(&deliveryId) else {
        return Ok(());
    };
    if row.ticketId != clean_ticket_id(&ticketId)
        || row.status != "sending"
        || row.claimId != claimId
    {
        return Ok(());
    }
    let clock = ctx.timestamp.to_micros_since_unix_epoch() / 1_000;
    if outcome == "invalid" {
        delete_subscription(ctx, &row.subscriptionId);
        return Ok(());
    }
    if outcome == "sent" && row.kind == "recovery" {
        table.id().delete(&row.id);
        return Ok(());
    }
    if outcome == "sent" {
        row.status = "sent".into();
        if let Some(mut health) = ctx
            .db
            .ticketremote_monitoring_health()
            .id()
            .find(phone_row_id(&row.ticketId, "pixel"))
        {
            if health.incidentId == row.incidentId && health.status != "ready" {
                health.alertSent = true;
                store_health(ctx, health);
            } else if health.enabled && health.status == "ready" {
                queue_recovery(&mut row, clock);
            }
        }
    } else if outcome == "retry" && row.attempts < MAX_ATTEMPTS {
        row.status = "pending".into();
        row.nextAttemptAtMs = clock + 30_000 * i64::from(row.attempts);
    } else {
        // Keep failed receipt until this incident ends; otherwise reconciliation
        // would repeatedly recreate it and defeat the three-attempt bound.
        row.status = "failed".into();
    }
    row.claimId.clear();
    row.claimUntilMs = 0;
    table.id().update(row);
    Ok(())
}

fn mark_overdue(row: &mut TicketremoteMonitoringHealth, clock: i64) -> bool {
    let due = if row.lastCheckedAtMs == 0 {
        row.enabledAtMs
    } else {
        row.lastCheckedAtMs + CHECK_MS
    };
    if row.enabled && clock.saturating_sub(due) >= CHECK_MS && row.reason != "observation_overdue" {
        row.status = "unavailable".into();
        row.reason = "observation_overdue".into();
        if row.problemStartedAtMs == 0 {
            row.problemStartedAtMs = due;
        }
        if row.incidentId.is_empty() {
            row.incidentId = format!("{}:{}", row.epoch, row.problemStartedAtMs);
        }
        return true;
    }
    false
}

pub(super) fn check_due(ctx: &ReducerContext, ticket: &str, clock: i64) {
    if let Some(mut row) = ctx
        .db
        .ticketremote_monitoring_health()
        .id()
        .find(phone_row_id(ticket, "pixel"))
    {
        if mark_overdue(&mut row, clock) {
            reconcile_deliveries(ctx, &row, clock);
            store_health(ctx, row);
        }
    }
    for row in ctx
        .db
        .ticketremote_push_delivery()
        .ticketId()
        .filter(ticket)
    {
        if row.expiresAtMs <= clock {
            // A long unresolved incident still needs its sent/failed receipt to
            // prevent repeated alerts. These are at most one row per device.
            let current = ctx
                .db
                .ticketremote_monitoring_health()
                .id()
                .find(phone_row_id(ticket, "pixel"));
            if current.is_some_and(|h| h.enabled && h.incidentId == row.incidentId) {
                continue;
            }
            ctx.db.ticketremote_push_delivery().id().delete(row.id);
        }
    }
    for row in ctx
        .db
        .ticketremote_push_subscription()
        .ticketEmail()
        .filter((ticket,))
    {
        if !is_admin(ctx, ticket, &row.email) {
            delete_subscription(ctx, &row.id);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn sustained_observation_requires_fresh_five_minute_confirmation_and_resets() {
        let mut row = empty_health("test", 1_000);
        row.enabled = true;
        observe(&mut row, "not_ready", "blocked", 1_000);
        observe(&mut row, "not_ready", "unknown", 300_999);
        assert!(row.incidentId.is_empty());
        observe(&mut row, "not_ready", "blocked", 301_000);
        let incident = row.incidentId.clone();
        assert!(!incident.is_empty());
        observe(&mut row, "unavailable", "capture_unavailable", 400_000);
        assert_eq!(row.incidentId, incident);
        observe(&mut row, "ready", "ticket_detail_unused", 500_000);
        assert_eq!(row.problemStartedAtMs, 0);
        assert!(row.incidentId.is_empty());
        observe(&mut row, "not_ready", "busy", 600_000);
        assert!(row.incidentId.is_empty());
        assert!(valid_observation("ready", "ticket_detail_activated"));
        assert!(!valid_observation("ready", "unknown"));
    }
    #[test]
    fn claims_are_leased_bounded_and_recovery_starts_a_new_budget() {
        let mut row = TicketremotePushDelivery {
            id: "d".into(),
            ticketId: "t".into(),
            subscriptionId: "s".into(),
            incidentId: "i".into(),
            kind: "problem".into(),
            reason: "blocked".into(),
            status: "sending".into(),
            attempts: 2,
            nextAttemptAtMs: 0,
            claimId: "claim".into(),
            claimUntilMs: 100,
            expiresAtMs: 0,
        };
        assert!(!delivery_claimable(&row, 99));
        assert!(delivery_claimable(&row, 100));
        row.attempts = 3;
        assert!(!delivery_claimable(&row, 100));
        queue_recovery(&mut row, 200);
        assert_eq!(row.attempts, 0);
        assert_eq!(row.kind, "recovery");
        assert!(delivery_claimable(&row, 200));
        let mut health = empty_health("t", 1);
        health.enabled = true;
        health.status = "ready".into();
        assert!(delivery_current(&row, &health));
        health.status = "not_ready".into();
        assert!(!delivery_current(&row, &health));
        row.kind = "problem".into();
        health.incidentId = row.incidentId.clone();
        assert!(delivery_current(&row, &health));
        health.incidentId = "new-incident".into();
        assert!(!delivery_current(&row, &health));
        health.enabled = false;
        assert!(!delivery_current(&row, &health));
    }
    #[test]
    fn reports_fence_epoch_session_and_sequence_without_refreshing_replays() {
        let mut row = empty_health("test", 1_000);
        row.enabled = true;
        row.sessionId = "current".into();
        row.sequence = 10;
        assert!(!accept_observation(&row, &row.epoch, "current", "current", 10).unwrap());
        assert!(!accept_observation(&row, &row.epoch, "current", "current", 9).unwrap());
        assert!(accept_observation(&row, &row.epoch, "current", "current", 11).unwrap());
        assert!(accept_observation(&row, &row.epoch, "restarted", "restarted", 1).unwrap());
        assert!(accept_observation(&row, &row.epoch, "current", "restarted", 11).is_err());
        assert!(accept_observation(&row, "retired", "current", "current", 11).is_err());
        row.enabled = false;
        assert!(accept_observation(&row, &row.epoch, "current", "current", 11).is_err());
        assert_eq!(row.lastCheckedAtMs, 0);
    }
    #[test]
    fn missing_checks_alert_five_minutes_after_their_due_time_once() {
        let mut row = empty_health("test", 1_000);
        row.enabled = true;
        assert!(!mark_overdue(&mut row, 300_999));
        assert!(mark_overdue(&mut row, 301_000));
        assert!(!mark_overdue(&mut row, 401_000));
        observe(&mut row, "ready", "ticket_detail_activated", 500_000);
        assert!(!mark_overdue(&mut row, 1_099_999));
        assert!(mark_overdue(&mut row, 1_100_000));
        assert_eq!(row.status, "unavailable");
        assert_eq!(row.lastCheckedAtMs, 500_000);
        row.enabled = false;
        row.reason.clear();
        assert!(!mark_overdue(&mut row, 2_000_000));
    }
    #[test]
    fn transport_delay_and_reordered_evidence_cannot_restore_readiness() {
        let mut row = empty_health("test", 1_000);
        row.lastCheckedAtMs = 50_000;
        assert!(fresh_observation(&row, 70_000, 100_000));
        assert!(!fresh_observation(&row, 69_999, 100_000));
        assert!(fresh_observation(&row, 49_999, 60_000));
        assert!(!fresh_observation(&row, 44_999, 60_000));
        assert!(fresh_observation(&row, 105_000, 100_000));
        assert!(!fresh_observation(&row, 105_001, 100_000));
        assert!(!fresh_observation(&row, 999, 1_000));
    }

    #[test]
    fn conservative_clock_renewal_does_not_discard_a_fresh_transition() {
        let mut row = empty_health("test", 100_000);
        row.enabled = true;
        assert!(fresh_observation(&row, 98_000, 101_000));
        assert_eq!(observation_time(&row, 98_000, 101_000), 100_000);
        row.lastCheckedAtMs = 120_000;
        assert!(fresh_observation(&row, 119_500, 121_000));
        let observed = observation_time(&row, 119_500, 121_000);
        observe(&mut row, "ready", "ticket_detail_unused", observed);
        assert_eq!(row.lastCheckedAtMs, 120_000);
        assert_eq!(row.status, "ready");
        assert_eq!(observation_time(&row, 125_000, 121_000), 121_000);
        assert!(!fresh_observation(&row, 114_999, 121_000));
    }
}
