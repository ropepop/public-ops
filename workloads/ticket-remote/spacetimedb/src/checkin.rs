//! Voluntary reports. No stream demand or phone command is created here.
use super::*;
use std::collections::BTreeMap;

const ACTIVE_MS: i64 = 40 * 60_000;
const HISTORY_MS: i64 = 120 * 60_000;

#[spacetimedb::table(accessor = ticketremote_train_checkin)]
#[derive(Clone)]
pub struct TicketremoteTrainCheckin {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    pub email: String,
    pub requestId: String,
    pub direction: String,
    pub carriage: u8,
    pub checkedInAtMs: i64,
    pub activeUntilMs: i64,
    pub historyUntilMs: i64,
    pub status: String,
}

// Per-account revision fences delayed retries even after the report expires.
#[spacetimedb::table(accessor = ticketremote_train_checkin_account)]
#[derive(Clone)]
pub struct TicketremoteTrainCheckinAccount {
    #[primary_key]
    pub id: String,
    pub revision: String,
    pub noticePageId: String,
    pub noticeUntilMs: i64,
}

#[derive(Clone, SpacetimeType)]
pub struct TicketremoteCheckinSelf {
    pub id: String,
    pub revision: String,
    pub direction: String,
    pub carriage: u8,
    pub checkedInAtMs: i64,
    pub activeUntilMs: i64,
    pub status: String,
    pub noticePageId: String,
    pub noticeUntilMs: i64,
}

#[derive(Clone, SpacetimeType)]
pub struct TicketremoteCheckinGroup {
    pub id: String,
    pub direction: String,
    pub carriage: u8,
    pub status: String,
    pub count: u32,
    pub latestAtMs: i64,
}

#[spacetimedb::view(accessor = ticketremote_member_checkin, public, primary_key = id)]
pub fn own(ctx: &ViewContext) -> Vec<TicketremoteCheckinSelf> {
    let Some(binding) = member_view_binding(ctx, false) else { return vec![]; };
    let id = member_id(&binding.ticketId, &binding.email);
    let account = ctx.db.ticketremote_train_checkin_account().id().find(&id);
    let row = ctx.db.ticketremote_train_checkin().id().find(&id);
    vec![TicketremoteCheckinSelf { id: "self".into(),
        revision: account.as_ref().map(|v| v.revision.clone()).unwrap_or_default(),
        direction: row.as_ref().map(|v| v.direction.clone()).unwrap_or_default(),
        carriage: row.as_ref().map(|v| v.carriage).unwrap_or_default(),
        checkedInAtMs: row.as_ref().map(|v| v.checkedInAtMs).unwrap_or_default(),
        activeUntilMs: row.as_ref().map(|v| v.activeUntilMs).unwrap_or_default(),
        status: row.map(|v| v.status).unwrap_or_default(),
        noticePageId: account.as_ref().map(|v| v.noticePageId.clone()).unwrap_or_default(),
        noticeUntilMs: account.map(|v| v.noticeUntilMs).unwrap_or_default() }]
}

#[spacetimedb::view(accessor = ticketremote_member_checkin_groups, public, primary_key = id)]
pub fn groups(ctx: &ViewContext) -> Vec<TicketremoteCheckinGroup> {
    let Some(binding) = member_view_binding(ctx, false) else { return vec![]; };
    let mut groups = BTreeMap::<String, TicketremoteCheckinGroup>::new();
    for row in ctx.db.ticketremote_train_checkin().ticketId().filter(&binding.ticketId) {
        if !ctx.db.ticketremote_ticket_member().id().find(member_id(&binding.ticketId, &row.email))
            .is_some_and(|member| member.active) && !invitations::active_guest_for_view(ctx, &binding.ticketId, &row.email) { continue; }
        let id = format!("{}:{}:{}", row.direction, row.carriage, row.status);
        let group = groups.entry(id.clone()).or_insert(TicketremoteCheckinGroup {
            id, direction: row.direction, carriage: row.carriage, status: row.status,
            count: 0, latestAtMs: 0 });
        group.count += 1;
        group.latestAtMs = group.latestAtMs.max(row.checkedInAtMs);
    }
    groups.into_values().collect()
}

fn account(ctx: &ReducerContext, id: &str) -> TicketremoteTrainCheckinAccount {
    ctx.db.ticketremote_train_checkin_account().id().find(id.to_string()).unwrap_or(TicketremoteTrainCheckinAccount {
        id: id.into(), revision: String::new(), noticePageId: String::new(), noticeUntilMs: 0 })
}

fn valid_id(id: &str) -> bool {
    !id.is_empty() && id.len() <= 80 && id.bytes().all(|v| v.is_ascii_alphanumeric() || matches!(v, b'-' | b'_'))
}

fn validate(direction: &str, carriage: u8) -> Result<(), String> {
    if !matches!(direction, "towards_riga" | "away_from_riga") || !(1..=4).contains(&carriage) {
        return Err("checkin_invalid_selection".into());
    }
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_member_check_in(ctx: &ReducerContext, ticketId: String, requestId: String,
    expectedRevision: String, direction: String, carriage: u8) -> Result<(), String> {
    validate(&direction, carriage)?;
    if !valid_id(&requestId) { return Err("checkin_invalid_request".into()); }
    let ticket = clean_ticket_id(&ticketId);
    let email = invitations::viewer_actor(ctx, &ticket)?;
    let id = member_id(&ticket, &email);
    let mut account = account(ctx, &id);
    if account.revision == requestId { return Ok(()); }
    if account.revision != expectedRevision { return Err("checkin_changed".into()); }
    let clock = ctx.timestamp.to_micros_since_unix_epoch() / 1_000;
    account.revision = requestId.clone();
    upsert_row!(ctx, ticketremote_train_checkin_account, account);
    upsert_row!(ctx, ticketremote_train_checkin, TicketremoteTrainCheckin {
        id, ticketId: ticket.clone(), email: email.clone(), requestId, direction, carriage,
        checkedInAtMs: clock, activeUntilMs: clock + ACTIVE_MS, historyUntilMs: clock + HISTORY_MS,
        status: "active".into() });
    replace_policy_boundary_timer(ctx, &ticket, "checkin", &email, clock + ACTIVE_MS, &now(ctx));
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_member_check_out(ctx: &ReducerContext, ticketId: String, expectedRevision: String) -> Result<(), String> {
    let ticket = clean_ticket_id(&ticketId);
    let email = invitations::viewer_actor(ctx, &ticket)?;
    let id = member_id(&ticket, &email);
    if account(ctx, &id).revision != expectedRevision { return Err("checkin_changed".into()); }
    if let Some(mut row) = ctx.db.ticketremote_train_checkin().id().find(&id) {
        if row.status == "active" {
            row.status = "checked_out".into();
            let until = row.historyUntilMs;
            ctx.db.ticketremote_train_checkin().id().update(row);
            replace_policy_boundary_timer(ctx, &ticket, "checkin", &email, until, &now(ctx));
        }
    }
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_member_claim_checkin_notice(ctx: &ReducerContext, ticketId: String, pageId: String) -> Result<(), String> {
    if !valid_id(&pageId) { return Err("checkin_invalid_request".into()); }
    let ticket = clean_ticket_id(&ticketId);
    let email = invitations::viewer_actor(ctx, &ticket)?;
    let clock = ctx.timestamp.to_micros_since_unix_epoch() / 1_000;
    let mut account = account(ctx, &member_id(&ticket, &email));
    if account.noticeUntilMs > clock { return Ok(()); }
    if !ctx.db.ticketremote_train_checkin().ticketId().filter(&ticket)
        .any(|row| row.email != email && row.status == "active" && row.activeUntilMs > clock && (is_member(ctx, &ticket, &row.email) || invitations::is_guest_actor(ctx, &ticket, &row.email))) {
        return Ok(());
    }
    account.noticePageId = pageId;
    account.noticeUntilMs = clock + HISTORY_MS;
    upsert_row!(ctx, ticketremote_train_checkin_account, account);
    Ok(())
}

pub(super) fn expire(ctx: &ReducerContext, ticket: &str, email: &str) {
    let table = ctx.db.ticketremote_train_checkin();
    let Some(mut row) = table.id().find(member_id(ticket, email)) else { return; };
    let clock = ctx.timestamp.to_micros_since_unix_epoch() / 1_000;
    if clock >= row.historyUntilMs { table.id().delete(&row.id); return; }
    if clock >= row.activeUntilMs && row.status == "active" { row.status = "expired".into(); }
    let next = if row.status == "active" { row.activeUntilMs } else { row.historyUntilMs };
    table.id().update(row);
    replace_policy_boundary_timer(ctx, ticket, "checkin", email, next, &now(ctx));
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn choices_and_request_ids_are_bounded() {
        for direction in ["towards_riga", "away_from_riga"] {
            for carriage in 1..=4 { assert!(validate(direction, carriage).is_ok()); }
        }
        assert!(validate("other", 1).is_err());
        assert!(validate("towards_riga", 0).is_err());
        assert!(validate("towards_riga", 5).is_err());
        assert!(valid_id("checkin-123"));
        assert!(!valid_id(""));
        assert!(!valid_id(&"x".repeat(81)));
    }
}
