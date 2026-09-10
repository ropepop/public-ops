//! Compact product counters. Command receipts own attribution and deduplication;
//! no event history, code digits, or phone protocol belongs here.
use super::*;

const RETENTION_DAYS: u64 = 30;

#[spacetimedb::table(accessor = ticketremote_member_daily_actions,
    index(accessor = ticketDay, btree(columns = [ticketId, day])),
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))
)]
#[derive(Clone)]
pub struct TicketremoteMemberDailyActions {
    #[primary_key]
    pub id: String,
    pub ticketId: String,
    pub accountScopeId: String,
    pub day: String,
    pub registrationAttempts: Vec<u32>,
    pub registrationSuccesses: Vec<u32>,
    pub controlCodeAttempts: Vec<u32>,
    pub controlCodeSuccesses: Vec<u32>,
    pub expiresAt: String,
}

// One persistent start watermark per product, not another usage-history store.
#[spacetimedb::table(accessor = ticketremote_action_statistics_tracking)]
#[derive(Clone)]
pub struct TicketremoteActionStatisticsTracking {
    #[primary_key]
    pub id: String,
    pub startedAt: String,
}

pub(super) fn ensure_tracking(ctx: &ReducerContext, ticket: &str) {
    let table = ctx.db.ticketremote_action_statistics_tracking();
    if table.id().find(ticket.to_string()).is_none() {
        table.insert(TicketremoteActionStatisticsTracking {
            id: ticket.into(),
            startedAt: now(ctx),
        });
    }
}

fn counted_kind(operation: &str, source: &str) -> Option<&'static str> {
    match (operation, source) {
        ("register_current", "browser_slider") => Some("registration"),
        ("control_code", _) => Some("control_code"),
        _ => None,
    }
}

fn bucket_at(accepted_at: &str) -> Option<MemberActivityBucket> {
    let utc = DateTime::parse_from_rfc3339(accepted_at)
        .ok()?
        .with_timezone(&Utc);
    member_activity_bucket_with_retention(utc, RETENTION_DAYS).ok()
}

fn empty_day(
    ticket: &str,
    email: &str,
    bucket: &MemberActivityBucket,
) -> TicketremoteMemberDailyActions {
    let scope = account_scope_id(email);
    TicketremoteMemberDailyActions {
        id: member_activity_row_id(ticket, &scope, &bucket.day),
        ticketId: ticket.into(),
        accountScopeId: scope,
        day: bucket.day.clone(),
        registrationAttempts: vec![0; 24],
        registrationSuccesses: vec![0; 24],
        controlCodeAttempts: vec![0; 24],
        controlCodeSuccesses: vec![0; 24],
        expiresAt: bucket.expires_at.clone(),
    }
}

fn increment(
    row: &mut TicketremoteMemberDailyActions,
    kind: &str,
    hour: usize,
    success: bool,
) -> bool {
    let (attempts, successes) = match kind {
        "registration" => (
            &mut row.registrationAttempts,
            &mut row.registrationSuccesses,
        ),
        "control_code" => (&mut row.controlCodeAttempts, &mut row.controlCodeSuccesses),
        _ => return false,
    };
    let (Some(attempts), Some(successes)) = (attempts.get_mut(hour), successes.get_mut(hour))
    else {
        return false;
    };
    if success {
        if *successes >= *attempts {
            return false;
        }
        *successes = successes.saturating_add(1);
    } else {
        *attempts = attempts.saturating_add(1);
    }
    true
}

/// Called only after admission succeeded and after the existing receipt check.
/// A rejected reducer transaction cannot retain this counter or its receipt.
pub(super) fn record_attempt(
    ctx: &ReducerContext,
    ticket: &str,
    email: &str,
    operation: &str,
    source: &str,
) -> Option<String> {
    let kind = counted_kind(operation, source)?;
    let bucket = bucket_at(&now(ctx))?;
    ensure_tracking(ctx, ticket);
    let table = ctx.db.ticketremote_member_daily_actions();
    let id = member_activity_row_id(ticket, &account_scope_id(email), &bucket.day);
    let existing = table.id().find(&id);
    let update = existing.is_some();
    let mut row = existing.unwrap_or_else(|| empty_day(ticket, email, &bucket));
    if !increment(&mut row, kind, bucket.hour, false) {
        return None;
    }
    if update {
        table.id().update(row);
    } else {
        table.insert(row);
    }
    Some(kind.into())
}

/// The existing receipt persists longer than physical commands. Set success in
/// the same transaction as the aggregate so retries cannot increment it twice.
pub(super) fn record_success(ctx: &ReducerContext, ticket: &str, backend: &str, command: &str) {
    use command::ticketremote_command_receipt;
    let receipts = ctx.db.ticketremote_command_receipt();
    let id = ticket_action_v3_row_id(ticket, backend, command);
    let Some(mut receipt) = receipts.id().find(&id) else {
        return;
    };
    let Some(kind) = receipt.statisticsKind.as_deref() else {
        return;
    };
    if receipt.statisticsSucceeded {
        return;
    }
    let Some(bucket) = bucket_at(&receipt.createdAt) else {
        return;
    };
    // Never resurrect expired aggregates for a delayed result.
    if parse_time_micros(&bucket.expires_at) <= ctx.timestamp.to_micros_since_unix_epoch() {
        return;
    }
    let table = ctx.db.ticketremote_member_daily_actions();
    let id = member_activity_row_id(
        ticket,
        &account_scope_id(&receipt.requestedEmail),
        &bucket.day,
    );
    let Some(mut row) = table.id().find(&id) else {
        return;
    };
    if !increment(&mut row, kind, bucket.hour, true) {
        return;
    }
    table.id().update(row);
    receipt.statisticsSucceeded = true;
    receipts.id().update(receipt);
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn only_slider_registration_and_control_codes_are_counted() {
        assert_eq!(
            counted_kind("register_current", "browser_slider"),
            Some("registration")
        );
        assert_eq!(counted_kind("control_code", ""), Some("control_code"));
        for (operation, source) in [
            ("register_current", "browser_button"),
            ("open_latest_and_register", "browser_slider"),
            ("redetect_latest", "scheduled"),
            ("", ""),
        ] {
            assert_eq!(counted_kind(operation, source), None);
        }
    }

    #[test]
    fn success_uses_original_riga_hour_and_cannot_exceed_accepted_requests() {
        let bucket = bucket_at("2026-09-09T20:59:59Z").unwrap();
        assert_eq!((&*bucket.day, bucket.hour), ("2026-09-09", 23));
        assert_eq!(bucket_at("2026-09-09T21:00:01Z").unwrap().day, "2026-09-10");
        let mut row = empty_day("vivi-default", "member@example.test", &bucket);
        assert!(!increment(&mut row, "registration", bucket.hour, true));
        assert!(increment(&mut row, "registration", bucket.hour, false));
        assert!(increment(&mut row, "registration", bucket.hour, true));
        assert!(!increment(&mut row, "registration", bucket.hour, true));
        assert_eq!(row.registrationAttempts[23], 1);
        assert_eq!(row.registrationSuccesses[23], 1);
        assert_eq!(row.controlCodeAttempts.iter().sum::<u32>(), 0);
        assert!(!increment(&mut row, "registration", 24, false));
    }

    #[test]
    fn retention_uses_thirty_calendar_days_including_dst() {
        let spring = bucket_at("2026-03-29T00:30:00Z").unwrap();
        assert_eq!(spring.hour, 2);
        assert_eq!(bucket_at("2026-03-29T01:30:00Z").unwrap().hour, 4);
        assert_eq!(
            parse_time_ms(&spring.expires_at),
            parse_time_ms("2026-04-27T21:00:00Z")
        );
        let first = bucket_at("2026-10-25T00:30:00Z").unwrap();
        let second = bucket_at("2026-10-25T01:30:00Z").unwrap();
        assert_eq!((first.day, first.hour), (second.day, second.hour));
        assert_eq!(
            parse_time_ms(&second.expires_at),
            parse_time_ms("2026-11-23T22:00:00Z")
        );
        assert!(bucket_at("invalid").is_none());
    }
}
