//! Scoped invitation reads and committed reducer results. Raw tokens never enter
//! the sidecar; the database owns quotas, session fencing, and redemption.
use super::*;

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct CreateRequest {
    ticket_id: String,
    invitation_id: String,
    token_hash: String,
    label: String,
    actor_email: String,
    duration_minutes: u32,
    stream_minutes: u32,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct RevokeRequest {
    ticket_id: String,
    invitation_id: String,
    actor_email: String,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct LookupRequest {
    ticket_id: String,
    token_hash: String,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct StartRequest {
    ticket_id: String,
    token_hash: String,
    session_id: String,
    takeover: bool,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct TrialRequest {
    ticket_id: String,
    invitation_id: String,
    session_id: String,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct StreamRequest {
    ticket_id: String,
    invitation_id: String,
    session_id: String,
    sequence: u64,
    #[serde(default)]
    result_only: bool,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct RedeemRequest {
    ticket_id: String,
    token_hash: String,
    email: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub(super) struct MembershipSourceJson {
    source: String,
    invitation_id: String,
    invitation_label: String,
    invited_by: String,
    registered_at: String,
}

pub(super) fn member_sources(
    conn: &DbConnection,
    ticket: &str,
) -> BTreeMap<String, MembershipSourceJson> {
    conn.db
        .ticketremote_service_member_source()
        .iter()
        .filter(|row| row.ticket_id == ticket)
        .map(|row| {
            (
                row.email,
                MembershipSourceJson {
                    source: row.source,
                    invitation_id: row.invitation_id,
                    invitation_label: row.invitation_label,
                    invited_by: row.invited_by,
                    registered_at: row.registered_at,
                },
            )
        })
        .collect()
}

fn find(conn: &DbConnection, ticket: &str, id: &str) -> Result<TicketremoteInvitation, HttpError> {
    conn.db
        .ticketremote_service_invitation()
        .iter()
        .find(|row| row.ticket_id == ticket && row.id == id)
        .ok_or_else(|| HttpError::new(404, "invitation_unavailable"))
}

fn find_hash(
    conn: &DbConnection,
    ticket: &str,
    hash: &str,
) -> Result<TicketremoteInvitation, HttpError> {
    if hash.len() != 64 || !hash.bytes().all(|c| c.is_ascii_hexdigit()) {
        return Err(HttpError::new(404, "invitation_unavailable"));
    }
    conn.db
        .ticketremote_service_invitation()
        .iter()
        .find(|row| {
            row.ticket_id == ticket && constant_time_eq(row.token_hash.as_bytes(), hash.as_bytes())
        })
        .ok_or_else(|| HttpError::new(404, "invitation_unavailable"))
}

fn ensure_trial(row: &TicketremoteInvitation, session: &str) -> Result<(), HttpError> {
    if !row.revoked_at.is_empty() || !row.redeemed_at.is_empty() || row.started_at.is_empty() {
        return Err(HttpError::new(403, "invitation_unavailable"));
    }
    if session.is_empty() || !constant_time_eq(row.active_session_id.as_bytes(), session.as_bytes())
    {
        return Err(HttpError::new(409, "invitation_takeover_required"));
    }
    Ok(())
}

fn status(row: &TicketremoteInvitation, now_ms: i64) -> &'static str {
    if !row.revoked_at.is_empty() {
        return "revoked";
    }
    if !row.redeemed_at.is_empty() {
        return "registered";
    }
    let expired = chrono::DateTime::parse_from_rfc3339(&row.expires_at)
        .map(|time| time.timestamp_millis() <= now_ms)
        .unwrap_or(true);
    let prepaid_stream = row.lease_charged && row.lease_until_ms > now_ms;
    if expired
        || (row.stream_used_ms >= row.stream_allowance_ms && !prepaid_stream)
        || row
            .activations_used
            .saturating_add(row.activations_reserved)
            >= 5
        || row
            .control_codes_used
            .saturating_add(row.control_codes_reserved)
            >= 5
    {
        return "registration_only";
    }
    if row.started_at.is_empty() {
        "not_started"
    } else {
        "trial_active"
    }
}

fn row_json(row: TicketremoteInvitation) -> serde_json::Value {
    json!({
        "id": row.id, "ticketId": row.ticket_id, "label": row.label,
        "createdBy": row.created_by, "createdAt": row.created_at, "expiresAt": row.expires_at,
        "streamAllowanceMs": row.stream_allowance_ms, "streamUsedMs": row.stream_used_ms,
        "remainingStreamMs": row.stream_allowance_ms.saturating_sub(row.stream_used_ms),
        "activationsUsed": row.activations_used, "controlCodesUsed": row.control_codes_used,
        "activationsReserved": row.activations_reserved, "controlCodesReserved": row.control_codes_reserved,
        "resultDeliveryUntilMs": row.result_delivery_until_ms,
        "startedAt": row.started_at, "activeSessionId": row.active_session_id,
        "leaseSequence": row.lease_sequence, "leaseStartedAtMs": row.lease_started_at_ms,
        "leaseUntilMs": row.lease_until_ms, "leaseCharged": row.lease_charged, "revokedAt": row.revoked_at,
        "redeemedAt": row.redeemed_at, "redeemedEmail": row.redeemed_email,
        "status": status(&row, Utc::now().timestamp_millis()),
    })
}

pub(super) fn route(
    app: &Arc<AppState>,
    request: &HttpRequest,
) -> Result<serde_json::Value, HttpError> {
    authorize_sidecar_request(app, request)?;
    ensure_ready(app)?;
    let conn = app
        .connection()
        .ok_or_else(|| HttpError::new(503, "Spacetime sidecar is not connected"))?;
    if request.method == "GET" {
        let ticket = query_value(&request.query, "ticketId").unwrap_or_default();
        let actor = query_value(&request.query, "actorEmail").unwrap_or_default();
        ensure_ticket_scope(app, &ticket)?;
        if monitoring::admin_role(&conn, &ticket, &actor).is_none() {
            return Err(HttpError::new(403, "forbidden"));
        }
        let mut rows: Vec<_> = conn
            .db
            .ticketremote_service_invitation()
            .iter()
            .filter(|row| row.ticket_id == ticket)
            .collect();
        rows.sort_by(|a, b| {
            b.created_at
                .cmp(&a.created_at)
                .then_with(|| a.id.cmp(&b.id))
        });
        return Ok(json!({"invitations": rows.into_iter().map(row_json).collect::<Vec<_>>() }));
    }
    ensure_json_content_type(request)?;
    match request.path.as_str() {
        "/internal/v1/invitations/create" => {
            let input: CreateRequest = decode_json_request(request)?;
            ensure_ticket_scope(app, &input.ticket_id)?;
            invoke_reducer("create-invitation", |sender| {
                conn.reducers
                    .ticketremote_create_invitation_then(
                        input.ticket_id.clone(),
                        input.invitation_id.clone(),
                        input.token_hash,
                        input.label,
                        input.actor_email,
                        input.duration_minutes,
                        input.stream_minutes,
                        move |_, result| {
                            let _ = sender.send(reducer_completion(result));
                        },
                    )
                    .map_err(|err| err.to_string())
            })?;
            Ok(row_json(find(
                &conn,
                &input.ticket_id,
                &input.invitation_id,
            )?))
        }
        "/internal/v1/invitations/revoke" => {
            let input: RevokeRequest = decode_json_request(request)?;
            ensure_ticket_scope(app, &input.ticket_id)?;
            invoke_reducer("revoke-invitation", |sender| {
                conn.reducers
                    .ticketremote_revoke_invitation_then(
                        input.ticket_id.clone(),
                        input.invitation_id.clone(),
                        input.actor_email,
                        move |_, result| {
                            let _ = sender.send(reducer_completion(result));
                        },
                    )
                    .map_err(|err| err.to_string())
            })?;
            Ok(row_json(find(
                &conn,
                &input.ticket_id,
                &input.invitation_id,
            )?))
        }
        "/internal/v1/invitations/lookup" => {
            let input: LookupRequest = decode_json_request(request)?;
            ensure_ticket_scope(app, &input.ticket_id)?;
            Ok(row_json(find_hash(
                &conn,
                &input.ticket_id,
                &input.token_hash,
            )?))
        }
        "/internal/v1/invitations/start" => {
            let input: StartRequest = decode_json_request(request)?;
            ensure_ticket_scope(app, &input.ticket_id)?;
            invoke_reducer("start-invitation", |sender| {
                conn.reducers
                    .ticketremote_start_invitation_then(
                        input.ticket_id.clone(),
                        input.token_hash.clone(),
                        input.session_id,
                        input.takeover,
                        move |_, result| {
                            let _ = sender.send(reducer_completion(result));
                        },
                    )
                    .map_err(|err| err.to_string())
            })?;
            Ok(row_json(find_hash(
                &conn,
                &input.ticket_id,
                &input.token_hash,
            )?))
        }
        "/internal/v1/invitations/status" | "/internal/v1/guest-token" => {
            let input: TrialRequest = decode_json_request(request)?;
            ensure_ticket_scope(app, &input.ticket_id)?;
            let row = find(&conn, &input.ticket_id, &input.invitation_id)?;
            ensure_trial(&row, &input.session_id)?;
            if request.path == "/internal/v1/guest-token" {
                return app
                    .cfg
                    .guest_token(&input.invitation_id, &input.session_id)
                    .map_err(|_| HttpError::new(503, "guest token unavailable"));
            }
            Ok(row_json(row))
        }
        "/internal/v1/invitations/reserve-stream" => {
            let input: StreamRequest = decode_json_request(request)?;
            ensure_ticket_scope(app, &input.ticket_id)?;
            invoke_reducer("reserve-invitation-stream", |sender| {
                conn.reducers
                    .ticketremote_reserve_invitation_stream_then(
                        input.ticket_id.clone(),
                        input.invitation_id.clone(),
                        input.session_id,
                        input.sequence,
                        input.result_only,
                        move |_, result| {
                            let _ = sender.send(reducer_completion(result));
                        },
                    )
                    .map_err(|err| err.to_string())
            })?;
            Ok(row_json(find(
                &conn,
                &input.ticket_id,
                &input.invitation_id,
            )?))
        }
        "/internal/v1/invitations/release-stream" => {
            let input: StreamRequest = decode_json_request(request)?;
            ensure_ticket_scope(app, &input.ticket_id)?;
            invoke_reducer("release-invitation-stream", |sender| {
                conn.reducers
                    .ticketremote_release_invitation_stream_then(
                        input.ticket_id.clone(),
                        input.invitation_id.clone(),
                        input.session_id,
                        input.sequence,
                        move |_, result| {
                            let _ = sender.send(reducer_completion(result));
                        },
                    )
                    .map_err(|err| err.to_string())
            })?;
            Ok(row_json(find(
                &conn,
                &input.ticket_id,
                &input.invitation_id,
            )?))
        }
        "/internal/v1/invitations/redeem" => {
            let input: RedeemRequest = decode_json_request(request)?;
            ensure_ticket_scope(app, &input.ticket_id)?;
            invoke_reducer("redeem-invitation", |sender| {
                conn.reducers
                    .ticketremote_redeem_invitation_then(
                        input.ticket_id.clone(),
                        input.token_hash.clone(),
                        input.email,
                        move |_, result| {
                            let _ = sender.send(reducer_completion(result));
                        },
                    )
                    .map_err(|err| err.to_string())
            })?;
            Ok(row_json(find_hash(
                &conn,
                &input.ticket_id,
                &input.token_hash,
            )?))
        }
        _ => Err(HttpError::new(404, "not found")),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn row() -> TicketremoteInvitation {
        TicketremoteInvitation {
            id: "invitation-1".into(),
            ticket_id: DEFAULT_TICKET_ID.into(),
            token_hash: "a".repeat(64),
            label: "Friend".into(),
            created_by: "owner@example.test".into(),
            created_at: "2026-09-22T10:00:00Z".into(),
            expires_at: "2026-09-25T10:00:00Z".into(),
            stream_allowance_ms: 900_000,
            stream_used_ms: 0,
            activations_used: 0,
            control_codes_used: 0,
            activations_reserved: 0,
            control_codes_reserved: 0,
            result_delivery_until_ms: 0,
            started_at: String::new(),
            active_session_id: String::new(),
            lease_sequence: 0,
            lease_started_at_ms: 0,
            lease_until_ms: 0,
            lease_charged: false,
            revoked_at: String::new(),
            redeemed_at: String::new(),
            redeemed_email: String::new(),
        }
    }

    #[test]
    fn private_invitation_dto_keeps_token_fingerprint_out_and_counts_pending_allowances() {
        let mut invite = row();
        let before_expiry = chrono::DateTime::parse_from_rfc3339("2026-09-25T09:59:59Z")
            .unwrap()
            .timestamp_millis();
        assert_eq!(status(&invite, before_expiry), "not_started");
        invite.started_at = "2026-09-22T10:10:00Z".into();
        assert_eq!(status(&invite, before_expiry), "trial_active");
        assert_eq!(status(&invite, before_expiry + 1_000), "registration_only");
        invite.activations_used = 4;
        invite.activations_reserved = 1;
        assert_eq!(status(&invite, before_expiry), "registration_only");
        invite.redeemed_at = "2026-09-22T10:12:00Z".into();
        assert_eq!(status(&invite, before_expiry), "registered");
        invite.revoked_at = "2026-09-22T10:13:00Z".into();
        assert_eq!(status(&invite, before_expiry), "revoked");
        let value = row_json(invite);
        assert!(value.get("tokenHash").is_none());
        assert!(!value.to_string().contains(&"a".repeat(64)));
        assert_eq!(value["remainingStreamMs"], 900_000);
        assert_eq!(value["activationsReserved"], 1);
    }

    #[test]
    fn guest_session_guard_fences_takeover_and_redemption_without_hiding_exhaustion() {
        let mut invite = row();
        assert_eq!(ensure_trial(&invite, "session-1").unwrap_err().status, 403);
        invite.started_at = "2026-09-22T10:10:00Z".into();
        invite.active_session_id = "session-1".into();
        assert!(ensure_trial(&invite, "session-1").is_ok());
        assert_eq!(ensure_trial(&invite, "session-2").unwrap_err().status, 409);
        invite.stream_used_ms = invite.stream_allowance_ms;
        assert!(ensure_trial(&invite, "session-1").is_ok());
        invite.redeemed_at = "2026-09-22T10:12:00Z".into();
        assert_eq!(ensure_trial(&invite, "session-1").unwrap_err().status, 403);
    }
}
