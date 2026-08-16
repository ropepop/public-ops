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
const MEMBER_PROXY_ROLE: &str = "ticketremote_member_proxy";
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
const LATEST_TICKET_RESELECT_COMMAND_TTL_MS: i64 = 10 * 60_000;
const LATEST_TICKET_RESELECT_MAX_HORIZON_MS: i64 = 90 * 24 * 60 * 60 * 1000;
const CONTROL_CODE_PHONE_TTL_MS: i64 = 105_000;
const CONTROL_CODE_FAST_READY_TTL_MS: i64 = 12_000;
const CONTROL_CODE_FAST_STATE_TTL_MS: i64 = 30_000;
const STREAM_VIEWER_FOCUS_TTL_MS: i64 = 90_000;
const SAFE_JSON_MAX_BYTES: usize = 4096;
const STREAM_BACKGROUND_SUPPRESS_FALLBACK_MAX_AGE_MS: i64 = 2_500;
const STREAM_BACKGROUND_REPORT_MAX_AGE_MS: i64 = 5_000;

macro_rules! same_fields {
    ($left:expr, $right:expr; $($field:ident),+ $(,)?) => {
        true $(&& $left.$field == $right.$field)+
    };
}

macro_rules! upsert_row {
    ($ctx:expr, $table:ident, $row:expr) => {{
        let row = $row;
        let id = row.id.clone();
        if $ctx.db.$table().id().find(&id).is_some() {
            $ctx.db.$table().id().update(row.clone());
        } else {
            $ctx.db.$table().insert(row.clone());
        }
        row
    }};
}

macro_rules! apply_changes {
    ($row:expr, $changes:expr; $($field:ident),+ $(,)?) => {
        $(if let Some(value) = $changes.$field { $row.$field = value; })+
    };
}

macro_rules! purge_control_code_rows {
    ($ctx:expr, $table:ident, $paired:ident, $ticket:expr, $bound:expr, $limit:expr, $deleted:expr) => {{
        let rows: Vec<_> = $ctx
            .db
            .$table()
            .ticketExpiresAt()
            .filter(($ticket, ..=$bound))
            .take(cleanup_remaining($limit, $deleted) as usize)
            .collect();
        for row in rows {
            let paired = $ctx.db.$paired().id().find(&row.id).is_some();
            let cost = 1 + u32::from(paired);
            if cleanup_remaining($limit, $deleted) < cost {
                break;
            }
            delete_control_code_request($ctx, &row.id);
            $deleted += cost;
        }
    }};
}

macro_rules! purge_expired_rows {
    ($ctx:expr, $table:ident, $ticket:expr, $bound:expr, $limit:expr, $deleted:expr) => {{
        let rows: Vec<_> = $ctx
            .db
            .$table()
            .ticketExpiresAt()
            .filter(($ticket, ..=$bound))
            .take(cleanup_remaining($limit, $deleted) as usize)
            .collect();
        for row in rows {
            $ctx.db.$table().id().delete(&row.id);
            $deleted += 1;
        }
    }};
}

macro_rules! ticket_expiry_purgers {
    ($( $name:ident($table:ident) |$ctx:ident, $ticket:ident, $touched:ident, $clock:ident|
        $after:block )+) => {$(
        fn $name(
            $ctx: &ReducerContext,
            ticket_id: &str,
            $clock: &str,
            batch_size: u32,
        ) -> u32 {
            let $ticket = clean_ticket_id(ticket_id);
            let expiry = canonical_time($clock);
            let table = $ctx.db.$table();
            let rows: Vec<_> = table.ticketExpiresAt()
                .filter((&$ticket, ..=expiry.as_str())).take(batch_size as usize).collect();
            let mut $touched = Vec::<String>::new();
            for row in &rows {
                if !$touched.contains(&row.backendId) { $touched.push(row.backendId.clone()); }
                table.id().delete(&row.id);
            }
            $after
            rows.len().min(u32::MAX as usize) as u32
        }
    )+};
}

macro_rules! purge_ticket_history {
    ($ctx:expr, $ticket:expr, $bound:expr, $limit:expr, $deleted:expr) => {{
        purge_control_code_rows!(
            $ctx,
            ticketremote_control_code_request,
            ticketremote_control_code_owner,
            $ticket,
            $bound,
            $limit,
            $deleted
        );
        purge_control_code_rows!(
            $ctx,
            ticketremote_control_code_owner,
            ticketremote_control_code_request,
            $ticket,
            $bound,
            $limit,
            $deleted
        );
        purge_expired_rows!(
            $ctx,
            ticketremote_control_code_fast_state,
            $ticket,
            $bound,
            $limit,
            $deleted
        );
        purge_expired_rows!(
            $ctx,
            ticketremote_safe_operational_log,
            $ticket,
            $bound,
            $limit,
            $deleted
        );
        purge_expired_rows!(
            $ctx,
            ticketremote_latest_ticket_reselect_schedule,
            $ticket,
            $bound,
            $limit,
            $deleted
        );
    }};
}

macro_rules! bootstrap_stream_state {
    ($ctx:expr, $ticket:expr, $backend:expr, $clock:expr) => {{
        upsert_stream_desired_state(
            $ctx,
            $ticket,
            $backend,
            false,
            0,
            "bootstrap",
            $clock,
            "service_bootstrap",
            $clock,
        );
        upsert_phone_current_report($ctx, $ticket, $backend, "idle", false, "", "", "{}", $clock);
        upsert_relay_current_report($ctx, $ticket, $backend, 0, "idle", "", "0", "{}", $clock);
    }};
}

macro_rules! service_ticket_reducers {
    ($( $name:ident($ctx:ident; $ticket_arg:ident; $($arg:ident: $kind:ty),+; $now_arg:ident)
        |$ticket:ident, $clock:ident| $body:block )+) => {$(
        #[spacetimedb::reducer]
        pub fn $name(
            $ctx: &ReducerContext,
            $ticket_arg: String,
            $($arg: $kind,)+
            $now_arg: String,
        ) -> Result<(), String> {
            require_service($ctx)?;
            let $clock = now_or($ctx, &$now_arg);
            let $ticket = ensure_ticket($ctx, &$ticket_arg, "", &$clock);
            $body;
            Ok(())
        }
    )+};
}

macro_rules! service_reducers {
    ($( $name:ident($ctx:ident; $($arg:ident: $kind:ty),* $(,)?) $body:block )+) => {$(
        #[spacetimedb::reducer]
        pub fn $name($ctx: &ReducerContext, $($arg: $kind),*) -> Result<(), String> {
            require_service($ctx)?;
            $body;
            Ok(())
        }
    )+};
}

macro_rules! member_reducers {
    ($( $name:ident($ctx:ident; $($arg:ident: $kind:ty),*; ticket = $ticket_arg:ident)
        |$ticket:ident, $email:ident, $clock:ident| $body:block )+) => {$(
        #[spacetimedb::reducer]
        pub fn $name(
            $ctx: &ReducerContext,
            $($arg: $kind),*
        ) -> Result<(), String> {
            let $clock = now($ctx);
            let $ticket = ensure_ticket($ctx, &$ticket_arg, "", &$clock);
            let $email = client_email_from_auth($ctx, &$ticket.id)?;
            $body;
            Ok(())
        }
    )+};
}

macro_rules! cloned_projection {
    ($name:ident from $source:ident with $convert:ident { $($field:ident: $kind:ty),+ $(,)? }) => {
        #[derive(Clone, SpacetimeType)]
        pub struct $name { $(pub $field: $kind,)+ }

        fn $convert(row: &$source) -> $name {
            $name { $($field: row.$field.clone(),)+ }
        }
    };
}

macro_rules! service_views {
    ($( $accessor:ident => $name:ident -> $row:ty
        |$ctx:ident, $ticket:ident| $body:block )+) => {$(
        #[spacetimedb::view(accessor = $accessor, public, primary_key = id)]
        pub fn $name($ctx: &ViewContext) -> Vec<$row> {
            let Some($ticket) = service_ticket_id_for_viewer($ctx) else {
                return Vec::new();
            };
            $body
        }
    )+};
}

macro_rules! expression_functions {
    ($(fn $name:ident($($arg:ident: $kind:ty),* $(,)?) -> $output:ty = $body:expr;)+) => {$(
        fn $name($($arg: $kind),*) -> $output { $body }
    )+};
}

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

#[spacetimedb::table(accessor = ticketremote_latest_ticket_reselect_schedule,
    index(accessor = ticketBackendStatus, btree(columns = [ticketId, backendId, status])),
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))
)]
#[derive(Clone)]
pub struct TicketremoteLatestTicketReselectSchedule {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub backendId: String,
    pub scheduledAt: String,
    pub phoneLocalTime: String,
    pub phoneTimeZone: String,
    #[index(btree)]
    pub status: String,
    #[index(btree)]
    pub commandId: String,
    pub resultReason: String,
    pub resultPhase: String,
    pub proofSource: String,
    pub requestedBy: String,
    pub createdAt: String,
    pub updatedAt: String,
    pub triggeredAt: String,
    pub completedAt: String,
    pub expiresAt: String,
}

#[spacetimedb::table(
    accessor = ticketremote_latest_ticket_reselect_timer,
    scheduled(ticketremote_scheduled_latest_ticket_reselect),
    index(accessor = ticketBackend, btree(columns = [ticketId, backendId]))
)]
#[derive(Clone)]
pub struct TicketremoteLatestTicketReselectTimer {
    #[primary_key]
    #[auto_inc]
    pub scheduled_id: u64,
    pub scheduled_at: ScheduleAt,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub backendId: String,
    #[index(btree)]
    pub scheduleId: String,
    pub createdAt: String,
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
// Legacy compatibility state. New operational events are written to the
// central operational-logging database; this table remains only so existing
// rows can age out safely during the migration.
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

cloned_projection! {
    TicketremoteServiceTicket from TicketremoteTicket with service_ticket_from_row {
        id: String, displayName: String, updatedAt: String
    }
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

cloned_projection! {
    TicketremoteServicePhone from TicketremotePhoneBackend with service_phone_from_row {
        id: String, ticketId: String, backendId: String, attachName: String, baseUrl: String,
        desiredState: String, streamState: String, healthJson: String, lastError: String,
        lastSeenAt: String
    }
}

cloned_projection! {
    TicketremoteServiceStreamCommand from TicketremoteStreamCommand with service_stream_command_from_row {
        id: String, ticketId: String, backendId: String, commandType: String, status: String,
        revision: String, reason: String, payloadJson: String, createdAt: String, updatedAt: String,
        expiresAt: String
    }
}

cloned_projection! {
    TicketremoteServiceLatestTicketReselectSchedule from TicketremoteLatestTicketReselectSchedule
        with service_latest_ticket_reselect_schedule_from_row {
        id: String, ticketId: String, backendId: String, scheduledAt: String,
        phoneLocalTime: String, phoneTimeZone: String, status: String, commandId: String,
        resultReason: String, resultPhase: String, proofSource: String, requestedBy: String,
        createdAt: String, updatedAt: String, triggeredAt: String, completedAt: String,
        expiresAt: String
    }
}

service_views! {
    ticketremote_service_ticket => ticketremote_service_ticket_view -> TicketremoteServiceTicket
    |ctx, ticket| {
        ctx.db.ticketremote_ticket().id().find(&ticket)
            .map(|row| vec![service_ticket_from_row(&row)]).unwrap_or_default()
    }
    ticketremote_service_ticket_member => ticketremote_service_ticket_member_view -> TicketremoteServiceMember
    |ctx, ticket| {
        ctx.db.ticketremote_ticket_member().ticketId().filter(&ticket)
            .map(|row| service_member_from_row(&row)).collect()
    }
    ticketremote_service_phone_backend => ticketremote_service_phone_backend_view -> TicketremoteServicePhone
    |ctx, ticket| {
        ctx.db.ticketremote_phone_backend().ticketId().filter(&ticket)
            .map(|row| service_phone_from_row(&row)).collect()
    }
    ticketremote_service_stream_command => ticketremote_service_stream_command_view -> TicketremoteServiceStreamCommand
    |ctx, ticket| {
        ctx.db.ticketremote_stream_command().ticketBackendStatus()
            .filter((&ticket, "pixel", "pending"))
            .map(|row| service_stream_command_from_row(&row)).collect()
    }
    ticketremote_service_latest_ticket_reselect_schedule =>
        ticketremote_service_latest_ticket_reselect_schedule_view ->
        TicketremoteServiceLatestTicketReselectSchedule
    |ctx, ticket| {
        ctx.db.ticketremote_latest_ticket_reselect_schedule().ticketId().filter(&ticket)
            .map(|row| service_latest_ticket_reselect_schedule_from_row(&row)).collect()
    }
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

service_reducers! {
    ticketremote_register_service_identity(ctx; ticketId: String, now: String) {
        register_service_identity(ctx, clean_ticket_id(&ticketId), &now_or(ctx, &now))
    }
}

member_reducers! {
    ticketremote_member_set_stream_focus(ctx; ticketId: String, backendId: String,
        sessionId: String, active: bool, reason: String; ticket = ticketId) |ticket, email, now| {
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
    }
}

macro_rules! member_stream_reducers {
    ($($name:ident => $command:literal, $fallback:literal, $ttl:expr);+ $(;)?) => {$(
        #[spacetimedb::reducer]
        pub fn $name(
            ctx: &ReducerContext,
            ticketId: String,
            backendId: String,
            reason: String,
        ) -> Result<(), String> {
            let clock = now(ctx);
            let ticket = ensure_ticket(ctx, &ticketId, "", &clock);
            let email = client_email_from_auth(ctx, &ticket.id)?;
            let command_reason = bounded_text(&non_empty(&reason, $fallback), 120);
            insert_stream_command(
                ctx, &ticket.id, &clean_backend_id(&backendId),
                &format!("{}:browser:{}:{}", ticket.id, stable_stamp(&clock), $command),
                $command, &clock, &command_reason,
                &json_object(&[("source", "browser"), ("actor", &account_public_id(&email))]),
                $ttl, &clock,
            );
            Ok(())
        }
    )+};
}

member_stream_reducers! {
    ticketremote_member_request_keyframe => "keyframe", "browser_request", 30_000;
    ticketremote_member_recover_stream => "recover_stream", "browser_recovery", CONTROL_CODE_COMMAND_TTL_MS;
}

member_reducers! {
    ticketremote_member_request_control_code(ctx; ticketId: String, backendId: String,
        sessionId: String, digits: String, expectedFastRevision: String; ticket = ticketId)
        |ticket, email, now| {
    let session_id = non_empty(&sessionId, &connection_session_id(ctx));
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
    let (cleanup_revision, cleanup_stream_epoch, cleanup_frame_sequence, stream_was_live) =
        fast_state.as_ref().map(|row| (
            row.revision.clone(), row.streamEpoch.clone(), row.frameSequence.clone(), row.streamLive,
        )).unwrap_or_else(|| (now.clone(), String::new(), String::new(), false));
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
    insert_control_code_public_request(ctx, &ticket.id, &request_id, &owner_public_id, &now);
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
        "fastRevision": bounded_text(&expectedFastRevision, 160)
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
    }
}

member_reducers! {
    ticketremote_member_confirm_control_code_browser_capture(ctx; ticketId: String,
        backendId: String, _sessionId: String, requestId: String, candidateFrameEpoch: String,
        candidateFrameSequence: String, acceptedReason: String; ticket = ticketId)
        |ticket, email, now| {
    let request_id = requestId.trim().to_string();
    let Some(current) = owned_control_code_request(ctx, &ticket.id, &email, &request_id, true)?
    else {
        return Err("request_not_ready".into());
    };
    if current.status != "succeeded" {
        return Err("request_not_ready".into());
    }
    let frame_epoch = bounded_frame_ordinal(&candidateFrameEpoch);
    let frame_sequence = bounded_frame_ordinal(&candidateFrameSequence);
    let (marker_epoch, marker_sequence) = control_code_result_marker(&current);
    if marker_epoch != "0" && frame_epoch != marker_epoch {
        return Err("frame_before_marker".into());
    }
    if marker_sequence != "0" && compare_ordinal(&frame_sequence, &marker_sequence) < 0 {
        return Err("frame_before_marker".into());
    }
    let accepted_reason = non_empty(&acceptedReason, "browser_capture_confirmed");
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
    publish_browser_capture(
        ctx,
        &ticket.id,
        &clean_backend_id(&backendId),
        &request_id,
        true,
        &accepted_reason,
        &frame_epoch,
        &frame_sequence,
        &now,
    );
    }
}

member_reducers! {
    ticketremote_member_close_control_code(ctx; ticketId: String, backendId: String,
        _sessionId: String, requestId: String, reason: String; ticket = ticketId)
        |ticket, email, now| {
    let request_id = requestId.trim().to_string();
    let Some(current_request) =
        owned_control_code_request(ctx, &ticket.id, &email, &request_id, false)?
    else {
        return Ok(());
    };
    if control_code_close_is_idempotent(Some(&current_request)) {
        return Ok(());
    }
    let capture_acknowledged = current_request.captureAcknowledged;
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
    publish_browser_capture(
        ctx,
        &ticket.id,
        &clean_backend_id(&backendId),
        &request_id,
        false,
        &capture_reason,
        "0",
        "0",
        &now,
    );
    }
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
    // Explicit compatibility rejection for an older browser bundle. Keep the
    // original membership gate, but never create a new legacy Ticket log row.
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    let _ = (&id, &level, &event, &correlationId, &detailJson, &email);
    Err("legacy_operational_log_writer_inactive".into())
}

member_reducers! {
    ticketremote_member_upsert_member(ctx; ticketId: String, email: String, role: String;
        ticket = ticketId)
        |ticket, actor, now| {
        require_admin(ctx, &ticket.id, &actor)?;
        upsert_member_row(ctx, &ticket.id, &email, &role, &now)
    }
    ticketremote_member_remove_member(ctx; ticketId: String, email: String; ticket = ticketId)
        |ticket, actor, now| {
        require_admin(ctx, &ticket.id, &actor)?;
        deactivate_member_row(ctx, &ticket.id, &email, &now)
    }
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
    let members = ctx.db.ticketremote_ticket_member();
    if !email.is_empty() && members.id().find(member_id(&ticket.id, &email)).is_none() {
        members.insert(TicketremoteTicketMember {
            id: member_id(&ticket.id, &email),
            ticketId: ticket.id.clone(),
            email,
            role: "owner".into(),
            active: true,
            createdAt: now.clone(),
            updatedAt: now.clone(),
        });
    }
    if !phoneBackendId.trim().is_empty() {
        let backend_id = clean_backend_id(&phoneBackendId);
        let attach_name = non_empty(&phoneAttachName, &backend_id);
        clear_phone_backends(ctx, &ticket.id);
        ctx.db
            .ticketremote_phone_backend()
            .insert(TicketremotePhoneBackend {
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
            });
        bootstrap_stream_state!(ctx, &ticket.id, &backend_id, &now);
    }
    let issuer = authIssuer.trim().to_string();
    let audience = authAudience.trim().to_string();
    if !issuer.is_empty() && !audience.is_empty() {
        let auth = ctx.db.ticketremote_auth_config();
        let row = TicketremoteAuthConfig {
            ticketId: ticket.id.clone(),
            issuer,
            audience,
            updatedAt: now.clone(),
        };
        if auth.ticketId().find(&ticket.id).is_some() {
            auth.ticketId().update(row);
        } else {
            auth.insert(row);
        }
    }
    ensure_cleanup_schedule(ctx, &ticket.id, &now);
    cleanup_expired(ctx, &ticket.id, &now, CLEANUP_BATCH_SIZE);
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
pub fn ticketremote_scheduled_latest_ticket_reselect(
    ctx: &ReducerContext,
    arg: TicketremoteLatestTicketReselectTimer,
) -> Result<(), String> {
    if !ctx.sender_auth().is_internal() {
        return Err("internal role required".into());
    }
    trigger_scheduled_latest_ticket_reselect(ctx, &arg)
}

service_reducers! {
    ticketremote_upsert_member(ctx; ticketId: String, actorEmail: String, email: String,
        role: String, nowArg: String) {
        let now = now_or(ctx, &nowArg);
        let ticket = ensure_ticket(ctx, &ticketId, "", &now);
        require_admin(ctx, &ticket.id, &actorEmail)?;
        upsert_member_row(ctx, &ticket.id, &email, &role, &now)
    }
    ticketremote_remove_member(ctx; ticketId: String, actorEmail: String, email: String,
        nowArg: String) {
        let now = now_or(ctx, &nowArg);
        let ticket = ensure_ticket(ctx, &ticketId, "", &now);
        require_admin(ctx, &ticket.id, &actorEmail)?;
        deactivate_member_row(ctx, &ticket.id, &email, &now)
    }
    ticketremote_update_phone(ctx; ticketId: String, backendId: String, attachName: String,
        baseUrl: String, desiredState: String, healthJson: String, lastError: String, nowArg: String) {
        apply_phone_update(ctx, &ticketId, &backendId, &attachName, &baseUrl, &desiredState,
            &healthJson, &lastError, &now_or(ctx, &nowArg))
    }
}

#[spacetimedb::reducer]
pub fn ticketremote_schedule_latest_ticket_reselect(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    scheduleId: String,
    scheduledAtMicros: i64,
    phoneLocalTime: String,
    phoneTimeZone: String,
    requestedBy: String,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    schedule_latest_ticket_reselect(
        ctx,
        &ticketId,
        &backendId,
        &scheduleId,
        scheduledAtMicros,
        &phoneLocalTime,
        &phoneTimeZone,
        &requestedBy,
        &now_or(ctx, &nowArg),
    )
}

#[spacetimedb::reducer]
pub fn ticketremote_cancel_latest_ticket_reselect(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    scheduleId: String,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    cancel_latest_ticket_reselect(
        ctx,
        &ticketId,
        &backendId,
        &scheduleId,
        &now_or(ctx, &nowArg),
    )
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
    let command_type = non_empty(&commandType, "command");
    let command_reason = non_empty(&reason, "stream_command");
    let background = suppressible_background_stream_command(&command_type);
    if (background
            && authoritative_stream_is_idle(ctx, &ticketId, &backendId, &now)
            && !idle_stream_command_is_allowed(&command_reason, &payloadJson))
        || (background
            && !stream_command_is_requester_scoped(&command_reason, &payloadJson)
            && live_relay_suppresses_background_stream_command(
                ctx,
                &ticketId,
                &backendId,
                &command_reason,
                &now,
            ))
    {
        return Ok(());
    }
    insert_stream_command(
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
    Ok(())
}

service_reducers! {
    ticketremote_ack_stream_command(ctx; commandId: String, status: String, reason: String,
        nowArg: String) {
        update_stream_command_status(ctx, &commandId, &status, &reason, &now_or(ctx, &nowArg))
    }
}

service_ticket_reducers! {
    ticketremote_set_stream_desired_state(ctx; ticketId;
        backendId: String, desiredActive: bool, viewerCount: u32, reason: String,
        revision: String, updatedBy: String; nowArg
    ) |ticket, now| {
        upsert_stream_desired_state(ctx, &ticket.id, &backendId, desiredActive, viewerCount,
            &reason, &revision, &updatedBy, &now);
    }
    ticketremote_update_phone_current_report(ctx; ticketId;
        backendId: String, streamState: String, desiredActive: bool, lastCommandId: String,
        lastCommandRevision: String, statusJson: String; nowArg
    ) |ticket, now| {
        upsert_phone_current_report(ctx, &ticket.id, &backendId, &streamState, desiredActive,
            &lastCommandId, &lastCommandRevision, &statusJson, &now);
    }
    ticketremote_update_control_code_fast_state(ctx; ticketId;
        backendId: String, status: String, revision: String, reason: String, streamEpoch: String,
        frameSequence: String, rawTicketConfirmed: bool, cleanupClear: bool, streamLive: bool; nowArg
    ) |ticket, now| {
        upsert_control_code_fast_state(ctx, &ticket.id, &backendId, &status, &revision, &reason,
            &streamEpoch, &frameSequence, rawTicketConfirmed, cleanupClear, streamLive, &now);
    }
    ticketremote_update_relay_current_report(ctx; ticketId;
        backendId: String, videoClients: u32, streamVerdict: String, lastFrameAt: String,
        framesForwarded: String, statusJson: String; nowArg
    ) |ticket, now| {
        upsert_relay_current_report(ctx, &ticket.id, &backendId, videoClients, &streamVerdict,
            &lastFrameAt, &framesForwarded, &statusJson, &now);
    }
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
    let mut clean_status = safe_token(&status, &existing.status);
    let incoming_reason = bounded_text(&non_empty(&reason, &existing.reason), 200);
    let preserve_captured_success = existing.status == "succeeded"
        && existing.captureAcknowledged
        && matches!(clean_status.as_str(), "succeeded" | "closed")
        && control_code_cleanup_reason(&incoming_reason);
    if preserve_captured_success {
        clean_status = existing.status.clone();
    }
    let preserve_terminal_failure =
        control_code_cleanup_preserves_terminal_failure(&existing, &clean_status, &incoming_reason);
    let (next_reason, next_message) = control_code_update_text(
        &existing,
        &incoming_reason,
        &message,
        preserve_captured_success,
        preserve_terminal_failure,
    );
    let terminal_failure = control_code_terminal_failure_status(&clean_status);
    let succeeded = clean_status == "succeeded";
    let clean_result_proof = clean_control_code_result_proof(&resultProof);
    let clean_result_proof_at = bounded_text(resultProofAt.trim(), 80);
    update_control_code_public_request(
        ctx,
        &requestId,
        ControlCodeChanges {
            status: Some(clean_status.clone()),
            reason: Some(next_reason),
            message: Some(next_message),
            streamEpoch: updated_ordinal(&streamEpoch, &existing.streamEpoch),
            frameSequence: updated_ordinal(&frameSequence, &existing.frameSequence),
            minFrameSequence: updated_ordinal(&minFrameSequence, &existing.minFrameSequence),
            resultFrameEpoch: updated_ordinal(&resultFrameEpoch, &existing.resultFrameEpoch),
            resultMinFrameSequence: updated_ordinal(
                &resultMinFrameSequence,
                &existing.resultMinFrameSequence,
            ),
            resultProof: optional_text(clean_result_proof),
            resultProofAt: optional_text(clean_result_proof_at),
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

fn owned_control_code_request(
    ctx: &ReducerContext,
    ticket_id: &str,
    email: &str,
    request_id: &str,
    owner_required: bool,
) -> Result<Option<TicketremoteControlCodeRequest>, String> {
    let request_id = request_id.to_string();
    let owner = ctx
        .db
        .ticketremote_control_code_owner()
        .id()
        .find(&request_id);
    let Some(owner) = owner else {
        return if owner_required {
            Err("not_found".into())
        } else {
            Ok(None)
        };
    };
    if owner.ticketId != ticket_id || clean_email(&owner.email) != email {
        return Err("not_found".into());
    }
    Ok(ctx
        .db
        .ticketremote_control_code_request()
        .id()
        .find(&request_id))
}

fn publish_browser_capture(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    request_id: &str,
    accepted: bool,
    reason: &str,
    frame_epoch: &str,
    frame_sequence: &str,
    now: &str,
) {
    let payload = serde_json::json!({
        "owner": "ticket", "app": "vivi", "flow": "control_code",
        "requestId": request_id, "accepted": accepted, "reason": reason,
        "candidateFrameEpoch": frame_ordinal_number(frame_epoch),
        "candidateFrameSequence": frame_ordinal_number(frame_sequence),
        "source": "browser_spacetime"
    })
    .to_string();
    insert_stream_command(
        ctx,
        ticket_id,
        backend_id,
        &format!(
            "{}:control_code_browser_capture{}",
            request_id,
            if accepted { "" } else { "_closed" }
        ),
        "control_code_browser_capture",
        now,
        &bounded_text(reason, 120),
        &payload,
        CONTROL_CODE_COMMAND_TTL_MS,
        now,
    );
}

fn control_code_update_text(
    existing: &TicketremoteControlCodeRequest,
    incoming_reason: &str,
    incoming_message: &str,
    preserve_captured_success: bool,
    preserve_terminal_failure: bool,
) -> (String, String) {
    let reason = if preserve_captured_success || preserve_terminal_failure {
        existing.reason.clone()
    } else {
        incoming_reason.into()
    };
    let message = if preserve_terminal_failure {
        existing.message.clone()
    } else {
        bounded_text(incoming_message, 240)
    };
    (reason, message)
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
    // Explicit compatibility rejection for older sidecars. New writers use
    // operationallog_append_ticket_event in operational-logging-prod.
    require_service(ctx)?;
    let _ = (
        &id,
        &ticketId,
        &source,
        &level,
        &event,
        &correlationId,
        &detailJson,
        &nowArg,
    );
    Err("legacy_operational_log_writer_inactive".into())
}

service_reducers! {
    ticketremote_purge_sensitive_operational_logs(ctx; ticketId: String) {
        let ticket_id = clean_ticket_id(&ticketId);
        let table = ctx.db.ticketremote_safe_operational_log();
        let rows: Vec<_> = table.ticketId().filter(&ticket_id).filter(|row| matches!(
            row.event.as_str(), "pixel_ticket_control_code_result"
                | "pixel_ticket_control_code_request_result_detected"
        )).collect();
        for row in rows { table.id().delete(&row.id); }
    }
    ticketremote_cleanup_expired(ctx; ticketId: String, nowArg: String, batchSize: u32) {
        cleanup_expired(ctx, &clean_ticket_id(&ticketId), &now_or(ctx, &nowArg), batchSize)
    }
}

expression_functions! {
    fn now(ctx: &ReducerContext) -> String = iso(ctx.timestamp);
    fn parse_time_ms(value: &str) -> i64 = parse_time_micros(value) / 1000;
    fn suppressible_background_stream_command(value: &str) -> bool =
        matches!(value.trim(), "start" | "keyframe" | "recover_stream");
    fn clean_ticket_id(value: &str) -> String = non_empty(value, DEFAULT_TICKET_ID);
    fn clean_backend_id(value: &str) -> String = non_empty(value, "pixel");
    fn clean_email(value: &str) -> String = value.trim().to_ascii_lowercase();
    fn bounded_text(value: &str, max: usize) -> String = value.chars().take(max).collect();
    fn member_id(ticket: &str, email: &str) -> String =
        format!("{}:{}", clean_ticket_id(ticket), clean_email(email));
    fn phone_row_id(ticket: &str, backend: &str) -> String =
        format!("{}:{}", clean_ticket_id(ticket), clean_backend_id(backend));
    fn stream_viewer_focus_expires_at(clock: &str) -> String = add_ms(clock, STREAM_VIEWER_FOCUS_TTL_MS);
    fn control_code_request_expires_at(clock: &str) -> String = add_ms(clock, CONTROL_CODE_REQUEST_TTL_MS);
    fn control_code_result_expires_at(clock: &str) -> String = add_ms(clock, CONTROL_CODE_RESULT_TTL_MS);
    fn valid_control_code_digits(value: &str) -> bool =
        (2..=8).contains(&value.len()) && value.chars().all(|c| c.is_ascii_digit());
    fn frame_ordinal_number(value: &str) -> i64 = value.trim().parse::<i64>().unwrap_or(0);
    fn operator_identity_is_valid(identity: &str) -> bool = identity.trim() == OPERATOR_IDENTITY;
    fn control_code_fast_state_id(ticket: &str, backend: &str) -> String = phone_row_id(ticket, backend);
    fn cleanup_remaining(limit: u32, deleted: u32) -> u32 =
        if limit == 0 { 0 } else { limit.saturating_sub(deleted) };
    fn stream_viewer_focus_expired(row: &TicketremoteStreamViewerFocus, clock: &str) -> bool =
        !row.active || parse_time_ms(&row.expiresAt) <= parse_time_ms(clock);
    fn control_code_terminal_failure_status(status: &str) -> bool =
        matches!(status, "failed" | "expired" | "closed");
    fn canonical_time(value: &str) -> String =
        iso(Timestamp::from_micros_since_unix_epoch(parse_time_micros(value)));
    fn account_public_id(email: &str) -> String = public_hash(&clean_email(email), 4);
    fn auth_config(ctx: &ReducerContext, ticket: &str) -> Option<TicketremoteAuthConfig> =
        ctx.db.ticketremote_auth_config().ticketId().find(clean_ticket_id(ticket));
    fn has_valid_service_identity(ctx: &ReducerContext) -> bool =
        jwt_payload(ctx).map(|claims| service_claims_are_valid(&claims)).unwrap_or(false);
    fn is_member(ctx: &ReducerContext, ticket: &str, email: &str) -> bool =
        ctx.db.ticketremote_ticket_member().id().find(member_id(ticket, email))
            .map(|row| row.active).unwrap_or(false);
    fn is_admin(ctx: &ReducerContext, ticket: &str, email: &str) -> bool =
        ctx.db.ticketremote_ticket_member().id().find(member_id(ticket, email))
            .map(|row| row.active && matches!(row.role.as_str(), "owner" | "admin"))
            .unwrap_or(false);
    fn stream_desired_core_equal(row: &TicketremoteStreamDesiredState,
        active: bool, viewers: u32) -> bool = row.desiredActive == active && row.viewerCount == viewers;
    fn control_code_cleanup_preserves_terminal_failure(existing: &TicketremoteControlCodeRequest,
        status: &str, reason: &str) -> bool = control_code_terminal_failure_status(&existing.status)
            && status == existing.status && control_code_cleanup_reason(reason);
    fn updated_ordinal(value: &str, fallback: &str) -> Option<String> =
        Some(bounded_frame_ordinal(&non_empty(value, fallback)));
    fn optional_text(value: String) -> Option<String> = (!value.is_empty()).then_some(value);
    fn control_code_request_same_payload(left: &TicketremoteControlCodeRequest,
        right: &TicketremoteControlCodeRequest) -> bool = same_fields!(left, right;
            status, reason, message, resultProof, resultProofAt, captureRequired,
            captureAcknowledged, cleanupPending, streamEpoch, frameSequence, minFrameSequence,
            resultFrameEpoch, resultMinFrameSequence, captureFrameEpoch, captureFrameSequence);
    fn now_or(ctx: &ReducerContext, value: &str) -> String = {
        let value = value.trim();
        if value.is_empty() { now(ctx) } else { value.into() }
    };
    fn iso(timestamp: Timestamp) -> String =
        timestamp.to_rfc3339().unwrap_or_else(|_| "1970-01-01T00:00:00Z".into());
    fn add_ms(value: &str, ms: i64) -> String = iso(Timestamp::from_micros_since_unix_epoch(
        parse_time_micros(value).saturating_add(ms.saturating_mul(1000))));
    fn json_i64(value: &serde_json::Value) -> Option<i64> = value.as_i64()
        .or_else(|| value.as_u64().and_then(|raw| i64::try_from(raw).ok()))
        .or_else(|| value.as_str().and_then(|raw| raw.trim().parse().ok()));
    fn json_str(value: &serde_json::Value, key: &str) -> String = value.get(key)
        .and_then(|raw| raw.as_str()).map(|raw| raw.trim().to_ascii_lowercase())
        .unwrap_or_default();
    fn clean_role(value: &str) -> String = allowlisted(value, &["owner", "admin"], "member");
    fn non_empty(value: &str, fallback: &str) -> String = {
        let value = value.trim();
        if value.is_empty() { fallback.into() } else { value.into() }
    };
    fn safe_token(value: &str, fallback: &str) -> String = non_empty(value, fallback)
        .replace(|c: char| !c.is_ascii_alphanumeric() && c != '_' && c != '-', "_");
    fn safe_json_string(value: &str, max: usize) -> String = {
        let raw = match value.trim() { "" => "{}", value => value };
        let valid = serde_json::from_str::<serde_json::Value>(raw).is_ok();
        (if valid { raw } else { "{}" }).chars().take(max).collect()
    };
    fn command_expires_at(clock: &str, ttl: i64) -> String = add_ms(clock,
        if ttl <= 0 || ttl > HISTORY_TTL_MS { HISTORY_TTL_MS } else { ttl });
    fn clean_control_code_result_proof(value: &str) -> String = allowlisted(value,
        &["phone_root", "phone_visual", "phone_visual_root_confirmed",
        "phone_visual_raw_ticket_after_submit", "phone_root_image", "browser_frame"], "");
    fn bounded_frame_ordinal(value: &str) -> String = non_empty(
        &value.chars().filter(|c| c.is_ascii_digit()).take(24).collect::<String>(), "0");
    fn connection_session_id(ctx: &ReducerContext) -> String = ctx.connection_id()
        .map(|id| format!("{id:?}")).unwrap_or_else(|| ctx.sender().to_string());
    fn control_code_cleanup_reason(reason: &str) -> bool = matches!(reason,
        "ticket_detail" | "return_to_raw_complete" | "browser_capture_confirmed"
        | "control_code_cleanup_attention_needed" | "rs_monthly_ticket_cleanup_attention_needed");
    fn control_code_result_marker(request: &TicketremoteControlCodeRequest) -> (String, String) = {
        let epoch = if request.resultFrameEpoch != "0" { &request.resultFrameEpoch }
            else { &request.streamEpoch };
        let sequence = if request.resultMinFrameSequence != "0" { &request.resultMinFrameSequence }
            else if request.minFrameSequence != "0" { &request.minFrameSequence }
            else { &request.frameSequence };
        (bounded_frame_ordinal(epoch), bounded_frame_ordinal(sequence))
    };
    fn control_code_close_is_idempotent(request: Option<&TicketremoteControlCodeRequest>) -> bool =
        request.is_none_or(|row| control_code_terminal_failure_status(&row.status)
            || (row.status == "succeeded" && row.captureAcknowledged));
    fn allowlisted(value: &str, allowed: &[&str], fallback: &str) -> String = {
        let value = value.trim();
        if allowed.contains(&value) { value.into() } else { fallback.into() }
    };
    fn json_object(pairs: &[(&str, &str)]) -> String = serde_json::Value::Object(
        pairs.iter().map(|(key, value)| ((*key).into(), (*value).into())).collect()).to_string();
    fn stable_stamp(value: &str) -> String = non_empty(
        &value.chars().filter(|c| c.is_ascii_alphanumeric()).collect::<String>(), "time");
    fn control_code_request_id(ticket: &str, session: &str, clock: &str) -> String =
        format!("{}:{}:{}:control_code", clean_ticket_id(ticket), session.trim(), stable_stamp(clock));
    fn jwt_payload(ctx: &ReducerContext) -> Result<serde_json::Value, String> = {
        let Some(jwt) = ctx.sender_auth().jwt() else { return Err("auth required".into()); };
        serde_json::from_str(jwt.raw_payload()).map_err(|_| "invalid auth payload".into())
    };
    fn service_claims_are_valid(payload: &serde_json::Value) -> bool = payload.get("iss")
        .and_then(|value| value.as_str()).map(str::trim) == Some(SERVICE_OIDC_ISSUER)
        && jwt_audience_includes(payload, SERVICE_OIDC_AUDIENCE)
        && payload.get("sub").and_then(|value| value.as_str()).map(str::trim)
            == Some(SERVICE_OIDC_SUBJECT)
        && jwt_roles_include(payload, SERVICE_ROLE);
    fn member_proxy_claims_are_valid(payload: &serde_json::Value, email: &str) -> bool = payload.get("iss")
        .and_then(|value| value.as_str()).map(str::trim) == Some(SERVICE_OIDC_ISSUER)
        && jwt_audience_includes(payload, SERVICE_OIDC_AUDIENCE)
        && payload.get("sub").and_then(|value| value.as_str()).map(str::trim)
            == Some(format!("member:{email}").as_str())
        && jwt_roles_include(payload, MEMBER_PROXY_ROLE);
    fn require_service(ctx: &ReducerContext) -> Result<(), String> = has_valid_service_identity(ctx)
        .then_some(()).ok_or_else(|| "service role required".into());
    fn service_ticket_id_for_viewer(ctx: &ViewContext) -> Option<String> = ctx.db
        .ticketremote_service_identity().identity().filter(&ctx.sender()).next()
        .map(|row| clean_ticket_id(&row.ticketId));
    fn service_member_from_row(row: &TicketremoteTicketMember) -> TicketremoteServiceMember = {
        let email = clean_email(&row.email);
        TicketremoteServiceMember { id: row.id.clone(), ticketId: row.ticketId.clone(),
            email: email.clone(), publicId: account_public_id(&email), role: clean_role(&row.role),
            active: row.active, updatedAt: row.updatedAt.clone() }
    };
    fn public_hash(value: &str, len: usize) -> String = {
        let mut out = to_base36(fnv32(value.trim()));
        if out.len() < len { out = format!("{:0>width$}", out, width = len); }
        out.chars().take(len).collect()
    };
    fn require_admin(ctx: &ReducerContext, ticket: &str, email: &str) -> Result<(), String> =
        is_admin(ctx, ticket, email).then_some(()).ok_or_else(|| "forbidden".into());
    fn jwt_audience_includes(payload: &serde_json::Value, expected: &str) -> bool = {
        let expected = expected.trim();
        if expected.is_empty() { return false; }
        match payload.get("aud") {
            Some(serde_json::Value::String(value)) => value.trim() == expected,
            Some(serde_json::Value::Array(values)) =>
                values.iter().any(|value| value.as_str().is_some_and(|raw| raw.trim() == expected)),
            _ => false,
        }
    };
    fn jwt_roles_include(payload: &serde_json::Value, expected: &str) -> bool = match payload.get("roles") {
        Some(serde_json::Value::String(value)) => value.split(',').any(|raw| raw.trim() == expected),
        Some(serde_json::Value::Array(values)) =>
            values.iter().any(|value| value.as_str().is_some_and(|raw| raw.trim() == expected)),
        _ => false,
    };
    fn control_code_fast_state_expires_at(status: &str, clock: &str) -> String = add_ms(clock,
        if status == "fast_ready" { CONTROL_CODE_FAST_READY_TTL_MS }
        else { CONTROL_CODE_FAST_STATE_TTL_MS });
    fn clean_control_code_fast_status(value: &str) -> String = allowlisted(&non_empty(value, "stale"),
        &["fast_ready", "warming", "cleanup", "blocked", "stale"], "blocked");
    fn control_code_request_occupies_phone(row: &TicketremoteControlCodeRequest, clock: &str) -> bool = {
        if parse_time_ms(&row.expiresAt) <= parse_time_ms(clock) { return false; }
        if matches!(row.status.as_str(), "closed" | "expired" | "failed") { return false; }
        matches!(row.status.as_str(), "queued" | "running")
            || (row.status == "succeeded" && (row.cleanupPending
                || (row.captureRequired && !row.captureAcknowledged)))
    };
    fn control_code_request_ttl_is_healthy(row: &TicketremoteControlCodeRequest, clock: &str) -> bool = {
        let now = parse_time_ms(clock);
        if parse_time_ms(&row.expiresAt).saturating_sub(now) <= CONTROL_CODE_COMMAND_TTL_MS / 2 {
            return false;
        }
        row.status != "succeeded" || parse_time_ms(&row.resultExpiresAt).saturating_sub(now)
            > CONTROL_CODE_RESULT_TTL_MS / 2
    };
    fn fnv32(value: &str) -> u32 = value.as_bytes().iter().fold(0x811c9dc5, |hash, byte|
        (hash ^ *byte as u32).wrapping_mul(0x01000193));
    fn refresh_touched_signals(ctx: &ReducerContext, ticket: &str,
        backends: &[String], clock: &str) -> () = for backend in backends {
            upsert_stream_command_signal(ctx, ticket, backend, clock, clock);
        };
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
    if !report.desiredActive || !matches!(report.streamState.trim(), "streaming" | "live") {
        return false;
    }
    let report_age_ms = parse_time_ms(now).saturating_sub(parse_time_ms(&report.updatedAt));
    if !(0..=STREAM_BACKGROUND_REPORT_MAX_AGE_MS).contains(&report_age_ms) {
        return false;
    }
    let Ok(status) = serde_json::from_str::<serde_json::Value>(&report.statusJson) else {
        return false;
    };
    if status.get("streamActive").and_then(|value| value.as_bool()) == Some(false)
        || status
            .get("hardwareH264Active")
            .and_then(|value| value.as_bool())
            == Some(false)
    {
        return false;
    }
    if ["sessionState", "relayStreamState"]
        .iter()
        .map(|key| json_str(&status, key))
        .any(|state| !state.is_empty() && !matches!(state.as_str(), "live" | "streaming"))
        || {
            let state = json_str(&status, "hardwareH264Visibility");
            !state.is_empty() && state != "visible"
        }
    {
        return false;
    }
    let watchdog_stage = json_str(&status, "streamWatchdogStage");
    if ["recover", "restart", "fail"]
        .iter()
        .any(|token| watchdog_stage.contains(token))
    {
        return false;
    }
    ["activeVideoClients", "videoClients", "relayViewers"]
        .iter()
        .find_map(|key| status.get(key).and_then(json_i64))
        .unwrap_or(0)
        > 0
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

fn register_service_identity(ctx: &ReducerContext, ticket_id: String, now: &str) {
    let id = ctx.sender().to_string();
    let table = ctx.db.ticketremote_service_identity();
    if let Some(existing) = table.id().find(&id) {
        table.id().update(TicketremoteServiceIdentity {
            ticketId: ticket_id,
            updatedAt: now.into(),
            ..existing
        });
    } else {
        table.insert(TicketremoteServiceIdentity {
            id,
            identity: ctx.sender(),
            ticketId: ticket_id,
            createdAt: now.into(),
            updatedAt: now.into(),
        });
    }
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
    let email = clean_email(payload.get("email").and_then(|v| v.as_str()).unwrap_or(""));
    if email.is_empty() || payload.get("email_verified").and_then(|v| v.as_bool()) != Some(true) {
        return Err("verified email required".into());
    }
    let public_auth = issuer == config.issuer.trim().trim_end_matches('/')
        && jwt_audience_includes(&payload, &config.audience);
    if !public_auth && !member_proxy_claims_are_valid(&payload, &email) {
        return Err("invalid member authentication".into());
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
    let table = ctx.db.ticketremote_ticket();
    if let Some(existing) = table.id().find(&id) {
        if !display_name.trim().is_empty() && existing.displayName != display_name.trim() {
            let updated = TicketremoteTicket {
                displayName: display_name.trim().into(),
                updatedAt: now.into(),
                ..existing
            };
            table.id().update(updated.clone());
            return updated;
        }
        return existing;
    }
    let ticket = TicketremoteTicket {
        id,
        displayName: non_empty(display_name, DEFAULT_TICKET_NAME),
        createdAt: now.into(),
        updatedAt: now.into(),
    };
    table.insert(ticket.clone());
    ticket
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

fn upsert_member_row(ctx: &ReducerContext, ticket_id: &str, email: &str, role: &str, now: &str) {
    let email = clean_email(email);
    if email.is_empty() {
        return;
    }
    let id = member_id(ticket_id, &email);
    let table = ctx.db.ticketremote_ticket_member();
    let created_at = table
        .id()
        .find(&id)
        .map(|row| {
            table.id().delete(&id);
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
    table.insert(row);
}

fn deactivate_member_row(ctx: &ReducerContext, ticket_id: &str, email: &str, now: &str) {
    let id = member_id(ticket_id, email);
    let table = ctx.db.ticketremote_ticket_member();
    if let Some(existing) = table.id().find(&id) {
        table.id().update(TicketremoteTicketMember {
            active: false,
            updatedAt: now.into(),
            ..existing
        });
    }
}

fn schedule_latest_ticket_reselect(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    schedule_id: &str,
    scheduled_at_micros: i64,
    phone_local_time: &str,
    phone_time_zone: &str,
    requested_by: &str,
    now: &str,
) -> Result<(), String> {
    let ticket = ensure_ticket(ctx, ticket_id, "", now);
    let backend_id = clean_backend_id(backend_id);
    let schedule_id = schedule_id.trim();
    if !valid_schedule_identifier(schedule_id) {
        return Err("invalid_schedule_id".into());
    }
    let requested_by = requested_by.trim();
    if !valid_public_identifier(requested_by) {
        return Err("invalid_requested_by".into());
    }
    let phone_local_time = bounded_text(phone_local_time.trim(), 80);
    let phone_time_zone = bounded_text(phone_time_zone.trim(), 80);
    if phone_local_time.is_empty() || phone_time_zone.is_empty() {
        return Err("phone_local_time_required".into());
    }
    let scheduled_at = iso(Timestamp::from_micros_since_unix_epoch(scheduled_at_micros));
    let table = ctx.db.ticketremote_latest_ticket_reselect_schedule();
    if let Some(existing) = table.id().find(schedule_id.to_string()) {
        if latest_ticket_reselect_submission_matches(
            &existing,
            &ticket.id,
            &backend_id,
            &scheduled_at,
            &phone_local_time,
            &phone_time_zone,
            requested_by,
        ) {
            return if latest_ticket_reselect_idempotent_status(&existing.status) {
                Ok(())
            } else {
                Err("schedule_id_not_reusable".into())
            };
        }
        return Err("schedule_id_conflict".into());
    }
    if scheduled_at_micros <= ctx.timestamp.to_micros_since_unix_epoch() {
        return Err("scheduled_time_must_be_future".into());
    }
    if scheduled_at_micros.saturating_sub(ctx.timestamp.to_micros_since_unix_epoch())
        > LATEST_TICKET_RESELECT_MAX_HORIZON_MS.saturating_mul(1000)
    {
        return Err("scheduled_time_too_far".into());
    }
    if table
        .ticketBackendStatus()
        .filter((&ticket.id, &backend_id, "queued"))
        .next()
        .is_some()
        || table
            .ticketBackendStatus()
            .filter((&ticket.id, &backend_id, "running"))
            .next()
            .is_some()
    {
        return Err("latest_ticket_reselect_already_in_progress".into());
    }

    let pending: Vec<_> = table
        .ticketBackendStatus()
        .filter((&ticket.id, &backend_id, "pending"))
        .collect();
    for existing in pending {
        delete_latest_ticket_reselect_timers(ctx, &existing.id);
        table.id().update(TicketremoteLatestTicketReselectSchedule {
            status: "replaced".into(),
            resultReason: "replaced_by_new_schedule".into(),
            resultPhase: "replaced".into(),
            proofSource: "admin".into(),
            updatedAt: now.into(),
            completedAt: now.into(),
            expiresAt: add_ms(now, HISTORY_TTL_MS),
            ..existing
        });
    }

    table.insert(TicketremoteLatestTicketReselectSchedule {
        id: schedule_id.into(),
        ticketId: ticket.id.clone(),
        backendId: backend_id.clone(),
        scheduledAt: scheduled_at.clone(),
        phoneLocalTime: phone_local_time,
        phoneTimeZone: phone_time_zone,
        status: "pending".into(),
        commandId: String::new(),
        resultReason: String::new(),
        resultPhase: String::new(),
        proofSource: String::new(),
        requestedBy: requested_by.into(),
        createdAt: now.into(),
        updatedAt: now.into(),
        triggeredAt: String::new(),
        completedAt: String::new(),
        expiresAt: add_ms(&scheduled_at, HISTORY_TTL_MS),
    });
    ctx.db.ticketremote_latest_ticket_reselect_timer().insert(
        TicketremoteLatestTicketReselectTimer {
            scheduled_id: 0,
            scheduled_at: ScheduleAt::Time(Timestamp::from_micros_since_unix_epoch(
                scheduled_at_micros,
            )),
            ticketId: ticket.id,
            backendId: backend_id,
            scheduleId: schedule_id.into(),
            createdAt: now.into(),
        },
    );
    Ok(())
}

fn cancel_latest_ticket_reselect(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    schedule_id: &str,
    now: &str,
) -> Result<(), String> {
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    let schedule_id = schedule_id.trim();
    if !valid_schedule_identifier(schedule_id) {
        return Err("invalid_schedule_id".into());
    }
    let table = ctx.db.ticketremote_latest_ticket_reselect_schedule();
    let Some(existing) = table.id().find(schedule_id.to_string()) else {
        return Ok(());
    };
    if existing.ticketId != ticket_id || existing.backendId != backend_id {
        return Err("schedule_mismatch".into());
    }
    if existing.status == "canceled" {
        return Ok(());
    }
    if existing.status != "pending" {
        return Err("schedule_not_pending".into());
    }
    delete_latest_ticket_reselect_timers(ctx, schedule_id);
    table.id().update(TicketremoteLatestTicketReselectSchedule {
        status: "canceled".into(),
        resultReason: "canceled_by_admin".into(),
        resultPhase: "canceled".into(),
        proofSource: "admin".into(),
        updatedAt: now.into(),
        completedAt: now.into(),
        expiresAt: add_ms(now, HISTORY_TTL_MS),
        ..existing
    });
    Ok(())
}

fn trigger_scheduled_latest_ticket_reselect(
    ctx: &ReducerContext,
    timer: &TicketremoteLatestTicketReselectTimer,
) -> Result<(), String> {
    let table = ctx.db.ticketremote_latest_ticket_reselect_schedule();
    let Some(existing) = table.id().find(&timer.scheduleId) else {
        return Ok(());
    };
    if existing.status != "pending"
        || existing.ticketId != timer.ticketId
        || existing.backendId != timer.backendId
        || !table
            .ticketBackendStatus()
            .filter((&timer.ticketId, &timer.backendId, "pending"))
            .any(|row| row.id == timer.scheduleId)
    {
        return Ok(());
    }
    let now = now(ctx);
    let command_id =
        latest_ticket_reselect_command_id(&existing.ticketId, &existing.backendId, &existing.id);
    let payload = serde_json::json!({
        "type": "force_ticket_reselect",
        "source": "ticket_remote_schedule",
        "reason": "scheduled_latest_ticket_reselect",
        "backendId": existing.backendId,
        "scheduleId": existing.id,
    })
    .to_string();
    let command = insert_stream_command(
        ctx,
        &existing.ticketId,
        &existing.backendId,
        &command_id,
        "force_ticket_reselect",
        &format!("schedule:{}", existing.id),
        "scheduled_latest_ticket_reselect",
        &payload,
        LATEST_TICKET_RESELECT_COMMAND_TTL_MS,
        &now,
    );
    table.id().update(TicketremoteLatestTicketReselectSchedule {
        status: "queued".into(),
        commandId: command.id,
        resultReason: "scheduled_triggered".into(),
        resultPhase: "queued".into(),
        proofSource: "spacetimedb_scheduler".into(),
        updatedAt: now.clone(),
        triggeredAt: now.clone(),
        expiresAt: add_ms(&now, HISTORY_TTL_MS),
        ..existing
    });
    Ok(())
}

fn delete_latest_ticket_reselect_timers(ctx: &ReducerContext, schedule_id: &str) {
    let table = ctx.db.ticketremote_latest_ticket_reselect_timer();
    let rows: Vec<_> = table.scheduleId().filter(schedule_id).collect();
    for row in rows {
        table.scheduled_id().delete(row.scheduled_id);
    }
}

fn latest_ticket_reselect_submission_matches(
    row: &TicketremoteLatestTicketReselectSchedule,
    ticket_id: &str,
    backend_id: &str,
    scheduled_at: &str,
    phone_local_time: &str,
    phone_time_zone: &str,
    requested_by: &str,
) -> bool {
    row.ticketId == ticket_id
        && row.backendId == backend_id
        && row.scheduledAt == scheduled_at
        && row.phoneLocalTime == phone_local_time
        && row.phoneTimeZone == phone_time_zone
        && row.requestedBy == requested_by
}

fn latest_ticket_reselect_idempotent_status(status: &str) -> bool {
    !matches!(status, "canceled" | "replaced")
}

fn latest_ticket_reselect_command_id(
    ticket_id: &str,
    backend_id: &str,
    schedule_id: &str,
) -> String {
    format!(
        "{}:{}:scheduled_latest_ticket_reselect:{}",
        clean_ticket_id(ticket_id),
        clean_backend_id(backend_id),
        schedule_id.trim()
    )
}

fn valid_schedule_identifier(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 120
        && value
            .chars()
            .all(|ch| ch.is_ascii_alphanumeric() || matches!(ch, '_' | '-' | ':'))
}

fn valid_public_identifier(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 64
        && value
            .chars()
            .all(|ch| ch.is_ascii_alphanumeric() || matches!(ch, '_' | '-'))
}

fn ensure_cleanup_schedule(ctx: &ReducerContext, ticket_id: &str, now: &str) {
    let schedule =
        ScheduleAt::Interval(std::time::Duration::from_secs(CLEANUP_INTERVAL_SECS).into());
    let table = ctx.db.ticketremote_cleanup_schedule();
    if let Some(existing) = table.ticketId().filter(ticket_id).next() {
        table.scheduled_id().update(TicketremoteCleanupSchedule {
            scheduled_at: schedule,
            batchSize: CLEANUP_BATCH_SIZE,
            updatedAt: now.into(),
            ..existing
        });
    } else {
        table.insert(TicketremoteCleanupSchedule {
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
    let table = ctx.db.ticketremote_phone_backend();
    let existing = table.id().find(&id);
    if existing.as_ref().is_some_and(|row| {
        row.attachName == attach_name
            && row.baseUrl == base_url.trim()
            && row.desiredState == desired_state
            && row.streamState == stream_state
            && row.healthJson == health_json
            && row.lastError == last_error
            && parse_time_ms(now).saturating_sub(parse_time_ms(&row.lastSeenAt))
                < PHONE_KEEPALIVE_MS
    }) {
        return;
    }
    if existing.is_some() {
        table.id().delete(&id);
    }
    table.insert(TicketremotePhoneBackend {
        id,
        ticketId: ticket.id,
        backendId: backend_id,
        attachName: attach_name,
        baseUrl: base_url.trim().into(),
        desiredState: desired_state,
        streamState: stream_state,
        healthJson: health_json.into(),
        lastError: last_error.into(),
        lastSeenAt: now.into(),
    });
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
        ctx.db.ticketremote_stream_viewer_focus().id().delete(&id);
        return;
    }
    upsert_row!(
        ctx,
        ticketremote_stream_viewer_focus,
        TicketremoteStreamViewerFocus {
            id,
            ticketId: ticket_id,
            backendId: backend_id,
            publicId: public_id,
            active: true,
            lastSeenAt: now.into(),
            expiresAt: stream_viewer_focus_expires_at(now),
        }
    );
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
        revision: non_empty(revision, now),
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
    if let Some(existing) = ctx.db.ticketremote_stream_desired_state().id().find(&id)
        && same_fields!(existing, row; desiredActive, viewerCount, reason, revision, updatedBy)
    {
        return existing;
    }
    let row = upsert_row!(ctx, ticketremote_stream_desired_state, row);
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
    let clean_revision = non_empty(revision, now);
    let row = TicketremoteStreamCommandSignal {
        id: id.clone(),
        ticketId: clean_ticket_id(ticket_id),
        backendId: clean_backend_id(backend_id),
        revision: clean_revision,
        pendingCount: pending_count,
        updatedAt: now.into(),
    };
    if let Some(existing) = ctx.db.ticketremote_stream_command_signal().id().find(&id)
        && same_fields!(existing, row; pendingCount, revision)
    {
        return;
    }
    upsert_row!(ctx, ticketremote_stream_command_signal, row);
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
    let command_type = safe_token(command_type, "unknown");
    let table = ctx.db.ticketremote_stream_command();
    if matches!(
        command_type.as_str(),
        "start" | "keyframe" | "recover_stream"
    ) {
        let now_ms = parse_time_ms(now);
        if let Some(existing) = table
            .ticketBackendStatus()
            .filter((&ticket.id, &backend_id, "pending"))
            .find(|row| row.commandType == command_type && parse_time_ms(&row.expiresAt) > now_ms)
        {
            return existing;
        }
    }
    let revision = non_empty(revision, now);
    let id = non_empty(
        command_id,
        &format!("{}:{}:{}:{}", ticket.id, backend_id, revision, command_type),
    );
    if let Some(existing) = table.id().find(&id) {
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
    table.insert(row.clone());
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
    let table = ctx.db.ticketremote_stream_command();
    let Some(existing) = table.id().find(&command_key) else {
        return;
    };
    let status = safe_token(status, "acknowledged");
    let scheduled_reselect = latest_ticket_reselect_schedule_for_command(ctx, &existing.id);
    if status == "acknowledged" {
        if scheduled_reselect.is_some() {
            update_latest_ticket_reselect_result(
                ctx,
                &existing.id,
                "succeeded",
                &bounded_text(&non_empty(reason, "ready"), 240),
                "ready",
                "phone_worker",
                now,
                true,
            );
        }
        table.id().delete(&existing.id);
        upsert_stream_command_signal(
            ctx,
            &existing.ticketId,
            &existing.backendId,
            &existing.revision,
            now,
        );
        return;
    }
    if status == "dispatched" {
        if scheduled_reselect.is_some() {
            update_latest_ticket_reselect_result(
                ctx,
                &existing.id,
                "running",
                &bounded_text(&non_empty(reason, "dispatched"), 240),
                "running",
                "phone_worker",
                now,
                false,
            );
            table.id().update(TicketremoteStreamCommand {
                status,
                reason: bounded_text(&non_empty(reason, &existing.reason), 240),
                updatedAt: now.into(),
                ..existing.clone()
            });
        } else {
            table.id().delete(&existing.id);
        }
        upsert_stream_command_signal(
            ctx,
            &existing.ticketId,
            &existing.backendId,
            &existing.revision,
            now,
        );
        return;
    }
    if status == "failed" && scheduled_reselect.is_some() {
        update_latest_ticket_reselect_result(
            ctx,
            &existing.id,
            "failed",
            &bounded_text(&non_empty(reason, "failed"), 240),
            "failed",
            "phone_worker",
            now,
            true,
        );
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
    table.id().update(row);
    upsert_stream_command_signal(
        ctx,
        &existing.ticketId,
        &existing.backendId,
        &existing.revision,
        now,
    );
}

fn latest_ticket_reselect_schedule_for_command(
    ctx: &ReducerContext,
    command_id: &str,
) -> Option<TicketremoteLatestTicketReselectSchedule> {
    ctx.db
        .ticketremote_latest_ticket_reselect_schedule()
        .commandId()
        .filter(command_id)
        .find(|row| matches!(row.status.as_str(), "queued" | "running"))
}

fn update_latest_ticket_reselect_result(
    ctx: &ReducerContext,
    command_id: &str,
    status: &str,
    result_reason: &str,
    result_phase: &str,
    proof_source: &str,
    now: &str,
    terminal: bool,
) {
    let table = ctx.db.ticketremote_latest_ticket_reselect_schedule();
    let rows: Vec<_> = table
        .commandId()
        .filter(command_id)
        .filter(|row| matches!(row.status.as_str(), "queued" | "running"))
        .collect();
    for existing in rows {
        table.id().update(TicketremoteLatestTicketReselectSchedule {
            status: status.into(),
            resultReason: bounded_text(result_reason, 240),
            resultPhase: safe_token(result_phase, status),
            proofSource: safe_token(proof_source, ""),
            updatedAt: now.into(),
            completedAt: if terminal { now.into() } else { String::new() },
            expiresAt: add_ms(now, HISTORY_TTL_MS),
            ..existing
        });
    }
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
        streamState: non_empty(stream_state, "idle"),
        desiredActive: desired_active,
        lastCommandId: last_command_id.trim().into(),
        lastCommandRevision: last_command_revision.trim().into(),
        statusJson: safe_json_string(status_json, SAFE_JSON_MAX_BYTES),
        updatedAt: now.into(),
    };
    if let Some(existing) = ctx.db.ticketremote_phone_current_report().id().find(&id)
        && same_fields!(existing, row; streamState, desiredActive, lastCommandId, lastCommandRevision, statusJson)
    {
        return;
    }
    upsert_row!(ctx, ticketremote_phone_current_report, row);
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
        streamVerdict: safe_token(stream_verdict, "unknown"),
        lastFrameAgoMillis: 0,
        framesForwarded: non_empty(frames_forwarded, "0"),
        statusJson: safe_json_string(status_json, SAFE_JSON_MAX_BYTES),
        updatedAt: now.into(),
        lastFrameAt: Some(bounded_text(last_frame_at.trim(), 80)),
    };
    if let Some(existing) = ctx.db.ticketremote_relay_current_report().id().find(&id)
        && same_fields!(existing, row; videoClients, streamVerdict, lastFrameAgoMillis, lastFrameAt, framesForwarded, statusJson)
    {
        return;
    }
    upsert_row!(ctx, ticketremote_relay_current_report, row);
}

fn delete_control_code_request(ctx: &ReducerContext, request_id: &str) {
    let id = request_id.to_string();
    ctx.db.ticketremote_control_code_request().id().delete(&id);
    ctx.db.ticketremote_control_code_owner().id().delete(&id);
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
    let table = ctx.db.ticketremote_control_code_fast_state();
    let existing = table.id().find(&id);
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
        if same_fields!(existing, row;
            ticketId, backendId, status, revision, reason, preparedAt, streamEpoch,
            frameSequence, rawTicketConfirmed, cleanupClear, streamLive
        ) && remaining_ms > ttl_ms / 2
        {
            return existing;
        }
    }
    if table.id().find(&id).is_some() {
        table.id().update(row.clone());
    } else {
        table.insert(row.clone());
    }
    row
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
        expiresAt: control_code_request_expires_at(now),
        streamEpoch: "0".into(),
        frameSequence: "0".into(),
        minFrameSequence: "0".into(),
        resultFrameEpoch: "0".into(),
        resultMinFrameSequence: "0".into(),
        captureFrameEpoch: "0".into(),
        captureFrameSequence: "0".into(),
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
    let table = ctx.db.ticketremote_control_code_request();
    let Some(mut row) = table.id().find(request_id.to_string()) else {
        return;
    };
    let existing = row.clone();
    apply_changes!(row, changes;
        status, reason, message, resultExpiresAt, captureRequired, captureAcknowledged,
        cleanupPending, streamEpoch, frameSequence, minFrameSequence, resultFrameEpoch,
        resultMinFrameSequence, captureFrameEpoch, captureFrameSequence, expiresAt
    );
    if let Some(value) = changes.resultProof {
        row.resultProof = Some(value);
    }
    if let Some(value) = changes.resultProofAt {
        row.resultProofAt = Some(value);
    }
    row.updatedAt = now.into();
    if control_code_request_same_payload(&existing, &row)
        && control_code_request_ttl_is_healthy(&existing, now)
    {
        return;
    }
    table.id().update(row);
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

    if deleted < limit {
        let stream_command_deleted = purge_expired_stream_commands_for_ticket(
            ctx,
            &ticket.id,
            now,
            cleanup_remaining(limit, deleted),
        );
        deleted += stream_command_deleted;
    }
    if deleted < limit {
        let viewer_focus_deleted = purge_expired_stream_viewer_focus_for_ticket(
            ctx,
            &ticket.id,
            now,
            cleanup_remaining(limit, deleted),
        );
        deleted += viewer_focus_deleted;
    }
    purge_ticket_history!(ctx, &ticket.id, expiry_bound.as_str(), limit, deleted);
    deleted
}

ticket_expiry_purgers! {
    purge_expired_stream_viewer_focus_for_ticket(ticketremote_stream_viewer_focus)
        |ctx, ticket, touched, now| {
        for backend in &touched {
            refresh_stream_desired_from_viewer_focus(ctx, &ticket, backend, now,
                "viewer_focus_expired");
        }
    }
}

fn purge_expired_stream_commands_for_ticket(
    ctx: &ReducerContext,
    ticket_id: &str,
    now: &str,
    batch_size: u32,
) -> u32 {
    let ticket_id = clean_ticket_id(ticket_id);
    let expiry = canonical_time(now);
    let table = ctx.db.ticketremote_stream_command();
    let rows: Vec<_> = table
        .ticketExpiresAt()
        .filter((&ticket_id, ..=expiry.as_str()))
        .take(batch_size as usize)
        .collect();
    let mut touched = Vec::<String>::new();
    for row in &rows {
        if !touched.contains(&row.backendId) {
            touched.push(row.backendId.clone());
        }
        update_latest_ticket_reselect_result(
            ctx,
            &row.id,
            "expired",
            "command_expired",
            "expired",
            "spacetimedb_command_ttl",
            now,
            true,
        );
        table.id().delete(&row.id);
    }
    refresh_touched_signals(ctx, &ticket_id, &touched, now);
    rows.len().min(u32::MAX as usize) as u32
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
        .filter(|row| stream_viewer_focus_expired(row, now))
        .take(batch_size as usize)
        .collect();
    for row in &rows {
        ctx.db
            .ticketremote_stream_viewer_focus()
            .id()
            .delete(&row.id);
    }
    rows.len().min(u32::MAX as usize) as u32
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

    fn stream_command(command_type: &str, reason: &str) -> TicketremoteStreamCommand {
        TicketremoteStreamCommand {
            id: format!("command-{command_type}"),
            ticketId: "vivi-default".into(),
            backendId: "pixel".into(),
            commandType: command_type.into(),
            status: "pending".into(),
            revision: "revision-1".into(),
            reason: reason.into(),
            payloadJson: "{}".into(),
            createdAt: "2026-07-10T12:00:00Z".into(),
            updatedAt: "2026-07-10T12:00:00Z".into(),
            expiresAt: "2026-07-10T12:05:00Z".into(),
        }
    }

    fn latest_ticket_reselect_schedule() -> TicketremoteLatestTicketReselectSchedule {
        TicketremoteLatestTicketReselectSchedule {
            id: "schedule-1".into(),
            ticketId: "vivi-default".into(),
            backendId: "pixel".into(),
            scheduledAt: "2026-07-23T15:00:00Z".into(),
            phoneLocalTime: "2026-07-23T18:00".into(),
            phoneTimeZone: "Europe/Riga".into(),
            status: "pending".into(),
            commandId: String::new(),
            resultReason: String::new(),
            resultPhase: String::new(),
            proofSource: String::new(),
            requestedBy: "1on9".into(),
            createdAt: "2026-07-23T12:00:00Z".into(),
            updatedAt: "2026-07-23T12:00:00Z".into(),
            triggeredAt: String::new(),
            completedAt: String::new(),
            expiresAt: "2026-07-23T21:00:00Z".into(),
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
    fn latest_ticket_reselect_submission_is_strictly_idempotent() {
        let mut row = latest_ticket_reselect_schedule();
        assert!(latest_ticket_reselect_submission_matches(
            &row,
            "vivi-default",
            "pixel",
            "2026-07-23T15:00:00Z",
            "2026-07-23T18:00",
            "Europe/Riga",
            "1on9",
        ));
        assert!(!latest_ticket_reselect_submission_matches(
            &row,
            "vivi-default",
            "pixel",
            "2026-07-23T15:01:00Z",
            "2026-07-23T18:01",
            "Europe/Riga",
            "1on9",
        ));
        assert!(latest_ticket_reselect_idempotent_status(&row.status));
        row.status = "canceled".into();
        assert!(!latest_ticket_reselect_idempotent_status(&row.status));
        row.status = "replaced".into();
        assert!(!latest_ticket_reselect_idempotent_status(&row.status));
        row.status = "succeeded".into();
        assert!(latest_ticket_reselect_idempotent_status(&row.status));
    }

    #[test]
    fn latest_ticket_reselect_ids_are_private_safe_and_deterministic() {
        assert!(valid_schedule_identifier("sched_20260723-abc:1"));
        assert!(!valid_schedule_identifier("member@example.com"));
        assert!(valid_public_identifier("1on9"));
        assert!(!valid_public_identifier("member@example.com"));
        assert_eq!(
            latest_ticket_reselect_command_id("vivi-default", "pixel", "schedule-1"),
            "vivi-default:pixel:scheduled_latest_ticket_reselect:schedule-1"
        );
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
    fn member_proxy_claims_pin_signer_role_subject_and_email() {
        let email = "member@example.com";
        let valid = serde_json::json!({
            "iss": SERVICE_OIDC_ISSUER, "aud": [SERVICE_OIDC_AUDIENCE],
            "sub": format!("member:{email}"), "roles": [MEMBER_PROXY_ROLE],
            "email": email, "email_verified": true,
        });
        assert!(member_proxy_claims_are_valid(&valid, email));
        for field in ["iss", "aud", "sub", "roles"] {
            let mut invalid = valid.clone();
            invalid[field] = serde_json::json!("wrong");
            assert!(
                !member_proxy_claims_are_valid(&invalid, email),
                "trusted {field}"
            );
        }
        assert!(!member_proxy_claims_are_valid(&valid, "other@example.com"));
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
        request.cleanupPending = true;
        assert!(!control_code_request_occupies_phone(&request, now));

        request.status = "running".into();
        request.expiresAt = now.into();
        assert!(!control_code_request_occupies_phone(&request, now));
    }

    #[test]
    fn cleanup_keeps_the_original_terminal_failure_detail() {
        let mut failed = control_request();
        failed.status = "failed".into();
        failed.reason = "control_code_submit_target_unproved".into();
        failed.message = "The phone could not prove the submit target".into();

        let preserve =
            control_code_cleanup_preserves_terminal_failure(&failed, "failed", "ticket_detail");
        assert!(preserve);
        let (reason, message) =
            control_code_update_text(&failed, "ticket_detail", "", false, preserve);
        assert_eq!(reason, "control_code_submit_target_unproved");
        assert_eq!(message, "The phone could not prove the submit target");

        assert!(!control_code_cleanup_preserves_terminal_failure(
            &failed,
            "failed",
            "control_code_submit_failed"
        ));
        assert!(!control_code_cleanup_preserves_terminal_failure(
            &failed,
            "running",
            "ticket_detail"
        ));
    }

    #[test]
    fn close_is_idempotent_after_a_terminal_failure() {
        assert!(control_code_close_is_idempotent(None));
        for status in ["failed", "expired", "closed"] {
            let mut request = control_request();
            request.status = status.into();
            assert!(control_code_close_is_idempotent(Some(&request)));
        }

        let mut request = control_request();
        request.status = "succeeded".into();
        assert!(!control_code_close_is_idempotent(Some(&request)));
        request.captureAcknowledged = true;
        assert!(control_code_close_is_idempotent(Some(&request)));
        request.status = "running".into();
        assert!(!control_code_close_is_idempotent(Some(&request)));
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
