#!/usr/bin/env python3
"""Real idle-refresh reducers and timers, in a disposable loopback database only.

The fixture replaces authentication and appends its own seed/assert reducers in
a temporary module copy. Production time constants and timer callbacks stay
unchanged. It never connects to a phone or publishes to a configured server.
"""
import os
from pathlib import Path
import runpy
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.request

SOURCE = Path(__file__).resolve().parents[1]
HELPERS = runpy.run_path(str(SOURCE.parent / "scripts/test-action-statistics.py"))
run = HELPERS["run"]
replace_function = HELPERS["replace_function"]

FIXTURE = r'''
use idle_refresh::{ticketremote_idle_ticket_refresh, TicketremoteIdleTicketRefresh};
use maintenance::ticketremote_owner_set_maintenance;

fn idle_fixture_seed(ctx: &ReducerContext, ticket: &str) {
    ticketremote_service_bootstrap(ctx, ticket.into(), "Idle fixture".into(),
        "fixture@example.test".into(), "pixel".into(), "http://127.0.0.1:9".into(),
        "fixture".into(), "https://example.test".into(), "fixture".into()).unwrap();
    let clock = now(ctx);
    upsert_row!(ctx, ticketremote_phone_control_state, TicketremotePhoneControlState {
        id: phone_row_id(ticket, "pixel"), ticketId: ticket.into(), backendId: "pixel".into(),
        sessionId: "pc-fixture".into(), sessionGeneration: 1, contextRevision: "pc-fixture:1".into(),
        observationSequence: 1, view: "unactivated_detail".into(), ready: true, busy: false,
        reason: "fixture".into(), leftBasisPoints: 100, topBasisPoints: 100,
        rightBasisPoints: 5000, bottomBasisPoints: 2000, observedAt: clock.clone(),
        expiresAt: add_ms(&clock, 3000), updatedAt: clock.clone(), clockAt: clock,
    });
}

fn idle_fixture_row(ctx: &ReducerContext, ticket: &str) -> TicketremoteIdleTicketRefresh {
    ctx.db.ticketremote_idle_ticket_refresh().phoneId().find(phone_row_id(ticket, "pixel")).unwrap()
}

fn idle_fixture_due(ctx: &ReducerContext, ticket: &str, due: i64, wake: i64) {
    let mut row = idle_fixture_row(ctx, ticket);
    row.nextDueMs = due;
    row.idleSinceMs = due - 40 * 60_000;
    row.scheduled_at = ScheduleAt::Time(Timestamp::from_micros_since_unix_epoch(wake * 1_000));
    ctx.db.ticketremote_idle_ticket_refresh().scheduled_id().update(row);
}

fn idle_fixture_actions(ctx: &ReducerContext, ticket: &str) -> Vec<TicketremoteTicketActionV3> {
    ctx.db.ticketremote_ticket_action_v3().ticketId().filter(ticket).collect()
}

fn idle_fixture_claim(ctx: &ReducerContext, ticket: &str, action: &str, session: &str) -> Result<(), String> {
    idle_refresh::ticketremote_begin_idle_ticket_refresh(ctx, ticket.into(), "pixel".into(), action.into(), session.into())
}

fn idle_fixture_focus(ctx: &ReducerContext, ticket: &str, session: &str, active: bool, clock: &str) {
    upsert_stream_viewer_focus(ctx, ticket, "pixel", session, "fixture@example.test", active, clock);
}

fn idle_fixture_finish(ctx: &ReducerContext, ticket: &str, action: &str, view: &str, clock: &str) {
    idle_fixture_result(ctx, ticket, action, "succeeded", view, "ticket_action_current_ticket_refreshed", clock).unwrap();
    assert_eq!(ctx.db.ticketremote_ticket_action_v3().id().find(ticket_action_v3_row_id(ticket, "pixel", action)).unwrap().reason,
               "ticket_action_current_ticket_refreshed");
}

fn idle_fixture_result(ctx: &ReducerContext, ticket: &str, action: &str, status: &str, view: &str, reason: &str, clock: &str) -> Result<(), String> {
    ticketremote_finalize_ticket_action_v3(ctx, ticket.into(), "pixel".into(),
        ticket_action_v3_command_id(ticket, "pixel", action), action.into(),
        "refresh_current_ticket".into(), status.into(), if status == "succeeded" { "complete" } else { "failed" }.into(), view.into(),
        "1".into(), "1".into(), reason.into(), clock.into(),
        "".into(), action.into(), "".into(), false, 0, 0, 0, 0, clock.into())
}

fn idle_fixture_delayed_result(ctx: &ReducerContext, ticket: &str, action: &str, status: &str,
    completed: &str, received: &str, proof: [&str; 3]) -> Result<(), String> {
    ticketremote_finalize_ticket_action_v3(ctx, ticket.into(), "pixel".into(),
        ticket_action_v3_command_id(ticket, "pixel", action), action.into(), "refresh_current_ticket".into(),
        status.into(), if status == "succeeded" { "complete" } else { "failed" }.into(),
        if status == "succeeded" { "activated_current" } else { "unknown" }.into(), "1".into(), "1".into(),
        if status == "succeeded" { "ticket_action_current_ticket_refreshed" } else { "command_expired" }.into(),
        completed.into(), proof[1].into(), proof[0].into(), proof[2].into(), false, 0, 0, 0, 0, received.into())
}

fn idle_fixture_switch(ctx: &ReducerContext, ticket: &str, clock: &str) -> TicketremoteTicketSwitchAnchor {
    ctx.db.ticketremote_ticket_switch_anchor().insert(TicketremoteTicketSwitchAnchor {
        id: phone_row_id(ticket, "pixel"), ticketId: ticket.into(), backendId: "pixel".into(),
        activationAttemptId: "original".into(), activationRevision: "original-revision".into(),
        activationAt: add_ms(clock, -1000), expiresAt: add_ms(clock, 10 * 60_000),
        latestUnactivatedProofActionId: "original-proof".into(), latestUnactivatedProofAt: clock.into(),
        currentView: "latest_unactivated".into(), policyRevision: "fixture".into(), updatedAt: clock.into(),
    })
}

fn idle_fixture_current_snapshot(ctx: &ReducerContext, ticket: &str) -> serde_json::Value {
    let id = phone_row_id(ticket, "pixel");
    serde_json::json!([
        ctx.db.ticketremote_phone_control_state().id().find(&id)
            .map(|row| (row.contextRevision, row.view, row.updatedAt, row.expiresAt)),
        ctx.db.ticketremote_stream_desired_state().id().find(&id)
            .map(|row| (row.desiredActive, row.viewerCount, row.reason, row.revision, row.updatedAt)),
        ctx.db.ticketremote_phone_current_report().id().find(&id)
            .map(|row| (row.streamState, row.statusJson, row.lastCommandId, row.lastCommandRevision, row.updatedAt)),
        ctx.db.ticketremote_ticket_switch_anchor().id().find(&id)
            .map(|row| (row.currentView, row.latestUnactivatedProofActionId, row.policyRevision, row.updatedAt)),
        ctx.db.ticketremote_activation_history().count(),
    ])
}

fn idle_fixture_conflict(ctx: &ReducerContext, ticket: &str, code: bool, active: bool, clock: &str) {
    let request = format!("{ticket}-other");
    if code {
        if active {
            let mut row = insert_control_code_public_request(ctx, ticket, &request, "fixture", clock);
            row.status = "running".into();
            ctx.db.ticketremote_control_code_request().id().update(row);
        } else { delete_control_code_request(ctx, &request); }
    } else if active {
        insert_vivi_reauth_attempt(ctx, ticket, "pixel", &request, "fixture", "fixture@example.test",
            "running", "verifying_signed_in", "running", clock).unwrap();
    } else { ctx.db.ticketremote_vivi_reauth_attempt().id().delete(vivi_reauth_attempt_id(ticket, "pixel", &request)); }
}

#[spacetimedb::reducer]
pub fn fixture_idle_case(ctx: &ReducerContext, case: String) -> Result<(), String> {
    let ticket = format!("idle-{case}");
    let clock = now(ctx);
    let ms = parse_time_ms(&clock);
    if !case.ends_with("-assert") { idle_fixture_seed(ctx, &ticket); }
    match case.as_str() {
        "cadence" => {
            let row = idle_fixture_row(ctx, &ticket);
            assert_eq!(row.idleSinceMs, ms);
            assert_eq!(row.nextDueMs, ms + 40 * 60_000);
            idle_refresh::reconcile(ctx, &ticket, "pixel", &add_ms(&clock, 40 * 60_000 - 1));
            assert!(idle_fixture_actions(ctx, &ticket).is_empty());
            for minute in [40, 70, 100] {
                let at = add_ms(&clock, minute * 60_000);
                idle_refresh::reconcile(ctx, &ticket, "pixel", &at);
                let row = idle_fixture_row(ctx, &ticket);
                assert_eq!(row.nextDueMs, ms + (minute + 30) * 60_000);
                assert!(!row.actionId.is_empty());
                // Release the real durable action lane before the next cycle.
                idle_fixture_finish(ctx, &ticket, &row.actionId, "latest_unactivated", &at);
            }
            assert_eq!(idle_fixture_actions(ctx, &ticket).len(), 3);
        }
        "presence" => {
            let initial = idle_fixture_row(ctx, &ticket);
            let mut desired = ctx.db.ticketremote_stream_desired_state().id().find(phone_row_id(&ticket, "pixel")).unwrap();
            desired.desiredActive = true;
            desired.viewerCount = 99; // Background warmth / relay counts are not focus.
            ctx.db.ticketremote_stream_desired_state().id().update(desired);
            idle_refresh::reconcile(ctx, &ticket, "pixel", &clock);
            assert_eq!(idle_fixture_row(ctx, &ticket).nextDueMs, initial.nextDueMs);
            idle_fixture_focus(ctx, &ticket, "passive", true, &clock);
            assert_eq!(idle_fixture_row(ctx, &ticket).nextDueMs, 0);
            idle_fixture_focus(ctx, &ticket, "second", true, &add_ms(&clock, 1000));
            idle_fixture_focus(ctx, &ticket, "passive", false, &add_ms(&clock, 2000));
            assert_eq!(idle_fixture_row(ctx, &ticket).idleSinceMs, 0);
            idle_fixture_focus(ctx, &ticket, "second", false, &add_ms(&clock, 3000));
            assert_eq!(idle_fixture_row(ctx, &ticket).nextDueMs, ms + 3000 + 40 * 60_000);
            // An expired connection starts idleness at its expiry, not cleanup time.
            idle_fixture_focus(ctx, &ticket, "abrupt", true, &add_ms(&clock, 4000));
            let expiry = idle_fixture_row(ctx, &ticket).viewerExpiresMs;
            idle_refresh::reconcile(ctx, &ticket, "pixel", &add_ms(&clock, expiry - ms + 6000));
            assert_eq!(idle_fixture_row(ctx, &ticket).idleSinceMs, expiry);
            assert_eq!(idle_fixture_row(ctx, &ticket).nextDueMs, expiry + 40 * 60_000);
        }
        "cancel" | "cancel-purged" => {
            idle_fixture_due(ctx, &ticket, ms, ms + 60_000);
            idle_refresh::reconcile(ctx, &ticket, "pixel", &clock);
            let action = idle_fixture_row(ctx, &ticket).actionId;
            let command = ticket_action_v3_command_id(&ticket, "pixel", &action);
            idle_fixture_focus(ctx, &ticket, "returned", true, &clock);
            assert!(idle_fixture_row(ctx, &ticket).actionId.is_empty());
            assert_eq!(ctx.db.ticketremote_stream_command().id().find(&command).unwrap().status, "failed");
            // A late delivery acknowledgement must not revive canceled work.
            update_stream_command_status(ctx, &command, "dispatched", "late_ack", &clock);
            assert_eq!(ctx.db.ticketremote_stream_command().id().find(&command).unwrap().status, "failed");
            assert_eq!(idle_fixture_actions(ctx, &ticket)[0].status, "failed");
            assert!(idle_fixture_claim(ctx, &ticket, &action, "pc-fixture").is_err());
            if case == "cancel-purged" { ctx.db.ticketremote_stream_command().id().delete(&command); }
            assert!(idle_fixture_result(ctx, &ticket, &action, "succeeded", "activated_current", "ticket_action_current_ticket_refreshed", &clock).is_err());
            let settled_at = if case == "cancel-purged" { add_ms(&clock, HISTORY_TTL_MS - 1) } else { clock.clone() };
            for _ in 0..2 {
                idle_fixture_result(ctx, &ticket, &action, "failed", "unknown", "ticket_action_idle_refresh_cancelled", &settled_at).unwrap();
            }
            assert_eq!(idle_fixture_actions(ctx, &ticket)[0].expiresAt, add_ms(&settled_at, HISTORY_TTL_MS));
            assert!(ctx.db.ticketremote_stream_command().id().find(&command).is_none());
        }
        "expired" => {
            idle_fixture_due(ctx, &ticket, ms, ms + 60_000);
            idle_refresh::reconcile(ctx, &ticket, "pixel", &clock);
            let action = idle_fixture_row(ctx, &ticket).actionId;
            let command_id = ticket_action_v3_command_id(&ticket, "pixel", &action);
            let mut command = ctx.db.ticketremote_stream_command().id().find(&command_id).unwrap();
            command.expiresAt = clock.clone();
            ctx.db.ticketremote_stream_command().id().update(command);
            assert_eq!(idle_fixture_claim(ctx, &ticket, &action, "pc-fixture").unwrap_err(), "idle_refresh_command_expired");
            update_stream_command_status(ctx, &command_id, "expired", "command_expired", &clock);
            ctx.db.ticketremote_stream_command().id().delete(&command_id);
            for _ in 0..2 {
                idle_fixture_result(ctx, &ticket, &action, "failed", "unknown", "command_expired", &clock).unwrap();
            }
        }
        "late-success" | "history-purged" => {
            idle_fixture_due(ctx, &ticket, ms, ms + 60_000);
            idle_refresh::reconcile(ctx, &ticket, "pixel", &clock);
            let action = idle_fixture_row(ctx, &ticket).actionId;
            let action_row_id = ticket_action_v3_row_id(&ticket, "pixel", &action);
            let command_id = ticket_action_v3_command_id(&ticket, "pixel", &action);
            idle_fixture_claim(ctx, &ticket, &action, "pc-fixture").unwrap();
            // The phone completed while online but its journal's result reaches
            // the service only after actual command cleanup has failed the row.
            let completed = add_ms(&clock, 1000);
            let expired = add_ms(&clock, 10 * 60_000 + 1);
            assert_eq!(purge_expired_stream_commands_for_ticket(ctx, &ticket, &expired, 100), 1);
            assert!(ctx.db.ticketremote_stream_command().id().find(&command_id).is_none());
            let failed = ctx.db.ticketremote_ticket_action_v3().id().find(&action_row_id).unwrap();
            assert_eq!((&*failed.status, &*failed.reason), ("failed", "command_expired"));
            if case == "late-success" {
                idle_fixture_switch(ctx, &ticket, &expired);
                let before = idle_fixture_current_snapshot(ctx, &ticket);
                for _ in 0..2 {
                    idle_fixture_delayed_result(ctx, &ticket, &action, "succeeded", &completed, &expired, [&action, "", ""]).unwrap();
                }
                let after = ctx.db.ticketremote_ticket_action_v3().id().find(&action_row_id).unwrap();
                assert_eq!((after.status, after.reason, after.currentView, after.completedAt),
                           (failed.status, failed.reason, failed.currentView, failed.completedAt));
                assert!(idle_fixture_delayed_result(ctx, &ticket, &action, "failed", &completed, &expired, [&action, "", ""]).is_err());
                assert_eq!(idle_fixture_current_snapshot(ctx, &ticket), before);
            } else {
                // This uses the real history cleanup, including the extended
                // expiry written when command cleanup first failed the action.
                let forgotten = add_ms(&failed.expiresAt, 1);
                cleanup_expired(ctx, &ticket, &forgotten, CLEANUP_BATCH_SIZE);
                assert!(ctx.db.ticketremote_ticket_action_v3().id().find(&action_row_id).is_none());
                assert!(ctx.db.ticketremote_stream_command().id().find(&command_id).is_none());
                idle_refresh::reconcile(ctx, &ticket, "pixel", &forgotten);
                let next_due = idle_fixture_row(ctx, &ticket).nextDueMs;
                let received = add_ms(&clock, next_due - ms);
                idle_refresh::reconcile(ctx, &ticket, "pixel", &received);
                let next_action = idle_fixture_row(ctx, &ticket).actionId;
                assert_ne!(next_action, action);
                idle_fixture_switch(ctx, &ticket, &received);
                let before = idle_fixture_current_snapshot(ctx, &ticket);
                let action_count = idle_fixture_actions(ctx, &ticket).len();
                for status in ["failed", "succeeded", "failed", "succeeded"] {
                    idle_fixture_delayed_result(ctx, &ticket, &action, status, &completed, &received, [&action, "", ""]).unwrap();
                }
                for proof in [["wrong-correlation", "", ""], [&action, "unexpected-attempt", ""], [&action, "", "unexpected-activation"]] {
                    assert!(idle_fixture_delayed_result(ctx, &ticket, &action, "failed", &completed, &received, proof).is_err());
                }
                for invalid in ["idle-refresh-not-a-number".to_string(), format!("idle-refresh-0-{ms}"),
                    format!("idle-refresh-999-{}", next_due + 1000), format!("idle-refresh-999-{}", next_due - 1000)] {
                    assert!(idle_fixture_delayed_result(ctx, &ticket, &invalid, "failed", &completed, &received, [&invalid, "", ""]).is_err());
                }
                // An absent action with a retained command is not a forgotten
                // result and must not bypass the ordinary correlation checks.
                insert_stream_command(ctx, &ticket, "pixel", &command_id, "ticket_action_v3", &action,
                    "fixture", "{}", 60_000, &received);
                assert!(idle_fixture_delayed_result(ctx, &ticket, &action, "failed", &completed, &received, [&action, "", ""]).is_err());
                ctx.db.ticketremote_stream_command().id().delete(&command_id);
                assert!(ctx.db.ticketremote_ticket_action_v3().id().find(&action_row_id).is_none());
                assert_eq!(idle_fixture_actions(ctx, &ticket).len(), action_count);
                assert_eq!(idle_fixture_row(ctx, &ticket).actionId, next_action);
                assert_eq!(idle_fixture_current_snapshot(ctx, &ticket), before);
                // The newer cycle retains its command and can settle normally.
                idle_fixture_finish(ctx, &ticket, &next_action, "latest_unactivated", &received);
                assert!(idle_fixture_row(ctx, &ticket).nextDueMs > next_due);
            }
        }
        "claim" => {
            assert_eq!(request_ticket_action_v3_impl(ctx, 3, &ticket, "pixel", "public-refresh",
                "refresh_current_ticket", "browser_button", "user_request", "", "pc-fixture:1", "",
                "fixture@example.test", &clock).unwrap_err(), "invalid_ticket_action_target");
            idle_fixture_due(ctx, &ticket, ms, ms + 60_000);
            idle_refresh::reconcile(ctx, &ticket, "pixel", &clock);
            let action = idle_fixture_row(ctx, &ticket).actionId;
            assert_eq!(idle_fixture_claim(ctx, &ticket, &action, "old-session").unwrap_err(), "phone_control_session_changed");
            ticketremote_owner_set_maintenance(ctx, ticket.clone(), true).unwrap();
            assert_eq!(idle_fixture_claim(ctx, &ticket, &action, "pc-fixture").unwrap_err(), "idle_refresh_blocked");
            ticketremote_owner_set_maintenance(ctx, ticket.clone(), false).unwrap();
            let mut desired = ctx.db.ticketremote_stream_desired_state().id().find(phone_row_id(&ticket, "pixel")).unwrap();
            for phase in ["stopping", "asleep"] {
                desired.coldRestartPhase = Some(phase.into());
                ctx.db.ticketremote_stream_desired_state().id().update(desired.clone());
                assert_eq!(idle_fixture_claim(ctx, &ticket, &action, "pc-fixture").unwrap_err(), "idle_refresh_blocked");
            }
            desired.coldRestartPhase = None;
            ctx.db.ticketremote_stream_desired_state().id().update(desired);
            let other = ticket_action_v3_upsert_pending(ctx, &ticket, "pixel", "other", "open_latest", "fixture", &clock);
            assert_eq!(idle_fixture_claim(ctx, &ticket, &action, "pc-fixture").unwrap_err(), "idle_refresh_phone_busy");
            ctx.db.ticketremote_ticket_action_v3().id().delete(other.id);
            // Insert directly to prove the claim independently rechecks focus.
            ctx.db.ticketremote_stream_viewer_focus().insert(TicketremoteStreamViewerFocus {
                id: "claim-focus".into(), ticketId: ticket.clone(), backendId: "pixel".into(), publicId: "passive".into(),
                active: true, lastSeenAt: clock.clone(), expiresAt: add_ms(&clock, 30_000), email: None,
            });
            assert_eq!(idle_fixture_claim(ctx, &ticket, &action, "pc-fixture").unwrap_err(), "idle_refresh_viewer_returned");
            ctx.db.ticketremote_stream_viewer_focus().id().delete("claim-focus".to_string());
            idle_fixture_claim(ctx, &ticket, &action, "pc-fixture").unwrap();
            let command = ctx.db.ticketremote_stream_command().id().find(ticket_action_v3_command_id(&ticket, "pixel", &action)).unwrap();
            assert_eq!(parse_time_ms(&command.expiresAt), ms + 10 * 60_000);
            assert_eq!(idle_fixture_claim(ctx, &ticket, &action, "pc-fixture").unwrap_err(), "idle_refresh_already_started");
            let started = idle_fixture_row(ctx, &ticket).startedAt;
            idle_fixture_focus(ctx, &ticket, "returned", true, &clock);
            assert_eq!(idle_fixture_row(ctx, &ticket).startedAt, started);
            assert_eq!(idle_fixture_row(ctx, &ticket).actionId, action);
            assert_eq!(idle_fixture_actions(ctx, &ticket)[0].status, "pending");
            // A started action can settle after viewer return, but never activate.
            let anchor = idle_fixture_switch(ctx, &ticket, &clock);
            let history = ctx.db.ticketremote_activation_history().count();
            let switches = ctx.db.ticketremote_ticket_switch_anchor().count();
            idle_fixture_finish(ctx, &ticket, &action, "activated_current", &clock);
            idle_fixture_finish(ctx, &ticket, &action, "activated_current", &clock);
            assert_eq!(ctx.db.ticketremote_activation_history().count(), history);
            assert_eq!(ctx.db.ticketremote_ticket_switch_anchor().count(), switches);
            let after = ctx.db.ticketremote_ticket_switch_anchor().id().find(&anchor.id).unwrap();
            assert_eq!((after.currentView, after.latestUnactivatedProofActionId, after.updatedAt),
                       (anchor.currentView, anchor.latestUnactivatedProofActionId, anchor.updatedAt));
            assert_eq!(idle_fixture_actions(ctx, &ticket)[0].status, "succeeded");
            assert!(ctx.db.ticketremote_stream_command().id().find(ticket_action_v3_command_id(&ticket, "pixel", &action)).is_none());
        }
        "conflict-code" | "conflict-reauth" => {
            let code = case == "conflict-code";
            idle_fixture_conflict(ctx, &ticket, code, true, &clock);
            idle_fixture_due(ctx, &ticket, ms, ms + 60_000);
            idle_refresh::reconcile(ctx, &ticket, "pixel", &clock);
            assert!(idle_fixture_row(ctx, &ticket).actionId.is_empty());
            assert_eq!(idle_fixture_actions(ctx, &ticket)[0].reason, "idle_refresh_phone_busy");
            idle_fixture_conflict(ctx, &ticket, code, false, &clock);
            idle_fixture_due(ctx, &ticket, ms, ms + 60_000);
            idle_refresh::reconcile(ctx, &ticket, "pixel", &clock);
            let action = idle_fixture_row(ctx, &ticket).actionId;
            assert!(!action.is_empty());
            idle_fixture_conflict(ctx, &ticket, code, true, &clock);
            assert_eq!(idle_fixture_claim(ctx, &ticket, &action, "pc-fixture").unwrap_err(), "idle_refresh_phone_busy");
            assert!(idle_fixture_row(ctx, &ticket).startedAt.is_empty());
            idle_fixture_conflict(ctx, &ticket, code, false, &clock);
            idle_fixture_claim(ctx, &ticket, &action, "pc-fixture").unwrap();
        }
        "blocked" | "blocked-asleep" | "blocked-busy" | "blocked-queued" => {
            let busy = case == "blocked-busy" || case == "blocked-queued";
            if case == "blocked" {
                ticketremote_owner_set_maintenance(ctx, ticket.clone(), true).unwrap();
            } else if case == "blocked-asleep" {
                let mut row = ctx.db.ticketremote_stream_desired_state().id().find(phone_row_id(&ticket, "pixel")).unwrap();
                row.coldRestartPhase = Some("asleep".into());
                ctx.db.ticketremote_stream_desired_state().id().update(row);
            } else if case == "blocked-busy" {
                ticket_action_v3_upsert_pending(ctx, &ticket, "pixel", "other", "open_latest_unactivated", "fixture", &clock);
            } else {
                queue_phone_intent(ctx, &ticket, "pixel", "queued", "fixture@example.test", &clock,
                    QueuedPhoneIntent::Ticket { target: "open_latest_unactivated", source: "browser_button", reason: "user_request",
                        attempt: "", expected_revision: "pc-fixture:1", schedule: "" }).unwrap();
            }
            idle_fixture_due(ctx, &ticket, ms, ms + 60_000);
            idle_refresh::reconcile(ctx, &ticket, "pixel", &clock);
            let actions = idle_fixture_actions(ctx, &ticket);
            let refresh = actions.iter().find(|row| row.target == "refresh_current_ticket").unwrap();
            assert_eq!(refresh.reason, if busy { "idle_refresh_phone_busy" } else { "idle_refresh_blocked" });
            assert_eq!(refresh.status, "failed");
            assert!(idle_fixture_row(ctx, &ticket).actionId.is_empty());
            assert_eq!(idle_fixture_row(ctx, &ticket).nextDueMs, ms + 30 * 60_000);
            ticketremote_owner_set_maintenance(ctx, ticket.clone(), false).unwrap();
            idle_refresh::reconcile(ctx, &ticket, "pixel", &add_ms(&clock, 1));
            assert_eq!(idle_fixture_actions(ctx, &ticket).len(), actions.len());
        }
        "callback" => {
            assert_eq!(idle_refresh::ticketremote_scheduled_idle_ticket_refresh(ctx, idle_fixture_row(ctx, &ticket)).unwrap_err(), "internal role required");
            idle_fixture_due(ctx, &ticket, ms + 1500, ms + 1500);
        }
        "callback-assert" => {
            let row = idle_fixture_row(ctx, "idle-callback");
            if row.actionId.is_empty() { return Err("fixture_waiting".into()); }
            assert_eq!(idle_fixture_actions(ctx, "idle-callback").len(), 1);
            assert_eq!(row.nextDueMs, row.idleSinceMs + 70 * 60_000);
            assert!(row.nextDueMs > ms);
        }
        "restart" => {
            // Timer will fire only after the server has been stopped/restarted.
            // Its missed logical deadline must skip directly to a future slot.
            idle_fixture_due(ctx, &ticket, ms - 65 * 60_000, ms + 10_000);
        }
        "restart-assert" => {
            let actions = idle_fixture_actions(ctx, "idle-restart");
            let row = ctx.db.ticketremote_idle_ticket_refresh().phoneId().find(phone_row_id("idle-restart", "pixel"))
                .ok_or_else(|| format!("fixture_restart_timer_missing_backend_{}_actions_{}_all_timers_{}",
                    ctx.db.ticketremote_phone_backend().id().find(phone_row_id("idle-restart", "pixel")).is_some(),
                    actions.len(), ctx.db.ticketremote_idle_ticket_refresh().count()))?;
            if actions.is_empty() { return Err("fixture_waiting".into()); }
            assert_eq!(actions.len(), 1);
            assert_eq!(actions[0].reason, "idle_refresh_missed_cycle");
            assert_eq!(actions[0].status, "failed");
            assert!(row.actionId.is_empty());
            assert_eq!(row.nextDueMs, row.idleSinceMs + 130 * 60_000);
            assert!(row.nextDueMs > ms);
        }
        _ => return Err("fixture_case_unknown".into()),
    }
    Ok(())
}
'''


def main():
    with tempfile.TemporaryDirectory(prefix="ticket-idle-refresh-test-") as temporary:
        directory = Path(temporary)
        module = directory / "module"
        shutil.copytree(SOURCE / "src", module / "src")
        for filename in ("Cargo.toml", "Cargo.lock"):
            shutil.copy2(SOURCE / filename, module / filename)
        source = (module / "src/lib.rs").read_text()
        source = replace_function(source, "require_service", "\n    let _ = ctx; Ok(())\n")
        source = replace_function(source, "client_email_from_auth", '\n    let _ = (ctx, ticket_id); Ok("fixture@example.test".into())\n')
        source = replace_function(source, "identity_connected", "\n    let _ = ctx; Ok(())\n")
        (module / "src/lib.rs").write_text(source + FIXTURE)
        target = SOURCE / "target/idle-refresh-fixture"
        build = subprocess.run(["spacetime", "build", "--module-path", str(module)], cwd=directory,
                               env={**os.environ, "CARGO_TARGET_DIR": str(target)}, capture_output=True, text=True)
        if build.returncode:
            raise RuntimeError(build.stdout + build.stderr)
        wasm = directory / "idle-fixture.wasm"
        shutil.copy2(target / "wasm32-unknown-unknown/release/ticket_remote_spacetimedb.wasm", wasm)
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
        server_url = f"http://127.0.0.1:{port}"
        args = ["--server", server_url, "--no-config", "--yes"]
        server = None
        with (directory / "server.log").open("w") as log:
            def start():
                process = subprocess.Popen(["spacetime", "start", "--listen-addr", f"127.0.0.1:{port}",
                    "--data-dir", str(directory / "data"), "--non-interactive"], stdout=log, stderr=log)
                for _ in range(100):
                    try:
                        urllib.request.urlopen(server_url + "/v1/ping", timeout=1).close()
                        return process
                    except OSError:
                        if process.poll() is not None:
                            raise RuntimeError((directory / "server.log").read_text())
                        time.sleep(.1)
                process.terminate()
                process.wait(timeout=10)
                raise RuntimeError("Local fixture server did not become ready")

            def stop():
                if server is not None:
                    server.terminate()
                    try:
                        server.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        server.kill()
                        server.wait()

            def case(name, poll=False):
                command = ["spacetime", "call", *args, "idle-fixture", "fixture_idle_case", name]
                if poll:
                    deadline = time.monotonic() + 15
                    while True:
                        result = subprocess.run(command, cwd=directory, capture_output=True, text=True)
                        output = result.stdout + result.stderr
                        if not result.returncode:
                            break
                        if "fixture_waiting" not in output or time.monotonic() >= deadline:
                            raise RuntimeError(output)
                        time.sleep(.2)
                else:
                    run(command, cwd=directory)
                print(f"PASS {name}", flush=True)

            try:
                server = start()
                run(["spacetime", "publish", *args, "--bin-path", str(wasm), "--delete-data=never", "idle-fixture"], cwd=directory)
                for name in ("cadence", "presence", "cancel", "cancel-purged", "expired", "late-success", "history-purged", "claim",
                             "conflict-code", "conflict-reauth", "blocked", "blocked-asleep", "blocked-busy", "blocked-queued", "callback"):
                    case(name)
                case("callback-assert", poll=True)
                case("restart")
                # Reducer acknowledgement precedes disk confirmation. Observe
                # the actual saved seed before deliberately killing the server.
                deadline = time.monotonic() + 5
                while True:
                    saved = run(["spacetime", "sql", *args, "--confirmed", "true", "idle-fixture",
                                 "SELECT * FROM ticketremote_idle_ticket_refresh WHERE ticketId = 'idle-restart'"], cwd=directory)
                    if "idle-restart" in saved:
                        break
                    assert time.monotonic() < deadline, "Fixture seed was not confirmed before restart"
                    time.sleep(.1)
                stop()
                time.sleep(10)
                server = start()
                case("restart-assert", poll=True)
                case("callback-assert")
            except Exception:
                logs = subprocess.run(["spacetime", "logs", "--server", server_url, "--no-config",
                                       "--num-lines", "12", "idle-fixture"], cwd=directory, capture_output=True, text=True)
                print(logs.stdout, flush=True)
                raise
            finally:
                stop()


if __name__ == "__main__":
    main()
