#![allow(non_snake_case)]
// Reducer parameters are the stable public SpacetimeDB contract, so grouping
// them solely to satisfy Clippy would break generated clients.
#![allow(clippy::too_many_arguments)]

use chrono::{DateTime, Utc};
use spacetimedb::{
    CaseConversionPolicy, Identity, ReducerContext, ScheduleAt, SpacetimeType, Table, Timestamp,
    ViewContext,
};

const DEFAULT_TICKET_ID: &str = "vivi-default";
const DEFAULT_TICKET_NAME: &str = "ViVi timed ticket";
// Service reducers are reachable directly through SpacetimeDB, so the role
// claim is not an authorization boundary by itself. Pin the complete runtime
// identity contract used by kitty-gration's ticket sidecar.
const SERVICE_OIDC_ISSUER: &str = "https://vilciens.kontrole.info/oidc";
const SERVICE_OIDC_AUDIENCE: &str = "train-bot-web";
const SERVICE_OIDC_SUBJECT: &str = "service:ticket-remote";
const SERVICE_ROLE: &str = "ticketremote_service";
// The database owner may connect for read-only operational SQL. Reducer writes
// still require member or service authorization below.
const OPERATOR_IDENTITY: &str = "c200ba2b19cf478fbb75ce99bd969ebe47cb313909a7ebf4d5f19c6bf3e325f9";
#[spacetimedb::settings]
const CASE_CONVERSION_POLICY: CaseConversionPolicy = CaseConversionPolicy::None;
const HISTORY_TTL_MS: i64 = 6 * 60 * 60 * 1000;
const CLEANUP_BATCH_SIZE: u32 = 500;
// Keep cleanup cheap by using the expiry indexes below, but run often enough to
// drain a burst without violating the six-hour retention contract for days.
const CLEANUP_INTERVAL_SECS: u64 = 60;
const PHONE_KEEPALIVE_MS: i64 = 60_000;
const CONTROL_CODE_RATE_LIMIT: usize = 2;
const CONTROL_CODE_RATE_WINDOW_MS: i64 = 60_000;
const CONTROL_CODE_REQUEST_TTL_MS: i64 = 5 * 60_000;
const CONTROL_CODE_RESULT_TTL_MS: i64 = 60_000;
const CONTROL_CODE_COMMAND_TTL_MS: i64 = 2 * 60_000;
const CONTROL_CODE_PHONE_TTL_MS: i64 = 105_000;
const CONTROL_CODE_FAST_READY_TTL_MS: i64 = 12_000;
const CONTROL_CODE_FAST_STATE_TTL_MS: i64 = 30_000;
const STREAM_VIEWER_FOCUS_TTL_MS: i64 = 90_000;
const SAFE_JSON_MAX_BYTES: usize = 4096;
const SAFE_LOG_DETAIL_MAX_BYTES: usize = 1024;
const STREAM_BACKGROUND_SUPPRESS_FALLBACK_MAX_AGE_MS: i64 = 2_500;
const STREAM_BACKGROUND_REPORT_MAX_AGE_MS: i64 = 5_000;

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

#[spacetimedb::table(accessor = ticketremote_stream_viewer_focus, public,
    index(accessor = ticketBackend, btree(columns = [ticketId, backendId])),
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))
)]
#[derive(Clone)]
pub struct TicketremoteStreamViewerFocus {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub backendId: String,
    #[index(btree)]
    pub publicId: String,
    pub active: bool,
    pub lastSeenAt: String,
    pub expiresAt: String,
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

#[spacetimedb::table(accessor = ticketremote_control_code_fast_state, public,
    index(accessor = ticketBackend, btree(columns = [ticketId, backendId])),
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))
)]
#[derive(Clone)]
pub struct TicketremoteControlCodeFastState {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub backendId: String,
    #[index(btree)]
    pub status: String,
    pub revision: String,
    pub reason: String,
    pub preparedAt: String,
    #[index(btree)]
    pub expiresAt: String,
    pub streamEpoch: String,
    pub frameSequence: String,
    pub rawTicketConfirmed: bool,
    pub cleanupClear: bool,
    pub streamLive: bool,
    #[index(btree)]
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
pub fn identity_connected(ctx: &ReducerContext) -> Result<(), String> {
    if has_valid_service_identity(ctx) || operator_identity_is_valid(&ctx.sender().to_string()) {
        return Ok(());
    }
    client_email_from_auth(ctx, DEFAULT_TICKET_ID)?;
    Ok(())
}

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
    let email = client_email_from_auth(ctx, &ticket.id)?;
    let session_id = non_empty(&sessionId, &connection_session_id(ctx));
    let backend_id = clean_backend_id(&backendId);
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
    upsert_stream_viewer_focus(
        ctx,
        &ticket.id,
        &backend_id,
        &session_id,
        &email,
        active,
        &now,
    );
    purge_expired_stream_viewer_focus_for_ticket_backend(ctx, &ticket.id, &backend_id, &now, 100);
    let viewers = active_stream_viewer_focus_count(ctx, &ticket.id, &backend_id, &now);
    if stream_desired_core_matches(ctx, &ticket.id, &backend_id, viewers > 0, viewers) {
        return Ok(());
    }
    upsert_stream_desired_state(
        ctx,
        &ticket.id,
        &backend_id,
        viewers > 0,
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
    let backend_id = clean_backend_id(&backendId);
    let command_reason = non_empty(&reason, "browser_request");
    // A shared relay report cannot prove that this requester's decoder is
    // healthy. Respect the authenticated browser's local stale-frame decision;
    // insert_stream_command still coalesces an already-pending global keyframe.
    insert_stream_command(
        ctx,
        &ticket.id,
        &backend_id,
        &format!("{}:browser:{}:keyframe", ticket.id, stable_stamp(&now)),
        "keyframe",
        &now,
        &bounded_text(&command_reason, 120),
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
    let backend_id = clean_backend_id(&backendId);
    let command_reason = non_empty(&reason, "browser_recovery");
    // As above, relay-wide liveness must not suppress recovery for one stale
    // requester while another viewer remains healthy.
    insert_stream_command(
        ctx,
        &ticket.id,
        &backend_id,
        &format!(
            "{}:browser:{}:recover_stream",
            ticket.id,
            stable_stamp(&now)
        ),
        "recover_stream",
        &now,
        &bounded_text(&command_reason, 120),
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
    let backend_id = clean_backend_id(&backendId);
    if control_code_fast_state_current_ready(ctx, &ticket.id, &backend_id, &now) {
        return Ok(());
    }
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
        &backend_id,
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
    expectedFastRevision: String,
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
    if ticket_has_control_code_request_in_progress(ctx, &ticket.id, &now) {
        return Err("request_in_progress".into());
    }
    if active_control_code_owner_rows(ctx, &ticket.id, &email, &now).len()
        >= CONTROL_CODE_RATE_LIMIT
    {
        return Err("rate_limited".into());
    }
    let backend_id = clean_backend_id(&backendId);
    let fast_state_id = control_code_fast_state_id(&ticket.id, &backend_id);
    let fast_state = ctx
        .db
        .ticketremote_control_code_fast_state()
        .id()
        .find(&fast_state_id);
    let fast_state_ready = fast_state
        .as_ref()
        .map(|row| control_code_fast_state_row_ready(row, &expectedFastRevision, &now))
        .unwrap_or(false);
    let fast_state_status = fast_state
        .as_ref()
        .map(|row| row.status.clone())
        .unwrap_or_else(|| "missing".into());
    let submit_mode = control_code_submit_mode(fast_state_ready);
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
        "requester": owner_public_id.clone(),
        "serverSentAt": now,
        "dispatchAttempt": 1,
        "fastRevision": bounded_text(&expectedFastRevision, 160),
        "fastStateStatusAtSubmit": fast_state_status.clone(),
        "fastStateReadyAtSubmit": fast_state_ready,
        "submitMode": submit_mode
    })
    .to_string();
    insert_stream_command(
        ctx,
        &ticket.id,
        &backend_id,
        &format!("{}:generate_control_code", request_id),
        "generate_control_code",
        &now,
        "control_code_request",
        &payload,
        CONTROL_CODE_PHONE_TTL_MS,
        &now,
    );
    let cleanup_revision = fast_state
        .as_ref()
        .map(|row| row.revision.clone())
        .unwrap_or_else(|| now.clone());
    let cleanup_stream_epoch = fast_state
        .as_ref()
        .map(|row| row.streamEpoch.clone())
        .unwrap_or_default();
    let cleanup_frame_sequence = fast_state
        .as_ref()
        .map(|row| row.frameSequence.clone())
        .unwrap_or_default();
    let stream_was_live = fast_state
        .as_ref()
        .map(|row| row.streamLive)
        .unwrap_or(false);
    upsert_control_code_fast_state(
        ctx,
        &ticket.id,
        &backend_id,
        "cleanup",
        &cleanup_revision,
        "control_code_request",
        &cleanup_stream_epoch,
        &cleanup_frame_sequence,
        false,
        false,
        stream_was_live,
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
            .find(member_id(&ticket.id, &email))
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
    if !ctx.sender_auth().is_internal() && !has_valid_service_identity(ctx) {
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
    let command_type = clean_token(&commandType, "command");
    let command_reason = non_empty(&reason, "stream_command");
    if command_type == "prepare_control_code"
        && control_code_fast_state_current_ready(ctx, &ticketId, &backendId, &now)
    {
        return Ok(());
    }
    if suppressible_background_stream_command(&command_type)
        && authoritative_stream_is_idle(ctx, &ticketId, &backendId, &now)
        && !idle_stream_command_is_allowed(&command_reason, &payloadJson)
    {
        return Ok(());
    }
    if suppressible_background_stream_command(&command_type)
        && !stream_command_is_requester_scoped(&command_reason, &payloadJson)
        && live_relay_suppresses_background_stream_command(
            ctx,
            &ticketId,
            &backendId,
            &command_reason,
            &now,
        )
    {
        return Ok(());
    }
    let row = insert_stream_command(
        ctx,
        &ticketId,
        &backendId,
        &commandId,
        &command_type,
        &revision,
        &command_reason,
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
pub fn ticketremote_update_control_code_fast_state(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    status: String,
    revision: String,
    reason: String,
    streamEpoch: String,
    frameSequence: String,
    rawTicketConfirmed: bool,
    cleanupClear: bool,
    streamLive: bool,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let now = now_or(ctx, &nowArg);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    upsert_control_code_fast_state(
        ctx,
        &ticket.id,
        &backendId,
        &status,
        &revision,
        &reason,
        &streamEpoch,
        &frameSequence,
        rawTicketConfirmed,
        cleanupClear,
        streamLive,
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
    if succeeded || terminal_failure {
        update_stream_command_status(
            ctx,
            &format!("{}:generate_control_code", requestId.trim()),
            "acknowledged",
            "terminal_request_published",
            &now,
        );
    }
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
pub fn ticketremote_purge_sensitive_operational_logs(
    ctx: &ReducerContext,
    ticketId: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let ticket_id = clean_ticket_id(&ticketId);
    let rows: Vec<_> = ctx
        .db
        .ticketremote_safe_operational_log()
        .ticketId()
        .filter(&ticket_id)
        .filter(|row| {
            matches!(
                row.event.as_str(),
                "pixel_ticket_control_code_result"
                    | "pixel_ticket_control_code_request_result_detected"
            )
        })
        .collect();
    for row in rows {
        ctx.db
            .ticketremote_safe_operational_log()
            .id()
            .delete(&row.id);
    }
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

fn canonical_time(value: &str) -> String {
    iso(Timestamp::from_micros_since_unix_epoch(parse_time_micros(
        value,
    )))
}

fn live_relay_suppresses_background_stream_command(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    reason: &str,
    now: &str,
) -> bool {
    let clean_reason = reason.trim().to_ascii_lowercase();
    if clean_reason.contains("control_code") {
        return false;
    }
    let id = phone_row_id(ticket_id, backend_id);
    if relay_current_report_suppresses_background_stream_command(ctx, &id, now) {
        return true;
    }
    phone_current_report_suppresses_background_stream_command(ctx, &id, now)
}

fn suppressible_background_stream_command(command_type: &str) -> bool {
    matches!(command_type.trim(), "start" | "keyframe" | "recover_stream")
}

fn stream_command_is_requester_scoped(reason: &str, payload_json: &str) -> bool {
    let reason = reason.trim().to_ascii_lowercase();
    if reason.contains("browser")
        || reason.contains("visibility")
        || reason.contains("decoder")
        || reason.contains("first_frame")
        || reason.contains("stale_frame")
    {
        return true;
    }
    serde_json::from_str::<serde_json::Value>(payload_json)
        .ok()
        .and_then(|payload| {
            payload
                .get("source")
                .and_then(|value| value.as_str())
                .map(str::to_owned)
        })
        .map(|source| source.trim().to_ascii_lowercase().contains("browser"))
        .unwrap_or(false)
}

fn stream_command_is_control_code(reason: &str, payload_json: &str) -> bool {
    if reason.trim().to_ascii_lowercase().contains("control_code") {
        return true;
    }
    serde_json::from_str::<serde_json::Value>(payload_json)
        .ok()
        .map(|payload| {
            ["type", "flow", "reason"].iter().any(|key| {
                payload
                    .get(key)
                    .and_then(|value| value.as_str())
                    .map(|value| value.trim().to_ascii_lowercase().contains("control_code"))
                    .unwrap_or(false)
            })
        })
        .unwrap_or(false)
}

fn idle_stream_command_is_allowed(reason: &str, payload_json: &str) -> bool {
    if stream_command_is_control_code(reason, payload_json) {
        return true;
    }
    let reason = reason.trim().to_ascii_lowercase();
    matches!(reason.as_str(), "stream_prewarm" | "index_auth_prewarm")
        || reason.contains("video_socket_open")
        || reason.contains("video_socket_adopted")
        || reason.contains("viewer_added")
        || reason.contains("public_connected")
}

fn authoritative_stream_is_idle(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    now: &str,
) -> bool {
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    let id = phone_row_id(&ticket_id, &backend_id);
    let Some(desired) = ctx.db.ticketremote_stream_desired_state().id().find(&id) else {
        return false;
    };
    !desired.desiredActive
        && desired.viewerCount == 0
        && active_stream_viewer_focus_count(ctx, &ticket_id, &backend_id, now) == 0
}

fn relay_current_report_suppresses_background_stream_command(
    ctx: &ReducerContext,
    id: &String,
    now: &str,
) -> bool {
    let Some(report) = ctx.db.ticketremote_relay_current_report().id().find(id) else {
        return false;
    };
    if report.videoClients == 0 || report.streamVerdict != "live" {
        return false;
    }
    let report_age_ms = parse_time_ms(now).saturating_sub(parse_time_ms(&report.updatedAt));
    if !(0..=STREAM_BACKGROUND_REPORT_MAX_AGE_MS).contains(&report_age_ms) {
        return false;
    }
    let Ok(status) = serde_json::from_str::<serde_json::Value>(&report.statusJson) else {
        return report_age_ms <= STREAM_BACKGROUND_SUPPRESS_FALLBACK_MAX_AGE_MS;
    };
    let live = status
        .get("live")
        .and_then(|value| value.as_bool())
        .unwrap_or(true);
    let active_clients = status
        .get("activeVideoClients")
        .and_then(json_i64)
        .unwrap_or(report.videoClients as i64);
    let visual_age = status
        .get("lastFrameVisualAgeMillis")
        .and_then(json_i64)
        .unwrap_or(0)
        .saturating_add(report_age_ms);
    let max_age = status
        .get("liveFrameMaxAgeMillis")
        .and_then(json_i64)
        .unwrap_or(STREAM_BACKGROUND_SUPPRESS_FALLBACK_MAX_AGE_MS);
    live && active_clients > 0 && visual_age >= 0 && visual_age <= max_age
}

fn phone_current_report_suppresses_background_stream_command(
    ctx: &ReducerContext,
    id: &String,
    now: &str,
) -> bool {
    let Some(report) = ctx.db.ticketremote_phone_current_report().id().find(id) else {
        return false;
    };
    let stream_state = report.streamState.trim();
    if !report.desiredActive || (stream_state != "streaming" && stream_state != "live") {
        return false;
    }
    let report_age_ms = parse_time_ms(now).saturating_sub(parse_time_ms(&report.updatedAt));
    if !(0..=STREAM_BACKGROUND_REPORT_MAX_AGE_MS).contains(&report_age_ms) {
        return false;
    }
    let Ok(status) = serde_json::from_str::<serde_json::Value>(&report.statusJson) else {
        return false;
    };
    let stream_active = status
        .get("streamActive")
        .and_then(|value| value.as_bool())
        .unwrap_or(true);
    if !stream_active {
        return false;
    }
    let session_state = json_str(&status, "sessionState");
    if !session_state.is_empty() && session_state != "live" && session_state != "streaming" {
        return false;
    }
    let relay_state = json_str(&status, "relayStreamState");
    if !relay_state.is_empty() && relay_state != "live" && relay_state != "streaming" {
        return false;
    }
    if status
        .get("hardwareH264Active")
        .and_then(|value| value.as_bool())
        == Some(false)
    {
        return false;
    }
    let hardware_visibility = json_str(&status, "hardwareH264Visibility");
    if !hardware_visibility.is_empty() && hardware_visibility != "visible" {
        return false;
    }
    let watchdog_stage = json_str(&status, "streamWatchdogStage");
    if watchdog_stage.contains("recover")
        || watchdog_stage.contains("restart")
        || watchdog_stage.contains("fail")
    {
        return false;
    }
    let active_clients = status
        .get("activeVideoClients")
        .and_then(json_i64)
        .or_else(|| status.get("videoClients").and_then(json_i64))
        .or_else(|| status.get("relayViewers").and_then(json_i64))
        .unwrap_or(0);
    active_clients > 0
}

fn json_i64(value: &serde_json::Value) -> Option<i64> {
    value
        .as_i64()
        .or_else(|| value.as_u64().and_then(|raw| i64::try_from(raw).ok()))
        .or_else(|| {
            value
                .as_str()
                .and_then(|raw| raw.trim().parse::<i64>().ok())
        })
}

fn json_str(value: &serde_json::Value, key: &str) -> String {
    value
        .get(key)
        .and_then(|raw| raw.as_str())
        .map(|raw| raw.trim().to_ascii_lowercase())
        .unwrap_or_default()
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

fn stream_viewer_focus_expires_at(now: &str) -> String {
    add_ms(now, STREAM_VIEWER_FOCUS_TTL_MS)
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

fn service_claims_are_valid(payload: &serde_json::Value) -> bool {
    payload
        .get("iss")
        .and_then(|value| value.as_str())
        .map(str::trim)
        == Some(SERVICE_OIDC_ISSUER)
        && jwt_audience_includes(payload, SERVICE_OIDC_AUDIENCE)
        && payload
            .get("sub")
            .and_then(|value| value.as_str())
            .map(str::trim)
            == Some(SERVICE_OIDC_SUBJECT)
        && jwt_roles_include(payload, SERVICE_ROLE)
}

fn has_valid_service_identity(ctx: &ReducerContext) -> bool {
    jwt_payload(ctx)
        .map(|payload| service_claims_are_valid(&payload))
        .unwrap_or(false)
}

fn operator_identity_is_valid(identity: &str) -> bool {
    identity.trim() == OPERATOR_IDENTITY
}

fn require_service(ctx: &ReducerContext) -> Result<(), String> {
    if has_valid_service_identity(ctx) {
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
        .find(clean_ticket_id(ticket_id))
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

fn public_hash(value: &str, len: usize) -> String {
    let mut hash: u32 = 0x811c9dc5;
    for byte in value.trim().as_bytes() {
        hash ^= *byte as u32;
        hash = hash.wrapping_mul(0x01000193);
    }
    let mut out = to_base36(hash);
    if out.len() < len {
        out = format!("{:0>width$}", out, width = len);
    }
    out.chars().take(len).collect()
}

fn stream_viewer_focus_id(
    ticket_id: &str,
    backend_id: &str,
    public_id: &str,
    session_id: &str,
) -> String {
    let session_hash = public_hash(
        &format!(
            "{}:{}:{}",
            clean_ticket_id(ticket_id),
            clean_backend_id(backend_id),
            session_id.trim()
        ),
        8,
    );
    format!(
        "{}:{}:{}:{}",
        clean_ticket_id(ticket_id),
        clean_backend_id(backend_id),
        public_id.trim(),
        session_hash
    )
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
        .find(member_id(ticket_id, email))
        .map(|row| row.active)
        .unwrap_or(false)
}

fn is_admin(ctx: &ReducerContext, ticket_id: &str, email: &str) -> bool {
    ctx.db
        .ticketremote_ticket_member()
        .id()
        .find(member_id(ticket_id, email))
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
        if let Some(value) = data.get(key).and_then(|v| v.as_str())
            && !value.trim().is_empty()
        {
            return value.trim().into();
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

fn upsert_stream_viewer_focus(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    session_id: &str,
    email: &str,
    active: bool,
    now: &str,
) {
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    let public_id = account_public_id(email);
    let id = stream_viewer_focus_id(&ticket_id, &backend_id, &public_id, session_id);
    if !active {
        if ctx
            .db
            .ticketremote_stream_viewer_focus()
            .id()
            .find(&id)
            .is_some()
        {
            ctx.db.ticketremote_stream_viewer_focus().id().delete(&id);
        }
        return;
    }
    let row = TicketremoteStreamViewerFocus {
        id: id.clone(),
        ticketId: ticket_id,
        backendId: backend_id,
        publicId: public_id,
        active: true,
        lastSeenAt: now.into(),
        expiresAt: stream_viewer_focus_expires_at(now),
    };
    if ctx
        .db
        .ticketremote_stream_viewer_focus()
        .id()
        .find(&id)
        .is_some()
    {
        ctx.db.ticketremote_stream_viewer_focus().id().update(row);
    } else {
        ctx.db.ticketremote_stream_viewer_focus().insert(row);
    }
}

fn active_stream_viewer_focus_count(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    now: &str,
) -> u32 {
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    let now_ms = parse_time_ms(now);
    ctx.db
        .ticketremote_stream_viewer_focus()
        .ticketBackend()
        .filter((&ticket_id, &backend_id))
        .filter(|row| row.active && parse_time_ms(&row.expiresAt) > now_ms)
        .count()
        .min(u32::MAX as usize) as u32
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
    if !row.desiredActive && row.viewerCount == 0 {
        purge_pending_idle_background_commands(
            ctx,
            &row.ticketId,
            &row.backendId,
            &row.revision,
            now,
        );
    }
    if let Some(existing) = ctx.db.ticketremote_stream_desired_state().id().find(&id) {
        if existing.desiredActive == row.desiredActive
            && existing.viewerCount == row.viewerCount
            && existing.reason == row.reason
            && existing.revision == row.revision
            && existing.updatedBy == row.updatedBy
        {
            return existing;
        }
        ctx.db
            .ticketremote_stream_desired_state()
            .id()
            .update(row.clone());
        upsert_stream_command_signal(ctx, &row.ticketId, &row.backendId, &row.revision, now);
        return row;
    }
    ctx.db
        .ticketremote_stream_desired_state()
        .insert(row.clone());
    upsert_stream_command_signal(ctx, &row.ticketId, &row.backendId, &row.revision, now);
    row
}

fn stream_desired_core_matches(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    desired_active: bool,
    viewer_count: u32,
) -> bool {
    let id = phone_row_id(ticket_id, backend_id);
    ctx.db
        .ticketremote_stream_desired_state()
        .id()
        .find(&id)
        .map(|row| stream_desired_core_equal(&row, desired_active, viewer_count))
        .unwrap_or(false)
}

fn stream_desired_core_equal(
    row: &TicketremoteStreamDesiredState,
    desired_active: bool,
    viewer_count: u32,
) -> bool {
    row.desiredActive == desired_active && row.viewerCount == viewer_count
}

fn purge_pending_idle_background_commands(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    revision: &str,
    now: &str,
) -> u32 {
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    let rows: Vec<_> = ctx
        .db
        .ticketremote_stream_command()
        .ticketBackendStatus()
        .filter((&ticket_id, &backend_id, "pending"))
        .filter(|row| {
            suppressible_background_stream_command(&row.commandType)
                && !stream_command_is_control_code(&row.reason, &row.payloadJson)
        })
        .collect();
    for row in &rows {
        ctx.db.ticketremote_stream_command().id().delete(&row.id);
        delete_service_command_projection(ctx, &row.id);
    }
    if !rows.is_empty() {
        upsert_stream_command_signal(ctx, &ticket_id, &backend_id, revision, now);
    }
    rows.len().min(u32::MAX as usize) as u32
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
    let clean_revision = clean_token(revision, now);
    let row = TicketremoteStreamCommandSignal {
        id: id.clone(),
        ticketId: clean_ticket_id(ticket_id),
        backendId: clean_backend_id(backend_id),
        revision: clean_revision,
        pendingCount: pending_count,
        updatedAt: now.into(),
    };
    if let Some(existing) = ctx.db.ticketremote_stream_command_signal().id().find(&id) {
        if existing.pendingCount == row.pendingCount && existing.revision == row.revision {
            return;
        }
        ctx.db.ticketremote_stream_command_signal().id().update(row);
        return;
    }
    ctx.db.ticketremote_stream_command_signal().insert(row);
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
    if matches!(
        command_type.as_str(),
        "start" | "keyframe" | "recover_stream" | "prepare_control_code"
    ) {
        let now_ms = parse_time_ms(now);
        if let Some(existing) = ctx
            .db
            .ticketremote_stream_command()
            .ticketBackendStatus()
            .filter((&ticket.id, &backend_id, "pending"))
            .find(|row| row.commandType == command_type && parse_time_ms(&row.expiresAt) > now_ms)
        {
            return existing;
        }
    }
    let revision = clean_token(revision, now);
    let id = non_empty(
        command_id,
        &format!("{}:{}:{}:{}", ticket.id, backend_id, revision, command_type),
    );
    if let Some(existing) = ctx.db.ticketremote_stream_command().id().find(&id) {
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
    if status == "acknowledged" || status == "dispatched" {
        ctx.db
            .ticketremote_stream_command()
            .id()
            .delete(&existing.id);
        delete_service_command_projection(ctx, &existing.id);
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
    if existing.status == row.status
        && existing.reason == row.reason
        && existing.payloadJson == row.payloadJson
    {
        return;
    }
    ctx.db
        .ticketremote_stream_command()
        .id()
        .update(row.clone());
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
        ctx.db.ticketremote_phone_current_report().id().update(row);
        return;
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
        ctx.db.ticketremote_relay_current_report().id().update(row);
        return;
    }
    ctx.db.ticketremote_relay_current_report().insert(row);
}

fn delete_control_code_request(ctx: &ReducerContext, request_id: &str) {
    let id = request_id.to_string();
    ctx.db.ticketremote_control_code_request().id().delete(&id);
    ctx.db.ticketremote_control_code_owner().id().delete(&id);
}

fn control_code_fast_state_id(ticket_id: &str, backend_id: &str) -> String {
    format!(
        "{}:{}",
        clean_ticket_id(ticket_id),
        clean_backend_id(backend_id)
    )
}

fn control_code_fast_state_expires_at(status: &str, now: &str) -> String {
    add_ms(
        now,
        if status == "fast_ready" {
            CONTROL_CODE_FAST_READY_TTL_MS
        } else {
            CONTROL_CODE_FAST_STATE_TTL_MS
        },
    )
}

fn clean_control_code_fast_status(value: &str) -> String {
    match clean_token(value, "stale").as_str() {
        "fast_ready" => "fast_ready".into(),
        "warming" => "warming".into(),
        "cleanup" => "cleanup".into(),
        "blocked" => "blocked".into(),
        "stale" => "stale".into(),
        _ => "blocked".into(),
    }
}

#[allow(clippy::too_many_arguments)]
fn upsert_control_code_fast_state(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    status: &str,
    revision: &str,
    reason: &str,
    stream_epoch: &str,
    frame_sequence: &str,
    raw_ticket_confirmed: bool,
    cleanup_clear: bool,
    stream_live: bool,
    now: &str,
) -> TicketremoteControlCodeFastState {
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    let id = control_code_fast_state_id(&ticket_id, &backend_id);
    let status = clean_control_code_fast_status(status);
    let ready = status == "fast_ready" && raw_ticket_confirmed && cleanup_clear && stream_live;
    let final_status = if status == "fast_ready" && !ready {
        "blocked".to_string()
    } else {
        status
    };
    let existing = ctx.db.ticketremote_control_code_fast_state().id().find(&id);
    let clean_revision = bounded_text(&non_empty(revision, now), 160);
    let row = TicketremoteControlCodeFastState {
        id: id.clone(),
        ticketId: ticket_id,
        backendId: backend_id,
        status: final_status.clone(),
        revision: clean_revision.clone(),
        reason: bounded_text(&non_empty(reason, &final_status), 160),
        preparedAt: if final_status == "fast_ready" {
            existing
                .as_ref()
                .filter(|row| row.status == "fast_ready" && row.revision == clean_revision)
                .map(|row| row.preparedAt.clone())
                .unwrap_or_else(|| now.into())
        } else {
            existing
                .as_ref()
                .map(|row| row.preparedAt.clone())
                .unwrap_or_else(|| now.into())
        },
        expiresAt: control_code_fast_state_expires_at(&final_status, now),
        streamEpoch: bounded_frame_ordinal(stream_epoch),
        frameSequence: bounded_frame_ordinal(frame_sequence),
        rawTicketConfirmed: raw_ticket_confirmed,
        cleanupClear: cleanup_clear,
        streamLive: stream_live,
        updatedAt: now.into(),
    };
    if let Some(existing) = existing {
        let ttl_ms = if final_status == "fast_ready" {
            CONTROL_CODE_FAST_READY_TTL_MS
        } else {
            CONTROL_CODE_FAST_STATE_TTL_MS
        };
        let remaining_ms = parse_time_ms(&existing.expiresAt).saturating_sub(parse_time_ms(now));
        if control_code_fast_state_same_payload(&existing, &row) && remaining_ms > ttl_ms / 2 {
            return existing;
        }
        ctx.db
            .ticketremote_control_code_fast_state()
            .id()
            .update(row.clone());
    } else {
        ctx.db
            .ticketremote_control_code_fast_state()
            .insert(row.clone());
    }
    row
}

fn control_code_fast_state_same_payload(
    left: &TicketremoteControlCodeFastState,
    right: &TicketremoteControlCodeFastState,
) -> bool {
    left.ticketId == right.ticketId
        && left.backendId == right.backendId
        && left.status == right.status
        && left.revision == right.revision
        && left.reason == right.reason
        && left.preparedAt == right.preparedAt
        && left.streamEpoch == right.streamEpoch
        && left.frameSequence == right.frameSequence
        && left.rawTicketConfirmed == right.rawTicketConfirmed
        && left.cleanupClear == right.cleanupClear
        && left.streamLive == right.streamLive
}

fn control_code_fast_state_row_ready(
    row: &TicketremoteControlCodeFastState,
    expected_revision: &str,
    now: &str,
) -> bool {
    let expected_revision = expected_revision.trim();
    !expected_revision.is_empty()
        && row.status == "fast_ready"
        && row.revision == expected_revision
        && row.rawTicketConfirmed
        && row.cleanupClear
        && row.streamLive
        && parse_time_ms(&row.expiresAt) > parse_time_ms(now)
}

fn control_code_fast_state_current_ready(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    now: &str,
) -> bool {
    let id = control_code_fast_state_id(ticket_id, backend_id);
    let Some(row) = ctx.db.ticketremote_control_code_fast_state().id().find(&id) else {
        return false;
    };
    let revision = row.revision.clone();
    control_code_fast_state_row_ready(&row, &revision, now)
}

fn active_control_code_owner_rows(
    ctx: &ReducerContext,
    ticket_id: &str,
    email: &str,
    now: &str,
) -> Vec<TicketremoteControlCodeOwner> {
    let cutoff = parse_time_ms(now).saturating_sub(CONTROL_CODE_RATE_WINDOW_MS);
    let ticket_id = clean_ticket_id(ticket_id);
    let email = clean_email(email);
    ctx.db
        .ticketremote_control_code_owner()
        .ticketEmail()
        .filter((&ticket_id, &email))
        .filter(|row| parse_time_ms(&row.requestedAt) >= cutoff)
        .collect()
}

fn control_code_submit_mode(fast_state_ready: bool) -> &'static str {
    if fast_state_ready {
        "fast_ready"
    } else {
        "queued_warmup"
    }
}

fn control_code_request_occupies_phone(row: &TicketremoteControlCodeRequest, now: &str) -> bool {
    if parse_time_ms(&row.expiresAt) <= parse_time_ms(now) {
        return false;
    }
    if matches!(row.status.as_str(), "queued" | "running") {
        return true;
    }
    row.cleanupPending
        || (row.status == "succeeded" && row.captureRequired && !row.captureAcknowledged)
}

fn ticket_has_control_code_request_in_progress(
    ctx: &ReducerContext,
    ticket_id: &str,
    now: &str,
) -> bool {
    let ticket_id = clean_ticket_id(ticket_id);
    ctx.db
        .ticketremote_control_code_request()
        .ticketId()
        .filter(&ticket_id)
        .any(|row| control_code_request_occupies_phone(&row, now))
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
    let row = TicketremoteControlCodeRequest {
        status: changes.status.unwrap_or_else(|| existing.status.clone()),
        reason: changes.reason.unwrap_or_else(|| existing.reason.clone()),
        message: changes.message.unwrap_or_else(|| existing.message.clone()),
        resultExpiresAt: changes
            .resultExpiresAt
            .unwrap_or_else(|| existing.resultExpiresAt.clone()),
        resultProof: changes
            .resultProof
            .map(Some)
            .unwrap_or_else(|| existing.resultProof.clone()),
        resultProofAt: changes
            .resultProofAt
            .map(Some)
            .unwrap_or_else(|| existing.resultProofAt.clone()),
        captureRequired: changes.captureRequired.unwrap_or(existing.captureRequired),
        captureAcknowledged: changes
            .captureAcknowledged
            .unwrap_or(existing.captureAcknowledged),
        cleanupPending: changes.cleanupPending.unwrap_or(existing.cleanupPending),
        streamEpoch: changes
            .streamEpoch
            .unwrap_or_else(|| existing.streamEpoch.clone()),
        frameSequence: changes
            .frameSequence
            .unwrap_or_else(|| existing.frameSequence.clone()),
        minFrameSequence: changes
            .minFrameSequence
            .unwrap_or_else(|| existing.minFrameSequence.clone()),
        resultFrameEpoch: changes
            .resultFrameEpoch
            .unwrap_or_else(|| existing.resultFrameEpoch.clone()),
        resultMinFrameSequence: changes
            .resultMinFrameSequence
            .unwrap_or_else(|| existing.resultMinFrameSequence.clone()),
        captureFrameEpoch: changes
            .captureFrameEpoch
            .unwrap_or_else(|| existing.captureFrameEpoch.clone()),
        captureFrameSequence: changes
            .captureFrameSequence
            .unwrap_or_else(|| existing.captureFrameSequence.clone()),
        expiresAt: changes
            .expiresAt
            .unwrap_or_else(|| existing.expiresAt.clone()),
        updatedAt: now.into(),
        ..existing.clone()
    };
    if control_code_request_same_payload(&existing, &row)
        && control_code_request_ttl_is_healthy(&existing, now)
    {
        return;
    }
    ctx.db.ticketremote_control_code_request().id().update(row);
}

fn control_code_request_same_payload(
    left: &TicketremoteControlCodeRequest,
    right: &TicketremoteControlCodeRequest,
) -> bool {
    left.status == right.status
        && left.reason == right.reason
        && left.message == right.message
        && left.resultProof == right.resultProof
        && left.resultProofAt == right.resultProofAt
        && left.captureRequired == right.captureRequired
        && left.captureAcknowledged == right.captureAcknowledged
        && left.cleanupPending == right.cleanupPending
        && left.streamEpoch == right.streamEpoch
        && left.frameSequence == right.frameSequence
        && left.minFrameSequence == right.minFrameSequence
        && left.resultFrameEpoch == right.resultFrameEpoch
        && left.resultMinFrameSequence == right.resultMinFrameSequence
        && left.captureFrameEpoch == right.captureFrameEpoch
        && left.captureFrameSequence == right.captureFrameSequence
}

fn control_code_request_ttl_is_healthy(row: &TicketremoteControlCodeRequest, now: &str) -> bool {
    let now_ms = parse_time_ms(now);
    let request_remaining_ms = parse_time_ms(&row.expiresAt).saturating_sub(now_ms);
    if request_remaining_ms <= CONTROL_CODE_COMMAND_TTL_MS / 2 {
        return false;
    }
    if row.status == "succeeded" {
        return parse_time_ms(&row.resultExpiresAt).saturating_sub(now_ms)
            > CONTROL_CODE_RESULT_TTL_MS / 2;
    }
    true
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
    let row_id = safe_log_sample_interval_ms(&level, &event)
        .map(|interval_ms| sampled_safe_log_row_id(&ticket.id, &source, &event, now, interval_ms))
        .unwrap_or_else(|| {
            safe_log_row_id(
                &ticket.id,
                &source,
                &event,
                &correlation_id,
                detail_json,
                source_id,
                now,
            )
        });
    if ctx
        .db
        .ticketremote_safe_operational_log()
        .id()
        .find(&row_id)
        .is_some()
    {
        return;
    }
    ctx.db
        .ticketremote_safe_operational_log()
        .insert(TicketremoteSafeOperationalLog {
            id: row_id,
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

fn safe_log_sample_interval_ms(level: &str, event: &str) -> Option<i64> {
    if !matches!(level.trim(), "info" | "debug" | "trace") {
        return None;
    }
    match event.trim() {
        "command_queued"
        | "keyframe_requested"
        | "stream_stalled"
        | "stream_recovery_requested"
        | "public_open_grace_retained"
        | "pixel_direct_phone_report_update"
        | "pixel_ticket_control_code_fast_state"
        | "pixel_direct_desired_state_observed"
        | "pixel_direct_pending_command_hot_scan" => Some(60_000),
        _ => None,
    }
}

fn sampled_safe_log_row_id(
    ticket_id: &str,
    source: &str,
    event: &str,
    now: &str,
    interval_ms: i64,
) -> String {
    let interval_ms = interval_ms.max(1);
    let bucket = parse_time_ms(now).div_euclid(interval_ms);
    format!(
        "{}:sample:{}:{}:{}",
        clean_ticket_id(ticket_id),
        to_base36(fnv32(source)),
        to_base36(fnv32(event)),
        bucket
    )
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

fn cleanup_expired(ctx: &ReducerContext, ticket_id: &str, now: &str, batch_size: u32) -> u32 {
    let ticket = ensure_ticket(ctx, ticket_id, "", now);
    let expiry_bound = canonical_time(now);
    let limit = if batch_size == 0 {
        CLEANUP_BATCH_SIZE
    } else {
        batch_size.min(CLEANUP_BATCH_SIZE)
    };
    let mut deleted = 0u32;
    let mut control_code_request_deleted = 0u32;
    let mut control_code_owner_deleted = 0u32;
    let mut control_code_fast_state_deleted = 0u32;
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
    if !cleanup_limit_reached(deleted, limit) {
        let viewer_focus_deleted = purge_expired_stream_viewer_focus_for_ticket(
            ctx,
            &ticket.id,
            now,
            cleanup_remaining(limit, deleted),
        );
        deleted += viewer_focus_deleted;
    }
    let request_rows: Vec<_> = ctx
        .db
        .ticketremote_control_code_request()
        .ticketExpiresAt()
        .filter((&ticket.id, ..=expiry_bound.as_str()))
        .take(cleanup_remaining(limit, deleted) as usize)
        .collect();
    for row in request_rows {
        if cleanup_limit_reached(deleted, limit) {
            break;
        }
        let owner_present = ctx
            .db
            .ticketremote_control_code_owner()
            .id()
            .find(&row.id)
            .is_some();
        if owner_present && cleanup_remaining(limit, deleted) < 2 {
            break;
        }
        delete_control_code_request(ctx, &row.id);
        deleted += 1;
        control_code_request_deleted += 1;
        if owner_present {
            deleted += 1;
            control_code_owner_deleted += 1;
        }
    }
    let owner_rows: Vec<_> = ctx
        .db
        .ticketremote_control_code_owner()
        .ticketExpiresAt()
        .filter((&ticket.id, ..=expiry_bound.as_str()))
        .take(cleanup_remaining(limit, deleted) as usize)
        .collect();
    for row in owner_rows {
        if cleanup_limit_reached(deleted, limit) {
            break;
        }
        let request_present = ctx
            .db
            .ticketremote_control_code_request()
            .id()
            .find(&row.id)
            .is_some();
        if request_present && cleanup_remaining(limit, deleted) < 2 {
            break;
        }
        delete_control_code_request(ctx, &row.id);
        deleted += 1;
        control_code_owner_deleted += 1;
        if request_present {
            deleted += 1;
            control_code_request_deleted += 1;
        }
    }
    let fast_state_rows: Vec<_> = ctx
        .db
        .ticketremote_control_code_fast_state()
        .ticketExpiresAt()
        .filter((&ticket.id, ..=expiry_bound.as_str()))
        .take(cleanup_remaining(limit, deleted) as usize)
        .collect();
    for row in fast_state_rows {
        if cleanup_limit_reached(deleted, limit) {
            break;
        }
        ctx.db
            .ticketremote_control_code_fast_state()
            .id()
            .delete(&row.id);
        deleted += 1;
        control_code_fast_state_deleted += 1;
    }
    let log_rows: Vec<_> = ctx
        .db
        .ticketremote_safe_operational_log()
        .ticketExpiresAt()
        .filter((&ticket.id, ..=expiry_bound.as_str()))
        .take(cleanup_remaining(limit, deleted) as usize)
        .collect();
    for row in log_rows {
        if cleanup_limit_reached(deleted, limit) {
            break;
        }
        ctx.db
            .ticketremote_safe_operational_log()
            .id()
            .delete(&row.id);
        deleted += 1;
        safe_log_deleted += 1;
    }
    let _ = control_code_request_deleted;
    let _ = control_code_owner_deleted;
    let _ = control_code_fast_state_deleted;
    let _ = safe_log_deleted;
    deleted
}

fn purge_expired_stream_viewer_focus_for_ticket(
    ctx: &ReducerContext,
    ticket_id: &str,
    now: &str,
    batch_size: u32,
) -> u32 {
    let ticket_id = clean_ticket_id(ticket_id);
    let expiry_bound = canonical_time(now);
    let rows: Vec<_> = ctx
        .db
        .ticketremote_stream_viewer_focus()
        .ticketExpiresAt()
        .filter((&ticket_id, ..=expiry_bound.as_str()))
        .take(batch_size as usize)
        .collect();
    let mut deleted = 0u32;
    let mut touched_backends: Vec<String> = Vec::new();
    for row in rows {
        if cleanup_limit_reached(deleted, batch_size) {
            break;
        }
        if !touched_backends.iter().any(|id| id == &row.backendId) {
            touched_backends.push(row.backendId.clone());
        }
        ctx.db
            .ticketremote_stream_viewer_focus()
            .id()
            .delete(&row.id);
        deleted += 1;
    }
    for backend_id in touched_backends {
        refresh_stream_desired_from_viewer_focus(
            ctx,
            &ticket_id,
            &backend_id,
            now,
            "viewer_focus_expired",
        );
    }
    deleted
}

fn purge_expired_stream_viewer_focus_for_ticket_backend(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    now: &str,
    batch_size: u32,
) -> u32 {
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    let rows: Vec<_> = ctx
        .db
        .ticketremote_stream_viewer_focus()
        .ticketBackend()
        .filter((&ticket_id, &backend_id))
        .collect();
    let mut deleted = 0u32;
    for row in rows {
        if cleanup_limit_reached(deleted, batch_size) {
            break;
        }
        if stream_viewer_focus_expired(&row, now) {
            ctx.db
                .ticketremote_stream_viewer_focus()
                .id()
                .delete(&row.id);
            deleted += 1;
        }
    }
    deleted
}

fn refresh_stream_desired_from_viewer_focus(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    now: &str,
    reason: &str,
) {
    let viewers = active_stream_viewer_focus_count(ctx, ticket_id, backend_id, now);
    if stream_desired_core_matches(ctx, ticket_id, backend_id, viewers > 0, viewers) {
        return;
    }
    upsert_stream_desired_state(
        ctx,
        ticket_id,
        backend_id,
        viewers > 0,
        viewers,
        reason,
        now,
        "cleanup",
        now,
    );
}

fn stream_viewer_focus_expired(row: &TicketremoteStreamViewerFocus, now: &str) -> bool {
    !row.active || parse_time_ms(&row.expiresAt) <= parse_time_ms(now)
}

fn purge_expired_stream_commands_for_ticket(
    ctx: &ReducerContext,
    ticket_id: &str,
    now: &str,
    batch_size: u32,
) -> u32 {
    let ticket_id = clean_ticket_id(ticket_id);
    let expiry_bound = canonical_time(now);
    let mut deleted = 0u32;
    let mut touched: Vec<String> = Vec::new();
    let rows: Vec<_> = ctx
        .db
        .ticketremote_stream_command()
        .ticketExpiresAt()
        .filter((&ticket_id, ..=expiry_bound.as_str()))
        .take(batch_size as usize)
        .collect();
    for row in rows {
        if cleanup_limit_reached(deleted, batch_size) {
            break;
        }
        if !touched.iter().any(|id| id == &row.backendId) {
            touched.push(row.backendId.clone());
        }
        ctx.db.ticketremote_stream_command().id().delete(&row.id);
        delete_service_command_projection(ctx, &row.id);
        deleted += 1;
    }
    refresh_touched_signals(ctx, &ticket_id, &touched, now);
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

#[cfg(test)]
mod tests {
    use super::*;

    fn fast_state(expires_at: &str) -> TicketremoteControlCodeFastState {
        TicketremoteControlCodeFastState {
            id: "vivi-default:pixel".into(),
            ticketId: "vivi-default".into(),
            backendId: "pixel".into(),
            status: "fast_ready".into(),
            revision: "revision-1".into(),
            reason: "ready".into(),
            preparedAt: "2026-07-10T12:00:00Z".into(),
            expiresAt: expires_at.into(),
            streamEpoch: "3".into(),
            frameSequence: "9".into(),
            rawTicketConfirmed: true,
            cleanupClear: true,
            streamLive: true,
            updatedAt: "2026-07-10T12:00:00Z".into(),
        }
    }

    fn control_request() -> TicketremoteControlCodeRequest {
        TicketremoteControlCodeRequest {
            id: "request-1".into(),
            ticketId: "vivi-default".into(),
            ownerPublicId: "abcd".into(),
            status: "running".into(),
            reason: "requested".into(),
            message: String::new(),
            requestedAt: "2026-07-10T12:00:00Z".into(),
            updatedAt: "2026-07-10T12:00:00Z".into(),
            resultExpiresAt: String::new(),
            captureRequired: false,
            captureAcknowledged: false,
            cleanupPending: false,
            streamEpoch: "3".into(),
            frameSequence: "9".into(),
            minFrameSequence: "9".into(),
            resultFrameEpoch: "0".into(),
            resultMinFrameSequence: "0".into(),
            captureFrameEpoch: "0".into(),
            captureFrameSequence: "0".into(),
            expiresAt: "2026-07-10T12:05:00Z".into(),
            resultProof: None,
            resultProofAt: None,
        }
    }

    fn valid_service_claims() -> serde_json::Value {
        serde_json::json!({
            "iss": SERVICE_OIDC_ISSUER,
            "aud": [SERVICE_OIDC_AUDIENCE],
            "sub": SERVICE_OIDC_SUBJECT,
            "roles": [SERVICE_ROLE],
        })
    }

    #[test]
    fn service_claims_accept_the_pinned_production_identity() {
        assert!(service_claims_are_valid(&valid_service_claims()));
    }

    #[test]
    fn service_claims_reject_each_untrusted_identity_dimension() {
        let mut claims = valid_service_claims();
        claims["iss"] = serde_json::json!("https://attacker.example/oidc");
        assert!(!service_claims_are_valid(&claims));

        let mut claims = valid_service_claims();
        claims["aud"] = serde_json::json!(["another-client"]);
        assert!(!service_claims_are_valid(&claims));

        let mut claims = valid_service_claims();
        claims["sub"] = serde_json::json!("service:another-runtime");
        assert!(!service_claims_are_valid(&claims));

        let mut claims = valid_service_claims();
        claims["roles"] = serde_json::json!(["member"]);
        assert!(!service_claims_are_valid(&claims));
    }

    #[test]
    fn operator_connection_is_pinned_without_granting_service_claims() {
        assert!(operator_identity_is_valid(OPERATOR_IDENTITY));
        assert!(!operator_identity_is_valid(
            "c200000000000000000000000000000000000000000000000000000000000000"
        ));
        assert!(!service_claims_are_valid(&serde_json::json!({
            "sub": OPERATOR_IDENTITY,
        })));
    }

    #[test]
    fn requester_stream_recovery_bypasses_shared_live_suppression() {
        assert!(stream_command_is_requester_scoped("browser_recovery", "{}"));
        assert!(stream_command_is_requester_scoped(
            "stale_frame_server_recover",
            "{}"
        ));
        assert!(stream_command_is_requester_scoped(
            "stream_recovery",
            r#"{"source":"browser_spacetime"}"#
        ));
        assert!(!stream_command_is_requester_scoped(
            "relay_watchdog",
            r#"{"source":"ticket_remote"}"#
        ));
        assert!(idle_stream_command_is_allowed("video_socket_open", "{}"));
        assert!(idle_stream_command_is_allowed("control_code_request", "{}"));
        assert!(idle_stream_command_is_allowed(
            "relay",
            r#"{"flow":"control_code"}"#
        ));
        assert!(!idle_stream_command_is_allowed(
            "state_tick",
            r#"{"source":"ticket_remote"}"#
        ));
    }

    #[test]
    fn cold_prewarm_wake_is_allowed_while_unrelated_idle_work_stays_blocked() {
        assert!(idle_stream_command_is_allowed(
            "stream_prewarm",
            r#"{"source":"ticket_remote"}"#
        ));
        assert!(idle_stream_command_is_allowed(
            "index_auth_prewarm",
            r#"{"source":"ticket_remote"}"#
        ));
        assert!(!idle_stream_command_is_allowed(
            "late_prewarm",
            r#"{"source":"ticket_remote"}"#
        ));
        assert!(!idle_stream_command_is_allowed(
            "state_tick",
            r#"{"source":"ticket_remote"}"#
        ));
        assert!(!idle_stream_command_is_allowed(
            "relay_watchdog",
            r#"{"source":"ticket_remote"}"#
        ));
    }

    #[test]
    fn fast_state_classifies_the_optional_submit_lane() {
        let ready = fast_state("2026-07-10T12:00:12Z");
        assert!(control_code_fast_state_row_ready(
            &ready,
            "revision-1",
            "2026-07-10T12:00:01Z"
        ));
        assert!(!control_code_fast_state_row_ready(
            &ready,
            "revision-2",
            "2026-07-10T12:00:01Z"
        ));
        assert!(!control_code_fast_state_row_ready(
            &ready,
            "revision-1",
            "2026-07-10T12:00:12Z"
        ));
        let mut warming = ready;
        warming.status = "warming".into();
        assert!(!control_code_fast_state_row_ready(
            &warming,
            "revision-1",
            "2026-07-10T12:00:01Z"
        ));
        assert_eq!(control_code_submit_mode(true), "fast_ready");
        assert_eq!(control_code_submit_mode(false), "queued_warmup");
    }

    #[test]
    fn only_unfinished_control_code_work_occupies_the_phone() {
        let now = "2026-07-10T12:00:01Z";
        let mut request = control_request();
        assert!(control_code_request_occupies_phone(&request, now));

        request.status = "queued".into();
        assert!(control_code_request_occupies_phone(&request, now));

        request.status = "succeeded".into();
        request.captureRequired = true;
        request.captureAcknowledged = false;
        assert!(control_code_request_occupies_phone(&request, now));

        request.captureRequired = false;
        request.cleanupPending = true;
        assert!(control_code_request_occupies_phone(&request, now));

        request.cleanupPending = false;
        assert!(!control_code_request_occupies_phone(&request, now));

        request.status = "failed".into();
        assert!(!control_code_request_occupies_phone(&request, now));

        request.status = "closed".into();
        assert!(!control_code_request_occupies_phone(&request, now));

        request.status = "running".into();
        request.expiresAt = now.into();
        assert!(!control_code_request_occupies_phone(&request, now));
    }

    #[test]
    fn routine_logs_are_sampled_but_failures_are_retained() {
        assert_eq!(
            safe_log_sample_interval_ms("info", "keyframe_requested"),
            Some(60_000)
        );
        assert_eq!(
            safe_log_sample_interval_ms("error", "keyframe_requested"),
            None
        );
        assert_eq!(
            safe_log_sample_interval_ms("info", "stream_recovery_failed"),
            None
        );
        let first = sampled_safe_log_row_id(
            "vivi-default",
            "browser",
            "keyframe_requested",
            "2026-07-10T12:00:01Z",
            60_000,
        );
        let same_bucket = sampled_safe_log_row_id(
            "vivi-default",
            "browser",
            "keyframe_requested",
            "2026-07-10T12:00:59Z",
            60_000,
        );
        let next_bucket = sampled_safe_log_row_id(
            "vivi-default",
            "browser",
            "keyframe_requested",
            "2026-07-10T12:01:00Z",
            60_000,
        );
        assert_eq!(first, same_bucket);
        assert_ne!(first, next_bucket);
    }

    #[test]
    fn duplicate_control_updates_skip_clock_only_rewrites_until_ttl_needs_refresh() {
        let existing = control_request();
        let mut clock_only = existing.clone();
        clock_only.updatedAt = "2026-07-10T12:00:05Z".into();
        clock_only.expiresAt = "2026-07-10T12:05:05Z".into();
        assert!(control_code_request_same_payload(&existing, &clock_only));
        assert!(control_code_request_ttl_is_healthy(
            &existing,
            "2026-07-10T12:00:30Z"
        ));
        assert!(!control_code_request_ttl_is_healthy(
            &existing,
            "2026-07-10T12:04:30Z"
        ));
        clock_only.status = "succeeded".into();
        assert!(!control_code_request_same_payload(&existing, &clock_only));
    }

    #[test]
    fn presence_heartbeat_changes_desired_state_only_when_core_state_changes() {
        let row = TicketremoteStreamDesiredState {
            id: "vivi-default:pixel".into(),
            ticketId: "vivi-default".into(),
            backendId: "pixel".into(),
            desiredActive: true,
            viewerCount: 2,
            reason: "browser_stream_heartbeat".into(),
            revision: "revision-1".into(),
            updatedBy: "browser".into(),
            updatedAt: "2026-07-10T12:00:00Z".into(),
        };
        assert!(stream_desired_core_equal(&row, true, 2));
        assert!(!stream_desired_core_equal(&row, true, 3));
        assert!(!stream_desired_core_equal(&row, false, 2));
    }
}
