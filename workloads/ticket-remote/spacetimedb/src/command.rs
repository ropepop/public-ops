//! One member command envelope. Existing operation handlers retain their safety rules
//! and share the same single waiting slot; receipt identity is independent of media.
use super::*;

const COMMAND_MAX_AGE_MS: i64 = 30_000;

/// Once a backend has established current control state, stale browser producers
/// cannot re-enter the retired media-proof admission paths. Retained completion
/// reducers and rows are unaffected; rollback must keep the common producer.
pub(super) fn require_legacy_phone_admission(ctx: &ReducerContext, ticket: &str, backend: &str) -> Result<(), String> {
    if ctx.db.ticketremote_phone_control_state().id().find(phone_row_id(ticket, backend)).is_some() {
        return Err("ticket_client_reload_required".into());
    }
    Ok(())
}

#[spacetimedb::table(accessor = ticketremote_command_receipt,
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))
)]
#[derive(Clone)]
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

fn command_payload(operation: &str, payload: &str) -> Result<serde_json::Value, String> {
    if payload.len() > 2_048 { return Err("invalid_ticket_command_payload".into()); }
    let value: serde_json::Value = serde_json::from_str(payload)
        .map_err(|_| "invalid_ticket_command_payload")?;
    let fields = value.as_object().ok_or("invalid_ticket_command_payload")?;
    let allowed: &[&str] = match operation {
        "control_code" => &["sessionId", "digits"],
        "vivi_full_reset" | "vivi_logout_login" | "vivi_logout_login_redetect" => &["credentialRevision"],
        "open_latest_unactivated" | "open_latest_and_register" | "register_current" |
        "show_recent_activated" | "return_to_latest_unactivated" | "redetect_latest" => &["source", "reason"],
        _ => return Err("invalid_ticket_command_operation".into()),
    };
    if fields.iter().any(|(key, value)| !allowed.contains(&key.as_str()) || !value.is_string()) {
        return Err("invalid_ticket_command_payload".into());
    }
    Ok(value)
}

fn command_fingerprint(operation: &str, context: &str, issued_at: &str, payload: &serde_json::Value) -> String {
    let canonical = serde_json::json!([operation, context, issued_at, payload]).to_string();
    Sha256::digest(canonical.as_bytes()).iter().map(|byte| format!("{byte:02x}")).collect()
}

fn command_time_valid(issued_at: &str, clock: &str) -> bool {
    let issued = parse_time_ms(issued_at);
    let age = parse_time_ms(clock).saturating_sub(issued);
    issued > 0 && (-2_000..COMMAND_MAX_AGE_MS).contains(&age)
}

#[spacetimedb::reducer]
pub fn ticketremote_member_command(
    ctx: &ReducerContext, version: u32, ticketId: String, backendId: String,
    commandId: String, operation: String, contextRevision: String, issuedAt: String,
    payloadJson: String,
) -> Result<(), String> {
    if version != 1 { return Err("unsupported_ticket_command_version".into()); }
    let clock = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &clock);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    let backend = canonical_activation_backend(ctx, &ticket.id, &backendId)?;
    if !valid_schedule_identifier(&commandId) { return Err("invalid_ticket_command_id".into()); }
    if contextRevision.len() > 160 { return Err("invalid_phone_control_context".into()); }
    let payload = command_payload(&operation, &payloadJson)?;
    let fingerprint = command_fingerprint(&operation, &contextRevision, &issuedAt, &payload);
    let id = ticket_action_v3_row_id(&ticket.id, &backend, &commandId);
    if let Some(existing) = ctx.db.ticketremote_command_receipt().id().find(&id) {
        return if existing.requestedEmail == email && existing.fingerprint == fingerprint {
            Ok(())
        } else { Err("ticket_command_id_reused".into()) };
    }
    // Expired envelopes cannot execute again after receipt retention ends.
    if !command_time_valid(&issuedAt, &clock) { return Err("ticket_command_expired".into()); }
    let field = |key: &str| payload.get(key).and_then(|value| value.as_str()).unwrap_or("");
    match operation.as_str() {
        "control_code" => {
            if !contextRevision.starts_with("pc-") {
                return Err("phone_control_context_required".into());
            }
            let digits = field("digits");
            let session = field("sessionId");
            if !valid_control_code_digits(digits) { return Err("invalid_code".into()); }
            if !valid_schedule_identifier(session) { return Err("invalid_session_id".into()); }
            if ticket_has_control_code_request_in_progress(ctx, &ticket.id, &clock) {
                return Err("request_in_progress".into());
            }
            if ticket_phone_mutation_lane_conflict(ctx, &ticket.id, &backend, &clock).is_some() {
                queue_control_code_intent(ctx, &ticket.id, &backend, &commandId, session,
                    digits, &contextRevision, &email, &clock)?;
            } else {
                admit_control_code_request_impl(ctx, &ticket.id, &backend, session, digits,
                    &contextRevision, &email, Some(&commandId), &clock)?;
            }
        }
        "vivi_full_reset" | "vivi_logout_login" | "vivi_logout_login_redetect" => {
            let mode = match operation.as_str() {
                "vivi_full_reset" => ViviReauthMode::FullResetV2,
                "vivi_logout_login" => ViviReauthMode::LogoutLoginV3,
                _ => ViviReauthMode::LogoutLoginRedetectV4,
            };
            if !vivi_reauth_request_mode_matches(&commandId, mode) {
                return Err("vivi_reauth_request_mode_mismatch".into());
            }
            owner_request_vivi_reauth(ctx, ticket.id.clone(), backend.clone(), commandId.clone(),
                field("credentialRevision").into(), mode)?;
        }
        _ => {
            if operation == "register_current" && !contextRevision.starts_with("pc-") {
                return Err("phone_control_context_required".into());
            }
            request_ticket_action_v3_impl(ctx, 3, &ticket.id, &backend, &commandId, &operation,
                field("source"), field("reason"),
                if ticket_action_v3_is_activation(&operation) { &commandId } else { "" },
                &contextRevision, "", &email, &clock)?;
        }
    }
    ctx.db.ticketremote_command_receipt().insert(TicketremoteCommandReceipt {
        id, ticketId: ticket.id, backendId: backend, commandId, requestedEmail: email,
        fingerprint, createdAt: clock.clone(), expiresAt: add_ms(&clock, HISTORY_TTL_MS),
    });
    Ok(())
}

pub(super) fn purge_command_receipts(ctx: &ReducerContext, ticket: &str, clock: &str, limit: u32) -> u32 {
    let table = ctx.db.ticketremote_command_receipt();
    let rows: Vec<_> = table.ticketExpiresAt().filter((ticket, ..=clock)).take(limit as usize).collect();
    for row in &rows { table.id().delete(&row.id); }
    rows.len() as u32
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn only_operation_specific_string_fields_are_admitted() {
        assert!(command_payload("control_code", r#"{"digits":"1234","sessionId":"s"}"#).is_ok());
        assert!(command_payload("control_code", r#"{"password":"no"}"#).is_err());
        assert!(command_payload("register_current", r#"{"source":1}"#).is_err());
        assert!(command_payload("prove_current", "{}").is_err());
        assert!(command_payload("unknown", "{}").is_err());
    }

    #[test]
    fn immutable_request_fingerprint_ignores_json_field_order_but_binds_context_and_payload() {
        let first = command_payload("control_code", r#"{"digits":"1234","sessionId":"s"}"#).unwrap();
        let reordered = command_payload("control_code", r#"{"sessionId":"s","digits":"1234"}"#).unwrap();
        let changed = command_payload("control_code", r#"{"sessionId":"s","digits":"4321"}"#).unwrap();
        let fingerprint = command_fingerprint("control_code", "pc-a:1", "clock", &first);
        assert_eq!(fingerprint, command_fingerprint("control_code", "pc-a:1", "clock", &reordered));
        assert_ne!(fingerprint, command_fingerprint("control_code", "pc-a:2", "clock", &first));
        assert_ne!(fingerprint, command_fingerprint("control_code", "pc-a:1", "clock", &changed));
    }

    #[test]
    fn expired_envelope_cannot_replay_after_receipt_cleanup() {
        let clock = "2026-09-06T12:00:30.000Z";
        assert!(command_time_valid("2026-09-06T12:00:00.001Z", clock));
        assert!(!command_time_valid("2026-09-06T12:00:00.000Z", clock));
        assert!(!command_time_valid("2026-09-06T12:00:32.001Z", clock));
        assert!(!command_time_valid("invalid", clock));
    }
}
