// Minimal old storage contract used only for a data-preserving migration test.
#![allow(non_snake_case)]
use spacetimedb::{CaseConversionPolicy, ReducerContext, Table};
#[spacetimedb::settings]
const CASE_CONVERSION_POLICY: CaseConversionPolicy = CaseConversionPolicy::None;

#[spacetimedb::table(accessor = ticketremote_command_receipt,
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt])))]
pub struct TicketremoteCommandReceipt {
    #[primary_key]
    pub id: String,
    pub ticketId: String,
    pub backendId: String,
    pub commandId: String,
    pub requestedEmail: String,
    pub fingerprint: String,
    pub createdAt: String,
    pub expiresAt: String,
}

#[spacetimedb::table(accessor = ticketremote_member_daily_activity,
    index(accessor = ticketDay, btree(columns = [ticketId, day])),
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt])))]
pub struct TicketremoteMemberDailyActivity {
    #[primary_key]
    pub id: String,
    pub ticketId: String,
    pub accountScopeId: String,
    pub day: String,
    pub hourlyTicks: Vec<u32>,
    pub lastTickSlot: i64,
    pub firstTickAt: String,
    pub lastTickAt: String,
    pub updatedAt: String,
    pub expiresAt: String,
}

#[spacetimedb::reducer]
pub fn fixture_migration_seed(ctx: &ReducerContext) -> Result<(), String> {
    let clock = ctx.timestamp.to_rfc3339().unwrap();
    ctx.db
        .ticketremote_command_receipt()
        .insert(TicketremoteCommandReceipt {
            id: "ticket-action-v3:stats-migration:pixel:old".into(),
            ticketId: "stats-migration".into(),
            backendId: "pixel".into(),
            commandId: "old".into(),
            requestedEmail: "fixture@example.test".into(),
            fingerprint: "old-fingerprint".into(),
            createdAt: clock.clone(),
            expiresAt: "2100-01-01T00:00:00Z".into(),
        });
    ctx.db
        .ticketremote_member_daily_activity()
        .insert(TicketremoteMemberDailyActivity {
            id: "pre-statistics-activity".into(),
            ticketId: "stats-migration".into(),
            accountScopeId: "old-account-scope".into(),
            day: "2026-09-09".into(),
            hourlyTicks: vec![1],
            lastTickSlot: 1,
            firstTickAt: clock.clone(),
            lastTickAt: clock.clone(),
            updatedAt: clock,
            expiresAt: "2100-01-01T00:00:00Z".into(),
        });
    Ok(())
}
