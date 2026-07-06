#![allow(non_snake_case)]

use chrono::{DateTime, Utc};
use spacetimedb::{
    CaseConversionPolicy, Identity, ReducerContext, ScheduleAt, SpacetimeType, Table, Timestamp,
    ViewContext,
};

const DEFAULT_TICKET_ID: &str = "vivi-default";
const DEFAULT_TICKET_NAME: &str = "ViVi timed ticket";
#[spacetimedb::settings]
const CASE_CONVERSION_POLICY: CaseConversionPolicy = CaseConversionPolicy::None;
const HISTORY_TTL_MS: i64 = 6 * 60 * 60 * 1000;
const CLEANUP_BATCH_SIZE: u32 = 500;
const CLEANUP_INTERVAL_SECS: u64 = 30 * 60;
const PHONE_KEEPALIVE_MS: i64 = 60_000;
const CONTROL_CODE_RATE_LIMIT: usize = 2;
const CONTROL_CODE_RATE_WINDOW_MS: i64 = 60_000;
const CONTROL_CODE_REQUEST_TTL_MS: i64 = 5 * 60_000;
const CONTROL_CODE_RESULT_TTL_MS: i64 = 60_000;
const CONTROL_CODE_COMMAND_TTL_MS: i64 = 2 * 60_000;
const CONTROL_CODE_PHONE_TTL_MS: i64 = 105_000;
const SAFE_JSON_MAX_BYTES: usize = 4096;
const SAFE_LOG_DETAIL_MAX_BYTES: usize = 1024;

#[spacetimedb::table(accessor = ticketremote_ticket)]
#[derive(Clone)]
pub struct TicketremoteTicket {
    #[primary_key]
    pub id: String,
    pub displayName: String,
    pub createdAt: String,
    pub updatedAt: String,
}

#[spacetimedb::table(accessor = ticketremote_ticket_member,
    index(accessor = ticketEmail, btree(columns = [ticketId, email]))
)]
#[derive(Clone)]
pub struct TicketremoteTicketMember {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub email: String,
    #[index(btree)]
    pub role: String,
    pub active: bool,
    pub createdAt: String,
    pub updatedAt: String,
}

#[spacetimedb::table(accessor = ticketremote_phone_backend)]
#[derive(Clone)]
pub struct TicketremotePhoneBackend {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub backendId: String,
    pub attachName: String,
    pub baseUrl: String,
    pub desiredState: String,
    pub streamState: String,
    pub healthJson: String,
    pub lastError: String,
    #[index(btree)]
    pub lastSeenAt: String,
}

#[spacetimedb::table(accessor = ticketremote_stream_desired_state, public,
    index(accessor = ticketBackend, btree(columns = [ticketId, backendId]))
)]
#[derive(Clone)]
pub struct TicketremoteStreamDesiredState {
    #[primary_key]
    pub id: String,
    pub ticketId: String,
    pub backendId: String,
    pub desiredActive: bool,
    pub viewerCount: u32,
    pub reason: String,
    pub revision: String,
    pub updatedBy: String,
    pub updatedAt: String,
}

#[spacetimedb::table(accessor = ticketremote_stream_command,
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt])),
    index(accessor = ticketBackendStatus, btree(columns = [ticketId, backendId, status]))
)]
#[derive(Clone)]
pub struct TicketremoteStreamCommand {
    #[primary_key]
    pub id: String,
    pub ticketId: String,
    pub backendId: String,
    pub commandType: String,
    #[index(btree)]
    pub status: String,
    pub revision: String,
    pub reason: String,
    pub payloadJson: String,
    pub createdAt: String,
    pub updatedAt: String,
    pub expiresAt: String,
}

#[spacetimedb::table(accessor = ticketremote_stream_command_signal, public)]
#[derive(Clone)]
pub struct TicketremoteStreamCommandSignal {
    #[primary_key]
    pub id: String,
    pub ticketId: String,
    pub backendId: String,
    pub revision: String,
    pub pendingCount: u32,
    pub updatedAt: String,
}

#[spacetimedb::table(accessor = ticketremote_phone_current_report, public)]
#[derive(Clone)]
pub struct TicketremotePhoneCurrentReport {
    #[primary_key]
    pub id: String,
    pub ticketId: String,
    pub backendId: String,
    pub streamState: String,
    pub desiredActive: bool,
    pub lastCommandId: String,
    pub lastCommandRevision: String,
    pub statusJson: String,
    pub updatedAt: String,
}

#[spacetimedb::table(accessor = ticketremote_relay_current_report, public)]
#[derive(Clone)]
pub struct TicketremoteRelayCurrentReport {
    #[primary_key]
    pub id: String,
    pub ticketId: String,
    pub backendId: String,
    pub videoClients: u32,
    pub streamVerdict: String,
    pub lastFrameAgoMillis: u32,
    pub framesForwarded: String,
    pub statusJson: String,
    pub updatedAt: String,
    #[default(None::<String>)]
    pub lastFrameAt: Option<String>,
}

#[spacetimedb::table(accessor = ticketremote_control_code_request, public,
    index(accessor = ticketOwnerUpdatedAt, btree(columns = [ticketId, ownerPublicId, updatedAt])),
    index(accessor = ticketUpdatedAt, btree(columns = [ticketId, updatedAt])),
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))
)]
#[derive(Clone)]
pub struct TicketremoteControlCodeRequest {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub ownerPublicId: String,
    #[index(btree)]
    pub status: String,
    pub reason: String,
    pub message: String,
    #[index(btree)]
    pub requestedAt: String,
    #[index(btree)]
    pub updatedAt: String,
    pub resultExpiresAt: String,
    pub captureRequired: bool,
    pub captureAcknowledged: bool,
    pub cleanupPending: bool,
    pub streamEpoch: String,
    pub frameSequence: String,
    pub minFrameSequence: String,
    pub resultFrameEpoch: String,
    pub resultMinFrameSequence: String,
    pub captureFrameEpoch: String,
    pub captureFrameSequence: String,
    #[index(btree)]
    pub expiresAt: String,
    #[default(None::<String>)]
    pub resultProof: Option<String>,
    #[default(None::<String>)]
    pub resultProofAt: Option<String>,
}

#[spacetimedb::table(accessor = ticketremote_control_code_owner,
    index(accessor = ticketEmail, btree(columns = [ticketId, email])),
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))
)]
#[derive(Clone)]
pub struct TicketremoteControlCodeOwner {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub sessionId: String,
    #[index(btree)]
    pub email: String,
    pub digits: String,
    #[index(btree)]
    pub requestedAt: String,
    #[index(btree)]
    pub expiresAt: String,
}

#[spacetimedb::table(accessor = ticketremote_safe_operational_log,
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt])),
    index(accessor = ticketCreatedAt, btree(columns = [ticketId, createdAt]))
)]
#[derive(Clone)]
pub struct TicketremoteSafeOperationalLog {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    pub source: String,
    pub level: String,
    pub event: String,
    pub correlationId: String,
    pub detailJson: String,
    pub createdAt: String,
    pub expiresAt: String,
}

#[spacetimedb::table(accessor = ticketremote_auth_config)]
#[derive(Clone)]
pub struct TicketremoteAuthConfig {
    #[primary_key]
    pub ticketId: String,
    pub issuer: String,
    pub audience: String,
    pub updatedAt: String,
}

#[spacetimedb::table(accessor = ticketremote_cleanup_schedule, scheduled(ticketremote_scheduled_cleanup_expired))]
#[derive(Clone)]
pub struct TicketremoteCleanupSchedule {
    #[primary_key]
    #[auto_inc]
    pub scheduled_id: u64,
    pub scheduled_at: ScheduleAt,
    #[index(btree)]
    pub ticketId: String,
    pub batchSize: u32,
    pub createdAt: String,
    pub updatedAt: String,
}

#[spacetimedb::table(accessor = ticketremote_service_identity)]
#[derive(Clone)]
pub struct TicketremoteServiceIdentity {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub identity: Identity,
    #[index(btree)]
    pub ticketId: String,
    pub createdAt: String,
    pub updatedAt: String,
}

#[derive(Clone, SpacetimeType)]
pub struct TicketremoteServiceTicket {
    pub id: String,
    pub displayName: String,
    pub updatedAt: String,
}

#[derive(Clone, SpacetimeType)]
pub struct TicketremoteServiceMember {
    pub id: String,
    pub ticketId: String,
    pub email: String,
    pub publicId: String,
    pub role: String,
    pub active: bool,
    pub updatedAt: String,
}

#[derive(Clone, SpacetimeType)]
pub struct TicketremoteServicePhone {
    pub id: String,
    pub ticketId: String,
    pub backendId: String,
    pub attachName: String,
    pub baseUrl: String,
    pub desiredState: String,
    pub streamState: String,
    pub healthJson: String,
    pub lastError: String,
    pub lastSeenAt: String,
}

#[derive(Clone, SpacetimeType)]
pub struct TicketremoteServiceStreamCommand {
    pub id: String,
    pub ticketId: String,
    pub backendId: String,
    pub commandType: String,
    pub status: String,
    pub revision: String,
    pub reason: String,
    pub payloadJson: String,
    pub createdAt: String,
    pub updatedAt: String,
    pub expiresAt: String,
}

#[spacetimedb::view(accessor = ticketremote_service_ticket, public, primary_key = id)]
pub fn ticketremote_service_ticket_view(ctx: &ViewContext) -> Vec<TicketremoteServiceTicket> {
    let Some(ticket_id) = service_ticket_id_for_viewer(ctx) else {
        return Vec::new();
    };
    ctx.db
        .ticketremote_ticket()
        .id()
        .find(&ticket_id)
        .map(|row| vec![service_ticket_from_row(&row)])
        .unwrap_or_default()
}

#[spacetimedb::view(accessor = ticketremote_service_ticket_member, public, primary_key = id)]
pub fn ticketremote_service_ticket_member_view(
    ctx: &ViewContext,
) -> Vec<TicketremoteServiceMember> {
    let Some(ticket_id) = service_ticket_id_for_viewer(ctx) else {
        return Vec::new();
    };
    ctx.db
        .ticketremote_ticket_member()
        .ticketId()
        .filter(&ticket_id)
        .map(|row| service_member_from_row(&row))
        .collect()
}

#[spacetimedb::view(accessor = ticketremote_service_phone_backend, public, primary_key = id)]
pub fn ticketremote_service_phone_backend_view(ctx: &ViewContext) -> Vec<TicketremoteServicePhone> {
    let Some(ticket_id) = service_ticket_id_for_viewer(ctx) else {
        return Vec::new();
    };
    ctx.db
        .ticketremote_phone_backend()
        .ticketId()
        .filter(&ticket_id)
        .map(|row| service_phone_from_row(&row))
        .collect()
}

#[spacetimedb::view(accessor = ticketremote_service_stream_command, public, primary_key = id)]
pub fn ticketremote_service_stream_command_view(
    ctx: &ViewContext,
) -> Vec<TicketremoteServiceStreamCommand> {
    let Some(ticket_id) = service_ticket_id_for_viewer(ctx) else {
        return Vec::new();
    };
    ctx.db
        .ticketremote_stream_command()
        .ticketBackendStatus()
        .filter((&ticket_id, "pixel", "pending"))
        .map(|row| service_stream_command_from_row(&row))
        .collect()
}

#[spacetimedb::reducer(init)]
pub fn init(ctx: &ReducerContext) {
    let now = now(ctx);
    ensure_cleanup_schedule(ctx, DEFAULT_TICKET_ID, &now);
}

#[spacetimedb::reducer(client_connected)]
pub fn identity_connected(_ctx: &ReducerContext) {}

#[spacetimedb::reducer(client_disconnected)]
pub fn identity_disconnected(_ctx: &ReducerContext) {}

#[spacetimedb::reducer]
pub fn ticketremote_register_service_identity(
    ctx: &ReducerContext,
    ticketId: String,
    now: String,
) -> Result<(), String> {
    require_service(ctx)?;
    register_service_identity(ctx, clean_ticket_id(&ticketId), &now_or(ctx, &now));
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_member_set_stream_focus(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    sessionId: String,
    active: bool,
    reason: String,
) -> Result<(), String> {
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let _email = client_email_from_auth(ctx, &ticket.id)?;
    let _session_id = non_empty(&sessionId, &connection_session_id(ctx));
    let viewers = if active { 1 } else { 0 };
    let focus_reason = bounded_text(
        &non_empty(
            &reason,
            if active {
                "browser_visible"
            } else {
                "browser_hidden"
            },
        ),
        120,
    );
    upsert_stream_desired_state(
        ctx,
        &ticket.id,
        &clean_backend_id(&backendId),
        active,
        viewers,
        &focus_reason,
        &now,
        "browser",
        &now,
    );
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_member_request_keyframe(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    reason: String,
) -> Result<(), String> {
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    insert_stream_command(
        ctx,
        &ticket.id,
        &clean_backend_id(&backendId),
        &format!("{}:browser:{}:keyframe", ticket.id, stable_stamp(&now)),
        "keyframe",
        &now,
        &bounded_text(&non_empty(&reason, "browser_request"), 120),
        &json_object(&[("source", "browser"), ("actor", &account_public_id(&email))]),
        30_000,
        &now,
    );
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_member_recover_stream(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    reason: String,
) -> Result<(), String> {
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    insert_stream_command(
        ctx,
        &ticket.id,
        &clean_backend_id(&backendId),
        &format!(
            "{}:browser:{}:recover_stream",
            ticket.id,
            stable_stamp(&now)
        ),
        "recover_stream",
        &now,
        &bounded_text(&non_empty(&reason, "browser_recovery"), 120),
        &json_object(&[("source", "browser"), ("actor", &account_public_id(&email))]),
        CONTROL_CODE_COMMAND_TTL_MS,
        &now,
    );
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_member_prepare_control_code(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    reason: String,
) -> Result<(), String> {
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    let owner_public_id = account_public_id(&email);
    let payload = serde_json::json!({
        "type": "prepare_control_code",
        "owner": "ticket",
        "app": "vivi",
        "flow": "control_code",
        "source": "browser_spacetime",
        "requester": owner_public_id
    })
    .to_string();
    insert_stream_command(
        ctx,
        &ticket.id,
        &clean_backend_id(&backendId),
        &format!(
            "{}:browser:{}:prepare_control_code",
            ticket.id,
            stable_stamp(&now)
        ),
        "prepare_control_code",
        &now,
        &bounded_text(&non_empty(&reason, "dialog_open"), 120),
        &payload,
        CONTROL_CODE_COMMAND_TTL_MS,
        &now,
    );
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_member_request_control_code(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    sessionId: String,
    digits: String,
) -> Result<(), String> {
    let now = now(ctx);
    let session_id = non_empty(&sessionId, &connection_session_id(ctx));
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    let clean_digits = digits
        .chars()
        .filter(|c| c.is_ascii_digit())
        .collect::<String>();
    if !valid_control_code_digits(&clean_digits) {
        return Err("invalid_code".into());
    }
    let now_ms = parse_time_ms(&now);
    for row in ctx
        .db
        .ticketremote_control_code_owner()
        .ticketId()
        .filter(&ticket.id)
    {
        if parse_time_ms(&row.expiresAt) <= now_ms {
            delete_control_code_request(ctx, &row.id);
        }
    }
    if active_control_code_owner_rows(ctx, &ticket.id, &email, &now).len()
        >= CONTROL_CODE_RATE_LIMIT
    {
        return Err("rate_limited".into());
    }
    let request_id = control_code_request_id(&ticket.id, &session_id, &now);
    let owner_public_id = account_public_id(&email);
    let owner = TicketremoteControlCodeOwner {
        id: request_id.clone(),
        ticketId: ticket.id.clone(),
        sessionId: session_id,
        email,
        digits: clean_digits.clone(),
        requestedAt: now.clone(),
        expiresAt: control_code_request_expires_at(&now),
    };
    ctx.db
        .ticketremote_control_code_owner()
        .insert(owner.clone());
    let public_request =
        insert_control_code_public_request(ctx, &ticket.id, &request_id, &owner_public_id, &now);
    let _ = public_request;
    let payload = serde_json::json!({
        "type": "generate_control_code",
        "owner": "ticket",
        "app": "vivi",
        "flow": "control_code",
        "requestId": request_id,
        "digits": clean_digits,
        "source": "browser_spacetime",
        "requester": owner_public_id,
        "serverSentAt": now,
        "dispatchAttempt": 1
    })
    .to_string();
    insert_stream_command(
        ctx,
        &ticket.id,
        &clean_backend_id(&backendId),
        &format!("{}:generate_control_code", request_id),
        "generate_control_code",
        &now,
        "control_code_request",
        &payload,
        CONTROL_CODE_PHONE_TTL_MS,
        &now,
    );
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_member_confirm_control_code_browser_capture(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    _sessionId: String,
    requestId: String,
    candidateFrameEpoch: String,
    candidateFrameSequence: String,
    acceptedReason: String,
) -> Result<(), String> {
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    let request_id = requestId.trim().to_string();
    let Some(owner) = ctx
        .db
        .ticketremote_control_code_owner()
        .id()
        .find(&request_id)
    else {
        return Err("not_found".into());
    };
    if owner.ticketId != ticket.id || clean_email(&owner.email) != email {
        return Err("not_found".into());
    }
    let Some(current) = ctx
        .db
        .ticketremote_control_code_request()
        .id()
        .find(&request_id)
    else {
        return Err("request_not_ready".into());
    };
    if current.status != "succeeded" {
        return Err("request_not_ready".into());
    }
    let frame_epoch = bounded_frame_ordinal(&candidateFrameEpoch);
    let frame_sequence = bounded_frame_ordinal(&candidateFrameSequence);
    let marker_epoch = bounded_frame_ordinal(if current.resultFrameEpoch != "0" {
        &current.resultFrameEpoch
    } else {
        &current.streamEpoch
    });
    let marker_sequence_source = if current.resultMinFrameSequence != "0" {
        &current.resultMinFrameSequence
    } else if current.minFrameSequence != "0" {
        &current.minFrameSequence
    } else {
        &current.frameSequence
    };
    let marker_sequence = bounded_frame_ordinal(marker_sequence_source);
    if marker_epoch != "0" && frame_epoch != marker_epoch {
        return Err("frame_before_marker".into());
    }
    if marker_sequence != "0" && compare_ordinal(&frame_sequence, &marker_sequence) < 0 {
        return Err("frame_before_marker".into());
    }
    let accepted_reason = non_empty(&acceptedReason, "browser_capture_confirmed");
    let frame_epoch_number = frame_ordinal_number(&frame_epoch);
    let frame_sequence_number = frame_ordinal_number(&frame_sequence);
    update_control_code_public_request(
        ctx,
        &request_id,
        ControlCodeChanges {
            captureRequired: Some(false),
            captureAcknowledged: Some(true),
            captureFrameEpoch: Some(frame_epoch.clone()),
            captureFrameSequence: Some(frame_sequence.clone()),
            reason: Some(bounded_text(&accepted_reason, 160)),
            expiresAt: Some(control_code_result_expires_at(&now)),
            ..Default::default()
        },
        &now,
    );
    let payload = serde_json::json!({
        "owner": "ticket",
        "app": "vivi",
        "flow": "control_code",
        "requestId": &request_id,
        "accepted": true,
        "reason": &accepted_reason,
        "candidateFrameEpoch": frame_epoch_number,
        "candidateFrameSequence": frame_sequence_number,
        "source": "browser_spacetime"
    })
    .to_string();
    insert_stream_command(
        ctx,
        &ticket.id,
        &clean_backend_id(&backendId),
        &format!("{}:control_code_browser_capture", request_id),
        "control_code_browser_capture",
        &now,
        &bounded_text(&accepted_reason, 120),
        &payload,
        CONTROL_CODE_COMMAND_TTL_MS,
        &now,
    );
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_member_close_control_code(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    _sessionId: String,
    requestId: String,
    reason: String,
) -> Result<(), String> {
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    let request_id = requestId.trim().to_string();
    let Some(owner) = ctx
        .db
        .ticketremote_control_code_owner()
        .id()
        .find(&request_id)
    else {
        return Err("not_found".into());
    };
    if owner.ticketId != ticket.id || clean_email(&owner.email) != email {
        return Err("not_found".into());
    }
    let current_request = ctx
        .db
        .ticketremote_control_code_request()
        .id()
        .find(&request_id);
    let capture_acknowledged = current_request
        .as_ref()
        .map(|row| row.captureAcknowledged)
        .unwrap_or(false);
    if capture_acknowledged {
        update_control_code_public_request(
            ctx,
            &request_id,
            ControlCodeChanges {
                captureRequired: Some(false),
                cleanupPending: Some(false),
                expiresAt: Some(control_code_result_expires_at(&now)),
                ..Default::default()
            },
            &now,
        );
        return Ok(());
    }
    let close_reason = non_empty(&reason, "browser_closed");
    let capture_reason = non_empty(&reason, "browser_capture_closed");
    update_control_code_public_request(
        ctx,
        &request_id,
        ControlCodeChanges {
            status: Some("closed".into()),
            reason: Some(bounded_text(&close_reason, 160)),
            message: Some(String::new()),
            captureRequired: Some(false),
            cleanupPending: Some(!capture_acknowledged),
            resultExpiresAt: Some(String::new()),
            expiresAt: Some(command_expires_at(&now, CONTROL_CODE_COMMAND_TTL_MS)),
            ..Default::default()
        },
        &now,
    );
    let payload = serde_json::json!({
        "owner": "ticket",
        "app": "vivi",
        "flow": "control_code",
        "requestId": &request_id,
        "accepted": false,
        "reason": &capture_reason,
        "candidateFrameEpoch": 0,
        "candidateFrameSequence": 0,
        "source": "browser_spacetime"
    })
    .to_string();
    insert_stream_command(
        ctx,
        &ticket.id,
        &clean_backend_id(&backendId),
        &format!("{}:control_code_browser_capture_closed", request_id),
        "control_code_browser_capture",
        &now,
        &bounded_text(&capture_reason, 120),
        &payload,
        CONTROL_CODE_COMMAND_TTL_MS,
        &now,
    );
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_member_append_safe_operational_log(
    ctx: &ReducerContext,
    id: String,
    ticketId: String,
    level: String,
    event: String,
    correlationId: String,
    detailJson: String,
) -> Result<(), String> {
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    client_email_from_auth(ctx, &ticket.id)?;
    insert_safe_operational_log(
        ctx,
        &ticket.id,
        "browser",
        &level,
        &event,
        &correlationId,
        &detailJson,
        &id,
        &now,
    );
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_member_upsert_member(
    ctx: &ReducerContext,
    ticketId: String,
    email: String,
    role: String,
) -> Result<(), String> {
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let actor = client_email_from_auth(ctx, &ticket.id)?;
    if !is_admin(ctx, &ticket.id, &actor) {
        return Err("forbidden".into());
    }
    upsert_member_row(ctx, &ticket.id, &email, &role, &now);
    let _ = actor;
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_member_remove_member(
    ctx: &ReducerContext,
    ticketId: String,
    email: String,
) -> Result<(), String> {
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let actor = client_email_from_auth(ctx, &ticket.id)?;
    if !is_admin(ctx, &ticket.id, &actor) {
        return Err("forbidden".into());
    }
    deactivate_member_row(ctx, &ticket.id, &email, &now);
    let _ = actor;
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_service_bootstrap(
    ctx: &ReducerContext,
    ticketId: String,
    displayName: String,
    adminEmail: String,
    phoneBackendId: String,
    phoneBaseUrl: String,
    phoneAttachName: String,
    authIssuer: String,
    authAudience: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, &displayName, &now);
    register_service_identity(ctx, ticket.id.clone(), &now);
    let email = clean_email(&adminEmail);
    if !email.is_empty()
        && ctx
            .db
            .ticketremote_ticket_member()
            .id()
            .find(&member_id(&ticket.id, &email))
            .is_none()
    {
        let member = TicketremoteTicketMember {
            id: member_id(&ticket.id, &email),
            ticketId: ticket.id.clone(),
            email,
            role: "owner".into(),
            active: true,
            createdAt: now.clone(),
            updatedAt: now.clone(),
        };
        ctx.db.ticketremote_ticket_member().insert(member.clone());
        upsert_service_member_projection(ctx, &member);
    }
    if !phoneBackendId.trim().is_empty() {
        let backend_id = clean_backend_id(&phoneBackendId);
        let attach_name = non_empty(&phoneAttachName, &backend_id);
        clear_phone_backends(ctx, &ticket.id);
        let phone = TicketremotePhoneBackend {
            id: phone_row_id(&ticket.id, &backend_id),
            ticketId: ticket.id.clone(),
            backendId: backend_id.clone(),
            attachName: attach_name.clone(),
            baseUrl: phoneBaseUrl.trim().to_string(),
            desiredState: "idle".into(),
            streamState: "idle".into(),
            healthJson: String::new(),
            lastError: String::new(),
            lastSeenAt: now.clone(),
        };
        ctx.db.ticketremote_phone_backend().insert(phone.clone());
        upsert_service_phone_projection(ctx, &phone);
        upsert_stream_desired_state(
            ctx,
            &ticket.id,
            &backend_id,
            false,
            0,
            "bootstrap",
            &now,
            "service_bootstrap",
            &now,
        );
        upsert_phone_current_report(
            ctx,
            &ticket.id,
            &backend_id,
            "idle",
            false,
            "",
            "",
            "{}",
            &now,
        );
        upsert_relay_current_report(ctx, &ticket.id, &backend_id, 0, "idle", "", "0", "{}", &now);
    }
    let issuer = authIssuer.trim().to_string();
    let audience = authAudience.trim().to_string();
    if !issuer.is_empty() && !audience.is_empty() {
        if ctx
            .db
            .ticketremote_auth_config()
            .ticketId()
            .find(&ticket.id)
            .is_some()
        {
            ctx.db
                .ticketremote_auth_config()
                .ticketId()
                .delete(&ticket.id);
        }
        ctx.db
            .ticketremote_auth_config()
            .insert(TicketremoteAuthConfig {
                ticketId: ticket.id.clone(),
                issuer,
                audience,
                updatedAt: now.clone(),
            });
    }
    ensure_cleanup_schedule(ctx, &ticket.id, &now);
    cleanup_expired(ctx, &ticket.id, &now, CLEANUP_BATCH_SIZE);
    sync_service_projections(ctx, &ticket.id);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_scheduled_cleanup_expired(
    ctx: &ReducerContext,
    arg: TicketremoteCleanupSchedule,
) -> Result<(), String> {
    if !ctx.sender_auth().is_internal() && !has_service_role(ctx) {
        return Err("internal role required".into());
    }
    let now = now(ctx);
    let batch_size = if arg.batchSize == 0 {
        CLEANUP_BATCH_SIZE
    } else {
        arg.batchSize.min(CLEANUP_BATCH_SIZE)
    };
    cleanup_expired(ctx, &arg.ticketId, &now, batch_size);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_upsert_member(
    ctx: &ReducerContext,
    ticketId: String,
    actorEmail: String,
    email: String,
    role: String,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let now = now_or(ctx, &nowArg);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    if !is_admin(ctx, &ticket.id, &actorEmail) {
        return Err("forbidden".into());
    }
    upsert_member_row(ctx, &ticket.id, &email, &role, &now);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_remove_member(
    ctx: &ReducerContext,
    ticketId: String,
    actorEmail: String,
    email: String,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let now = now_or(ctx, &nowArg);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    if !is_admin(ctx, &ticket.id, &actorEmail) {
        return Err("forbidden".into());
    }
    deactivate_member_row(ctx, &ticket.id, &email, &now);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_update_phone(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    attachName: String,
    baseUrl: String,
    desiredState: String,
    healthJson: String,
    lastError: String,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    apply_phone_update(
        ctx,
        &ticketId,
        &backendId,
        &attachName,
        &baseUrl,
        &desiredState,
        &healthJson,
        &lastError,
        &now_or(ctx, &nowArg),
    );
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_set_stream_desired_state(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    desiredActive: bool,
    viewerCount: u32,
    reason: String,
    revision: String,
    updatedBy: String,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let now = now_or(ctx, &nowArg);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    upsert_stream_desired_state(
        ctx,
        &ticket.id,
        &backendId,
        desiredActive,
        viewerCount,
        &reason,
        &revision,
        &updatedBy,
        &now,
    );
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_append_stream_command(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    commandId: String,
    commandType: String,
    revision: String,
    reason: String,
    payloadJson: String,
    ttlMillis: u32,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let now = now_or(ctx, &nowArg);
    let row = insert_stream_command(
        ctx,
        &ticketId,
        &backendId,
        &commandId,
        &commandType,
        &revision,
        &reason,
        &payloadJson,
        ttlMillis as i64,
        &now,
    );
    let _ = row;
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_ack_stream_command(
    ctx: &ReducerContext,
    commandId: String,
    status: String,
    reason: String,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let now = now_or(ctx, &nowArg);
    update_stream_command_status(ctx, &commandId, &status, &reason, &now);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_update_phone_current_report(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    streamState: String,
    desiredActive: bool,
    lastCommandId: String,
    lastCommandRevision: String,
    statusJson: String,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let now = now_or(ctx, &nowArg);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    upsert_phone_current_report(
        ctx,
        &ticket.id,
        &backendId,
        &streamState,
        desiredActive,
        &lastCommandId,
        &lastCommandRevision,
        &statusJson,
        &now,
    );
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_update_relay_current_report(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    videoClients: u32,
    streamVerdict: String,
    lastFrameAt: String,
    framesForwarded: String,
    statusJson: String,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let now = now_or(ctx, &nowArg);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    upsert_relay_current_report(
        ctx,
        &ticket.id,
        &backendId,
        videoClients,
        &streamVerdict,
        &lastFrameAt,
        &framesForwarded,
        &statusJson,
        &now,
    );
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_update_control_code_request(
    ctx: &ReducerContext,
    ticketId: String,
    requestId: String,
    status: String,
    reason: String,
    message: String,
    streamEpoch: String,
    frameSequence: String,
    minFrameSequence: String,
    resultFrameEpoch: String,
    resultMinFrameSequence: String,
    resultProof: String,
    resultProofAt: String,
    cleanupPending: bool,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let now = now_or(ctx, &nowArg);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let Some(existing) = ctx
        .db
        .ticketremote_control_code_request()
        .id()
        .find(&requestId)
    else {
        return Ok(());
    };
    if existing.ticketId != ticket.id {
        return Ok(());
    }
    let mut clean_status = clean_token(&non_empty(&status, &existing.status), "queued").replace(
        |c: char| !c.is_ascii_alphanumeric() && c != '_' && c != '-',
        "_",
    );
    let incoming_reason = bounded_text(&non_empty(&reason, &existing.reason), 200);
    let preserve_captured_success = existing.status == "succeeded"
        && existing.captureAcknowledged
        && matches!(clean_status.as_str(), "succeeded" | "closed")
        && control_code_cleanup_reason(&incoming_reason);
    if preserve_captured_success {
        clean_status = existing.status.clone();
    }
    let terminal_failure = matches!(clean_status.as_str(), "failed" | "expired" | "closed");
    let succeeded = clean_status == "succeeded";
    let clean_result_proof = clean_control_code_result_proof(&resultProof);
    let clean_result_proof_at = bounded_text(resultProofAt.trim(), 80);
    update_control_code_public_request(
        ctx,
        &requestId,
        ControlCodeChanges {
            status: Some(clean_status.clone()),
            reason: Some(if preserve_captured_success {
                existing.reason.clone()
            } else {
                incoming_reason
            }),
            message: Some(bounded_text(&message, 240)),
            streamEpoch: Some(bounded_frame_ordinal(&non_empty(
                &streamEpoch,
                &existing.streamEpoch,
            ))),
            frameSequence: Some(bounded_frame_ordinal(&non_empty(
                &frameSequence,
                &existing.frameSequence,
            ))),
            minFrameSequence: Some(bounded_frame_ordinal(&non_empty(
                &minFrameSequence,
                &existing.minFrameSequence,
            ))),
            resultFrameEpoch: Some(bounded_frame_ordinal(&non_empty(
                &resultFrameEpoch,
                &existing.resultFrameEpoch,
            ))),
            resultMinFrameSequence: Some(bounded_frame_ordinal(&non_empty(
                &resultMinFrameSequence,
                &existing.resultMinFrameSequence,
            ))),
            resultProof: if clean_result_proof.is_empty() {
                None
            } else {
                Some(clean_result_proof)
            },
            resultProofAt: if clean_result_proof_at.is_empty() {
                None
            } else {
                Some(clean_result_proof_at)
            },
            captureRequired: Some(succeeded && !existing.captureAcknowledged),
            cleanupPending: Some(cleanupPending),
            resultExpiresAt: Some(if succeeded {
                control_code_result_expires_at(&now)
            } else {
                String::new()
            }),
            expiresAt: Some(command_expires_at(
                &now,
                if terminal_failure {
                    CONTROL_CODE_COMMAND_TTL_MS
                } else {
                    CONTROL_CODE_REQUEST_TTL_MS
                },
            )),
            ..Default::default()
        },
        &now,
    );
    Ok(())
}

fn control_code_cleanup_reason(reason: &str) -> bool {
    matches!(
        reason,
        "ticket_detail"
            | "return_to_raw_complete"
            | "browser_capture_confirmed"
            | "control_code_cleanup_attention_needed"
            | "rs_monthly_ticket_cleanup_attention_needed"
    )
}

#[spacetimedb::reducer]
pub fn ticketremote_append_safe_operational_log(
    ctx: &ReducerContext,
    id: String,
    ticketId: String,
    source: String,
    level: String,
    event: String,
    correlationId: String,
    detailJson: String,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let now = now_or(ctx, &nowArg);
    insert_safe_operational_log(
        ctx,
        &clean_ticket_id(&ticketId),
        &source,
        &level,
        &event,
        &correlationId,
        &detailJson,
        &id,
        &now,
    );
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_cleanup_expired(
    ctx: &ReducerContext,
    ticketId: String,
    nowArg: String,
    batchSize: u32,
) -> Result<(), String> {
    require_service(ctx)?;
    cleanup_expired(
        ctx,
        &clean_ticket_id(&ticketId),
        &now_or(ctx, &nowArg),
        batchSize,
    );
    Ok(())
}

fn now(ctx: &ReducerContext) -> String {
    iso(ctx.timestamp)
}

fn now_or(ctx: &ReducerContext, value: &str) -> String {
    let clean = value.trim();
    if clean.is_empty() {
        now(ctx)
    } else {
        clean.to_string()
    }
}

fn iso(timestamp: Timestamp) -> String {
    timestamp
        .to_rfc3339()
        .unwrap_or_else(|_| "1970-01-01T00:00:00Z".into())
}

fn add_ms(value: &str, ms: i64) -> String {
    let micros = parse_time_micros(value).saturating_add(ms.saturating_mul(1000));
    iso(Timestamp::from_micros_since_unix_epoch(micros))
}

fn parse_time_micros(value: &str) -> i64 {
    DateTime::parse_from_rfc3339(value.trim())
        .map(|dt| dt.timestamp_micros())
        .or_else(|_| {
            value
                .trim()
                .parse::<DateTime<Utc>>()
                .map(|dt| dt.timestamp_micros())
        })
        .unwrap_or(0)
}

fn parse_time_ms(value: &str) -> i64 {
    parse_time_micros(value) / 1000
}

fn clean_ticket_id(value: &str) -> String {
    non_empty(value, DEFAULT_TICKET_ID)
}

fn clean_backend_id(value: &str) -> String {
    non_empty(value, "pixel")
}

fn clean_email(value: &str) -> String {
    value.trim().to_ascii_lowercase()
}

fn clean_role(value: &str) -> String {
    match value.trim() {
        "owner" => "owner".into(),
        "admin" => "admin".into(),
        _ => "member".into(),
    }
}

fn clean_token(value: &str, fallback: &str) -> String {
    let clean = value.trim();
    if clean.is_empty() {
        fallback.to_string()
    } else {
        clean.to_string()
    }
}

fn non_empty(value: &str, fallback: &str) -> String {
    let clean = value.trim();
    if clean.is_empty() {
        fallback.to_string()
    } else {
        clean.to_string()
    }
}

fn bounded_text(value: &str, max: usize) -> String {
    value.chars().take(max).collect()
}

fn safe_json_string(value: &str, max: usize) -> String {
    let trimmed = value.trim();
    let raw = if trimmed.is_empty() { "{}" } else { trimmed };
    let valid = serde_json::from_str::<serde_json::Value>(raw).is_ok();
    let source = if valid { raw } else { "{}" };
    source.chars().take(max).collect()
}

fn json_object(pairs: &[(&str, &str)]) -> String {
    let mut map = serde_json::Map::new();
    for (key, value) in pairs {
        map.insert((*key).into(), serde_json::Value::String((*value).into()));
    }
    serde_json::Value::Object(map).to_string()
}

fn member_id(ticket_id: &str, email: &str) -> String {
    format!("{}:{}", clean_ticket_id(ticket_id), clean_email(email))
}

fn phone_row_id(ticket_id: &str, backend_id: &str) -> String {
    format!(
        "{}:{}",
        clean_ticket_id(ticket_id),
        clean_backend_id(backend_id)
    )
}

fn stable_stamp(value: &str) -> String {
    let out: String = value
        .chars()
        .filter(|c| c.is_ascii_alphanumeric())
        .collect();
    non_empty(&out, "time")
}

fn account_public_id(email: &str) -> String {
    let mut hash: u32 = 0x811c9dc5;
    for byte in clean_email(email).as_bytes() {
        hash ^= *byte as u32;
        hash = hash.wrapping_mul(0x01000193);
    }
    let mut value = to_base36(hash);
    if value.len() < 4 {
        value = format!("{:0>4}", value);
    }
    value.chars().take(4).collect()
}

fn to_base36(mut value: u32) -> String {
    if value == 0 {
        return "0".into();
    }
    let mut chars = Vec::new();
    while value > 0 {
        let digit = (value % 36) as u8;
        chars.push(match digit {
            0..=9 => (b'0' + digit) as char,
            _ => (b'a' + digit - 10) as char,
        });
        value /= 36;
    }
    chars.iter().rev().collect()
}

fn history_expires_at(now: &str) -> String {
    add_ms(now, HISTORY_TTL_MS)
}

fn command_expires_at(now: &str, ttl_ms: i64) -> String {
    let ttl = if ttl_ms <= 0 || ttl_ms > HISTORY_TTL_MS {
        HISTORY_TTL_MS
    } else {
        ttl_ms
    };
    add_ms(now, ttl)
}

fn control_code_request_expires_at(now: &str) -> String {
    add_ms(now, CONTROL_CODE_REQUEST_TTL_MS)
}

fn control_code_result_expires_at(now: &str) -> String {
    add_ms(now, CONTROL_CODE_RESULT_TTL_MS)
}

fn valid_control_code_digits(value: &str) -> bool {
    (2..=8).contains(&value.len()) && value.chars().all(|c| c.is_ascii_digit())
}

fn clean_control_code_result_proof(value: &str) -> String {
    match value.trim() {
        "phone_root" => "phone_root".into(),
        "phone_visual" => "phone_visual".into(),
        "phone_visual_root_confirmed" => "phone_visual_root_confirmed".into(),
        "phone_visual_raw_ticket_after_submit" => "phone_visual_raw_ticket_after_submit".into(),
        "phone_root_image" => "phone_root_image".into(),
        "browser_frame" => "browser_frame".into(),
        _ => String::new(),
    }
}

fn bounded_frame_ordinal(value: &str) -> String {
    let digits: String = value
        .chars()
        .filter(|c| c.is_ascii_digit())
        .take(24)
        .collect();
    non_empty(&digits, "0")
}

fn frame_ordinal_number(value: &str) -> i64 {
    value.trim().parse::<i64>().unwrap_or(0)
}

fn compare_ordinal(left: &str, right: &str) -> i8 {
    let l = left.trim_start_matches('0');
    let r = right.trim_start_matches('0');
    let l = if l.is_empty() { "0" } else { l };
    let r = if r.is_empty() { "0" } else { r };
    if l.len() != r.len() {
        return if l.len() < r.len() { -1 } else { 1 };
    }
    match l.cmp(r) {
        std::cmp::Ordering::Less => -1,
        std::cmp::Ordering::Equal => 0,
        std::cmp::Ordering::Greater => 1,
    }
}

fn control_code_request_id(ticket_id: &str, session_id: &str, now: &str) -> String {
    format!(
        "{}:{}:{}:control_code",
        clean_ticket_id(ticket_id),
        session_id.trim(),
        stable_stamp(now)
    )
}

fn connection_session_id(ctx: &ReducerContext) -> String {
    ctx.connection_id()
        .map(|id| format!("{id:?}"))
        .unwrap_or_else(|| ctx.sender().to_string())
}

fn jwt_payload(ctx: &ReducerContext) -> Result<serde_json::Value, String> {
    let Some(jwt) = ctx.sender_auth().jwt() else {
        return Err("auth required".into());
    };
    serde_json::from_str(jwt.raw_payload()).map_err(|_| "invalid auth payload".to_string())
}

fn jwt_audience_includes(payload: &serde_json::Value, expected: &str) -> bool {
    let expected = expected.trim();
    if expected.is_empty() {
        return false;
    }
    match payload.get("aud") {
        Some(serde_json::Value::String(value)) => value.trim() == expected,
        Some(serde_json::Value::Array(values)) => values
            .iter()
            .any(|v| v.as_str().map(|s| s.trim() == expected).unwrap_or(false)),
        _ => false,
    }
}

fn jwt_roles_include(payload: &serde_json::Value, expected: &str) -> bool {
    match payload.get("roles") {
        Some(serde_json::Value::String(value)) => value.split(',').any(|v| v.trim() == expected),
        Some(serde_json::Value::Array(values)) => values
            .iter()
            .any(|v| v.as_str().map(|s| s.trim() == expected).unwrap_or(false)),
        _ => false,
    }
}

fn has_service_role(ctx: &ReducerContext) -> bool {
    jwt_payload(ctx)
        .map(|payload| jwt_roles_include(&payload, "ticketremote_service"))
        .unwrap_or(false)
}

fn require_service(ctx: &ReducerContext) -> Result<(), String> {
    if has_service_role(ctx) {
        Ok(())
    } else {
        Err("service role required".into())
    }
}

fn register_service_identity(ctx: &ReducerContext, ticket_id: String, now: &str) {
    let id = ctx.sender().to_string();
    if let Some(existing) = ctx.db.ticketremote_service_identity().id().find(&id) {
        ctx.db
            .ticketremote_service_identity()
            .id()
            .update(TicketremoteServiceIdentity {
                ticketId: ticket_id,
                updatedAt: now.into(),
                ..existing
            });
    } else {
        ctx.db
            .ticketremote_service_identity()
            .insert(TicketremoteServiceIdentity {
                id,
                identity: ctx.sender(),
                ticketId: ticket_id,
                createdAt: now.into(),
                updatedAt: now.into(),
            });
    }
}

fn auth_config(ctx: &ReducerContext, ticket_id: &str) -> Option<TicketremoteAuthConfig> {
    ctx.db
        .ticketremote_auth_config()
        .ticketId()
        .find(&clean_ticket_id(ticket_id))
}

fn client_email_from_auth(ctx: &ReducerContext, ticket_id: &str) -> Result<String, String> {
    if !ctx.sender_auth().has_jwt() {
        return Err("auth required".into());
    }
    let payload = jwt_payload(ctx)?;
    let Some(config) = auth_config(ctx, ticket_id) else {
        return Err("auth config required".into());
    };
    let issuer = payload
        .get("iss")
        .and_then(|v| v.as_str())
        .unwrap_or("")
        .trim()
        .trim_end_matches('/');
    if issuer != config.issuer.trim().trim_end_matches('/') {
        return Err("invalid auth issuer".into());
    }
    if !jwt_audience_includes(&payload, &config.audience) {
        return Err("invalid auth audience".into());
    }
    let email = clean_email(payload.get("email").and_then(|v| v.as_str()).unwrap_or(""));
    if email.is_empty() || payload.get("email_verified").and_then(|v| v.as_bool()) != Some(true) {
        return Err("verified email required".into());
    }
    if !is_member(ctx, ticket_id, &email) {
        return Err("ticket membership required".into());
    }
    Ok(email)
}

fn ensure_ticket(
    ctx: &ReducerContext,
    ticket_id: &str,
    display_name: &str,
    now: &str,
) -> TicketremoteTicket {
    let id = clean_ticket_id(ticket_id);
    if let Some(existing) = ctx.db.ticketremote_ticket().id().find(&id) {
        if !display_name.trim().is_empty() && existing.displayName != display_name.trim() {
            let updated = TicketremoteTicket {
                displayName: display_name.trim().into(),
                updatedAt: now.into(),
                ..existing
            };
            ctx.db.ticketremote_ticket().id().update(updated.clone());
            upsert_service_ticket_projection(ctx, &updated);
            return updated;
        }
        upsert_service_ticket_projection(ctx, &existing);
        return existing;
    }
    let ticket = TicketremoteTicket {
        id,
        displayName: non_empty(display_name, DEFAULT_TICKET_NAME),
        createdAt: now.into(),
        updatedAt: now.into(),
    };
    ctx.db.ticketremote_ticket().insert(ticket.clone());
    upsert_service_ticket_projection(ctx, &ticket);
    ticket
}

fn service_ticket_id_for_viewer(ctx: &ViewContext) -> Option<String> {
    let identity = ctx.sender();
    ctx.db
        .ticketremote_service_identity()
        .identity()
        .filter(&identity)
        .next()
        .map(|row| clean_ticket_id(&row.ticketId))
}

fn service_ticket_from_row(ticket: &TicketremoteTicket) -> TicketremoteServiceTicket {
    TicketremoteServiceTicket {
        id: ticket.id.clone(),
        displayName: ticket.displayName.clone(),
        updatedAt: ticket.updatedAt.clone(),
    }
}

fn service_member_from_row(row: &TicketremoteTicketMember) -> TicketremoteServiceMember {
    let email = clean_email(&row.email);
    TicketremoteServiceMember {
        id: row.id.clone(),
        ticketId: row.ticketId.clone(),
        email: email.clone(),
        publicId: account_public_id(&email),
        role: clean_role(&row.role),
        active: row.active,
        updatedAt: row.updatedAt.clone(),
    }
}

fn service_phone_from_row(row: &TicketremotePhoneBackend) -> TicketremoteServicePhone {
    TicketremoteServicePhone {
        id: row.id.clone(),
        ticketId: row.ticketId.clone(),
        backendId: row.backendId.clone(),
        attachName: row.attachName.clone(),
        baseUrl: row.baseUrl.clone(),
        desiredState: row.desiredState.clone(),
        streamState: row.streamState.clone(),
        healthJson: row.healthJson.clone(),
        lastError: row.lastError.clone(),
        lastSeenAt: row.lastSeenAt.clone(),
    }
}

fn service_stream_command_from_row(
    row: &TicketremoteStreamCommand,
) -> TicketremoteServiceStreamCommand {
    TicketremoteServiceStreamCommand {
        id: row.id.clone(),
        ticketId: row.ticketId.clone(),
        backendId: row.backendId.clone(),
        commandType: row.commandType.clone(),
        status: row.status.clone(),
        revision: row.revision.clone(),
        reason: row.reason.clone(),
        payloadJson: row.payloadJson.clone(),
        createdAt: row.createdAt.clone(),
        updatedAt: row.updatedAt.clone(),
        expiresAt: row.expiresAt.clone(),
    }
}

fn upsert_service_ticket_projection(_ctx: &ReducerContext, _ticket: &TicketremoteTicket) {}
fn upsert_service_member_projection(_ctx: &ReducerContext, _row: &TicketremoteTicketMember) {}
fn upsert_service_phone_projection(_ctx: &ReducerContext, _row: &TicketremotePhoneBackend) {}
fn upsert_service_command_projection(_ctx: &ReducerContext, _row: &TicketremoteStreamCommand) {}
fn delete_service_command_projection(_ctx: &ReducerContext, _command_id: &str) {}
fn sync_service_projections(_ctx: &ReducerContext, _ticket_id: &str) {}

fn is_member(ctx: &ReducerContext, ticket_id: &str, email: &str) -> bool {
    ctx.db
        .ticketremote_ticket_member()
        .id()
        .find(&member_id(ticket_id, email))
        .map(|row| row.active)
        .unwrap_or(false)
}

fn is_admin(ctx: &ReducerContext, ticket_id: &str, email: &str) -> bool {
    ctx.db
        .ticketremote_ticket_member()
        .id()
        .find(&member_id(ticket_id, email))
        .map(|row| row.active && (row.role == "owner" || row.role == "admin"))
        .unwrap_or(false)
}

fn upsert_member_row(ctx: &ReducerContext, ticket_id: &str, email: &str, role: &str, now: &str) {
    let email = clean_email(email);
    if email.is_empty() {
        return;
    }
    let id = member_id(ticket_id, &email);
    let created_at = ctx
        .db
        .ticketremote_ticket_member()
        .id()
        .find(&id)
        .map(|row| {
            ctx.db.ticketremote_ticket_member().id().delete(&id);
            row.createdAt
        })
        .unwrap_or_else(|| now.into());
    let row = TicketremoteTicketMember {
        id,
        ticketId: clean_ticket_id(ticket_id),
        email,
        role: clean_role(role),
        active: true,
        createdAt: created_at,
        updatedAt: now.into(),
    };
    ctx.db.ticketremote_ticket_member().insert(row.clone());
    upsert_service_member_projection(ctx, &row);
}

fn deactivate_member_row(ctx: &ReducerContext, ticket_id: &str, email: &str, now: &str) {
    let id = member_id(ticket_id, email);
    if let Some(existing) = ctx.db.ticketremote_ticket_member().id().find(&id) {
        let row = TicketremoteTicketMember {
            active: false,
            updatedAt: now.into(),
            ..existing
        };
        ctx.db.ticketremote_ticket_member().id().update(row.clone());
        upsert_service_member_projection(ctx, &row);
    }
}

fn ensure_cleanup_schedule(ctx: &ReducerContext, ticket_id: &str, now: &str) {
    let schedule =
        ScheduleAt::Interval(std::time::Duration::from_secs(CLEANUP_INTERVAL_SECS).into());
    if let Some(existing) = ctx
        .db
        .ticketremote_cleanup_schedule()
        .ticketId()
        .filter(ticket_id)
        .next()
    {
        ctx.db
            .ticketremote_cleanup_schedule()
            .scheduled_id()
            .update(TicketremoteCleanupSchedule {
                scheduled_at: schedule,
                batchSize: CLEANUP_BATCH_SIZE,
                updatedAt: now.into(),
                ..existing
            });
    } else {
        ctx.db
            .ticketremote_cleanup_schedule()
            .insert(TicketremoteCleanupSchedule {
                scheduled_id: 0,
                scheduled_at: schedule,
                ticketId: clean_ticket_id(ticket_id),
                batchSize: CLEANUP_BATCH_SIZE,
                createdAt: now.into(),
                updatedAt: now.into(),
            });
    }
}

fn clear_phone_backends(ctx: &ReducerContext, ticket_id: &str) {
    let rows: Vec<_> = ctx
        .db
        .ticketremote_phone_backend()
        .ticketId()
        .filter(ticket_id)
        .collect();
    for row in rows {
        ctx.db.ticketremote_phone_backend().id().delete(&row.id);
    }
}

fn compact_phone_stream_state(desired_state: &str, health_json: &str) -> String {
    let desired = non_empty(desired_state, "idle");
    let raw = health_json.trim();
    if raw.is_empty() {
        return desired;
    }
    let Ok(parsed) = serde_json::from_str::<serde_json::Value>(raw) else {
        return desired;
    };
    let data = parsed.get("data").unwrap_or(&parsed);
    for key in ["streamVerdict", "streamState", "captureState"] {
        if let Some(value) = data.get(key).and_then(|v| v.as_str()) {
            if !value.trim().is_empty() {
                return value.trim().into();
            }
        }
    }
    if data.get("streamActive").and_then(|v| v.as_bool()) == Some(true)
        || data.get("connected").and_then(|v| v.as_bool()) == Some(true)
    {
        return "streaming".into();
    }
    if data.get("streamActive").and_then(|v| v.as_bool()) == Some(false) {
        return "idle".into();
    }
    desired
}

fn apply_phone_update(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    attach_name: &str,
    base_url: &str,
    desired_state: &str,
    health_json: &str,
    last_error: &str,
    now: &str,
) {
    let ticket = ensure_ticket(ctx, ticket_id, "", now);
    let backend_id = clean_backend_id(backend_id);
    let attach_name = non_empty(attach_name, &backend_id);
    let desired_state = non_empty(desired_state, "idle");
    let stream_state = compact_phone_stream_state(&desired_state, health_json);
    let id = phone_row_id(&ticket.id, &backend_id);
    let existing = ctx.db.ticketremote_phone_backend().id().find(&id);
    let keepalive_due = existing
        .as_ref()
        .map(|row| {
            parse_time_ms(now).saturating_sub(parse_time_ms(&row.lastSeenAt)) >= PHONE_KEEPALIVE_MS
        })
        .unwrap_or(true);
    let unchanged = existing
        .as_ref()
        .map(|row| {
            row.attachName == attach_name
                && row.baseUrl == base_url.trim()
                && row.desiredState == desired_state
                && row.streamState == stream_state
                && row.healthJson == health_json
                && row.lastError == last_error
        })
        .unwrap_or(false);
    if !unchanged || keepalive_due {
        if existing.is_some() {
            ctx.db.ticketremote_phone_backend().id().delete(&id);
        }
        let row = TicketremotePhoneBackend {
            id,
            ticketId: ticket.id.clone(),
            backendId: backend_id.clone(),
            attachName: attach_name.clone(),
            baseUrl: base_url.trim().into(),
            desiredState: desired_state.clone(),
            streamState: stream_state.clone(),
            healthJson: health_json.into(),
            lastError: last_error.into(),
            lastSeenAt: now.into(),
        };
        ctx.db.ticketremote_phone_backend().insert(row.clone());
        upsert_service_phone_projection(ctx, &row);
    }
}

fn upsert_stream_desired_state(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    desired_active: bool,
    viewer_count: u32,
    reason: &str,
    revision: &str,
    updated_by: &str,
    now: &str,
) -> TicketremoteStreamDesiredState {
    let ticket = ensure_ticket(ctx, ticket_id, "", now);
    let backend_id = clean_backend_id(backend_id);
    let id = phone_row_id(&ticket.id, &backend_id);
    let row = TicketremoteStreamDesiredState {
        id: id.clone(),
        ticketId: ticket.id,
        backendId: backend_id,
        desiredActive: desired_active,
        viewerCount: viewer_count,
        reason: bounded_text(reason, 240),
        revision: clean_token(revision, now),
        updatedBy: bounded_text(updated_by, 120),
        updatedAt: now.into(),
    };
    if let Some(existing) = ctx.db.ticketremote_stream_desired_state().id().find(&id) {
        if existing.desiredActive == row.desiredActive
            && existing.viewerCount == row.viewerCount
            && existing.reason == row.reason
            && existing.revision == row.revision
            && existing.updatedBy == row.updatedBy
        {
            return existing;
        }
        ctx.db.ticketremote_stream_desired_state().id().delete(&id);
    }
    ctx.db
        .ticketremote_stream_desired_state()
        .insert(row.clone());
    upsert_stream_command_signal(ctx, &row.ticketId, &row.backendId, &row.revision, now);
    row
}

fn upsert_stream_command_signal(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    revision: &str,
    now: &str,
) {
    let id = phone_row_id(ticket_id, backend_id);
    let now_ms = parse_time_ms(now);
    let pending_count = ctx
        .db
        .ticketremote_stream_command()
        .ticketBackendStatus()
        .filter((ticket_id, backend_id, "pending"))
        .filter(|row| parse_time_ms(&row.expiresAt) > now_ms)
        .count() as u32;
    if ctx
        .db
        .ticketremote_stream_command_signal()
        .id()
        .find(&id)
        .is_some()
    {
        ctx.db.ticketremote_stream_command_signal().id().delete(&id);
    }
    ctx.db
        .ticketremote_stream_command_signal()
        .insert(TicketremoteStreamCommandSignal {
            id,
            ticketId: clean_ticket_id(ticket_id),
            backendId: clean_backend_id(backend_id),
            revision: clean_token(revision, now),
            pendingCount: pending_count,
            updatedAt: now.into(),
        });
}

fn insert_stream_command(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    command_id: &str,
    command_type: &str,
    revision: &str,
    reason: &str,
    payload_json: &str,
    ttl_ms: i64,
    now: &str,
) -> TicketremoteStreamCommand {
    let ticket = ensure_ticket(ctx, ticket_id, "", now);
    let backend_id = clean_backend_id(backend_id);
    let command_type = clean_token(command_type, "unknown").replace(
        |c: char| !c.is_ascii_alphanumeric() && c != '_' && c != '-',
        "_",
    );
    if matches!(command_type.as_str(), "keyframe" | "recover_stream") {
        let now_ms = parse_time_ms(now);
        if let Some(existing) = ctx
            .db
            .ticketremote_stream_command()
            .ticketBackendStatus()
            .filter((&ticket.id, &backend_id, "pending"))
            .find(|row| row.commandType == command_type && parse_time_ms(&row.expiresAt) > now_ms)
        {
            upsert_stream_command_signal(ctx, &ticket.id, &backend_id, &existing.revision, now);
            return existing;
        }
    }
    let revision = clean_token(revision, now);
    let id = non_empty(
        command_id,
        &format!("{}:{}:{}:{}", ticket.id, backend_id, revision, command_type),
    );
    if let Some(existing) = ctx.db.ticketremote_stream_command().id().find(&id) {
        upsert_stream_command_signal(ctx, &ticket.id, &backend_id, &existing.revision, now);
        return existing;
    }
    let row = TicketremoteStreamCommand {
        id,
        ticketId: ticket.id.clone(),
        backendId: backend_id.clone(),
        commandType: command_type,
        status: "pending".into(),
        revision: revision.clone(),
        reason: bounded_text(reason, 240),
        payloadJson: safe_json_string(payload_json, SAFE_JSON_MAX_BYTES),
        createdAt: now.into(),
        updatedAt: now.into(),
        expiresAt: command_expires_at(now, ttl_ms),
    };
    ctx.db.ticketremote_stream_command().insert(row.clone());
    upsert_service_command_projection(ctx, &row);
    upsert_stream_command_signal(ctx, &ticket.id, &backend_id, &revision, now);
    row
}

fn update_stream_command_status(
    ctx: &ReducerContext,
    command_id: &str,
    status: &str,
    reason: &str,
    now: &str,
) {
    let command_key = command_id.trim().to_string();
    let Some(existing) = ctx.db.ticketremote_stream_command().id().find(&command_key) else {
        return;
    };
    let status = clean_token(status, "acknowledged").replace(
        |c: char| !c.is_ascii_alphanumeric() && c != '_' && c != '-',
        "_",
    );
    ctx.db
        .ticketremote_stream_command()
        .id()
        .delete(&existing.id);
    delete_service_command_projection(ctx, &existing.id);
    if status == "acknowledged" || status == "dispatched" {
        upsert_stream_command_signal(
            ctx,
            &existing.ticketId,
            &existing.backendId,
            &existing.revision,
            now,
        );
        return;
    }
    let row = TicketremoteStreamCommand {
        status,
        reason: bounded_text(&non_empty(reason, &existing.reason), 240),
        payloadJson: "{}".into(),
        updatedAt: now.into(),
        expiresAt: command_expires_at(now, CONTROL_CODE_COMMAND_TTL_MS),
        ..existing.clone()
    };
    ctx.db.ticketremote_stream_command().insert(row.clone());
    upsert_service_command_projection(ctx, &row);
    upsert_stream_command_signal(
        ctx,
        &existing.ticketId,
        &existing.backendId,
        &existing.revision,
        now,
    );
}

fn upsert_phone_current_report(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    stream_state: &str,
    desired_active: bool,
    last_command_id: &str,
    last_command_revision: &str,
    status_json: &str,
    now: &str,
) {
    let ticket = ensure_ticket(ctx, ticket_id, "", now);
    let backend_id = clean_backend_id(backend_id);
    let id = phone_row_id(&ticket.id, &backend_id);
    let row = TicketremotePhoneCurrentReport {
        id: id.clone(),
        ticketId: ticket.id,
        backendId: backend_id,
        streamState: clean_token(stream_state, "idle"),
        desiredActive: desired_active,
        lastCommandId: last_command_id.trim().into(),
        lastCommandRevision: last_command_revision.trim().into(),
        statusJson: safe_json_string(status_json, SAFE_JSON_MAX_BYTES),
        updatedAt: now.into(),
    };
    if let Some(existing) = ctx.db.ticketremote_phone_current_report().id().find(&id) {
        if existing.streamState == row.streamState
            && existing.desiredActive == row.desiredActive
            && existing.lastCommandId == row.lastCommandId
            && existing.lastCommandRevision == row.lastCommandRevision
            && existing.statusJson == row.statusJson
        {
            return;
        }
        ctx.db.ticketremote_phone_current_report().id().delete(&id);
    }
    ctx.db.ticketremote_phone_current_report().insert(row);
}

fn upsert_relay_current_report(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    video_clients: u32,
    stream_verdict: &str,
    last_frame_at: &str,
    frames_forwarded: &str,
    status_json: &str,
    now: &str,
) {
    let ticket = ensure_ticket(ctx, ticket_id, "", now);
    let backend_id = clean_backend_id(backend_id);
    let id = phone_row_id(&ticket.id, &backend_id);
    let row = TicketremoteRelayCurrentReport {
        id: id.clone(),
        ticketId: ticket.id,
        backendId: backend_id,
        videoClients: video_clients,
        streamVerdict: clean_token(stream_verdict, "unknown").replace(
            |c: char| !c.is_ascii_alphanumeric() && c != '_' && c != '-',
            "_",
        ),
        lastFrameAgoMillis: 0,
        framesForwarded: non_empty(frames_forwarded, "0"),
        statusJson: safe_json_string(status_json, SAFE_JSON_MAX_BYTES),
        updatedAt: now.into(),
        lastFrameAt: Some(bounded_text(last_frame_at.trim(), 80)),
    };
    if let Some(existing) = ctx.db.ticketremote_relay_current_report().id().find(&id) {
        if existing.videoClients == row.videoClients
            && existing.streamVerdict == row.streamVerdict
            && existing.lastFrameAgoMillis == row.lastFrameAgoMillis
            && existing.lastFrameAt == row.lastFrameAt
            && existing.framesForwarded == row.framesForwarded
            && existing.statusJson == row.statusJson
        {
            return;
        }
        ctx.db.ticketremote_relay_current_report().id().delete(&id);
    }
    ctx.db.ticketremote_relay_current_report().insert(row);
}

fn delete_control_code_request(ctx: &ReducerContext, request_id: &str) {
    let id = request_id.to_string();
    ctx.db.ticketremote_control_code_request().id().delete(&id);
    ctx.db.ticketremote_control_code_owner().id().delete(&id);
}

fn active_control_code_owner_rows(
    ctx: &ReducerContext,
    ticket_id: &str,
    email: &str,
    now: &str,
) -> Vec<TicketremoteControlCodeOwner> {
    let cutoff = parse_time_ms(now).saturating_sub(CONTROL_CODE_RATE_WINDOW_MS);
    ctx.db
        .ticketremote_control_code_owner()
        .ticketId()
        .filter(ticket_id)
        .filter(|row| {
            clean_email(&row.email) == clean_email(email)
                && parse_time_ms(&row.requestedAt) >= cutoff
        })
        .collect()
}

fn insert_control_code_public_request(
    ctx: &ReducerContext,
    ticket_id: &str,
    request_id: &str,
    owner_public_id: &str,
    now: &str,
) -> TicketremoteControlCodeRequest {
    let row = TicketremoteControlCodeRequest {
        id: request_id.into(),
        ticketId: clean_ticket_id(ticket_id),
        ownerPublicId: owner_public_id.into(),
        status: "queued".into(),
        reason: "requested".into(),
        message: String::new(),
        requestedAt: now.into(),
        updatedAt: now.into(),
        resultExpiresAt: String::new(),
        resultProof: None,
        resultProofAt: None,
        captureRequired: false,
        captureAcknowledged: false,
        cleanupPending: false,
        streamEpoch: "0".into(),
        frameSequence: "0".into(),
        minFrameSequence: "0".into(),
        resultFrameEpoch: "0".into(),
        resultMinFrameSequence: "0".into(),
        captureFrameEpoch: "0".into(),
        captureFrameSequence: "0".into(),
        expiresAt: control_code_request_expires_at(now),
    };
    ctx.db
        .ticketremote_control_code_request()
        .insert(row.clone());
    row
}

#[derive(Default)]
struct ControlCodeChanges {
    status: Option<String>,
    reason: Option<String>,
    message: Option<String>,
    resultExpiresAt: Option<String>,
    resultProof: Option<String>,
    resultProofAt: Option<String>,
    captureRequired: Option<bool>,
    captureAcknowledged: Option<bool>,
    cleanupPending: Option<bool>,
    streamEpoch: Option<String>,
    frameSequence: Option<String>,
    minFrameSequence: Option<String>,
    resultFrameEpoch: Option<String>,
    resultMinFrameSequence: Option<String>,
    captureFrameEpoch: Option<String>,
    captureFrameSequence: Option<String>,
    expiresAt: Option<String>,
}

fn update_control_code_public_request(
    ctx: &ReducerContext,
    request_id: &str,
    changes: ControlCodeChanges,
    now: &str,
) {
    let request_key = request_id.to_string();
    let Some(existing) = ctx
        .db
        .ticketremote_control_code_request()
        .id()
        .find(&request_key)
    else {
        return;
    };
    ctx.db
        .ticketremote_control_code_request()
        .id()
        .delete(&request_key);
    let row = TicketremoteControlCodeRequest {
        status: changes.status.unwrap_or(existing.status),
        reason: changes.reason.unwrap_or(existing.reason),
        message: changes.message.unwrap_or(existing.message),
        resultExpiresAt: changes.resultExpiresAt.unwrap_or(existing.resultExpiresAt),
        resultProof: changes
            .resultProof
            .map(Some)
            .unwrap_or(existing.resultProof),
        resultProofAt: changes
            .resultProofAt
            .map(Some)
            .unwrap_or(existing.resultProofAt),
        captureRequired: changes.captureRequired.unwrap_or(existing.captureRequired),
        captureAcknowledged: changes
            .captureAcknowledged
            .unwrap_or(existing.captureAcknowledged),
        cleanupPending: changes.cleanupPending.unwrap_or(existing.cleanupPending),
        streamEpoch: changes.streamEpoch.unwrap_or(existing.streamEpoch),
        frameSequence: changes.frameSequence.unwrap_or(existing.frameSequence),
        minFrameSequence: changes
            .minFrameSequence
            .unwrap_or(existing.minFrameSequence),
        resultFrameEpoch: changes
            .resultFrameEpoch
            .unwrap_or(existing.resultFrameEpoch),
        resultMinFrameSequence: changes
            .resultMinFrameSequence
            .unwrap_or(existing.resultMinFrameSequence),
        captureFrameEpoch: changes
            .captureFrameEpoch
            .unwrap_or(existing.captureFrameEpoch),
        captureFrameSequence: changes
            .captureFrameSequence
            .unwrap_or(existing.captureFrameSequence),
        expiresAt: changes.expiresAt.unwrap_or(existing.expiresAt),
        updatedAt: now.into(),
        ..existing
    };
    ctx.db
        .ticketremote_control_code_request()
        .insert(row.clone());
}

fn insert_safe_operational_log(
    ctx: &ReducerContext,
    ticket_id: &str,
    source: &str,
    level: &str,
    event: &str,
    correlation_id: &str,
    detail_json: &str,
    source_id: &str,
    now: &str,
) {
    let ticket = ensure_ticket(ctx, ticket_id, "", now);
    let source = clean_token(source, "unknown").replace(
        |c: char| !c.is_ascii_alphanumeric() && c != '_' && c != '-',
        "_",
    );
    let event = clean_token(event, "event").replace(
        |c: char| !c.is_ascii_alphanumeric() && c != '_' && c != '-',
        "_",
    );
    let level = clean_token(level, "info").replace(
        |c: char| !c.is_ascii_alphanumeric() && c != '_' && c != '-',
        "_",
    );
    let correlation_id = bounded_text(correlation_id, 160);
    ctx.db
        .ticketremote_safe_operational_log()
        .insert(TicketremoteSafeOperationalLog {
            id: safe_log_row_id(
                &ticket.id,
                &source,
                &event,
                &correlation_id,
                detail_json,
                source_id,
                now,
            ),
            ticketId: ticket.id,
            source,
            level,
            event,
            correlationId: correlation_id,
            detailJson: safe_json_string(detail_json, SAFE_LOG_DETAIL_MAX_BYTES),
            createdAt: now.into(),
            expiresAt: history_expires_at(now),
        });
}

fn safe_log_row_id(
    ticket_id: &str,
    source: &str,
    event: &str,
    correlation_id: &str,
    detail_json: &str,
    source_id: &str,
    now: &str,
) -> String {
    let explicit = source_id.trim();
    if !explicit.is_empty() {
        return bounded_text(explicit, 220);
    }
    format!(
        "{}:{}:{}:{}:{}:{}",
        clean_ticket_id(ticket_id),
        stable_stamp(now),
        source,
        event,
        bounded_text(correlation_id, 40),
        to_base36(fnv32(detail_json))
    )
}

fn fnv32(value: &str) -> u32 {
    let mut hash: u32 = 0x811c9dc5;
    for byte in value.as_bytes() {
        hash ^= *byte as u32;
        hash = hash.wrapping_mul(0x01000193);
    }
    hash
}

fn cleanup_limit_reached(deleted: u32, limit: u32) -> bool {
    limit > 0 && deleted >= limit
}

fn cleanup_remaining(limit: u32, deleted: u32) -> u32 {
    if limit == 0 {
        0
    } else {
        limit.saturating_sub(deleted)
    }
}

fn history_expired(created_at: &str, expires_at: &str, now_ms: i64) -> bool {
    parse_time_ms(expires_at) <= now_ms
        || parse_time_ms(created_at).saturating_add(HISTORY_TTL_MS) <= now_ms
}

fn cleanup_expired(ctx: &ReducerContext, ticket_id: &str, now: &str, batch_size: u32) -> u32 {
    let ticket = ensure_ticket(ctx, ticket_id, "", now);
    let now_ms = parse_time_ms(now);
    let limit = if batch_size == 0 {
        CLEANUP_BATCH_SIZE
    } else {
        batch_size.min(CLEANUP_BATCH_SIZE)
    };
    let mut deleted = 0u32;
    let mut control_code_request_deleted = 0u32;
    let mut control_code_owner_deleted = 0u32;
    let mut safe_log_deleted = 0u32;

    if !cleanup_limit_reached(deleted, limit) {
        let stream_command_deleted = purge_expired_stream_commands_for_ticket(
            ctx,
            &ticket.id,
            now,
            cleanup_remaining(limit, deleted),
        );
        deleted += stream_command_deleted;
    }
    let request_rows: Vec<_> = ctx
        .db
        .ticketremote_control_code_request()
        .ticketId()
        .filter(&ticket.id)
        .collect();
    for row in request_rows {
        if cleanup_limit_reached(deleted, limit) {
            break;
        }
        if parse_time_ms(&row.expiresAt) <= now_ms {
            let owner_present = ctx
                .db
                .ticketremote_control_code_owner()
                .id()
                .find(&row.id)
                .is_some();
            delete_control_code_request(ctx, &row.id);
            deleted += 1;
            control_code_request_deleted += 1;
            if owner_present {
                deleted += 1;
                control_code_owner_deleted += 1;
            }
        }
    }
    let owner_rows: Vec<_> = ctx
        .db
        .ticketremote_control_code_owner()
        .ticketId()
        .filter(&ticket.id)
        .collect();
    for row in owner_rows {
        if cleanup_limit_reached(deleted, limit) {
            break;
        }
        if parse_time_ms(&row.expiresAt) <= now_ms {
            let request_present = ctx
                .db
                .ticketremote_control_code_request()
                .id()
                .find(&row.id)
                .is_some();
            delete_control_code_request(ctx, &row.id);
            deleted += 1;
            control_code_owner_deleted += 1;
            if request_present {
                deleted += 1;
                control_code_request_deleted += 1;
            }
        }
    }
    let log_rows: Vec<_> = ctx
        .db
        .ticketremote_safe_operational_log()
        .ticketId()
        .filter(&ticket.id)
        .collect();
    for row in log_rows {
        if cleanup_limit_reached(deleted, limit) {
            break;
        }
        if history_expired(&row.createdAt, &row.expiresAt, now_ms) {
            ctx.db
                .ticketremote_safe_operational_log()
                .id()
                .delete(&row.id);
            deleted += 1;
            safe_log_deleted += 1;
        }
    }
    let _ = control_code_request_deleted;
    let _ = control_code_owner_deleted;
    let _ = safe_log_deleted;
    deleted
}

fn purge_expired_stream_commands_for_ticket(
    ctx: &ReducerContext,
    ticket_id: &str,
    now: &str,
    batch_size: u32,
) -> u32 {
    let now_ms = parse_time_ms(now);
    let mut deleted = 0u32;
    let limit = batch_size;
    let mut touched: Vec<String> = Vec::new();
    for status in ["acknowledged", "dispatched", "failed", "expired", "pending"] {
        let rows: Vec<_> = ctx
            .db
            .ticketremote_stream_command()
            .status()
            .filter(status)
            .collect();
        for row in rows {
            if cleanup_limit_reached(deleted, limit) {
                refresh_touched_signals(ctx, ticket_id, &touched, now);
                return deleted;
            }
            if row.ticketId == ticket_id && parse_time_ms(&row.expiresAt) <= now_ms {
                if !touched.iter().any(|id| id == &row.backendId) {
                    touched.push(row.backendId.clone());
                }
                ctx.db.ticketremote_stream_command().id().delete(&row.id);
                delete_service_command_projection(ctx, &row.id);
                deleted += 1;
            }
        }
    }
    refresh_touched_signals(ctx, ticket_id, &touched, now);
    deleted
}

fn refresh_touched_signals(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_ids: &[String],
    now: &str,
) {
    for backend_id in backend_ids {
        upsert_stream_command_signal(ctx, ticket_id, backend_id, now, now);
    }
}
