//! Private statistics snapshot preparation; wire fields remain unchanged.
use super::*;

pub(super) fn statistics_snapshot(conn: &DbConnection, ticket_id: &str) -> (Vec<PageActivityDailyJson>, Vec<ActionActivityDailyJson>, String) {
    let mut page_activity_daily: Vec<_> = conn
        .db
        .ticketremote_service_member_daily_activity()
        .iter()
        .filter(|row| row.ticket_id == ticket_id)
        .map(|row| PageActivityDailyJson {
            account_scope_id: row.account_scope_id,
            day: row.day,
            hourly_ticks: row.hourly_ticks,
            first_tick_at: row.first_tick_at,
            last_tick_at: row.last_tick_at,
            updated_at: row.updated_at,
            expires_at: row.expires_at,
        })
        .collect();
    sort_page_activity_daily(&mut page_activity_daily);
    let mut action_activity_daily: Vec<_> = conn.db.ticketremote_service_member_daily_actions().iter()
        .filter(|row| row.ticket_id == ticket_id)
        .map(|row| ActionActivityDailyJson {
            account_scope_id: row.account_scope_id,
            day: row.day,
            registration_attempts: row.registration_attempts,
            registration_successes: row.registration_successes,
            control_code_attempts: row.control_code_attempts,
            control_code_successes: row.control_code_successes,
        }).collect();
    action_activity_daily.sort_by(|a, b| b.day.cmp(&a.day).then_with(|| a.account_scope_id.cmp(&b.account_scope_id)));
    let action_statistics_started_at = conn.db.ticketremote_service_action_statistics_tracking().iter()
        .find(|row| row.id == ticket_id).map(|row| row.started_at).unwrap_or_default();
    (page_activity_daily, action_activity_daily, action_statistics_started_at)
}

pub(super) fn sort_page_activity_daily(rows: &mut [PageActivityDailyJson]) {
    rows.sort_by(|left, right| {
        right
            .day
            .cmp(&left.day)
            .then_with(|| left.account_scope_id.cmp(&right.account_scope_id))
    });
}

