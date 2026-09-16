//! One durable timer per phone. Visible presence owns cancellation; stream warmth
//! is deliberately absent from both scheduling and first-tap authorization.
use super::*;

const FIRST_IDLE_MS: i64 = 40 * 60_000;
const REPEAT_MS: i64 = 30 * 60_000;
const DELIVERY_MS: i64 = 2 * 60_000;
const TARGET: &str = "refresh_current_ticket";

#[spacetimedb::table(
    accessor = ticketremote_idle_ticket_refresh,
    scheduled(ticketremote_scheduled_idle_ticket_refresh)
)]
#[derive(Clone)]
pub struct TicketremoteIdleTicketRefresh {
    #[primary_key]
    #[auto_inc]
    pub scheduled_id: u64,
    pub scheduled_at: ScheduleAt,
    #[unique]
    pub phoneId: String,
    pub ticketId: String,
    pub backendId: String,
    pub idleSinceMs: i64,
    pub nextDueMs: i64,
    pub viewerExpiresMs: i64,
    pub actionId: String,
    pub startedAt: String,
}

fn visible_until(ctx: &ReducerContext, ticket: &str, backend: &str, clock: i64) -> i64 {
    ctx.db
        .ticketremote_stream_viewer_focus()
        .ticketBackend()
        .filter((ticket, backend))
        .filter(|row| row.active)
        .map(|row| parse_time_ms(&row.expiresAt))
        .filter(|expiry| *expiry > clock)
        .max()
        .unwrap_or(0)
}

fn admission_blocked(ctx: &ReducerContext, ticket: &str, backend: &str) -> bool {
    require_new_phone_admission(ctx, ticket).is_err()
        || ctx
            .db
            .ticketremote_stream_desired_state()
            .id()
            .find(phone_row_id(ticket, backend))
            .is_some_and(|row| row.coldRestartPhase.as_deref() == Some("asleep"))
}

pub(super) fn cancelled_result_matches(
    action: &TicketremoteTicketActionV3,
    facts: &TicketActionV3TerminalFacts,
) -> bool {
    action.target == TARGET
        && action.status == "failed"
        && matches!(
            action.reason.as_str(),
            "idle_refresh_viewer_returned" | "command_expired"
        )
        && action.terminalFingerprint.is_none()
        && facts.target == TARGET
        // A delayed proved result may arrive after execution TTL cleanup. Drain
        // that transport receipt while retaining the database's expired outcome.
        && (facts.status != "succeeded" || action.reason == "command_expired")
        && facts.interaction_revision == action.actionId
        && facts.attempt_id.is_empty()
        && facts.activation_revision.is_empty()
}

pub(super) fn purged_result_matches(
    action_id: &str,
    facts: &TicketActionV3TerminalFacts,
    clock: &str,
) -> bool {
    let Some((schedule_id, due)) = action_id
        .strip_prefix("idle-refresh-")
        .and_then(|suffix| suffix.split_once('-'))
    else {
        return false;
    };
    let valid_schedule_id = schedule_id.parse::<u64>().is_ok_and(|id| id > 0);
    let expired_due = due
        .parse::<i64>()
        .is_ok_and(|due| due > 0 && parse_time_ms(clock).saturating_sub(due) >= HISTORY_TTL_MS);
    valid_schedule_id
        && expired_due
        && facts.target == TARGET
        && facts.interaction_revision == action_id
        && facts.attempt_id.is_empty()
        && facts.activation_revision.is_empty()
}

// Advance directly past missed slots, preserving the original 40/70/100 cadence.
fn next_due(due: i64, clock: i64) -> i64 {
    due.saturating_add((clock.saturating_sub(due) / REPEAT_MS + 1).saturating_mul(REPEAT_MS))
}

fn idle_start(previous_expiry: i64, clock: i64) -> i64 {
    if previous_expiry > 0 && previous_expiry <= clock {
        previous_expiry
    } else {
        clock
    }
}

fn replace(ctx: &ReducerContext, mut row: TicketremoteIdleTicketRefresh) {
    let table = ctx.db.ticketremote_idle_ticket_refresh();
    table.scheduled_id().delete(row.scheduled_id);
    row.scheduled_id = 0;
    let wake = if row.viewerExpiresMs > 0 {
        row.viewerExpiresMs
    } else {
        row.nextDueMs
    };
    row.scheduled_at = ScheduleAt::Time(Timestamp::from_micros_since_unix_epoch(
        wake.saturating_mul(1_000),
    ));
    table.insert(row);
}

pub(super) fn reconcile_ticket(ctx: &ReducerContext, ticket: &str, now: &str) {
    for phone in ctx
        .db
        .ticketremote_phone_backend()
        .ticketId()
        .filter(ticket)
    {
        reconcile(ctx, ticket, &phone.backendId, now);
    }
}

pub(super) fn reconcile(ctx: &ReducerContext, ticket: &str, backend: &str, now: &str) {
    let clock = parse_time_ms(now);
    let phone_id = phone_row_id(ticket, backend);
    // Focus from an obsolete backend must not create another phone scheduler.
    if ctx
        .db
        .ticketremote_phone_backend()
        .id()
        .find(&phone_id)
        .is_none()
    {
        ctx.db
            .ticketremote_idle_ticket_refresh()
            .phoneId()
            .delete(&phone_id);
        return;
    }
    let viewers = visible_until(ctx, ticket, backend, clock);
    let previous = ctx
        .db
        .ticketremote_idle_ticket_refresh()
        .phoneId()
        .find(&phone_id);
    let Some(mut row) = previous else {
        replace(
            ctx,
            TicketremoteIdleTicketRefresh {
                scheduled_id: 0,
                scheduled_at: ScheduleAt::Time(ctx.timestamp),
                phoneId: phone_id,
                ticketId: ticket.into(),
                backendId: backend.into(),
                idleSinceMs: if viewers > 0 { 0 } else { clock },
                nextDueMs: if viewers > 0 {
                    0
                } else {
                    clock.saturating_add(FIRST_IDLE_MS)
                },
                viewerExpiresMs: viewers,
                actionId: String::new(),
                startedAt: String::new(),
            },
        );
        return;
    };
    if viewers > 0 {
        if !row.actionId.is_empty() && row.startedAt.is_empty() {
            let command_id = ticket_action_v3_command_id(ticket, backend, &row.actionId);
            update_stream_command_status(
                ctx,
                &command_id,
                "failed",
                "idle_refresh_viewer_returned",
                now,
            );
            row.actionId.clear();
        }
        if row.viewerExpiresMs == viewers && row.idleSinceMs == 0 {
            return;
        }
        row.idleSinceMs = 0;
        row.nextDueMs = 0;
        row.viewerExpiresMs = viewers;
        replace(ctx, row);
        return;
    }
    if row.idleSinceMs == 0 {
        row.idleSinceMs = idle_start(row.viewerExpiresMs, clock);
        row.nextDueMs = row.idleSinceMs.saturating_add(FIRST_IDLE_MS);
        row.viewerExpiresMs = 0;
        replace(ctx, row);
        return;
    }
    if row.nextDueMs > clock {
        return;
    }
    let due = row.nextDueMs;
    let action_id = format!("idle-refresh-{}-{}", row.scheduled_id, due);
    let blocked = if clock.saturating_sub(due) >= DELIVERY_MS {
        Some("idle_refresh_missed_cycle")
    } else if admission_blocked(ctx, ticket, backend) {
        Some("idle_refresh_blocked")
    } else if ticket_phone_mutation_lane_conflict(ctx, ticket, backend, now).is_some()
        || ctx
            .db
            .ticketremote_ticket_action_v3_queued_intent()
            .id()
            .find(&phone_id)
            .is_some()
    {
        Some("idle_refresh_phone_busy")
    } else {
        None
    };
    let action = ticket_action_v3_upsert_pending(
        ctx,
        ticket,
        backend,
        &action_id,
        TARGET,
        "idle_refresh_requested",
        now,
    );
    if let Some(reason) = blocked {
        ticket_action_v3_finish_without_command(ctx, action, reason, now);
    } else {
        let payload = serde_json::json!({
            "version": 3, "actionId": action_id, "target": TARGET,
            "source": "ticket_remote_idle_refresh", "flow": "idle_ticket_refresh",
            "reason": "idle_refresh_requested", "attemptId": "",
            "expectedInteractionRevision": "", "scheduleId": "",
            "switchExpiresAt": "", "policyRevision": "",
        })
        .to_string();
        insert_stream_command(
            ctx,
            ticket,
            backend,
            &ticket_action_v3_command_id(ticket, backend, &action_id),
            "ticket_action_v3",
            &action_id,
            "idle_refresh_requested",
            &payload,
            DELIVERY_MS,
            now,
        );
        row.actionId = action_id;
        row.startedAt.clear();
    }
    row.nextDueMs = next_due(due, clock);
    replace(ctx, row);
}

#[spacetimedb::reducer]
pub fn ticketremote_scheduled_idle_ticket_refresh(
    ctx: &ReducerContext,
    arg: TicketremoteIdleTicketRefresh,
) -> Result<(), String> {
    if !ctx.sender_auth().is_internal() {
        return Err("internal role required".into());
    }
    if ctx
        .db
        .ticketremote_idle_ticket_refresh()
        .phoneId()
        .find(&arg.phoneId)
        .is_some_and(|row| row.scheduled_id == arg.scheduled_id)
    {
        reconcile(ctx, &arg.ticketId, &arg.backendId, &now(ctx));
    }
    Ok(())
}

/// One successful claim is the start of the bounded physical attempt. A lost
/// response is not permission to tap or to claim again. Viewer return after this
/// transaction may permit restoration, but never a new refresh attempt.
#[spacetimedb::reducer]
pub fn ticketremote_begin_idle_ticket_refresh(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    actionId: String,
    sessionId: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let ticket = clean_ticket_id(&ticketId);
    let backend = clean_backend_id(&backendId);
    let clock = now(ctx);
    if admission_blocked(ctx, &ticket, &backend) {
        return Err("idle_refresh_blocked".into());
    }
    if visible_until(ctx, &ticket, &backend, parse_time_ms(&clock)) > 0 {
        return Err("idle_refresh_viewer_returned".into());
    }
    let phone_id = phone_row_id(&ticket, &backend);
    let table = ctx.db.ticketremote_idle_ticket_refresh();
    let mut schedule = table
        .phoneId()
        .find(&phone_id)
        .ok_or("idle_refresh_not_scheduled")?;
    if schedule.actionId != actionId || actionId.is_empty() {
        return Err("idle_refresh_superseded".into());
    }
    if !schedule.startedAt.is_empty() {
        return Err("idle_refresh_already_started".into());
    }
    if !ctx
        .db
        .ticketremote_phone_control_state()
        .id()
        .find(&phone_id)
        .is_some_and(|phone| phone.sessionId == sessionId && !sessionId.is_empty())
    {
        return Err("phone_control_session_changed".into());
    }
    let action = ctx
        .db
        .ticketremote_ticket_action_v3()
        .id()
        .find(ticket_action_v3_row_id(&ticket, &backend, &actionId))
        .ok_or("ticket_action_not_found")?;
    if action.target != TARGET || !matches!(action.status.as_str(), "pending" | "running") {
        return Err("idle_refresh_not_pending".into());
    }
    let command = ctx
        .db
        .ticketremote_stream_command()
        .id()
        .find(ticket_action_v3_command_id(&ticket, &backend, &actionId))
        .ok_or("idle_refresh_command_missing")?;
    if !matches!(command.status.as_str(), "pending" | "dispatched")
        || parse_time_ms(&command.expiresAt) <= parse_time_ms(&clock)
    {
        return Err("idle_refresh_command_expired".into());
    }
    let another_action = ticket_action_v3_phone_lane_statuses()
        .into_iter()
        .any(|status| {
            ctx.db
                .ticketremote_ticket_action_v3()
                .ticketBackendStatus()
                .filter((&ticket, &backend, status))
                .any(|other| other.actionId != actionId)
        });
    if another_action
        || ticket_has_control_code_request_in_progress(ctx, &ticket, &clock)
        || ticket_has_vivi_reauth_in_progress(ctx, &ticket, &backend)
        || ctx
            .db
            .ticketremote_ticket_action_v3_queued_intent()
            .id()
            .find(&phone_id)
            .is_some()
    {
        return Err("idle_refresh_phone_busy".into());
    }
    schedule.startedAt = clock;
    table.scheduled_id().update(schedule);
    // The delivery deadline fences the first tap. Once admitted, keep the same
    // bounded execution/terminal-delivery allowance as other phone actions.
    ctx.db
        .ticketremote_stream_command()
        .id()
        .update(TicketremoteStreamCommand {
            expiresAt: add_ms(&now(ctx), TICKET_ACTIVATION_COMMAND_TTL_MS),
            ..command
        });
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn first_at_forty_then_seventy_and_one_hundred_minutes() {
        let start = 1_000;
        let mut due = start + FIRST_IDLE_MS;
        assert_eq!(due, start + 40 * 60_000);
        due = next_due(due, due);
        assert_eq!(due, start + 70 * 60_000);
        assert_eq!(next_due(due, due), start + 100 * 60_000);
    }

    #[test]
    fn missed_slots_do_not_accumulate_or_move_the_cadence() {
        let due = 40 * 60_000;
        assert_eq!(next_due(due, 99 * 60_000), 100 * 60_000);
        assert_eq!(next_due(due, 100 * 60_000), 130 * 60_000);
    }

    #[test]
    fn abrupt_expiry_and_explicit_departure_have_distinct_idle_starts() {
        assert_eq!(idle_start(90_000, 120_000), 90_000);
        assert_eq!(idle_start(90_000, 60_000), 60_000);
        assert_eq!(idle_start(0, 120_000), 120_000);
        assert_eq!(idle_start(180_000, 120_000), 120_000);
    }
}
