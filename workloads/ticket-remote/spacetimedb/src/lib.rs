#![allow(non_snake_case)]
// Reducer parameters are the stable public SpacetimeDB contract, so grouping
// them solely to satisfy Clippy would break generated clients.
#![allow(clippy::too_many_arguments)]

use chrono::{DateTime, Datelike, Days, LocalResult, TimeZone, Timelike, Utc};
use chrono_tz::Europe::Riga;
use sha2::{Digest, Sha256};
use spacetimedb::{
    CaseConversionPolicy, Identity, ReducerContext, ScheduleAt, SpacetimeType, Table, Timestamp,
    ViewContext,
};

mod phone_control;
use phone_control::*;
mod command;
use command::require_legacy_phone_admission;

fn account_scope_id(email: &str) -> String {
    let normalized = email.trim().to_ascii_lowercase();
    Sha256::digest(normalized.as_bytes())
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}

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
const REGISTRATION_RATE_INTERVAL_MS: i64 = 30_000;
const REGISTRATION_RATE_LIMIT: usize = 10;
const REGISTRATION_RATE_WINDOW_MS: i64 = 60 * 60 * 1000;
const MEMBER_LIMIT_EVENT_TTL_MS: i64 = 30 * 24 * 60 * 60 * 1000;
const MEMBER_ACTIVITY_TICK_SLOT_MICROS: i64 = 5 * 1_000_000;
const MEMBER_ACTIVITY_RETENTION_DAYS: u64 = 37;
const MEMBER_ACTIVITY_HOURS_PER_DAY: usize = 24;
const CONTROL_CODE_REQUEST_TTL_MS: i64 = 5 * 60_000;
const CONTROL_CODE_RESULT_TTL_MS: i64 = 60_000;
const CONTROL_CODE_COMMAND_TTL_MS: i64 = 2 * 60_000;
const TICKET_ACTIVATION_COMMAND_TTL_MS: i64 = 10 * 60_000;
const LATEST_TICKET_RESELECT_COMMAND_TTL_MS: i64 = TICKET_ACTIVATION_COMMAND_TTL_MS;
const TICKET_INTERACTION_STALE_RESET_AFTER_MS: i64 = 2 * 60_000;
const LATEST_TICKET_RESELECT_MAX_HORIZON_MS: i64 = 90 * 24 * 60 * 60 * 1000;
const TICKET_INTERACTION_TTL_MS: i64 = 2 * 60 * 60 * 1000;
const TICKET_SLIDER_LEASE_MS: i64 = 3_000;
const TICKET_SLIDER_COOLDOWN_MS: i64 = 800;
const TICKET_SLIDER_QUALIFY_HOLD_MS: u32 = 80;
const TICKET_SLIDER_QUALIFY_TRAVEL_CSS: u32 = 10;
const TICKET_ACTIVATION_RESET_DELAY_MS: i64 = 60 * 60 * 1000;
const TICKET_ACTION_SWITCH_WINDOW_MS: i64 = 15 * 60 * 1000;
// Slider geometry is an ephemeral capability tied to one exact visual proof.
// The browser refreshes the non-mutating proof before this expires; raw phone
// coordinates never enter the public row.
const TICKET_SLIDER_REGION_V3_TTL_MS: i64 = 5 * 60 * 1000;
const SCHEDULED_REDETECT_RETRY_BASE_MS: i64 = 5_000;
const SCHEDULED_REDETECT_RETRY_MAX_MS: i64 = 60_000;
const TICKET_ACTIVATION_LEDGER_TTL_MS: i64 = 30 * 24 * 60 * 60 * 1000;
const TICKET_ACTIVATION_DECISION_TTL_MS: i64 = 10 * 60 * 1000;
const TICKET_ACTIVATION_CLEANUP_BATCH_SIZE: u32 = 10_000;
const TICKET_ACTIVATION_CLEANUP_INTERVAL_SECS: u64 = 24 * 60 * 60;
const TICKET_ACTIVATION_CATCHUP_DELAY_SECS: u64 = 60;
const CONTROL_CODE_PHONE_TTL_MS: i64 = 105_000;
const VIVI_REAUTH_COMMAND_TTL_MS: i64 = 3 * 60_000;
const VIVI_REAUTH_LEGACY_REQUEST_PREFIX: &str = "vivi-reauth-";
const VIVI_REAUTH_FULL_RESET_REQUEST_PREFIX: &str = "vivi-full-reset-";
const VIVI_REAUTH_LOGOUT_LOGIN_REQUEST_PREFIX: &str = "vivi-logout-login-";
const VIVI_REAUTH_LOGOUT_REDETECT_LOGIN_REQUEST_PREFIX: &str = "vivi-logout-redetect-login-";
const CONTROL_CODE_FAST_READY_TTL_MS: i64 = 12_000;
const CONTROL_CODE_FAST_STATE_TTL_MS: i64 = 30_000;
const STREAM_VIEWER_FOCUS_TTL_MS: i64 = 90_000;
const SAFE_JSON_MAX_BYTES: usize = 4096;
// Public/durable "live" authority is intentionally narrower than visual
// continuity. LIVE_OK and DEGRADED frames may remain visible, but only a
// conservatively timed LIVE_FRESH frame may suppress recovery work.
const STREAM_BACKGROUND_SUPPRESS_FALLBACK_MAX_AGE_MS: i64 = 3_000;
const STREAM_BACKGROUND_REPORT_MAX_AGE_MS: i64 = 5_000;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum ViviReauthMode {
    LegacyV1,
    FullResetV2,
    LogoutLoginV3,
    LogoutLoginRedetectV4,
}

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
            ticketremote_ticket_interaction,
            $ticket,
            $bound,
            $limit,
            $deleted
        );
        purge_expired_rows!(
            $ctx,
            ticketremote_ticket_action_v3_queued_intent,
            $ticket,
            $bound,
            $limit,
            $deleted
        );
        purge_expired_rows!(
            $ctx,
            ticketremote_ticket_action_v3,
            $ticket,
            $bound,
            $limit,
            $deleted
        );
        // A scheduled terminal action needs its schedule to validate the
        // original command revision on replay. Purge the action first so
        // bounded cleanup never leaves an action without that correlation.
        purge_expired_rows!(
            $ctx,
            ticketremote_latest_ticket_reselect_schedule,
            $ticket,
            $bound,
            $limit,
            $deleted
        );
        purge_expired_rows!(
            $ctx,
            ticketremote_vivi_reauth_attempt,
            $ticket,
            $bound,
            $limit,
            $deleted
        );
        purge_expired_rows!(
            $ctx,
            ticketremote_vivi_reauth_owner,
            $ticket,
            $bound,
            $limit,
            $deleted
        );
        purge_expired_rows!(
            $ctx,
            ticketremote_ticket_slider_region_v3,
            $ticket,
            $bound,
            $limit,
            $deleted
        );
        purge_expired_rows!(
            $ctx,
            ticketremote_member_limit_event,
            $ticket,
            $bound,
            $limit,
            $deleted
        );
        purge_expired_rows!(
            $ctx,
            ticketremote_member_daily_activity,
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
        current_ticket_interaction($ctx, $ticket, $backend, $clock);
        refresh_activation_eligibility($ctx, $ticket, $backend, $clock);
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

/// Private, server-time activity aggregate for one authenticated account and
/// one Europe/Riga calendar day. Raw usage is exposed only through the
/// service-identity-gated projection below.
#[spacetimedb::table(
    accessor = ticketremote_member_daily_activity,
    index(accessor = ticketDay, btree(columns = [ticketId, day])),
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))
)]
#[derive(Clone)]
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
    #[default(None::<String>)]
    pub purpose: Option<String>,
    #[default(None::<String>)]
    pub activationRevision: Option<String>,
    #[default(None::<String>)]
    pub activationAttemptId: Option<String>,
    #[default(None::<String>)]
    pub originalDueAt: Option<String>,
    #[default(None::<String>)]
    pub nextRetryAt: Option<String>,
    #[default(0u32)]
    pub retryAttempt: u32,
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

#[spacetimedb::table(accessor = ticketremote_ticket_interaction, public,
    index(accessor = ticketBackend, btree(columns = [ticketId, backendId])),
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))
)]
#[derive(Clone)]
pub struct TicketremoteTicketInteraction {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub backendId: String,
    pub status: String,
    pub interactionRevision: String,
    pub activationRevision: String,
    pub activationAt: String,
    pub scheduledResetAt: String,
    pub resetRequestId: String,
    pub streamEpoch: String,
    pub frameSequence: String,
    pub phoneDisplayWidth: u32,
    pub phoneDisplayHeight: u32,
    pub sliderLeft: u32,
    pub sliderTop: u32,
    pub sliderRight: u32,
    pub sliderBottom: u32,
    pub ownerPublicId: String,
    pub controlId: String,
    pub leasePhase: String,
    pub leaseExpiresAt: String,
    pub latestInputSequence: String,
    pub latestInputPhase: String,
    pub latestProgress: u32,
    pub lastAppliedSequence: String,
    pub lastAppliedProgress: u32,
    pub reason: String,
    pub createdAt: String,
    pub updatedAt: String,
    #[index(btree)]
    pub expiresAt: String,
}

/// Public, privacy-safe status for one explicit browser ticket action. The
/// durable command payload remains private; members see only bounded state
/// needed to render progress and the reversible view switch.
#[spacetimedb::table(accessor = ticketremote_ticket_action_v3, public,
    index(accessor = ticketBackendStatus, btree(columns = [ticketId, backendId, status])),
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))
)]
#[derive(Clone)]
pub struct TicketremoteTicketActionV3 {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub actionId: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub backendId: String,
    pub target: String,
    #[index(btree)]
    pub status: String,
    pub phase: String,
    pub currentView: String,
    pub switchAvailable: bool,
    pub switchExpiresAt: String,
    pub streamEpoch: String,
    pub frameSequence: String,
    pub reason: String,
    pub createdAt: String,
    pub updatedAt: String,
    pub completedAt: String,
    pub expiresAt: String,
    #[default(None::<String>)]
    pub parentActionId: Option<String>,
    #[default(None::<String>)]
    pub rootActionId: Option<String>,
    #[default(0u32)]
    pub retryOrdinal: u32,
}

/// Private one-slot waiting intent for a second browser window. Admission is
/// intentionally deferred until promotion so stale proofs do not consume a
/// registration quota or create activation history.
#[spacetimedb::table(accessor = ticketremote_ticket_action_v3_queued_intent,
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))
)]
#[derive(Clone)]
pub struct TicketremoteTicketActionV3QueuedIntent {
    #[primary_key]
    pub id: String,
    pub ticketId: String,
    pub backendId: String,
    pub actionId: String,
    pub kind: String,
    pub target: String,
    pub source: String,
    pub reason: String,
    pub attemptId: String,
    pub expectedInteractionRevision: String,
    pub scheduleId: String,
    pub requestedEmail: String,
    pub privatePayloadJson: String,
    pub createdAt: String,
    #[index(btree)]
    pub expiresAt: String,
}

/// Private owner-to-service credential authority for the single ViVi account
/// used by one Ticket phone backend. These values must never be copied into a
/// public table, command payload, status report, or operational event.
#[spacetimedb::table(
    accessor = ticketremote_vivi_credentials,
    index(accessor = ticketBackend, btree(columns = [ticketId, backendId]))
)]
#[derive(Clone)]
pub struct TicketremoteViviCredentials {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub backendId: String,
    pub email: String,
    pub password: String,
    pub revision: String,
    pub createdAt: String,
    pub updatedAt: String,
}

/// Private binding used only to make owner-filtered views depend on the
/// authenticated Spacetime identity rather than on caller-supplied email.
#[spacetimedb::table(
    accessor = ticketremote_member_identity,
    index(accessor = byIdentity, btree(columns = [identity])),
    index(accessor = ticketEmail, btree(columns = [ticketId, email]))
)]
#[derive(Clone)]
pub struct TicketremoteMemberIdentity {
    #[primary_key]
    pub id: String,
    pub identity: Identity,
    pub ticketId: String,
    pub email: String,
    pub updatedAt: String,
}

/// Private exact requester binding for revoking queued or not-yet-started
/// owner re-authentication work without exposing an email in public status.
#[spacetimedb::table(
    accessor = ticketremote_vivi_reauth_owner,
    index(accessor = ticketOwner, btree(columns = [ticketId, ownerEmail])),
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))
)]
#[derive(Clone)]
pub struct TicketremoteViviReauthOwner {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    pub backendId: String,
    pub requestId: String,
    pub ownerEmail: String,
    #[index(btree)]
    pub expiresAt: String,
}

/// Public credential projection. It deliberately exposes only whether an
/// owner has configured credentials and the opaque revision needed to fence a
/// re-auth request from a concurrent credential replacement.
#[spacetimedb::table(
    accessor = ticketremote_vivi_credential_state,
    public,
    index(accessor = ticketBackend, btree(columns = [ticketId, backendId]))
)]
#[derive(Clone)]
pub struct TicketremoteViviCredentialState {
    #[primary_key]
    pub id: String,
    pub ticketId: String,
    pub backendId: String,
    pub configured: bool,
    pub revision: String,
    pub updatedAt: String,
}

/// Public, privacy-safe status for one explicit owner re-auth request. The
/// matching credentials remain exclusively in the private credential table.
#[spacetimedb::table(
    accessor = ticketremote_vivi_reauth_attempt,
    public,
    index(accessor = ticketBackendStatus, btree(columns = [ticketId, backendId, status])),
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))
)]
#[derive(Clone)]
pub struct TicketremoteViviReauthAttempt {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub requestId: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub backendId: String,
    pub credentialRevision: String,
    pub ownerPublicId: String,
    #[index(btree)]
    pub status: String,
    pub phase: String,
    pub reason: String,
    pub proofSource: String,
    pub streamEpoch: String,
    pub frameSequence: String,
    pub createdAt: String,
    pub updatedAt: String,
    pub completedAt: String,
    #[index(btree)]
    pub expiresAt: String,
}

/// Short-lived, privacy-safe registration gesture geometry. Values are basis
/// points in the already-cropped encoded Ticket frame, never raw display
/// coordinates. A row is useful only with its exact successful visual action.
#[spacetimedb::table(accessor = ticketremote_ticket_slider_region_v3, public,
    index(accessor = ticketBackend, btree(columns = [ticketId, backendId])),
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))
)]
#[derive(Clone)]
pub struct TicketremoteTicketSliderRegionV3 {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub backendId: String,
    #[index(btree)]
    pub proofActionId: String,
    pub streamEpoch: String,
    pub frameSequence: String,
    pub leftBasisPoints: u32,
    pub topBasisPoints: u32,
    pub rightBasisPoints: u32,
    pub bottomBasisPoints: u32,
    pub updatedAt: String,
    pub expiresAt: String,
}

/// Private authoritative activation history.  This table is deliberately
/// separate from the short-lived operational state above: it is the source
/// used for exact-attempt idempotency, physical registration reconciliation,
/// and refresh auditing. Per-account admission policy lives in the separate
/// member-limit ledger. It contains only bounded safety metadata and opaque
/// correlations; member identity, ticket content, coordinates, and payloads
/// never enter this activation ledger.
#[spacetimedb::table(
    accessor = ticketremote_activation_history,
    index(accessor = ticketBackendAdmitted, btree(columns = [ticketId, backendId, admission, admittedAt])),
    index(accessor = ticketAttempt, btree(columns = [ticketId, attemptId])),
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))
)]
#[derive(Clone)]
pub struct TicketremoteActivationHistory {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub backendId: String,
    #[index(btree)]
    pub flow: String,
    #[index(btree)]
    pub admission: String,
    #[index(btree)]
    pub outcome: String,
    #[index(btree)]
    pub reason: String,
    pub occurredAt: String,
    pub occurrenceDay: String,
    #[index(btree)]
    pub admittedAt: String,
    pub updatedAt: String,
    pub completedAt: String,
    #[index(btree)]
    pub attemptId: String,
    pub interactionRevision: String,
    pub interactionCorrelation: String,
    pub activationRevision: String,
    pub inputFingerprint: String,
    pub refreshDueAt: String,
    pub refreshCompletedAt: String,
    pub refreshOutcome: String,
    pub refreshRetryAt: String,
    pub refreshAttempt: u32,
    pub occurrenceCount: u32,
    #[index(btree)]
    pub expiresAt: String,
}

/// Short-lived public decision projection.  The browser subscribes to this
/// row by its opaque attempt ID so a policy rejection can be committed and
/// rendered without turning a normal v2 reducer call into an error.
#[spacetimedb::table(
    accessor = ticketremote_activation_decision,
    public,
    index(accessor = ticketBackend, btree(columns = [ticketId, backendId])),
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))
)]
#[derive(Clone)]
pub struct TicketremoteActivationDecision {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub backendId: String,
    pub attemptId: String,
    pub flow: String,
    pub accepted: bool,
    pub reason: String,
    pub retryAt: String,
    pub serverAt: String,
    pub interactionRevision: String,
    pub inputFingerprint: String,
    pub updatedAt: String,
    #[index(btree)]
    pub expiresAt: String,
}

/// Compatibility-only backend projection retained for existing clients.
/// Account-specific control authority is ticketremote_member_limit_state.
#[spacetimedb::table(
    accessor = ticketremote_activation_eligibility,
    public,
    index(accessor = ticketBackend, btree(columns = [ticketId, backendId]))
)]
#[derive(Clone)]
pub struct TicketremoteActivationEligibility {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub backendId: String,
    pub allowed: bool,
    pub reason: String,
    pub retryAt: String,
    pub cooldownUntil: String,
    pub admissionsInWindow: u32,
    pub serverAt: String,
    pub updatedAt: String,
}

/// Private, account-persistent choice for admins and owners. Missing rows mean
/// the safe default: obey the same limits as ordinary members. The email never
/// appears in a public table.
#[spacetimedb::table(
    accessor = ticketremote_member_limit_preference,
    index(accessor = ticketEmail, btree(columns = [ticketId, email]))
)]
#[derive(Clone)]
pub struct TicketremoteMemberLimitPreference {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub email: String,
    pub obeyLimits: bool,
    pub createdAt: String,
    pub updatedAt: String,
}

/// Private durable presentation choice for one authenticated Ticket account.
/// Missing rows mean the safe default: HDR is disabled. Email stays private.
#[spacetimedb::table(
    accessor = ticketremote_member_hdr_preference,
    index(accessor = ticketEmail, btree(columns = [ticketId, email]))
)]
#[derive(Clone)]
pub struct TicketremoteMemberHDRPreference {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub email: String,
    pub enabled: bool,
    pub createdAt: String,
    pub updatedAt: String,
}

/// Compatibility tombstone for the retired HDR engine selector. Browser HDR
/// is now the only runtime engine. Existing values are normalized to v2 during
/// an authenticated owner refresh; email remains private.
#[spacetimedb::table(
    accessor = ticketremote_member_hdr_engine_preference,
    index(accessor = ticketEmail, btree(columns = [ticketId, email]))
)]
#[derive(Clone)]
pub struct TicketremoteMemberHDREnginePreference {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub email: String,
    pub engine: String,
    pub createdAt: String,
    pub updatedAt: String,
}

/// Private durable HDR display-boost choice for one authenticated Ticket
/// account. Missing or unrecognized values safely select the 4x presentation
/// target. The email and retained choice never appear in a public table.
#[spacetimedb::table(
    accessor = ticketremote_member_hdr_boost_preference,
    index(accessor = ticketEmail, btree(columns = [ticketId, email]))
)]
#[derive(Clone)]
pub struct TicketremoteMemberHDRBoostPreference {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub email: String,
    pub selectedDisplayBoost: u32,
    pub createdAt: String,
    pub updatedAt: String,
}

/// Private durable admission audit shared by registration and control-code
/// policy. Consequential admin bypasses are retained with counted=false so
/// they are auditable without consuming a later enforced quota.
#[spacetimedb::table(
    accessor = ticketremote_member_limit_event,
    index(accessor = ticketEmailKindAt, btree(columns = [ticketId, email, kind, admittedAt])),
    index(accessor = ticketKindCorrelation, btree(columns = [ticketId, kind, correlationId])),
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))
)]
#[derive(Clone)]
pub struct TicketremoteMemberLimitEvent {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub email: String,
    pub ownerPublicId: String,
    #[index(btree)]
    pub kind: String,
    #[index(btree)]
    pub correlationId: String,
    pub counted: bool,
    pub enforcementMode: String,
    #[index(btree)]
    pub admittedAt: String,
    pub updatedAt: String,
    #[index(btree)]
    pub expiresAt: String,
}

/// Sanitized browser authority for one authenticated account. The public key
/// is opaque and no email, ticket content, coordinates, or action payload is
/// exposed. Countdown text is advisory; only the booleans authorize controls.
#[spacetimedb::table(
    accessor = ticketremote_member_limit_state,
    public,
    index(accessor = ticketOwner, btree(columns = [ticketId, ownerPublicId]))
)]
#[derive(Clone)]
pub struct TicketremoteMemberLimitState {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub ownerPublicId: String,
    pub obeyLimits: bool,
    pub canBypass: bool,
    pub effectiveLimited: bool,
    pub registrationAllowed: bool,
    pub registrationReason: String,
    pub registrationCount: u32,
    pub registrationLimit: u32,
    pub registrationIntervalSeconds: u32,
    pub registrationRetryAt: String,
    pub registrationNextReleaseAt: String,
    pub controlCodeAllowed: bool,
    pub controlCodeReason: String,
    pub controlCodeCount: u32,
    pub controlCodeLimit: u32,
    pub controlCodeWindowSeconds: u32,
    pub controlCodeRetryAt: String,
    pub updatedAt: String,
    pub serverAt: String,
}

/// Sanitized browser projection for one authenticated account's HDR choice.
/// The browser subscribes only to its opaque account key; no email, media,
/// device data, ticket content, or capability decision is stored here.
#[spacetimedb::table(
    accessor = ticketremote_member_hdr_state,
    public,
    index(accessor = ticketAccount, btree(columns = [ticketId, accountScopeId]))
)]
#[derive(Clone)]
pub struct TicketremoteMemberHDRState {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub accountScopeId: String,
    pub enabled: bool,
    pub updatedAt: String,
    pub serverAt: String,
}

/// Sanitized browser projection of the active owner's HDR processing choice.
/// Demoted and inactive accounts have no row; their private preference is
/// retained for possible later owner restoration.
#[spacetimedb::table(
    accessor = ticketremote_member_hdr_engine_state,
    public,
    index(accessor = ticketAccount, btree(columns = [ticketId, accountScopeId]))
)]
#[derive(Clone)]
pub struct TicketremoteMemberHDREngineState {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub accountScopeId: String,
    pub engine: String,
    pub updatedAt: String,
    pub serverAt: String,
}

/// Sanitized browser projection of an active member's HDR display-boost
/// choice. Inactive accounts have no row; their private preference is retained
/// for possible later membership restoration.
#[spacetimedb::table(
    accessor = ticketremote_member_hdr_boost_state,
    public,
    index(accessor = ticketAccount, btree(columns = [ticketId, accountScopeId]))
)]
#[derive(Clone)]
pub struct TicketremoteMemberHDRBoostState {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub accountScopeId: String,
    pub selectedDisplayBoost: u32,
    pub updatedAt: String,
    pub serverAt: String,
}

/// Spacetime-owned reversible-view policy anchor. Visual signatures stay on
/// Pixel; this row stores only opaque correlations and reducer timestamps.
#[spacetimedb::table(
    accessor = ticketremote_ticket_switch_anchor,
    index(accessor = ticketBackend, btree(columns = [ticketId, backendId]))
)]
#[derive(Clone)]
pub struct TicketremoteTicketSwitchAnchor {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub backendId: String,
    pub activationAttemptId: String,
    pub activationRevision: String,
    pub activationAt: String,
    pub expiresAt: String,
    pub latestUnactivatedProofActionId: String,
    pub latestUnactivatedProofAt: String,
    pub currentView: String,
    pub policyRevision: String,
    pub updatedAt: String,
}

/// One-shot Spacetime boundary callbacks keep browser authority fresh without
/// browser or phone polling. subjectId is private (email or backend id).
#[spacetimedb::table(
    accessor = ticketremote_policy_boundary_timer,
    scheduled(ticketremote_scheduled_policy_boundary),
    index(accessor = ticketSubject, btree(columns = [ticketId, subjectKind, subjectId]))
)]
#[derive(Clone)]
pub struct TicketremotePolicyBoundaryTimer {
    #[primary_key]
    #[auto_inc]
    pub scheduled_id: u64,
    pub scheduled_at: ScheduleAt,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub subjectKind: String,
    #[index(btree)]
    pub subjectId: String,
    pub boundaryAt: String,
    pub createdAt: String,
}

// Compatibility table retained because the live Ticket database already
// publishes this additive latency projection.  The SDR control module does
// not depend on it, but omitting it would turn an otherwise behavior-only
// publish into a breaking schema migration for existing clients.
#[spacetimedb::table(accessor = ticketremote_latency_link_v1, public,
    index(accessor = ticketBackendKind, btree(columns = [ticketId, backendId, subjectKind])),
    index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))
)]
#[derive(Clone)]
pub struct TicketremoteLatencyLinkV1 {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub ticketId: String,
    #[index(btree)]
    pub backendId: String,
    #[index(btree)]
    pub subjectKind: String,
    #[index(btree)]
    pub subjectId: String,
    pub traceId: String,
    pub action: String,
    pub cohort: String,
    pub variant: String,
    pub submittedAt: String,
    #[default(None::<String>)]
    pub phoneObservedAt: Option<String>,
    #[default(0u32)]
    pub databaseToPhoneMillis: u32,
    #[index(btree)]
    pub expiresAt: String,
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

#[spacetimedb::table(
    accessor = ticketremote_activation_cleanup_schedule,
    scheduled(ticketremote_scheduled_activation_cleanup)
)]
#[derive(Clone)]
pub struct TicketremoteActivationCleanupSchedule {
    #[primary_key]
    #[auto_inc]
    pub scheduled_id: u64,
    pub scheduled_at: ScheduleAt,
    pub createdAt: String,
    pub updatedAt: String,
}

#[spacetimedb::table(
    accessor = ticketremote_activation_cleanup_catchup,
    scheduled(ticketremote_scheduled_activation_cleanup_catchup)
)]
#[derive(Clone)]
pub struct TicketremoteActivationCleanupCatchup {
    #[primary_key]
    #[auto_inc]
    pub scheduled_id: u64,
    pub scheduled_at: ScheduleAt,
    pub createdAt: String,
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

/// Additive account-identity mapping for activity joins. This intentionally
/// leaves the pre-existing service member projection byte-for-byte compatible
/// with sidecars that are still running during a module-first rollout.
#[derive(Clone, SpacetimeType)]
pub struct TicketremoteServiceMemberAccount {
    pub id: String,
    pub ticketId: String,
    pub email: String,
    pub publicId: String,
    pub accountScopeId: String,
    pub role: String,
    pub active: bool,
    pub updatedAt: String,
}

cloned_projection! {
    TicketremoteServiceMemberDailyActivity from TicketremoteMemberDailyActivity
        with service_member_daily_activity_from_row {
        id: String, ticketId: String, accountScopeId: String, day: String,
        hourlyTicks: Vec<u32>, lastTickSlot: i64, firstTickAt: String,
        lastTickAt: String, updatedAt: String, expiresAt: String
    }
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
        phoneLocalTime: String, phoneTimeZone: String, purpose: Option<String>,
        activationRevision: Option<String>, status: String, commandId: String,
        resultReason: String, resultPhase: String, proofSource: String, requestedBy: String,
        createdAt: String, updatedAt: String, triggeredAt: String, completedAt: String,
        expiresAt: String
    }
}

cloned_projection! {
    TicketremoteViviCredentialView from TicketremoteViviCredentials
        with vivi_credential_view_from_row {
        id: String, ticketId: String, backendId: String, email: String,
        password: String, revision: String, updatedAt: String
    }
}

#[spacetimedb::view(
    accessor = ticketremote_owner_vivi_credentials,
    public,
    primary_key = id
)]
pub fn ticketremote_owner_vivi_credentials_view(
    ctx: &ViewContext,
) -> Vec<TicketremoteViviCredentialView> {
    let Some(binding) = ctx
        .db
        .ticketremote_member_identity()
        .byIdentity()
        .filter(&ctx.sender())
        .next()
    else {
        return Vec::new();
    };
    let member_id = member_id(&binding.ticketId, &binding.email);
    let owner = ctx
        .db
        .ticketremote_ticket_member()
        .id()
        .find(&member_id)
        .is_some_and(|row| row.active && row.role == "owner");
    if !owner {
        return Vec::new();
    }
    ctx.db
        .ticketremote_vivi_credentials()
        .ticketBackend()
        .filter((&binding.ticketId,))
        .map(|row| vivi_credential_view_from_row(&row))
        .collect()
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
    ticketremote_service_member_account =>
        ticketremote_service_member_account_view -> TicketremoteServiceMemberAccount
    |ctx, ticket| {
        ctx.db.ticketremote_ticket_member().ticketId().filter(&ticket)
            .map(|row| service_member_account_from_row(&row)).collect()
    }
    ticketremote_service_member_daily_activity =>
        ticketremote_service_member_daily_activity_view -> TicketremoteServiceMemberDailyActivity
    |ctx, ticket| {
        ctx.db.ticketremote_member_daily_activity().ticketDay().filter((&ticket,))
            .map(|row| service_member_daily_activity_from_row(&row)).collect()
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
    ticketremote_service_vivi_credentials =>
        ticketremote_service_vivi_credentials_view -> TicketremoteViviCredentialView
    |ctx, ticket| {
        ctx.db.ticketremote_vivi_credentials().ticketBackend().filter((&ticket,))
            .filter(|credential| ["pending", "running"].into_iter().any(|status| {
                ctx.db.ticketremote_vivi_reauth_attempt().ticketBackendStatus()
                    .filter((&credential.ticketId, &credential.backendId, status))
                    .any(|attempt| {
                        attempt.credentialRevision == credential.revision &&
                            ctx.db.ticketremote_stream_command().id()
                                .find(vivi_reauth_command_id(
                                    &attempt.ticketId,
                                    &attempt.backendId,
                                    &attempt.requestId,
                                ))
                                .is_some_and(|command| {
                                    command.commandType == "vivi_reauth" &&
                                        matches!(command.status.as_str(), "pending" | "running")
                                })
                    })
            }))
            .map(|row| vivi_credential_view_from_row(&row)).collect()
    }
}

#[derive(Clone)]
struct ActivationAdmission {
    accepted: bool,
    ticket_id: String,
    backend_id: String,
    flow: String,
    attempt_id: String,
    interaction_revision: String,
    reason: String,
    retry_at: String,
}

enum ActivationAdmissionResult {
    Accepted(ActivationAdmission),
    Rejected(ActivationAdmission),
}

fn activation_flow(value: &str) -> String {
    allowlisted(
        value,
        &["manual_slider", "menu_activate", "reset_and_activate"],
        "",
    )
}

fn ticket_action_v3_target(value: &str) -> String {
    allowlisted(
        value,
        &[
            "open_latest_unactivated",
            "open_latest_and_register",
            "register_current",
            "show_recent_activated",
            "return_to_latest_unactivated",
            "redetect_latest",
            "prove_current",
        ],
        "",
    )
}

fn ticket_action_v3_status(value: &str) -> String {
    allowlisted(
        value,
        &[
            "queued",
            "pending",
            "running",
            "succeeded",
            "failed",
            "needs_attention",
        ],
        "",
    )
}

fn ticket_action_v3_view(value: &str) -> String {
    allowlisted(
        value,
        &[
            "latest_unactivated",
            "recent_activated",
            "activated_current",
            "unknown",
        ],
        "unknown",
    )
}

fn ticket_action_v3_public_reason(value: &str, fallback: &str) -> String {
    allowlisted(
        value,
        &[
            "ticket_action_queued",
            "ticket_action_requested",
            "ticket_action_updated",
            "ticket_action_rejected",
            "ticket_action_failed",
            "ticket_action_v3_admitted",
            "ticket_action_v3_running",
            "ticket_action_v3_phone_lane_busy",
            "ticket_action_v3_superseded",
            "ticket_action_v3_failed",
            "ticket_action_v3_internal_failure",
            "ticket_action_v3_service_stopping",
            "ticket_action_v3_startup_reconcile_unproved",
            "control_code_cleanup_pending_requires_visual_reopen",
            "ticket_action_current_physical_touch_active",
            "ticket_action_current_stream_unavailable",
            "ticket_action_current_proof_fence_changed",
            "ticket_action_current_activated",
            "ticket_action_current_list",
            "ticket_action_current_tickets_single_use_empty",
            "ticket_action_current_tickets_time_empty",
            "ticket_action_current_vivi_home",
            "ticket_action_current_vivi_profile",
            "ticket_action_current_vivi_other_tab",
            "ticket_action_current_login_required",
            "ticket_action_current_blocked",
            "ticket_action_current_unknown",
            "ticket_action_current_unactivated_proved",
            "ticket_action_visual_stream_unavailable",
            "ticket_action_latest_not_detected",
            "ticket_action_latest_redetected",
            "ticket_action_navigation_dispatch_uncertain",
            "ticket_action_visual_state_login_required",
            "ticket_action_visual_state_blocked",
            "ticket_action_visual_state_unknown",
            "ticket_action_visual_target_ambiguous",
            "ticket_action_visual_tap_uncertain",
            "ticket_action_visual_transition_unproved",
            "ticket_action_visual_unproved",
            "ticket_action_selected_anchor_unproved",
            "ticket_action_selected_anchor_missing",
            "ticket_action_transition_anchor_missing",
            "ticket_action_selected_anchor_conflict",
            "ticket_action_target_not_reached",
            "ticket_action_target_visible",
            "ticket_action_navigation_journal_unproved",
            "ticket_action_navigation_journal_unproved_after_dispatch",
            "ticket_action_slider_unproved",
            "ticket_action_slider_geometry_invalid",
            "ticket_action_interaction_proof_invalid",
            "ticket_action_interaction_revision_unproved",
            "ticket_action_detail_identity_conflict",
            "ticket_action_detail_identity_unproved",
            "ticket_action_current_detail_identity_unproved",
            "ticket_action_register_current_requires_unactivated_detail",
            "ticket_action_accessibility_unavailable",
            "ticket_action_input_window_unproved",
            "ticket_action_exact_input_fence_changed",
            "ticket_action_frame_watermark_unproved",
            "ticket_action_panel_dark_preempted",
            "ticket_action_panel_dark_preempted_after_dispatch",
            "ticket_action_physical_touch_preempted_before_dispatch",
            "ticket_action_physical_touch_preempted_after_dispatch",
            "ticket_action_physical_touch_preempted_after_dispatch_reconciled",
            "ticket_action_physical_touch_preempted_after_dispatch_visual_unproved",
            "ticket_action_activation_dispatch_uncertain",
            "ticket_action_activation_outcome_unknown",
            "ticket_action_activation_checkpoint_unproved",
            "ticket_action_activation_dispatch_checkpoint_unproved",
            "ticket_action_activation_proven_checkpoint_unproved",
            "ticket_action_gesture_start_uncertain",
            "ticket_action_gesture_start_rejected",
            "ticket_action_gesture_rejected",
            "ticket_action_gesture_completion_uncertain",
            "ticket_action_gesture_completed_no_transition",
            "ticket_action_retry_not_dispatched",
            "ticket_action_no_transition_checkpoint_unproved",
            "ticket_action_post_gesture_visual_unproved",
            "ticket_action_activation_visual_unproved",
            "ticket_action_terminal_journal_unproved",
            "ticket_action_terminal_view_unproved",
            "ticket_action_registered",
            "ticket_view_switch_unavailable",
            "slider_proof_stale",
            "activation_policy_rejected",
            "activation_attempt_in_progress",
            "activation_cooldown_active",
            "activation_rate_limited",
            "registration_interval",
            "registration_hour_limit",
            "activation_requires_unactivated_ticket",
            "activation_proof_stale",
            "activation_attempt_mismatch",
            "command_expired",
        ],
        fallback,
    )
}

fn ticket_action_v3_is_activation(target: &str) -> bool {
    matches!(target, "open_latest_and_register" | "register_current")
}

#[allow(dead_code)]
fn ticket_action_v3_switch_allowed(
    target: &str,
    current_view: &str,
    switch_available: bool,
    switch_expires_at: &str,
    now: &str,
) -> bool {
    if !switch_available || parse_time_micros(switch_expires_at) <= parse_time_micros(now) {
        return false;
    }
    matches!(
        (target, current_view),
        ("show_recent_activated", "latest_unactivated")
            | ("return_to_latest_unactivated", "recent_activated")
    )
}

#[allow(dead_code)]
fn ticket_action_v3_switch_expiry_valid(switch_expires_at: &str, now: &str) -> bool {
    let now_ms = parse_time_ms(now);
    let expires_ms = parse_time_ms(switch_expires_at);
    expires_ms > now_ms && expires_ms <= now_ms.saturating_add(TICKET_ACTION_SWITCH_WINDOW_MS)
}

fn live_ticket_switch_anchor(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    now: &str,
) -> Option<TicketremoteTicketSwitchAnchor> {
    ctx.db
        .ticketremote_ticket_switch_anchor()
        .id()
        .find(phone_row_id(ticket_id, backend_id))
        .filter(|anchor| {
            !anchor.policyRevision.trim().is_empty()
                && parse_time_micros(&anchor.expiresAt) > parse_time_micros(now)
                && parse_time_micros(&anchor.expiresAt)
                    <= parse_time_micros(&anchor.activationAt)
                        .saturating_add(TICKET_ACTION_SWITCH_WINDOW_MS.saturating_mul(1_000))
        })
}

fn ticket_switch_anchor_has_later_unactivated_proof(
    anchor: &TicketremoteTicketSwitchAnchor,
) -> bool {
    !anchor.latestUnactivatedProofActionId.trim().is_empty()
        && parse_time_micros(&anchor.latestUnactivatedProofAt)
            > parse_time_micros(&anchor.activationAt)
}

fn ticket_action_v3_switch_authority(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    target: &str,
    now: &str,
) -> Option<TicketremoteTicketSwitchAnchor> {
    live_ticket_switch_anchor(ctx, ticket_id, backend_id, now).filter(|anchor| {
        ticket_switch_anchor_has_later_unactivated_proof(anchor)
            && matches!(
                (target, anchor.currentView.as_str()),
                ("show_recent_activated", "latest_unactivated")
                    | ("return_to_latest_unactivated", "recent_activated")
            )
    })
}

fn ticket_action_v3_registration_proof_row_valid(
    row: &TicketremoteTicketActionV3,
    proof_action_id: &str,
    now: &str,
) -> bool {
    row.actionId == proof_action_id
        && matches!(
            row.target.as_str(),
            "open_latest_unactivated"
                | "return_to_latest_unactivated"
                | "redetect_latest"
                | "prove_current"
        )
        && row.status == "succeeded"
        && row.currentView == "latest_unactivated"
        && !row.streamEpoch.trim().is_empty()
        && row.streamEpoch != "0"
        && !row.frameSequence.trim().is_empty()
        && row.frameSequence != "0"
        && parse_time_ms(&row.expiresAt) > parse_time_ms(now)
}

fn ticket_action_v3_has_registration_authority(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    proof_action_id: &str,
    now: &str,
) -> bool {
    // New commands bind the current phone observation, not a video-backed
    // historical action. Retained V3 commands keep their original authority
    // until the coordinated producer cutover has drained them.
    if proof_action_id.starts_with("pc-") {
        return ctx.db.ticketremote_phone_control_state().id()
            .find(phone_row_id(ticket_id, backend_id))
            .is_some_and(|row| phone_control_registration_ready(&row, proof_action_id, now));
    }
    if !valid_schedule_identifier(proof_action_id) {
        return false;
    }
    let id = ticket_action_v3_row_id(ticket_id, backend_id, proof_action_id);
    ctx.db
        .ticketremote_ticket_action_v3()
        .id()
        .find(id)
        .is_some_and(|row| {
            ticket_action_v3_registration_proof_row_valid(&row, proof_action_id, now)
                && (row.target != "prove_current"
                    || ctx
                        .db
                        .ticketremote_ticket_slider_region_v3()
                        .id()
                        .find(ticket_slider_region_v3_id(ticket_id, backend_id))
                        .is_some_and(|region| {
                            ticket_slider_region_v3_matches_action(&region, &row, now)
                        }))
        })
}

fn ticket_slider_region_v3_id(ticket_id: &str, backend_id: &str) -> String {
    format!(
        "{}:{}",
        clean_ticket_id(ticket_id),
        clean_backend_id(backend_id)
    )
}

fn ticket_slider_region_v3_bounds_valid(left: u32, top: u32, right: u32, bottom: u32) -> bool {
    right <= 10_000 && bottom <= 10_000 && left < right && top < bottom
}

fn ticket_slider_region_v3_matches_action(
    region: &TicketremoteTicketSliderRegionV3,
    action: &TicketremoteTicketActionV3,
    now: &str,
) -> bool {
    region.ticketId == action.ticketId
        && region.backendId == action.backendId
        && region.proofActionId == action.actionId
        && region.streamEpoch == action.streamEpoch
        && region.frameSequence == action.frameSequence
        && parse_time_ms(&region.expiresAt) > parse_time_ms(now)
        && ticket_slider_region_v3_bounds_valid(
            region.leftBasisPoints,
            region.topBasisPoints,
            region.rightBasisPoints,
            region.bottomBasisPoints,
        )
}

fn ticket_action_v3_row_id(ticket_id: &str, backend_id: &str, action_id: &str) -> String {
    format!(
        "ticket-action-v3:{}:{}:{}",
        clean_ticket_id(ticket_id),
        clean_backend_id(backend_id),
        action_id.trim()
    )
}

fn ticket_action_v3_command_id(ticket_id: &str, backend_id: &str, action_id: &str) -> String {
    format!(
        "{}:{}:ticket_action_v3:{}",
        clean_ticket_id(ticket_id),
        clean_backend_id(backend_id),
        action_id.trim()
    )
}

fn vivi_reauth_attempt_id(ticket_id: &str, backend_id: &str, request_id: &str) -> String {
    format!(
        "vivi-reauth:{}:{}:{}",
        clean_ticket_id(ticket_id),
        clean_backend_id(backend_id),
        request_id.trim()
    )
}

fn vivi_reauth_command_id(ticket_id: &str, backend_id: &str, request_id: &str) -> String {
    format!(
        "{}:{}:vivi_reauth:{}",
        clean_ticket_id(ticket_id),
        clean_backend_id(backend_id),
        request_id.trim()
    )
}

/// The durable command key is the authoritative correlation for cleanup. It
/// remains usable even when a corrupt or legacy payload cannot be decoded (or
/// has already been scrubbed after a failed acknowledgement).
fn vivi_reauth_request_id_from_command(command: &TicketremoteStreamCommand) -> Option<String> {
    if command.commandType != "vivi_reauth" {
        return None;
    }
    let prefix = format!(
        "{}:{}:vivi_reauth:",
        clean_ticket_id(&command.ticketId),
        clean_backend_id(&command.backendId)
    );
    command
        .id
        .strip_prefix(&prefix)
        .and_then(|request_id| clean_vivi_request_id(request_id).ok())
}

fn vivi_reauth_attempt_for_command(
    ctx: &ReducerContext,
    command: &TicketremoteStreamCommand,
) -> Option<TicketremoteViviReauthAttempt> {
    let request_id = vivi_reauth_request_id_from_command(command)?;
    let attempt_id = vivi_reauth_attempt_id(&command.ticketId, &command.backendId, &request_id);
    ctx.db
        .ticketremote_vivi_reauth_attempt()
        .id()
        .find(&attempt_id)
}

fn vivi_reauth_terminal(status: &str) -> bool {
    matches!(status, "succeeded" | "failed" | "needs_attention")
}

fn vivi_reauth_interrupted_terminal_status(current_status: &str) -> &'static str {
    if current_status == "running" {
        "needs_attention"
    } else {
        "failed"
    }
}

fn ticket_has_vivi_reauth_in_progress(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
) -> bool {
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    ["pending", "running"].into_iter().any(|status| {
        ctx.db
            .ticketremote_vivi_reauth_attempt()
            .ticketBackendStatus()
            .filter((&ticket_id, &backend_id, status))
            .next()
            .is_some()
    })
}

fn ticket_has_live_vivi_reauth(ctx: &ReducerContext, ticket_id: &str, backend_id: &str) -> bool {
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    ["queued", "pending", "running"].into_iter().any(|status| {
        ctx.db
            .ticketremote_vivi_reauth_attempt()
            .ticketBackendStatus()
            .filter((&ticket_id, &backend_id, status))
            .next()
            .is_some()
    })
}

fn ticket_action_v3_terminal(status: &str) -> bool {
    matches!(status, "succeeded" | "failed" | "needs_attention")
}

fn ticket_action_v3_obsolete_read_only_finalization(
    target: &str,
    action: Option<(&str, &str)>,
    command_exists: bool,
    attempt_id: &str,
    activation_revision: &str,
) -> bool {
    target == "prove_current"
        && !command_exists
        && attempt_id.is_empty()
        && activation_revision.is_empty()
        && action.is_none_or(|(action_target, action_status)| {
            action_target == "prove_current"
                && ticket_action_v3_terminal(action_status)
                && action_status != "succeeded"
        })
}

fn ticket_action_v3_phone_lane_statuses() -> [&'static str; 2] {
    ["pending", "running"]
}

fn ticket_has_ticket_action_v3_in_progress(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
) -> bool {
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    ticket_action_v3_phone_lane_statuses()
        .into_iter()
        .any(|status| {
            ctx.db
                .ticketremote_ticket_action_v3()
                .ticketBackendStatus()
                .filter((&ticket_id, &backend_id, status))
                .next()
                .is_some()
        })
}

fn ticket_phone_mutation_lane_conflict_reason(
    control_code_busy: bool,
    ticket_action_v3_busy: bool,
    vivi_reauth_busy: bool,
    legacy_reset_busy: bool,
    interaction_busy: bool,
) -> Option<&'static str> {
    if control_code_busy {
        Some("control_code_in_progress")
    } else if ticket_action_v3_busy {
        Some("ticket_action_in_progress")
    } else if vivi_reauth_busy {
        Some("vivi_reauth_in_progress")
    } else if legacy_reset_busy {
        Some("ticket_reset_in_progress")
    } else if interaction_busy {
        Some("ticket_mutation_in_progress")
    } else {
        None
    }
}

fn ticket_phone_mutation_lane_conflict(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    now: &str,
) -> Option<&'static str> {
    ticket_phone_mutation_lane_conflict_ignoring_control_request(
        ctx, ticket_id, backend_id, "", now,
    )
}

fn ticket_phone_mutation_lane_conflict_ignoring_control_request(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    ignored_control_request_id: &str,
    now: &str,
) -> Option<&'static str> {
    let conflict = ticket_phone_mutation_lane_conflict_reason(
        ticket_has_control_code_request_in_progress_except(
            ctx,
            ticket_id,
            ignored_control_request_id,
            now,
        ),
        ticket_has_ticket_action_v3_in_progress(ctx, ticket_id, backend_id),
        ticket_has_vivi_reauth_in_progress(ctx, ticket_id, backend_id),
        ticket_has_ticket_registration_reset_in_progress(ctx, ticket_id, backend_id, now),
        false,
    );
    if conflict.is_some() {
        return conflict;
    }

    let interaction = repair_missing_reset_command_interaction(
        ctx,
        current_ticket_interaction(ctx, ticket_id, backend_id, now),
        now,
    );
    let interaction = if let Some(repaired) =
        repair_expired_ticket_slider_lease_for_mutation(&interaction, now)
    {
        purge_pending_ticket_slider_commands(
            ctx,
            ticket_id,
            backend_id,
            &interaction.interactionRevision,
            now,
        );
        upsert_ticket_interaction(ctx, repaired)
    } else {
        interaction
    };
    ticket_phone_mutation_lane_conflict_reason(
        false,
        false,
        false,
        false,
        ticket_interaction_blocks_control_code(&interaction),
    )
}

fn ticket_action_v3_duplicate_result(
    existing_target: &str,
    requested_target: &str,
) -> Result<(), String> {
    if existing_target == requested_target {
        Ok(())
    } else {
        Err("ticket_action_id_reused".into())
    }
}

fn ticket_action_v3_upsert_pending(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    action_id: &str,
    target: &str,
    reason: &str,
    now: &str,
) -> TicketremoteTicketActionV3 {
    let id = ticket_action_v3_row_id(ticket_id, backend_id, action_id);
    let row = TicketremoteTicketActionV3 {
        id: id.clone(),
        actionId: action_id.into(),
        ticketId: clean_ticket_id(ticket_id),
        backendId: clean_backend_id(backend_id),
        target: target.into(),
        parentActionId: None,
        rootActionId: Some(action_id.into()),
        retryOrdinal: 0,
        status: "pending".into(),
        phase: "queued".into(),
        currentView: "unknown".into(),
        switchAvailable: false,
        switchExpiresAt: String::new(),
        streamEpoch: "0".into(),
        frameSequence: "0".into(),
        reason: ticket_action_v3_public_reason(reason, "ticket_action_queued"),
        createdAt: now.into(),
        updatedAt: now.into(),
        completedAt: String::new(),
        expiresAt: add_ms(now, HISTORY_TTL_MS),
    };
    if let Some(existing) = ctx.db.ticketremote_ticket_action_v3().id().find(&id) {
        return existing;
    }
    ctx.db.ticketremote_ticket_action_v3().insert(row.clone());
    row
}

fn ticket_action_v3_finish_without_command(
    ctx: &ReducerContext,
    row: TicketremoteTicketActionV3,
    reason: &str,
    now: &str,
) {
    let (status, phase, projected_reason, emit_command) = ticket_action_v3_rejection_plan(reason);
    debug_assert!(!emit_command);
    ctx.db
        .ticketremote_ticket_action_v3()
        .id()
        .update(TicketremoteTicketActionV3 {
            status,
            phase,
            reason: projected_reason,
            updatedAt: now.into(),
            completedAt: now.into(),
            expiresAt: add_ms(now, HISTORY_TTL_MS),
            ..row
        });
}

fn ticket_action_v3_queue_id(ticket_id: &str, backend_id: &str) -> String {
    phone_row_id(ticket_id, backend_id)
}

fn current_vivi_credential_revision(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
) -> String {
    let id = phone_row_id(ticket_id, backend_id);
    ctx.db
        .ticketremote_vivi_credential_state()
        .id()
        .find(&id)
        .map(|row| row.revision)
        .or_else(|| {
            ctx.db
                .ticketremote_vivi_credentials()
                .id()
                .find(&id)
                .map(|row| row.revision)
        })
        .unwrap_or_default()
}

fn upsert_vivi_credential_state(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    configured: bool,
    revision: &str,
    now: &str,
) -> TicketremoteViviCredentialState {
    let row = TicketremoteViviCredentialState {
        id: phone_row_id(ticket_id, backend_id),
        ticketId: clean_ticket_id(ticket_id),
        backendId: clean_backend_id(backend_id),
        configured,
        revision: bounded_text(revision.trim(), 160),
        updatedAt: now.into(),
    };
    upsert_row!(ctx, ticketremote_vivi_credential_state, row)
}

fn upsert_vivi_reauth_owner(
    ctx: &ReducerContext,
    attempt_id: &str,
    ticket_id: &str,
    backend_id: &str,
    request_id: &str,
    owner_email: &str,
    now: &str,
) {
    let row = TicketremoteViviReauthOwner {
        id: attempt_id.into(),
        ticketId: clean_ticket_id(ticket_id),
        backendId: clean_backend_id(backend_id),
        requestId: request_id.trim().into(),
        ownerEmail: clean_email(owner_email),
        expiresAt: add_ms(now, HISTORY_TTL_MS),
    };
    upsert_row!(ctx, ticketremote_vivi_reauth_owner, row);
}

fn insert_vivi_reauth_attempt(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    request_id: &str,
    credential_revision: &str,
    owner_email: &str,
    status: &str,
    phase: &str,
    reason: &str,
    now: &str,
) -> Result<TicketremoteViviReauthAttempt, String> {
    let id = vivi_reauth_attempt_id(ticket_id, backend_id, request_id);
    let table = ctx.db.ticketremote_vivi_reauth_attempt();
    if let Some(existing) = table.id().find(&id) {
        if existing.requestId == request_id.trim()
            && existing.credentialRevision == credential_revision.trim()
        {
            return Ok(existing);
        }
        return Err("vivi_reauth_request_id_reused".into());
    }
    let row = TicketremoteViviReauthAttempt {
        id,
        requestId: request_id.trim().into(),
        ticketId: clean_ticket_id(ticket_id),
        backendId: clean_backend_id(backend_id),
        credentialRevision: bounded_text(credential_revision.trim(), 160),
        ownerPublicId: account_public_id(owner_email),
        status: vivi_reauth_status(status)?,
        phase: vivi_reauth_phase(phase)?,
        reason: vivi_reauth_reason(reason)?,
        proofSource: String::new(),
        streamEpoch: "0".into(),
        frameSequence: "0".into(),
        createdAt: now.into(),
        updatedAt: now.into(),
        completedAt: String::new(),
        expiresAt: add_ms(now, HISTORY_TTL_MS),
    };
    table.insert(row.clone());
    upsert_vivi_reauth_owner(
        ctx,
        &row.id,
        &row.ticketId,
        &row.backendId,
        &row.requestId,
        owner_email,
        now,
    );
    Ok(row)
}

fn finish_vivi_reauth_attempt(
    ctx: &ReducerContext,
    mut row: TicketremoteViviReauthAttempt,
    status: &str,
    phase: &str,
    reason: &str,
    proof_source: &str,
    stream_epoch: &str,
    frame_sequence: &str,
    now: &str,
) -> Result<(), String> {
    row.status = vivi_reauth_status(status)?;
    row.phase = vivi_reauth_phase(phase)?;
    row.reason = vivi_reauth_reason(reason)?;
    row.proofSource = vivi_reauth_proof_source(proof_source)?;
    row.streamEpoch = bounded_frame_ordinal(stream_epoch);
    row.frameSequence = bounded_frame_ordinal(frame_sequence);
    row.updatedAt = now.into();
    if vivi_reauth_terminal(&row.status) {
        row.completedAt = now.into();
    }
    row.expiresAt = add_ms(now, HISTORY_TTL_MS);
    let terminal = vivi_reauth_terminal(&row.status);
    let id = row.id.clone();
    ctx.db.ticketremote_vivi_reauth_attempt().id().update(row);
    if terminal {
        ctx.db.ticketremote_vivi_reauth_owner().id().delete(id);
    }
    Ok(())
}

fn vivi_reauth_command_payload(
    request_id: &str,
    credential_revision: &str,
    mode: ViviReauthMode,
) -> String {
    match mode {
        ViviReauthMode::LegacyV1 => serde_json::json!({
            "version": 1,
            "requestId": request_id,
            "credentialRevision": credential_revision,
        })
        .to_string(),
        ViviReauthMode::FullResetV2 => serde_json::json!({
            "version": 2,
            "requestId": request_id,
            "credentialRevision": credential_revision,
            "resetAppData": true,
        })
        .to_string(),
        ViviReauthMode::LogoutLoginV3 => serde_json::json!({
            "version": 3,
            "requestId": request_id,
            "credentialRevision": credential_revision,
            "logoutInApp": true,
        })
        .to_string(),
        ViviReauthMode::LogoutLoginRedetectV4 => serde_json::json!({
            "version": 4,
            "requestId": request_id,
            "credentialRevision": credential_revision,
            "logoutInApp": true,
            "redetectAfterLogin": true,
        })
        .to_string(),
    }
}

fn vivi_reauth_queued_intent_payload(credential_revision: &str, mode: ViviReauthMode) -> String {
    match mode {
        ViviReauthMode::LegacyV1 => json_object(&[("credentialRevision", credential_revision)]),
        ViviReauthMode::FullResetV2 => serde_json::json!({
            "credentialRevision": credential_revision,
            "resetAppData": true,
        })
        .to_string(),
        ViviReauthMode::LogoutLoginV3 => serde_json::json!({
            "credentialRevision": credential_revision,
            "logoutInApp": true,
        })
        .to_string(),
        ViviReauthMode::LogoutLoginRedetectV4 => serde_json::json!({
            "credentialRevision": credential_revision,
            "logoutInApp": true,
            "redetectAfterLogin": true,
        })
        .to_string(),
    }
}

fn vivi_reauth_queued_intent_fields(payload_json: &str) -> Option<(String, ViviReauthMode)> {
    let value = serde_json::from_str::<serde_json::Value>(payload_json).ok()?;
    let object = value.as_object()?;
    let credential_revision = clean_vivi_revision(
        object
            .get("credentialRevision")
            .and_then(serde_json::Value::as_str)?,
    )
    .ok()?;
    match (
        object.len(),
        object.get("resetAppData"),
        object.get("logoutInApp"),
        object.get("redetectAfterLogin"),
    ) {
        (1, None, None, None) => Some((credential_revision, ViviReauthMode::LegacyV1)),
        (2, Some(serde_json::Value::Bool(true)), None, None) => {
            Some((credential_revision, ViviReauthMode::FullResetV2))
        }
        (2, None, Some(serde_json::Value::Bool(true)), None) => {
            Some((credential_revision, ViviReauthMode::LogoutLoginV3))
        }
        (3, None, Some(serde_json::Value::Bool(true)), Some(serde_json::Value::Bool(true))) => {
            Some((credential_revision, ViviReauthMode::LogoutLoginRedetectV4))
        }
        _ => None,
    }
}

fn admit_vivi_reauth(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    request_id: &str,
    credential_revision: &str,
    mode: ViviReauthMode,
    owner_email: &str,
    now: &str,
) -> Result<(), String> {
    let attempt = insert_vivi_reauth_attempt(
        ctx,
        ticket_id,
        backend_id,
        request_id,
        credential_revision,
        owner_email,
        "pending",
        "queued",
        "requested",
        now,
    )?;
    if attempt.status != "pending" {
        return Ok(());
    }
    let payload = vivi_reauth_command_payload(request_id, credential_revision, mode);
    insert_stream_command(
        ctx,
        ticket_id,
        backend_id,
        &vivi_reauth_command_id(ticket_id, backend_id, request_id),
        "vivi_reauth",
        credential_revision,
        "vivi_reauth_requested",
        &payload,
        VIVI_REAUTH_COMMAND_TTL_MS,
        now,
    );
    Ok(())
}

fn queue_vivi_reauth_intent(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    request_id: &str,
    credential_revision: &str,
    mode: ViviReauthMode,
    owner_email: &str,
    now: &str,
) -> Result<(), String> {
    let queue_id = ticket_action_v3_queue_id(ticket_id, backend_id);
    if ctx
        .db
        .ticketremote_ticket_action_v3_queued_intent()
        .id()
        .find(&queue_id)
        .is_some_and(|row| parse_time_ms(&row.expiresAt) > parse_time_ms(now))
    {
        return Err("ticket_action_queue_full".into());
    }
    ctx.db
        .ticketremote_ticket_action_v3_queued_intent()
        .id()
        .delete(&queue_id);
    insert_vivi_reauth_attempt(
        ctx,
        ticket_id,
        backend_id,
        request_id,
        credential_revision,
        owner_email,
        "queued",
        "waiting_for_phone_lane",
        "queued",
        now,
    )?;
    let expires_at = command_expires_at(now, VIVI_REAUTH_COMMAND_TTL_MS);
    ctx.db.ticketremote_ticket_action_v3_queued_intent().insert(
        TicketremoteTicketActionV3QueuedIntent {
            id: queue_id,
            ticketId: clean_ticket_id(ticket_id),
            backendId: clean_backend_id(backend_id),
            actionId: request_id.into(),
            kind: "vivi_reauth".into(),
            target: String::new(),
            source: "ticket_remote_admin".into(),
            reason: "vivi_reauth_queued".into(),
            attemptId: String::new(),
            expectedInteractionRevision: String::new(),
            scheduleId: String::new(),
            requestedEmail: owner_email.into(),
            privatePayloadJson: vivi_reauth_queued_intent_payload(credential_revision, mode),
            createdAt: now.into(),
            expiresAt: expires_at.clone(),
        },
    );
    ctx.db
        .ticketremote_stream_command()
        .insert(TicketremoteStreamCommand {
            id: vivi_reauth_command_id(ticket_id, backend_id, request_id),
            ticketId: clean_ticket_id(ticket_id),
            backendId: clean_backend_id(backend_id),
            commandType: "vivi_reauth".into(),
            status: "queued".into(),
            revision: credential_revision.into(),
            reason: "vivi_reauth_queued".into(),
            payloadJson: json_object(&[
                ("requestId", request_id),
                ("credentialRevision", credential_revision),
                ("queueSlot", "1"),
            ]),
            createdAt: now.into(),
            updatedAt: now.into(),
            expiresAt: expires_at,
        });
    Ok(())
}

#[allow(clippy::too_many_arguments)]
fn queue_ticket_action_v3_intent(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    action_id: &str,
    target: &str,
    source: &str,
    reason: &str,
    attempt_id: &str,
    expected_interaction_revision: &str,
    schedule_id: &str,
    email: &str,
    now: &str,
) -> Result<(), String> {
    let queue_id = ticket_action_v3_queue_id(ticket_id, backend_id);
    if ctx
        .db
        .ticketremote_ticket_action_v3_queued_intent()
        .id()
        .find(&queue_id)
        .is_some_and(|row| parse_time_ms(&row.expiresAt) > parse_time_ms(now))
    {
        return Err("ticket_action_queue_full".into());
    }
    ctx.db
        .ticketremote_ticket_action_v3_queued_intent()
        .id()
        .delete(&queue_id);
    let action =
        ticket_action_v3_upsert_pending(ctx, ticket_id, backend_id, action_id, target, reason, now);
    ctx.db
        .ticketremote_ticket_action_v3()
        .id()
        .update(TicketremoteTicketActionV3 {
            status: "queued".into(),
            phase: "waiting_for_phone_lane".into(),
            reason: "ticket_action_queued".into(),
            ..action
        });
    let expires_at = command_expires_at(now, TICKET_ACTIVATION_COMMAND_TTL_MS);
    ctx.db.ticketremote_ticket_action_v3_queued_intent().insert(
        TicketremoteTicketActionV3QueuedIntent {
            id: queue_id,
            ticketId: clean_ticket_id(ticket_id),
            backendId: clean_backend_id(backend_id),
            actionId: action_id.into(),
            kind: "ticket_action_v3".into(),
            target: target.into(),
            source: source.into(),
            reason: reason.into(),
            attemptId: attempt_id.into(),
            expectedInteractionRevision: expected_interaction_revision.into(),
            scheduleId: schedule_id.into(),
            requestedEmail: email.into(),
            privatePayloadJson: "{}".into(),
            createdAt: now.into(),
            expiresAt: expires_at.clone(),
        },
    );
    let payload = serde_json::json!({
        "version": 3,
        "actionId": action_id,
        "target": target,
        "source": source,
        "reason": "ticket_action_queued",
        "attemptId": attempt_id,
        "expectedInteractionRevision": expected_interaction_revision,
        "scheduleId": schedule_id,
        "queueSlot": 1,
    })
    .to_string();
    let command_id = ticket_action_v3_command_id(ticket_id, backend_id, action_id);
    ctx.db
        .ticketremote_stream_command()
        .insert(TicketremoteStreamCommand {
            id: command_id,
            ticketId: clean_ticket_id(ticket_id),
            backendId: clean_backend_id(backend_id),
            commandType: "ticket_action_v3".into(),
            status: "queued".into(),
            revision: action_id.into(),
            reason: "ticket_action_queued".into(),
            payloadJson: safe_json_string(&payload, SAFE_JSON_MAX_BYTES),
            createdAt: now.into(),
            updatedAt: now.into(),
            expiresAt: expires_at,
        });
    Ok(())
}

#[allow(clippy::too_many_arguments)]
fn queue_control_code_intent(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    request_id: &str,
    session_id: &str,
    digits: &str,
    expected_fast_revision: &str,
    email: &str,
    now: &str,
) -> Result<(), String> {
    let queue_id = ticket_action_v3_queue_id(ticket_id, backend_id);
    if ctx
        .db
        .ticketremote_ticket_action_v3_queued_intent()
        .id()
        .find(&queue_id)
        .is_some_and(|row| parse_time_ms(&row.expiresAt) > parse_time_ms(now))
    {
        return Err("ticket_action_queue_full".into());
    }
    ctx.db
        .ticketremote_ticket_action_v3_queued_intent()
        .id()
        .delete(&queue_id);
    let expires_at = command_expires_at(now, CONTROL_CODE_PHONE_TTL_MS);
    let private_payload = serde_json::json!({
        "sessionId": session_id,
        "digits": digits,
        "expectedFastRevision": expected_fast_revision,
    })
    .to_string();
    ctx.db.ticketremote_ticket_action_v3_queued_intent().insert(
        TicketremoteTicketActionV3QueuedIntent {
            id: queue_id,
            ticketId: clean_ticket_id(ticket_id),
            backendId: clean_backend_id(backend_id),
            actionId: request_id.into(),
            kind: "control_code".into(),
            target: String::new(),
            source: "browser_spacetime".into(),
            reason: "control_code_queued".into(),
            attemptId: String::new(),
            expectedInteractionRevision: String::new(),
            scheduleId: String::new(),
            requestedEmail: email.into(),
            privatePayloadJson: safe_json_string(&private_payload, SAFE_JSON_MAX_BYTES),
            createdAt: now.into(),
            expiresAt: expires_at.clone(),
        },
    );
    insert_control_code_public_request(ctx, ticket_id, request_id, &account_public_id(email), now);
    ctx.db
        .ticketremote_stream_command()
        .insert(TicketremoteStreamCommand {
            id: format!("{}:generate_control_code", request_id),
            ticketId: clean_ticket_id(ticket_id),
            backendId: clean_backend_id(backend_id),
            commandType: "generate_control_code".into(),
            status: "queued".into(),
            revision: request_id.into(),
            reason: "control_code_queued".into(),
            payloadJson: json_object(&[("requestId", request_id), ("queueSlot", "1")]),
            createdAt: now.into(),
            updatedAt: now.into(),
            expiresAt: expires_at,
        });
    Ok(())
}

fn finish_queued_control_code_request(
    ctx: &ReducerContext,
    ticket_id: &str,
    request_id: &str,
    requested_email: &str,
    reason: &str,
    now: &str,
) {
    insert_control_code_public_request(
        ctx,
        ticket_id,
        request_id,
        &account_public_id(requested_email),
        now,
    );
    update_control_code_public_request(
        ctx,
        request_id,
        ControlCodeChanges {
            status: Some("failed".into()),
            reason: Some(safe_token(&bounded_text(reason, 120), "request_rejected")),
            captureRequired: Some(false),
            cleanupPending: Some(false),
            expiresAt: Some(command_expires_at(now, CONTROL_CODE_COMMAND_TTL_MS)),
            ..Default::default()
        },
        now,
    );
}

fn promote_ticket_action_v3_queue(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    now: &str,
) {
    let queue_id = ticket_action_v3_queue_id(ticket_id, backend_id);
    let Some(intent) = ctx
        .db
        .ticketremote_ticket_action_v3_queued_intent()
        .id()
        .find(&queue_id)
    else {
        return;
    };
    let ignored_control_request_id = if intent.kind == "control_code" {
        intent.actionId.as_str()
    } else {
        ""
    };
    if ticket_phone_mutation_lane_conflict_ignoring_control_request(
        ctx,
        ticket_id,
        backend_id,
        ignored_control_request_id,
        now,
    )
    .is_some()
    {
        return;
    }
    if intent.kind == "vivi_reauth" {
        let command_id = vivi_reauth_command_id(ticket_id, backend_id, &intent.actionId);
        ctx.db
            .ticketremote_ticket_action_v3_queued_intent()
            .id()
            .delete(&queue_id);
        ctx.db
            .ticketremote_stream_command()
            .id()
            .delete(&command_id);
        let attempt_id = vivi_reauth_attempt_id(ticket_id, backend_id, &intent.actionId);
        let Some(attempt) = ctx
            .db
            .ticketremote_vivi_reauth_attempt()
            .id()
            .find(&attempt_id)
        else {
            return;
        };
        let queued_fields = vivi_reauth_queued_intent_fields(&intent.privatePayloadJson);
        let credential_revision = queued_fields
            .as_ref()
            .map(|(revision, _)| revision.as_str())
            .unwrap_or("");
        let mode = queued_fields.as_ref().map(|(_, mode)| *mode);
        let rejection = if parse_time_ms(&intent.expiresAt) <= parse_time_ms(now) {
            Some("command_expired")
        } else if queued_fields.is_none() {
            Some("credential_revision_stale")
        } else if mode.is_none_or(|mode| !vivi_reauth_request_mode_matches(&intent.actionId, mode))
        {
            Some("internal_failure")
        } else if !is_owner(ctx, ticket_id, &intent.requestedEmail) {
            Some("owner_role_required")
        } else {
            let credentials = ctx
                .db
                .ticketremote_vivi_credentials()
                .id()
                .find(phone_row_id(ticket_id, backend_id));
            if credentials
                .as_ref()
                .is_none_or(|row| row.revision != credential_revision)
            {
                Some("credential_revision_stale")
            } else {
                None
            }
        };
        if let Some(reason) = rejection {
            let _ = finish_vivi_reauth_attempt(
                ctx,
                attempt,
                "failed",
                "complete",
                reason,
                "spacetimedb",
                "0",
                "0",
                now,
            );
            return;
        }
        ctx.db
            .ticketremote_vivi_reauth_attempt()
            .id()
            .delete(&attempt_id);
        let _ = admit_vivi_reauth(
            ctx,
            ticket_id,
            backend_id,
            &intent.actionId,
            credential_revision,
            mode.expect("validated queued ViVi re-auth mode"),
            &intent.requestedEmail,
            now,
        );
        return;
    }
    if intent.kind == "control_code" {
        let command_id = format!("{}:generate_control_code", intent.actionId);
        ctx.db
            .ticketremote_ticket_action_v3_queued_intent()
            .id()
            .delete(&queue_id);
        ctx.db
            .ticketremote_stream_command()
            .id()
            .delete(&command_id);
        delete_control_code_request(ctx, &intent.actionId);
        if parse_time_ms(&intent.expiresAt) <= parse_time_ms(now) {
            finish_queued_control_code_request(
                ctx,
                ticket_id,
                &intent.actionId,
                &intent.requestedEmail,
                "command_expired",
                now,
            );
            return;
        }
        if !is_member(ctx, ticket_id, &intent.requestedEmail) {
            finish_queued_control_code_request(
                ctx,
                ticket_id,
                &intent.actionId,
                &intent.requestedEmail,
                "membership_required",
                now,
            );
            return;
        }
        let payload = serde_json::from_str::<serde_json::Value>(&intent.privatePayloadJson)
            .unwrap_or_else(|_| serde_json::json!({}));
        let session_id = payload
            .get("sessionId")
            .and_then(|value| value.as_str())
            .unwrap_or("");
        let digits = payload
            .get("digits")
            .and_then(|value| value.as_str())
            .unwrap_or("");
        let expected = payload
            .get("expectedFastRevision")
            .and_then(|value| value.as_str())
            .unwrap_or("");
        if let Err(error) = admit_control_code_request_impl(
            ctx,
            ticket_id,
            backend_id,
            session_id,
            digits,
            expected,
            &intent.requestedEmail,
            Some(&intent.actionId),
            now,
        ) {
            finish_queued_control_code_request(
                ctx,
                ticket_id,
                &intent.actionId,
                &intent.requestedEmail,
                &error,
                now,
            );
        }
        return;
    }
    let command_id = ticket_action_v3_command_id(ticket_id, backend_id, &intent.actionId);
    ctx.db
        .ticketremote_ticket_action_v3_queued_intent()
        .id()
        .delete(&queue_id);
    ctx.db
        .ticketremote_stream_command()
        .id()
        .delete(&command_id);
    ctx.db
        .ticketremote_ticket_action_v3()
        .id()
        .delete(ticket_action_v3_row_id(
            ticket_id,
            backend_id,
            &intent.actionId,
        ));
    if parse_time_ms(&intent.expiresAt) <= parse_time_ms(now) {
        let row = ticket_action_v3_upsert_pending(
            ctx,
            ticket_id,
            backend_id,
            &intent.actionId,
            &intent.target,
            "command_expired",
            now,
        );
        ticket_action_v3_finish_without_command(ctx, row, "command_expired", now);
        return;
    }
    if !is_member(ctx, ticket_id, &intent.requestedEmail) {
        let row = ticket_action_v3_upsert_pending(
            ctx,
            ticket_id,
            backend_id,
            &intent.actionId,
            &intent.target,
            "membership_required",
            now,
        );
        ticket_action_v3_finish_without_command(ctx, row, "membership_required", now);
        return;
    }
    if request_ticket_action_v3_impl(
        ctx,
        3,
        ticket_id,
        backend_id,
        &intent.actionId,
        &intent.target,
        &intent.source,
        &intent.reason,
        &intent.attemptId,
        &intent.expectedInteractionRevision,
        &intent.scheduleId,
        &intent.requestedEmail,
        now,
    )
    .is_err()
    {
        let row = ticket_action_v3_upsert_pending(
            ctx,
            ticket_id,
            backend_id,
            &intent.actionId,
            &intent.target,
            "ticket_action_rejected",
            now,
        );
        ticket_action_v3_finish_without_command(ctx, row, "ticket_action_rejected", now);
    }
}

fn ticket_action_v3_rejection_plan(reason: &str) -> (String, String, String, bool) {
    (
        "failed".into(),
        "rejected".into(),
        ticket_action_v3_public_reason(reason, "ticket_action_rejected"),
        false,
    )
}

fn ticket_action_v3_committed_rejection() -> Result<(), String> {
    // Reducer errors roll the transaction back. A user-visible rejection is a
    // terminal projection, so it must commit after the row above is updated.
    Ok(())
}

fn activation_history_id(ticket_id: &str, backend_id: &str, attempt_id: &str) -> String {
    format!(
        "activation:{}:{}:{}",
        clean_ticket_id(ticket_id),
        clean_backend_id(backend_id),
        attempt_id.trim()
    )
}

fn activation_decision_id(ticket_id: &str, backend_id: &str, attempt_id: &str) -> String {
    format!(
        "activation-decision:{}",
        public_hash(
            &format!(
                "{}|{}|{}",
                clean_ticket_id(ticket_id),
                clean_backend_id(backend_id),
                attempt_id.trim()
            ),
            24,
        )
    )
}

fn activation_minute_bucket(value: &str) -> String {
    let millis = parse_time_ms(value);
    if millis <= 0 {
        return canonical_time(value);
    }
    iso(Timestamp::from_micros_since_unix_epoch(
        (millis - millis.rem_euclid(60_000)).saturating_mul(1_000),
    ))
}

fn activation_day_bucket(value: &str) -> String {
    let canonical = canonical_time(value);
    canonical.chars().take(10).collect()
}

#[derive(Clone, Debug, PartialEq, Eq)]
struct MemberLimitEvaluation {
    registration_allowed: bool,
    registration_reason: &'static str,
    registration_count: usize,
    registration_retry_at_ms: i64,
    registration_next_release_at_ms: i64,
    control_code_allowed: bool,
    control_code_reason: &'static str,
    control_code_count: usize,
    control_code_retry_at_ms: i64,
    next_boundary_ms: i64,
}

fn member_limit_evaluation(
    now_ms: i64,
    registration_admitted_at_ms: &[i64],
    control_code_admitted_at_ms: &[i64],
    effective_limited: bool,
) -> MemberLimitEvaluation {
    let registration_cutoff = now_ms.saturating_sub(REGISTRATION_RATE_WINDOW_MS);
    let mut registrations: Vec<i64> = registration_admitted_at_ms
        .iter()
        .copied()
        .filter(|at| *at > registration_cutoff && *at <= now_ms)
        .collect();
    registrations.sort_unstable();
    let registration_interval_until = registrations
        .last()
        .copied()
        .map(|at| at.saturating_add(REGISTRATION_RATE_INTERVAL_MS))
        .filter(|until| *until > now_ms)
        .unwrap_or(0);
    let registration_next_release_at_ms = registrations
        .first()
        .copied()
        .map(|at| at.saturating_add(REGISTRATION_RATE_WINDOW_MS))
        .filter(|until| *until > now_ms)
        .unwrap_or(0);
    let registration_quota_until = (registrations.len() >= REGISTRATION_RATE_LIMIT)
        .then_some(registration_next_release_at_ms)
        .unwrap_or(0);
    let registration_retry_at_ms = registration_interval_until.max(registration_quota_until);
    let registration_reason = if !effective_limited {
        "limits_bypassed"
    } else if registration_quota_until > now_ms {
        "registration_hour_limit"
    } else if registration_interval_until > now_ms {
        "registration_interval"
    } else {
        "registration_allowed"
    };
    let registration_allowed = !effective_limited || registration_retry_at_ms <= now_ms;

    let control_code_cutoff = now_ms.saturating_sub(CONTROL_CODE_RATE_WINDOW_MS);
    let mut control_codes: Vec<i64> = control_code_admitted_at_ms
        .iter()
        .copied()
        .filter(|at| *at > control_code_cutoff && *at <= now_ms)
        .collect();
    control_codes.sort_unstable();
    let control_code_retry_at_ms = if control_codes.len() >= CONTROL_CODE_RATE_LIMIT {
        control_codes[0].saturating_add(CONTROL_CODE_RATE_WINDOW_MS)
    } else {
        0
    };
    let control_code_reason = if !effective_limited {
        "limits_bypassed"
    } else if control_code_retry_at_ms > now_ms {
        "control_code_window_limit"
    } else {
        "control_code_allowed"
    };
    let control_code_allowed = !effective_limited || control_code_retry_at_ms <= now_ms;

    let next_boundary_ms = [
        registration_interval_until,
        registration_next_release_at_ms,
        control_codes
            .first()
            .copied()
            .map(|at| at.saturating_add(CONTROL_CODE_RATE_WINDOW_MS))
            .unwrap_or(0),
    ]
    .into_iter()
    .filter(|at| *at > now_ms)
    .min()
    .unwrap_or(0);
    MemberLimitEvaluation {
        registration_allowed,
        registration_reason,
        registration_count: registrations.len(),
        registration_retry_at_ms,
        registration_next_release_at_ms,
        control_code_allowed,
        control_code_reason,
        control_code_count: control_codes.len(),
        control_code_retry_at_ms,
        next_boundary_ms,
    }
}

fn activation_refresh_due_at_ms(activation_at_ms: i64) -> i64 {
    activation_at_ms.saturating_add(TICKET_ACTIVATION_RESET_DELAY_MS)
}

fn activation_input_fingerprint(
    flow: &str,
    ticket_id: &str,
    backend_id: &str,
    interaction_revision: &str,
    control_id: &str,
    reset_request_id: &str,
    input_sequence: &str,
) -> String {
    public_hash(
        &format!(
            "{}|{}|{}|{}|{}|{}|{}",
            activation_flow(flow),
            clean_ticket_id(ticket_id),
            clean_backend_id(backend_id),
            bounded_text(interaction_revision, 160),
            bounded_text(control_id, 120),
            bounded_text(reset_request_id, 120),
            bounded_frame_ordinal(input_sequence),
        ),
        16,
    )
}

fn activation_interaction_correlation(control_id: &str, reset_request_id: &str) -> String {
    bounded_text(&non_empty(control_id, reset_request_id), 120)
}

fn activation_correlation_matches_current(
    flow: &str,
    stored_correlation: &str,
    current_control_id: &str,
    current_reset_request_id: &str,
) -> bool {
    let current_correlation = if activation_flow(flow) == "reset_and_activate" {
        current_reset_request_id
    } else {
        current_control_id
    };
    !stored_correlation.trim().is_empty() && stored_correlation == current_correlation
}

fn canonical_activation_backend(
    ctx: &ReducerContext,
    ticket_id: &str,
    requested_backend_id: &str,
) -> Result<String, String> {
    let ticket_id = clean_ticket_id(ticket_id);
    let requested = clean_backend_id(requested_backend_id);
    let rows: Vec<_> = ctx
        .db
        .ticketremote_phone_backend()
        .ticketId()
        .filter(&ticket_id)
        .collect();
    if rows.len() != 1 || rows[0].backendId != requested {
        return Err("backend_not_registered".into());
    }
    Ok(rows[0].backendId.clone())
}

fn activation_history_for_attempt(
    ctx: &ReducerContext,
    ticket_id: &str,
    attempt_id: &str,
) -> Option<TicketremoteActivationHistory> {
    let ticket_id = clean_ticket_id(ticket_id);
    let attempt_id = attempt_id.trim().to_string();
    ctx.db
        .ticketremote_activation_history()
        .ticketAttempt()
        .filter((&ticket_id, &attempt_id))
        .next()
}

fn activation_decision_from_row(row: &TicketremoteActivationDecision) -> ActivationAdmission {
    ActivationAdmission {
        accepted: row.accepted,
        ticket_id: row.ticketId.clone(),
        backend_id: row.backendId.clone(),
        flow: row.flow.clone(),
        attempt_id: row.attemptId.clone(),
        interaction_revision: row.interactionRevision.clone(),
        reason: row.reason.clone(),
        retry_at: row.retryAt.clone(),
    }
}

fn upsert_activation_decision(
    ctx: &ReducerContext,
    admission: &ActivationAdmission,
    input_fingerprint: &str,
    now: &str,
) {
    let row = TicketremoteActivationDecision {
        id: activation_decision_id(
            &admission.ticket_id,
            &admission.backend_id,
            &admission.attempt_id,
        ),
        ticketId: admission.ticket_id.clone(),
        backendId: admission.backend_id.clone(),
        attemptId: admission.attempt_id.clone(),
        flow: admission.flow.clone(),
        accepted: admission.accepted,
        reason: admission.reason.clone(),
        retryAt: admission.retry_at.clone(),
        serverAt: now.into(),
        interactionRevision: admission.interaction_revision.clone(),
        inputFingerprint: input_fingerprint.into(),
        updatedAt: now.into(),
        expiresAt: add_ms(now, TICKET_ACTIVATION_DECISION_TTL_MS),
    };
    let table = ctx.db.ticketremote_activation_decision();
    if let Some(existing) = table.id().find(&row.id) {
        table.id().update(TicketremoteActivationDecision {
            updatedAt: now.into(),
            serverAt: now.into(),
            expiresAt: row.expiresAt.clone(),
            ..existing
        });
    } else {
        table.insert(row);
    }
}

fn record_activation_rejection(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    flow: &str,
    reason: &str,
    now: &str,
) {
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    let flow = activation_flow(flow);
    let reason = safe_token(&bounded_text(reason, 80), "activation_policy_rejected");
    let bucket = activation_minute_bucket(now);
    let id = format!(
        "activation-rejection:{}",
        public_hash(
            &format!(
                "{}|{}|{}|{}|{}",
                ticket_id, backend_id, flow, reason, bucket
            ),
            20,
        )
    );
    let table = ctx.db.ticketremote_activation_history();
    if let Some(existing) = table.id().find(&id) {
        table.id().update(TicketremoteActivationHistory {
            updatedAt: now.into(),
            occurrenceCount: existing.occurrenceCount.saturating_add(1),
            ..existing
        });
        return;
    }
    table.insert(TicketremoteActivationHistory {
        id,
        ticketId: ticket_id,
        backendId: backend_id,
        flow,
        admission: "rejected".into(),
        outcome: "rejected".into(),
        reason,
        occurredAt: bucket,
        occurrenceDay: activation_day_bucket(now),
        admittedAt: String::new(),
        updatedAt: now.into(),
        completedAt: now.into(),
        attemptId: String::new(),
        interactionRevision: String::new(),
        interactionCorrelation: String::new(),
        activationRevision: String::new(),
        inputFingerprint: String::new(),
        refreshDueAt: String::new(),
        refreshCompletedAt: String::new(),
        refreshOutcome: String::new(),
        refreshRetryAt: String::new(),
        refreshAttempt: 0,
        occurrenceCount: 1,
        expiresAt: add_ms(now, TICKET_ACTIVATION_LEDGER_TTL_MS),
    });
}

#[derive(Clone, Debug, PartialEq, Eq)]
struct MemberLimitAdmission {
    allowed: bool,
    reason: String,
    retry_at: String,
}

fn member_limit_state_id(ticket_id: &str, email: &str) -> String {
    format!(
        "{}:{}:member-limits",
        clean_ticket_id(ticket_id),
        account_public_id(email)
    )
}

fn member_limit_event_id(ticket_id: &str, email: &str, kind: &str, correlation_id: &str) -> String {
    format!(
        "member-limit:{}:{}:{}:{}",
        clean_ticket_id(ticket_id),
        clean_email(email),
        safe_token(kind, "event"),
        bounded_text(correlation_id, 160)
    )
}

fn member_limit_preference(
    ctx: &ReducerContext,
    ticket_id: &str,
    email: &str,
) -> Option<TicketremoteMemberLimitPreference> {
    ctx.db
        .ticketremote_member_limit_preference()
        .id()
        .find(member_id(ticket_id, email))
}

fn member_hdr_state_id(ticket_id: &str, email: &str) -> String {
    format!(
        "{}:{}:member-hdr",
        clean_ticket_id(ticket_id),
        account_scope_id(email)
    )
}

fn member_hdr_preference(
    ctx: &ReducerContext,
    ticket_id: &str,
    email: &str,
) -> Option<TicketremoteMemberHDRPreference> {
    ctx.db
        .ticketremote_member_hdr_preference()
        .id()
        .find(member_id(ticket_id, email))
}

fn refresh_member_hdr_state(ctx: &ReducerContext, ticket_id: &str, email: &str, now: &str) {
    let ticket_id = clean_ticket_id(ticket_id);
    let email = clean_email(email);
    let id = member_hdr_state_id(&ticket_id, &email);
    let table = ctx.db.ticketremote_member_hdr_state();
    if !is_member(ctx, &ticket_id, &email) {
        table.id().delete(id);
        return;
    }
    let enabled = member_hdr_preference(ctx, &ticket_id, &email)
        .map(|row| row.enabled)
        .unwrap_or(false);
    let row = TicketremoteMemberHDRState {
        id,
        ticketId: ticket_id,
        accountScopeId: account_scope_id(&email),
        enabled,
        updatedAt: now.into(),
        serverAt: now.into(),
    };
    if table.id().find(&row.id).is_some() {
        table.id().update(row);
    } else {
        table.insert(row);
    }
}

fn member_hdr_engine_state_id(ticket_id: &str, email: &str) -> String {
    format!(
        "{}:{}:member-hdr-engine",
        clean_ticket_id(ticket_id),
        account_scope_id(email)
    )
}

fn member_hdr_engine_preference(
    ctx: &ReducerContext,
    ticket_id: &str,
    email: &str,
) -> Option<TicketremoteMemberHDREnginePreference> {
    ctx.db
        .ticketremote_member_hdr_engine_preference()
        .id()
        .find(member_id(ticket_id, email))
}

fn migrate_legacy_member_hdr_engine_preference(
    ctx: &ReducerContext,
    ticket_id: &str,
    email: &str,
    now: &str,
) {
    let id = member_id(ticket_id, email);
    let table = ctx.db.ticketremote_member_hdr_engine_preference();
    if let Some(existing) = table.id().find(&id) {
        if existing.engine.trim() == "client_webgpu_v2" {
            return;
        }
        table.id().update(TicketremoteMemberHDREnginePreference {
            engine: "client_webgpu_v2".into(),
            updatedAt: now.into(),
            ..existing
        });
    } else {
        table.insert(TicketremoteMemberHDREnginePreference {
            id,
            ticketId: clean_ticket_id(ticket_id),
            email: clean_email(email),
            engine: "client_webgpu_v2".into(),
            createdAt: now.into(),
            updatedAt: now.into(),
        });
    }
}

fn refresh_member_hdr_engine_state(ctx: &ReducerContext, ticket_id: &str, email: &str, now: &str) {
    let ticket_id = clean_ticket_id(ticket_id);
    let email = clean_email(email);
    let id = member_hdr_engine_state_id(&ticket_id, &email);
    let table = ctx.db.ticketremote_member_hdr_engine_state();
    if !is_owner(ctx, &ticket_id, &email) {
        table.id().delete(id);
        return;
    }
    let engine = member_hdr_engine_preference(ctx, &ticket_id, &email)
        .map(|row| clean_hdr_engine(&row.engine))
        .unwrap_or_else(|| "client_webgpu_v2".into());
    let row = TicketremoteMemberHDREngineState {
        id,
        ticketId: ticket_id,
        accountScopeId: account_scope_id(&email),
        engine,
        updatedAt: now.into(),
        serverAt: now.into(),
    };
    if table.id().find(&row.id).is_some() {
        table.id().update(row);
    } else {
        table.insert(row);
    }
}

fn member_hdr_boost_state_id(ticket_id: &str, email: &str) -> String {
    format!(
        "{}:{}:member-hdr-boost",
        clean_ticket_id(ticket_id),
        account_scope_id(email)
    )
}

fn member_hdr_boost_preference(
    ctx: &ReducerContext,
    ticket_id: &str,
    email: &str,
) -> Option<TicketremoteMemberHDRBoostPreference> {
    ctx.db
        .ticketremote_member_hdr_boost_preference()
        .id()
        .find(member_id(ticket_id, email))
}

fn migrate_legacy_member_hdr_boost_preference(
    ctx: &ReducerContext,
    ticket_id: &str,
    email: &str,
    now: &str,
) {
    let id = member_id(ticket_id, email);
    let table = ctx.db.ticketremote_member_hdr_boost_preference();
    let Some(existing) = table.id().find(&id) else {
        return;
    };
    let selected_display_boost = clean_hdr_display_boost(existing.selectedDisplayBoost);
    if existing.selectedDisplayBoost == selected_display_boost {
        return;
    }
    table.id().update(TicketremoteMemberHDRBoostPreference {
        selectedDisplayBoost: selected_display_boost,
        updatedAt: now.into(),
        ..existing
    });
}

fn refresh_member_hdr_boost_state(ctx: &ReducerContext, ticket_id: &str, email: &str, now: &str) {
    let ticket_id = clean_ticket_id(ticket_id);
    let email = clean_email(email);
    let id = member_hdr_boost_state_id(&ticket_id, &email);
    let table = ctx.db.ticketremote_member_hdr_boost_state();
    if !is_member(ctx, &ticket_id, &email) {
        table.id().delete(id);
        return;
    }
    migrate_legacy_member_hdr_boost_preference(ctx, &ticket_id, &email, now);
    let selected_display_boost = member_hdr_boost_preference(ctx, &ticket_id, &email)
        .map(|row| clean_hdr_display_boost(row.selectedDisplayBoost))
        .unwrap_or(4);
    let row = TicketremoteMemberHDRBoostState {
        id,
        ticketId: ticket_id,
        accountScopeId: account_scope_id(&email),
        selectedDisplayBoost: selected_display_boost,
        updatedAt: now.into(),
        serverAt: now.into(),
    };
    if table.id().find(&row.id).is_some() {
        table.id().update(row);
    } else {
        table.insert(row);
    }
}

fn member_limit_effective_config(
    ctx: &ReducerContext,
    ticket_id: &str,
    email: &str,
) -> (bool, bool, bool) {
    let can_bypass = is_admin(ctx, ticket_id, email);
    let stored_obey = member_limit_preference(ctx, ticket_id, email)
        .map(|row| row.obeyLimits)
        .unwrap_or(true);
    // A demoted or ordinary member is always enforced even if an older admin
    // preference remains private for a possible later role restoration.
    let obey_limits = if can_bypass { stored_obey } else { true };
    (obey_limits, can_bypass, !can_bypass || obey_limits)
}

fn member_limit_counted_times(
    ctx: &ReducerContext,
    ticket_id: &str,
    email: &str,
    kind: &str,
    now: &str,
) -> Vec<i64> {
    let ticket_id = clean_ticket_id(ticket_id);
    let email = clean_email(email);
    let window_ms = if kind == "registration" {
        REGISTRATION_RATE_WINDOW_MS
    } else {
        CONTROL_CODE_RATE_WINDOW_MS
    };
    let cutoff = parse_time_ms(now).saturating_sub(window_ms);
    let events: Vec<_> = ctx
        .db
        .ticketremote_member_limit_event()
        .ticketEmailKindAt()
        .filter((&ticket_id, &email, kind))
        .filter(|row| row.counted && parse_time_ms(&row.admittedAt) > cutoff)
        .collect();
    let mut times: Vec<i64> = events
        .iter()
        .map(|row| parse_time_ms(&row.admittedAt))
        .collect();
    if kind == "control_code" {
        // Preserve an in-flight pre-rollout control-code quota. New requests
        // have a matching policy event and are therefore not double-counted.
        for owner in ctx
            .db
            .ticketremote_control_code_owner()
            .ticketEmail()
            .filter((&ticket_id, &email))
        {
            let requested_at = parse_time_ms(&owner.requestedAt);
            if requested_at <= cutoff || requested_at > parse_time_ms(now) {
                continue;
            }
            if !events.iter().any(|event| event.correlationId == owner.id) {
                times.push(requested_at);
            }
        }
    }
    times
}

fn delete_policy_boundary_timers(
    ctx: &ReducerContext,
    ticket_id: &str,
    subject_kind: &str,
    subject_id: &str,
) {
    let ticket_id = clean_ticket_id(ticket_id);
    let subject_kind = safe_token(subject_kind, "member");
    let subject_id = subject_id.trim().to_string();
    let rows: Vec<_> = ctx
        .db
        .ticketremote_policy_boundary_timer()
        .ticketSubject()
        .filter((&ticket_id, &subject_kind, &subject_id))
        .collect();
    for row in rows {
        ctx.db
            .ticketremote_policy_boundary_timer()
            .scheduled_id()
            .delete(row.scheduled_id);
    }
}

fn replace_policy_boundary_timer(
    ctx: &ReducerContext,
    ticket_id: &str,
    subject_kind: &str,
    subject_id: &str,
    boundary_at_ms: i64,
    now: &str,
) {
    delete_policy_boundary_timers(ctx, ticket_id, subject_kind, subject_id);
    if boundary_at_ms <= parse_time_ms(now) {
        return;
    }
    let boundary_at = iso(Timestamp::from_micros_since_unix_epoch(
        boundary_at_ms.saturating_mul(1_000),
    ));
    ctx.db
        .ticketremote_policy_boundary_timer()
        .insert(TicketremotePolicyBoundaryTimer {
            scheduled_id: 0,
            scheduled_at: ScheduleAt::Time(Timestamp::from_micros_since_unix_epoch(
                boundary_at_ms.saturating_mul(1_000),
            )),
            ticketId: clean_ticket_id(ticket_id),
            subjectKind: safe_token(subject_kind, "member"),
            subjectId: subject_id.trim().into(),
            boundaryAt: boundary_at,
            createdAt: now.into(),
        });
}

fn refresh_member_limit_state(
    ctx: &ReducerContext,
    ticket_id: &str,
    email: &str,
    now: &str,
) -> TicketremoteMemberLimitState {
    let ticket_id = clean_ticket_id(ticket_id);
    let email = clean_email(email);
    let owner_public_id = account_public_id(&email);
    let (obey_limits, can_bypass, effective_limited) =
        member_limit_effective_config(ctx, &ticket_id, &email);
    let registrations = member_limit_counted_times(ctx, &ticket_id, &email, "registration", now);
    let control_codes = member_limit_counted_times(ctx, &ticket_id, &email, "control_code", now);
    let evaluation = member_limit_evaluation(
        parse_time_ms(now),
        &registrations,
        &control_codes,
        effective_limited,
    );
    let timestamp_or_empty = |millis: i64| {
        if millis > 0 {
            iso(Timestamp::from_micros_since_unix_epoch(
                millis.saturating_mul(1_000),
            ))
        } else {
            String::new()
        }
    };
    let row = TicketremoteMemberLimitState {
        id: member_limit_state_id(&ticket_id, &email),
        ticketId: ticket_id.clone(),
        ownerPublicId: owner_public_id,
        obeyLimits: obey_limits,
        canBypass: can_bypass,
        effectiveLimited: effective_limited,
        registrationAllowed: evaluation.registration_allowed,
        registrationReason: evaluation.registration_reason.into(),
        registrationCount: evaluation.registration_count.min(u32::MAX as usize) as u32,
        registrationLimit: REGISTRATION_RATE_LIMIT as u32,
        registrationIntervalSeconds: (REGISTRATION_RATE_INTERVAL_MS / 1_000) as u32,
        registrationRetryAt: timestamp_or_empty(evaluation.registration_retry_at_ms),
        registrationNextReleaseAt: timestamp_or_empty(evaluation.registration_next_release_at_ms),
        controlCodeAllowed: evaluation.control_code_allowed,
        controlCodeReason: evaluation.control_code_reason.into(),
        controlCodeCount: evaluation.control_code_count.min(u32::MAX as usize) as u32,
        controlCodeLimit: CONTROL_CODE_RATE_LIMIT as u32,
        controlCodeWindowSeconds: (CONTROL_CODE_RATE_WINDOW_MS / 1_000) as u32,
        controlCodeRetryAt: timestamp_or_empty(evaluation.control_code_retry_at_ms),
        updatedAt: now.into(),
        serverAt: now.into(),
    };
    let table = ctx.db.ticketremote_member_limit_state();
    if table.id().find(&row.id).is_some() {
        table.id().update(row.clone());
    } else {
        table.insert(row.clone());
    }
    replace_policy_boundary_timer(
        ctx,
        &ticket_id,
        "member",
        &email,
        evaluation.next_boundary_ms,
        now,
    );
    row
}

fn admit_member_limit_event(
    ctx: &ReducerContext,
    ticket_id: &str,
    email: &str,
    kind: &str,
    correlation_id: &str,
    now: &str,
) -> Result<MemberLimitAdmission, String> {
    let ticket_id = clean_ticket_id(ticket_id);
    let email = clean_email(email);
    let kind = allowlisted(kind, &["registration", "control_code"], "");
    if kind.is_empty() {
        return Err("invalid_member_limit_kind".into());
    }
    let correlation_id = bounded_text(correlation_id.trim(), 160);
    if correlation_id.is_empty() {
        return Err("member_limit_correlation_required".into());
    }
    let event_id = member_limit_event_id(&ticket_id, &email, &kind, &correlation_id);
    if let Some(existing) = ctx
        .db
        .ticketremote_member_limit_event()
        .id()
        .find(&event_id)
    {
        if existing.ticketId != ticket_id
            || existing.email != email
            || existing.kind != kind
            || existing.correlationId != correlation_id
        {
            return Err("member_limit_correlation_reused".into());
        }
        refresh_member_limit_state(ctx, &ticket_id, &email, now);
        return Ok(MemberLimitAdmission {
            allowed: true,
            reason: if existing.counted {
                format!("{kind}_admitted")
            } else {
                "limits_bypassed".into()
            },
            retry_at: String::new(),
        });
    }
    let (_, _, effective_limited) = member_limit_effective_config(ctx, &ticket_id, &email);
    let registrations = member_limit_counted_times(ctx, &ticket_id, &email, "registration", now);
    let control_codes = member_limit_counted_times(ctx, &ticket_id, &email, "control_code", now);
    let evaluation = member_limit_evaluation(
        parse_time_ms(now),
        &registrations,
        &control_codes,
        effective_limited,
    );
    let (allowed, reason, retry_at_ms) = if kind == "registration" {
        (
            evaluation.registration_allowed,
            evaluation.registration_reason,
            evaluation.registration_retry_at_ms,
        )
    } else {
        (
            evaluation.control_code_allowed,
            evaluation.control_code_reason,
            evaluation.control_code_retry_at_ms,
        )
    };
    if !allowed {
        refresh_member_limit_state(ctx, &ticket_id, &email, now);
        return Ok(MemberLimitAdmission {
            allowed: false,
            reason: reason.into(),
            retry_at: if retry_at_ms > 0 {
                iso(Timestamp::from_micros_since_unix_epoch(
                    retry_at_ms.saturating_mul(1_000),
                ))
            } else {
                String::new()
            },
        });
    }
    ctx.db
        .ticketremote_member_limit_event()
        .insert(TicketremoteMemberLimitEvent {
            id: event_id,
            ticketId: ticket_id.clone(),
            email: email.clone(),
            ownerPublicId: account_public_id(&email),
            kind: kind.clone(),
            correlationId: correlation_id,
            counted: effective_limited,
            enforcementMode: if effective_limited {
                "enforced".into()
            } else {
                "bypassed".into()
            },
            admittedAt: now.into(),
            updatedAt: now.into(),
            expiresAt: add_ms(now, MEMBER_LIMIT_EVENT_TTL_MS),
        });
    refresh_member_limit_state(ctx, &ticket_id, &email, now);
    Ok(MemberLimitAdmission {
        allowed: true,
        reason: if effective_limited {
            format!("{kind}_admitted")
        } else {
            "limits_bypassed".into()
        },
        retry_at: String::new(),
    })
}

fn refresh_activation_eligibility(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    now: &str,
) -> TicketremoteActivationEligibility {
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    // Compatibility-only backend projection. Per-account authorization lives
    // in ticketremote_member_limit_state and is rechecked transactionally by
    // every consequential member reducer.
    let row = TicketremoteActivationEligibility {
        id: phone_row_id(&ticket_id, &backend_id),
        ticketId: ticket_id,
        backendId: backend_id,
        allowed: true,
        reason: "account_policy_authority".into(),
        retryAt: String::new(),
        cooldownUntil: String::new(),
        admissionsInWindow: 0,
        serverAt: now.into(),
        updatedAt: now.into(),
    };
    let table = ctx.db.ticketremote_activation_eligibility();
    if table.id().find(&row.id).is_some() {
        table.id().update(row.clone());
    } else {
        table.insert(row.clone());
    }
    row
}

fn activation_admission(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    email: &str,
    flow: &str,
    attempt_id: &str,
    interaction_revision: &str,
    control_id: &str,
    reset_request_id: &str,
    input_sequence: &str,
    persist_rejection: bool,
    now: &str,
) -> Result<ActivationAdmissionResult, String> {
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = canonical_activation_backend(ctx, &ticket_id, backend_id)?;
    let flow = activation_flow(flow);
    if flow.is_empty() {
        return Err("invalid_activation_flow".into());
    }
    let attempt_id = attempt_id.trim();
    if !valid_schedule_identifier(attempt_id) {
        return Err("invalid_activation_attempt_id".into());
    }
    let interaction_revision = bounded_text(interaction_revision, 160);
    let interaction_correlation = activation_interaction_correlation(control_id, reset_request_id);
    let fingerprint = activation_input_fingerprint(
        &flow,
        &ticket_id,
        &backend_id,
        &interaction_revision,
        control_id,
        reset_request_id,
        input_sequence,
    );
    let decision_id = activation_decision_id(&ticket_id, &backend_id, attempt_id);
    if let Some(existing_decision) = ctx
        .db
        .ticketremote_activation_decision()
        .id()
        .find(&decision_id)
    {
        if existing_decision.ticketId != ticket_id
            || existing_decision.backendId != backend_id
            || existing_decision.flow != flow
            || existing_decision.interactionRevision != interaction_revision
            || existing_decision.inputFingerprint != fingerprint
        {
            return Err("activation_attempt_id_reused".into());
        }
        return Ok(if existing_decision.accepted {
            ActivationAdmissionResult::Accepted(activation_decision_from_row(&existing_decision))
        } else {
            ActivationAdmissionResult::Rejected(activation_decision_from_row(&existing_decision))
        });
    }
    if let Some(existing_history) = activation_history_for_attempt(ctx, &ticket_id, attempt_id) {
        if existing_history.backendId != backend_id
            || existing_history.flow != flow
            || existing_history.inputFingerprint != fingerprint
        {
            return Err("activation_attempt_id_reused".into());
        }
        let existing_interaction_revision =
            if existing_history.interactionRevision.trim().is_empty() {
                interaction_revision.clone()
            } else {
                existing_history.interactionRevision.clone()
            };
        let admission = ActivationAdmission {
            accepted: existing_history.admission == "admitted",
            ticket_id: ticket_id.clone(),
            backend_id: backend_id.clone(),
            flow: flow.clone(),
            attempt_id: attempt_id.into(),
            interaction_revision: existing_interaction_revision,
            reason: if existing_history.admission == "admitted" {
                "activation_admitted".into()
            } else {
                existing_history.reason.clone()
            },
            retry_at: String::new(),
        };
        upsert_activation_decision(ctx, &admission, &fingerprint, now);
        return Ok(if admission.accepted {
            ActivationAdmissionResult::Accepted(admission)
        } else {
            ActivationAdmissionResult::Rejected(admission)
        });
    }

    let policy = admit_member_limit_event(ctx, &ticket_id, email, "registration", attempt_id, now)?;
    let accepted = policy.allowed;
    let admission = ActivationAdmission {
        accepted,
        ticket_id: ticket_id.clone(),
        backend_id: backend_id.clone(),
        flow: flow.clone(),
        attempt_id: attempt_id.into(),
        interaction_revision: interaction_revision.clone(),
        reason: if accepted {
            "activation_admitted".into()
        } else {
            policy.reason
        },
        retry_at: policy.retry_at,
    };
    if !accepted {
        if persist_rejection {
            record_activation_rejection(
                ctx,
                &ticket_id,
                &backend_id,
                &flow,
                &admission.reason,
                now,
            );
            upsert_activation_decision(ctx, &admission, &fingerprint, now);
            refresh_activation_eligibility(ctx, &ticket_id, &backend_id, now);
        }
        return Ok(ActivationAdmissionResult::Rejected(admission));
    }

    let history = TicketremoteActivationHistory {
        id: activation_history_id(&ticket_id, &backend_id, attempt_id),
        ticketId: ticket_id.clone(),
        backendId: backend_id.clone(),
        flow,
        admission: "admitted".into(),
        outcome: "pending".into(),
        reason: "activation_admitted".into(),
        occurredAt: now.into(),
        occurrenceDay: activation_day_bucket(now),
        admittedAt: now.into(),
        updatedAt: now.into(),
        completedAt: String::new(),
        attemptId: attempt_id.into(),
        interactionRevision: interaction_revision,
        interactionCorrelation: interaction_correlation,
        activationRevision: String::new(),
        inputFingerprint: fingerprint.clone(),
        refreshDueAt: String::new(),
        refreshCompletedAt: String::new(),
        refreshOutcome: String::new(),
        refreshRetryAt: String::new(),
        refreshAttempt: 0,
        occurrenceCount: 1,
        expiresAt: add_ms(now, TICKET_ACTIVATION_LEDGER_TTL_MS),
    };
    ctx.db.ticketremote_activation_history().insert(history);
    upsert_activation_decision(ctx, &admission, &fingerprint, now);
    refresh_activation_eligibility(ctx, &ticket_id, &backend_id, now);
    Ok(ActivationAdmissionResult::Accepted(admission))
}

fn activation_admission_for_action(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    email: &str,
    flow: &str,
    attempt_id: &str,
    interaction_revision: &str,
    control_id: &str,
    reset_request_id: &str,
    input_sequence: &str,
    v2: bool,
    now: &str,
) -> Result<ActivationAdmission, String> {
    match activation_admission(
        ctx,
        ticket_id,
        backend_id,
        email,
        flow,
        attempt_id,
        interaction_revision,
        control_id,
        reset_request_id,
        input_sequence,
        true,
        now,
    )? {
        ActivationAdmissionResult::Accepted(admission) => Ok(admission),
        ActivationAdmissionResult::Rejected(admission) => {
            if v2 {
                Ok(admission)
            } else {
                Err(admission.reason)
            }
        }
    }
}

fn activation_history_for_revision(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    activation_revision: &str,
) -> Option<TicketremoteActivationHistory> {
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    let activation_revision = activation_revision.trim();
    ctx.db.ticketremote_activation_history().iter().find(|row| {
        row.ticketId == ticket_id
            && row.backendId == backend_id
            && row.admission == "admitted"
            && row.activationRevision == activation_revision
    })
}

fn activation_history_success_is_newer(
    candidate: &TicketremoteActivationHistory,
    authority: &TicketremoteActivationHistory,
) -> bool {
    if candidate.ticketId != authority.ticketId
        || candidate.backendId != authority.backendId
        || candidate.admission != "admitted"
        || candidate.outcome != "succeeded"
        || candidate.activationRevision == authority.activationRevision
    {
        return false;
    }
    let candidate_completed = parse_time_micros(&candidate.completedAt);
    let authority_completed = parse_time_micros(&authority.completedAt);
    candidate_completed > authority_completed
        || (candidate_completed == authority_completed && candidate.id > authority.id)
}

fn activation_history_is_latest_success(
    ctx: &ReducerContext,
    authority: &TicketremoteActivationHistory,
) -> bool {
    !ctx.db
        .ticketremote_activation_history()
        .iter()
        .any(|candidate| activation_history_success_is_newer(&candidate, authority))
}

fn activation_history_matches_refresh_schedule_identity(
    history: &TicketremoteActivationHistory,
    schedule: &TicketremoteLatestTicketReselectSchedule,
) -> bool {
    let activation_revision = schedule.activationRevision.as_deref().unwrap_or("");
    let activation_attempt_id = schedule.activationAttemptId.as_deref().unwrap_or("");
    let original_due_at = schedule
        .originalDueAt
        .as_deref()
        .unwrap_or(schedule.scheduledAt.as_str());
    schedule.purpose.as_deref() == Some("activation_expiry_reset")
        && history.ticketId == schedule.ticketId
        && history.backendId == schedule.backendId
        && history.admission == "admitted"
        && history.outcome == "succeeded"
        && !activation_revision.is_empty()
        && history.activationRevision == activation_revision
        && (activation_attempt_id.is_empty() || history.attemptId == activation_attempt_id)
        && !history.refreshDueAt.is_empty()
        && history.refreshDueAt == schedule.scheduledAt
        && history.refreshDueAt == original_due_at
}

fn activation_history_authorizes_refresh_schedule(
    history: &TicketremoteActivationHistory,
    schedule: &TicketremoteLatestTicketReselectSchedule,
) -> bool {
    history.refreshOutcome == "pending"
        && activation_history_matches_refresh_schedule_identity(history, schedule)
}

fn activation_history_can_restore_state_replaced_schedule(
    history: &TicketremoteActivationHistory,
    schedule: &TicketremoteLatestTicketReselectSchedule,
) -> bool {
    history.refreshOutcome == "canceled"
        && schedule.status == "canceled"
        && schedule.resultReason == "activation_state_replaced"
        && activation_history_matches_refresh_schedule_identity(history, schedule)
}

fn activation_refresh_recovery_timer_micros(
    original_due_at_micros: i64,
    database_now_micros: i64,
) -> i64 {
    if original_due_at_micros > database_now_micros {
        original_due_at_micros
    } else {
        database_now_micros.saturating_add(1_000_000)
    }
}

fn activation_refresh_history_for_schedule(
    ctx: &ReducerContext,
    schedule: &TicketremoteLatestTicketReselectSchedule,
) -> Option<TicketremoteActivationHistory> {
    let history = activation_history_for_revision(
        ctx,
        &schedule.ticketId,
        &schedule.backendId,
        schedule.activationRevision.as_deref().unwrap_or(""),
    )?;
    (activation_history_authorizes_refresh_schedule(&history, schedule)
        && activation_history_is_latest_success(ctx, &history))
    .then_some(history)
}

fn activation_refresh_failure_has_history_authority(
    history: Option<&TicketremoteActivationHistory>,
    schedule: &TicketremoteLatestTicketReselectSchedule,
) -> bool {
    history.is_some_and(|history| activation_history_authorizes_refresh_schedule(history, schedule))
}

fn active_activation_refresh_schedule_for_revision(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    activation_revision: &str,
) -> Option<TicketremoteLatestTicketReselectSchedule> {
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    let activation_revision = activation_revision.trim();
    if activation_revision.is_empty() {
        return None;
    }
    ctx.db
        .ticketremote_latest_ticket_reselect_schedule()
        .iter()
        .find(|schedule| {
            schedule.ticketId == ticket_id
                && schedule.backendId == backend_id
                && matches!(schedule.status.as_str(), "queued" | "running")
                && schedule.purpose.as_deref() == Some("activation_expiry_reset")
                && schedule.activationRevision.as_deref() == Some(activation_revision)
                && activation_refresh_history_for_schedule(ctx, schedule).is_some()
        })
}

fn ticket_action_v3_is_activation_refresh_proof(
    action: &TicketremoteTicketActionV3,
    schedule: &TicketremoteLatestTicketReselectSchedule,
) -> bool {
    action.actionId == schedule.id
        && action.ticketId == schedule.ticketId
        && action.backendId == schedule.backendId
        && action.target == "open_latest_unactivated"
        && action.status == "succeeded"
        && action.currentView == "latest_unactivated"
}

struct ActivationRefreshTerminalReconciliation {
    schedule_status: &'static str,
    history_outcome: &'static str,
    reason: String,
    phase: String,
    completed_at: String,
}

fn activation_refresh_terminal_reconciliation(
    action: &TicketremoteTicketActionV3,
    schedule: &TicketremoteLatestTicketReselectSchedule,
    now: &str,
) -> Option<ActivationRefreshTerminalReconciliation> {
    if action.id != ticket_action_v3_row_id(&schedule.ticketId, &schedule.backendId, &schedule.id)
        || action.actionId != schedule.id
        || action.ticketId != schedule.ticketId
        || action.backendId != schedule.backendId
        || action.target != "open_latest_unactivated"
        || !ticket_action_v3_terminal(&action.status)
    {
        return None;
    }
    let completed_at = bounded_text(&non_empty(&action.completedAt, now), 80);
    if action.status == "succeeded"
        && ticket_action_v3_is_activation_refresh_proof(action, schedule)
    {
        return Some(ActivationRefreshTerminalReconciliation {
            schedule_status: "succeeded",
            history_outcome: "succeeded",
            reason: bounded_text(
                &non_empty(&action.reason, "activation_refresh_completed"),
                240,
            ),
            phase: "ready".into(),
            completed_at,
        });
    }
    Some(ActivationRefreshTerminalReconciliation {
        schedule_status: "failed",
        history_outcome: "failed",
        reason: if action.status == "succeeded" {
            "activation_refresh_visual_proof_invalid".into()
        } else {
            bounded_text(&non_empty(&action.reason, "activation_refresh_failed"), 240)
        },
        phase: "failed".into(),
        completed_at,
    })
}

fn terminal_activation_refresh_command_matches(
    command: &TicketremoteStreamCommand,
    schedule: &TicketremoteLatestTicketReselectSchedule,
    action: &TicketremoteTicketActionV3,
) -> bool {
    let command_id =
        ticket_action_v3_command_id(&schedule.ticketId, &schedule.backendId, &schedule.id);
    matches!(command.status.as_str(), "pending" | "queued")
        && command.id == command_id
        && (schedule.commandId.is_empty() || schedule.commandId == command.id)
        && command.ticketId == schedule.ticketId
        && command.backendId == schedule.backendId
        && command.commandType == "ticket_action_v3"
        && command.revision == format!("schedule:{}", schedule.id)
        && ticket_reset_command_payload_value(&command.payloadJson, "actionId") == schedule.id
        && ticket_reset_command_payload_value(&command.payloadJson, "target") == action.target
        && activation_refresh_terminal_reconciliation(action, schedule, &action.completedAt)
            .is_some()
}

fn retire_terminal_activation_refresh_command(
    ctx: &ReducerContext,
    schedule: &TicketremoteLatestTicketReselectSchedule,
    action: &TicketremoteTicketActionV3,
    now: &str,
) {
    let command_id =
        ticket_action_v3_command_id(&schedule.ticketId, &schedule.backendId, &schedule.id);
    let table = ctx.db.ticketremote_stream_command();
    let Some(command) = table.id().find(command_id) else {
        return;
    };
    if !terminal_activation_refresh_command_matches(&command, schedule, action) {
        return;
    }
    table.id().delete(&command.id);
    upsert_stream_command_signal(
        ctx,
        &command.ticketId,
        &command.backendId,
        &command.revision,
        now,
    );
}

fn reconcile_activation_refresh_terminal_action(
    ctx: &ReducerContext,
    history: &TicketremoteActivationHistory,
    schedule: &TicketremoteLatestTicketReselectSchedule,
    now: &str,
) -> bool {
    if !activation_history_matches_refresh_schedule_identity(history, schedule)
        || !activation_history_is_latest_success(ctx, history)
    {
        return false;
    }
    let action_id = ticket_action_v3_row_id(&schedule.ticketId, &schedule.backendId, &schedule.id);
    let Some(action) = ctx.db.ticketremote_ticket_action_v3().id().find(action_id) else {
        return false;
    };
    let Some(reconciliation) = activation_refresh_terminal_reconciliation(&action, schedule, now)
    else {
        return false;
    };

    // A prior module instance may already have queued the deterministic command before the
    // terminal projection committed. Once that exact terminal row exists the command is stale;
    // retire it before publishing the reconciled schedule so it cannot block newer work.
    retire_terminal_activation_refresh_command(ctx, schedule, &action, now);
    delete_latest_ticket_reselect_timers(ctx, &schedule.id);
    ctx.db
        .ticketremote_activation_history()
        .id()
        .update(TicketremoteActivationHistory {
            refreshOutcome: reconciliation.history_outcome.into(),
            refreshCompletedAt: reconciliation.completed_at.clone(),
            refreshRetryAt: String::new(),
            updatedAt: now.into(),
            ..history.clone()
        });
    ctx.db
        .ticketremote_latest_ticket_reselect_schedule()
        .id()
        .update(TicketremoteLatestTicketReselectSchedule {
            status: reconciliation.schedule_status.into(),
            commandId: ticket_action_v3_command_id(
                &schedule.ticketId,
                &schedule.backendId,
                &schedule.id,
            ),
            resultReason: reconciliation.reason,
            resultPhase: reconciliation.phase,
            proofSource: "ticket_action_v3_projection".into(),
            updatedAt: now.into(),
            completedAt: reconciliation.completed_at,
            expiresAt: add_ms(now, HISTORY_TTL_MS),
            nextRetryAt: None,
            ..schedule.clone()
        });
    true
}

fn schedule_activation_refresh(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    activation_attempt_id: &str,
    activation_revision: &str,
    due_at: &str,
    now: &str,
) -> Result<(), String> {
    let due_at_micros = parse_time_micros(due_at);
    if due_at_micros <= ctx.timestamp.to_micros_since_unix_epoch() {
        return Err("activation_refresh_deadline_invalid".into());
    }
    let schedule_id = format!(
        "{}:{}:activation_expiry:{}",
        clean_ticket_id(ticket_id),
        clean_backend_id(backend_id),
        stable_stamp(&safe_token(activation_revision, "activation"))
    );
    schedule_latest_ticket_reselect(
        ctx,
        ticket_id,
        backend_id,
        &schedule_id,
        due_at_micros,
        "",
        "",
        "pixel_activation",
        "activation_expiry_reset",
        activation_revision,
        "open_latest_unactivated",
        now,
    )?;
    let table = ctx.db.ticketremote_latest_ticket_reselect_schedule();
    if let Some(existing) = table.id().find(schedule_id) {
        table.id().update(TicketremoteLatestTicketReselectSchedule {
            activationAttemptId: Some(activation_attempt_id.trim().into()),
            originalDueAt: Some(due_at.into()),
            nextRetryAt: None,
            retryAttempt: 0,
            updatedAt: now.into(),
            ..existing
        });
    }
    Ok(())
}

fn switch_anchor_policy_revision(
    ticket_id: &str,
    backend_id: &str,
    attempt_id: &str,
    activation_revision: &str,
    activation_at: &str,
) -> String {
    format!(
        "switch-{}",
        public_hash(
            &format!(
                "{}|{}|{}|{}|{}",
                clean_ticket_id(ticket_id),
                clean_backend_id(backend_id),
                attempt_id.trim(),
                activation_revision.trim(),
                canonical_time(activation_at)
            ),
            24,
        )
    )
}

fn ensure_ticket_switch_anchor(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    attempt_id: &str,
    activation_revision: &str,
    activation_at: &str,
    now: &str,
) {
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    let id = phone_row_id(&ticket_id, &backend_id);
    let activation_at = canonical_time(activation_at);
    let expires_at = add_ms(&activation_at, TICKET_ACTION_SWITCH_WINDOW_MS);
    if parse_time_micros(&expires_at) <= parse_time_micros(now) {
        return;
    }
    let table = ctx.db.ticketremote_ticket_switch_anchor();
    if let Some(existing) = table.id().find(&id) {
        if existing.activationRevision == activation_revision.trim()
            && existing.activationAttemptId == attempt_id.trim()
        {
            replace_policy_boundary_timer(
                ctx,
                &ticket_id,
                "switch",
                &backend_id,
                parse_time_ms(&existing.expiresAt),
                now,
            );
            refresh_ticket_switch_action_projections(ctx, &ticket_id, &backend_id, now);
            return;
        }
        table.id().update(TicketremoteTicketSwitchAnchor {
            activationAttemptId: attempt_id.trim().into(),
            activationRevision: bounded_text(activation_revision, 160),
            activationAt: activation_at.clone(),
            expiresAt: expires_at.clone(),
            latestUnactivatedProofActionId: String::new(),
            latestUnactivatedProofAt: String::new(),
            currentView: "recent_activated".into(),
            policyRevision: switch_anchor_policy_revision(
                &ticket_id,
                &backend_id,
                attempt_id,
                activation_revision,
                &activation_at,
            ),
            updatedAt: now.into(),
            ..existing
        });
    } else {
        table.insert(TicketremoteTicketSwitchAnchor {
            id,
            ticketId: ticket_id.clone(),
            backendId: backend_id.clone(),
            activationAttemptId: attempt_id.trim().into(),
            activationRevision: bounded_text(activation_revision, 160),
            activationAt: activation_at.clone(),
            expiresAt: expires_at.clone(),
            latestUnactivatedProofActionId: String::new(),
            latestUnactivatedProofAt: String::new(),
            currentView: "recent_activated".into(),
            policyRevision: switch_anchor_policy_revision(
                &ticket_id,
                &backend_id,
                attempt_id,
                activation_revision,
                &activation_at,
            ),
            updatedAt: now.into(),
        });
    }
    replace_policy_boundary_timer(
        ctx,
        &ticket_id,
        "switch",
        &backend_id,
        parse_time_ms(&expires_at),
        now,
    );
    refresh_ticket_switch_action_projections(ctx, &ticket_id, &backend_id, now);
}

fn note_ticket_switch_visual_result(
    ctx: &ReducerContext,
    action: &TicketremoteTicketActionV3,
    now: &str,
) {
    if action.status != "succeeded" {
        return;
    }
    let Some(anchor) = live_ticket_switch_anchor(ctx, &action.ticketId, &action.backendId, now)
    else {
        return;
    };
    let mut updated = anchor.clone();
    if action.currentView == "latest_unactivated"
        && matches!(
            action.target.as_str(),
            "open_latest_unactivated"
                | "return_to_latest_unactivated"
                | "redetect_latest"
                | "prove_current"
        )
        && parse_time_micros(now) > parse_time_micros(&anchor.activationAt)
    {
        updated.latestUnactivatedProofActionId = action.actionId.clone();
        updated.latestUnactivatedProofAt = now.into();
        updated.currentView = "latest_unactivated".into();
    } else if action.currentView == "recent_activated"
        && action.target == "show_recent_activated"
        && ticket_switch_anchor_has_later_unactivated_proof(&anchor)
    {
        updated.currentView = "recent_activated".into();
    }
    if updated.currentView != anchor.currentView
        || updated.latestUnactivatedProofActionId != anchor.latestUnactivatedProofActionId
    {
        updated.updatedAt = now.into();
        ctx.db
            .ticketremote_ticket_switch_anchor()
            .id()
            .update(updated);
        // The anchor and every public switch projection must change in the same
        // transaction. Otherwise an older row for the opposite view can remain
        // switchAvailable after a successful round trip and compete with the
        // current view in browser subscriptions.
        refresh_ticket_switch_action_projections(ctx, &action.ticketId, &action.backendId, now);
    }
}

fn ticket_switch_projection_for_view(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    current_view: &str,
    now: &str,
) -> Option<TicketremoteTicketSwitchAnchor> {
    live_ticket_switch_anchor(ctx, ticket_id, backend_id, now).filter(|anchor| {
        ticket_switch_anchor_has_later_unactivated_proof(anchor)
            && anchor.currentView == current_view
            && matches!(current_view, "latest_unactivated" | "recent_activated")
    })
}

fn refresh_ticket_switch_action_projections(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    now: &str,
) {
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    let anchor = live_ticket_switch_anchor(ctx, &ticket_id, &backend_id, now);
    let rows: Vec<_> = ctx
        .db
        .ticketremote_ticket_action_v3()
        .iter()
        .filter(|row| row.ticketId == ticket_id && row.backendId == backend_id)
        .collect();
    for row in rows {
        let (available, expires_at) = ticket_switch_projection_values(&row, anchor.as_ref());
        if row.switchAvailable != available || row.switchExpiresAt != expires_at {
            ctx.db
                .ticketremote_ticket_action_v3()
                .id()
                .update(TicketremoteTicketActionV3 {
                    switchAvailable: available,
                    switchExpiresAt: expires_at,
                    updatedAt: now.into(),
                    ..row
                });
        }
    }
}

fn ticket_switch_projection_values(
    row: &TicketremoteTicketActionV3,
    anchor: Option<&TicketremoteTicketSwitchAnchor>,
) -> (bool, String) {
    let authorized_anchor = anchor.filter(|value| {
        row.status == "succeeded"
            && ticket_switch_anchor_has_later_unactivated_proof(value)
            && value.currentView == row.currentView
            && matches!(
                row.currentView.as_str(),
                "latest_unactivated" | "recent_activated"
            )
    });
    match authorized_anchor {
        Some(value) => (true, value.expiresAt.clone()),
        None => (false, String::new()),
    }
}

fn expire_ticket_switch_anchor(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    boundary_at: &str,
    now: &str,
) {
    let id = phone_row_id(ticket_id, backend_id);
    let Some(anchor) = ctx.db.ticketremote_ticket_switch_anchor().id().find(&id) else {
        return;
    };
    if anchor.expiresAt != canonical_time(boundary_at)
        || parse_time_micros(&anchor.expiresAt) > parse_time_micros(now)
    {
        return;
    }
    ctx.db.ticketremote_ticket_switch_anchor().id().delete(&id);
    refresh_ticket_switch_action_projections(ctx, ticket_id, backend_id, now);
}

fn commit_ticket_activation_at_impl(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    attempt_id: &str,
    interaction_revision: &str,
    activation_revision: &str,
    completed_at: &str,
    now_arg: &str,
) -> Result<(), String> {
    let now = canonical_time(now_arg);
    let completed_at = canonical_time(completed_at);
    let ticket = ensure_ticket(ctx, ticket_id, "", &now);
    let backend_id = canonical_activation_backend(ctx, &ticket.id, backend_id)?;
    let attempt_id = attempt_id.trim();
    let interaction_revision = bounded_text(interaction_revision, 160);
    let activation_revision = bounded_text(activation_revision, 160);
    if !valid_schedule_identifier(attempt_id) {
        return Err("invalid_activation_attempt_id".into());
    }
    if activation_revision.is_empty() {
        return Err("activation_revision_required".into());
    }
    let history = activation_history_for_attempt(ctx, &ticket.id, attempt_id)
        .ok_or_else(|| "activation_admission_not_found".to_string())?;
    if history.backendId != backend_id
        || history.admission != "admitted"
        || history.interactionRevision != interaction_revision
    {
        return Err("activation_admission_mismatch".into());
    }
    if let Some(existing_revision) =
        activation_history_for_revision(ctx, &ticket.id, &backend_id, &activation_revision)
    {
        if existing_revision.attemptId != attempt_id {
            return Err("activation_revision_reused".into());
        }
    }
    let interaction_id = ticket_interaction_id(&ticket.id, &backend_id);
    let current = current_ticket_interaction(ctx, &ticket.id, &backend_id, &now);
    if current.interactionRevision != interaction_revision {
        return Err("activation_interaction_revision_stale".into());
    }
    if !current.activationRevision.trim().is_empty()
        && current.activationRevision != activation_revision
    {
        return Err("activation_revision_mismatch".into());
    }
    if history.outcome == "succeeded" {
        if history.activationRevision != activation_revision {
            return Err("activation_revision_mismatch".into());
        }
        ensure_ticket_switch_anchor(
            ctx,
            &ticket.id,
            &backend_id,
            &history.attemptId,
            &history.activationRevision,
            &history.completedAt,
            &now,
        );
        refresh_activation_eligibility(ctx, &ticket.id, &backend_id, &now);
        return Ok(());
    }
    if !activation_correlation_matches_current(
        &history.flow,
        &history.interactionCorrelation,
        &current.controlId,
        &current.resetRequestId,
    ) {
        return Err("activation_attempt_stale".into());
    }
    if history.outcome != "pending" {
        return Err("activation_admission_not_pending".into());
    }
    let refresh_due_at = iso(Timestamp::from_micros_since_unix_epoch(
        activation_refresh_due_at_ms(parse_time_ms(&completed_at)).saturating_mul(1_000),
    ));
    schedule_activation_refresh(
        ctx,
        &ticket.id,
        &backend_id,
        attempt_id,
        &activation_revision,
        &refresh_due_at,
        &now,
    )?;
    let history_table = ctx.db.ticketremote_activation_history();
    history_table.id().update(TicketremoteActivationHistory {
        outcome: "succeeded".into(),
        reason: "activation_succeeded".into(),
        activationRevision: activation_revision.clone(),
        completedAt: completed_at.clone(),
        refreshDueAt: refresh_due_at.clone(),
        refreshCompletedAt: String::new(),
        refreshOutcome: "pending".into(),
        refreshRetryAt: String::new(),
        refreshAttempt: 0,
        updatedAt: now.clone(),
        ..history
    });
    ensure_ticket_switch_anchor(
        ctx,
        &ticket.id,
        &backend_id,
        attempt_id,
        &activation_revision,
        &completed_at,
        &now,
    );
    let mut next = current;
    next.status = "activated".into();
    next.activationRevision = activation_revision;
    next.activationAt = completed_at;
    next.scheduledResetAt = refresh_due_at;
    next.ownerPublicId.clear();
    next.controlId.clear();
    next.leasePhase = "none".into();
    next.leaseExpiresAt.clear();
    next.reason = "activation_proven".into();
    next.updatedAt = now.clone();
    next.expiresAt = add_ms(&now, TICKET_INTERACTION_TTL_MS);
    ctx.db
        .ticketremote_ticket_interaction()
        .id()
        .update(TicketremoteTicketInteraction {
            id: interaction_id,
            ..next
        });
    refresh_activation_eligibility(ctx, &ticket.id, &backend_id, &now);
    Ok(())
}

fn commit_ticket_activation_impl(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    attempt_id: &str,
    interaction_revision: &str,
    activation_revision: &str,
) -> Result<(), String> {
    let now = now(ctx);
    commit_ticket_activation_at_impl(
        ctx,
        ticket_id,
        backend_id,
        attempt_id,
        interaction_revision,
        activation_revision,
        &now,
        &now,
    )
}

fn finalize_ticket_activation_failure_checked_impl(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    attempt_id: &str,
    outcome: &str,
    reason: &str,
    interaction_revision: &str,
    completed_at: &str,
    now: &str,
) -> Result<(), String> {
    let attempt_id = attempt_id.trim();
    if !valid_schedule_identifier(attempt_id) {
        return Err("invalid_activation_attempt_id".into());
    }
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = canonical_activation_backend(ctx, &ticket_id, backend_id)?;
    let history = activation_history_for_attempt(ctx, &ticket_id, attempt_id)
        .ok_or_else(|| "activation_admission_not_found".to_string())?;
    let interaction_revision = bounded_text(interaction_revision, 160);
    let completed_at = canonical_time(completed_at);
    if history.backendId != backend_id
        || history.admission != "admitted"
        || history.interactionRevision != interaction_revision
    {
        return Err("activation_admission_mismatch".into());
    }
    let outcome = if outcome.trim() == "expired" {
        "expired"
    } else {
        "failed"
    };
    let reason = safe_token(
        &bounded_text(reason, 80),
        if outcome == "expired" {
            "activation_expired"
        } else {
            "activation_failed"
        },
    );
    if history.outcome != "pending" {
        return if history.outcome == outcome
            && history.reason == reason
            && canonical_time(&history.completedAt) == completed_at
        {
            Ok(())
        } else {
            Err("activation_terminal_conflict".into())
        };
    }
    ctx.db
        .ticketremote_activation_history()
        .id()
        .update(TicketremoteActivationHistory {
            outcome: outcome.into(),
            reason,
            completedAt: completed_at,
            refreshOutcome: "not_scheduled".into(),
            refreshCompletedAt: String::new(),
            refreshRetryAt: String::new(),
            updatedAt: now.into(),
            ..history
        });
    refresh_activation_eligibility(ctx, &ticket_id, &backend_id, now);
    Ok(())
}

fn finalize_ticket_activation_failure_impl(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    attempt_id: &str,
    outcome: &str,
    reason: &str,
    now: &str,
) {
    let interaction_revision = activation_history_for_attempt(ctx, ticket_id, attempt_id)
        .map(|history| history.interactionRevision)
        .unwrap_or_default();
    let _ = finalize_ticket_activation_failure_checked_impl(
        ctx,
        ticket_id,
        backend_id,
        attempt_id,
        outcome,
        reason,
        &interaction_revision,
        now,
        now,
    );
}

fn finalize_ticket_activation_refresh_at_impl(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    activation_revision: &str,
    interaction_revision: &str,
    reason: &str,
    phase: &str,
    completed_at: &str,
    now_arg: &str,
) -> Result<(), String> {
    let now = canonical_time(now_arg);
    let completed_at = canonical_time(completed_at);
    let ticket = ensure_ticket(ctx, ticket_id, "", &now);
    let backend_id = clean_backend_id(backend_id);
    let activation_revision = bounded_text(activation_revision, 160);
    if activation_revision.is_empty() {
        return Err("activation_revision_required".into());
    }
    let Some(history) =
        activation_history_for_revision(ctx, &ticket.id, &backend_id, &activation_revision)
    else {
        return Err("activation_history_not_found".into());
    };
    if history.refreshOutcome == "succeeded" {
        return Ok(());
    }
    let schedule = active_activation_refresh_schedule_for_revision(
        ctx,
        &ticket.id,
        &backend_id,
        &activation_revision,
    )
    .ok_or_else(|| "activation_refresh_schedule_not_active".to_string())?;
    let scheduled_revision = format!("schedule:{}", schedule.id);
    if interaction_revision.trim() != scheduled_revision {
        return Err("activation_refresh_interaction_revision_stale".into());
    }
    let action_id = ticket_action_v3_row_id(&ticket.id, &backend_id, &schedule.id);
    let action = ctx
        .db
        .ticketremote_ticket_action_v3()
        .id()
        .find(action_id)
        .ok_or_else(|| "activation_refresh_visual_proof_missing".to_string())?;
    if !ticket_action_v3_is_activation_refresh_proof(&action, &schedule) {
        return Err("activation_refresh_visual_proof_invalid".into());
    }
    let current = current_ticket_interaction(ctx, &ticket.id, &backend_id, &now);
    let history_table = ctx.db.ticketremote_activation_history();
    history_table.id().update(TicketremoteActivationHistory {
        refreshCompletedAt: completed_at.clone(),
        refreshOutcome: "succeeded".into(),
        refreshRetryAt: String::new(),
        updatedAt: now.clone(),
        ..history
    });
    let schedule_table = ctx.db.ticketremote_latest_ticket_reselect_schedule();
    delete_latest_ticket_reselect_timers(ctx, &schedule.id);
    schedule_table
        .id()
        .update(TicketremoteLatestTicketReselectSchedule {
            status: "succeeded".into(),
            resultReason: bounded_text(&non_empty(reason, "activation_refresh_completed"), 240),
            resultPhase: bounded_text(&safe_token(phase, "ready"), 80),
            proofSource: "phone_worker".into(),
            updatedAt: now.clone(),
            completedAt: completed_at,
            expiresAt: add_ms(&now, HISTORY_TTL_MS),
            ..schedule
        });
    let mut next = current;
    next.status = "unactivated_ready".into();
    next.interactionRevision = bounded_text(
        &non_empty(interaction_revision, &next.interactionRevision),
        160,
    );
    next.activationAt.clear();
    next.activationRevision.clear();
    next.scheduledResetAt.clear();
    next.ownerPublicId.clear();
    next.controlId.clear();
    next.leasePhase = "none".into();
    next.leaseExpiresAt.clear();
    next.latestInputSequence = "0".into();
    next.latestInputPhase.clear();
    next.latestProgress = 0;
    next.lastAppliedSequence = "0".into();
    next.lastAppliedProgress = 0;
    next.reason = bounded_text(&non_empty(reason, "activation_refresh_completed"), 200);
    next.updatedAt = now.clone();
    next.expiresAt = add_ms(&now, TICKET_INTERACTION_TTL_MS);
    upsert_ticket_interaction(ctx, next);
    refresh_activation_eligibility(ctx, &ticket.id, &backend_id, &now);
    Ok(())
}

fn finalize_ticket_activation_refresh_impl(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    activation_revision: &str,
    interaction_revision: &str,
    reason: &str,
) -> Result<(), String> {
    let now = now(ctx);
    finalize_ticket_activation_refresh_at_impl(
        ctx,
        ticket_id,
        backend_id,
        activation_revision,
        interaction_revision,
        reason,
        "ready",
        &now,
        &now,
    )
}

fn request_ticket_action_v3_impl(
    ctx: &ReducerContext,
    version: u32,
    ticket_id: &str,
    backend_id: &str,
    action_id: &str,
    target: &str,
    source: &str,
    reason: &str,
    attempt_id: &str,
    expected_interaction_revision: &str,
    schedule_id: &str,
    email: &str,
    now: &str,
) -> Result<(), String> {
    if version != 3 {
        return Err("unsupported_ticket_action_version".into());
    }
    let ticket = ensure_ticket(ctx, ticket_id, "", now);
    let backend_id = canonical_activation_backend(ctx, &ticket.id, backend_id)?;
    let action_id = action_id.trim();
    if !valid_schedule_identifier(action_id) {
        return Err("invalid_ticket_action_id".into());
    }
    let target = ticket_action_v3_target(target);
    if target.is_empty() {
        return Err("invalid_ticket_action_target".into());
    }
    let source = allowlisted(
        source,
        &[
            "browser_button",
            "browser_slider",
            "browser_smart_switch",
            "browser_auto_proof",
            "ticket_remote_admin",
            "ticket_remote_schedule",
        ],
        "",
    );
    if source.is_empty() {
        return Err("invalid_ticket_action_source".into());
    }
    let public_reason = ticket_action_v3_public_reason(reason, "ticket_action_requested");
    if !schedule_id.trim().is_empty() && !valid_schedule_identifier(schedule_id.trim()) {
        return Err("invalid_ticket_action_schedule_id".into());
    }
    let activation_target = ticket_action_v3_is_activation(&target);
    let attempt_id = attempt_id.trim();
    if activation_target && attempt_id != action_id {
        return Err("ticket_action_attempt_id_mismatch".into());
    }
    let expected_revision = bounded_text(expected_interaction_revision, 160);
    if target == "register_current" && expected_revision.is_empty() {
        return Err("ticket_action_interaction_revision_required".into());
    }
    let row_id = ticket_action_v3_row_id(&ticket.id, &backend_id, action_id);
    if let Some(existing) = ctx.db.ticketremote_ticket_action_v3().id().find(&row_id) {
        return ticket_action_v3_duplicate_result(&existing.target, &target);
    }
    if let Some(conflict_reason) =
        ticket_phone_mutation_lane_conflict(ctx, &ticket.id, &backend_id, now)
    {
        if target != "prove_current" {
            return queue_ticket_action_v3_intent(
                ctx,
                &ticket.id,
                &backend_id,
                action_id,
                &target,
                &source,
                &public_reason,
                attempt_id,
                &expected_revision,
                schedule_id.trim(),
                email,
                now,
            );
        }
        return Err(conflict_reason.into());
    }

    let mut interaction = current_ticket_interaction(ctx, &ticket.id, &backend_id, now);
    let mut command_revision = action_id.to_string();
    let action_row = ticket_action_v3_upsert_pending(
        ctx,
        &ticket.id,
        &backend_id,
        action_id,
        &target,
        &public_reason,
        now,
    );

    let live_switch_anchor = live_ticket_switch_anchor(ctx, &ticket.id, &backend_id, now);
    if matches!(
        target.as_str(),
        "show_recent_activated" | "return_to_latest_unactivated"
    ) && ticket_action_v3_switch_authority(ctx, &ticket.id, &backend_id, &target, now).is_none()
    {
        ticket_action_v3_finish_without_command(
            ctx,
            action_row,
            "ticket_view_switch_unavailable",
            now,
        );
        return ticket_action_v3_committed_rejection();
    }

    if target == "register_current" {
        if !ticket_action_v3_has_registration_authority(
            ctx,
            &ticket.id,
            &backend_id,
            &expected_revision,
            now,
        ) {
            ticket_action_v3_finish_without_command(ctx, action_row, "slider_proof_stale", now);
            return ticket_action_v3_committed_rejection();
        }
        let admission = activation_admission_for_action(
            ctx,
            &ticket.id,
            &backend_id,
            email,
            "menu_activate",
            attempt_id,
            &expected_revision,
            action_id,
            "",
            "1",
            true,
            now,
        )?;
        if !admission.accepted {
            ticket_action_v3_finish_without_command(ctx, action_row, &admission.reason, now);
            return ticket_action_v3_committed_rejection();
        }
        // The visual action row is the registration authority. Do not carry an
        // older activation, slider lease, geometry, or reset correlation into
        // this exact attempt through the retained v1/v2 compatibility row.
        clear_current_ticket_activation_state(&mut interaction);
        command_revision = expected_revision.clone();
        interaction.status = "control_active".into();
        // The successful v3 visual action is the registration proof authority. Claim that
        // exact revision before the physical registration command is queued so commit and
        // checkpoint reconciliation do not depend on a legacy navigation publication.
        interaction.interactionRevision = expected_revision.clone();
        interaction.ownerPublicId = account_public_id(email);
        interaction.controlId = action_id.into();
        interaction.leasePhase = "active".into();
        interaction.leaseExpiresAt = add_ms(now, TICKET_SLIDER_LEASE_MS);
        interaction.latestInputSequence = "1".into();
        interaction.latestInputPhase = "up".into();
        interaction.latestProgress = 10_000;
        interaction.reason = "ticket_action_v3_register_queued".into();
        interaction.updatedAt = now.into();
        interaction.expiresAt = add_ms(now, TICKET_INTERACTION_TTL_MS);
        upsert_ticket_interaction(ctx, interaction);
    } else if target == "open_latest_and_register" {
        let revision = action_id.to_string();
        let admission = activation_admission_for_action(
            ctx,
            &ticket.id,
            &backend_id,
            email,
            "reset_and_activate",
            attempt_id,
            &revision,
            "",
            action_id,
            "0",
            true,
            now,
        )?;
        if !admission.accepted {
            ticket_action_v3_finish_without_command(ctx, action_row, &admission.reason, now);
            return ticket_action_v3_committed_rejection();
        }
        // This composite action starts from its own visual observation and
        // attempt id. Retained compatibility metadata from a previous ticket
        // must not become authority for its registration commit.
        clear_current_ticket_activation_state(&mut interaction);
        command_revision = revision.clone();
        interaction.status = "reset_queued".into();
        interaction.interactionRevision = revision;
        interaction.resetRequestId = action_id.into();
        interaction.reason = "ticket_action_v3_open_and_register_queued".into();
        interaction.updatedAt = now.into();
        interaction.expiresAt = add_ms(now, TICKET_INTERACTION_TTL_MS);
        upsert_ticket_interaction(ctx, interaction);
    }

    let payload = serde_json::json!({
        "version": 3,
        "actionId": action_id,
        "target": target,
        "source": source,
        "reason": public_reason,
        "attemptId": if activation_target { attempt_id } else { "" },
        "expectedInteractionRevision": if target == "register_current" { expected_revision.as_str() } else { "" },
        "scheduleId": schedule_id.trim(),
        "switchExpiresAt": live_switch_anchor.as_ref().map(|anchor| anchor.expiresAt.as_str()).unwrap_or(""),
        "policyRevision": live_switch_anchor.as_ref().map(|anchor| anchor.policyRevision.as_str()).unwrap_or(""),
    })
    .to_string();
    insert_stream_command(
        ctx,
        &ticket.id,
        &backend_id,
        &ticket_action_v3_command_id(&ticket.id, &backend_id, action_id),
        "ticket_action_v3",
        &command_revision,
        &public_reason,
        &payload,
        TICKET_ACTIVATION_COMMAND_TTL_MS,
        now,
    );
    Ok(())
}

fn request_ticket_reset_impl(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    reset_request_id: &str,
    reason: &str,
    activation_attempt_id: &str,
    email: &str,
    v2: bool,
    now: &str,
) -> Result<(), String> {
    let ticket = ensure_ticket(ctx, ticket_id, "", now);
    if ticket_has_control_code_request_in_progress(ctx, &ticket.id, now) {
        return Err("control_code_in_progress".into());
    }
    let backend_id = clean_backend_id(backend_id);
    let request_id = reset_request_id.trim();
    if !valid_schedule_identifier(request_id) {
        return Err("invalid_reset_request_id".into());
    }
    let activate_after_reset = reason.trim() == "browser_reset_and_activate";
    let activation_attempt_id = if activate_after_reset {
        non_empty(activation_attempt_id, request_id)
    } else {
        String::new()
    };
    let revision = interaction_revision(&ticket.id, &backend_id, request_id);
    let current = repair_missing_reset_command_interaction(
        ctx,
        current_ticket_interaction(ctx, &ticket.id, &backend_id, now),
        now,
    );
    if activate_after_reset
        && (ctx
            .db
            .ticketremote_activation_decision()
            .id()
            .find(activation_decision_id(
                &ticket.id,
                &backend_id,
                &activation_attempt_id,
            ))
            .is_some()
            || activation_history_for_attempt(ctx, &ticket.id, &activation_attempt_id).is_some())
    {
        let admission = activation_admission_for_action(
            ctx,
            &ticket.id,
            &backend_id,
            email,
            "reset_and_activate",
            &activation_attempt_id,
            &revision,
            "",
            request_id,
            "0",
            v2,
            now,
        )?;
        return if admission.accepted || v2 {
            Ok(())
        } else {
            Err(admission.reason)
        };
    }
    if ticket_has_ticket_registration_reset_in_progress(ctx, &ticket.id, &backend_id, now) {
        if current.resetRequestId != request_id {
            return Err("ticket_reset_in_progress".into());
        }
    }
    if ticket_interaction_blocks_ticket_reset(&current, now) {
        return Err("slider_in_progress".into());
    }
    let stale_lease = ticket_interaction_has_stale_lease(&current, now);
    if matches!(current.status.as_str(), "reset_queued" | "preparing") {
        if current.resetRequestId == request_id {
            return Ok(());
        }
        return Err("reset_in_progress".into());
    }
    let mut current = current;
    if stale_lease {
        purge_pending_ticket_slider_commands(ctx, &ticket.id, &backend_id, &revision, now);
        current.ownerPublicId.clear();
        current.controlId.clear();
        current.leasePhase = "none".into();
        current.leaseExpiresAt.clear();
        current.latestInputSequence = "0".into();
        current.latestInputPhase.clear();
        current.latestProgress = 0;
        current.lastAppliedSequence = "0".into();
        current.lastAppliedProgress = 0;
    }
    if activate_after_reset {
        let admission = activation_admission_for_action(
            ctx,
            &ticket.id,
            &backend_id,
            email,
            "reset_and_activate",
            &activation_attempt_id,
            &revision,
            "",
            request_id,
            "0",
            v2,
            now,
        )?;
        if !admission.accepted {
            return Ok(());
        }
    }
    if !current.activationRevision.trim().is_empty() {
        cancel_pending_activation_expiry_schedules(
            ctx,
            &ticket.id,
            &backend_id,
            &current.activationRevision,
            None,
            now,
        );
        current.activationRevision.clear();
        current.activationAt.clear();
        current.scheduledResetAt.clear();
    }
    let payload = serde_json::json!({
        "type": "reset_ticket_registration",
        "flow": if activate_after_reset { "reset_and_activate" } else { "manual_ticket_reset" },
        "resetRequestId": request_id,
        "correlationId": request_id,
        "activationAttemptId": activation_attempt_id,
        "activateAfterReset": activate_after_reset,
        "source": "browser",
        "reason": bounded_text(&non_empty(reason, "ticket_reset_requested"), 120)
    })
    .to_string();
    insert_stream_command(
        ctx,
        &ticket.id,
        &backend_id,
        &format!("{}:{}:reset_ticket_registration", ticket.id, request_id),
        "reset_ticket_registration",
        &revision,
        "ticket_reset_requested",
        &payload,
        LATEST_TICKET_RESELECT_COMMAND_TTL_MS,
        now,
    );
    upsert_ticket_interaction(
        ctx,
        TicketremoteTicketInteraction {
            status: "reset_queued".into(),
            interactionRevision: revision,
            resetRequestId: request_id.into(),
            reason: bounded_text(&non_empty(reason, "ticket_reset_requested"), 160),
            updatedAt: now.into(),
            expiresAt: add_ms(now, TICKET_INTERACTION_TTL_MS),
            ..current
        },
    );
    Ok(())
}

fn activate_ticket_button_impl(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    interaction_revision: &str,
    control_id: &str,
    input_sequence: &str,
    activation_attempt_id: &str,
    email: &str,
    v2: bool,
    now: &str,
) -> Result<(), String> {
    let ticket = ensure_ticket(ctx, ticket_id, "", now);
    if ticket_has_control_code_request_in_progress(ctx, &ticket.id, now) {
        return Err("control_code_in_progress".into());
    }
    let backend_id = clean_backend_id(backend_id);
    let mut current = current_ticket_interaction(ctx, &ticket.id, &backend_id, now);
    let control_id = control_id.trim();
    let input_sequence = bounded_frame_ordinal(input_sequence);
    if !valid_schedule_identifier(control_id) || input_sequence == "0" {
        return Err("invalid_slider_control".into());
    }
    let activation_attempt_id = non_empty(activation_attempt_id, control_id);
    let requested_revision = bounded_text(interaction_revision, 160);
    let existing_attempt = ctx
        .db
        .ticketremote_activation_decision()
        .id()
        .find(activation_decision_id(
            &ticket.id,
            &backend_id,
            &activation_attempt_id,
        ))
        .is_some()
        || activation_history_for_attempt(ctx, &ticket.id, &activation_attempt_id).is_some();
    if existing_attempt {
        let admission = activation_admission_for_action(
            ctx,
            &ticket.id,
            &backend_id,
            email,
            "menu_activate",
            &activation_attempt_id,
            &requested_revision,
            control_id,
            "",
            &input_sequence,
            v2,
            now,
        )?;
        return if admission.accepted || v2 {
            Ok(())
        } else {
            Err(admission.reason)
        };
    }
    if !button_activation_state_is_exact(&current, interaction_revision) {
        return Err("slider_proof_stale".into());
    }
    if current.sliderRight <= current.sliderLeft || current.sliderBottom <= current.sliderTop {
        return Err("slider_bounds_missing".into());
    }
    let lease_available = current.leasePhase == "none"
        || current.leasePhase.is_empty()
        || parse_time_ms(&current.leaseExpiresAt) <= parse_time_ms(now);
    if !lease_available {
        return Err("slider_busy".into());
    }
    let admission = activation_admission_for_action(
        ctx,
        &ticket.id,
        &backend_id,
        email,
        "menu_activate",
        &activation_attempt_id,
        &requested_revision,
        control_id,
        "",
        &input_sequence,
        v2,
        now,
    )?;
    if !admission.accepted {
        return Ok(());
    }
    purge_pending_ticket_slider_commands(
        ctx,
        &ticket.id,
        &backend_id,
        &current.interactionRevision,
        now,
    );
    let revision = current.interactionRevision.clone();
    let payload = serde_json::json!({
        "type": "slider_control_start",
        "flow": "ticket_slider_button",
        "activationFlow": "menu_activate",
        "activationAttemptId": activation_attempt_id,
        "interactionRevision": revision,
        "controlId": control_id,
        "correlationId": control_id,
        "initialInputSequence": input_sequence,
        "initialInputPhase": "up",
        "initialProgress": 10_000,
        "instantActivation": true
    })
    .to_string();
    insert_stream_command(
        ctx,
        &ticket.id,
        &backend_id,
        &format!("{}:{}:slider_control_start", ticket.id, control_id),
        "slider_control_start",
        &revision,
        "slider_button_activation_queued",
        &payload,
        TICKET_ACTIVATION_COMMAND_TTL_MS,
        now,
    );
    current.status = "control_active".into();
    current.ownerPublicId = account_public_id(email);
    current.controlId = control_id.into();
    current.leasePhase = "active".into();
    current.leaseExpiresAt = add_ms(now, TICKET_SLIDER_LEASE_MS);
    current.latestInputSequence = input_sequence;
    current.latestInputPhase = "up".into();
    current.latestProgress = 10_000;
    current.reason = "slider_button_activation_queued".into();
    current.updatedAt = now.into();
    current.expiresAt = add_ms(now, TICKET_INTERACTION_TTL_MS);
    upsert_ticket_interaction(ctx, current);
    Ok(())
}

fn claim_ticket_slider_impl(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    interaction_revision: &str,
    control_id: &str,
    initial_input_sequence: &str,
    hold_duration_millis: u32,
    horizontal_travel_css: u32,
    vertical_travel_css: u32,
    initial_progress: u32,
    activation_attempt_id: &str,
    email: &str,
    v2: bool,
    now: &str,
) -> Result<(), String> {
    let ticket = ensure_ticket(ctx, ticket_id, "", now);
    if ticket_has_control_code_request_in_progress(ctx, &ticket.id, now) {
        return Err("control_code_in_progress".into());
    }
    let backend_id = clean_backend_id(backend_id);
    let mut current = current_ticket_interaction(ctx, &ticket.id, &backend_id, now);
    let control_id = control_id.trim();
    let input_sequence = bounded_frame_ordinal(initial_input_sequence);
    if !valid_schedule_identifier(control_id) || input_sequence == "0" {
        return Err("invalid_slider_control".into());
    }
    let activation_attempt_id = non_empty(activation_attempt_id, control_id);
    let existing_attempt = ctx
        .db
        .ticketremote_activation_decision()
        .id()
        .find(activation_decision_id(
            &ticket.id,
            &backend_id,
            &activation_attempt_id,
        ))
        .is_some()
        || activation_history_for_attempt(ctx, &ticket.id, &activation_attempt_id).is_some();
    if existing_attempt {
        let admission = activation_admission_for_action(
            ctx,
            &ticket.id,
            &backend_id,
            email,
            "manual_slider",
            &activation_attempt_id,
            &bounded_text(interaction_revision, 160),
            control_id,
            "",
            &input_sequence,
            v2,
            now,
        )?;
        return if admission.accepted || v2 {
            Ok(())
        } else {
            Err(admission.reason)
        };
    }
    let stale_lease = !matches!(current.leasePhase.as_str(), "" | "none")
        && parse_time_ms(&current.leaseExpiresAt) <= parse_time_ms(now);
    let recoverable = current.status == "unactivated_ready"
        || current.status == "needs_attention"
        || (current.status == "control_active" && stale_lease);
    if !recoverable || current.interactionRevision != bounded_text(interaction_revision, 160) {
        return Err("slider_proof_stale".into());
    }
    if stale_lease || current.status == "needs_attention" {
        current.status = "unactivated_ready".into();
        current.ownerPublicId.clear();
        current.controlId.clear();
        current.leasePhase = "none".into();
        current.leaseExpiresAt.clear();
        current.latestInputSequence = "0".into();
        current.latestInputPhase.clear();
        current.latestProgress = 0;
        current.lastAppliedSequence = "0".into();
        current.lastAppliedProgress = 0;
    }
    if current.sliderRight <= current.sliderLeft || current.sliderBottom <= current.sliderTop {
        return Err("slider_bounds_missing".into());
    }
    if hold_duration_millis < TICKET_SLIDER_QUALIFY_HOLD_MS
        || horizontal_travel_css < TICKET_SLIDER_QUALIFY_TRAVEL_CSS
        || vertical_travel_css.saturating_mul(3) > horizontal_travel_css.saturating_mul(2)
    {
        return Err("slider_qualification_failed".into());
    }
    let lease_available = current.leasePhase == "none"
        || current.leasePhase.is_empty()
        || parse_time_ms(&current.leaseExpiresAt) <= parse_time_ms(now);
    if !lease_available {
        return Err("slider_busy".into());
    }
    let public_id = account_public_id(email);
    let admission = activation_admission_for_action(
        ctx,
        &ticket.id,
        &backend_id,
        email,
        "manual_slider",
        &activation_attempt_id,
        &current.interactionRevision,
        control_id,
        "",
        &input_sequence,
        v2,
        now,
    )?;
    if !admission.accepted {
        return Ok(());
    }
    purge_pending_ticket_slider_commands(
        ctx,
        &ticket.id,
        &backend_id,
        &current.interactionRevision,
        now,
    );
    let progress = initial_progress.min(10_000);
    let revision = current.interactionRevision.clone();
    let payload = serde_json::json!({
        "type": "slider_control_start",
        "flow": "ticket_slider",
        "activationFlow": "manual_slider",
        "activationAttemptId": activation_attempt_id,
        "interactionRevision": revision,
        "controlId": control_id,
        "initialInputSequence": input_sequence,
        "initialProgress": progress
    })
    .to_string();
    insert_stream_command(
        ctx,
        &ticket.id,
        &backend_id,
        &format!("{}:{}:slider_control_start", ticket.id, control_id),
        "slider_control_start",
        &revision,
        "slider_lease_acquired",
        &payload,
        TICKET_ACTIVATION_COMMAND_TTL_MS,
        now,
    );
    current.status = "control_active".into();
    current.ownerPublicId = public_id;
    current.controlId = control_id.into();
    current.leasePhase = "active".into();
    current.leaseExpiresAt = add_ms(now, TICKET_SLIDER_LEASE_MS);
    current.latestInputSequence = input_sequence;
    current.latestInputPhase = "move".into();
    current.latestProgress = progress;
    current.reason = "slider_lease_acquired".into();
    current.updatedAt = now.into();
    current.expiresAt = add_ms(now, TICKET_INTERACTION_TTL_MS);
    upsert_ticket_interaction(ctx, current);
    Ok(())
}

#[spacetimedb::reducer(init)]
pub fn init(ctx: &ReducerContext) {
    let now = now(ctx);
    ensure_cleanup_schedule(ctx, DEFAULT_TICKET_ID, &now);
    ensure_activation_cleanup_schedule(ctx, &now);
}

#[spacetimedb::reducer(client_connected)]
pub fn identity_connected(ctx: &ReducerContext) -> Result<(), String> {
    if has_valid_service_identity(ctx) || operator_identity_is_valid(&ctx.sender().to_string()) {
        return Ok(());
    }
    let email = client_email_from_auth(ctx, DEFAULT_TICKET_ID)?;
    let now = now(ctx);
    upsert_member_identity(ctx, DEFAULT_TICKET_ID, &email, &now);
    refresh_member_limit_state(ctx, DEFAULT_TICKET_ID, &email, &now);
    refresh_member_hdr_state(ctx, DEFAULT_TICKET_ID, &email, &now);
    refresh_member_hdr_engine_state(ctx, DEFAULT_TICKET_ID, &email, &now);
    refresh_member_hdr_boost_state(ctx, DEFAULT_TICKET_ID, &email, &now);
    Ok(())
}

#[spacetimedb::reducer(client_disconnected)]
pub fn identity_disconnected(_ctx: &ReducerContext) {}

#[spacetimedb::reducer]
pub fn ticketremote_member_record_activity_tick(
    ctx: &ReducerContext,
    ticketId: String,
) -> Result<(), String> {
    let observed_at = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &observed_at);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    let bucket = member_activity_bucket(ctx.timestamp)?;
    upsert_member_activity_tick(ctx, &ticket.id, &email, &observed_at, &bucket);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_member_set_limit_preference(
    ctx: &ReducerContext,
    ticketId: String,
    obeyLimits: bool,
) -> Result<(), String> {
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    require_admin(ctx, &ticket.id, &email)?;
    let id = member_id(&ticket.id, &email);
    let table = ctx.db.ticketremote_member_limit_preference();
    if let Some(existing) = table.id().find(&id) {
        table.id().update(TicketremoteMemberLimitPreference {
            obeyLimits,
            updatedAt: now.clone(),
            ..existing
        });
    } else {
        table.insert(TicketremoteMemberLimitPreference {
            id,
            ticketId: ticket.id.clone(),
            email: email.clone(),
            obeyLimits,
            createdAt: now.clone(),
            updatedAt: now.clone(),
        });
    }
    refresh_member_limit_state(ctx, &ticket.id, &email, &now);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_member_refresh_limit_state(
    ctx: &ReducerContext,
    ticketId: String,
) -> Result<(), String> {
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    refresh_member_limit_state(ctx, &ticket.id, &email, &now);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_member_set_hdr_preference(
    ctx: &ReducerContext,
    ticketId: String,
    enabled: bool,
) -> Result<(), String> {
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    let id = member_id(&ticket.id, &email);
    let table = ctx.db.ticketremote_member_hdr_preference();
    if let Some(existing) = table.id().find(&id) {
        table.id().update(TicketremoteMemberHDRPreference {
            enabled,
            updatedAt: now.clone(),
            ..existing
        });
    } else {
        table.insert(TicketremoteMemberHDRPreference {
            id,
            ticketId: ticket.id.clone(),
            email: email.clone(),
            enabled,
            createdAt: now.clone(),
            updatedAt: now.clone(),
        });
    }
    refresh_member_hdr_state(ctx, &ticket.id, &email, &now);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_member_refresh_hdr_state(
    ctx: &ReducerContext,
    ticketId: String,
) -> Result<(), String> {
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    refresh_member_hdr_state(ctx, &ticket.id, &email, &now);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_owner_set_hdr_engine(
    ctx: &ReducerContext,
    ticketId: String,
    engine: String,
) -> Result<(), String> {
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    require_owner(ctx, &ticket.id, &email)?;
    let engine = clean_hdr_engine(&engine);
    let id = member_id(&ticket.id, &email);
    let table = ctx.db.ticketremote_member_hdr_engine_preference();
    if let Some(existing) = table.id().find(&id) {
        table.id().update(TicketremoteMemberHDREnginePreference {
            engine,
            updatedAt: now.clone(),
            ..existing
        });
    } else {
        table.insert(TicketremoteMemberHDREnginePreference {
            id,
            ticketId: ticket.id.clone(),
            email: email.clone(),
            engine,
            createdAt: now.clone(),
            updatedAt: now.clone(),
        });
    }
    refresh_member_hdr_engine_state(ctx, &ticket.id, &email, &now);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_member_refresh_hdr_engine_state(
    ctx: &ReducerContext,
    ticketId: String,
) -> Result<(), String> {
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    if is_owner(ctx, &ticket.id, &email) {
        migrate_legacy_member_hdr_engine_preference(ctx, &ticket.id, &email, &now);
    }
    refresh_member_hdr_engine_state(ctx, &ticket.id, &email, &now);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_owner_set_hdr_display_boost(
    ctx: &ReducerContext,
    ticketId: String,
    selectedDisplayBoost: u32,
) -> Result<(), String> {
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    let selected_display_boost = clean_hdr_display_boost(selectedDisplayBoost);
    let id = member_id(&ticket.id, &email);
    let table = ctx.db.ticketremote_member_hdr_boost_preference();
    if let Some(existing) = table.id().find(&id) {
        table.id().update(TicketremoteMemberHDRBoostPreference {
            selectedDisplayBoost: selected_display_boost,
            updatedAt: now.clone(),
            ..existing
        });
    } else {
        table.insert(TicketremoteMemberHDRBoostPreference {
            id,
            ticketId: ticket.id.clone(),
            email: email.clone(),
            selectedDisplayBoost: selected_display_boost,
            createdAt: now.clone(),
            updatedAt: now.clone(),
        });
    }
    refresh_member_hdr_boost_state(ctx, &ticket.id, &email, &now);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_member_refresh_hdr_boost_state(
    ctx: &ReducerContext,
    ticketId: String,
) -> Result<(), String> {
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    refresh_member_hdr_boost_state(ctx, &ticket.id, &email, &now);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_scheduled_policy_boundary(
    ctx: &ReducerContext,
    arg: TicketremotePolicyBoundaryTimer,
) -> Result<(), String> {
    if !ctx.sender_auth().is_internal() {
        return Err("internal role required".into());
    }
    ctx.db
        .ticketremote_policy_boundary_timer()
        .scheduled_id()
        .delete(arg.scheduled_id);
    let now = now(ctx);
    match arg.subjectKind.as_str() {
        "member" => {
            if is_member(ctx, &arg.ticketId, &arg.subjectId) {
                refresh_member_limit_state(ctx, &arg.ticketId, &arg.subjectId, &now);
            } else {
                ctx.db
                    .ticketremote_member_limit_state()
                    .id()
                    .delete(member_limit_state_id(&arg.ticketId, &arg.subjectId));
            }
        }
        "switch" => {
            expire_ticket_switch_anchor(ctx, &arg.ticketId, &arg.subjectId, &arg.boundaryAt, &now)
        }
        _ => return Err("invalid_policy_boundary_subject".into()),
    }
    Ok(())
}

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
    // Keep the schema-compatible reason argument, but focus is advisory
    // presence telemetry and never owns the service's stream demand.
    let _ = reason;
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

#[allow(clippy::too_many_arguments)]
fn admit_control_code_request_impl(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    session_id: &str,
    clean_digits: &str,
    expected_fast_revision: &str,
    requested_email: &str,
    request_id: Option<&str>,
    now: &str,
) -> Result<(), String> {
    let backend_id = clean_backend_id(backend_id);
    let fast_state_id = control_code_fast_state_id(ticket_id, &backend_id);
    if expected_fast_revision.starts_with("pc-") && !ctx.db.ticketremote_phone_control_state()
        .id().find(phone_row_id(ticket_id, &backend_id))
        .map(|row| phone_control::phone_control_ready(&row, expected_fast_revision, now))
        .unwrap_or(false) {
        return Err("phone_control_context_changed".into());
    }
    let fast_state = ctx
        .db
        .ticketremote_control_code_fast_state()
        .id()
        .find(&fast_state_id);
    let (cleanup_revision, cleanup_stream_epoch, cleanup_frame_sequence, stream_was_live) =
        fast_state
            .as_ref()
            .map(|row| {
                (
                    row.revision.clone(),
                    row.streamEpoch.clone(),
                    row.frameSequence.clone(),
                    row.streamLive,
                )
            })
            .unwrap_or_else(|| (now.into(), String::new(), String::new(), false));
    let request_id = request_id
        .map(str::to_string)
        .unwrap_or_else(|| control_code_request_id(ticket_id, session_id, now));
    let limit_admission = admit_member_limit_event(
        ctx,
        ticket_id,
        requested_email,
        "control_code",
        &request_id,
        now,
    )?;
    if !limit_admission.allowed {
        return Err(limit_admission.reason);
    }
    let owner_public_id = account_public_id(requested_email);
    ctx.db
        .ticketremote_control_code_owner()
        .insert(TicketremoteControlCodeOwner {
            id: request_id.clone(),
            ticketId: clean_ticket_id(ticket_id),
            sessionId: session_id.into(),
            email: requested_email.into(),
            digits: clean_digits.into(),
            requestedAt: now.into(),
            expiresAt: control_code_request_expires_at(now),
        });
    insert_control_code_public_request(ctx, ticket_id, &request_id, &owner_public_id, now);
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
        "dispatchAttempt": 1,
        "fastRevision": bounded_text(expected_fast_revision, 160)
    })
    .to_string();
    insert_stream_command(
        ctx,
        ticket_id,
        &backend_id,
        &format!("{}:generate_control_code", request_id),
        "generate_control_code",
        now,
        "control_code_request",
        &payload,
        CONTROL_CODE_PHONE_TTL_MS,
        now,
    );
    upsert_control_code_fast_state(
        ctx,
        ticket_id,
        &backend_id,
        "cleanup",
        &cleanup_revision,
        "control_code_request",
        &cleanup_stream_epoch,
        &cleanup_frame_sequence,
        false,
        false,
        stream_was_live,
        now,
    );
    Ok(())
}

member_reducers! {
    ticketremote_member_request_control_code(ctx; ticketId: String, backendId: String,
        sessionId: String, digits: String, expectedFastRevision: String; ticket = ticketId)
        |ticket, email, now| {
    require_legacy_phone_admission(ctx, &ticket.id, &backendId)?;
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
    let backend_id = clean_backend_id(&backendId);
    if ticket_phone_mutation_lane_conflict(ctx, &ticket.id, &backend_id, &now).is_some()
    {
        let request_id = control_code_request_id(&ticket.id, &session_id, &now);
        return queue_control_code_intent(
            ctx, &ticket.id, &backend_id, &request_id, &session_id, &clean_digits,
            &expectedFastRevision, &email, &now,
        );
    }
    admit_control_code_request_impl(
        ctx, &ticket.id, &backend_id, &session_id, &clean_digits,
        &expectedFastRevision, &email, None, &now,
    )?;
    }
}

member_reducers! {
    ticketremote_member_request_ticket_reset(ctx; ticketId: String, backendId: String,
        resetRequestId: String, reason: String; ticket = ticketId)
        |ticket, email, now| {
    require_legacy_phone_admission(ctx, &ticket.id, &backendId)?;
    request_ticket_reset_impl(
        ctx,
        &ticket.id,
        &backendId,
        &resetRequestId,
        &reason,
        "",
        &email,
        false,
        &now,
    )?;
    }
}

#[spacetimedb::reducer]
pub fn ticketremote_member_request_ticket_reset_v2(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    resetRequestId: String,
    reason: String,
    attemptId: String,
) -> Result<(), String> {
    require_legacy_phone_admission(ctx, &ticketId, &backendId)?;
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    request_ticket_reset_impl(
        ctx,
        &ticket.id,
        &backendId,
        &resetRequestId,
        &reason,
        &attemptId,
        &email,
        true,
        &now,
    )
}

#[spacetimedb::reducer]
pub fn ticketremote_member_request_ticket_action_v3(
    ctx: &ReducerContext,
    version: u32,
    ticketId: String,
    backendId: String,
    actionId: String,
    target: String,
    source: String,
    reason: String,
    attemptId: String,
    expectedInteractionRevision: String,
    scheduleId: String,
) -> Result<(), String> {
    require_legacy_phone_admission(ctx, &ticketId, &backendId)?;
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    request_ticket_action_v3_impl(
        ctx,
        version,
        &ticket.id,
        &backendId,
        &actionId,
        &target,
        &source,
        &reason,
        &attemptId,
        &expectedInteractionRevision,
        &scheduleId,
        &email,
        &now,
    )
}

member_reducers! {
    ticketremote_member_activate_ticket_button(ctx; ticketId: String, backendId: String,
        interactionRevision: String, controlId: String, inputSequence: String;
        ticket = ticketId) |ticket, email, now| {
    require_legacy_phone_admission(ctx, &ticket.id, &backendId)?;
    activate_ticket_button_impl(
        ctx,
        &ticket.id,
        &backendId,
        &interactionRevision,
        &controlId,
        &inputSequence,
        "",
        &email,
        false,
        &now,
    )?;
    }
}

#[spacetimedb::reducer]
pub fn ticketremote_member_activate_ticket_button_v2(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    interactionRevision: String,
    controlId: String,
    inputSequence: String,
    attemptId: String,
) -> Result<(), String> {
    require_legacy_phone_admission(ctx, &ticketId, &backendId)?;
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    activate_ticket_button_impl(
        ctx,
        &ticket.id,
        &backendId,
        &interactionRevision,
        &controlId,
        &inputSequence,
        &attemptId,
        &email,
        true,
        &now,
    )
}

member_reducers! {
    ticketremote_member_claim_ticket_slider(ctx; ticketId: String, backendId: String,
        interactionRevision: String, controlId: String, initialInputSequence: String,
        holdDurationMillis: u32, horizontalTravelCss: u32, verticalTravelCss: u32,
        initialProgress: u32; ticket = ticketId)
        |ticket, email, now| {
    require_legacy_phone_admission(ctx, &ticket.id, &backendId)?;
    claim_ticket_slider_impl(
        ctx,
        &ticket.id,
        &backendId,
        &interactionRevision,
        &controlId,
        &initialInputSequence,
        holdDurationMillis,
        horizontalTravelCss,
        verticalTravelCss,
        initialProgress,
        "",
        &email,
        false,
        &now,
    )?;
    }
}

#[spacetimedb::reducer]
pub fn ticketremote_member_claim_ticket_slider_v2(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    interactionRevision: String,
    controlId: String,
    initialInputSequence: String,
    holdDurationMillis: u32,
    horizontalTravelCss: u32,
    verticalTravelCss: u32,
    initialProgress: u32,
    attemptId: String,
) -> Result<(), String> {
    require_legacy_phone_admission(ctx, &ticketId, &backendId)?;
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    claim_ticket_slider_impl(
        ctx,
        &ticket.id,
        &backendId,
        &interactionRevision,
        &controlId,
        &initialInputSequence,
        holdDurationMillis,
        horizontalTravelCss,
        verticalTravelCss,
        initialProgress,
        &attemptId,
        &email,
        true,
        &now,
    )
}

member_reducers! {
    ticketremote_member_update_ticket_slider(ctx; ticketId: String, backendId: String,
        interactionRevision: String, controlId: String, inputSequence: String,
        inputPhase: String, progress: u32; ticket = ticketId)
        |ticket, email, now| {
    require_legacy_phone_admission(ctx, &ticket.id, &backendId)?;
    let backend_id = clean_backend_id(&backendId);
    let mut current = current_ticket_interaction(ctx, &ticket.id, &backend_id, &now);
    if current.interactionRevision != bounded_text(&interactionRevision, 160) ||
        current.controlId != controlId.trim() ||
        current.ownerPublicId != account_public_id(&email) {
        return Ok(());
    }
    if current.leasePhase != "active" || current.status != "control_active" {
        return Ok(());
    }
    let sequence = bounded_frame_ordinal(&inputSequence);
    if sequence == "0" || compare_ordinal(&sequence, &current.latestInputSequence) <= 0 {
        return Ok(());
    }
    let phase = allowlisted(&inputPhase, &["move", "heartbeat", "up", "cancel"], "");
    if phase.is_empty() {
        return Err("invalid_slider_phase".into());
    }
    current.latestInputSequence = sequence;
    current.latestInputPhase = phase.clone();
    current.latestProgress = progress.min(10_000);
    current.updatedAt = now.clone();
    current.expiresAt = add_ms(&now, TICKET_INTERACTION_TTL_MS);
    if matches!(phase.as_str(), "up" | "cancel") {
        current.leasePhase = "cooldown".into();
        current.leaseExpiresAt = add_ms(&now, TICKET_SLIDER_COOLDOWN_MS);
        current.status = "unactivated_ready".into();
    } else {
        current.leaseExpiresAt = add_ms(&now, TICKET_SLIDER_LEASE_MS);
    }
    upsert_ticket_interaction(ctx, current);
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
        authorize_and_upsert_member(ctx, &ticket.id, &actor, &email, &role, &now)?
    }
    ticketremote_member_remove_member(ctx; ticketId: String, email: String; ticket = ticketId)
        |ticket, actor, now| {
        authorize_and_deactivate_member(ctx, &ticket.id, &actor, &email, &now)?
    }
}

#[spacetimedb::reducer]
pub fn ticketremote_owner_prepare_vivi_credentials(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
) -> Result<(), String> {
    let now = now(ctx);
    let (ticket_id, _backend_id) = vivi_scope(&ticketId, &backendId)?;
    let ticket = ensure_ticket(ctx, &ticket_id, "", &now);
    let owner_email = client_email_from_auth(ctx, &ticket.id)?;
    require_owner(ctx, &ticket.id, &owner_email)?;
    upsert_member_identity(ctx, &ticket.id, &owner_email, &now);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_owner_save_vivi_credentials(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    email: String,
    password: String,
    expectedRevision: String,
    revision: String,
) -> Result<(), String> {
    let now = now(ctx);
    let (ticket_id, backend_id) = vivi_scope(&ticketId, &backendId)?;
    let ticket = ensure_ticket(ctx, &ticket_id, "", &now);
    let owner_email = client_email_from_auth(ctx, &ticket.id)?;
    require_owner(ctx, &ticket.id, &owner_email)?;
    upsert_member_identity(ctx, &ticket.id, &owner_email, &now);
    if ticket_has_live_vivi_reauth(ctx, &ticket.id, &backend_id) {
        return Err("vivi_reauth_in_progress".into());
    }
    let email = email.trim();
    if email.is_empty()
        || email.len() > 254
        || !email.contains('@')
        || email.chars().any(char::is_control)
    {
        return Err("invalid_vivi_email".into());
    }
    if password.is_empty() || password.len() > 1024 || password.chars().any(char::is_control) {
        return Err("invalid_vivi_password".into());
    }
    let expected_revision = clean_expected_vivi_revision(&expectedRevision)?;
    let revision = clean_vivi_revision(&revision)?;
    let id = phone_row_id(&ticket.id, &backend_id);
    let table = ctx.db.ticketremote_vivi_credentials();
    if let Some(existing) = table.id().find(&id) {
        if existing.revision == revision {
            if existing.email == email && existing.password == password {
                upsert_vivi_credential_state(ctx, &ticket.id, &backend_id, true, &revision, &now);
                return Ok(());
            }
            return Err("vivi_credential_revision_reused".into());
        }
        if current_vivi_credential_revision(ctx, &ticket.id, &backend_id) != expected_revision {
            return Err("vivi_credential_revision_stale".into());
        }
        table.id().update(TicketremoteViviCredentials {
            email: email.into(),
            password,
            revision: revision.clone(),
            updatedAt: now.clone(),
            ..existing
        });
    } else {
        if current_vivi_credential_revision(ctx, &ticket.id, &backend_id) != expected_revision {
            return Err("vivi_credential_revision_stale".into());
        }
        table.insert(TicketremoteViviCredentials {
            id,
            ticketId: ticket.id.clone(),
            backendId: backend_id.clone(),
            email: email.into(),
            password,
            revision: revision.clone(),
            createdAt: now.clone(),
            updatedAt: now.clone(),
        });
    }
    upsert_vivi_credential_state(ctx, &ticket.id, &backend_id, true, &revision, &now);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_owner_clear_vivi_credentials(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    expectedRevision: String,
    revision: String,
) -> Result<(), String> {
    let now = now(ctx);
    let (ticket_id, backend_id) = vivi_scope(&ticketId, &backendId)?;
    let ticket = ensure_ticket(ctx, &ticket_id, "", &now);
    let owner_email = client_email_from_auth(ctx, &ticket.id)?;
    require_owner(ctx, &ticket.id, &owner_email)?;
    upsert_member_identity(ctx, &ticket.id, &owner_email, &now);
    if ticket_has_live_vivi_reauth(ctx, &ticket.id, &backend_id) {
        return Err("vivi_reauth_in_progress".into());
    }
    let expected_revision = clean_expected_vivi_revision(&expectedRevision)?;
    let revision = clean_vivi_revision(&revision)?;
    let id = phone_row_id(&ticket.id, &backend_id);
    let state = ctx.db.ticketremote_vivi_credential_state().id().find(&id);
    let credentials = ctx.db.ticketremote_vivi_credentials().id().find(&id);
    if state
        .as_ref()
        .is_some_and(|row| !row.configured && row.revision == revision)
        && credentials.is_none()
    {
        return Ok(());
    }
    if current_vivi_credential_revision(ctx, &ticket.id, &backend_id) != expected_revision {
        return Err("vivi_credential_revision_stale".into());
    }
    ctx.db.ticketremote_vivi_credentials().id().delete(id);
    upsert_vivi_credential_state(ctx, &ticket.id, &backend_id, false, &revision, &now);
    Ok(())
}

#[allow(clippy::too_many_arguments)]
fn owner_request_vivi_reauth(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    requestId: String,
    credentialRevision: String,
    mode: ViviReauthMode,
) -> Result<(), String> {
    let now = now(ctx);
    let (ticket_id, backend_id) = vivi_scope(&ticketId, &backendId)?;
    let ticket = ensure_ticket(ctx, &ticket_id, "", &now);
    let owner_email = client_email_from_auth(ctx, &ticket.id)?;
    require_owner(ctx, &ticket.id, &owner_email)?;
    upsert_member_identity(ctx, &ticket.id, &owner_email, &now);
    let request_id = clean_vivi_request_id(&requestId)?;
    let credential_revision = clean_vivi_revision(&credentialRevision)?;
    let credentials = ctx
        .db
        .ticketremote_vivi_credentials()
        .id()
        .find(phone_row_id(&ticket.id, &backend_id))
        .ok_or_else(|| "credential_missing".to_string())?;
    if credentials.revision != credential_revision {
        return Err("credential_revision_stale".into());
    }
    let attempt_id = vivi_reauth_attempt_id(&ticket.id, &backend_id, &request_id);
    if let Some(existing) = ctx
        .db
        .ticketremote_vivi_reauth_attempt()
        .id()
        .find(&attempt_id)
    {
        return if existing.credentialRevision == credential_revision {
            Ok(())
        } else {
            Err("vivi_reauth_request_id_reused".into())
        };
    }
    if ticket_phone_mutation_lane_conflict(ctx, &ticket.id, &backend_id, &now).is_some() {
        return queue_vivi_reauth_intent(
            ctx,
            &ticket.id,
            &backend_id,
            &request_id,
            &credential_revision,
            mode,
            &owner_email,
            &now,
        );
    }
    admit_vivi_reauth(
        ctx,
        &ticket.id,
        &backend_id,
        &request_id,
        &credential_revision,
        mode,
        &owner_email,
        &now,
    )
}

#[spacetimedb::reducer]
pub fn ticketremote_owner_request_vivi_reauth(
    ctx: &ReducerContext,
    version: u32,
    ticketId: String,
    backendId: String,
    requestId: String,
    credentialRevision: String,
) -> Result<(), String> {
    require_legacy_phone_admission(ctx, &ticketId, &backendId)?;
    if version != 1 {
        return Err("unsupported_vivi_reauth_version".into());
    }
    if !vivi_reauth_request_mode_matches(&requestId, ViviReauthMode::LegacyV1) {
        return Err("vivi_reauth_request_mode_mismatch".into());
    }
    owner_request_vivi_reauth(
        ctx,
        ticketId,
        backendId,
        requestId,
        credentialRevision,
        ViviReauthMode::LegacyV1,
    )
}

#[spacetimedb::reducer]
pub fn ticketremote_owner_request_vivi_reauth_full_reset(
    ctx: &ReducerContext,
    version: u32,
    ticketId: String,
    backendId: String,
    requestId: String,
    credentialRevision: String,
) -> Result<(), String> {
    require_legacy_phone_admission(ctx, &ticketId, &backendId)?;
    if version != 2 {
        return Err("unsupported_vivi_reauth_version".into());
    }
    if !vivi_reauth_request_mode_matches(&requestId, ViviReauthMode::FullResetV2) {
        return Err("vivi_reauth_request_mode_mismatch".into());
    }
    owner_request_vivi_reauth(
        ctx,
        ticketId,
        backendId,
        requestId,
        credentialRevision,
        ViviReauthMode::FullResetV2,
    )
}

#[spacetimedb::reducer]
pub fn ticketremote_owner_request_vivi_reauth_logout_login(
    ctx: &ReducerContext,
    version: u32,
    ticketId: String,
    backendId: String,
    requestId: String,
    credentialRevision: String,
) -> Result<(), String> {
    require_legacy_phone_admission(ctx, &ticketId, &backendId)?;
    let mode = vivi_reauth_logout_login_mode(version, &requestId)?;
    owner_request_vivi_reauth(
        ctx,
        ticketId,
        backendId,
        requestId,
        credentialRevision,
        mode,
    )
}

#[spacetimedb::reducer]
pub fn ticketremote_update_vivi_reauth_attempt(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    requestId: String,
    credentialRevision: String,
    status: String,
    phase: String,
    reason: String,
    proofSource: String,
    streamEpoch: String,
    frameSequence: String,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let now = now_or(ctx, &nowArg);
    let (ticket_id, backend_id) = vivi_scope(&ticketId, &backendId)?;
    let request_id = clean_vivi_request_id(&requestId)?;
    let credential_revision = clean_vivi_revision(&credentialRevision)?;
    let status = vivi_reauth_status(&status)?;
    let phase = vivi_reauth_phase(&phase)?;
    let reason = vivi_reauth_reason(&reason)?;
    let proof_source = vivi_reauth_proof_source(&proofSource)?;
    let stream_epoch = bounded_frame_ordinal(&streamEpoch);
    let frame_sequence = bounded_frame_ordinal(&frameSequence);
    if !matches!(
        status.as_str(),
        "running" | "succeeded" | "failed" | "needs_attention"
    ) {
        return Err("invalid_vivi_reauth_service_status".into());
    }
    validate_vivi_reauth_service_report_mode(
        &request_id,
        &status,
        &phase,
        &reason,
        &proof_source,
        &stream_epoch,
        &frame_sequence,
    )?;
    let attempt_id = vivi_reauth_attempt_id(&ticket_id, &backend_id, &request_id);
    let attempt = ctx
        .db
        .ticketremote_vivi_reauth_attempt()
        .id()
        .find(&attempt_id)
        .ok_or_else(|| "vivi_reauth_attempt_not_found".to_string())?;
    if attempt.credentialRevision != credential_revision {
        return Err("credential_revision_stale".into());
    }
    if vivi_reauth_terminal(&attempt.status) {
        return if attempt.status == status
            && attempt.phase == phase
            && attempt.reason == reason
            && attempt.proofSource == proof_source
            && attempt.streamEpoch == stream_epoch
            && attempt.frameSequence == frame_sequence
        {
            Ok(())
        } else {
            Err("vivi_reauth_terminal_conflict".into())
        };
    }
    if attempt.status == "queued" {
        return Err("vivi_reauth_attempt_not_dispatched".into());
    }
    // This transition is the durable at-most-once claim for the physical
    // login. A response lost after commit may leave work unperformed, but a
    // second worker must never be allowed to cross the mutation boundary.
    if status == "running" && attempt.status != "pending" {
        return Err("vivi_reauth_attempt_already_claimed".into());
    }
    if status != "running" && attempt.status != "running" {
        return Err("vivi_reauth_attempt_not_claimed".into());
    }
    finish_vivi_reauth_attempt(
        ctx,
        attempt,
        &status,
        &phase,
        &reason,
        &proof_source,
        &stream_epoch,
        &frame_sequence,
        &now,
    )?;
    let command_id = vivi_reauth_command_id(&ticket_id, &backend_id, &request_id);
    if status != "running" {
        update_stream_command_status(ctx, &command_id, "acknowledged", &reason, &now);
    }
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
    let members = ctx.db.ticketremote_ticket_member();
    if !email.is_empty() && members.id().find(member_id(&ticket.id, &email)).is_none() {
        members.insert(TicketremoteTicketMember {
            id: member_id(&ticket.id, &email),
            ticketId: ticket.id.clone(),
            email: email.clone(),
            role: "owner".into(),
            active: true,
            createdAt: now.clone(),
            updatedAt: now.clone(),
        });
    }
    if !email.is_empty() && is_member(ctx, &ticket.id, &email) {
        refresh_member_limit_state(ctx, &ticket.id, &email, &now);
        refresh_member_hdr_state(ctx, &ticket.id, &email, &now);
        refresh_member_hdr_engine_state(ctx, &ticket.id, &email, &now);
        refresh_member_hdr_boost_state(ctx, &ticket.id, &email, &now);
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
    ensure_activation_cleanup_schedule(ctx, &now);
    reconcile_pending_scheduled_redetect_timers(ctx, &now);
    restore_state_replaced_activation_refreshes(ctx, &now);
    reconcile_activation_refresh_timers(ctx, &now);
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
    reconcile_pending_scheduled_redetect_timers(ctx, &now);
    let batch_size = if arg.batchSize == 0 {
        CLEANUP_BATCH_SIZE
    } else {
        arg.batchSize.min(CLEANUP_BATCH_SIZE)
    };
    cleanup_expired(ctx, &arg.ticketId, &now, batch_size);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_scheduled_activation_cleanup(
    ctx: &ReducerContext,
    _arg: TicketremoteActivationCleanupSchedule,
) -> Result<(), String> {
    if !ctx.sender_auth().is_internal() {
        return Err("internal role required".into());
    }
    let now = now(ctx);
    let (_, saturated) =
        cleanup_activation_history(ctx, &now, TICKET_ACTIVATION_CLEANUP_BATCH_SIZE);
    if saturated {
        schedule_activation_cleanup_catchup(ctx, &now);
    }
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_scheduled_activation_cleanup_catchup(
    ctx: &ReducerContext,
    arg: TicketremoteActivationCleanupCatchup,
) -> Result<(), String> {
    if !ctx.sender_auth().is_internal() {
        return Err("internal role required".into());
    }
    ctx.db
        .ticketremote_activation_cleanup_catchup()
        .scheduled_id()
        .delete(arg.scheduled_id);
    let now = now(ctx);
    let (_, saturated) =
        cleanup_activation_history(ctx, &now, TICKET_ACTIVATION_CLEANUP_BATCH_SIZE);
    if saturated {
        schedule_activation_cleanup_catchup(ctx, &now);
    }
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
        authorize_and_upsert_member(ctx, &ticket.id, &actorEmail, &email, &role, &now)?
    }
    ticketremote_remove_member(ctx; ticketId: String, actorEmail: String, email: String,
        nowArg: String) {
        let now = now_or(ctx, &nowArg);
        let ticket = ensure_ticket(ctx, &ticketId, "", &now);
        authorize_and_deactivate_member(ctx, &ticket.id, &actorEmail, &email, &now)?
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
        "latest_ticket_reselect",
        "",
        "",
        &now_or(ctx, &nowArg),
    )
}

#[spacetimedb::reducer]
pub fn ticketremote_schedule_latest_ticket_reselect_v3(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    scheduleId: String,
    scheduledAtMicros: i64,
    phoneLocalTime: String,
    phoneTimeZone: String,
    requestedBy: String,
    target: String,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let target = ticket_action_v3_target(&target);
    if target != "redetect_latest" {
        return Err("invalid_scheduled_ticket_action_target".into());
    }
    schedule_latest_ticket_reselect(
        ctx,
        &ticketId,
        &backendId,
        &scheduleId,
        scheduledAtMicros,
        &phoneLocalTime,
        &phoneTimeZone,
        &requestedBy,
        "latest_ticket_reselect",
        "",
        &target,
        &now_or(ctx, &nowArg),
    )
}

#[spacetimedb::reducer]
pub fn ticketremote_admin_schedule_ticket_action_v3(
    ctx: &ReducerContext,
    version: u32,
    ticketId: String,
    backendId: String,
    scheduleId: String,
    scheduledAtMicros: i64,
    phoneLocalTime: String,
    phoneTimeZone: String,
    target: String,
) -> Result<(), String> {
    if version != 3 {
        return Err("unsupported_ticket_action_version".into());
    }
    let now = now(ctx);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let email = client_email_from_auth(ctx, &ticket.id)?;
    require_admin(ctx, &ticket.id, &email)?;
    let target = ticket_action_v3_target(&target);
    if target != "redetect_latest" {
        return Err("invalid_scheduled_ticket_action_target".into());
    }
    schedule_latest_ticket_reselect(
        ctx,
        &ticket.id,
        &backendId,
        &scheduleId,
        scheduledAtMicros,
        &phoneLocalTime,
        &phoneTimeZone,
        &account_public_id(&email),
        "latest_ticket_reselect",
        "",
        &target,
        &now,
    )
}

#[spacetimedb::reducer]
pub fn ticketremote_schedule_activation_expiry_reset(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    activationRevision: String,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let now = now_or(ctx, &nowArg);
    let activation_revision = bounded_text(&activationRevision, 160);
    if activation_revision.is_empty() {
        return Err("activation_revision_required".into());
    }
    let ticket_id = clean_ticket_id(&ticketId);
    let backend_id = clean_backend_id(&backendId);
    let history =
        activation_history_for_revision(ctx, &ticket_id, &backend_id, &activation_revision)
            .ok_or_else(|| "activation_not_proven".to_string())?;
    if history.outcome != "succeeded" || !activation_history_is_latest_success(ctx, &history) {
        return Err("activation_not_proven".into());
    }
    let original_due_at = history.refreshDueAt.clone();
    let original_due_at_micros = parse_time_micros(&original_due_at);
    if original_due_at_micros <= 0 {
        return Err("activation_refresh_deadline_invalid".into());
    }
    // Preserve the original one-hour deadline in durable state. A late recovery only moves the
    // one-shot timer forward far enough for Spacetime to invoke it; it never manufactures a
    // fresh one-hour activation window.
    let timer_at_micros = activation_refresh_recovery_timer_micros(
        original_due_at_micros,
        ctx.timestamp.to_micros_since_unix_epoch(),
    );
    let schedule_id = format!(
        "{}:{}:activation_expiry:{}",
        ticket_id,
        backend_id,
        stable_stamp(&safe_token(&activation_revision, "activation"))
    );
    let schedule_table = ctx.db.ticketremote_latest_ticket_reselect_schedule();
    if let Some(existing) = schedule_table.id().find(schedule_id.clone()) {
        if activation_history_authorizes_refresh_schedule(&history, &existing)
            && latest_ticket_reselect_idempotent_status(&existing.status)
        {
            return Ok(());
        }
        if !activation_history_can_restore_state_replaced_schedule(&history, &existing) {
            return Err("activation_refresh_schedule_not_restorable".into());
        }
        if reconcile_activation_refresh_terminal_action(ctx, &history, &existing, &now) {
            return Ok(());
        }
        delete_latest_ticket_reselect_timers(ctx, &existing.id);
        ctx.db
            .ticketremote_activation_history()
            .id()
            .update(TicketremoteActivationHistory {
                refreshOutcome: "pending".into(),
                refreshCompletedAt: String::new(),
                refreshRetryAt: if timer_at_micros != original_due_at_micros {
                    iso(Timestamp::from_micros_since_unix_epoch(timer_at_micros))
                } else {
                    String::new()
                },
                refreshAttempt: history.refreshAttempt.saturating_add(1),
                updatedAt: now.clone(),
                ..history.clone()
            });
        let restored_attempt_id = existing
            .activationAttemptId
            .clone()
            .filter(|value| !value.trim().is_empty())
            .unwrap_or_else(|| history.attemptId.clone());
        schedule_table
            .id()
            .update(TicketremoteLatestTicketReselectSchedule {
                scheduledAt: original_due_at.clone(),
                status: "pending".into(),
                commandId: String::new(),
                resultReason: "activation_refresh_restored".into(),
                resultPhase: "pending".into(),
                proofSource: "activation_history".into(),
                updatedAt: now.clone(),
                triggeredAt: String::new(),
                completedAt: String::new(),
                expiresAt: add_ms(&original_due_at, HISTORY_TTL_MS),
                activationAttemptId: Some(restored_attempt_id),
                originalDueAt: Some(original_due_at.clone()),
                nextRetryAt: (timer_at_micros != original_due_at_micros)
                    .then(|| iso(Timestamp::from_micros_since_unix_epoch(timer_at_micros))),
                retryAttempt: existing.retryAttempt.saturating_add(1),
                ..existing
            });
        ctx.db.ticketremote_latest_ticket_reselect_timer().insert(
            TicketremoteLatestTicketReselectTimer {
                scheduled_id: 0,
                scheduled_at: ScheduleAt::Time(Timestamp::from_micros_since_unix_epoch(
                    timer_at_micros,
                )),
                ticketId: ticket_id,
                backendId: backend_id,
                scheduleId: schedule_id,
                createdAt: now,
            },
        );
        return Ok(());
    }
    if history.refreshOutcome != "pending" {
        return Err("activation_refresh_not_pending".into());
    }
    let result = schedule_latest_ticket_reselect(
        ctx,
        &ticket_id,
        &backend_id,
        &schedule_id,
        timer_at_micros,
        "",
        "",
        "pixel_activation",
        "activation_expiry_reset",
        &activation_revision,
        "open_latest_unactivated",
        &now,
    );
    if result.is_ok() {
        if let Some(inserted) = schedule_table.id().find(schedule_id) {
            // schedule_latest_ticket_reselect requires a future timer. Keep the durable row's
            // exact original deadline even when a late recovery uses an immediate timer.
            schedule_table
                .id()
                .update(TicketremoteLatestTicketReselectSchedule {
                    scheduledAt: original_due_at.clone(),
                    activationAttemptId: Some(history.attemptId.clone()),
                    originalDueAt: Some(original_due_at),
                    nextRetryAt: (timer_at_micros != original_due_at_micros)
                        .then(|| iso(Timestamp::from_micros_since_unix_epoch(timer_at_micros))),
                    ..inserted
                });
        }
    }
    result
}

#[spacetimedb::reducer]
pub fn ticketremote_commit_ticket_activation(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    attemptId: String,
    interactionRevision: String,
    activationRevision: String,
) -> Result<(), String> {
    require_service(ctx)?;
    commit_ticket_activation_impl(
        ctx,
        &ticketId,
        &backendId,
        &attemptId,
        &interactionRevision,
        &activationRevision,
    )
}

#[spacetimedb::reducer]
pub fn ticketremote_finalize_ticket_activation_refresh(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    activationRevision: String,
    interactionRevision: String,
    reason: String,
) -> Result<(), String> {
    require_service(ctx)?;
    finalize_ticket_activation_refresh_impl(
        ctx,
        &ticketId,
        &backendId,
        &activationRevision,
        &interactionRevision,
        &reason,
    )
}

#[spacetimedb::reducer]
pub fn ticketremote_finalize_ticket_activation_attempt(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    attemptId: String,
    outcome: String,
    reason: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let now = now(ctx);
    finalize_ticket_activation_failure_impl(
        ctx, &ticketId, &backendId, &attemptId, &outcome, &reason, &now,
    );
    Ok(())
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
        && authoritative_stream_is_idle(ctx, &ticketId, &backendId)
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

/// Pixel-only control-code cleanup handoff. The request barrier and the
/// short-lived ready watermark change in one transaction, so browsers never
/// observe cleanup as complete while the phone lane still projects blocked.
#[spacetimedb::reducer]
pub fn ticketremote_complete_control_code_cleanup_ready(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    requestId: String,
    revision: String,
    streamEpoch: String,
    frameSequence: String,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let now = now_or(ctx, &nowArg);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let backend_id = clean_backend_id(&backendId);
    let request_id = requestId.trim();
    let request_key = request_id.to_string();
    let Some(request) = ctx
        .db
        .ticketremote_control_code_request()
        .id()
        .find(&request_key)
    else {
        return Ok(());
    };
    if request.ticketId != ticket.id {
        return Err("control_code_request_ticket_mismatch".into());
    }
    if !request.captureAcknowledged || !matches!(request.status.as_str(), "succeeded" | "closed") {
        return Err("control_code_cleanup_not_authorized".into());
    }
    if revision.starts_with("pc-") {
        let current = ctx.db.ticketremote_phone_control_state().id()
            .find(phone_row_id(&ticket.id, &backend_id))
            .ok_or("phone_control_session_required")?;
        if !revision.starts_with(&format!("{}:", current.sessionId)) {
            return Err("phone_control_session_changed".into());
        }
        // The service emits this only after raw-detail proof, durable checkpoint
        // clearance and panel finalization. This is a result, not a readiness lease.
        // The independent observation publisher remains the only readiness owner.
        update_control_code_public_request(ctx, request_id, ControlCodeChanges {
            captureRequired: Some(false), cleanupPending: Some(false),
            reason: Some("phone_visual_cleanup_complete".into()),
            expiresAt: Some(control_code_result_expires_at(&now)),
            ..Default::default()
        }, &now);
        promote_ticket_action_v3_queue(ctx, &ticket.id, &backend_id, &now);
        return Ok(());
    }
    // Removed after older phone producers and in-flight control requests drain.
    let stream_epoch = bounded_frame_ordinal(&streamEpoch);
    let frame_sequence = bounded_frame_ordinal(&frameSequence);
    if stream_epoch == "0" || frame_sequence == "0" {
        return Err("control_code_cleanup_visual_proof_required".into());
    }
    update_control_code_public_request(
        ctx,
        request_id,
        ControlCodeChanges {
            captureRequired: Some(false),
            cleanupPending: Some(false),
            reason: Some("phone_visual_cleanup_complete".into()),
            streamEpoch: Some(stream_epoch.clone()),
            frameSequence: Some(frame_sequence.clone()),
            expiresAt: Some(control_code_result_expires_at(&now)),
            ..Default::default()
        },
        &now,
    );
    upsert_control_code_fast_state(
        ctx,
        &ticket.id,
        &backend_id,
        "fast_ready",
        &non_empty(&revision, request_id),
        "phone_visual_cleanup_complete",
        &stream_epoch,
        &frame_sequence,
        true,
        true,
        true,
        &now,
    );
    promote_ticket_action_v3_queue(ctx, &ticket.id, &backend_id, &now);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_update_ticket_interaction(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    status: String,
    interactionRevision: String,
    activationRevision: String,
    activationAt: String,
    scheduledResetAt: String,
    resetRequestId: String,
    streamEpoch: String,
    frameSequence: String,
    phoneDisplayWidth: u32,
    phoneDisplayHeight: u32,
    sliderBoundsJson: String,
    ownerPublicId: String,
    controlId: String,
    leasePhase: String,
    leaseExpiresAt: String,
    latestInputSequence: String,
    latestInputPhase: String,
    latestProgress: u32,
    lastAppliedSequence: String,
    lastAppliedProgress: u32,
    reason: String,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let now = now_or(ctx, &nowArg);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let backend_id = clean_backend_id(&backendId);
    let mut current = current_ticket_interaction(ctx, &ticket.id, &backend_id, &now);
    let bounds = serde_json::from_str::<serde_json::Value>(&sliderBoundsJson)
        .unwrap_or_else(|_| serde_json::json!({}));
    let incoming_sequence = bounded_frame_ordinal(&latestInputSequence);
    let incoming_is_older = compare_ordinal(&current.latestInputSequence, &incoming_sequence) > 0;
    let incoming_status = allowlisted(
        &status,
        &[
            "activated",
            "reset_queued",
            "preparing",
            "unactivated_ready",
            "control_active",
            "completing",
            "needs_attention",
            "failed",
        ],
        "needs_attention",
    );
    let incoming_revision = bounded_text(
        &non_empty(&interactionRevision, &current.interactionRevision),
        160,
    );
    if ticket_interaction_is_fenced_by_terminal_composite_v3(
        ctx,
        &ticket.id,
        &backend_id,
        &incoming_revision,
    ) {
        // The durable composite action already published its terminal result.
        // A compatibility snapshot for that exact revision is necessarily
        // late and may not restore reset, slider, lease, or geometry state.
        return Ok(());
    }
    if ticket_interaction_update_is_stale(&current, &incoming_status, &incoming_revision) {
        return Ok(());
    }
    if ticket_interaction_revision_is_stale(&current, &incoming_status, &incoming_revision) {
        // A late phone snapshot from an older reset/activation may not replace a newer
        // server-owned transition. The phone must re-read the current command revision first.
        return Ok(());
    }
    if ticket_interaction_failure_matches_current(&current, &incoming_status, &incoming_revision) {
        let reason = ticket_reset_failure_reason(&reason, "ticket_reset_proof_failed");
        if ticket_interaction_has_retained_v3_activation_command(ctx, &current) {
            // The V3 action row, command, and activation ledger own retry
            // policy. Preserve their exact revision fence instead of turning a
            // compatibility publication into a synthetic legacy retry.
            current.status = incoming_status;
            current.scheduledResetAt.clear();
            current.resetRequestId.clear();
            current.ownerPublicId.clear();
            current.controlId.clear();
            current.leasePhase = "none".into();
            current.leaseExpiresAt.clear();
            current.latestInputSequence = "0".into();
            current.latestInputPhase.clear();
            current.latestProgress = 0;
            current.lastAppliedSequence = "0".into();
            current.lastAppliedProgress = 0;
            current.reason = bounded_text(&reason, 200);
            current.updatedAt = now.clone();
            current.expiresAt = add_ms(&now, TICKET_INTERACTION_TTL_MS);
            upsert_ticket_interaction(ctx, current);
            return Ok(());
        }
        if let Some(repaired) = repair_ticket_interaction_for_retry(&current, &now, &reason) {
            upsert_ticket_interaction(ctx, repaired);
            return Ok(());
        }
    }
    if incoming_status == "activated" {
        if current.status == "activated"
            && !current.activationRevision.trim().is_empty()
            && bounded_text(&activationRevision, 160) == current.activationRevision
        {
            return Ok(());
        }
        return Err("activation_commit_required".into());
    }
    // A phone worker publishes the result of an input it observed from a
    // snapshot. The browser can advance the interaction between that read and
    // this reducer call (for example, releasing the slider and starting a new
    // attempt). Once a newer input for the same revision is durable, an older
    // worker snapshot must not put its status, lease, owner, activation, or
    // other control metadata back on the row.
    if incoming_status != "unactivated_ready"
        && ticket_interaction_input_update_is_older(
            &current,
            &incoming_revision,
            &incoming_sequence,
        )
    {
        return Ok(());
    }
    if matches!(incoming_status.as_str(), "failed" | "needs_attention")
        && (!current.activationRevision.trim().is_empty()
            || !current.activationAt.trim().is_empty()
            || !current.scheduledResetAt.trim().is_empty())
    {
        let activation_revision = current.activationRevision.clone();
        fail_pending_activation_expiry_schedules(
            ctx,
            &current.ticketId,
            &current.backendId,
            &activation_revision,
            &reason,
            &now,
        );
        clear_current_ticket_activation_state(&mut current);
    }
    let fresh_unactivated_proof = incoming_status == "unactivated_ready";
    let authoritative_reset_request_id = current.resetRequestId.clone();
    if fresh_unactivated_proof {
        cancel_pending_activation_expiry_schedules(
            ctx,
            &ticket.id,
            &backend_id,
            &current.activationRevision,
            Some(&current.interactionRevision),
            &now,
        );
        current.activationAt = String::new();
        current.scheduledResetAt = String::new();
        current.activationRevision = String::new();
        // A fresh registration proof starts a new slider revision. Do not let
        // a previous browser lease or progress sample make the new thumb look
        // occupied or partially moved.
        current.ownerPublicId = String::new();
        current.controlId = String::new();
        current.leasePhase = "none".into();
        current.leaseExpiresAt = String::new();
        current.latestInputSequence = "0".into();
        current.latestInputPhase = String::new();
        current.latestProgress = 0;
        current.lastAppliedSequence = "0".into();
        current.lastAppliedProgress = 0;
        // Keep only the reset correlation owned by the current server interaction. The phone
        // proof may contain stale activation/lease/progress fields, but that must not erase the
        // correlation needed to commit a reset-and-activate attempt.
        current.resetRequestId = authoritative_reset_request_id;
    }
    current.status = incoming_status.clone();
    current.interactionRevision = incoming_revision;
    if !fresh_unactivated_proof {
        if !activationRevision.trim().is_empty() {
            current.activationRevision = bounded_text(&activationRevision, 160);
        }
        if !activationAt.trim().is_empty() {
            current.activationAt = bounded_text(&activationAt, 80);
        }
        if !scheduledResetAt.trim().is_empty() {
            current.scheduledResetAt = bounded_text(&scheduledResetAt, 80);
        }
        if !resetRequestId.trim().is_empty() {
            current.resetRequestId = bounded_text(&resetRequestId, 160);
        }
    }
    current.streamEpoch = bounded_frame_ordinal(&streamEpoch);
    current.frameSequence = bounded_frame_ordinal(&frameSequence);
    current.phoneDisplayWidth = phoneDisplayWidth.min(10_000);
    current.phoneDisplayHeight = phoneDisplayHeight.min(10_000);
    current.sliderLeft = bounds.get("left").and_then(json_i64).unwrap_or(0).max(0) as u32;
    current.sliderTop = bounds.get("top").and_then(json_i64).unwrap_or(0).max(0) as u32;
    current.sliderRight = bounds.get("right").and_then(json_i64).unwrap_or(0).max(0) as u32;
    current.sliderBottom = bounds.get("bottom").and_then(json_i64).unwrap_or(0).max(0) as u32;
    if !fresh_unactivated_proof {
        current.ownerPublicId = bounded_text(&ownerPublicId, 64);
        current.controlId = bounded_text(&controlId, 120);
        current.leasePhase = allowlisted(&leasePhase, &["none", "active", "cooldown"], "none");
        current.leaseExpiresAt = bounded_text(&leaseExpiresAt, 80);
        if !incoming_is_older {
            current.latestInputSequence = incoming_sequence;
            current.latestInputPhase = allowlisted(
                &latestInputPhase,
                &["move", "heartbeat", "up", "cancel", ""],
                "",
            );
            current.latestProgress = latestProgress.min(10_000);
        }
    }
    // `lastAppliedSequence` is an acknowledgement cursor, so an out-of-order
    // worker publication may never move it backwards. A fresh registration
    // proof is the deliberate exception: the unactivated state starts a new
    // interaction and the reset block above clears the cursor.
    if incoming_status != "unactivated_ready"
        && ticket_interaction_last_applied_is_current_or_newer(
            &current.lastAppliedSequence,
            &lastAppliedSequence,
        )
    {
        current.lastAppliedSequence = bounded_frame_ordinal(&lastAppliedSequence);
        current.lastAppliedProgress = lastAppliedProgress.min(10_000);
    }
    current.reason = bounded_text(&non_empty(&reason, &current.reason), 200);
    current.updatedAt = now.clone();
    current.expiresAt = add_ms(&now, TICKET_INTERACTION_TTL_MS);
    upsert_ticket_interaction(ctx, current);
    Ok(())
}

#[derive(Clone, Copy)]
struct TicketSliderRegionV3Input {
    left_basis_points: u32,
    top_basis_points: u32,
    right_basis_points: u32,
    bottom_basis_points: u32,
}

#[derive(Clone, Debug, PartialEq, Eq)]
struct TicketActionV3TerminalFacts {
    target: String,
    status: String,
    phase: String,
    current_view: String,
    stream_epoch: String,
    frame_sequence: String,
    reason: String,
    completed_at: String,
    attempt_id: String,
    interaction_revision: String,
    activation_revision: String,
}

#[allow(clippy::too_many_arguments)]
fn ticket_action_v3_terminal_facts(
    target: &str,
    status: &str,
    phase: &str,
    current_view: &str,
    stream_epoch: &str,
    frame_sequence: &str,
    reason: &str,
    completed_at: &str,
    attempt_id: &str,
    interaction_revision: &str,
    activation_revision: &str,
    now: &str,
) -> Result<TicketActionV3TerminalFacts, String> {
    let target = ticket_action_v3_target(target);
    let status = ticket_action_v3_status(status);
    if target.is_empty() || !ticket_action_v3_terminal(&status) {
        return Err("ticket_action_terminal_result_invalid".into());
    }
    let current_view = current_view.trim();
    if !matches!(
        current_view,
        "latest_unactivated" | "recent_activated" | "activated_current" | "unknown"
    ) {
        return Err("ticket_action_terminal_view_invalid".into());
    }
    let phase = allowlisted(
        phase,
        &[
            "activation_proven",
            "complete",
            "failed",
            "needs_attention",
            "no_transition",
            "not_dispatched",
            "outcome_unknown",
            "retry_not_dispatched",
            "startup_reconcile",
        ],
        "",
    );
    if phase.is_empty() {
        return Err("ticket_action_terminal_phase_invalid".into());
    }
    let reason = ticket_action_v3_public_reason(reason, "ticket_action_v3_failed");
    let activation_target = ticket_action_v3_is_activation(&target);
    let phase_matches_result = match phase.as_str() {
        "activation_proven" => activation_target && status == "succeeded",
        "complete" => !activation_target && status == "succeeded",
        "failed" => status == "failed",
        "needs_attention" => status == "needs_attention",
        "no_transition" | "retry_not_dispatched" => {
            activation_target && status == "needs_attention" && current_view == "latest_unactivated"
        }
        "not_dispatched" => activation_target && status != "succeeded",
        "outcome_unknown" => activation_target && status == "needs_attention",
        "startup_reconcile" => status == "failed",
        _ => false,
    };
    if !phase_matches_result {
        return Err("ticket_action_terminal_phase_result_mismatch".into());
    }
    let completed_at = canonical_time(completed_at);
    if parse_time_micros(&completed_at) <= 0
        || parse_time_micros(&completed_at) > parse_time_micros(now).saturating_add(60 * 1_000_000)
    {
        return Err("ticket_action_terminal_time_invalid".into());
    }
    let stream_epoch = bounded_frame_ordinal(stream_epoch);
    let frame_sequence = bounded_frame_ordinal(frame_sequence);
    // Only the authenticated phone service can settle this immutable command.
    // Typed outcomes are proved from unencoded observations; media ordinals are
    // optional presentation metadata and cannot withhold a completed outcome.
    let attempt_id = attempt_id.trim();
    if !attempt_id.is_empty() && !valid_schedule_identifier(attempt_id) {
        return Err("invalid_activation_attempt_id".into());
    }
    let interaction_revision = bounded_text(interaction_revision.trim(), 160);
    if interaction_revision.is_empty() {
        return Err("ticket_action_interaction_revision_required".into());
    }
    let activation_revision = bounded_text(activation_revision.trim(), 160);
    if activation_target {
        if attempt_id.is_empty() {
            return Err("ticket_action_attempt_id_mismatch".into());
        }
        if status == "succeeded" {
            if activation_revision.is_empty() || current_view != "activated_current" {
                return Err("ticket_action_activation_proof_invalid".into());
            }
        } else if !activation_revision.is_empty() {
            return Err("ticket_action_unproved_activation_revision".into());
        }
    }
    Ok(TicketActionV3TerminalFacts {
        target,
        status,
        phase,
        current_view: current_view.into(),
        stream_epoch,
        frame_sequence,
        reason,
        completed_at,
        attempt_id: attempt_id.into(),
        interaction_revision,
        activation_revision,
    })
}

fn ticket_action_v3_terminal_replay_matches(
    action: &TicketremoteTicketActionV3,
    facts: &TicketActionV3TerminalFacts,
) -> bool {
    ticket_action_v3_terminal(&action.status)
        && action.parentActionId.as_deref().unwrap_or("").is_empty()
        && action.rootActionId.as_deref().unwrap_or(&action.actionId) == action.actionId
        && action.retryOrdinal == 0
        && action.target == facts.target
        && action.status == facts.status
        && action.phase == facts.phase
        && action.currentView == facts.current_view
        && action.streamEpoch == facts.stream_epoch
        && action.frameSequence == facts.frame_sequence
        && action.reason == facts.reason
        && canonical_time(&action.completedAt) == facts.completed_at
}

fn ticket_action_v3_slider_region_replay_matches(
    region: Option<&TicketremoteTicketSliderRegionV3>,
    action: &TicketremoteTicketActionV3,
    input: Option<TicketSliderRegionV3Input>,
) -> bool {
    match region {
        // Geometry is intentionally short-lived. Its absence cannot turn an
        // already matching terminal action into a conflict, and replay must
        // not recreate or extend an expired proof.
        None => true,
        // The one region row is a current-view projection. A later proof may
        // legitimately supersede this action while its immutable terminal
        // facts remain available for idempotent acknowledgement.
        Some(region) if region.proofActionId != action.actionId => true,
        Some(region) => input.is_some_and(|input| {
            region.ticketId == action.ticketId
                && region.backendId == action.backendId
                && region.streamEpoch == action.streamEpoch
                && region.frameSequence == action.frameSequence
                && region.leftBasisPoints == input.left_basis_points
                && region.topBasisPoints == input.top_basis_points
                && region.rightBasisPoints == input.right_basis_points
                && region.bottomBasisPoints == input.bottom_basis_points
        }),
    }
}

fn ticket_action_v3_command_matches_finalization(
    command: &TicketremoteStreamCommand,
    action: &TicketremoteTicketActionV3,
    facts: &TicketActionV3TerminalFacts,
) -> Result<bool, String> {
    if command.commandType != "ticket_action_v3"
        || command.ticketId != action.ticketId
        || command.backendId != action.backendId
        || !matches!(
            command.status.as_str(),
            "pending" | "dispatched" | "running"
        )
        || command.revision != facts.interaction_revision
        || !action.parentActionId.as_deref().unwrap_or("").is_empty()
        || action.rootActionId.as_deref().unwrap_or(&action.actionId) != action.actionId
        || action.retryOrdinal != 0
    {
        return Ok(false);
    }
    let payload = serde_json::from_str::<serde_json::Value>(&command.payloadJson)
        .map_err(|_| "ticket_action_command_payload_invalid".to_string())?;
    if payload.get("version").and_then(|value| value.as_u64()) != Some(3)
        || payload
            .get("actionId")
            .and_then(|value| value.as_str())
            .map(str::trim)
            != Some(action.actionId.as_str())
        || payload
            .get("target")
            .and_then(|value| value.as_str())
            .map(str::trim)
            != Some(facts.target.as_str())
    {
        return Ok(false);
    }
    let payload_attempt = payload
        .get("attemptId")
        .and_then(|value| value.as_str())
        .unwrap_or("")
        .trim();
    let payload_schedule = payload
        .get("scheduleId")
        .and_then(|value| value.as_str())
        .unwrap_or("")
        .trim();
    let refresh_flow = payload
        .get("flow")
        .and_then(|value| value.as_str())
        .unwrap_or("")
        == "activation_expiry_reset";
    if ticket_action_v3_is_activation(&facts.target) {
        return Ok(facts.attempt_id == action.actionId
            && payload_attempt == facts.attempt_id
            && payload_schedule.is_empty()
            && payload
                .get("expectedInteractionRevision")
                .and_then(|value| value.as_str())
                .unwrap_or("")
                == if facts.target == "register_current" {
                    facts.interaction_revision.as_str()
                } else {
                    ""
                });
    }
    if refresh_flow {
        return Ok(payload_schedule == action.actionId
            && payload
                .get("activationAttemptId")
                .and_then(|value| value.as_str())
                .unwrap_or("")
                == facts.attempt_id
            && payload
                .get("activationRevision")
                .and_then(|value| value.as_str())
                .unwrap_or("")
                == facts.activation_revision
            && !facts.attempt_id.is_empty()
            && !facts.activation_revision.is_empty());
    }
    Ok(payload_attempt.is_empty()
        && facts.attempt_id.is_empty()
        && facts.activation_revision.is_empty()
        && (payload_schedule.is_empty() || payload_schedule == action.actionId))
}

fn ticket_action_v3_command_is_activation_refresh(command: &TicketremoteStreamCommand) -> bool {
    serde_json::from_str::<serde_json::Value>(&command.payloadJson)
        .ok()
        .is_some_and(|payload| {
            payload.get("flow").and_then(|value| value.as_str()) == Some("activation_expiry_reset")
        })
}

fn ticket_action_v3_activation_admission_matches(
    history: &TicketremoteActivationHistory,
    action: &TicketremoteTicketActionV3,
    facts: &TicketActionV3TerminalFacts,
) -> bool {
    history.ticketId == action.ticketId
        && history.backendId == action.backendId
        && history.admission == "admitted"
        && history.attemptId == action.actionId
        && history.attemptId == facts.attempt_id
        && history.interactionRevision == facts.interaction_revision
        && history.interactionCorrelation == action.actionId
}

fn ticket_slider_region_v3_row_for_action(
    ticket_id: &str,
    backend_id: &str,
    action: &TicketremoteTicketActionV3,
    input: TicketSliderRegionV3Input,
    now: &str,
) -> Result<TicketremoteTicketSliderRegionV3, String> {
    if !ticket_slider_region_v3_bounds_valid(
        input.left_basis_points,
        input.top_basis_points,
        input.right_basis_points,
        input.bottom_basis_points,
    ) {
        return Err("invalid_ticket_slider_region_bounds".into());
    }
    if !matches!(
        action.target.as_str(),
        "open_latest_unactivated"
            | "return_to_latest_unactivated"
            | "redetect_latest"
            | "prove_current"
    ) || action.status != "succeeded"
        || action.currentView != "latest_unactivated"
        || action.streamEpoch == "0"
        || action.frameSequence == "0"
        || parse_time_ms(&action.expiresAt) <= parse_time_ms(now)
    {
        return Err("ticket_slider_region_proof_mismatch".into());
    }
    Ok(TicketremoteTicketSliderRegionV3 {
        id: ticket_slider_region_v3_id(ticket_id, backend_id),
        ticketId: ticket_id.into(),
        backendId: backend_id.into(),
        proofActionId: action.actionId.clone(),
        streamEpoch: action.streamEpoch.clone(),
        frameSequence: action.frameSequence.clone(),
        leftBasisPoints: input.left_basis_points,
        topBasisPoints: input.top_basis_points,
        rightBasisPoints: input.right_basis_points,
        bottomBasisPoints: input.bottom_basis_points,
        updatedAt: now.into(),
        expiresAt: add_ms(now, TICKET_SLIDER_REGION_V3_TTL_MS),
    })
}

fn update_ticket_action_v3_projection(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    action_id: &str,
    target: &str,
    status: &str,
    phase: &str,
    current_view: &str,
    stream_epoch: &str,
    frame_sequence: &str,
    reason: &str,
    completed_at: &str,
    slider_region: Option<TicketSliderRegionV3Input>,
    now: &str,
) -> Result<(), String> {
    if !valid_schedule_identifier(action_id) {
        return Err("invalid_ticket_action_id".into());
    }
    let clean_target = ticket_action_v3_target(target);
    let clean_status = ticket_action_v3_status(status);
    if clean_target.is_empty() || clean_status.is_empty() {
        return Err("invalid_ticket_action_update".into());
    }
    let id = ticket_action_v3_row_id(ticket_id, backend_id, action_id);
    let Some(existing) = ctx.db.ticketremote_ticket_action_v3().id().find(&id) else {
        return Err("ticket_action_not_found".into());
    };
    if existing.target != clean_target {
        return Err("ticket_action_target_mismatch".into());
    }
    if ticket_action_v3_terminal(&existing.status) {
        if existing.status != clean_status {
            return Err("ticket_action_already_terminal".into());
        }
        if let Some(input) = slider_region {
            let row = ticket_slider_region_v3_row_for_action(
                ticket_id, backend_id, &existing, input, now,
            )?;
            upsert_row!(ctx, ticketremote_ticket_slider_region_v3, row);
        }
        return Ok(());
    }
    let current_view = ticket_action_v3_view(current_view);
    let terminal = ticket_action_v3_terminal(&clean_status);
    let completed_at = if terminal {
        non_empty(completed_at, now)
    } else {
        String::new()
    };
    let command_revision = ctx
        .db
        .ticketremote_stream_command()
        .id()
        .find(ticket_action_v3_command_id(
            ticket_id, backend_id, action_id,
        ))
        .map(|command| command.revision);
    let mut updated = TicketremoteTicketActionV3 {
        status: clean_status,
        phase: bounded_text(&safe_token(phase, "running"), 80),
        currentView: current_view,
        switchAvailable: false,
        switchExpiresAt: String::new(),
        streamEpoch: bounded_frame_ordinal(stream_epoch),
        frameSequence: bounded_frame_ordinal(frame_sequence),
        reason: ticket_action_v3_public_reason(reason, "ticket_action_updated"),
        updatedAt: now.into(),
        completedAt: bounded_text(&completed_at, 80),
        expiresAt: add_ms(
            now,
            if clean_target == "prove_current" {
                TICKET_SLIDER_REGION_V3_TTL_MS
            } else {
                HISTORY_TTL_MS
            },
        ),
        ..existing
    };
    ctx.db
        .ticketremote_ticket_action_v3()
        .id()
        .update(updated.clone());
    note_ticket_switch_visual_result(ctx, &updated, now);
    let switch_anchor = if updated.status == "succeeded" {
        ticket_switch_projection_for_view(ctx, ticket_id, backend_id, &updated.currentView, now)
    } else {
        None
    };
    if let Some(anchor) = switch_anchor {
        updated.switchAvailable = true;
        updated.switchExpiresAt = anchor.expiresAt;
        ctx.db
            .ticketremote_ticket_action_v3()
            .id()
            .update(updated.clone());
    }
    let interaction_id = ticket_interaction_id(ticket_id, backend_id);
    let reconciled = ctx
        .db
        .ticketremote_ticket_interaction()
        .id()
        .find(&interaction_id)
        .and_then(|current| {
            reconcile_legacy_interaction_after_ticket_action_v3(
                &current,
                &updated,
                command_revision.as_deref(),
                now,
            )
        });
    if let Some(reconciled) = reconciled {
        upsert_ticket_interaction(ctx, reconciled);
    }
    if terminal
        && !(updated.status == "succeeded"
            && updated.currentView == "latest_unactivated"
            && matches!(
                updated.target.as_str(),
                "open_latest_unactivated"
                    | "return_to_latest_unactivated"
                    | "redetect_latest"
                    | "prove_current"
            ))
    {
        ctx.db
            .ticketremote_ticket_slider_region_v3()
            .id()
            .delete(ticket_slider_region_v3_id(ticket_id, backend_id));
    }
    if let Some(input) = slider_region {
        let row =
            ticket_slider_region_v3_row_for_action(ticket_id, backend_id, &updated, input, now)?;
        upsert_row!(ctx, ticketremote_ticket_slider_region_v3, row);
    }
    Ok(())
}

fn ticket_action_v3_activation_replay_matches(
    ctx: &ReducerContext,
    action: &TicketremoteTicketActionV3,
    facts: &TicketActionV3TerminalFacts,
) -> bool {
    let Some(history) = activation_history_for_attempt(ctx, &action.ticketId, &facts.attempt_id)
    else {
        return false;
    };
    if !ticket_action_v3_activation_admission_matches(&history, action, facts)
        || canonical_time(&history.completedAt) != facts.completed_at
    {
        return false;
    }
    if facts.status == "succeeded" {
        history.outcome == "succeeded" && history.activationRevision == facts.activation_revision
    } else {
        history.outcome == "failed"
            && history.reason == safe_token(&bounded_text(&facts.reason, 80), "activation_failed")
            && history.activationRevision.is_empty()
    }
}

fn ticket_action_v3_refresh_replay_matches(
    ctx: &ReducerContext,
    action: &TicketremoteTicketActionV3,
    command_id: &str,
    facts: &TicketActionV3TerminalFacts,
) -> bool {
    let Some(history) = activation_history_for_revision(
        ctx,
        &action.ticketId,
        &action.backendId,
        &facts.activation_revision,
    ) else {
        return false;
    };
    let schedule = ctx
        .db
        .ticketremote_latest_ticket_reselect_schedule()
        .id()
        .find(&action.actionId);
    ticket_action_v3_refresh_terminal_replay_matches(
        &history,
        schedule.as_ref(),
        action,
        command_id,
        facts,
    )
}

fn ticket_action_v3_refresh_terminal_replay_matches(
    history: &TicketremoteActivationHistory,
    schedule: Option<&TicketremoteLatestTicketReselectSchedule>,
    action: &TicketremoteTicketActionV3,
    command_id: &str,
    facts: &TicketActionV3TerminalFacts,
) -> bool {
    let terminal_status = if facts.status == "succeeded" {
        "succeeded"
    } else {
        "failed"
    };
    history.ticketId == action.ticketId
        && history.backendId == action.backendId
        && history.admission == "admitted"
        && history.outcome == "succeeded"
        && history.attemptId == facts.attempt_id
        && history.activationRevision == facts.activation_revision
        && facts.interaction_revision == format!("schedule:{}", action.actionId)
        && history.refreshOutcome == terminal_status
        && canonical_time(&history.refreshCompletedAt) == facts.completed_at
        // A terminal schedule has the same six-hour retention as its action,
        // but bounded cleanup may remove it first. Immutable action + history
        // facts remain sufficient; never recreate an expired schedule.
        && schedule.is_none_or(|schedule| {
            schedule.purpose.as_deref() == Some("activation_expiry_reset")
                && schedule.activationAttemptId.as_deref() == Some(facts.attempt_id.as_str())
                && schedule.activationRevision.as_deref()
                    == Some(facts.activation_revision.as_str())
                && schedule.ticketId == action.ticketId
                && schedule.backendId == action.backendId
                && schedule.commandId == command_id
                && schedule.status == terminal_status
                && schedule.resultReason == facts.reason
                && schedule.resultPhase == facts.phase
                && schedule.proofSource == "phone_worker"
                && schedule.completedAt == facts.completed_at
        })
}

fn ticket_action_v3_scheduled_replay_matches(
    ctx: &ReducerContext,
    action: &TicketremoteTicketActionV3,
    command_id: &str,
    facts: &TicketActionV3TerminalFacts,
) -> bool {
    let schedule = ctx
        .db
        .ticketremote_latest_ticket_reselect_schedule()
        .id()
        .find(&action.actionId);
    ticket_action_v3_scheduled_terminal_replay_matches(schedule.as_ref(), action, command_id, facts)
}

fn ticket_action_v3_scheduled_terminal_replay_matches(
    schedule: Option<&TicketremoteLatestTicketReselectSchedule>,
    action: &TicketremoteTicketActionV3,
    command_id: &str,
    facts: &TicketActionV3TerminalFacts,
) -> bool {
    if !facts.attempt_id.is_empty() || !facts.activation_revision.is_empty() {
        return false;
    }
    let terminal_status = if facts.status == "succeeded" {
        "succeeded"
    } else {
        "failed"
    };
    match schedule {
        // Ordinary browser actions use their action id as the immutable
        // command revision. A scheduled action must retain its schedule row
        // for replay, because no existing action field can distinguish it
        // safely after that row is gone.
        None => facts.interaction_revision == action.actionId,
        Some(schedule) => {
            facts.interaction_revision == format!("schedule:{}", action.actionId)
                && schedule.purpose.as_deref() == Some("ticket_action_v3_redetect_latest")
                && schedule.ticketId == action.ticketId
                && schedule.backendId == action.backendId
                && schedule.commandId == command_id
                && schedule.status == terminal_status
                && schedule.resultReason == facts.reason
                && schedule.resultPhase == facts.phase
                && schedule.proofSource == "phone_worker"
                && schedule.completedAt == facts.completed_at
        }
    }
}

fn finalize_ticket_action_v3_refresh_failure(
    ctx: &ReducerContext,
    action: &TicketremoteTicketActionV3,
    facts: &TicketActionV3TerminalFacts,
    now: &str,
) -> Result<(), String> {
    let schedule = active_activation_refresh_schedule_for_revision(
        ctx,
        &action.ticketId,
        &action.backendId,
        &facts.activation_revision,
    )
    .ok_or_else(|| "activation_refresh_schedule_not_active".to_string())?;
    if schedule.id != action.actionId
        || schedule.activationAttemptId.as_deref() != Some(facts.attempt_id.as_str())
        || facts.interaction_revision != format!("schedule:{}", schedule.id)
    {
        return Err("activation_refresh_correlation_mismatch".into());
    }
    let history = activation_refresh_history_for_schedule(ctx, &schedule)
        .ok_or_else(|| "activation_refresh_history_mismatch".to_string())?;
    let _ = fail_current_activation_refresh(
        ctx,
        &action.ticketId,
        &action.backendId,
        &facts.activation_revision,
        &facts.reason,
        now,
    );
    ctx.db
        .ticketremote_activation_history()
        .id()
        .update(TicketremoteActivationHistory {
            refreshOutcome: "failed".into(),
            refreshCompletedAt: facts.completed_at.clone(),
            refreshRetryAt: String::new(),
            updatedAt: now.into(),
            ..history
        });
    delete_latest_ticket_reselect_timers(ctx, &schedule.id);
    ctx.db
        .ticketremote_latest_ticket_reselect_schedule()
        .id()
        .update(TicketremoteLatestTicketReselectSchedule {
            status: "failed".into(),
            resultReason: facts.reason.clone(),
            resultPhase: facts.phase.clone(),
            proofSource: "phone_worker".into(),
            updatedAt: now.into(),
            completedAt: facts.completed_at.clone(),
            expiresAt: add_ms(now, HISTORY_TTL_MS),
            ..schedule
        });
    refresh_activation_eligibility(ctx, &action.ticketId, &action.backendId, now);
    Ok(())
}

fn finalize_ticket_action_v3_scheduled_result(
    ctx: &ReducerContext,
    command: &TicketremoteStreamCommand,
    action: &TicketremoteTicketActionV3,
    facts: &TicketActionV3TerminalFacts,
    now: &str,
) -> Result<(), String> {
    let schedule_id = ticket_reset_command_payload_value(&command.payloadJson, "scheduleId");
    if schedule_id.is_empty() {
        return Ok(());
    }
    if schedule_id != action.actionId {
        return Err("ticket_action_schedule_id_mismatch".into());
    }
    let schedule = latest_ticket_reselect_schedule_for_command(ctx, &command.id)
        .ok_or_else(|| "ticket_action_schedule_not_active".to_string())?;
    if schedule.id != schedule_id
        || schedule.ticketId != action.ticketId
        || schedule.backendId != action.backendId
        || schedule.purpose.as_deref() != Some("ticket_action_v3_redetect_latest")
    {
        return Err("ticket_action_schedule_mismatch".into());
    }
    delete_latest_ticket_reselect_timers(ctx, &schedule.id);
    ctx.db
        .ticketremote_latest_ticket_reselect_schedule()
        .id()
        .update(TicketremoteLatestTicketReselectSchedule {
            status: if facts.status == "succeeded" {
                "succeeded".into()
            } else {
                "failed".into()
            },
            resultReason: facts.reason.clone(),
            resultPhase: facts.phase.clone(),
            proofSource: "phone_worker".into(),
            updatedAt: now.into(),
            completedAt: facts.completed_at.clone(),
            expiresAt: add_ms(now, HISTORY_TTL_MS),
            ..schedule
        });
    Ok(())
}

/// Pixel's single terminal handoff. Reducers are transactional, so action,
/// activation/refresh state, optional geometry, command retirement, signal,
/// and one queue promotion either all commit or all roll back.
#[spacetimedb::reducer]
pub fn ticketremote_finalize_ticket_action_v3(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    commandId: String,
    actionId: String,
    target: String,
    status: String,
    phase: String,
    currentView: String,
    streamEpoch: String,
    frameSequence: String,
    reason: String,
    completedAt: String,
    attemptId: String,
    interactionRevision: String,
    activationRevision: String,
    hasSliderRegion: bool,
    leftBasisPoints: u32,
    topBasisPoints: u32,
    rightBasisPoints: u32,
    bottomBasisPoints: u32,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let now = canonical_time(&now_or(ctx, &nowArg));
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let backend_id = canonical_activation_backend(ctx, &ticket.id, &backendId)?;
    let action_id = actionId.trim();
    if !valid_schedule_identifier(action_id) {
        return Err("invalid_ticket_action_id".into());
    }
    let expected_command_id = ticket_action_v3_command_id(&ticket.id, &backend_id, action_id);
    if commandId.trim() != expected_command_id {
        return Err("ticket_action_command_id_mismatch".into());
    }
    let facts = ticket_action_v3_terminal_facts(
        &target,
        &status,
        &phase,
        &currentView,
        &streamEpoch,
        &frameSequence,
        &reason,
        &completedAt,
        &attemptId,
        &interactionRevision,
        &activationRevision,
        &now,
    )?;
    let action_row_id = ticket_action_v3_row_id(&ticket.id, &backend_id, action_id);
    let action = ctx
        .db
        .ticketremote_ticket_action_v3()
        .id()
        .find(&action_row_id);
    let command = ctx
        .db
        .ticketremote_stream_command()
        .id()
        .find(&expected_command_id);
    if ticket_action_v3_obsolete_read_only_finalization(
        &facts.target,
        action
            .as_ref()
            .map(|row| (row.target.as_str(), row.status.as_str())),
        command.is_some(),
        &facts.attempt_id,
        &facts.activation_revision,
    ) {
        return Ok(());
    }
    let action = action.ok_or_else(|| "ticket_action_not_found".to_string())?;
    if action.target != facts.target {
        return Err("ticket_action_target_mismatch".into());
    }
    let slider_input = hasSliderRegion.then_some(TicketSliderRegionV3Input {
        left_basis_points: leftBasisPoints,
        top_basis_points: topBasisPoints,
        right_basis_points: rightBasisPoints,
        bottom_basis_points: bottomBasisPoints,
    });
    if slider_input.is_some()
        && (facts.status != "succeeded"
            || facts.current_view != "latest_unactivated"
            || ticket_action_v3_is_activation(&facts.target))
    {
        return Err("ticket_action_slider_region_result_invalid".into());
    }
    if ticket_action_v3_terminal(&action.status) {
        if command.is_some() || !ticket_action_v3_terminal_replay_matches(&action, &facts) {
            return Err("ticket_action_terminal_conflict".into());
        }
        let region_id = ticket_slider_region_v3_id(&ticket.id, &backend_id);
        let region = ctx
            .db
            .ticketremote_ticket_slider_region_v3()
            .id()
            .find(region_id);
        if !ticket_action_v3_slider_region_replay_matches(region.as_ref(), &action, slider_input) {
            return Err("ticket_action_terminal_region_conflict".into());
        }
        if ticket_action_v3_is_activation(&facts.target)
            && !ticket_action_v3_activation_replay_matches(ctx, &action, &facts)
        {
            return Err("ticket_action_activation_terminal_conflict".into());
        }
        if !ticket_action_v3_is_activation(&facts.target) {
            if !facts.activation_revision.is_empty() {
                if !ticket_action_v3_refresh_replay_matches(
                    ctx,
                    &action,
                    &expected_command_id,
                    &facts,
                ) {
                    return Err("ticket_action_refresh_terminal_conflict".into());
                }
            } else if !ticket_action_v3_scheduled_replay_matches(
                ctx,
                &action,
                &expected_command_id,
                &facts,
            ) {
                return Err("ticket_action_schedule_terminal_conflict".into());
            }
        }
        return Ok(());
    }
    if !matches!(action.status.as_str(), "pending" | "running") {
        return Err("ticket_action_not_dispatched".into());
    }
    let command = command.ok_or_else(|| "ticket_action_command_not_found".to_string())?;
    if !ticket_action_v3_command_matches_finalization(&command, &action, &facts)? {
        return Err("ticket_action_command_mismatch".into());
    }
    let activation_refresh = ticket_action_v3_command_is_activation_refresh(&command);
    if ticket_action_v3_is_activation(&facts.target) {
        let history = activation_history_for_attempt(ctx, &ticket.id, &facts.attempt_id)
            .ok_or_else(|| "activation_admission_not_found".to_string())?;
        if !ticket_action_v3_activation_admission_matches(&history, &action, &facts) {
            return Err("activation_admission_mismatch".into());
        }
    }

    update_ticket_action_v3_projection(
        ctx,
        &ticket.id,
        &backend_id,
        action_id,
        &facts.target,
        &facts.status,
        &facts.phase,
        &facts.current_view,
        &facts.stream_epoch,
        &facts.frame_sequence,
        &facts.reason,
        &facts.completed_at,
        slider_input,
        &now,
    )?;
    if slider_input.is_none() {
        ctx.db
            .ticketremote_ticket_slider_region_v3()
            .id()
            .delete(ticket_slider_region_v3_id(&ticket.id, &backend_id));
    }

    if ticket_action_v3_is_activation(&facts.target) {
        if facts.status == "succeeded" {
            commit_ticket_activation_at_impl(
                ctx,
                &ticket.id,
                &backend_id,
                &facts.attempt_id,
                &facts.interaction_revision,
                &facts.activation_revision,
                &facts.completed_at,
                &now,
            )?;
        } else {
            finalize_ticket_activation_failure_checked_impl(
                ctx,
                &ticket.id,
                &backend_id,
                &facts.attempt_id,
                "failed",
                &facts.reason,
                &facts.interaction_revision,
                &facts.completed_at,
                &now,
            )?;
        }
    } else if activation_refresh {
        if facts.status == "succeeded" {
            finalize_ticket_activation_refresh_at_impl(
                ctx,
                &ticket.id,
                &backend_id,
                &facts.activation_revision,
                &facts.interaction_revision,
                &facts.reason,
                &facts.phase,
                &facts.completed_at,
                &now,
            )?;
        } else {
            finalize_ticket_action_v3_refresh_failure(ctx, &action, &facts, &now)?;
        }
    } else {
        finalize_ticket_action_v3_scheduled_result(ctx, &command, &action, &facts, &now)?;
    }

    ctx.db
        .ticketremote_stream_command()
        .id()
        .delete(&expected_command_id);
    upsert_stream_command_signal(ctx, &ticket.id, &backend_id, &command.revision, &now);
    promote_ticket_action_v3_queue(ctx, &ticket.id, &backend_id, &now);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_update_ticket_action_v3(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    actionId: String,
    target: String,
    status: String,
    phase: String,
    currentView: String,
    switchAvailable: bool,
    switchExpiresAt: String,
    streamEpoch: String,
    frameSequence: String,
    reason: String,
    completedAt: String,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let now = now_or(ctx, &nowArg);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let backend_id = clean_backend_id(&backendId);
    // Compatibility parameters are deliberately ignored. Pixel proves the
    // visual view; only the Spacetime anchor decides availability and expiry.
    let _ = (switchAvailable, &switchExpiresAt);
    update_ticket_action_v3_projection(
        ctx,
        &ticket.id,
        &backend_id,
        actionId.trim(),
        &target,
        &status,
        &phase,
        &currentView,
        &streamEpoch,
        &frameSequence,
        &reason,
        &completedAt,
        None,
        &now,
    )
}

/// Additive Pixel-only path. The terminal action projection and its optional
/// normalized slider geometry commit in one database transaction, so members
/// never observe a successful proof without the matching hit region.
#[spacetimedb::reducer]
pub fn ticketremote_update_ticket_action_v3_with_slider_region(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    actionId: String,
    target: String,
    status: String,
    phase: String,
    currentView: String,
    streamEpoch: String,
    frameSequence: String,
    reason: String,
    completedAt: String,
    hasSliderRegion: bool,
    leftBasisPoints: u32,
    topBasisPoints: u32,
    rightBasisPoints: u32,
    bottomBasisPoints: u32,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    if !ticket_action_v3_terminal(&ticket_action_v3_status(&status)) {
        return Err("ticket_action_terminal_projection_required".into());
    }
    let now = now_or(ctx, &nowArg);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let backend_id = clean_backend_id(&backendId);
    let slider_region = hasSliderRegion.then_some(TicketSliderRegionV3Input {
        left_basis_points: leftBasisPoints,
        top_basis_points: topBasisPoints,
        right_basis_points: rightBasisPoints,
        bottom_basis_points: bottomBasisPoints,
    });
    update_ticket_action_v3_projection(
        ctx,
        &ticket.id,
        &backend_id,
        actionId.trim(),
        &target,
        &status,
        &phase,
        &currentView,
        &streamEpoch,
        &frameSequence,
        &reason,
        &completedAt,
        slider_region,
        &now,
    )
}

#[spacetimedb::reducer]
pub fn ticketremote_update_ticket_slider_region_v3(
    ctx: &ReducerContext,
    ticketId: String,
    backendId: String,
    proofActionId: String,
    streamEpoch: String,
    frameSequence: String,
    leftBasisPoints: u32,
    topBasisPoints: u32,
    rightBasisPoints: u32,
    bottomBasisPoints: u32,
    nowArg: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let now = now_or(ctx, &nowArg);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let backend_id = clean_backend_id(&backendId);
    let proof_action_id = proofActionId.trim();
    if !valid_schedule_identifier(proof_action_id) {
        return Err("invalid_ticket_slider_region_proof".into());
    }
    if !ticket_slider_region_v3_bounds_valid(
        leftBasisPoints,
        topBasisPoints,
        rightBasisPoints,
        bottomBasisPoints,
    ) {
        return Err("invalid_ticket_slider_region_bounds".into());
    }
    let stream_epoch = bounded_frame_ordinal(&streamEpoch);
    let frame_sequence = bounded_frame_ordinal(&frameSequence);
    if stream_epoch == "0" || frame_sequence == "0" {
        return Err("invalid_ticket_slider_region_watermark".into());
    }
    let action_id = ticket_action_v3_row_id(&ticket.id, &backend_id, proof_action_id);
    let Some(action) = ctx.db.ticketremote_ticket_action_v3().id().find(action_id) else {
        return Err("ticket_slider_region_proof_not_found".into());
    };
    if !matches!(
        action.target.as_str(),
        "open_latest_unactivated"
            | "return_to_latest_unactivated"
            | "redetect_latest"
            | "prove_current"
    ) || action.status != "succeeded"
        || action.currentView != "latest_unactivated"
        || action.streamEpoch != stream_epoch
        || action.frameSequence != frame_sequence
        || parse_time_ms(&action.expiresAt) <= parse_time_ms(&now)
    {
        return Err("ticket_slider_region_proof_mismatch".into());
    }
    let row = TicketremoteTicketSliderRegionV3 {
        id: ticket_slider_region_v3_id(&ticket.id, &backend_id),
        ticketId: ticket.id,
        backendId: backend_id,
        proofActionId: proof_action_id.into(),
        streamEpoch: stream_epoch,
        frameSequence: frame_sequence,
        leftBasisPoints,
        topBasisPoints,
        rightBasisPoints,
        bottomBasisPoints,
        updatedAt: now.clone(),
        expiresAt: add_ms(&now, TICKET_SLIDER_REGION_V3_TTL_MS),
    };
    upsert_row!(ctx, ticketremote_ticket_slider_region_v3, row);
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
    let mut clean_status = safe_token(&status, &existing.status);
    if !control_code_status_update_allowed(&existing.status, &clean_status) {
        return Ok(());
    }
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

// Queue delivery may prioritize a terminal result ahead of retained progress.
// Such late progress must never revive or regress an already-settled request.
fn control_code_status_update_allowed(current: &str, incoming: &str) -> bool {
    fn rank(status: &str) -> u8 {
        match status {
            "queued" => 1,
            "running" => 2,
            "generated" => 3,
            "succeeded" | "failed" => 4,
            "closed" | "expired" => 5,
            _ => 0,
        }
    }
    if control_code_terminal_failure_status(current) && incoming != current {
        return control_code_terminal_failure_status(incoming) && rank(incoming) >= rank(current);
    }
    rank(incoming) >= rank(current) && rank(incoming) > 0
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

fn vivi_scope(ticket_id: &str, backend_id: &str) -> Result<(String, String), String> {
    let ticket_id = clean_ticket_id(ticket_id);
    if ticket_id != DEFAULT_TICKET_ID {
        return Err("unsupported_vivi_ticket".into());
    }
    let backend_id = clean_backend_id(backend_id);
    if backend_id != "pixel" {
        return Err("unsupported_vivi_backend".into());
    }
    Ok((ticket_id, backend_id))
}

fn clean_vivi_revision(value: &str) -> Result<String, String> {
    let value = value.trim();
    if value.is_empty()
        || value.len() > 160
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'-' | b':'))
    {
        return Err("invalid_vivi_credential_revision".into());
    }
    Ok(value.into())
}

fn clean_expected_vivi_revision(value: &str) -> Result<String, String> {
    let value = value.trim();
    if value.is_empty() {
        Ok(String::new())
    } else {
        clean_vivi_revision(value)
    }
}

fn clean_vivi_request_id(value: &str) -> Result<String, String> {
    let value = value.trim();
    if value.is_empty()
        || value.len() > 160
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'-' | b':'))
    {
        return Err("invalid_vivi_reauth_request_id".into());
    }
    Ok(value.into())
}

fn vivi_reauth_request_mode(request_id: &str) -> Option<ViviReauthMode> {
    let request_id = request_id.trim();
    for (prefix, mode) in [
        (VIVI_REAUTH_LEGACY_REQUEST_PREFIX, ViviReauthMode::LegacyV1),
        (
            VIVI_REAUTH_FULL_RESET_REQUEST_PREFIX,
            ViviReauthMode::FullResetV2,
        ),
        (
            VIVI_REAUTH_LOGOUT_LOGIN_REQUEST_PREFIX,
            ViviReauthMode::LogoutLoginV3,
        ),
        (
            VIVI_REAUTH_LOGOUT_REDETECT_LOGIN_REQUEST_PREFIX,
            ViviReauthMode::LogoutLoginRedetectV4,
        ),
    ] {
        if request_id
            .strip_prefix(prefix)
            .is_some_and(|suffix| !suffix.is_empty())
        {
            return Some(mode);
        }
    }
    None
}

fn vivi_reauth_request_mode_matches(request_id: &str, expected: ViviReauthMode) -> bool {
    vivi_reauth_request_mode(request_id) == Some(expected)
}

fn vivi_reauth_logout_login_mode(version: u32, request_id: &str) -> Result<ViviReauthMode, String> {
    let mode = match version {
        3 => ViviReauthMode::LogoutLoginV3,
        4 => ViviReauthMode::LogoutLoginRedetectV4,
        _ => return Err("unsupported_vivi_reauth_version".into()),
    };
    vivi_reauth_request_mode_matches(request_id, mode)
        .then_some(mode)
        .ok_or_else(|| "vivi_reauth_request_mode_mismatch".into())
}

fn validate_vivi_reauth_service_report_mode(
    request_id: &str,
    status: &str,
    phase: &str,
    reason: &str,
    proof_source: &str,
    stream_epoch: &str,
    frame_sequence: &str,
) -> Result<(), String> {
    let mode = vivi_reauth_request_mode(request_id)
        .ok_or_else(|| "vivi_reauth_request_mode_mismatch".to_string())?;
    let v4_phase = phase == "redetecting_latest_ticket";
    let v4_reason = matches!(
        reason,
        "saved_credentials_original_ticket_restored"
            | "saved_credentials_latest_ticket_redetected"
            | "saved_credentials_no_ticket_proven"
    );
    if mode != ViviReauthMode::LogoutLoginRedetectV4 && (v4_phase || v4_reason) {
        return Err("vivi_reauth_report_mode_mismatch".into());
    }
    if mode != ViviReauthMode::LogoutLoginRedetectV4 {
        return Ok(());
    }
    if v4_phase
        && (status != "running"
            || reason != "running"
            || !proof_source.is_empty()
            || stream_epoch != "0"
            || frame_sequence != "0")
    {
        return Err("vivi_reauth_report_mode_mismatch".into());
    }
    if v4_reason && status != "succeeded" {
        return Err("vivi_reauth_report_mode_mismatch".into());
    }
    if status == "succeeded"
        && (phase != "complete"
            || !v4_reason
            || proof_source != "phone_visual")
    {
        return Err("vivi_reauth_v4_success_proof_invalid".into());
    }
    Ok(())
}

fn vivi_reauth_status(value: &str) -> Result<String, String> {
    let value = value.trim();
    matches!(
        value,
        "queued" | "pending" | "running" | "succeeded" | "failed" | "needs_attention"
    )
    .then(|| value.to_string())
    .ok_or_else(|| "invalid_vivi_reauth_status".into())
}

fn vivi_reauth_phase(value: &str) -> Result<String, String> {
    let value = value.trim();
    matches!(
        value,
        "queued"
            | "waiting_for_phone_lane"
            | "opening_vivi"
            | "resetting_vivi"
            | "detecting_login"
            | "opening_account_controls"
            | "requesting_logout"
            | "verifying_signed_out"
            | "entering_credentials"
            | "submitting"
            | "verifying_signed_in"
            | "redetecting_latest_ticket"
            | "complete"
    )
    .then(|| value.to_string())
    .ok_or_else(|| "invalid_vivi_reauth_phase".into())
}

fn vivi_reauth_reason(value: &str) -> Result<String, String> {
    let value = value.trim();
    matches!(
        value,
        "requested"
            | "queued"
            | "running"
            | "signed_in_proven"
            | "saved_credentials_sign_in_proven"
            | "saved_credentials_original_ticket_restored"
            | "saved_credentials_latest_ticket_redetected"
            | "saved_credentials_no_ticket_proven"
            | "credentials_rejected"
            | "login_required"
            | "login_screen_not_detected"
            | "login_fields_not_detected"
            | "additional_verification_required"
            | "device_link_required"
            | "profile_selection_required"
            | "captcha_required"
            | "onboarding_required"
            | "logout_control_not_detected"
            | "logout_transition_not_proven"
            | "logout_action_uncertain"
            | "visual_proof_failed"
            | "credential_missing"
            | "credential_revision_stale"
            | "command_expired"
            | "owner_role_required"
            | "internal_failure"
    )
    .then(|| value.to_string())
    .ok_or_else(|| "invalid_vivi_reauth_reason".into())
}

fn vivi_reauth_proof_source(value: &str) -> Result<String, String> {
    let value = value.trim();
    matches!(value, "" | "phone_visual" | "phone_worker" | "spacetimedb")
        .then(|| value.to_string())
        .ok_or_else(|| "invalid_vivi_reauth_proof_source".into())
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
    fn is_owner(ctx: &ReducerContext, ticket: &str, email: &str) -> bool =
        ctx.db.ticketremote_ticket_member().id().find(member_id(ticket, email))
            .map(|row| row.active && row.role == "owner")
            .unwrap_or(false);
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
    fn clean_role(value: &str) -> String = allowlisted(value, &["owner", "admin"], "member");
    fn clean_hdr_engine(_value: &str) -> String = "client_webgpu_v2".into();
    fn clean_hdr_display_boost(value: u32) -> u32 = match value {
        2 | 3 | 4 | 5 | 6 => value,
        _ => 4,
    };
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
        "phone_visual_generated_inline", "phone_visual_generated_with_close",
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
    fn service_member_account_from_row(row: &TicketremoteTicketMember) -> TicketremoteServiceMemberAccount = {
        let email = clean_email(&row.email);
        TicketremoteServiceMemberAccount { id: row.id.clone(), ticketId: row.ticketId.clone(),
            email: email.clone(), publicId: account_public_id(&email),
            accountScopeId: account_scope_id(&email), role: clean_role(&row.role), active: row.active,
            updatedAt: row.updatedAt.clone() }
    };
    fn public_hash(value: &str, len: usize) -> String = {
        let mut out = to_base36(fnv32(value.trim()));
        if out.len() < len { out = format!("{:0>width$}", out, width = len); }
        out.chars().take(len).collect()
    };
    fn require_admin(ctx: &ReducerContext, ticket: &str, email: &str) -> Result<(), String> =
        is_admin(ctx, ticket, email).then_some(()).ok_or_else(|| "forbidden".into());
    fn require_owner(ctx: &ReducerContext, ticket: &str, email: &str) -> Result<(), String> =
        is_owner(ctx, ticket, email).then_some(()).ok_or_else(|| "owner role required".into());
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
        matches!(row.status.as_str(), "queued" | "running" | "generated")
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

#[derive(Clone, Debug, Eq, PartialEq)]
struct MemberActivityBucket {
    day: String,
    hour: usize,
    tick_slot: i64,
    expires_at: String,
}

fn member_activity_bucket(timestamp: Timestamp) -> Result<MemberActivityBucket, String> {
    let micros = timestamp.to_micros_since_unix_epoch();
    let utc = DateTime::<Utc>::from_timestamp_micros(micros)
        .ok_or_else(|| "activity timestamp outside supported range".to_string())?;
    member_activity_bucket_from_utc(utc)
}

fn member_activity_bucket_from_utc(utc: DateTime<Utc>) -> Result<MemberActivityBucket, String> {
    let local = utc.with_timezone(&Riga);
    let day = local.date_naive();
    let expiry_day = day
        .checked_add_days(Days::new(MEMBER_ACTIVITY_RETENTION_DAYS))
        .ok_or_else(|| "activity expiry outside supported range".to_string())?;
    let expiry_midnight = expiry_day
        .and_hms_opt(0, 0, 0)
        .ok_or_else(|| "activity expiry boundary unavailable".to_string())?;
    let expiry_local = match Riga.from_local_datetime(&expiry_midnight) {
        LocalResult::Single(value) => value,
        // Europe/Riga has no modern midnight transition, but choosing the
        // earliest occurrence keeps the boundary deterministic if that ever
        // changes in the timezone database.
        LocalResult::Ambiguous(earliest, _) => earliest,
        LocalResult::None => return Err("activity expiry boundary unavailable".into()),
    };
    let expires_at = iso(Timestamp::from_micros_since_unix_epoch(
        expiry_local.with_timezone(&Utc).timestamp_micros(),
    ));
    Ok(MemberActivityBucket {
        day: format!("{:04}-{:02}-{:02}", day.year(), day.month(), day.day()),
        hour: local.hour() as usize,
        tick_slot: utc
            .timestamp_micros()
            .div_euclid(MEMBER_ACTIVITY_TICK_SLOT_MICROS),
        expires_at,
    })
}

fn member_activity_row_id(ticket_id: &str, account_scope_id: &str, day: &str) -> String {
    format!(
        "{}:{}:{}",
        clean_ticket_id(ticket_id),
        account_scope_id.trim(),
        day.trim()
    )
}

fn apply_member_activity_tick(
    hourly_ticks: &mut Vec<u32>,
    last_tick_slot: &mut i64,
    hour: usize,
    tick_slot: i64,
) -> bool {
    if hour >= MEMBER_ACTIVITY_HOURS_PER_DAY || tick_slot <= *last_tick_slot {
        return false;
    }
    hourly_ticks.resize(MEMBER_ACTIVITY_HOURS_PER_DAY, 0);
    hourly_ticks.truncate(MEMBER_ACTIVITY_HOURS_PER_DAY);
    hourly_ticks[hour] = hourly_ticks[hour].saturating_add(1);
    *last_tick_slot = tick_slot;
    true
}

fn upsert_member_activity_tick(
    ctx: &ReducerContext,
    ticket_id: &str,
    email: &str,
    observed_at: &str,
    bucket: &MemberActivityBucket,
) {
    let ticket_id = clean_ticket_id(ticket_id);
    let account_scope_id = account_scope_id(email);
    let id = member_activity_row_id(&ticket_id, &account_scope_id, &bucket.day);
    let table = ctx.db.ticketremote_member_daily_activity();
    if let Some(mut existing) = table.id().find(&id) {
        if !apply_member_activity_tick(
            &mut existing.hourlyTicks,
            &mut existing.lastTickSlot,
            bucket.hour,
            bucket.tick_slot,
        ) {
            return;
        }
        existing.lastTickAt = observed_at.into();
        existing.updatedAt = observed_at.into();
        existing.expiresAt = bucket.expires_at.clone();
        table.id().update(existing);
        return;
    }

    let mut hourly_ticks = vec![0; MEMBER_ACTIVITY_HOURS_PER_DAY];
    let mut last_tick_slot = i64::MIN;
    let accepted = apply_member_activity_tick(
        &mut hourly_ticks,
        &mut last_tick_slot,
        bucket.hour,
        bucket.tick_slot,
    );
    debug_assert!(accepted);
    table.insert(TicketremoteMemberDailyActivity {
        id,
        ticketId: ticket_id,
        accountScopeId: account_scope_id,
        day: bucket.day.clone(),
        hourlyTicks: hourly_ticks,
        lastTickSlot: last_tick_slot,
        firstTickAt: observed_at.into(),
        lastTickAt: observed_at.into(),
        updatedAt: observed_at.into(),
        expiresAt: bucket.expires_at.clone(),
    });
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
    relay_current_report_suppresses_background_stream_command(ctx, &id, now)
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

fn authoritative_stream_is_idle(ctx: &ReducerContext, ticket_id: &str, backend_id: &str) -> bool {
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    let id = phone_row_id(&ticket_id, &backend_id);
    let Some(desired) = ctx.db.ticketremote_stream_desired_state().id().find(&id) else {
        return false;
    };
    !desired.desiredActive && desired.viewerCount == 0
}

fn relay_current_report_suppresses_background_stream_command(
    ctx: &ReducerContext,
    id: &String,
    now: &str,
) -> bool {
    let Some(report) = ctx.db.ticketremote_relay_current_report().id().find(id) else {
        return false;
    };
    relay_current_report_is_live_for_background_suppression(&report, now)
}

fn relay_current_report_is_live_for_background_suppression(
    report: &TicketremoteRelayCurrentReport,
    now: &str,
) -> bool {
    if report.videoClients == 0 || report.streamVerdict != "live" {
        return false;
    }
    let now_ms = parse_time_ms(now);
    let updated_at_ms = parse_time_ms(&report.updatedAt);
    if now_ms <= 0 || updated_at_ms <= 0 || updated_at_ms > now_ms {
        return false;
    }
    let report_age_ms = now_ms - updated_at_ms;
    if !(0..=STREAM_BACKGROUND_REPORT_MAX_AGE_MS).contains(&report_age_ms) {
        return false;
    }
    let Some(visual_age) = relay_last_frame_age_ms(report.lastFrameAt.as_deref(), now) else {
        return false;
    };
    let Ok(status) = serde_json::from_str::<serde_json::Value>(&report.statusJson) else {
        return false;
    };
    let live = status
        .get("live")
        .and_then(|value| value.as_bool())
        .unwrap_or(false);
    let live_fresh = status
        .get("freshnessState")
        .and_then(|value| value.as_str())
        .is_some_and(|value| value == "LIVE_FRESH");
    let visual_age_known = status
        .get("lastFrameVisualAgeKnown")
        .and_then(|value| value.as_bool())
        .unwrap_or(false);
    let bounded_clock = status
        .get("phoneClockBoundedCalibrated")
        .and_then(|value| value.as_bool())
        .unwrap_or(false);
    let tsf3 = status
        .get("frameEnvelope")
        .and_then(|value| value.as_str())
        .is_some_and(|value| value == "tsf3");
    let active_clients = status
        .get("activeVideoClients")
        .and_then(json_i64)
        .unwrap_or(report.videoClients as i64);
    let max_age = match status.get("liveFrameMaxAgeMillis") {
        None => STREAM_BACKGROUND_SUPPRESS_FALLBACK_MAX_AGE_MS,
        Some(value) => {
            let Some(value) = json_i64(value).filter(|value| *value >= 0) else {
                return false;
            };
            value.min(STREAM_BACKGROUND_SUPPRESS_FALLBACK_MAX_AGE_MS)
        }
    };
    let reported_visual_age = status
        .get("lastFrameVisualAgeMillis")
        .and_then(json_i64)
        .filter(|value| *value >= 0 && *value <= max_age);
    live && live_fresh
        && visual_age_known
        && bounded_clock
        && tsf3
        && active_clients > 0
        && reported_visual_age.is_some()
        && visual_age <= max_age
}

fn relay_public_stream_verdict(report: &TicketremoteRelayCurrentReport, now: &str) -> String {
    if report.streamVerdict == "live"
        && !relay_current_report_is_live_for_background_suppression(report, now)
    {
        return "stale_recovering".into();
    }
    report.streamVerdict.clone()
}

fn relay_last_frame_age_ms(last_frame_at: Option<&str>, now: &str) -> Option<i64> {
    let last_frame_at_ms = parse_time_ms(last_frame_at?.trim());
    let now_ms = parse_time_ms(now);
    if last_frame_at_ms <= 0 || now_ms <= 0 || last_frame_at_ms > now_ms {
        return None;
    }
    Some(now_ms - last_frame_at_ms)
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

fn upsert_member_identity(ctx: &ReducerContext, ticket_id: &str, email: &str, now: &str) {
    let identity = ctx.sender();
    let id = identity.to_string();
    let row = TicketremoteMemberIdentity {
        id: id.clone(),
        identity,
        ticketId: clean_ticket_id(ticket_id),
        email: clean_email(email),
        updatedAt: now.into(),
    };
    let table = ctx.db.ticketremote_member_identity();
    if table.id().find(&id).is_some() {
        table.id().update(row);
    } else {
        table.insert(row);
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

fn requested_member_role(value: &str) -> Result<String, String> {
    match value.trim().to_ascii_lowercase().as_str() {
        "owner" => Ok("owner".into()),
        "admin" => Ok("admin".into()),
        "member" => Ok("member".into()),
        _ => Err("invalid_role".into()),
    }
}

fn member_upsert_policy(
    actor_role: Option<&str>,
    target_role: Option<&str>,
    requested_role: &str,
    active_owner_count: usize,
) -> Result<String, String> {
    let requested_role = requested_member_role(requested_role)?;
    match actor_role {
        Some("owner") => {}
        Some("admin")
            if requested_role == "member" && target_role.is_none_or(|role| role == "member") => {}
        _ => return Err("forbidden".into()),
    }
    if target_role == Some("owner") && requested_role != "owner" && active_owner_count <= 1 {
        return Err("owner_protected".into());
    }
    Ok(requested_role)
}

fn member_remove_policy(
    actor_role: Option<&str>,
    target_role: Option<&str>,
    active_owner_count: usize,
) -> Result<(), String> {
    match actor_role {
        Some("owner") => {}
        Some("admin") if target_role.is_none_or(|role| role == "member") => {}
        _ => return Err("forbidden".into()),
    }
    if target_role == Some("owner") && active_owner_count <= 1 {
        return Err("owner_protected".into());
    }
    Ok(())
}

fn active_member_role(ctx: &ReducerContext, ticket_id: &str, email: &str) -> Option<String> {
    ctx.db
        .ticketremote_ticket_member()
        .id()
        .find(member_id(ticket_id, email))
        .filter(|row| row.active)
        .map(|row| row.role)
}

fn active_owner_count(ctx: &ReducerContext, ticket_id: &str) -> usize {
    let ticket_id = clean_ticket_id(ticket_id);
    ctx.db
        .ticketremote_ticket_member()
        .ticketId()
        .filter(&ticket_id)
        .filter(|row| row.active && row.role == "owner")
        .count()
}

/// Revocation fence for an owner-triggered phone login that has not started on
/// the Pixel. The phone publishes `running` before its first physical boundary;
/// after that point the already-started at-most-once action is allowed to report
/// its terminal result rather than being orphaned mid-mutation.
fn cancel_unstarted_vivi_reauth_for_owner(
    ctx: &ReducerContext,
    ticket_id: &str,
    owner_email: &str,
    now: &str,
) {
    let ticket_id = clean_ticket_id(ticket_id);
    let owner_email = clean_email(owner_email);
    let mut affected_backends = Vec::new();
    let attempts = ctx
        .db
        .ticketremote_vivi_reauth_owner()
        .ticketOwner()
        .filter((&ticket_id, &owner_email))
        .filter_map(|authority| {
            ctx.db
                .ticketremote_vivi_reauth_attempt()
                .id()
                .find(&authority.id)
        })
        .filter(|attempt| matches!(attempt.status.as_str(), "queued" | "pending"))
        .collect::<Vec<_>>();
    for attempt in attempts {
        if attempt.status == "queued" {
            let queue_id = ticket_action_v3_queue_id(&attempt.ticketId, &attempt.backendId);
            let queued = ctx
                .db
                .ticketremote_ticket_action_v3_queued_intent()
                .id()
                .find(&queue_id)
                .is_some_and(|intent| {
                    intent.kind == "vivi_reauth"
                        && intent.actionId == attempt.requestId
                        && clean_email(&intent.requestedEmail) == owner_email
                });
            if queued {
                ctx.db
                    .ticketremote_ticket_action_v3_queued_intent()
                    .id()
                    .delete(&queue_id);
            }
        }
        // Queued and pending attempts both already have an exact durable
        // command. Remove it at the same revocation boundary so expiry cannot
        // overwrite the owner-role terminal result later.
        let command_id =
            vivi_reauth_command_id(&attempt.ticketId, &attempt.backendId, &attempt.requestId);
        if let Some(command) = ctx.db.ticketremote_stream_command().id().find(&command_id) {
            ctx.db
                .ticketremote_stream_command()
                .id()
                .delete(&command_id);
            upsert_stream_command_signal(
                ctx,
                &command.ticketId,
                &command.backendId,
                &command.revision,
                now,
            );
        }
        let backend_id = attempt.backendId.clone();
        let _ = finish_vivi_reauth_attempt(
            ctx,
            attempt,
            "failed",
            "complete",
            "owner_role_required",
            "spacetimedb",
            "0",
            "0",
            now,
        );
        affected_backends.push(backend_id);
    }
    affected_backends.sort();
    affected_backends.dedup();
    for backend_id in affected_backends {
        promote_ticket_action_v3_queue(ctx, &ticket_id, &backend_id, now);
    }
}

fn authorize_and_upsert_member(
    ctx: &ReducerContext,
    ticket_id: &str,
    actor_email: &str,
    target_email: &str,
    requested_role: &str,
    now: &str,
) -> Result<(), String> {
    let actor_role = active_member_role(ctx, ticket_id, actor_email);
    let target_role = active_member_role(ctx, ticket_id, target_email);
    let role = member_upsert_policy(
        actor_role.as_deref(),
        target_role.as_deref(),
        requested_role,
        active_owner_count(ctx, ticket_id),
    )?;
    let owner_revoked = target_role.as_deref() == Some("owner") && role != "owner";
    upsert_member_row(ctx, ticket_id, target_email, &role, now);
    if owner_revoked {
        cancel_unstarted_vivi_reauth_for_owner(ctx, ticket_id, target_email, now);
    }
    Ok(())
}

fn authorize_and_deactivate_member(
    ctx: &ReducerContext,
    ticket_id: &str,
    actor_email: &str,
    target_email: &str,
    now: &str,
) -> Result<(), String> {
    let actor_role = active_member_role(ctx, ticket_id, actor_email);
    let target_role = active_member_role(ctx, ticket_id, target_email);
    member_remove_policy(
        actor_role.as_deref(),
        target_role.as_deref(),
        active_owner_count(ctx, ticket_id),
    )?;
    deactivate_member_row(ctx, ticket_id, target_email, now);
    if target_role.as_deref() == Some("owner") {
        cancel_unstarted_vivi_reauth_for_owner(ctx, ticket_id, target_email, now);
    }
    Ok(())
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
        email: email.clone(),
        role: role.into(),
        active: true,
        createdAt: created_at,
        updatedAt: now.into(),
    };
    table.insert(row);
    refresh_member_limit_state(ctx, ticket_id, &email, now);
    refresh_member_hdr_state(ctx, ticket_id, &email, now);
    refresh_member_hdr_engine_state(ctx, ticket_id, &email, now);
    refresh_member_hdr_boost_state(ctx, ticket_id, &email, now);
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
    ctx.db
        .ticketremote_member_limit_state()
        .id()
        .delete(member_limit_state_id(ticket_id, email));
    ctx.db
        .ticketremote_member_hdr_state()
        .id()
        .delete(member_hdr_state_id(ticket_id, email));
    ctx.db
        .ticketremote_member_hdr_engine_state()
        .id()
        .delete(member_hdr_engine_state_id(ticket_id, email));
    ctx.db
        .ticketremote_member_hdr_boost_state()
        .id()
        .delete(member_hdr_boost_state_id(ticket_id, email));
    delete_policy_boundary_timers(ctx, ticket_id, "member", &clean_email(email));
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
    purpose: &str,
    activation_revision: &str,
    target: &str,
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
    let requested_purpose = safe_token(purpose, "latest_ticket_reselect");
    let activation_revision = bounded_text(activation_revision, 160);
    let target = if target.trim().is_empty() {
        String::new()
    } else {
        ticket_action_v3_target(target)
    };
    let purpose = match (requested_purpose.as_str(), target.as_str()) {
        ("latest_ticket_reselect", "") => "latest_ticket_reselect".to_string(),
        ("latest_ticket_reselect", "redetect_latest") => {
            "ticket_action_v3_redetect_latest".to_string()
        }
        ("activation_expiry_reset", "")
        | ("activation_expiry_reset", "open_latest_unactivated") => {
            "activation_expiry_reset".to_string()
        }
        _ => return Err("invalid_scheduled_ticket_action_target".into()),
    };
    let phone_local_time = bounded_text(phone_local_time.trim(), 80);
    let phone_time_zone = bounded_text(phone_time_zone.trim(), 80);
    if matches!(
        purpose.as_str(),
        "latest_ticket_reselect" | "ticket_action_v3_redetect_latest"
    ) && (phone_local_time.is_empty() || phone_time_zone.is_empty())
    {
        return Err("phone_local_time_required".into());
    }
    if purpose == "activation_expiry_reset" && activation_revision.is_empty() {
        return Err("activation_revision_required".into());
    }
    let scheduled_at = iso(Timestamp::from_micros_since_unix_epoch(scheduled_at_micros));
    let table = ctx.db.ticketremote_latest_ticket_reselect_schedule();
    if let Some(existing) = table.id().find(schedule_id.to_string()) {
        let activation_expiry_matches = purpose == "activation_expiry_reset"
            && existing.ticketId == ticket.id
            && existing.backendId == backend_id
            && existing.activationRevision.as_deref().unwrap_or("") == activation_revision;
        if activation_expiry_matches {
            if latest_ticket_reselect_idempotent_status(&existing.status) {
                return Ok(());
            }
        }
        if latest_ticket_reselect_submission_matches(
            &existing,
            &ticket.id,
            &backend_id,
            &scheduled_at,
            &phone_local_time,
            &phone_time_zone,
            requested_by,
            &purpose,
            &activation_revision,
        ) {
            return if latest_ticket_reselect_idempotent_status(&existing.status) {
                Ok(())
            } else {
                Err("schedule_id_not_reusable".into())
            };
        }
        return Err("schedule_id_conflict".into());
    }
    validate_latest_ticket_reselect_schedule_time(ctx, scheduled_at_micros)?;
    // Ordinary/admin re-selection and the mandatory activation refresh have
    // independent lifecycles. One must not block, replace, or consume the
    // other while both are pending for the same phone.
    if table
        .ticketBackendStatus()
        .filter((&ticket.id, &backend_id, "queued"))
        .chain(
            table
                .ticketBackendStatus()
                .filter((&ticket.id, &backend_id, "running")),
        )
        .any(|row| {
            scheduled_ticket_purpose_class(row.purpose.as_deref().unwrap_or(""))
                == scheduled_ticket_purpose_class(&purpose)
        })
    {
        return Err("latest_ticket_reselect_already_in_progress".into());
    }

    let pending: Vec<_> = table
        .ticketBackendStatus()
        .filter((&ticket.id, &backend_id, "pending"))
        .collect();
    for existing in pending.into_iter().filter(|row| {
        scheduled_ticket_purpose_class(row.purpose.as_deref().unwrap_or(""))
            == scheduled_ticket_purpose_class(&purpose)
    }) {
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
        purpose: Some(purpose),
        activationRevision: Some(activation_revision),
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
        activationAttemptId: None,
        originalDueAt: Some(scheduled_at.clone()),
        nextRetryAt: None,
        retryAttempt: 0,
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

fn validate_latest_ticket_reselect_schedule_time(
    ctx: &ReducerContext,
    scheduled_at_micros: i64,
) -> Result<(), String> {
    if scheduled_at_micros <= ctx.timestamp.to_micros_since_unix_epoch() {
        return Err("scheduled_time_must_be_future".into());
    }
    if scheduled_at_micros.saturating_sub(ctx.timestamp.to_micros_since_unix_epoch())
        > LATEST_TICKET_RESELECT_MAX_HORIZON_MS.saturating_mul(1000)
    {
        return Err("scheduled_time_too_far".into());
    }
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
    if !latest_ticket_reselect_admin_cancellable(&existing) {
        return Err("schedule_not_manual_redetection".into());
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

fn latest_ticket_reselect_admin_cancellable(
    schedule: &TicketremoteLatestTicketReselectSchedule,
) -> bool {
    schedule.purpose.as_deref() == Some("ticket_action_v3_redetect_latest")
}

fn cancel_queued_activation_refresh_command(
    ctx: &ReducerContext,
    schedule: &TicketremoteLatestTicketReselectSchedule,
    now: &str,
) {
    if schedule.commandId.trim().is_empty() {
        return;
    }
    let table = ctx.db.ticketremote_stream_command();
    let Some(command) = table.id().find(schedule.commandId.clone()) else {
        return;
    };
    if !matches!(command.status.as_str(), "pending" | "queued") {
        return;
    }
    table.id().delete(&command.id);
    upsert_stream_command_signal(
        ctx,
        &command.ticketId,
        &command.backendId,
        &command.revision,
        now,
    );
}

fn cancel_pending_activation_expiry_schedules(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    activation_revision: &str,
    preserve_interaction_revision: Option<&str>,
    now: &str,
) {
    let activation_revision = activation_revision.trim();
    if activation_revision.is_empty() {
        return;
    }
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    let preserve_interaction_revision = preserve_interaction_revision
        .unwrap_or("")
        .trim()
        .to_string();
    let table = ctx.db.ticketremote_latest_ticket_reselect_schedule();
    let mut rows = Vec::new();
    for status in ["pending", "queued", "running"] {
        rows.extend(
            table
                .ticketBackendStatus()
                .filter((&ticket_id, &backend_id, status))
                .filter(|row| {
                    row.purpose.as_deref() == Some("activation_expiry_reset")
                        && row.activationRevision.as_deref() == Some(activation_revision)
                        && format!("schedule:{}", row.id) != preserve_interaction_revision
                }),
        );
    }
    for existing in rows {
        cancel_queued_activation_refresh_command(ctx, &existing, now);
        mark_activation_refresh_terminal(
            ctx,
            &existing.ticketId,
            &existing.backendId,
            existing.activationRevision.as_deref().unwrap_or(""),
            "canceled",
            now,
        );
        delete_latest_ticket_reselect_timers(ctx, &existing.id);
        table.id().update(TicketremoteLatestTicketReselectSchedule {
            status: "canceled".into(),
            resultReason: "activation_reset_completed".into(),
            resultPhase: "canceled".into(),
            proofSource: "phone_worker".into(),
            updatedAt: now.into(),
            completedAt: now.into(),
            expiresAt: add_ms(now, HISTORY_TTL_MS),
            ..existing
        });
    }
}

fn fail_pending_activation_expiry_schedules(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    activation_revision: &str,
    reason: &str,
    now: &str,
) {
    let activation_revision = activation_revision.trim();
    if activation_revision.is_empty() {
        return;
    }
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    let table = ctx.db.ticketremote_latest_ticket_reselect_schedule();
    let mut rows = Vec::new();
    for status in ["pending", "queued", "running"] {
        rows.extend(
            table
                .ticketBackendStatus()
                .filter((&ticket_id, &backend_id, status))
                .filter(|row| {
                    row.purpose.as_deref() == Some("activation_expiry_reset")
                        && row.activationRevision.as_deref() == Some(activation_revision)
                }),
        );
    }
    let result_reason = bounded_text(&non_empty(reason, "activation_refresh_failed"), 240);
    for existing in rows {
        cancel_queued_activation_refresh_command(ctx, &existing, now);
        mark_activation_refresh_terminal(
            ctx,
            &existing.ticketId,
            &existing.backendId,
            existing.activationRevision.as_deref().unwrap_or(""),
            "failed",
            now,
        );
        delete_latest_ticket_reselect_timers(ctx, &existing.id);
        table.id().update(TicketremoteLatestTicketReselectSchedule {
            status: "failed".into(),
            resultReason: result_reason.clone(),
            resultPhase: "failed".into(),
            proofSource: "phone_worker".into(),
            updatedAt: now.into(),
            completedAt: now.into(),
            expiresAt: add_ms(now, HISTORY_TTL_MS),
            ..existing
        });
    }
}

fn trigger_scheduled_latest_ticket_reselect(
    ctx: &ReducerContext,
    timer: &TicketremoteLatestTicketReselectTimer,
) -> Result<(), String> {
    let table = ctx.db.ticketremote_latest_ticket_reselect_schedule();
    let Some(existing) = table.id().find(&timer.scheduleId) else {
        return Ok(());
    };
    if !latest_ticket_reselect_timer_matches_schedule(&existing, timer)
        || !table
            .ticketBackendStatus()
            .filter((&timer.ticketId, &timer.backendId, "pending"))
            .any(|row| row.id == timer.scheduleId)
    {
        return Ok(());
    }
    if existing.purpose.as_deref() == Some("activation_expiry_reset")
        && !activation_refresh_is_current(ctx, &existing)
    {
        let now = now(ctx);
        mark_activation_refresh_terminal(
            ctx,
            &existing.ticketId,
            &existing.backendId,
            existing.activationRevision.as_deref().unwrap_or(""),
            "canceled",
            &now,
        );
        delete_latest_ticket_reselect_timers(ctx, &existing.id);
        table.id().update(TicketremoteLatestTicketReselectSchedule {
            status: "canceled".into(),
            resultReason: "activation_state_replaced".into(),
            resultPhase: "canceled".into(),
            proofSource: "spacetimedb".into(),
            updatedAt: now.clone(),
            completedAt: now.clone(),
            expiresAt: add_ms(&now, HISTORY_TTL_MS),
            ..existing
        });
        return Ok(());
    }
    let now = now(ctx);
    let purpose = existing.purpose.as_deref().unwrap_or("");
    let v3_target = scheduled_ticket_action_v3_target(purpose);
    if purpose == "ticket_action_v3_redetect_latest" {
        if let Some(conflict_reason) =
            ticket_phone_mutation_lane_conflict(ctx, &existing.ticketId, &existing.backendId, &now)
        {
            defer_pending_scheduled_redetect(ctx, existing, conflict_reason, &now);
            return Ok(());
        }
    }
    let command_id = if !v3_target.is_empty() {
        ticket_action_v3_command_id(&existing.ticketId, &existing.backendId, &existing.id)
    } else {
        latest_ticket_reselect_command_id(&existing.ticketId, &existing.backendId, &existing.id)
    };
    let activation_revision = existing.activationRevision.as_deref().unwrap_or("");
    let activation_expiry = purpose == "activation_expiry_reset";
    let command_reason = if activation_expiry {
        "activation_expiry_reset"
    } else {
        "scheduled_latest_ticket_reselect"
    };
    let command_revision = format!("schedule:{}", existing.id);
    if activation_expiry {
        let Some(history) = activation_refresh_history_for_schedule(ctx, &existing) else {
            return Ok(());
        };
        if reconcile_activation_refresh_terminal_action(ctx, &history, &existing, &now) {
            return Ok(());
        }
        let current =
            current_ticket_interaction(ctx, &existing.ticketId, &existing.backendId, &now);
        let Some(preparing) = prepare_activation_refresh_interaction(
            &current,
            &existing,
            &history,
            &command_revision,
            &now,
        ) else {
            mark_activation_refresh_terminal(
                ctx,
                &existing.ticketId,
                &existing.backendId,
                activation_revision,
                "canceled",
                &now,
            );
            delete_latest_ticket_reselect_timers(ctx, &existing.id);
            table.id().update(TicketremoteLatestTicketReselectSchedule {
                status: "canceled".into(),
                resultReason: "activation_state_replaced".into(),
                resultPhase: "canceled".into(),
                proofSource: "spacetimedb_scheduler".into(),
                updatedAt: now.clone(),
                completedAt: now.clone(),
                expiresAt: add_ms(&now, HISTORY_TTL_MS),
                ..existing
            });
            return Ok(());
        };
        // Claim the interaction revision in the same reducer transaction that queues the
        // command. The phone's fresh unregistered proof must be able to publish against this
        // scheduled reset, while an older activated snapshot remains stale.
        upsert_ticket_interaction(ctx, preparing);
    }
    let payload = if !v3_target.is_empty() {
        ticket_action_v3_upsert_pending(
            ctx,
            &existing.ticketId,
            &existing.backendId,
            &existing.id,
            &v3_target,
            command_reason,
            &now,
        );
        let switch_anchor =
            live_ticket_switch_anchor(ctx, &existing.ticketId, &existing.backendId, &now);
        scheduled_ticket_action_v3_payload(
            &existing.id,
            &v3_target,
            command_reason,
            purpose,
            activation_revision,
            existing.activationAttemptId.as_deref().unwrap_or(""),
            switch_anchor
                .as_ref()
                .map(|anchor| anchor.expiresAt.as_str())
                .unwrap_or(""),
            switch_anchor
                .as_ref()
                .map(|anchor| anchor.policyRevision.as_str())
                .unwrap_or(""),
        )
    } else {
        serde_json::json!({
            "type": "force_ticket_reselect",
            "source": "ticket_remote_schedule",
            "flow": purpose,
            "reason": command_reason,
            "backendId": existing.backendId,
            "scheduleId": existing.id,
            "activationRevision": activation_revision,
            "activationAttemptId": existing.activationAttemptId.as_deref().unwrap_or(""),
            "scheduledAt": existing.scheduledAt,
        })
        .to_string()
    };
    let command = insert_stream_command(
        ctx,
        &existing.ticketId,
        &existing.backendId,
        &command_id,
        if !v3_target.is_empty() {
            "ticket_action_v3"
        } else {
            "force_ticket_reselect"
        },
        &command_revision,
        command_reason,
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

fn scheduled_redetect_retry_delay_ms(retry_attempt: u32) -> i64 {
    let multiplier = 1_i64 << retry_attempt.min(4);
    SCHEDULED_REDETECT_RETRY_BASE_MS
        .saturating_mul(multiplier)
        .min(SCHEDULED_REDETECT_RETRY_MAX_MS)
}

fn scheduled_redetect_retry_at_micros(now_micros: i64, retry_attempt: u32) -> i64 {
    now_micros
        .saturating_add(scheduled_redetect_retry_delay_ms(retry_attempt).saturating_mul(1_000))
}

fn scheduled_redetect_deferred_schedule(
    mut schedule: TicketremoteLatestTicketReselectSchedule,
    conflict_reason: &str,
    now: &str,
) -> (TicketremoteLatestTicketReselectSchedule, i64) {
    let retry_at_micros =
        scheduled_redetect_retry_at_micros(parse_time_micros(now), schedule.retryAttempt);
    schedule.status = "pending".into();
    schedule.commandId.clear();
    schedule.resultReason = bounded_text(conflict_reason, 240);
    schedule.resultPhase = "retry_wait".into();
    schedule.proofSource = "spacetimedb_scheduler".into();
    schedule.updatedAt = now.into();
    schedule.triggeredAt.clear();
    schedule.completedAt.clear();
    schedule.nextRetryAt = Some(iso(Timestamp::from_micros_since_unix_epoch(
        retry_at_micros,
    )));
    schedule.retryAttempt = schedule.retryAttempt.saturating_add(1);
    (schedule, retry_at_micros)
}

fn defer_pending_scheduled_redetect(
    ctx: &ReducerContext,
    schedule: TicketremoteLatestTicketReselectSchedule,
    conflict_reason: &str,
    now: &str,
) {
    let (schedule, retry_at_micros) =
        scheduled_redetect_deferred_schedule(schedule, conflict_reason, now);
    let ticket_id = schedule.ticketId.clone();
    let backend_id = schedule.backendId.clone();
    let schedule_id = schedule.id.clone();
    delete_latest_ticket_reselect_timers(ctx, &schedule_id);
    ctx.db
        .ticketremote_latest_ticket_reselect_schedule()
        .id()
        .update(schedule);
    ctx.db.ticketremote_latest_ticket_reselect_timer().insert(
        TicketremoteLatestTicketReselectTimer {
            scheduled_id: 0,
            scheduled_at: ScheduleAt::Time(Timestamp::from_micros_since_unix_epoch(
                retry_at_micros,
            )),
            ticketId: ticket_id,
            backendId: backend_id,
            scheduleId: schedule_id,
            createdAt: now.into(),
        },
    );
}

fn scheduled_ticket_action_v3_payload(
    schedule_id: &str,
    target: &str,
    reason: &str,
    purpose: &str,
    activation_revision: &str,
    activation_attempt_id: &str,
    switch_expires_at: &str,
    policy_revision: &str,
) -> String {
    serde_json::json!({
        "version": 3,
        "actionId": schedule_id,
        "target": target,
        "source": "ticket_remote_schedule",
        "reason": reason,
        "attemptId": "",
        "expectedInteractionRevision": "",
        "scheduleId": schedule_id,
        "flow": if purpose == "activation_expiry_reset" { purpose } else { "" },
        "activationRevision": if purpose == "activation_expiry_reset" { activation_revision } else { "" },
        "activationAttemptId": if purpose == "activation_expiry_reset" { activation_attempt_id } else { "" },
        "switchExpiresAt": switch_expires_at,
        "policyRevision": policy_revision,
    })
    .to_string()
}

fn scheduled_ticket_action_v3_target(purpose: &str) -> String {
    if purpose == "activation_expiry_reset" {
        return "open_latest_unactivated".into();
    }
    if purpose == "ticket_action_v3_redetect_latest" {
        return "redetect_latest".into();
    }
    String::new()
}

fn scheduled_ticket_purpose_class(purpose: &str) -> &str {
    if matches!(
        purpose,
        "latest_ticket_reselect" | "ticket_action_v3_redetect_latest"
    ) {
        "latest_ticket_reselect"
    } else {
        purpose
    }
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
    purpose: &str,
    activation_revision: &str,
) -> bool {
    row.ticketId == ticket_id
        && row.backendId == backend_id
        && row.scheduledAt == scheduled_at
        && row.phoneLocalTime == phone_local_time
        && row.phoneTimeZone == phone_time_zone
        && row.requestedBy == requested_by
        && row.purpose.as_deref().unwrap_or("") == purpose
        && row.activationRevision.as_deref().unwrap_or("") == activation_revision
}

fn latest_ticket_reselect_idempotent_status(status: &str) -> bool {
    !matches!(status, "canceled" | "replaced" | "expired" | "failed")
}

fn latest_ticket_reselect_timer_matches_schedule(
    schedule: &TicketremoteLatestTicketReselectSchedule,
    timer: &TicketremoteLatestTicketReselectTimer,
) -> bool {
    schedule.status == "pending"
        && schedule.ticketId == timer.ticketId
        && schedule.backendId == timer.backendId
        && schedule.id == timer.scheduleId
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

fn ensure_activation_cleanup_schedule(ctx: &ReducerContext, now: &str) {
    let schedule = ScheduleAt::Interval(
        std::time::Duration::from_secs(TICKET_ACTIVATION_CLEANUP_INTERVAL_SECS).into(),
    );
    let table = ctx.db.ticketremote_activation_cleanup_schedule();
    if let Some(existing) = table.iter().next() {
        table
            .scheduled_id()
            .update(TicketremoteActivationCleanupSchedule {
                scheduled_at: schedule,
                updatedAt: now.into(),
                ..existing
            });
    } else {
        table.insert(TicketremoteActivationCleanupSchedule {
            scheduled_id: 0,
            scheduled_at: schedule,
            createdAt: now.into(),
            updatedAt: now.into(),
        });
    }
}

fn scheduled_redetect_recovery_timer_micros(
    schedule: &TicketremoteLatestTicketReselectSchedule,
    now_micros: i64,
) -> i64 {
    let desired_micros = schedule
        .nextRetryAt
        .as_deref()
        .filter(|value| !value.trim().is_empty())
        .map(parse_time_micros)
        .filter(|value| *value > 0)
        .unwrap_or_else(|| parse_time_micros(&schedule.scheduledAt));
    if desired_micros > now_micros {
        desired_micros
    } else {
        scheduled_redetect_retry_at_micros(now_micros, schedule.retryAttempt)
    }
}

fn reconcile_pending_scheduled_redetect_timers(ctx: &ReducerContext, now: &str) {
    let now_micros = parse_time_micros(now);
    let schedules: Vec<_> = ctx
        .db
        .ticketremote_latest_ticket_reselect_schedule()
        .iter()
        .filter(|row| {
            row.purpose.as_deref() == Some("ticket_action_v3_redetect_latest")
                && row.status == "pending"
                && parse_time_micros(&row.expiresAt) > now_micros
        })
        .collect();
    for schedule in schedules {
        if ctx
            .db
            .ticketremote_latest_ticket_reselect_timer()
            .scheduleId()
            .filter(&schedule.id)
            .next()
            .is_some()
        {
            continue;
        }
        let timer_at_micros = scheduled_redetect_recovery_timer_micros(&schedule, now_micros);
        let retry_wait = schedule.retryAttempt > 0
            || schedule.nextRetryAt.is_some()
            || parse_time_micros(&schedule.scheduledAt) <= now_micros;
        let ticket_id = schedule.ticketId.clone();
        let backend_id = schedule.backendId.clone();
        let schedule_id = schedule.id.clone();
        if retry_wait {
            let result_reason = non_empty(&schedule.resultReason, "scheduled_retry_restored");
            ctx.db
                .ticketremote_latest_ticket_reselect_schedule()
                .id()
                .update(TicketremoteLatestTicketReselectSchedule {
                    resultReason: result_reason,
                    resultPhase: "retry_wait".into(),
                    proofSource: "spacetimedb_reconcile".into(),
                    updatedAt: now.into(),
                    nextRetryAt: Some(iso(Timestamp::from_micros_since_unix_epoch(
                        timer_at_micros,
                    ))),
                    ..schedule
                });
        }
        ctx.db.ticketremote_latest_ticket_reselect_timer().insert(
            TicketremoteLatestTicketReselectTimer {
                scheduled_id: 0,
                scheduled_at: ScheduleAt::Time(Timestamp::from_micros_since_unix_epoch(
                    timer_at_micros,
                )),
                ticketId: ticket_id,
                backendId: backend_id,
                scheduleId: schedule_id,
                createdAt: now.into(),
            },
        );
    }
}

fn reconcile_activation_refresh_timers(ctx: &ReducerContext, now: &str) {
    let schedules: Vec<_> = ctx
        .db
        .ticketremote_latest_ticket_reselect_schedule()
        .iter()
        .filter(|row| {
            row.purpose.as_deref() == Some("activation_expiry_reset")
                && matches!(row.status.as_str(), "pending" | "queued" | "running")
        })
        .collect();
    for schedule in schedules {
        let history = activation_history_for_revision(
            ctx,
            &schedule.ticketId,
            &schedule.backendId,
            schedule.activationRevision.as_deref().unwrap_or(""),
        );
        if history.as_ref().is_some_and(|history| {
            reconcile_activation_refresh_terminal_action(ctx, history, &schedule, now)
        }) {
            continue;
        }
        if !activation_refresh_is_current(ctx, &schedule) {
            cancel_queued_activation_refresh_command(ctx, &schedule, now);
            mark_activation_refresh_terminal(
                ctx,
                &schedule.ticketId,
                &schedule.backendId,
                schedule.activationRevision.as_deref().unwrap_or(""),
                "canceled",
                now,
            );
            delete_latest_ticket_reselect_timers(ctx, &schedule.id);
            ctx.db
                .ticketremote_latest_ticket_reselect_schedule()
                .id()
                .update(TicketremoteLatestTicketReselectSchedule {
                    status: "canceled".into(),
                    resultReason: "activation_state_replaced".into(),
                    resultPhase: "canceled".into(),
                    proofSource: "spacetimedb_bootstrap".into(),
                    updatedAt: now.into(),
                    completedAt: now.into(),
                    expiresAt: add_ms(now, HISTORY_TTL_MS),
                    ..schedule
                });
            continue;
        }
        let has_timer = ctx
            .db
            .ticketremote_latest_ticket_reselect_timer()
            .scheduleId()
            .filter(&schedule.id)
            .next()
            .is_some();
        let command_active = !schedule.commandId.trim().is_empty()
            && ctx
                .db
                .ticketremote_stream_command()
                .id()
                .find(schedule.commandId.clone())
                .is_some_and(|command| {
                    matches!(
                        command.status.as_str(),
                        "pending" | "queued" | "dispatched" | "running"
                    )
                });
        if has_timer || command_active {
            continue;
        }
        let failure_reason = "activation_refresh_command_missing";
        // The exact immutable activation history and schedule identity above
        // already proved that this refresh is current. Mutable interaction
        // cleanup is best-effort only: later navigation may legitimately have
        // replaced or cleared that compatibility projection.
        let _ = fail_current_activation_refresh(
            ctx,
            &schedule.ticketId,
            &schedule.backendId,
            schedule.activationRevision.as_deref().unwrap_or(""),
            failure_reason,
            now,
        );
        mark_activation_refresh_terminal(
            ctx,
            &schedule.ticketId,
            &schedule.backendId,
            schedule.activationRevision.as_deref().unwrap_or(""),
            "failed",
            now,
        );
        delete_latest_ticket_reselect_timers(ctx, &schedule.id);
        ctx.db
            .ticketremote_latest_ticket_reselect_schedule()
            .id()
            .update(TicketremoteLatestTicketReselectSchedule {
                status: "failed".into(),
                resultReason: failure_reason.into(),
                resultPhase: "failed".into(),
                proofSource: "spacetimedb_bootstrap".into(),
                updatedAt: now.into(),
                completedAt: now.into(),
                expiresAt: add_ms(now, HISTORY_TTL_MS),
                ..schedule
            });
    }
}

fn restore_state_replaced_activation_refreshes(ctx: &ReducerContext, now: &str) {
    // Service bootstrap is the durable restart boundary. Repair only the narrow historical bug
    // signature, then let the service-only reducer re-check the immutable activation, attempt,
    // exact original deadline, latest-success authority, and schedule identity transactionally.
    let candidates: Vec<_> = ctx
        .db
        .ticketremote_latest_ticket_reselect_schedule()
        .iter()
        .filter(|row| {
            row.purpose.as_deref() == Some("activation_expiry_reset")
                && row.status == "canceled"
                && row.resultReason == "activation_state_replaced"
        })
        .collect();
    for schedule in candidates {
        let activation_revision = schedule.activationRevision.as_deref().unwrap_or("");
        if activation_revision.is_empty() {
            continue;
        }
        let _ = ticketremote_schedule_activation_expiry_reset(
            ctx,
            schedule.ticketId,
            schedule.backendId,
            activation_revision.into(),
            now.into(),
        );
    }
}

fn schedule_activation_cleanup_catchup(ctx: &ReducerContext, now: &str) {
    if ctx
        .db
        .ticketremote_activation_cleanup_catchup()
        .iter()
        .next()
        .is_some()
    {
        return;
    }
    let scheduled_at = Timestamp::from_micros_since_unix_epoch(
        ctx.timestamp
            .to_micros_since_unix_epoch()
            .saturating_add(TICKET_ACTIVATION_CATCHUP_DELAY_SECS as i64 * 1_000_000),
    );
    ctx.db
        .ticketremote_activation_cleanup_catchup()
        .insert(TicketremoteActivationCleanupCatchup {
            scheduled_id: 0,
            scheduled_at: ScheduleAt::Time(scheduled_at),
            createdAt: now.into(),
        });
}

fn cleanup_activation_history(ctx: &ReducerContext, now: &str, batch_size: u32) -> (u32, bool) {
    let bound = canonical_time(now);
    let limit = batch_size.min(TICKET_ACTIVATION_CLEANUP_BATCH_SIZE) as usize;
    if limit == 0 {
        return (0, false);
    }
    let history_rows: Vec<_> = ctx
        .db
        .ticketremote_activation_history()
        .expiresAt()
        .filter(..=bound.as_str())
        .take(limit.saturating_add(1))
        .collect();
    let mut saturated = history_rows.len() > limit;
    let mut deleted = 0usize;
    for row in history_rows.into_iter().take(limit) {
        ctx.db
            .ticketremote_activation_history()
            .id()
            .delete(&row.id);
        deleted += 1;
    }
    if deleted < limit {
        let remaining = limit - deleted;
        let decision_rows: Vec<_> = ctx
            .db
            .ticketremote_activation_decision()
            .iter()
            .filter(|row| parse_time_ms(&row.expiresAt) <= parse_time_ms(&bound))
            .take(remaining.saturating_add(1))
            .collect();
        saturated |= decision_rows.len() > remaining;
        for row in decision_rows.into_iter().take(remaining) {
            ctx.db
                .ticketremote_activation_decision()
                .id()
                .delete(&row.id);
            deleted += 1;
        }
    }
    (deleted.min(u32::MAX as usize) as u32, saturated)
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

fn purge_pending_ticket_slider_commands(
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
        .filter(|row| row.commandType == "slider_control_start")
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

fn fail_ticket_action_v3_for_command(
    ctx: &ReducerContext,
    command: &TicketremoteStreamCommand,
    reason: &str,
    now: &str,
) {
    if command.commandType != "ticket_action_v3" {
        return;
    }
    let payload = serde_json::from_str::<serde_json::Value>(&command.payloadJson)
        .unwrap_or_else(|_| serde_json::json!({}));
    let action_id = payload
        .get("actionId")
        .and_then(|value| value.as_str())
        .unwrap_or("")
        .trim();
    if !valid_schedule_identifier(action_id) {
        return;
    }
    let target = payload
        .get("target")
        .and_then(|value| value.as_str())
        .map(ticket_action_v3_target)
        .unwrap_or_default();
    let (failure_status, failure_phase) = ticket_action_v3_command_failure_projection(&target);
    let id = ticket_action_v3_row_id(&command.ticketId, &command.backendId, action_id);
    let existing_action = ctx.db.ticketremote_ticket_action_v3().id().find(&id);
    let action_status = existing_action.as_ref().map(|row| row.status.clone());
    if let Some(existing) = existing_action {
        if !ticket_action_v3_terminal(&existing.status) {
            ctx.db
                .ticketremote_ticket_action_v3()
                .id()
                .update(TicketremoteTicketActionV3 {
                    status: failure_status.into(),
                    phase: failure_phase.into(),
                    switchAvailable: false,
                    switchExpiresAt: String::new(),
                    reason: ticket_action_v3_public_reason(reason, "ticket_action_failed"),
                    updatedAt: now.into(),
                    completedAt: now.into(),
                    expiresAt: add_ms(now, HISTORY_TTL_MS),
                    ..existing
                });
        }
    }
    if ticket_action_v3_failure_requires_activation_cleanup(&target, action_status.as_deref()) {
        let attempt_id = payload
            .get("attemptId")
            .and_then(|value| value.as_str())
            .unwrap_or("");
        finalize_ticket_activation_failure_impl(
            ctx,
            &command.ticketId,
            &command.backendId,
            attempt_id,
            "failed",
            reason,
            now,
        );
        reconcile_ticket_action_activation_terminal_interaction(ctx, command, reason, now);
    }
}

fn ticket_action_v3_command_failure_projection(target: &str) -> (&'static str, &'static str) {
    if ticket_action_v3_is_activation(target) {
        ("needs_attention", "needs_attention")
    } else {
        ("failed", "failed")
    }
}

fn ticket_action_v3_failure_requires_activation_cleanup(
    target: &str,
    action_status: Option<&str>,
) -> bool {
    ticket_action_v3_is_activation(target) && action_status != Some("succeeded")
}

fn reconcile_ticket_action_activation_terminal_interaction(
    ctx: &ReducerContext,
    command: &TicketremoteStreamCommand,
    reason: &str,
    now: &str,
) {
    let payload = serde_json::from_str::<serde_json::Value>(&command.payloadJson)
        .unwrap_or_else(|_| serde_json::json!({}));
    let action_id = payload
        .get("actionId")
        .and_then(|value| value.as_str())
        .unwrap_or("")
        .trim();
    if !valid_schedule_identifier(action_id) {
        return;
    }
    let action_id = ticket_action_v3_row_id(&command.ticketId, &command.backendId, action_id);
    let Some(mut action) = ctx.db.ticketremote_ticket_action_v3().id().find(action_id) else {
        return;
    };
    if action.reason.trim().is_empty() {
        action.reason = ticket_action_v3_public_reason(reason, "ticket_action_failed");
    }
    let interaction_id = ticket_interaction_id(&command.ticketId, &command.backendId);
    let Some(current) = ctx
        .db
        .ticketremote_ticket_interaction()
        .id()
        .find(&interaction_id)
    else {
        return;
    };
    if let Some(reconciled) = reconcile_legacy_interaction_after_ticket_action_v3(
        &current,
        &action,
        Some(&command.revision),
        now,
    ) {
        upsert_ticket_interaction(ctx, reconciled);
    }
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
        if existing.commandType == "ticket_action_v3" {
            let payload = serde_json::from_str::<serde_json::Value>(&existing.payloadJson)
                .unwrap_or_else(|_| serde_json::json!({}));
            let action_id = payload
                .get("actionId")
                .and_then(|value| value.as_str())
                .unwrap_or("");
            let row_id =
                ticket_action_v3_row_id(&existing.ticketId, &existing.backendId, action_id);
            let terminal = ctx
                .db
                .ticketremote_ticket_action_v3()
                .id()
                .find(&row_id)
                .is_some_and(|row| ticket_action_v3_terminal(&row.status));
            if terminal {
                // A late compatibility publication may have landed after the
                // action's terminal projection. Re-apply exact-revision cleanup
                // before the command row that correlates them is deleted.
                reconcile_ticket_action_activation_terminal_interaction(
                    ctx, &existing, reason, now,
                );
            } else {
                fail_ticket_action_v3_for_command(
                    ctx,
                    &existing,
                    "ticket_action_result_missing",
                    now,
                );
            }
        }
        if existing.commandType == "vivi_reauth" {
            if let Some(attempt) = vivi_reauth_attempt_for_command(ctx, &existing)
                && !vivi_reauth_terminal(&attempt.status)
            {
                let terminal_status = vivi_reauth_interrupted_terminal_status(&attempt.status);
                let _ = finish_vivi_reauth_attempt(
                    ctx,
                    attempt,
                    terminal_status,
                    "complete",
                    "visual_proof_failed",
                    "spacetimedb",
                    "0",
                    "0",
                    now,
                );
            }
        }
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
        promote_ticket_action_v3_queue(ctx, &existing.ticketId, &existing.backendId, now);
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
        } else if ticket_reset_command_is_relevant(&existing.commandType, &existing.payloadJson)
            || matches!(
                existing.commandType.as_str(),
                "ticket_action_v3" | "vivi_reauth"
            )
        {
            // A reset/reselect may still be running on the phone after dispatch. Keep its
            // command addressable until the phone reports a terminal result or the TTL
            // cleanup repairs the matching interaction.
            table.id().update(TicketremoteStreamCommand {
                status: "dispatched".into(),
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
    if matches!(status.as_str(), "failed" | "expired")
        && ticket_reset_command_is_relevant(&existing.commandType, &existing.payloadJson)
    {
        let fallback = if status == "expired" {
            ticket_reset_command_expiry_reason(&existing)
        } else {
            "ticket_reset_proof_failed"
        };
        let repair_reason = ticket_reset_failure_reason(reason, fallback);
        repair_stale_ticket_interaction_after_command(
            ctx,
            &existing,
            now,
            &repair_reason,
            Some(&existing.id),
        );
    }
    if matches!(status.as_str(), "failed" | "expired") {
        fail_ticket_action_v3_for_command(ctx, &existing, reason, now);
    }
    if matches!(status.as_str(), "failed" | "expired") && existing.commandType == "vivi_reauth" {
        if let Some(attempt) = vivi_reauth_attempt_for_command(ctx, &existing) {
            let terminal_reason = if status == "expired" {
                "command_expired"
            } else {
                "internal_failure"
            };
            let terminal_status = vivi_reauth_interrupted_terminal_status(&attempt.status);
            let _ = finish_vivi_reauth_attempt(
                ctx,
                attempt,
                terminal_status,
                "complete",
                terminal_reason,
                "spacetimedb",
                "0",
                "0",
                now,
            );
        }
    }
    if matches!(status.as_str(), "failed" | "expired")
        && matches!(
            existing.commandType.as_str(),
            "reset_ticket_registration" | "slider_control_start"
        )
    {
        let activation_attempt_id =
            ticket_reset_command_payload_value(&existing.payloadJson, "activationAttemptId");
        if !activation_attempt_id.is_empty() {
            finalize_ticket_activation_failure_impl(
                ctx,
                &existing.ticketId,
                &existing.backendId,
                &activation_attempt_id,
                &status,
                reason,
                now,
            );
        }
    }
    if matches!(status.as_str(), "failed" | "expired") && scheduled_reselect.is_some() {
        let result_status = if status == "expired" {
            "expired"
        } else {
            "failed"
        };
        let result_phase = result_status;
        let proof_source = if status == "expired" {
            "spacetimedb_command_ttl"
        } else {
            "phone_worker"
        };
        update_latest_ticket_reselect_result(
            ctx,
            &existing.id,
            result_status,
            &bounded_text(&non_empty(reason, result_status), 240),
            result_phase,
            proof_source,
            now,
            true,
        );
    }
    let row = TicketremoteStreamCommand {
        status: status.clone(),
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
    if matches!(status.as_str(), "failed" | "expired") {
        promote_ticket_action_v3_queue(ctx, &existing.ticketId, &existing.backendId, now);
    }
}

fn ticket_reset_command_is_relevant(command_type: &str, payload_json: &str) -> bool {
    match command_type.trim() {
        "reset_ticket_registration" => true,
        "force_ticket_reselect" => {
            let flow = ticket_reset_command_payload_value(payload_json, "flow");
            flow.is_empty() || flow == "activation_expiry_reset" || flow == "latest_ticket_reselect"
        }
        "ticket_action_v3" => {
            ticket_reset_command_payload_value(payload_json, "target") == "open_latest_unactivated"
        }
        _ => false,
    }
}

fn ticket_reset_command_payload_value(payload_json: &str, key: &str) -> String {
    serde_json::from_str::<serde_json::Value>(payload_json)
        .ok()
        .and_then(|payload| {
            payload
                .get(key)
                .and_then(|value| value.as_str())
                .map(str::trim)
                .filter(|value| !value.is_empty())
                .map(str::to_owned)
        })
        .unwrap_or_default()
}

fn ticket_reset_command_matches_interaction(
    command: &TicketremoteStreamCommand,
    interaction: &TicketremoteTicketInteraction,
) -> bool {
    if command.ticketId != interaction.ticketId || command.backendId != interaction.backendId {
        return false;
    }
    let command_revision = bounded_text(&command.revision, 160);
    if command_revision == interaction.interactionRevision {
        return true;
    }
    match command.commandType.trim() {
        "reset_ticket_registration" => {
            let reset_request_id =
                ticket_reset_command_payload_value(&command.payloadJson, "resetRequestId");
            let correlation_id =
                ticket_reset_command_payload_value(&command.payloadJson, "correlationId");
            (!reset_request_id.is_empty() && reset_request_id == interaction.resetRequestId)
                || (!correlation_id.is_empty() && correlation_id == interaction.resetRequestId)
        }
        "force_ticket_reselect" => {
            let flow = ticket_reset_command_payload_value(&command.payloadJson, "flow");
            if flow == "activation_expiry_reset" {
                let activation_revision =
                    ticket_reset_command_payload_value(&command.payloadJson, "activationRevision");
                !activation_revision.is_empty()
                    && activation_revision == interaction.activationRevision
            } else {
                false
            }
        }
        _ => false,
    }
}

fn ticket_reset_command_is_live(
    command: &TicketremoteStreamCommand,
    interaction: &TicketremoteTicketInteraction,
    now: &str,
    ignored_command_id: Option<&str>,
) -> bool {
    ignored_command_id != Some(command.id.as_str())
        && matches!(
            command.status.as_str(),
            "pending" | "queued" | "dispatched" | "running"
        )
        && parse_time_ms(&command.expiresAt) > parse_time_ms(now)
        && ticket_reset_command_matches_interaction(command, interaction)
}

fn ticket_reset_command_expiry_reason(command: &TicketremoteStreamCommand) -> &'static str {
    if command.commandType == "force_ticket_reselect"
        && ticket_reset_command_payload_value(&command.payloadJson, "flow")
            == "activation_expiry_reset"
    {
        "ticket_reselect_command_expired"
    } else {
        "ticket_reset_command_expired"
    }
}

fn repair_stale_ticket_interaction_after_command(
    ctx: &ReducerContext,
    command: &TicketremoteStreamCommand,
    now: &str,
    reason: &str,
    ignored_command_id: Option<&str>,
) -> bool {
    if !ticket_reset_command_is_relevant(&command.commandType, &command.payloadJson) {
        return false;
    }
    // Activation-expiry refreshes keep the activation revision on the interaction while the
    // scheduled phone proof is in flight. Their failure path must be handled by
    // update_latest_ticket_reselect_result so the activation history is finalized as failed;
    // the generic reset repair would erase that correlation first and turn a real failure into a
    // misleading cancellation.
    if ticket_reset_command_payload_value(&command.payloadJson, "flow") == "activation_expiry_reset"
    {
        return false;
    }
    let interaction_id = ticket_interaction_id(&command.ticketId, &command.backendId);
    let Some(current) = ctx
        .db
        .ticketremote_ticket_interaction()
        .id()
        .find(&interaction_id)
    else {
        return false;
    };
    if !ticket_interaction_is_reset_in_flight(&current.status)
        || !ticket_reset_command_matches_interaction(command, &current)
    {
        return false;
    }
    if ctx
        .db
        .ticketremote_stream_command()
        .iter()
        .any(|candidate| {
            ticket_reset_command_is_live(&candidate, &current, now, ignored_command_id)
        })
    {
        return false;
    }
    let Some(repaired) = repair_ticket_interaction_for_retry(&current, now, reason) else {
        return false;
    };
    upsert_ticket_interaction(ctx, repaired);
    true
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
        let activation_refresh = existing.purpose.as_deref() == Some("activation_expiry_reset");
        if activation_refresh && terminal && status == "succeeded" {
            // A successful phone command is not the success authority for an
            // activation refresh. The phone worker must publish a fresh
            // unregistered proof and call the refresh-finalization reducer.
            table.id().update(TicketremoteLatestTicketReselectSchedule {
                status: "running".into(),
                resultReason: "refresh_command_completed_waiting_proof".into(),
                resultPhase: "waiting_for_proof".into(),
                proofSource: safe_token(proof_source, "phone_worker"),
                updatedAt: now.into(),
                completedAt: String::new(),
                expiresAt: add_ms(now, HISTORY_TTL_MS),
                ..existing
            });
            continue;
        }
        if activation_refresh && terminal && matches!(status, "failed" | "expired") {
            let history = activation_refresh_history_for_schedule(ctx, &existing);
            if activation_refresh_failure_has_history_authority(history.as_ref(), &existing) {
                // This interaction repair is useful for the legacy projection,
                // but it is not the authority for the durable schedule result.
                // A later visual navigation action may already have replaced it.
                let _ = fail_current_activation_refresh(
                    ctx,
                    &existing.ticketId,
                    &existing.backendId,
                    existing.activationRevision.as_deref().unwrap_or(""),
                    result_reason,
                    now,
                );
                mark_activation_refresh_terminal(
                    ctx,
                    &existing.ticketId,
                    &existing.backendId,
                    existing.activationRevision.as_deref().unwrap_or(""),
                    "failed",
                    now,
                );
                table.id().update(TicketremoteLatestTicketReselectSchedule {
                    status: "failed".into(),
                    resultReason: bounded_text(
                        &non_empty(result_reason, "activation_refresh_failed"),
                        240,
                    ),
                    resultPhase: "failed".into(),
                    proofSource: safe_token(proof_source, "phone_worker"),
                    updatedAt: now.into(),
                    completedAt: now.into(),
                    expiresAt: add_ms(now, HISTORY_TTL_MS),
                    ..existing
                });
                continue;
            } else {
                mark_activation_refresh_terminal(
                    ctx,
                    &existing.ticketId,
                    &existing.backendId,
                    existing.activationRevision.as_deref().unwrap_or(""),
                    "canceled",
                    now,
                );
                table.id().update(TicketremoteLatestTicketReselectSchedule {
                    status: "canceled".into(),
                    resultReason: "activation_state_replaced".into(),
                    resultPhase: "canceled".into(),
                    proofSource: "spacetimedb".into(),
                    updatedAt: now.into(),
                    completedAt: now.into(),
                    expiresAt: add_ms(now, HISTORY_TTL_MS),
                    ..existing
                });
                continue;
            }
        }
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

fn activation_refresh_is_current(
    ctx: &ReducerContext,
    schedule: &TicketremoteLatestTicketReselectSchedule,
) -> bool {
    activation_refresh_history_for_schedule(ctx, schedule).is_some()
}

fn prepare_activation_refresh_interaction(
    current: &TicketremoteTicketInteraction,
    schedule: &TicketremoteLatestTicketReselectSchedule,
    history: &TicketremoteActivationHistory,
    interaction_revision: &str,
    now: &str,
) -> Option<TicketremoteTicketInteraction> {
    if !activation_history_authorizes_refresh_schedule(history, schedule) {
        return None;
    }
    if current.status == "preparing" && current.interactionRevision == interaction_revision {
        let mut current = current.clone();
        current.activationRevision = history.activationRevision.clone();
        current.activationAt = history.completedAt.clone();
        current.scheduledResetAt = history.refreshDueAt.clone();
        return Some(current);
    }
    if schedule.id.trim().is_empty()
        || matches!(current.status.as_str(), "control_active" | "completing")
    {
        return None;
    }
    let mut preparing = current.clone();
    preparing.status = "preparing".into();
    preparing.interactionRevision = bounded_text(interaction_revision, 160);
    // Rehydrate only the opaque activation correlation needed by the compatibility proof path.
    // The immutable successful history and exact schedule identity are the authority; later
    // navigation actions are free to replace the mutable interaction before this timer fires.
    preparing.activationRevision = history.activationRevision.clone();
    preparing.activationAt = history.completedAt.clone();
    preparing.scheduledResetAt = history.refreshDueAt.clone();
    preparing.resetRequestId.clear();
    preparing.ownerPublicId.clear();
    preparing.controlId.clear();
    preparing.leasePhase = "none".into();
    preparing.leaseExpiresAt.clear();
    preparing.latestInputSequence = "0".into();
    preparing.latestInputPhase.clear();
    preparing.latestProgress = 0;
    preparing.lastAppliedSequence = "0".into();
    preparing.lastAppliedProgress = 0;
    preparing.reason = "activation_expiry_reset_started".into();
    preparing.updatedAt = now.into();
    preparing.expiresAt = add_ms(now, TICKET_INTERACTION_TTL_MS);
    Some(preparing)
}

fn fail_current_activation_refresh(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    activation_revision: &str,
    reason: &str,
    now: &str,
) -> bool {
    let activation_revision = activation_revision.trim();
    if activation_revision.is_empty() {
        return false;
    }
    let current = current_ticket_interaction(ctx, ticket_id, backend_id, now);
    if !matches!(current.status.as_str(), "activated" | "preparing")
        || current.activationRevision != activation_revision
    {
        return false;
    }
    let mut failed = current;
    clear_current_ticket_activation_state(&mut failed);
    failed.status = "needs_attention".into();
    failed.reason = bounded_text(&non_empty(reason, "activation_refresh_failed"), 200);
    failed.updatedAt = now.into();
    failed.expiresAt = add_ms(now, TICKET_INTERACTION_TTL_MS);
    upsert_ticket_interaction(ctx, failed);
    true
}

fn mark_activation_refresh_terminal(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    activation_revision: &str,
    outcome: &str,
    now: &str,
) {
    if let Some(history) =
        activation_history_for_revision(ctx, ticket_id, backend_id, activation_revision)
    {
        if history.refreshOutcome == "succeeded" {
            return;
        }
        ctx.db
            .ticketremote_activation_history()
            .id()
            .update(TicketremoteActivationHistory {
                refreshOutcome: safe_token(outcome, "failed"),
                refreshCompletedAt: now.into(),
                refreshRetryAt: String::new(),
                updatedAt: now.into(),
                ..history
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
    let mut row = TicketremoteRelayCurrentReport {
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
    // An older or compromised reporter may still call LIVE_OK/DEGRADED
    // "live". Never persist that broad continuity verdict as public authority.
    row.streamVerdict = relay_public_stream_verdict(&row, now);
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

fn ticket_interaction_id(ticket_id: &str, backend_id: &str) -> String {
    phone_row_id(ticket_id, backend_id)
}

fn button_activation_state_is_exact(
    current: &TicketremoteTicketInteraction,
    interaction_revision: &str,
) -> bool {
    current.status == "unactivated_ready"
        && current.interactionRevision == bounded_text(interaction_revision, 160)
}

fn default_ticket_interaction(
    ticket_id: &str,
    backend_id: &str,
    now: &str,
) -> TicketremoteTicketInteraction {
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    let id = ticket_interaction_id(&ticket_id, &backend_id);
    TicketremoteTicketInteraction {
        id,
        ticketId: ticket_id,
        backendId: backend_id,
        status: "needs_attention".into(),
        interactionRevision: now.into(),
        activationRevision: String::new(),
        activationAt: String::new(),
        scheduledResetAt: String::new(),
        resetRequestId: String::new(),
        streamEpoch: "0".into(),
        frameSequence: "0".into(),
        phoneDisplayWidth: 0,
        phoneDisplayHeight: 0,
        sliderLeft: 0,
        sliderTop: 0,
        sliderRight: 0,
        sliderBottom: 0,
        ownerPublicId: String::new(),
        controlId: String::new(),
        leasePhase: "none".into(),
        leaseExpiresAt: String::new(),
        latestInputSequence: "0".into(),
        latestInputPhase: String::new(),
        latestProgress: 0,
        lastAppliedSequence: "0".into(),
        lastAppliedProgress: 0,
        reason: "interaction_not_proved".into(),
        createdAt: now.into(),
        updatedAt: now.into(),
        expiresAt: add_ms(now, TICKET_INTERACTION_TTL_MS),
    }
}

fn ticket_interaction_is_reset_in_flight(status: &str) -> bool {
    matches!(status, "reset_queued" | "preparing")
}

fn clear_current_ticket_activation_state(current: &mut TicketremoteTicketInteraction) {
    current.activationAt.clear();
    current.scheduledResetAt.clear();
    current.activationRevision.clear();
    current.resetRequestId.clear();
    current.streamEpoch.clear();
    current.frameSequence.clear();
    current.phoneDisplayWidth = 0;
    current.phoneDisplayHeight = 0;
    current.sliderLeft = 0;
    current.sliderTop = 0;
    current.sliderRight = 0;
    current.sliderBottom = 0;
    current.ownerPublicId.clear();
    current.controlId.clear();
    current.leasePhase = "none".into();
    current.leaseExpiresAt.clear();
    current.latestInputSequence = "0".into();
    current.latestInputPhase.clear();
    current.latestProgress = 0;
    current.lastAppliedSequence = "0".into();
    current.lastAppliedProgress = 0;
}

fn reconcile_legacy_interaction_after_ticket_action_v3(
    current: &TicketremoteTicketInteraction,
    action: &TicketremoteTicketActionV3,
    command_revision: Option<&str>,
    now: &str,
) -> Option<TicketremoteTicketInteraction> {
    // A successful open/return action with a fresh frame watermark supersedes
    // a stale compatibility failure. Rebase its revision fence without
    // touching the prior activation correlation or one-hour refresh deadline;
    // those are retained here for compatibility and remain authoritative in
    // the private history/schedule tables.
    if ticket_action_v3_registration_proof_row_valid(action, &action.actionId, now)
        && parse_time_micros(&action.updatedAt) >= parse_time_micros(&current.updatedAt)
        && current.status == "needs_attention"
        && current.interactionRevision != action.actionId
    {
        let mut superseded = current.clone();
        superseded.interactionRevision = bounded_text(&action.actionId, 160);
        superseded.resetRequestId.clear();
        superseded.streamEpoch = action.streamEpoch.clone();
        superseded.frameSequence = action.frameSequence.clone();
        superseded.phoneDisplayWidth = 0;
        superseded.phoneDisplayHeight = 0;
        superseded.sliderLeft = 0;
        superseded.sliderTop = 0;
        superseded.sliderRight = 0;
        superseded.sliderBottom = 0;
        superseded.ownerPublicId.clear();
        superseded.controlId.clear();
        superseded.leasePhase = "none".into();
        superseded.leaseExpiresAt.clear();
        superseded.latestInputSequence = "0".into();
        superseded.latestInputPhase.clear();
        superseded.latestProgress = 0;
        superseded.lastAppliedSequence = "0".into();
        superseded.lastAppliedProgress = 0;
        superseded.reason = "ticket_action_v3_visual_proof_current".into();
        superseded.updatedAt = now.into();
        superseded.expiresAt = add_ms(now, TICKET_INTERACTION_TTL_MS);
        return Some(superseded);
    }

    // Registration terminal state may clear only the exact in-flight
    // compatibility revision claimed when this V3 command was inserted. Keep
    // that revision as the fence; never mint a synthetic retry authority.
    let command_revision = command_revision.unwrap_or("").trim();
    if !ticket_action_v3_is_activation(&action.target)
        || !matches!(action.status.as_str(), "failed" | "needs_attention")
        || command_revision.is_empty()
        || current.interactionRevision != command_revision
        || !matches!(
            current.status.as_str(),
            "reset_queued"
                | "preparing"
                | "control_active"
                | "completing"
                | "failed"
                | "needs_attention"
        )
    {
        return None;
    }
    let mut terminal = current.clone();
    terminal.status = action.status.clone();
    terminal.scheduledResetAt.clear();
    terminal.resetRequestId.clear();
    terminal.streamEpoch = "0".into();
    terminal.frameSequence = "0".into();
    terminal.phoneDisplayWidth = 0;
    terminal.phoneDisplayHeight = 0;
    terminal.sliderLeft = 0;
    terminal.sliderTop = 0;
    terminal.sliderRight = 0;
    terminal.sliderBottom = 0;
    terminal.ownerPublicId.clear();
    terminal.controlId.clear();
    terminal.leasePhase = "none".into();
    terminal.leaseExpiresAt.clear();
    terminal.latestInputSequence = "0".into();
    terminal.latestInputPhase.clear();
    terminal.latestProgress = 0;
    terminal.lastAppliedSequence = "0".into();
    terminal.lastAppliedProgress = 0;
    terminal.reason = bounded_text(&action.reason, 200);
    terminal.updatedAt = now.into();
    terminal.expiresAt = add_ms(now, TICKET_INTERACTION_TTL_MS);
    Some(terminal)
}

fn repair_missing_reset_command_interaction(
    ctx: &ReducerContext,
    current: TicketremoteTicketInteraction,
    now: &str,
) -> TicketremoteTicketInteraction {
    if !ticket_interaction_is_reset_in_flight(&current.status)
        || parse_time_ms(now).saturating_sub(parse_time_ms(&current.updatedAt))
            < TICKET_INTERACTION_STALE_RESET_AFTER_MS
    {
        return current;
    }
    let has_live_matching_command = ctx
        .db
        .ticketremote_stream_command()
        .iter()
        .any(|candidate| ticket_reset_command_is_live(&candidate, &current, now, None));
    if has_live_matching_command {
        return current;
    }
    let Some(repaired) =
        repair_ticket_interaction_for_retry(&current, now, "ticket_reset_command_missing")
    else {
        return current;
    };
    upsert_ticket_interaction(ctx, repaired.clone());
    repaired
}

fn ticket_interaction_has_stale_lease(
    interaction: &TicketremoteTicketInteraction,
    now: &str,
) -> bool {
    !matches!(interaction.leasePhase.as_str(), "" | "none")
        && parse_time_ms(&interaction.leaseExpiresAt) <= parse_time_ms(now)
}

fn repair_expired_ticket_slider_lease_for_mutation(
    interaction: &TicketremoteTicketInteraction,
    now: &str,
) -> Option<TicketremoteTicketInteraction> {
    if interaction.status != "control_active"
        || interaction.leasePhase != "active"
        || !ticket_interaction_has_stale_lease(interaction, now)
    {
        return None;
    }
    let mut repaired = interaction.clone();
    repaired.status = "unactivated_ready".into();
    repaired.ownerPublicId.clear();
    repaired.controlId.clear();
    repaired.leasePhase = "none".into();
    repaired.leaseExpiresAt.clear();
    repaired.latestInputSequence = "0".into();
    repaired.latestInputPhase.clear();
    repaired.latestProgress = 0;
    repaired.lastAppliedSequence = "0".into();
    repaired.lastAppliedProgress = 0;
    repaired.reason = "slider_lease_expired".into();
    repaired.updatedAt = now.into();
    repaired.expiresAt = add_ms(now, TICKET_INTERACTION_TTL_MS);
    Some(repaired)
}

fn ticket_interaction_blocks_control_code(interaction: &TicketremoteTicketInteraction) -> bool {
    matches!(
        interaction.status.as_str(),
        "reset_queued" | "preparing" | "control_active" | "completing"
    ) || interaction.leasePhase == "active"
}

fn ticket_interaction_blocks_ticket_reset(
    interaction: &TicketremoteTicketInteraction,
    now: &str,
) -> bool {
    let lease_active = interaction.leasePhase == "active"
        && parse_time_ms(&interaction.leaseExpiresAt) > parse_time_ms(now);
    let cooldown_active = interaction.leasePhase == "cooldown"
        && parse_time_ms(&interaction.leaseExpiresAt) > parse_time_ms(now);
    let stale_lease = ticket_interaction_has_stale_lease(interaction, now);
    lease_active
        || cooldown_active
        || interaction.status == "completing"
        || (interaction.status == "control_active" && !stale_lease)
}

fn ticket_interaction_failure_matches_current(
    current: &TicketremoteTicketInteraction,
    incoming_status: &str,
    incoming_revision: &str,
) -> bool {
    matches!(incoming_status, "failed" | "needs_attention")
        && ticket_interaction_is_reset_in_flight(&current.status)
        && !incoming_revision.trim().is_empty()
        && incoming_revision == current.interactionRevision
}

fn ticket_interaction_is_fenced_by_terminal_composite_v3(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    interaction_revision: &str,
) -> bool {
    if !valid_schedule_identifier(interaction_revision) {
        return false;
    }
    let row_id = ticket_action_v3_row_id(ticket_id, backend_id, interaction_revision);
    ctx.db
        .ticketremote_ticket_action_v3()
        .id()
        .find(row_id)
        .is_some_and(|action| {
            ticket_action_v3_terminal_composite_fences_interaction(
                &action,
                ticket_id,
                backend_id,
                interaction_revision,
            )
        })
}

fn ticket_action_v3_terminal_composite_fences_interaction(
    action: &TicketremoteTicketActionV3,
    ticket_id: &str,
    backend_id: &str,
    interaction_revision: &str,
) -> bool {
    action.ticketId == ticket_id
        && action.backendId == backend_id
        && action.actionId == interaction_revision
        && action.target == "open_latest_and_register"
        && ticket_action_v3_terminal(&action.status)
}

fn ticket_interaction_has_retained_v3_activation_command(
    ctx: &ReducerContext,
    current: &TicketremoteTicketInteraction,
) -> bool {
    ctx.db
        .ticketremote_stream_command()
        .iter()
        .any(|command| ticket_interaction_v3_activation_command_matches(&command, current))
}

fn ticket_interaction_v3_activation_command_matches(
    command: &TicketremoteStreamCommand,
    current: &TicketremoteTicketInteraction,
) -> bool {
    command.ticketId == current.ticketId
        && command.backendId == current.backendId
        && command.commandType == "ticket_action_v3"
        && command.revision == current.interactionRevision
        && matches!(command.status.as_str(), "pending" | "queued" | "dispatched")
        && ticket_action_v3_is_activation(&ticket_reset_command_payload_value(
            &command.payloadJson,
            "target",
        ))
}

fn ticket_interaction_input_update_is_older(
    current: &TicketremoteTicketInteraction,
    incoming_revision: &str,
    incoming_sequence: &str,
) -> bool {
    !current.interactionRevision.trim().is_empty()
        && incoming_revision == current.interactionRevision
        && compare_ordinal(&current.latestInputSequence, incoming_sequence) > 0
}

fn ticket_interaction_last_applied_is_current_or_newer(
    current_sequence: &str,
    incoming_sequence: &str,
) -> bool {
    compare_ordinal(
        &bounded_frame_ordinal(incoming_sequence),
        &bounded_frame_ordinal(current_sequence),
    ) >= 0
}

fn ticket_interaction_update_is_stale(
    current: &TicketremoteTicketInteraction,
    incoming_status: &str,
    incoming_revision: &str,
) -> bool {
    current.status == "needs_attention"
        && !incoming_revision.trim().is_empty()
        && incoming_revision != current.interactionRevision
        && matches!(
            incoming_status,
            "activated"
                | "reset_queued"
                | "preparing"
                | "unactivated_ready"
                | "control_active"
                | "completing"
                | "needs_attention"
                | "failed"
        )
}

fn ticket_interaction_revision_is_stale(
    current: &TicketremoteTicketInteraction,
    incoming_status: &str,
    incoming_revision: &str,
) -> bool {
    if incoming_revision.trim().is_empty()
        || current.interactionRevision.trim().is_empty()
        || incoming_revision == current.interactionRevision
    {
        return false;
    }
    // Once the server has admitted a reset, slider lease, activation, or refresh transition,
    // an older phone snapshot may not publish any status back onto the same row. A new proof is
    // accepted only after the current command has established its own revision.
    matches!(
        current.status.as_str(),
        "reset_queued" | "preparing" | "control_active" | "completing" | "activated"
    ) && matches!(
        incoming_status,
        "activated"
            | "reset_queued"
            | "preparing"
            | "unactivated_ready"
            | "control_active"
            | "completing"
            | "needs_attention"
            | "failed"
    )
}

fn ticket_reset_failure_reason(reason: &str, fallback: &str) -> String {
    bounded_text(&non_empty(reason, fallback), 200)
}

fn repair_ticket_interaction_for_retry(
    current: &TicketremoteTicketInteraction,
    now: &str,
    reason: &str,
) -> Option<TicketremoteTicketInteraction> {
    if !ticket_interaction_is_reset_in_flight(&current.status) {
        return None;
    }
    let mut repaired = current.clone();
    repaired.status = "needs_attention".into();
    repaired.interactionRevision = bounded_text(
        &interaction_revision(
            &current.ticketId,
            &current.backendId,
            &format!("ticket_reset_retry:{}:{}", current.interactionRevision, now),
        ),
        160,
    );
    repaired.scheduledResetAt.clear();
    repaired.resetRequestId.clear();
    repaired.activationRevision.clear();
    repaired.activationAt.clear();
    repaired.ownerPublicId.clear();
    repaired.controlId.clear();
    repaired.leasePhase = "none".into();
    repaired.leaseExpiresAt.clear();
    repaired.latestInputSequence = "0".into();
    repaired.latestInputPhase.clear();
    repaired.latestProgress = 0;
    repaired.lastAppliedSequence = "0".into();
    repaired.lastAppliedProgress = 0;
    repaired.reason = ticket_reset_failure_reason(reason, "ticket_reset_retry_required");
    repaired.updatedAt = now.into();
    repaired.expiresAt = add_ms(now, TICKET_INTERACTION_TTL_MS);
    Some(repaired)
}

fn upsert_ticket_interaction(
    ctx: &ReducerContext,
    mut row: TicketremoteTicketInteraction,
) -> TicketremoteTicketInteraction {
    row.ticketId = clean_ticket_id(&row.ticketId);
    row.backendId = clean_backend_id(&row.backendId);
    row.id = ticket_interaction_id(&row.ticketId, &row.backendId);
    row.status = safe_token(&row.status, "needs_attention");
    row.leasePhase = allowlisted(&row.leasePhase, &["none", "active", "cooldown"], "none");
    row.latestInputPhase = allowlisted(
        &row.latestInputPhase,
        &["move", "heartbeat", "up", "cancel", ""],
        "",
    );
    row.latestProgress = row.latestProgress.min(10_000);
    row.lastAppliedProgress = row.lastAppliedProgress.min(10_000);
    let table = ctx.db.ticketremote_ticket_interaction();
    if let Some(existing) = table.id().find(&row.id) {
        if same_fields!(existing, row;
            status, interactionRevision, activationRevision, activationAt, scheduledResetAt,
            resetRequestId, streamEpoch, frameSequence, phoneDisplayWidth, phoneDisplayHeight,
            sliderLeft, sliderTop, sliderRight, sliderBottom, ownerPublicId, controlId,
            leasePhase, leaseExpiresAt, latestInputSequence, latestInputPhase, latestProgress,
            lastAppliedSequence, lastAppliedProgress, reason
        ) && parse_time_ms(&existing.expiresAt).saturating_sub(parse_time_ms(&row.updatedAt))
            > TICKET_INTERACTION_TTL_MS / 2
        {
            return existing;
        }
        table.id().update(row.clone());
    } else {
        table.insert(row.clone());
    }
    row
}

fn current_ticket_interaction(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    now: &str,
) -> TicketremoteTicketInteraction {
    let id = ticket_interaction_id(ticket_id, backend_id);
    let Some(existing) = ctx.db.ticketremote_ticket_interaction().id().find(&id) else {
        let row = default_ticket_interaction(ticket_id, backend_id, now);
        ctx.db.ticketremote_ticket_interaction().insert(row.clone());
        return row;
    };
    // This row is short-lived execution state, not activation authority. In particular, a
    // later visual navigation failure may legitimately put it in needs_attention while the
    // immutable successful activation and its one-hour refresh remain valid. Reconciliation
    // is therefore driven by activation_history + the durable schedule, never by a read of
    // this mutable projection.
    existing
}

fn interaction_revision(ticket_id: &str, backend_id: &str, stamp: &str) -> String {
    format!(
        "{}:{}:{}",
        clean_ticket_id(ticket_id),
        clean_backend_id(backend_id),
        stable_stamp(stamp)
    )
}

fn ticket_has_control_code_request_in_progress(
    ctx: &ReducerContext,
    ticket_id: &str,
    now: &str,
) -> bool {
    ticket_has_control_code_request_in_progress_except(ctx, ticket_id, "", now)
}

fn ticket_has_control_code_request_in_progress_except(
    ctx: &ReducerContext,
    ticket_id: &str,
    ignored_request_id: &str,
    now: &str,
) -> bool {
    let ticket_id = clean_ticket_id(ticket_id);
    let ignored_request_id = ignored_request_id.trim();
    ctx.db
        .ticketremote_control_code_request()
        .ticketId()
        .filter(&ticket_id)
        .any(|row| row.id != ignored_request_id && control_code_request_occupies_phone(&row, now))
}

fn ticket_has_ticket_registration_reset_in_progress(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    now: &str,
) -> bool {
    let ticket_id = clean_ticket_id(ticket_id);
    let backend_id = clean_backend_id(backend_id);
    let now_ms = parse_time_ms(now);
    ctx.db
        .ticketremote_stream_command()
        .ticketBackendStatus()
        .filter((&ticket_id, &backend_id, "pending"))
        .chain(
            ctx.db
                .ticketremote_stream_command()
                .ticketBackendStatus()
                .filter((&ticket_id, &backend_id, "queued")),
        )
        .chain(
            ctx.db
                .ticketremote_stream_command()
                .ticketBackendStatus()
                .filter((&ticket_id, &backend_id, "dispatched")),
        )
        .chain(
            ctx.db
                .ticketremote_stream_command()
                .ticketBackendStatus()
                .filter((&ticket_id, &backend_id, "running")),
        )
        .any(|row| ticket_registration_reset_command_is_live(&row, now_ms))
}

fn ticket_registration_reset_command_is_live(
    command: &TicketremoteStreamCommand,
    now_ms: i64,
) -> bool {
    matches!(
        command.status.as_str(),
        "pending" | "queued" | "dispatched" | "running"
    ) && parse_time_ms(&command.expiresAt) > now_ms
        && (command.commandType == "reset_ticket_registration"
            || (command.commandType == "force_ticket_reselect"
                && command.payloadJson.contains("activation_expiry_reset")))
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
    deleted += command::purge_command_receipts(ctx, &ticket.id, expiry_bound.as_str(),
        cleanup_remaining(limit, deleted));
    deleted
}

fn purge_expired_stream_viewer_focus_for_ticket(
    ctx: &ReducerContext,
    ticket_id: &str,
    now: &str,
    batch_size: u32,
) -> u32 {
    let ticket_id = clean_ticket_id(ticket_id);
    let expiry = canonical_time(now);
    let table = ctx.db.ticketremote_stream_viewer_focus();
    let rows: Vec<_> = table
        .ticketExpiresAt()
        .filter((&ticket_id, ..=expiry.as_str()))
        .take(batch_size as usize)
        .collect();
    for row in &rows {
        table.id().delete(&row.id);
    }
    rows.len().min(u32::MAX as usize) as u32
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
        if row.commandType == "ticket_action_v3" {
            fail_ticket_action_v3_for_command(ctx, row, "command_expired", now);
        }
        if row.commandType == "vivi_reauth" {
            if let Some(attempt) = vivi_reauth_attempt_for_command(ctx, row) {
                let terminal_status = vivi_reauth_interrupted_terminal_status(&attempt.status);
                let _ = finish_vivi_reauth_attempt(
                    ctx,
                    attempt,
                    terminal_status,
                    "complete",
                    "command_expired",
                    "spacetimedb",
                    "0",
                    "0",
                    now,
                );
            }
        }
        if matches!(
            row.commandType.as_str(),
            "reset_ticket_registration" | "slider_control_start"
        ) {
            let activation_attempt_id =
                ticket_reset_command_payload_value(&row.payloadJson, "activationAttemptId");
            if !activation_attempt_id.is_empty() {
                finalize_ticket_activation_failure_impl(
                    ctx,
                    &row.ticketId,
                    &row.backendId,
                    &activation_attempt_id,
                    "expired",
                    "ticket_activation_command_expired",
                    now,
                );
            }
        }
        if ticket_reset_command_is_relevant(&row.commandType, &row.payloadJson) {
            let reason = ticket_reset_command_expiry_reason(row);
            repair_stale_ticket_interaction_after_command(ctx, row, now, reason, None);
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
    for backend_id in &touched {
        promote_ticket_action_v3_queue(ctx, &ticket_id, backend_id, now);
    }
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

#[cfg(test)]
mod tests {
    use super::*;

    fn relay_current_report(last_frame_at: Option<&str>) -> TicketremoteRelayCurrentReport {
        TicketremoteRelayCurrentReport {
            id: "vivi-default:pixel".into(),
            ticketId: "vivi-default".into(),
            backendId: "pixel".into(),
            videoClients: 1,
            streamVerdict: "live".into(),
            lastFrameAgoMillis: 0,
            framesForwarded: "10".into(),
            statusJson: serde_json::json!({
                "live": true,
                "freshnessState": "LIVE_FRESH",
                "activeVideoClients": 1,
                "lastFrameVisualAgeKnown": true,
                "lastFrameVisualAgeMillis": 0,
                "phoneClockBoundedCalibrated": true,
                "frameEnvelope": "tsf3",
                "liveFrameMaxAgeMillis": 2_500,
            })
            .to_string(),
            updatedAt: "2026-09-04T12:00:10Z".into(),
            lastFrameAt: last_frame_at.map(str::to_string),
        }
    }

    #[test]
    fn relay_background_suppression_uses_the_durable_frame_timestamp() {
        let now = "2026-09-04T12:00:10Z";

        let fresh = relay_current_report(Some("2026-09-04T12:00:09Z"));
        assert!(relay_current_report_is_live_for_background_suppression(
            &fresh, now
        ));

        let stale = relay_current_report(Some("2026-09-04T12:00:00Z"));
        assert!(!relay_current_report_is_live_for_background_suppression(
            &stale, now
        ));
    }

    #[test]
    fn relay_background_suppression_requires_conservative_live_fresh() {
        let now = "2026-09-04T12:00:10Z";
        for status_patch in [
            serde_json::json!({"freshnessState": "LIVE_OK"}),
            serde_json::json!({"freshnessState": "DEGRADED"}),
            serde_json::json!({"lastFrameVisualAgeKnown": false}),
            serde_json::json!({"phoneClockBoundedCalibrated": false}),
            serde_json::json!({"frameEnvelope": "tsf2"}),
        ] {
            let mut report = relay_current_report(Some("2026-09-04T12:00:09Z"));
            let mut status: serde_json::Value =
                serde_json::from_str(&report.statusJson).expect("valid relay status fixture");
            status
                .as_object_mut()
                .expect("object relay status fixture")
                .extend(status_patch.as_object().expect("object patch").clone());
            report.statusJson = status.to_string();
            assert!(!relay_current_report_is_live_for_background_suppression(
                &report, now
            ));
            assert_eq!(
                relay_public_stream_verdict(&report, now),
                "stale_recovering"
            );
        }

        let fresh = relay_current_report(Some("2026-09-04T12:00:09Z"));
        assert_eq!(relay_public_stream_verdict(&fresh, now), "live");
    }

    #[test]
    fn relay_live_ok_continuity_remains_durable_but_never_authoritative() {
        let now = "2026-09-04T12:00:10Z";
        let mut report = relay_current_report(Some("2026-09-04T12:00:09Z"));
        report.statusJson = serde_json::json!({
            "streamVerdict": "stale_recovering",
            "live": false,
            "continuity": true,
            "freshnessState": "LIVE_OK",
            "fps": 1,
            "sourceFps": 1,
            "keyframeIntervalFrames": 1,
            "frameEnvelope": "tsf3",
            "allIntraConfigValid": true,
            "streamEpoch": 7,
            "lastFrameSequence": 42,
            "lastFrameVisualAgeKnown": true,
            "lastFrameVisualAgeMillis": 1_700,
            "liveOKMaxAgeMillis": 2_000,
            "phoneConnected": true,
            "phoneDesired": true,
            "phoneStreamState": "streaming",
            "activeVideoClients": 1,
            "phoneClockBoundedCalibrated": true,
            "liveFrameMaxAgeMillis": 3_000,
        })
        .to_string();

        assert!(!relay_current_report_is_live_for_background_suppression(
            &report, now
        ));
        assert_eq!(
            relay_public_stream_verdict(&report, now),
            "stale_recovering"
        );

        let durable: serde_json::Value =
            serde_json::from_str(&report.statusJson).expect("valid durable relay status");
        for (key, want) in [
            ("continuity", serde_json::json!(true)),
            ("freshnessState", serde_json::json!("LIVE_OK")),
            ("streamEpoch", serde_json::json!(7)),
            ("lastFrameSequence", serde_json::json!(42)),
            ("lastFrameVisualAgeMillis", serde_json::json!(1_700)),
            ("phoneConnected", serde_json::json!(true)),
            ("phoneDesired", serde_json::json!(true)),
            ("phoneStreamState", serde_json::json!("streaming")),
        ] {
            assert_eq!(durable.get(key), Some(&want), "durable field {key}");
        }
    }

    #[test]
    fn background_stream_suppression_has_one_fresh_relay_authority() {
        let source = include_str!("lib.rs");
        let helper = source
            .split("fn live_relay_suppresses_background_stream_command(")
            .nth(1)
            .and_then(|body| body.split("fn stream_command_is_requester_scoped(").next())
            .expect("live relay suppression helper");
        assert!(helper.contains("relay_current_report_suppresses_background_stream_command("));
        assert!(!helper.contains("phone_current_report"));
        let retired_phone_fallback = [
            "fn phone_current_report_",
            "suppresses_background_stream_command(",
        ]
        .concat();
        assert!(!source.contains(&retired_phone_fallback));
    }

    #[test]
    fn relay_background_suppression_fails_closed_without_a_valid_frame_timestamp() {
        let now = "2026-09-04T12:00:10Z";
        for last_frame_at in [None, Some("not-a-time"), Some("2026-09-04T12:00:11Z")] {
            let report = relay_current_report(last_frame_at);
            assert!(!relay_current_report_is_live_for_background_suppression(
                &report, now
            ));
        }
    }

    #[test]
    fn relay_background_suppression_never_exceeds_the_server_owned_age_limit() {
        let now = "2026-09-04T12:00:10Z";
        let mut report = relay_current_report(Some("2026-09-04T12:00:06.999Z"));
        report.statusJson = serde_json::json!({
            "live": true,
            "freshnessState": "LIVE_FRESH",
            "activeVideoClients": 1,
            "lastFrameVisualAgeKnown": true,
            "lastFrameVisualAgeMillis": 0,
            "phoneClockBoundedCalibrated": true,
            "frameEnvelope": "tsf3",
            "liveFrameMaxAgeMillis": 9_999_999,
        })
        .to_string();
        assert!(!relay_current_report_is_live_for_background_suppression(
            &report, now
        ));

        report.lastFrameAt = Some("2026-09-04T12:00:07Z".into());
        assert!(relay_current_report_is_live_for_background_suppression(
            &report, now
        ));
    }

    #[test]
    fn relay_background_suppression_accepts_three_second_pictures_only() {
        let now = "2026-09-04T12:00:10Z";
        for advertised in [None, Some(3_000), Some(9_999)] {
            for age in [1_251, 2_500, 3_000, 3_001] {
                let mut report = relay_current_report(Some("2026-09-04T12:00:07Z"));
                let mut status: serde_json::Value = serde_json::from_str(&report.statusJson).unwrap();
                status["lastFrameVisualAgeMillis"] = serde_json::json!(age);
                if let Some(limit) = advertised {
                    status["liveFrameMaxAgeMillis"] = serde_json::json!(limit);
                } else {
                    status.as_object_mut().unwrap().remove("liveFrameMaxAgeMillis");
                }
                report.statusJson = status.to_string();
                assert_eq!(
                    relay_current_report_is_live_for_background_suppression(&report, now),
                    age <= 3_000,
                    "age={age}, advertised={advertised:?}"
                );
            }
        }
    }

    #[test]
    fn relay_background_suppression_rejects_invalid_advertised_age_limits() {
        let now = "2026-09-04T12:00:10Z";
        for max_age in [serde_json::json!(-1), serde_json::json!("not-a-number")] {
            let mut report = relay_current_report(Some("2026-09-04T12:00:09Z"));
            report.statusJson = serde_json::json!({
                "live": true,
                "freshnessState": "LIVE_FRESH",
                "activeVideoClients": 1,
                "lastFrameVisualAgeKnown": true,
                "lastFrameVisualAgeMillis": 0,
                "phoneClockBoundedCalibrated": true,
                "frameEnvelope": "tsf3",
                "liveFrameMaxAgeMillis": max_age,
            })
            .to_string();
            assert!(!relay_current_report_is_live_for_background_suppression(
                &report, now
            ));
        }

        let mut missing = relay_current_report(Some("2026-09-04T12:00:09Z"));
        missing.statusJson = serde_json::json!({
            "live": true,
            "freshnessState": "LIVE_FRESH",
            "activeVideoClients": 1,
            "lastFrameVisualAgeKnown": true,
            "lastFrameVisualAgeMillis": 0,
            "phoneClockBoundedCalibrated": true,
            "frameEnvelope": "tsf3",
        })
        .to_string();
        assert!(relay_current_report_is_live_for_background_suppression(
            &missing, now
        ));
    }

    fn pending_atomic_ticket_action(target: &str) -> TicketremoteTicketActionV3 {
        TicketremoteTicketActionV3 {
            id: ticket_action_v3_row_id("vivi-default", "pixel", "action-1"),
            actionId: "action-1".into(),
            ticketId: "vivi-default".into(),
            backendId: "pixel".into(),
            target: target.into(),
            status: "running".into(),
            phase: "dispatching".into(),
            currentView: "latest_unactivated".into(),
            switchAvailable: false,
            switchExpiresAt: String::new(),
            streamEpoch: "41".into(),
            frameSequence: "51".into(),
            reason: "ticket_action_v3_running".into(),
            createdAt: "2026-09-03T10:00:00Z".into(),
            updatedAt: "2026-09-03T10:00:01Z".into(),
            completedAt: String::new(),
            expiresAt: "2026-09-03T16:00:00Z".into(),
            parentActionId: None,
            rootActionId: Some("action-1".into()),
            retryOrdinal: 0,
        }
    }

    fn atomic_ticket_action_command(target: &str, revision: &str) -> TicketremoteStreamCommand {
        let activation = ticket_action_v3_is_activation(target);
        TicketremoteStreamCommand {
            id: ticket_action_v3_command_id("vivi-default", "pixel", "action-1"),
            ticketId: "vivi-default".into(),
            backendId: "pixel".into(),
            commandType: "ticket_action_v3".into(),
            status: "dispatched".into(),
            revision: revision.into(),
            reason: "ticket_action_requested".into(),
            payloadJson: serde_json::json!({
                "version": 3,
                "actionId": "action-1",
                "target": target,
                "source": "browser_slider",
                "reason": "ticket_action_requested",
                "attemptId": if activation { "action-1" } else { "" },
                "expectedInteractionRevision": if target == "register_current" { revision } else { "" },
                "scheduleId": "",
            })
            .to_string(),
            createdAt: "2026-09-03T10:00:00Z".into(),
            updatedAt: "2026-09-03T10:00:01Z".into(),
            expiresAt: "2026-09-03T10:10:00Z".into(),
        }
    }

    fn atomic_terminal_facts(
        target: &str,
        status: &str,
    ) -> Result<TicketActionV3TerminalFacts, String> {
        ticket_action_v3_terminal_facts(
            target,
            status,
            if status == "succeeded" {
                "activation_proven"
            } else {
                "no_transition"
            },
            if status == "succeeded" {
                "activated_current"
            } else {
                "latest_unactivated"
            },
            "42",
            "52",
            if status == "succeeded" {
                "ticket_action_registered"
            } else {
                "ticket_action_gesture_completed_no_transition"
            },
            "2026-09-03T10:00:05Z",
            "action-1",
            "proof-1",
            if status == "succeeded" {
                "activation-1"
            } else {
                ""
            },
            "2026-09-03T10:00:06Z",
        )
    }

    #[test]
    fn atomic_ticket_action_terminal_contract_fails_closed() {
        let succeeded = atomic_terminal_facts("register_current", "succeeded").unwrap();
        assert_eq!(succeeded.current_view, "activated_current");
        assert_eq!(succeeded.activation_revision, "activation-1");

        let retry_not_dispatched = ticket_action_v3_terminal_facts(
            "register_current",
            "needs_attention",
            "retry_not_dispatched",
            "latest_unactivated",
            "42",
            "52",
            "ticket_action_gesture_completed_no_transition",
            "2026-09-03T10:00:05Z",
            "action-1",
            "proof-1",
            "",
            "2026-09-03T10:00:06Z",
        )
        .unwrap();
        assert_eq!(retry_not_dispatched.phase, "retry_not_dispatched");

        assert!(
            ticket_action_v3_terminal_facts(
                "register_current",
                "needs_attention",
                "retry_not_dispatched",
                "latest_unactivated",
                "0",
                "52",
                "ticket_action_retry_not_dispatched",
                "2026-09-03T10:00:05Z",
                "action-1",
                "proof-1",
                "",
                "2026-09-03T10:00:06Z",
            ).is_ok()
        );
        assert_eq!(
            ticket_action_v3_terminal_facts(
                "register_current",
                "failed",
                "activation_proven",
                "activated_current",
                "42",
                "52",
                "ticket_action_registered",
                "2026-09-03T10:00:05Z",
                "action-1",
                "proof-1",
                "activation-1",
                "2026-09-03T10:00:06Z",
            ),
            Err("ticket_action_terminal_phase_result_mismatch".into())
        );

        assert!(
            ticket_action_v3_terminal_facts(
                "register_current",
                "succeeded",
                "activation_proven",
                "activated_current",
                "0",
                "52",
                "ticket_action_registered",
                "2026-09-03T10:00:05Z",
                "action-1",
                "proof-1",
                "activation-1",
                "2026-09-03T10:00:06Z",
            ).is_ok()
        );
        assert_eq!(
            ticket_action_v3_terminal_facts(
                "register_current",
                "needs_attention",
                "no_transition",
                "latest_unactivated",
                "42",
                "52",
                "ticket_action_gesture_completed_no_transition",
                "2026-09-03T10:00:05Z",
                "action-1",
                "proof-1",
                "unproved-revision",
                "2026-09-03T10:00:06Z",
            ),
            Err("ticket_action_unproved_activation_revision".into())
        );
        assert_eq!(
            ticket_action_v3_terminal_facts(
                "register_current",
                "needs_attention",
                "no_transition",
                "latest_unactivated",
                "42",
                "52",
                "untrusted_reason",
                "2026-09-03T10:00:05Z",
                "action-1",
                "proof-1",
                "",
                "2026-09-03T10:00:06Z",
            )
            .unwrap()
            .reason,
            "ticket_action_v3_failed"
        );
    }

    #[test]
    fn atomic_ticket_action_command_requires_exact_root_correlation() {
        let action = pending_atomic_ticket_action("register_current");
        let facts = atomic_terminal_facts("register_current", "succeeded").unwrap();
        let command = atomic_ticket_action_command("register_current", "proof-1");
        assert_eq!(
            ticket_action_v3_command_matches_finalization(&command, &action, &facts),
            Ok(true)
        );

        let mut conflicting = command.clone();
        conflicting.revision = "other-proof".into();
        assert_eq!(
            ticket_action_v3_command_matches_finalization(&conflicting, &action, &facts),
            Ok(false)
        );
        conflicting = command.clone();
        conflicting.payloadJson = conflicting.payloadJson.replace(
            "\"attemptId\":\"action-1\"",
            "\"attemptId\":\"other-action\"",
        );
        assert_eq!(
            ticket_action_v3_command_matches_finalization(&conflicting, &action, &facts),
            Ok(false)
        );
        conflicting = command.clone();
        conflicting.payloadJson = conflicting
            .payloadJson
            .replace("\"scheduleId\":\"\"", "\"scheduleId\":\"other-action\"");
        assert_eq!(
            ticket_action_v3_command_matches_finalization(&conflicting, &action, &facts),
            Ok(false)
        );
        let mut child = action;
        child.parentActionId = Some("parent-1".into());
        child.retryOrdinal = 1;
        assert_eq!(
            ticket_action_v3_command_matches_finalization(&command, &child, &facts),
            Ok(false)
        );
    }

    #[test]
    fn atomic_activation_admission_requires_exact_action_and_revision() {
        let action = pending_atomic_ticket_action("register_current");
        let facts = atomic_terminal_facts("register_current", "succeeded").unwrap();
        let mut history = successful_activation_history();
        history.attemptId = action.actionId.clone();
        history.interactionCorrelation = action.actionId.clone();
        history.interactionRevision = facts.interaction_revision.clone();
        assert!(ticket_action_v3_activation_admission_matches(
            &history, &action, &facts
        ));

        history.interactionCorrelation = "other-action".into();
        assert!(!ticket_action_v3_activation_admission_matches(
            &history, &action, &facts
        ));
        history.interactionCorrelation = action.actionId.clone();
        history.interactionRevision = "other-proof".into();
        assert!(!ticket_action_v3_activation_admission_matches(
            &history, &action, &facts
        ));
    }

    #[test]
    fn atomic_ticket_action_replay_requires_every_terminal_fact_and_region() {
        let facts = atomic_terminal_facts("register_current", "succeeded").unwrap();
        let mut action = pending_atomic_ticket_action("register_current");
        action.status = facts.status.clone();
        action.phase = facts.phase.clone();
        action.currentView = facts.current_view.clone();
        action.streamEpoch = facts.stream_epoch.clone();
        action.frameSequence = facts.frame_sequence.clone();
        action.reason = facts.reason.clone();
        action.completedAt = facts.completed_at.clone();
        assert!(ticket_action_v3_terminal_replay_matches(&action, &facts));
        action.reason = "ticket_action_activation_visual_unproved".into();
        assert!(!ticket_action_v3_terminal_replay_matches(&action, &facts));

        let proof_action = TicketremoteTicketActionV3 {
            target: "prove_current".into(),
            status: "succeeded".into(),
            phase: "complete".into(),
            currentView: "latest_unactivated".into(),
            actionId: "proof-1".into(),
            streamEpoch: "70".into(),
            frameSequence: "80".into(),
            ..pending_atomic_ticket_action("prove_current")
        };
        let input = TicketSliderRegionV3Input {
            left_basis_points: 1_000,
            top_basis_points: 7_000,
            right_basis_points: 9_000,
            bottom_basis_points: 7_700,
        };
        let mut region = TicketremoteTicketSliderRegionV3 {
            id: ticket_slider_region_v3_id("vivi-default", "pixel"),
            ticketId: "vivi-default".into(),
            backendId: "pixel".into(),
            proofActionId: "proof-1".into(),
            streamEpoch: "70".into(),
            frameSequence: "80".into(),
            leftBasisPoints: 1_000,
            topBasisPoints: 7_000,
            rightBasisPoints: 9_000,
            bottomBasisPoints: 7_700,
            updatedAt: "2026-09-03T10:00:05Z".into(),
            expiresAt: "2026-09-03T10:05:05Z".into(),
        };
        assert!(ticket_action_v3_slider_region_replay_matches(
            Some(&region),
            &proof_action,
            Some(input)
        ));
        region.rightBasisPoints = 8_999;
        assert!(!ticket_action_v3_slider_region_replay_matches(
            Some(&region),
            &proof_action,
            Some(input)
        ));
        assert!(!ticket_action_v3_slider_region_replay_matches(
            Some(&region),
            &proof_action,
            None
        ));
        assert!(ticket_action_v3_slider_region_replay_matches(
            None,
            &proof_action,
            Some(input)
        ));

        region.proofActionId = "newer-proof".into();
        assert!(ticket_action_v3_slider_region_replay_matches(
            Some(&region),
            &proof_action,
            Some(input)
        ));
        assert!(ticket_action_v3_slider_region_replay_matches(
            Some(&region),
            &proof_action,
            None
        ));
    }

    #[test]
    fn atomic_refresh_replay_survives_terminal_schedule_cleanup() {
        let mut action = pending_atomic_ticket_action("open_latest_unactivated");
        action.id = ticket_action_v3_row_id("vivi-default", "pixel", "schedule-1");
        action.actionId = "schedule-1".into();
        action.rootActionId = Some("schedule-1".into());
        let facts = ticket_action_v3_terminal_facts(
            "open_latest_unactivated",
            "succeeded",
            "complete",
            "latest_unactivated",
            "42",
            "52",
            "ticket_action_latest_redetected",
            "2026-09-03T10:00:05Z",
            "activation-attempt-1",
            "schedule:schedule-1",
            "activation-1",
            "2026-09-03T10:00:06Z",
        )
        .unwrap();
        let command_id = ticket_action_v3_command_id("vivi-default", "pixel", "schedule-1");
        let mut history = successful_activation_history();
        history.refreshOutcome = "succeeded".into();
        history.refreshCompletedAt = facts.completed_at.clone();
        let mut schedule = activation_refresh_schedule();
        schedule.commandId = command_id.clone();
        schedule.status = "succeeded".into();
        schedule.resultReason = facts.reason.clone();
        schedule.resultPhase = facts.phase.clone();
        schedule.proofSource = "phone_worker".into();
        schedule.completedAt = facts.completed_at.clone();

        assert!(ticket_action_v3_refresh_terminal_replay_matches(
            &history,
            Some(&schedule),
            &action,
            &command_id,
            &facts,
        ));
        assert!(ticket_action_v3_refresh_terminal_replay_matches(
            &history,
            None,
            &action,
            &command_id,
            &facts,
        ));
        let mut wrong_revision = facts.clone();
        wrong_revision.interaction_revision = "schedule:different-action".into();
        assert!(!ticket_action_v3_refresh_terminal_replay_matches(
            &history,
            None,
            &action,
            &command_id,
            &wrong_revision,
        ));

        schedule.resultReason = "ticket_action_v3_failed".into();
        assert!(!ticket_action_v3_refresh_terminal_replay_matches(
            &history,
            Some(&schedule),
            &action,
            &command_id,
            &facts,
        ));
        history.refreshCompletedAt = "2026-09-03T10:00:04Z".into();
        assert!(!ticket_action_v3_refresh_terminal_replay_matches(
            &history,
            None,
            &action,
            &command_id,
            &facts,
        ));
    }

    #[test]
    fn atomic_nonactivation_replay_requires_the_original_command_revision() {
        let mut action = pending_atomic_ticket_action("redetect_latest");
        let direct = ticket_action_v3_terminal_facts(
            "redetect_latest",
            "succeeded",
            "complete",
            "latest_unactivated",
            "42",
            "52",
            "ticket_action_latest_redetected",
            "2026-09-03T10:00:05Z",
            "",
            "action-1",
            "",
            "2026-09-03T10:00:06Z",
        )
        .unwrap();
        let command_id = ticket_action_v3_command_id("vivi-default", "pixel", "action-1");
        assert!(ticket_action_v3_scheduled_terminal_replay_matches(
            None,
            &action,
            &command_id,
            &direct,
        ));
        let mut wrong_revision = direct.clone();
        wrong_revision.interaction_revision = "different-revision".into();
        assert!(!ticket_action_v3_scheduled_terminal_replay_matches(
            None,
            &action,
            &command_id,
            &wrong_revision,
        ));
        let mut unexpected_attempt = direct.clone();
        unexpected_attempt.attempt_id = "unexpected-attempt".into();
        assert!(!ticket_action_v3_scheduled_terminal_replay_matches(
            None,
            &action,
            &command_id,
            &unexpected_attempt,
        ));
        let mut unexpected_activation = direct.clone();
        unexpected_activation.activation_revision = "unexpected-activation".into();
        assert!(!ticket_action_v3_scheduled_terminal_replay_matches(
            None,
            &action,
            &command_id,
            &unexpected_activation,
        ));

        let mut schedule = latest_ticket_reselect_schedule();
        schedule.id = "action-1".into();
        schedule.purpose = Some("ticket_action_v3_redetect_latest".into());
        schedule.commandId = command_id.clone();
        schedule.status = "succeeded".into();
        schedule.resultReason = direct.reason.clone();
        schedule.resultPhase = direct.phase.clone();
        schedule.proofSource = "phone_worker".into();
        schedule.completedAt = direct.completed_at.clone();
        let mut scheduled = direct.clone();
        scheduled.interaction_revision = "schedule:action-1".into();
        assert!(ticket_action_v3_scheduled_terminal_replay_matches(
            Some(&schedule),
            &action,
            &command_id,
            &scheduled,
        ));
        assert!(!ticket_action_v3_scheduled_terminal_replay_matches(
            Some(&schedule),
            &action,
            &command_id,
            &direct,
        ));
        assert!(!ticket_action_v3_scheduled_terminal_replay_matches(
            None,
            &action,
            &command_id,
            &scheduled,
        ));

        action.actionId = "other-action".into();
        assert!(!ticket_action_v3_scheduled_terminal_replay_matches(
            Some(&schedule),
            &action,
            &command_id,
            &scheduled,
        ));

        let source = include_str!("lib.rs");
        let cleanup = source
            .split("macro_rules! purge_ticket_history")
            .nth(1)
            .and_then(|body| body.split("macro_rules! bootstrap_stream_state").next())
            .expect("bounded ticket history cleanup");
        assert!(
            cleanup.find("ticketremote_ticket_action_v3,").unwrap()
                < cleanup
                    .find("ticketremote_latest_ticket_reselect_schedule,")
                    .unwrap()
        );
    }

    #[test]
    fn atomic_finalizer_rolls_back_before_retirement_and_creates_no_child() {
        let source = include_str!("lib.rs");
        let body = source
            .split("pub fn ticketremote_finalize_ticket_action_v3(")
            .nth(1)
            .and_then(|body| {
                body.split("pub fn ticketremote_update_ticket_action_v3(")
                    .next()
            })
            .expect("atomic finalizer body");
        assert!(body.contains("require_service(ctx)?"));
        assert!(body.contains("ticket_action_v3_obsolete_read_only_finalization("));
        assert!(body.contains("ticket_action_v3_command_matches_finalization"));
        assert!(body.contains("commit_ticket_activation_at_impl("));
        assert!(body.contains("finalize_ticket_activation_failure_checked_impl("));
        assert!(body.contains("finalize_ticket_activation_refresh_at_impl("));
        assert!(body.contains(".delete(&expected_command_id)"));
        assert!(body.contains("upsert_stream_command_signal("));
        assert!(body.contains("promote_ticket_action_v3_queue("));
        let delete = body.find(".delete(&expected_command_id)").unwrap();
        assert!(body.find("update_ticket_action_v3_projection(").unwrap() < delete);
        assert!(body.find("commit_ticket_activation_at_impl(").unwrap() < delete);
        assert!(
            body.find("finalize_ticket_activation_failure_checked_impl(")
                .unwrap()
                < delete
        );
        assert!(body.find("upsert_stream_command_signal(").unwrap() > delete);
        assert!(body.find("promote_ticket_action_v3_queue(").unwrap() > delete);
        assert_eq!(body.matches(".delete(&expected_command_id)").count(), 1);
        assert_eq!(body.matches("promote_ticket_action_v3_queue(").count(), 1);
        assert!(!body.contains("admit_member_limit_event("));
        assert!(!body.contains("activation_admission_for_action("));
        assert!(!body.contains("retryOrdinal: 1"));
        assert!(!body.contains("ticketremote_ticket_action_v3().insert"));
    }

    #[test]
    fn vivi_credential_and_reauth_contract_is_private_fenced_and_sanitized() {
        assert_eq!(
            vivi_scope("vivi-default", "pixel").unwrap(),
            ("vivi-default".into(), "pixel".into())
        );
        assert_eq!(
            vivi_scope("", "").unwrap(),
            ("vivi-default".into(), "pixel".into())
        );
        assert_eq!(
            vivi_scope("another-ticket", "pixel"),
            Err("unsupported_vivi_ticket".into())
        );
        assert_eq!(
            vivi_scope("vivi-default", "another-backend"),
            Err("unsupported_vivi_backend".into())
        );
        assert_eq!(clean_vivi_revision(" cred-01 ").unwrap(), "cred-01");
        assert!(clean_vivi_revision("").is_err());
        assert!(clean_vivi_revision("revision with spaces").is_err());
        assert_eq!(clean_expected_vivi_revision("  ").unwrap(), "");
        assert_eq!(
            clean_expected_vivi_revision(" cred-01 ").unwrap(),
            "cred-01"
        );
        assert_eq!(clean_vivi_request_id("request_01").unwrap(), "request_01");
        assert!(clean_vivi_request_id("request/01").is_err());
        assert!(
            !VIVI_REAUTH_LOGOUT_REDETECT_LOGIN_REQUEST_PREFIX
                .starts_with(VIVI_REAUTH_LOGOUT_LOGIN_REQUEST_PREFIX)
        );
        assert_eq!(
            vivi_reauth_request_mode("vivi-reauth-01"),
            Some(ViviReauthMode::LegacyV1)
        );
        assert_eq!(
            vivi_reauth_request_mode("vivi-full-reset-01"),
            Some(ViviReauthMode::FullResetV2)
        );
        assert_eq!(
            vivi_reauth_request_mode("vivi-logout-login-01"),
            Some(ViviReauthMode::LogoutLoginV3)
        );
        assert_eq!(
            vivi_reauth_request_mode("vivi-logout-redetect-login-01"),
            Some(ViviReauthMode::LogoutLoginRedetectV4)
        );
        assert_eq!(vivi_reauth_request_mode("vivi-reauth-"), None);
        assert_eq!(vivi_reauth_request_mode("vivi-full-reset-"), None);
        assert_eq!(vivi_reauth_request_mode("vivi-logout-login-"), None);
        assert_eq!(
            vivi_reauth_request_mode("vivi-logout-redetect-login-"),
            None
        );
        assert_eq!(vivi_reauth_request_mode("request_01"), None);
        assert!(vivi_reauth_request_mode_matches(
            "vivi-reauth-01",
            ViviReauthMode::LegacyV1
        ));
        assert!(!vivi_reauth_request_mode_matches(
            "vivi-full-reset-01",
            ViviReauthMode::LegacyV1
        ));
        assert!(vivi_reauth_request_mode_matches(
            "vivi-full-reset-01",
            ViviReauthMode::FullResetV2
        ));
        assert!(vivi_reauth_request_mode_matches(
            "vivi-logout-login-01",
            ViviReauthMode::LogoutLoginV3
        ));
        assert!(vivi_reauth_request_mode_matches(
            "vivi-logout-redetect-login-01",
            ViviReauthMode::LogoutLoginRedetectV4
        ));
        assert_eq!(
            vivi_reauth_logout_login_mode(3, "vivi-logout-login-01"),
            Ok(ViviReauthMode::LogoutLoginV3)
        );
        assert_eq!(
            vivi_reauth_logout_login_mode(4, "vivi-logout-redetect-login-01"),
            Ok(ViviReauthMode::LogoutLoginRedetectV4)
        );
        assert_eq!(
            vivi_reauth_logout_login_mode(3, "vivi-logout-redetect-login-01"),
            Err("vivi_reauth_request_mode_mismatch".into())
        );
        assert_eq!(
            vivi_reauth_logout_login_mode(4, "vivi-logout-login-01"),
            Err("vivi_reauth_request_mode_mismatch".into())
        );
        assert_eq!(
            vivi_reauth_logout_login_mode(5, "vivi-logout-redetect-login-01"),
            Err("unsupported_vivi_reauth_version".into())
        );
        assert_eq!(
            validate_vivi_reauth_service_report_mode(
                "vivi-logout-redetect-login-01",
                "running",
                "redetecting_latest_ticket",
                "running",
                "",
                "0",
                "0",
            ),
            Ok(())
        );
        for reason in [
            "saved_credentials_original_ticket_restored",
            "saved_credentials_latest_ticket_redetected",
            "saved_credentials_no_ticket_proven",
        ] {
            assert_eq!(
                validate_vivi_reauth_service_report_mode(
                    "vivi-logout-redetect-login-01",
                    "succeeded",
                    "complete",
                    reason,
                    "phone_visual",
                    "10",
                    "20",
                ),
                Ok(())
            );
            assert_eq!(
                validate_vivi_reauth_service_report_mode(
                    "vivi-logout-login-01",
                    "succeeded",
                    "complete",
                    reason,
                    "phone_visual",
                    "10",
                    "20",
                ),
                Err("vivi_reauth_report_mode_mismatch".into())
            );
        }
        assert_eq!(
            validate_vivi_reauth_service_report_mode(
                "vivi-logout-redetect-login-01",
                "succeeded",
                "complete",
                "saved_credentials_sign_in_proven",
                "phone_visual",
                "10",
                "20",
            ),
            Err("vivi_reauth_v4_success_proof_invalid".into())
        );
        assert_eq!(
            validate_vivi_reauth_service_report_mode(
                "vivi-logout-redetect-login-01",
                "succeeded",
                "complete",
                "saved_credentials_latest_ticket_redetected",
                "",
                "0",
                "0",
            ),
            Err("vivi_reauth_v4_success_proof_invalid".into())
        );
        assert_eq!(
            validate_vivi_reauth_service_report_mode(
                "vivi-full-reset-01",
                "running",
                "redetecting_latest_ticket",
                "running",
                "",
                "0",
                "0",
            ),
            Err("vivi_reauth_report_mode_mismatch".into())
        );

        for status in [
            "queued",
            "pending",
            "running",
            "succeeded",
            "failed",
            "needs_attention",
        ] {
            assert_eq!(vivi_reauth_status(status).unwrap(), status);
        }
        assert!(vivi_reauth_status("retrying").is_err());
        for phase in [
            "resetting_vivi",
            "opening_account_controls",
            "requesting_logout",
            "verifying_signed_out",
            "redetecting_latest_ticket",
        ] {
            assert_eq!(vivi_reauth_phase(phase).unwrap(), phase);
        }
        for reason in [
            "saved_credentials_sign_in_proven",
            "logout_control_not_detected",
            "logout_transition_not_proven",
            "logout_action_uncertain",
            "saved_credentials_original_ticket_restored",
            "saved_credentials_latest_ticket_redetected",
            "saved_credentials_no_ticket_proven",
        ] {
            assert_eq!(vivi_reauth_reason(reason).unwrap(), reason);
        }
        assert!(vivi_reauth_reason("password=secret").is_err());
        assert!(vivi_reauth_phase("raw_password_entry").is_err());
        assert!(vivi_reauth_proof_source("screenshot_ocr").is_err());

        let safe_payload = serde_json::from_str::<serde_json::Value>(&vivi_reauth_command_payload(
            "vivi-reauth-01",
            "cred-01",
            ViviReauthMode::LegacyV1,
        ))
        .unwrap();
        assert_eq!(
            safe_payload,
            serde_json::json!({
                "version": 1,
                "requestId": "vivi-reauth-01",
                "credentialRevision": "cred-01",
            })
        );
        let full_reset_payload =
            serde_json::from_str::<serde_json::Value>(&vivi_reauth_command_payload(
                "vivi-full-reset-01",
                "cred-01",
                ViviReauthMode::FullResetV2,
            ))
            .unwrap();
        assert_eq!(
            full_reset_payload,
            serde_json::json!({
                "version": 2,
                "requestId": "vivi-full-reset-01",
                "credentialRevision": "cred-01",
                "resetAppData": true,
            })
        );
        let logout_login_payload =
            serde_json::from_str::<serde_json::Value>(&vivi_reauth_command_payload(
                "vivi-logout-login-01",
                "cred-01",
                ViviReauthMode::LogoutLoginV3,
            ))
            .unwrap();
        assert_eq!(
            logout_login_payload,
            serde_json::json!({
                "version": 3,
                "requestId": "vivi-logout-login-01",
                "credentialRevision": "cred-01",
                "logoutInApp": true,
            })
        );
        let logout_redetect_login_payload =
            serde_json::from_str::<serde_json::Value>(&vivi_reauth_command_payload(
                "vivi-logout-redetect-login-01",
                "cred-01",
                ViviReauthMode::LogoutLoginRedetectV4,
            ))
            .unwrap();
        assert_eq!(
            logout_redetect_login_payload,
            serde_json::json!({
                "version": 4,
                "requestId": "vivi-logout-redetect-login-01",
                "credentialRevision": "cred-01",
                "logoutInApp": true,
                "redetectAfterLogin": true,
            })
        );
        assert_eq!(
            vivi_reauth_queued_intent_fields(&vivi_reauth_queued_intent_payload(
                "cred-01",
                ViviReauthMode::LegacyV1,
            )),
            Some(("cred-01".into(), ViviReauthMode::LegacyV1))
        );
        assert_eq!(
            vivi_reauth_queued_intent_fields(&vivi_reauth_queued_intent_payload(
                "cred-01",
                ViviReauthMode::FullResetV2,
            )),
            Some(("cred-01".into(), ViviReauthMode::FullResetV2))
        );
        let logout_login_queued_payload =
            vivi_reauth_queued_intent_payload("cred-01", ViviReauthMode::LogoutLoginV3);
        assert_eq!(
            serde_json::from_str::<serde_json::Value>(&logout_login_queued_payload).unwrap(),
            serde_json::json!({
                "credentialRevision": "cred-01",
                "logoutInApp": true,
            })
        );
        assert_eq!(
            vivi_reauth_queued_intent_fields(&logout_login_queued_payload),
            Some(("cred-01".into(), ViviReauthMode::LogoutLoginV3))
        );
        let logout_redetect_login_queued_payload =
            vivi_reauth_queued_intent_payload("cred-01", ViviReauthMode::LogoutLoginRedetectV4);
        assert_eq!(
            serde_json::from_str::<serde_json::Value>(&logout_redetect_login_queued_payload)
                .unwrap(),
            serde_json::json!({
                "credentialRevision": "cred-01",
                "logoutInApp": true,
                "redetectAfterLogin": true,
            })
        );
        assert_eq!(
            vivi_reauth_queued_intent_fields(&logout_redetect_login_queued_payload),
            Some(("cred-01".into(), ViviReauthMode::LogoutLoginRedetectV4))
        );
        assert_eq!(
            vivi_reauth_queued_intent_fields(
                r#"{"credentialRevision":"cred-01","resetAppData":false}"#
            ),
            None
        );
        assert_eq!(
            vivi_reauth_queued_intent_fields(
                r#"{"credentialRevision":"cred-01","logoutInApp":false}"#
            ),
            None
        );
        assert_eq!(
            vivi_reauth_queued_intent_fields(
                r#"{"credentialRevision":"cred-01","logoutInApp":true,"redetectAfterLogin":false}"#
            ),
            None
        );
        assert_eq!(
            vivi_reauth_queued_intent_fields(
                r#"{"credentialRevision":"cred-01","logoutInApp":true,"redetectAfterLogin":"true"}"#
            ),
            None
        );
        assert_eq!(
            vivi_reauth_queued_intent_fields(
                r#"{"credentialRevision":"cred-01","redetectAfterLogin":true}"#
            ),
            None
        );
        assert_eq!(
            vivi_reauth_queued_intent_fields(
                r#"{"credentialRevision":"cred-01","logoutInApp":true,"extra":1}"#
            ),
            None
        );
        assert_eq!(
            vivi_reauth_queued_intent_fields(
                r#"{"credentialRevision":"cred-01","resetAppData":true,"extra":1}"#
            ),
            None
        );
        assert_eq!(
            vivi_reauth_queued_intent_fields(
                r#"{"credentialRevision":"cred-01","logoutInApp":true,"redetectAfterLogin":true,"extra":1}"#
            ),
            None
        );

        let source = include_str!("lib.rs");
        let command = source
            .split("fn vivi_reauth_command_payload(")
            .nth(1)
            .and_then(|body| body.split("fn queue_vivi_reauth_intent(").next())
            .expect("ViVi reauth command contract must remain inspectable");
        for required in [
            "vivi_reauth_command_payload(request_id, credential_revision, mode)",
            "\"vivi_reauth\"",
        ] {
            assert!(
                command.contains(required),
                "missing command marker {required}"
            );
        }
        for forbidden in ["\"password\"", "\"email\"", "TicketremoteViviCredentials"] {
            assert!(
                !command.contains(forbidden),
                "credential material escaped into command payload: {forbidden}"
            );
        }
        let promotion = source
            .split("if intent.kind == \"vivi_reauth\"")
            .nth(1)
            .and_then(|body| body.split("if intent.kind == \"control_code\"").next())
            .expect("queued ViVi reauth promotion must remain inspectable");
        for required in [
            "vivi_reauth_queued_intent_fields(&intent.privatePayloadJson)",
            "vivi_reauth_request_mode_matches(&intent.actionId, mode)",
            "admit_vivi_reauth(",
        ] {
            assert!(
                promotion.contains(required),
                "queued ViVi re-auth mode was not preserved through promotion: {required}"
            );
        }

        let credential_table = source
            .split("accessor = ticketremote_vivi_credentials,")
            .nth(1)
            .and_then(|body| body.split("pub struct TicketremoteViviCredentials").next())
            .expect("private credential table must remain inspectable");
        assert!(!credential_table.contains("public"));
        let owner_authority_table = source
            .split("accessor = ticketremote_vivi_reauth_owner,")
            .nth(1)
            .and_then(|body| body.split("pub struct TicketremoteViviReauthOwner").next())
            .expect("private owner authority table must remain inspectable");
        assert!(!owner_authority_table.contains("public"));
        assert!(source.contains("accessor = ticketremote_owner_vivi_credentials"));
        assert!(source.contains("accessor = ticketremote_service_vivi_credentials"));
        let owner_prepare = source
            .split("pub fn ticketremote_owner_prepare_vivi_credentials(")
            .nth(1)
            .and_then(|body| {
                body.split("pub fn ticketremote_owner_save_vivi_credentials(")
                    .next()
            })
            .expect("owner credential access preparation must remain inspectable");
        for required in [
            "vivi_scope(&ticketId, &backendId)?",
            "client_email_from_auth(ctx, &ticket.id)?",
            "require_owner(ctx, &ticket.id, &owner_email)?",
            "upsert_member_identity(ctx, &ticket.id, &owner_email, &now)",
        ] {
            assert!(
                owner_prepare.contains(required),
                "missing owner access preparation marker {required}"
            );
        }
        assert!(source.contains("ticket_has_vivi_reauth_in_progress(ctx, ticket_id, backend_id)"));
        assert!(source.contains("cancel_unstarted_vivi_reauth_for_owner"));
        assert!(source.contains("vivi_reauth_terminal_conflict"));
        assert!(source.contains("vivi_reauth_attempt_already_claimed"));
        assert!(source.contains("vivi_reauth_attempt_not_claimed"));
        assert!(source.contains("expectedRevision: String"));
        assert_eq!(vivi_reauth_interrupted_terminal_status("pending"), "failed");
        assert_eq!(
            vivi_reauth_interrupted_terminal_status("running"),
            "needs_attention"
        );

        let mut malformed_command = stream_command("vivi_reauth", "requested");
        malformed_command.id = vivi_reauth_command_id("vivi-default", "pixel", "request_01");
        malformed_command.payloadJson = "{not-json".into();
        assert_eq!(
            vivi_reauth_request_id_from_command(&malformed_command).as_deref(),
            Some("request_01")
        );
        malformed_command.payloadJson =
            r#"{"version":"1","requestId":7,"credentialRevision":false}"#.into();
        assert_eq!(
            vivi_reauth_request_id_from_command(&malformed_command).as_deref(),
            Some("request_01")
        );
        malformed_command.payloadJson = "{}".into();
        assert_eq!(
            vivi_reauth_request_id_from_command(&malformed_command).as_deref(),
            Some("request_01")
        );
    }

    #[test]
    fn member_role_parser_rejects_unknown_and_empty_roles() {
        assert_eq!(requested_member_role(" MEMBER ").unwrap(), "member");
        assert_eq!(requested_member_role("Admin").unwrap(), "admin");
        assert_eq!(requested_member_role("owner").unwrap(), "owner");
        assert_eq!(requested_member_role(""), Err("invalid_role".into()));
        assert_eq!(
            requested_member_role("superuser"),
            Err("invalid_role".into())
        );
    }

    fn activity_utc(value: &str) -> DateTime<Utc> {
        DateTime::parse_from_rfc3339(value)
            .expect("valid UTC activity fixture")
            .with_timezone(&Utc)
    }

    #[test]
    fn member_activity_bucket_uses_riga_day_hour_and_global_five_second_slot() {
        let utc = activity_utc("2026-09-02T21:34:56.789Z");
        let bucket = member_activity_bucket_from_utc(utc).expect("valid activity bucket");

        assert_eq!(bucket.day, "2026-09-03");
        assert_eq!(bucket.hour, 0);
        assert_eq!(
            bucket.tick_slot,
            utc.timestamp_micros()
                .div_euclid(MEMBER_ACTIVITY_TICK_SLOT_MICROS)
        );
        assert_eq!(bucket.expires_at, "2026-10-09T21:00:00+00:00");
    }

    #[test]
    fn member_activity_combines_repeated_riga_hour_and_skips_spring_gap() {
        let autumn_first = member_activity_bucket_from_utc(activity_utc("2026-10-25T00:30:00Z"))
            .expect("first repeated-hour bucket");
        let autumn_second = member_activity_bucket_from_utc(activity_utc("2026-10-25T01:30:00Z"))
            .expect("second repeated-hour bucket");
        assert_eq!(autumn_first.day, "2026-10-25");
        assert_eq!(autumn_second.day, autumn_first.day);
        assert_eq!(autumn_first.hour, 3);
        assert_eq!(autumn_second.hour, autumn_first.hour);
        assert_ne!(autumn_first.tick_slot, autumn_second.tick_slot);

        let spring_before = member_activity_bucket_from_utc(activity_utc("2026-03-29T00:59:59Z"))
            .expect("pre-gap bucket");
        let spring_after = member_activity_bucket_from_utc(activity_utc("2026-03-29T01:00:00Z"))
            .expect("post-gap bucket");
        assert_eq!(spring_before.day, "2026-03-29");
        assert_eq!(spring_after.day, spring_before.day);
        assert_eq!(spring_before.hour, 2);
        assert_eq!(spring_after.hour, 4);
    }

    #[test]
    fn member_activity_expiry_is_thirty_seven_local_calendar_days() {
        let day_start = activity_utc("2026-09-30T21:00:00Z");
        let bucket = member_activity_bucket_from_utc(day_start).expect("valid autumn bucket");
        let expiry = activity_utc(&bucket.expires_at);

        assert_eq!(bucket.day, "2026-10-01");
        assert_eq!(bucket.expires_at, "2026-11-06T22:00:00+00:00");
        assert_eq!(
            expiry.timestamp_micros() - day_start.timestamp_micros(),
            (37 * 24 * 60 * 60 + 60 * 60) * 1_000_000
        );
    }

    #[test]
    fn member_activity_tick_is_slot_deduplicated_normalized_and_saturating() {
        let mut ticks = Vec::new();
        let mut last_slot = i64::MIN;
        assert!(apply_member_activity_tick(
            &mut ticks,
            &mut last_slot,
            3,
            100
        ));
        assert_eq!(ticks.len(), 24);
        assert_eq!(ticks[3], 1);
        assert_eq!(last_slot, 100);

        assert!(!apply_member_activity_tick(
            &mut ticks,
            &mut last_slot,
            3,
            100
        ));
        assert!(!apply_member_activity_tick(
            &mut ticks,
            &mut last_slot,
            4,
            99
        ));
        assert_eq!(ticks[3], 1);
        assert_eq!(ticks[4], 0);

        assert!(apply_member_activity_tick(
            &mut ticks,
            &mut last_slot,
            3,
            101
        ));
        assert_eq!(ticks[3], 2);
        ticks[3] = u32::MAX;
        assert!(apply_member_activity_tick(
            &mut ticks,
            &mut last_slot,
            3,
            102
        ));
        assert_eq!(ticks[3], u32::MAX);
        assert_eq!(last_slot, 102);
        assert!(!apply_member_activity_tick(
            &mut ticks,
            &mut last_slot,
            24,
            103
        ));
        assert_eq!(last_slot, 102);
    }

    #[test]
    fn member_activity_row_identity_is_account_and_day_scoped() {
        let first = account_scope_id(" First@Example.Invalid ");
        let same = account_scope_id("first@example.invalid");
        let second = account_scope_id("second@example.invalid");
        assert_eq!(first, same);
        assert_eq!(
            member_activity_row_id("vivi-default", &first, "2026-09-02"),
            member_activity_row_id("vivi-default", &same, "2026-09-02")
        );
        assert_ne!(
            member_activity_row_id("vivi-default", &first, "2026-09-02"),
            member_activity_row_id("vivi-default", &second, "2026-09-02")
        );
        assert_ne!(
            member_activity_row_id("vivi-default", &first, "2026-09-02"),
            member_activity_row_id("vivi-default", &first, "2026-09-03")
        );
    }

    #[test]
    fn member_activity_contract_is_private_authenticated_and_service_only() {
        let source = include_str!("lib.rs");
        let table = source
            .split("accessor = ticketremote_member_daily_activity")
            .nth(1)
            .and_then(|body| body.split("accessor = ticketremote_phone_backend").next())
            .expect("private activity table schema");
        assert!(!table.lines().next().unwrap_or_default().contains("public"));
        for field in [
            "pub id: String",
            "pub ticketId: String",
            "pub accountScopeId: String",
            "pub day: String",
            "pub hourlyTicks: Vec<u32>",
            "pub lastTickSlot: i64",
            "pub firstTickAt: String",
            "pub lastTickAt: String",
            "pub updatedAt: String",
            "pub expiresAt: String",
        ] {
            assert!(table.contains(field), "missing activity field: {field}");
        }
        assert!(table.contains("index(accessor = ticketDay"));
        assert!(table.contains("index(accessor = ticketExpiresAt"));

        let reducer = source
            .split("pub fn ticketremote_member_record_activity_tick(")
            .nth(1)
            .and_then(|body| {
                body.split("pub fn ticketremote_member_set_limit_preference(")
                    .next()
            })
            .expect("member activity reducer");
        assert!(reducer.contains("ticketId: String"));
        assert!(!reducer.contains("nowArg"));
        assert!(!reducer.contains("email: String"));
        assert!(reducer.contains("client_email_from_auth(ctx, &ticket.id)?"));
        assert!(reducer.contains("member_activity_bucket(ctx.timestamp)?"));
        assert!(reducer.contains("upsert_member_activity_tick("));

        let service_view = source
            .split("ticketremote_service_member_daily_activity =>")
            .nth(1)
            .and_then(|body| body.split("ticketremote_service_phone_backend =>").next())
            .expect("service-only activity projection");
        assert!(service_view.contains("ticketremote_service_member_daily_activity_view"));
        assert!(service_view.contains("ticketDay().filter((&ticket,))"));
        let view_gate = source
            .split("macro_rules! service_views")
            .nth(1)
            .and_then(|body| body.split("macro_rules! expression_functions").next())
            .expect("service view authorization gate");
        assert!(view_gate.contains("service_ticket_id_for_viewer($ctx)"));

        let legacy_member_projection = source
            .split("pub struct TicketremoteServiceMember {")
            .nth(1)
            .and_then(|body| {
                body.split("pub struct TicketremoteServiceMemberAccount")
                    .next()
            })
            .expect("compatible service member projection");
        assert!(!legacy_member_projection.contains("pub accountScopeId: String"));
        let member_account_projection = source
            .split("pub struct TicketremoteServiceMemberAccount {")
            .nth(1)
            .and_then(|body| body.split("cloned_projection!").next())
            .expect("additive service member account projection");
        assert!(member_account_projection.contains("pub accountScopeId: String"));
        assert!(source.contains("ticketremote_service_member_account_view"));
        assert!(source.contains("accountScopeId: account_scope_id(&email)"));
    }

    #[test]
    fn member_activity_cleanup_uses_the_shared_bounded_expiry_path() {
        let source = include_str!("lib.rs");
        let cleanup = source
            .split("macro_rules! purge_ticket_history")
            .nth(1)
            .and_then(|body| body.split("macro_rules! bootstrap_stream_state").next())
            .expect("bounded ticket cleanup path");
        assert!(cleanup.contains("ticketremote_member_daily_activity"));
        let generic_purger = source
            .split("macro_rules! purge_expired_rows")
            .nth(1)
            .and_then(|body| body.split("macro_rules! purge_ticket_history").next())
            .expect("generic indexed expiry purger");
        assert!(generic_purger.contains("ticketExpiresAt()"));
        assert!(generic_purger.contains("cleanup_remaining($limit, $deleted)"));
        assert!(generic_purger.contains(".take("));
    }

    #[test]
    fn member_upsert_policy_separates_admin_and_owner_authority() {
        for target in [None, Some("member")] {
            assert_eq!(
                member_upsert_policy(Some("admin"), target, "member", 1),
                Ok("member".into())
            );
        }
        for requested in ["admin", "owner"] {
            assert_eq!(
                member_upsert_policy(Some("admin"), None, requested, 1),
                Err("forbidden".into())
            );
        }
        for target in [Some("admin"), Some("owner")] {
            assert_eq!(
                member_upsert_policy(Some("admin"), target, "member", 2),
                Err("forbidden".into())
            );
        }
        for actor in [None, Some("member")] {
            assert_eq!(
                member_upsert_policy(actor, None, "member", 1),
                Err("forbidden".into())
            );
        }
        for requested in ["member", "admin", "owner"] {
            assert_eq!(
                member_upsert_policy(Some("owner"), Some("admin"), requested, 1),
                Ok(requested.into())
            );
        }
        assert_eq!(
            member_upsert_policy(Some("owner"), Some("owner"), "owner", 1),
            Ok("owner".into())
        );
        assert_eq!(
            member_upsert_policy(Some("owner"), Some("owner"), "member", 1),
            Err("owner_protected".into())
        );
        assert_eq!(
            member_upsert_policy(Some("owner"), Some("owner"), "admin", 2),
            Ok("admin".into())
        );
    }

    #[test]
    fn member_remove_policy_protects_privileged_roles_and_the_final_owner() {
        assert_eq!(member_remove_policy(Some("admin"), None, 1), Ok(()));
        assert_eq!(
            member_remove_policy(Some("admin"), Some("member"), 1),
            Ok(())
        );
        for target in [Some("admin"), Some("owner")] {
            assert_eq!(
                member_remove_policy(Some("admin"), target, 2),
                Err("forbidden".into())
            );
        }
        assert_eq!(
            member_remove_policy(Some("owner"), Some("owner"), 1),
            Err("owner_protected".into())
        );
        assert_eq!(
            member_remove_policy(Some("owner"), Some("owner"), 2),
            Ok(())
        );
        assert_eq!(
            member_remove_policy(Some("owner"), Some("admin"), 1),
            Ok(())
        );
    }

    #[test]
    fn direct_and_service_member_reducers_share_the_authorization_helpers() {
        let production = include_str!("lib.rs")
            .split("#[cfg(test)]")
            .next()
            .expect("production source must precede tests");
        for (reducer, next_reducer, helper) in [
            (
                "ticketremote_member_upsert_member",
                "ticketremote_member_remove_member",
                "authorize_and_upsert_member",
            ),
            (
                "ticketremote_member_remove_member",
                "ticketremote_service_bootstrap",
                "authorize_and_deactivate_member",
            ),
            (
                "ticketremote_upsert_member",
                "ticketremote_remove_member",
                "authorize_and_upsert_member",
            ),
            (
                "ticketremote_remove_member",
                "ticketremote_update_phone",
                "authorize_and_deactivate_member",
            ),
        ] {
            let body = production
                .split(reducer)
                .nth(1)
                .and_then(|remainder| remainder.split_once(next_reducer).map(|(body, _)| body))
                .unwrap_or_else(|| panic!("{reducer} reducer body must remain inspectable"));
            assert!(
                body.contains(helper),
                "{reducer} must call {helper} inside its own reducer body"
            );
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
            purpose: Some("latest_ticket_reselect".into()),
            activationRevision: Some(String::new()),
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
            activationAttemptId: None,
            originalDueAt: Some("2026-07-23T15:00:00Z".into()),
            nextRetryAt: None,
            retryAttempt: 0,
        }
    }

    fn successful_activation_history() -> TicketremoteActivationHistory {
        TicketremoteActivationHistory {
            id: "activation-history-1".into(),
            ticketId: "vivi-default".into(),
            backendId: "pixel".into(),
            flow: "menu_activate".into(),
            admission: "admitted".into(),
            outcome: "succeeded".into(),
            reason: "activation_succeeded".into(),
            occurredAt: "2026-07-23T14:00:00Z".into(),
            occurrenceDay: "2026-07-23".into(),
            admittedAt: "2026-07-23T14:00:00Z".into(),
            updatedAt: "2026-07-23T14:00:00Z".into(),
            completedAt: "2026-07-23T14:00:00Z".into(),
            attemptId: "activation-attempt-1".into(),
            interactionRevision: "open-proof-1".into(),
            interactionCorrelation: "activation-attempt-1".into(),
            activationRevision: "activation-1".into(),
            inputFingerprint: "opaque-fingerprint".into(),
            refreshDueAt: "2026-07-23T15:00:00Z".into(),
            refreshCompletedAt: String::new(),
            refreshOutcome: "pending".into(),
            refreshRetryAt: String::new(),
            refreshAttempt: 0,
            occurrenceCount: 1,
            expiresAt: "2026-08-22T14:00:00Z".into(),
        }
    }

    fn activation_refresh_schedule() -> TicketremoteLatestTicketReselectSchedule {
        TicketremoteLatestTicketReselectSchedule {
            purpose: Some("activation_expiry_reset".into()),
            activationRevision: Some("activation-1".into()),
            activationAttemptId: Some("activation-attempt-1".into()),
            requestedBy: "pixel_activation".into(),
            phoneLocalTime: String::new(),
            phoneTimeZone: String::new(),
            ..latest_ticket_reselect_schedule()
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
    fn button_activation_requires_exact_current_unactivated_proof() {
        let revision = "2026-08-19T12:00:00Z";
        let mut interaction = default_ticket_interaction("vivi-default", "pixel", revision);
        interaction.status = "unactivated_ready".into();
        assert!(button_activation_state_is_exact(&interaction, revision));

        interaction.status = "needs_attention".into();
        assert!(!button_activation_state_is_exact(&interaction, revision));
        interaction.status = "control_active".into();
        assert!(!button_activation_state_is_exact(&interaction, revision));
        interaction.status = "unactivated_ready".into();
        assert!(!button_activation_state_is_exact(
            &interaction,
            "stale-revision"
        ));
    }

    #[test]
    fn older_slider_worker_move_cannot_replace_newer_browser_up() {
        let revision = "vivi-default:pixel:slider-1";
        let mut interaction = default_ticket_interaction("vivi-default", "pixel", revision);
        interaction.status = "unactivated_ready".into();
        interaction.latestInputSequence = "42".into();
        interaction.latestInputPhase = "up".into();
        interaction.latestProgress = 10_000;
        interaction.lastAppliedSequence = "42".into();
        interaction.lastAppliedProgress = 10_000;

        assert!(ticket_interaction_input_update_is_older(
            &interaction,
            revision,
            "41"
        ));
        assert!(!ticket_interaction_input_update_is_older(
            &interaction,
            revision,
            "42"
        ));
        assert!(!ticket_interaction_input_update_is_older(
            &interaction,
            "vivi-default:pixel:slider-2",
            "41"
        ));
        assert!(!ticket_interaction_last_applied_is_current_or_newer(
            &interaction.lastAppliedSequence,
            "41"
        ));
        assert!(ticket_interaction_last_applied_is_current_or_newer(
            &interaction.lastAppliedSequence,
            "42"
        ));
        assert!(ticket_interaction_last_applied_is_current_or_newer(
            &interaction.lastAppliedSequence,
            "43"
        ));
    }

    #[test]
    fn fresh_registration_proof_accepts_equal_watermark_and_resets_it_atomically() {
        let revision = "vivi-default:pixel:reset-fresh";
        let mut interaction = default_ticket_interaction("vivi-default", "pixel", revision);
        interaction.status = "preparing".into();
        interaction.latestInputSequence = "17".into();
        interaction.latestInputPhase = "move".into();
        interaction.latestProgress = 4_200;
        interaction.lastAppliedSequence = "16".into();
        interaction.lastAppliedProgress = 4_000;

        assert!(!ticket_interaction_input_update_is_older(
            &interaction,
            revision,
            "17"
        ));
        assert!(ticket_interaction_input_update_is_older(
            &interaction,
            revision,
            "16"
        ));
        assert!(!ticket_interaction_update_is_stale(
            &interaction,
            "unactivated_ready",
            revision
        ));

        let source = include_str!("lib.rs");
        let start = source
            .find("let fresh_unactivated_proof = incoming_status == \"unactivated_ready\";")
            .expect("fresh proof reducer branch must exist");
        let end = source[start..]
            .find("current.status = incoming_status.clone()")
            .map(|offset| start + offset)
            .expect("fresh proof reset must happen before writing the new status");
        let reset = &source[start..end];
        for required in [
            "current.ownerPublicId = String::new();",
            "current.controlId = String::new();",
            "current.leasePhase = \"none\".into();",
            "current.latestInputSequence = \"0\".into();",
            "current.latestInputPhase = String::new();",
            "current.latestProgress = 0;",
            "current.lastAppliedSequence = \"0\".into();",
            "current.lastAppliedProgress = 0;",
        ] {
            assert!(
                reset.contains(required),
                "fresh proof reset missing {required}"
            );
        }

        clear_current_ticket_activation_state(&mut interaction);
        assert_eq!(interaction.latestInputSequence, "0");
        assert!(interaction.latestInputPhase.is_empty());
        assert_eq!(interaction.latestProgress, 0);
        assert_eq!(interaction.lastAppliedSequence, "0");
        assert_eq!(interaction.lastAppliedProgress, 0);
        assert!(interaction.ownerPublicId.is_empty());
        assert!(interaction.controlId.is_empty());
        assert_eq!(interaction.leasePhase, "none");
    }

    #[test]
    fn expired_slider_lease_is_released_for_control_code_retry() {
        let now = "2026-08-19T12:10:00Z";
        let mut interaction = default_ticket_interaction("vivi-default", "pixel", now);
        interaction.status = "control_active".into();
        interaction.ownerPublicId = "owner-old".into();
        interaction.controlId = "control-old".into();
        interaction.leasePhase = "active".into();
        interaction.leaseExpiresAt = "2026-08-19T12:09:59Z".into();
        interaction.latestInputSequence = "17".into();
        interaction.latestInputPhase = "move".into();
        interaction.latestProgress = 4_200;
        interaction.lastAppliedSequence = "16".into();
        interaction.lastAppliedProgress = 4_000;

        let repaired = repair_expired_ticket_slider_lease_for_mutation(&interaction, now)
            .expect("expired active lease must be recoverable");
        assert_eq!(repaired.status, "unactivated_ready");
        assert!(repaired.ownerPublicId.is_empty());
        assert!(repaired.controlId.is_empty());
        assert_eq!(repaired.leasePhase, "none");
        assert!(repaired.leaseExpiresAt.is_empty());
        assert_eq!(repaired.latestInputSequence, "0");
        assert_eq!(repaired.lastAppliedSequence, "0");
        assert_eq!(repaired.reason, "slider_lease_expired");

        interaction.leaseExpiresAt = "2026-08-19T12:10:01Z".into();
        assert!(repair_expired_ticket_slider_lease_for_mutation(&interaction, now).is_none());
        interaction.leasePhase = "cooldown".into();
        interaction.leaseExpiresAt = "2026-08-19T12:09:59Z".into();
        assert!(repair_expired_ticket_slider_lease_for_mutation(&interaction, now).is_none());
    }

    #[test]
    fn expired_reset_repair_clears_retry_state_and_allows_new_work() {
        let now = "2026-08-19T12:10:00Z";
        let mut interaction = default_ticket_interaction("vivi-default", "pixel", now);
        interaction.status = "preparing".into();
        interaction.interactionRevision = "vivi-default:pixel:old-reset".into();
        interaction.resetRequestId = "reset-old".into();
        interaction.ownerPublicId = "owner-old".into();
        interaction.controlId = "control-old".into();
        interaction.leasePhase = "active".into();
        interaction.leaseExpiresAt = "2026-08-19T12:20:00Z".into();
        interaction.latestInputSequence = "17".into();
        interaction.latestInputPhase = "move".into();
        interaction.latestProgress = 4_200;
        interaction.lastAppliedSequence = "16".into();
        interaction.lastAppliedProgress = 4_000;

        let repaired =
            repair_ticket_interaction_for_retry(&interaction, now, "ticket_reset_command_expired")
                .expect("in-flight reset must be repairable");

        assert_eq!(repaired.status, "needs_attention");
        assert_ne!(
            repaired.interactionRevision,
            interaction.interactionRevision
        );
        assert!(repaired.resetRequestId.is_empty());
        assert!(repaired.ownerPublicId.is_empty());
        assert!(repaired.controlId.is_empty());
        assert_eq!(repaired.leasePhase, "none");
        assert!(repaired.leaseExpiresAt.is_empty());
        assert_eq!(repaired.latestInputSequence, "0");
        assert!(repaired.latestInputPhase.is_empty());
        assert_eq!(repaired.latestProgress, 0);
        assert_eq!(repaired.lastAppliedSequence, "0");
        assert_eq!(repaired.lastAppliedProgress, 0);
        assert_eq!(repaired.reason, "ticket_reset_command_expired");
        assert!(!ticket_interaction_blocks_control_code(&repaired));
        assert!(!ticket_interaction_blocks_ticket_reset(&repaired, now));
        assert!(ticket_interaction_update_is_stale(
            &repaired,
            "preparing",
            &interaction.interactionRevision
        ));
        assert!(!ticket_interaction_update_is_stale(
            &repaired,
            "preparing",
            &repaired.interactionRevision
        ));
    }

    #[test]
    fn reset_command_expiry_does_not_override_a_live_matching_command() {
        let now = "2026-08-19T12:10:00Z";
        let mut interaction = default_ticket_interaction("vivi-default", "pixel", now);
        interaction.status = "reset_queued".into();
        interaction.interactionRevision = "vivi-default:pixel:reset-1".into();
        interaction.resetRequestId = "reset-1".into();

        let mut command = stream_command("reset_ticket_registration", "ticket_reset_requested");
        command.revision = interaction.interactionRevision.clone();
        command.payloadJson = r#"{"resetRequestId":"reset-1"}"#.into();
        command.expiresAt = "2026-08-19T12:09:59Z".into();
        assert!(!ticket_reset_command_is_live(
            &command,
            &interaction,
            now,
            None
        ));

        command.status = "pending".into();
        command.expiresAt = "2026-08-19T12:11:00Z".into();
        assert!(ticket_reset_command_is_live(
            &command,
            &interaction,
            now,
            None
        ));
        assert!(!ticket_reset_command_is_live(
            &command,
            &interaction,
            now,
            Some(&command.id)
        ));

        let mut newer = command.clone();
        newer.id = "command-newer".into();
        newer.revision = "vivi-default:pixel:reset-newer".into();
        newer.payloadJson = r#"{"resetRequestId":"reset-newer"}"#.into();
        assert!(!ticket_reset_command_matches_interaction(
            &newer,
            &interaction
        ));
    }

    #[test]
    fn queued_activation_refresh_blocks_a_competing_reset() {
        let now = "2026-08-19T12:10:00Z";
        let now_ms = parse_time_ms(now);
        let mut command = stream_command("force_ticket_reselect", "activation_expiry_reset");
        command.payloadJson = r#"{"flow":"activation_expiry_reset"}"#.into();
        command.expiresAt = "2026-08-19T12:11:00Z".into();

        for status in ["pending", "queued", "dispatched", "running"] {
            command.status = status.into();
            assert!(ticket_registration_reset_command_is_live(&command, now_ms));
        }

        command.status = "completed".into();
        assert!(!ticket_registration_reset_command_is_live(&command, now_ms));
        command.status = "queued".into();
        command.expiresAt = "2026-08-19T12:09:59Z".into();
        assert!(!ticket_registration_reset_command_is_live(&command, now_ms));
    }

    #[test]
    fn terminal_reset_failure_requires_the_current_revision() {
        let now = "2026-08-19T12:10:00Z";
        let mut interaction = default_ticket_interaction("vivi-default", "pixel", now);
        interaction.status = "preparing".into();
        interaction.interactionRevision = "revision-current".into();

        assert!(ticket_interaction_failure_matches_current(
            &interaction,
            "needs_attention",
            "revision-current"
        ));
        assert!(ticket_interaction_failure_matches_current(
            &interaction,
            "failed",
            "revision-current"
        ));
        assert!(!ticket_interaction_failure_matches_current(
            &interaction,
            "needs_attention",
            "revision-old"
        ));
        assert!(!ticket_interaction_failure_matches_current(
            &interaction,
            "activated",
            "revision-current"
        ));
    }

    #[test]
    fn latest_ticket_reselect_submission_is_strictly_idempotent() {
        let mut row = latest_ticket_reselect_schedule();
        row.purpose = Some("ticket_action_v3_redetect_latest".into());
        assert!(latest_ticket_reselect_submission_matches(
            &row,
            "vivi-default",
            "pixel",
            "2026-07-23T15:00:00Z",
            "2026-07-23T18:00",
            "Europe/Riga",
            "1on9",
            "ticket_action_v3_redetect_latest",
            "",
        ));
        assert!(!latest_ticket_reselect_submission_matches(
            &row,
            "vivi-default",
            "pixel",
            "2026-07-23T15:01:00Z",
            "2026-07-23T18:01",
            "Europe/Riga",
            "1on9",
            "ticket_action_v3_redetect_latest",
            "",
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
    fn failed_ticket_state_cannot_retain_current_activation_proof() {
        let now = "2026-08-19T12:10:00Z";
        let mut interaction = default_ticket_interaction("vivi-default", "pixel", now);
        interaction.status = "needs_attention".into();
        interaction.activationRevision = "activation-1".into();
        interaction.activationAt = "2026-08-19T11:10:00Z".into();
        interaction.scheduledResetAt = "2026-08-19T12:10:00Z".into();
        interaction.resetRequestId = "reset-1".into();
        interaction.streamEpoch = "42".into();
        interaction.frameSequence = "100".into();
        interaction.sliderRight = 900;
        interaction.leasePhase = "active".into();

        clear_current_ticket_activation_state(&mut interaction);

        assert!(interaction.activationRevision.is_empty());
        assert!(interaction.activationAt.is_empty());
        assert!(interaction.scheduledResetAt.is_empty());
        assert!(interaction.resetRequestId.is_empty());
        assert_eq!(interaction.streamEpoch, "");
        assert_eq!(interaction.frameSequence, "");
        assert_eq!(interaction.sliderRight, 0);
        assert_eq!(interaction.leasePhase, "none");
        assert!(interaction.leaseExpiresAt.is_empty());
    }

    #[test]
    fn scheduled_timer_only_triggers_for_its_pending_matching_schedule() {
        let schedule = latest_ticket_reselect_schedule();
        let timer = TicketremoteLatestTicketReselectTimer {
            scheduled_id: 1,
            scheduled_at: ScheduleAt::Time(Timestamp::from_micros_since_unix_epoch(1)),
            ticketId: schedule.ticketId.clone(),
            backendId: schedule.backendId.clone(),
            scheduleId: schedule.id.clone(),
            createdAt: schedule.createdAt.clone(),
        };
        assert!(latest_ticket_reselect_timer_matches_schedule(
            &schedule, &timer
        ));

        let mut stale_timer = timer.clone();
        stale_timer.scheduleId = "other-schedule".into();
        assert!(!latest_ticket_reselect_timer_matches_schedule(
            &schedule,
            &stale_timer
        ));

        let mut completed = schedule.clone();
        completed.status = "succeeded".into();
        assert!(!latest_ticket_reselect_timer_matches_schedule(
            &completed, &timer
        ));
    }

    #[test]
    fn every_new_phone_mutation_uses_the_full_shared_lane() {
        assert_eq!(
            ticket_action_v3_phone_lane_statuses(),
            ["pending", "running"]
        );
        assert_eq!(
            ticket_phone_mutation_lane_conflict_reason(true, false, false, false, false),
            Some("control_code_in_progress")
        );
        assert_eq!(
            ticket_phone_mutation_lane_conflict_reason(false, true, false, false, false),
            Some("ticket_action_in_progress")
        );
        assert_eq!(
            ticket_phone_mutation_lane_conflict_reason(false, false, false, true, false),
            Some("ticket_reset_in_progress")
        );
        assert_eq!(
            ticket_phone_mutation_lane_conflict_reason(false, false, false, false, true),
            Some("ticket_mutation_in_progress")
        );
        assert_eq!(
            ticket_phone_mutation_lane_conflict_reason(false, false, false, false, false),
            None
        );
        assert_eq!(
            ticket_phone_mutation_lane_conflict_reason(false, false, true, false, false),
            Some("vivi_reauth_in_progress")
        );

        let source = include_str!("lib.rs");
        let immediate_v3 = source
            .split("fn request_ticket_action_v3_impl(")
            .nth(1)
            .and_then(|body| body.split("fn request_ticket_reset_impl(").next())
            .expect("immediate V3 reducer body must remain inspectable");
        let duplicate = immediate_v3
            .find("ticket_action_v3_duplicate_result")
            .expect("V3 duplicate replay must remain explicit");
        let lane = immediate_v3
            .find("ticket_phone_mutation_lane_conflict")
            .expect("new V3 actions must use the shared phone lane");
        assert!(
            duplicate < lane,
            "an existing action id must stay idempotent before checking other lane owners"
        );

        let control_code_reducer = source
            .split("ticketremote_member_request_control_code(ctx;")
            .nth(1)
            .and_then(|body| {
                body.split("ticketremote_member_request_ticket_reset(ctx;")
                    .next()
            })
            .expect("control-code reducer body must remain inspectable");
        assert!(control_code_reducer.contains("ticket_phone_mutation_lane_conflict("));
        assert!(!control_code_reducer.contains("target =="));

        let scheduled_redetect = source
            .split("fn trigger_scheduled_latest_ticket_reselect(")
            .nth(1)
            .and_then(|body| body.split("fn scheduled_redetect_retry_delay_ms(").next())
            .expect("scheduled redetect reducer body must remain inspectable");
        assert!(scheduled_redetect.contains("ticket_phone_mutation_lane_conflict("));
    }

    #[test]
    fn expired_v3_command_becomes_terminal_before_the_command_is_deleted() {
        for (target, expected_status) in [
            ("open_latest_unactivated", "failed"),
            ("redetect_latest", "failed"),
            ("open_latest_and_register", "needs_attention"),
            ("register_current", "needs_attention"),
        ] {
            let (status, phase) = ticket_action_v3_command_failure_projection(target);
            assert_eq!(status, expected_status);
            assert_eq!(phase, expected_status);
            assert!(ticket_action_v3_terminal(status));
            assert!(!ticket_action_v3_phone_lane_statuses().contains(&status));
        }

        let cleanup = include_str!("lib.rs")
            .split("fn purge_expired_stream_commands_for_ticket(")
            .nth(1)
            .and_then(|body| {
                body.split("fn purge_expired_stream_viewer_focus_for_ticket_backend(")
                    .next()
            })
            .expect("expired stream-command cleanup must remain inspectable");
        let terminalize = cleanup
            .find("fail_ticket_action_v3_for_command(ctx, row, \"command_expired\", now)")
            .expect("expired V3 commands must terminalize their action projection");
        let delete = cleanup
            .find("table.id().delete(&row.id)")
            .expect("expired stream commands must still be deleted");
        assert!(
            terminalize < delete,
            "the action must release the phone lane before its command correlation is removed"
        );
    }

    #[test]
    fn scheduled_redetect_conflicts_preserve_the_pending_schedule_for_bounded_retry() {
        assert_eq!(
            ticket_phone_mutation_lane_conflict_reason(true, false, false, false, false),
            Some("control_code_in_progress")
        );
        assert_eq!(
            ticket_phone_mutation_lane_conflict_reason(false, true, false, false, false),
            Some("ticket_action_in_progress")
        );
        assert_eq!(
            ticket_phone_mutation_lane_conflict_reason(true, true, true, true, true),
            Some("control_code_in_progress")
        );
        assert_eq!(
            ticket_phone_mutation_lane_conflict_reason(false, false, false, false, false),
            None
        );

        let original = latest_ticket_reselect_schedule();
        let original_due = original.scheduledAt.clone();
        let original_identity = original.id.clone();
        let original_authority = original.originalDueAt.clone();
        let now = "2026-07-23T15:00:00Z";
        let (control_code_retry, retry_at_micros) =
            scheduled_redetect_deferred_schedule(original.clone(), "control_code_in_progress", now);
        assert_eq!(control_code_retry.status, "pending");
        assert_eq!(control_code_retry.id, original_identity);
        assert_eq!(control_code_retry.scheduledAt, original_due);
        assert_eq!(control_code_retry.originalDueAt, original_authority);
        assert!(control_code_retry.commandId.is_empty());
        assert!(control_code_retry.triggeredAt.is_empty());
        assert!(control_code_retry.completedAt.is_empty());
        assert_eq!(control_code_retry.resultReason, "control_code_in_progress");
        assert_eq!(control_code_retry.resultPhase, "retry_wait");
        assert_eq!(control_code_retry.retryAttempt, 1);
        assert_eq!(
            retry_at_micros,
            parse_time_micros(now) + SCHEDULED_REDETECT_RETRY_BASE_MS * 1_000
        );
        assert_eq!(
            control_code_retry.nextRetryAt.as_deref(),
            Some("2026-07-23T15:00:05+00:00")
        );

        let (v3_retry, _) = scheduled_redetect_deferred_schedule(
            original.clone(),
            "ticket_action_in_progress",
            now,
        );
        assert_eq!(v3_retry.status, "pending");
        assert_eq!(v3_retry.resultReason, "ticket_action_in_progress");
        let (reset_retry, _) =
            scheduled_redetect_deferred_schedule(original.clone(), "ticket_reset_in_progress", now);
        assert_eq!(reset_retry.status, "pending");
        assert_eq!(reset_retry.resultReason, "ticket_reset_in_progress");
        let (interaction_retry, _) =
            scheduled_redetect_deferred_schedule(original, "ticket_mutation_in_progress", now);
        assert_eq!(interaction_retry.status, "pending");
        assert_eq!(
            interaction_retry.resultReason,
            "ticket_mutation_in_progress"
        );

        for (attempt, expected_delay) in [
            (0, 5_000),
            (1, 10_000),
            (2, 20_000),
            (3, 40_000),
            (4, 60_000),
            (40, 60_000),
        ] {
            assert_eq!(scheduled_redetect_retry_delay_ms(attempt), expected_delay);
        }

        let source = include_str!("lib.rs");
        let trigger = source
            .split("fn trigger_scheduled_latest_ticket_reselect(")
            .nth(1)
            .and_then(|body| body.split("fn scheduled_redetect_retry_delay_ms(").next())
            .expect("scheduled reducer body must remain inspectable");
        assert!(trigger.contains("defer_pending_scheduled_redetect"));
        assert!(
            trigger.find("defer_pending_scheduled_redetect").unwrap()
                < trigger.find("ticket_action_v3_upsert_pending").unwrap()
        );
    }

    #[test]
    fn scheduled_redetect_retry_timer_is_recoverable_after_restart() {
        let now_micros = parse_time_micros("2026-07-23T15:00:00Z");
        let mut future = latest_ticket_reselect_schedule();
        future.scheduledAt = "2026-07-23T15:02:00Z".into();
        assert_eq!(
            scheduled_redetect_recovery_timer_micros(&future, now_micros),
            parse_time_micros(&future.scheduledAt)
        );

        future.retryAttempt = 3;
        future.nextRetryAt = Some("2026-07-23T15:01:00Z".into());
        assert_eq!(
            scheduled_redetect_recovery_timer_micros(&future, now_micros),
            parse_time_micros("2026-07-23T15:01:00Z")
        );

        future.nextRetryAt = Some("2026-07-23T14:59:00Z".into());
        assert_eq!(
            scheduled_redetect_recovery_timer_micros(&future, now_micros),
            now_micros + 40_000 * 1_000
        );

        let production = include_str!("lib.rs")
            .split("#[cfg(test)]")
            .next()
            .expect("production source must precede unit tests");
        assert_eq!(
            production
                .matches("reconcile_pending_scheduled_redetect_timers(ctx, &now);")
                .count(),
            2,
            "service setup and periodic cleanup must both restore a missing durable timer"
        );
        assert!(
            production
                .contains("row.purpose.as_deref() == Some(\"ticket_action_v3_redetect_latest\")")
        );
    }

    #[test]
    fn scheduled_ticket_action_v3_emits_exact_redetection_contract() {
        let payload = scheduled_ticket_action_v3_payload(
            "schedule-1",
            "redetect_latest",
            "scheduled_latest_ticket_reselect",
            "ticket_action_v3_redetect_latest",
            "",
            "",
            "",
            "",
        );
        let value: serde_json::Value = serde_json::from_str(&payload).unwrap();
        assert_eq!(value["version"], 3);
        assert_eq!(value["actionId"], "schedule-1");
        assert_eq!(value["target"], "redetect_latest");
        assert_eq!(value["source"], "ticket_remote_schedule");
        assert_eq!(value["attemptId"], "");
        assert_eq!(value["expectedInteractionRevision"], "");
        assert_eq!(value["scheduleId"], "schedule-1");
        assert_eq!(value["flow"], "");
        assert_eq!(value["activationRevision"], "");
        assert_eq!(value["switchExpiresAt"], "");
        assert_eq!(value["policyRevision"], "");
        let activation_payload = scheduled_ticket_action_v3_payload(
            "activation-schedule-1",
            "open_latest_unactivated",
            "activation_expiry_reset",
            "activation_expiry_reset",
            "activation-revision-1",
            "activation-attempt-1",
            "2026-08-25T01:00:00Z",
            "switch-policy-1",
        );
        let activation_value: serde_json::Value =
            serde_json::from_str(&activation_payload).unwrap();
        assert_eq!(activation_value["flow"], "activation_expiry_reset");
        assert_eq!(
            activation_value["activationRevision"],
            "activation-revision-1"
        );
        assert_eq!(
            activation_value["activationAttemptId"],
            "activation-attempt-1"
        );
        assert_eq!(activation_value["switchExpiresAt"], "2026-08-25T01:00:00Z");
        assert_eq!(activation_value["policyRevision"], "switch-policy-1");
        assert_eq!(
            ticket_action_v3_command_id("vivi-default", "pixel", "schedule-1"),
            "vivi-default:pixel:ticket_action_v3:schedule-1"
        );
        assert_eq!(
            scheduled_ticket_action_v3_target("activation_expiry_reset"),
            "open_latest_unactivated"
        );
        assert_eq!(
            scheduled_ticket_action_v3_target("latest_ticket_reselect"),
            ""
        );
        assert_eq!(
            scheduled_ticket_action_v3_target("ticket_action_v3_redetect_latest"),
            "redetect_latest"
        );
        assert_eq!(
            scheduled_ticket_purpose_class("ticket_action_v3_redetect_latest"),
            "latest_ticket_reselect"
        );
    }

    #[test]
    fn scheduled_activation_refresh_rehydrates_after_later_navigation_failure() {
        let now = "2026-08-19T12:10:00Z";
        let mut interaction = default_ticket_interaction("vivi-default", "pixel", now);
        interaction.status = "needs_attention".into();
        interaction.interactionRevision = "failed-open-action".into();
        interaction.reason = "ticket_action_selected_anchor_missing".into();
        interaction.activationRevision.clear();
        interaction.activationAt.clear();
        interaction.scheduledResetAt.clear();
        let schedule = activation_refresh_schedule();
        let history = successful_activation_history();

        let prepared = prepare_activation_refresh_interaction(
            &interaction,
            &schedule,
            &history,
            "schedule:schedule-1",
            now,
        );

        let prepared =
            prepared.expect("immutable activation proof must survive navigation failure");
        assert_eq!(prepared.status, "preparing");
        assert_eq!(prepared.interactionRevision, "schedule:schedule-1");
        assert_eq!(prepared.activationRevision, "activation-1");
        assert_eq!(prepared.activationAt, history.completedAt);
        assert_eq!(prepared.scheduledResetAt, history.refreshDueAt);
        assert!(prepared.resetRequestId.is_empty());
        assert!(prepared.ownerPublicId.is_empty());
        assert_eq!(prepared.latestProgress, 0);
        assert!(activation_history_authorizes_refresh_schedule(
            &history, &schedule
        ));

        let source = include_str!("lib.rs");
        assert!(source.contains("prepare_activation_refresh_interaction("));
        assert!(source.contains("activation_refresh_history_for_schedule(ctx, schedule)"));
        assert!(!source.contains(&["ticket_action_v3_open", "_latest_queued",].concat()));
        assert!(source.contains("interaction.interactionRevision = expected_revision.clone();"));
    }

    #[test]
    fn scheduled_v3_visual_failure_uses_immutable_history_when_interaction_was_replaced() {
        let history = successful_activation_history();
        let mut schedule = activation_refresh_schedule();
        schedule.status = "running".into();
        schedule.commandId =
            ticket_action_v3_command_id(&schedule.ticketId, &schedule.backendId, &schedule.id);

        let mut replaced_interaction = default_ticket_interaction(
            &schedule.ticketId,
            &schedule.backendId,
            "later-navigation-action",
        );
        replaced_interaction.status = "needs_attention".into();
        replaced_interaction.activationRevision.clear();
        assert!(replaced_interaction.activationRevision.is_empty());

        assert!(activation_refresh_failure_has_history_authority(
            Some(&history),
            &schedule,
        ));

        let mut mismatched_schedule = schedule;
        mismatched_schedule.activationAttemptId = Some("another-attempt".into());
        assert!(!activation_refresh_failure_has_history_authority(
            Some(&history),
            &mismatched_schedule,
        ));

        let source = include_str!("lib.rs");
        assert!(source.contains(
            "if activation_refresh_failure_has_history_authority(history.as_ref(), &existing)"
        ));
        assert!(source.contains("let _ = fail_current_activation_refresh("));
    }

    #[test]
    fn restart_reconcile_authority_comes_from_exact_immutable_history() {
        let history = successful_activation_history();
        let schedule = activation_refresh_schedule();
        assert!(activation_history_authorizes_refresh_schedule(
            &history, &schedule
        ));

        let mut wrong_attempt = schedule.clone();
        wrong_attempt.activationAttemptId = Some("other-attempt".into());
        assert!(!activation_history_authorizes_refresh_schedule(
            &history,
            &wrong_attempt
        ));

        let mut wrong_due = schedule.clone();
        wrong_due.originalDueAt = Some("2026-07-23T15:00:01Z".into());
        assert!(!activation_history_authorizes_refresh_schedule(
            &history, &wrong_due
        ));

        let mut newer = successful_activation_history();
        newer.id = "activation-history-2".into();
        newer.activationRevision = "activation-2".into();
        newer.completedAt = "2026-07-23T14:01:00Z".into();
        assert!(activation_history_success_is_newer(&newer, &history));
    }

    #[test]
    fn only_state_replaced_schedule_can_restore_with_original_due_time() {
        let mut history = successful_activation_history();
        history.refreshOutcome = "canceled".into();
        let mut schedule = activation_refresh_schedule();
        schedule.status = "canceled".into();
        schedule.resultReason = "activation_state_replaced".into();
        schedule.proofSource = "spacetimedb_bootstrap".into();
        assert!(activation_history_can_restore_state_replaced_schedule(
            &history, &schedule
        ));

        let original_due = parse_time_micros(&history.refreshDueAt);
        assert_eq!(
            activation_refresh_recovery_timer_micros(original_due, original_due - 1),
            original_due
        );
        assert_eq!(
            activation_refresh_recovery_timer_micros(original_due, original_due + 10),
            original_due + 1_000_010
        );
        assert_eq!(schedule.scheduledAt, history.refreshDueAt);
        assert_eq!(
            schedule.originalDueAt.as_deref(),
            Some(history.refreshDueAt.as_str())
        );

        schedule.resultReason = "canceled_by_admin".into();
        assert!(!activation_history_can_restore_state_replaced_schedule(
            &history, &schedule
        ));
    }

    #[test]
    fn terminal_scheduled_action_is_reconciled_instead_of_reenqueued() {
        let now = "2026-08-24T01:00:00Z";
        let mut schedule = activation_refresh_schedule();
        schedule.status = "queued".into();
        schedule.retryAttempt = 2;
        schedule.commandId =
            ticket_action_v3_command_id(&schedule.ticketId, &schedule.backendId, &schedule.id);
        let mut action = TicketremoteTicketActionV3 {
            id: ticket_action_v3_row_id(&schedule.ticketId, &schedule.backendId, &schedule.id),
            actionId: schedule.id.clone(),
            ticketId: schedule.ticketId.clone(),
            backendId: schedule.backendId.clone(),
            target: "open_latest_unactivated".into(),
            parentActionId: None,
            rootActionId: Some(schedule.id.clone()),
            retryOrdinal: 0,
            status: "failed".into(),
            phase: "failed".into(),
            currentView: "unknown".into(),
            switchAvailable: false,
            switchExpiresAt: String::new(),
            streamEpoch: "77".into(),
            frameSequence: "88".into(),
            reason: "ticket_action_visual_target_ambiguous".into(),
            createdAt: "2026-08-24T00:59:38Z".into(),
            updatedAt: "2026-08-24T00:59:40Z".into(),
            completedAt: "2026-08-24T00:59:40Z".into(),
            expiresAt: "2026-09-23T00:59:40Z".into(),
        };

        let result = activation_refresh_terminal_reconciliation(&action, &schedule, now)
            .expect("the exact terminal action must be immutable reconciliation authority");
        assert_eq!(result.schedule_status, "failed");
        assert_eq!(result.history_outcome, "failed");
        assert_eq!(result.reason, "ticket_action_visual_target_ambiguous");
        assert_eq!(result.completed_at, action.completedAt);

        let mut command = stream_command("ticket_action_v3", "activation_expiry_reset");
        command.id = schedule.commandId.clone();
        command.revision = format!("schedule:{}", schedule.id);
        command.payloadJson = scheduled_ticket_action_v3_payload(
            &schedule.id,
            "open_latest_unactivated",
            "activation_expiry_reset",
            "activation_expiry_reset",
            schedule.activationRevision.as_deref().unwrap_or(""),
            schedule.activationAttemptId.as_deref().unwrap_or(""),
            "",
            "",
        );
        assert_eq!(schedule.status, "queued");
        assert_eq!(schedule.retryAttempt, 2);
        assert!(terminal_activation_refresh_command_matches(
            &command, &schedule, &action
        ));
        command.status = "dispatched".into();
        assert!(!terminal_activation_refresh_command_matches(
            &command, &schedule, &action
        ));
        command.status = "pending".into();

        action.actionId = "different-schedule".into();
        assert!(activation_refresh_terminal_reconciliation(&action, &schedule, now).is_none());
        action.actionId = schedule.id.clone();
        action.status = "running".into();
        assert!(activation_refresh_terminal_reconciliation(&action, &schedule, now).is_none());

        let source = include_str!("lib.rs");
        let production_source = source
            .split("#[cfg(test)]")
            .next()
            .expect("production source must precede unit tests");
        assert_eq!(
            production_source
                .matches(
                    "reconcile_activation_refresh_terminal_action(ctx, &history, &existing, &now)"
                )
                .count(),
            2,
            "restore and timer trigger must stop before command insertion"
        );
        assert!(
            production_source.contains(
                "reconcile_activation_refresh_terminal_action(ctx, history, &schedule, now)"
            ),
            "bootstrap must reconcile an already queued duplicate"
        );
        assert!(
            production_source.contains(
                "retire_terminal_activation_refresh_command(ctx, schedule, &action, now)"
            ),
            "bootstrap reconciliation must retire the exact pending duplicate"
        );
    }

    #[test]
    fn succeeded_scheduled_action_requires_the_exact_visual_proof() {
        let now = "2026-08-24T01:00:00Z";
        let schedule = activation_refresh_schedule();
        let mut action = TicketremoteTicketActionV3 {
            id: ticket_action_v3_row_id(&schedule.ticketId, &schedule.backendId, &schedule.id),
            actionId: schedule.id.clone(),
            ticketId: schedule.ticketId.clone(),
            backendId: schedule.backendId.clone(),
            target: "open_latest_unactivated".into(),
            parentActionId: None,
            rootActionId: Some(schedule.id.clone()),
            retryOrdinal: 0,
            status: "succeeded".into(),
            phase: "complete".into(),
            currentView: "latest_unactivated".into(),
            switchAvailable: false,
            switchExpiresAt: String::new(),
            streamEpoch: "77".into(),
            frameSequence: "88".into(),
            reason: "ticket_action_latest_redetected".into(),
            createdAt: "2026-08-24T00:59:38Z".into(),
            updatedAt: "2026-08-24T00:59:40Z".into(),
            completedAt: "2026-08-24T00:59:40Z".into(),
            expiresAt: "2026-09-23T00:59:40Z".into(),
        };

        let result = activation_refresh_terminal_reconciliation(&action, &schedule, now)
            .expect("a successful visual proof must reconcile as success");
        assert_eq!(result.schedule_status, "succeeded");
        assert_eq!(result.history_outcome, "succeeded");

        action.frameSequence = "0".into();
        let without_video = activation_refresh_terminal_reconciliation(&action, &schedule, now).unwrap();
        assert_eq!(without_video.schedule_status, "succeeded");
        action.currentView = "unknown".into();
        let invalid = activation_refresh_terminal_reconciliation(&action, &schedule, now)
            .expect("a terminal row must never be re-enqueued even when its proof is invalid");
        assert_eq!(invalid.schedule_status, "failed");
        assert_eq!(invalid.reason, "activation_refresh_visual_proof_invalid");
    }

    #[test]
    fn activation_expiry_reset_cancellation_is_explicitly_scoped() {
        let source = include_str!("lib.rs");
        assert!(source.contains("cancel_pending_activation_expiry_schedules"));
        assert!(source.contains("activation_reset_completed"));
        assert!(source.contains("purpose.as_deref() == Some(\"activation_expiry_reset\")"));
    }

    #[test]
    fn older_phone_revision_cannot_replace_newer_activation_work() {
        let mut interaction = default_ticket_interaction("vivi-default", "pixel", "revision-new");
        interaction.status = "activated".into();
        assert!(ticket_interaction_revision_is_stale(
            &interaction,
            "unactivated_ready",
            "revision-old"
        ));
        assert!(ticket_interaction_revision_is_stale(
            &interaction,
            "needs_attention",
            "revision-old"
        ));
        assert!(!ticket_interaction_revision_is_stale(
            &interaction,
            "unactivated_ready",
            "revision-new"
        ));

        interaction.status = "needs_attention".into();
        assert!(!ticket_interaction_revision_is_stale(
            &interaction,
            "unactivated_ready",
            "revision-old"
        ));
    }

    #[test]
    fn activation_command_expiry_finalizes_pending_history_without_a_refresh() {
        let source = include_str!("lib.rs");
        assert!(source.contains("const TICKET_ACTIVATION_COMMAND_TTL_MS: i64 = 10 * 60_000;"));
        assert!(source.contains("TICKET_ACTIVATION_COMMAND_TTL_MS,\n        now"));
        assert!(source.contains("finalize_ticket_activation_failure_impl("));
        assert!(source.contains("\"ticket_activation_command_expired\""));
        assert!(source.contains("refreshOutcome: \"not_scheduled\".into()"));
    }

    #[test]
    fn fresh_proof_owns_the_new_state_and_preserves_only_reset_correlation() {
        let source = include_str!("lib.rs");
        assert!(
            source.contains("let authoritative_reset_request_id = current.resetRequestId.clone();")
        );
        assert!(source.contains("current.resetRequestId = authoritative_reset_request_id;"));
        assert!(source.contains("if !fresh_unactivated_proof {"));
        assert!(
            source.contains("current.activationRevision = bounded_text(&activationRevision, 160);")
        );
        assert!(source.contains("current.ownerPublicId = bounded_text(&ownerPublicId, 64);"));
    }

    #[test]
    fn ordinary_and_activation_expiry_schedules_are_filtered_by_purpose() {
        let source = include_str!("lib.rs");
        assert!(
            source
                .contains("scheduled_ticket_purpose_class(row.purpose.as_deref().unwrap_or(\"\"))")
        );
        assert_eq!(
            scheduled_ticket_purpose_class("activation_expiry_reset"),
            "activation_expiry_reset"
        );
        assert_eq!(
            scheduled_ticket_purpose_class("latest_ticket_reselect"),
            "latest_ticket_reselect"
        );
        assert_eq!(
            scheduled_ticket_purpose_class("ticket_action_v3_redetect_latest"),
            "latest_ticket_reselect"
        );
        assert!(source.contains("independent lifecycles"));
    }

    #[test]
    fn admin_cancellation_preserves_automatic_activation_refresh() {
        let mut manual = latest_ticket_reselect_schedule();
        manual.purpose = Some("ticket_action_v3_redetect_latest".into());
        assert!(latest_ticket_reselect_admin_cancellable(&manual));

        let automatic = activation_refresh_schedule();
        assert!(!latest_ticket_reselect_admin_cancellable(&automatic));

        let legacy = latest_ticket_reselect_schedule();
        assert!(!latest_ticket_reselect_admin_cancellable(&legacy));

        let source = include_str!("lib.rs");
        let cancel = source
            .split("fn cancel_latest_ticket_reselect(")
            .nth(1)
            .and_then(|body| {
                body.split("fn latest_ticket_reselect_admin_cancellable(")
                    .next()
            })
            .expect("cancel reducer body must remain inspectable");
        let purpose_gate = cancel
            .find("if !latest_ticket_reselect_admin_cancellable(&existing)")
            .expect("manual-purpose guard");
        assert!(purpose_gate < cancel.find("delete_latest_ticket_reselect_timers").unwrap());
        assert!(purpose_gate < cancel.find("status: \"canceled\"").unwrap());
        assert!(cancel.contains("schedule_not_manual_redetection"));
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
    fn viewer_focus_is_advisory_and_cannot_own_stream_demand() {
        let source = include_str!("lib.rs");
        let reducer = source
            .split("ticketremote_member_set_stream_focus(ctx;")
            .nth(1)
            .and_then(|body| body.split("macro_rules! member_stream_reducers").next())
            .expect("member focus reducer body");
        assert!(reducer.contains("upsert_stream_viewer_focus("));
        assert!(reducer.contains("purge_expired_stream_viewer_focus_for_ticket_backend("));
        assert!(!reducer.contains("upsert_stream_desired_state("));

        let cleanup = source
            .split("fn purge_expired_stream_viewer_focus_for_ticket(")
            .nth(1)
            .and_then(|body| {
                body.split("fn purge_expired_stream_commands_for_ticket(")
                    .next()
            })
            .expect("focus expiry cleanup body");
        assert!(!cleanup.contains("upsert_stream_desired_state("));

        let idle_authority = source
            .split("fn authoritative_stream_is_idle(")
            .nth(1)
            .and_then(|body| {
                body.split("fn relay_current_report_suppresses_background_stream_command(")
                    .next()
            })
            .expect("authoritative idle helper body");
        assert!(!idle_authority.contains("stream_viewer_focus"));
    }

    #[test]
    fn member_registration_policy_has_exact_thirty_second_boundary() {
        let now_ms = 3_600_000_i64;
        let at_boundary =
            member_limit_evaluation(now_ms, &[now_ms - REGISTRATION_RATE_INTERVAL_MS], &[], true);
        assert!(at_boundary.registration_allowed);
        assert_eq!(at_boundary.registration_count, 1);

        let one_millisecond_early = member_limit_evaluation(
            now_ms,
            &[now_ms - REGISTRATION_RATE_INTERVAL_MS + 1],
            &[],
            true,
        );
        assert!(!one_millisecond_early.registration_allowed);
        assert_eq!(
            one_millisecond_early.registration_reason,
            "registration_interval"
        );
        assert_eq!(one_millisecond_early.registration_retry_at_ms, now_ms + 1);
    }

    #[test]
    fn member_registration_policy_is_ten_per_rolling_hour() {
        let now_ms = 7_200_000_i64;
        let ten: Vec<i64> = (1..=10)
            .map(|slot| now_ms - slot * REGISTRATION_RATE_INTERVAL_MS)
            .collect();
        let full = member_limit_evaluation(now_ms, &ten, &[], true);
        assert_eq!(full.registration_count, REGISTRATION_RATE_LIMIT);
        assert!(!full.registration_allowed);
        assert_eq!(full.registration_reason, "registration_hour_limit");
        assert_eq!(
            full.registration_retry_at_ms,
            ten[9] + REGISTRATION_RATE_WINDOW_MS
        );

        let mut boundary = ten;
        boundary[9] = now_ms - REGISTRATION_RATE_WINDOW_MS;
        let released = member_limit_evaluation(now_ms, &boundary, &[], true);
        assert_eq!(released.registration_count, REGISTRATION_RATE_LIMIT - 1);
        assert!(released.registration_allowed);
    }

    #[test]
    fn member_control_code_policy_preserves_two_per_rolling_minute() {
        let now_ms = 3_600_000_i64;
        let blocked = member_limit_evaluation(
            now_ms,
            &[],
            &[now_ms - CONTROL_CODE_RATE_WINDOW_MS + 1, now_ms - 1],
            true,
        );
        assert!(!blocked.control_code_allowed);
        assert_eq!(blocked.control_code_reason, "control_code_window_limit");
        assert_eq!(blocked.control_code_retry_at_ms, now_ms + 1);

        let released = member_limit_evaluation(
            now_ms,
            &[],
            &[now_ms - CONTROL_CODE_RATE_WINDOW_MS, now_ms - 1],
            true,
        );
        assert_eq!(released.control_code_count, 1);
        assert!(released.control_code_allowed);
    }

    #[test]
    fn admin_bypass_keeps_usage_visible_without_blocking() {
        let now_ms = 7_200_000_i64;
        let registrations: Vec<i64> = (1..=10)
            .map(|slot| now_ms - slot * REGISTRATION_RATE_INTERVAL_MS)
            .collect();
        let evaluation =
            member_limit_evaluation(now_ms, &registrations, &[now_ms - 2, now_ms - 1], false);
        assert!(evaluation.registration_allowed);
        assert!(evaluation.control_code_allowed);
        assert_eq!(evaluation.registration_reason, "limits_bypassed");
        assert_eq!(evaluation.control_code_reason, "limits_bypassed");
        assert_eq!(evaluation.registration_count, 10);
        assert_eq!(evaluation.control_code_count, 2);
    }

    #[test]
    fn member_limit_event_identity_is_account_scoped_and_replay_stable() {
        let first = member_limit_event_id(
            "vivi-default",
            "first@example.com",
            "registration",
            "attempt-1",
        );
        assert_eq!(
            first,
            member_limit_event_id(
                "vivi-default",
                "first@example.com",
                "registration",
                "attempt-1",
            )
        );
        assert_ne!(
            first,
            member_limit_event_id(
                "vivi-default",
                "second@example.com",
                "registration",
                "attempt-1",
            )
        );
        assert_ne!(
            first,
            member_limit_event_id(
                "vivi-default",
                "first@example.com",
                "control_code",
                "attempt-1",
            )
        );
    }

    #[test]
    fn member_limit_boundary_uses_the_earliest_server_owned_deadline() {
        let now_ms = 3_600_000_i64;
        let registration = member_limit_evaluation(now_ms, &[now_ms - 1], &[], true);
        assert_eq!(
            registration.next_boundary_ms,
            now_ms + REGISTRATION_RATE_INTERVAL_MS - 1
        );
        let control_code = member_limit_evaluation(now_ms, &[], &[now_ms - 1], true);
        assert_eq!(
            control_code.next_boundary_ms,
            now_ms + CONTROL_CODE_RATE_WINDOW_MS - 1
        );
    }

    #[test]
    fn member_limit_public_projection_is_sanitized_and_authoritative() {
        let source = include_str!("lib.rs");
        let projection = source
            .split("pub struct TicketremoteMemberLimitState")
            .nth(1)
            .and_then(|body| {
                body.split("pub struct TicketremoteTicketSwitchAnchor")
                    .next()
            })
            .expect("member limit projection must remain inspectable");
        assert!(!projection.contains("pub email:"));
        assert!(projection.contains("pub ownerPublicId: String"));
        assert!(projection.contains("pub registrationAllowed: bool"));
        assert!(projection.contains("pub controlCodeAllowed: bool"));

        let production = source
            .split("#[cfg(test)]")
            .next()
            .expect("production source must precede tests");
        assert!(!production.contains("TICKET_ACTIVATION_SUCCESS_COOLDOWN_MS"));
        assert!(!production.contains("TICKET_ACTIVATION_WINDOW_MS"));
        assert!(production.contains("refresh_member_limit_state(ctx, DEFAULT_TICKET_ID"));
        assert!(production.contains("ticketremote_member_refresh_limit_state"));
        assert!(production.contains("ticketremote_scheduled_policy_boundary"));
    }

    #[test]
    fn member_hdr_preference_is_private_default_off_and_account_scoped() {
        // These two synthetic accounts deliberately collide under Ticket's
        // existing four-character display ID. The durable setting must not.
        let first = member_hdr_state_id("vivi-default", "synthetic-review-247@example.invalid");
        let second = member_hdr_state_id("vivi-default", "synthetic-review-259@example.invalid");
        assert_ne!(first, second);
        assert!(!first.contains("synthetic-review-247@example.invalid"));
        assert!(!second.contains("synthetic-review-259@example.invalid"));
        assert_eq!(
            account_scope_id(" Scope-Vector@Example.Invalid "),
            "801e6852dd9ec833c6627a23f54faa19d507556ff3de6378756503a1b6bb627b"
        );

        let source = include_str!("lib.rs");
        let private_schema = source
            .split("accessor = ticketremote_member_hdr_preference")
            .nth(1)
            .and_then(|body| body.split("pub struct TicketremoteMemberLimitEvent").next())
            .expect("private HDR preference schema must remain inspectable");
        assert!(!private_schema.contains("public,"));
        assert!(private_schema.contains("pub email: String"));
        assert!(private_schema.contains("pub enabled: bool"));

        let public_declaration = source
            .split("accessor = ticketremote_member_hdr_state")
            .nth(1)
            .and_then(|body| body.split("#[derive(Clone)]").next())
            .expect("public HDR state declaration must remain inspectable");
        assert!(public_declaration.contains("public,"));
        let public_schema = source
            .split("pub struct TicketremoteMemberHDRState")
            .nth(1)
            .and_then(|body| {
                body.split("pub struct TicketremoteTicketSwitchAnchor")
                    .next()
            })
            .expect("public HDR state schema must remain inspectable");
        assert!(!public_schema.contains("pub email:"));
        assert!(public_schema.contains("pub accountScopeId: String"));
        assert!(public_schema.contains("pub enabled: bool"));

        let production = source
            .split("#[cfg(test)]")
            .next()
            .expect("production source must precede tests");
        let preference_lookup = production
            .split("fn refresh_member_hdr_state(")
            .nth(1)
            .and_then(|body| body.split("fn member_limit_effective_config(").next())
            .expect("HDR state refresh must remain inspectable");
        assert!(preference_lookup.contains(".unwrap_or(false)"));
        assert!(preference_lookup.contains("if !is_member"));
        assert!(preference_lookup.contains("ticketremote_member_hdr_state()"));
    }

    #[test]
    fn member_hdr_reducer_derives_the_authenticated_account_and_role_changes_refresh_state() {
        let source = include_str!("lib.rs");
        let production = source
            .split("#[cfg(test)]")
            .next()
            .expect("production source must precede tests");
        let reducer = production
            .split("pub fn ticketremote_member_set_hdr_preference(")
            .nth(1)
            .and_then(|body| {
                body.split("pub fn ticketremote_member_refresh_hdr_state(")
                    .next()
            })
            .expect("HDR preference reducer must remain inspectable");
        assert!(reducer.contains("client_email_from_auth(ctx, &ticket.id)?"));
        assert!(!reducer.contains("email: String"));
        assert!(!reducer.contains("require_admin"));
        assert!(reducer.contains("refresh_member_hdr_state"));

        let membership = production
            .split("fn upsert_member_row(")
            .nth(1)
            .and_then(|body| body.split("fn schedule_latest_ticket_reselect(").next())
            .expect("member lifecycle must remain inspectable");
        assert!(membership.contains("refresh_member_hdr_state"));
        assert!(membership.contains("ticketremote_member_hdr_state()"));
        assert!(membership.contains("delete(member_hdr_state_id"));
        let deactivate = membership
            .split("fn deactivate_member_row(")
            .nth(1)
            .expect("member deactivation must remain inspectable");
        assert!(!deactivate.contains("ticketremote_member_hdr_preference()"));
        assert!(production.contains("refresh_member_hdr_state(ctx, DEFAULT_TICKET_ID"));
        assert!(production.contains("ticketremote_member_refresh_hdr_state"));
    }

    #[test]
    fn member_hdr_engine_is_owner_only_sanitized_and_normalizes_to_browser_v2() {
        assert_eq!(clean_hdr_engine("server_gainmap_v1"), "client_webgpu_v2");
        assert_eq!(clean_hdr_engine("client_webgpu_v1"), "client_webgpu_v2");
        assert_eq!(clean_hdr_engine("client_webgpu_v2"), "client_webgpu_v2");
        assert_eq!(clean_hdr_engine("unexpected"), "client_webgpu_v2");

        let source = include_str!("lib.rs");
        let private_declaration = source
            .split("accessor = ticketremote_member_hdr_engine_preference")
            .nth(1)
            .and_then(|body| body.split("#[derive(Clone)]").next())
            .expect("private HDR engine declaration must remain inspectable");
        assert!(!private_declaration.contains("public,"));

        let public_declaration = source
            .split("accessor = ticketremote_member_hdr_engine_state")
            .nth(1)
            .and_then(|body| body.split("#[derive(Clone)]").next())
            .expect("public HDR engine declaration must remain inspectable");
        assert!(public_declaration.contains("public,"));
        let public_schema = source
            .split("pub struct TicketremoteMemberHDREngineState")
            .nth(1)
            .and_then(|body| {
                body.split("pub struct TicketremoteTicketSwitchAnchor")
                    .next()
            })
            .expect("public HDR engine state must remain inspectable");
        assert!(!public_schema.contains("pub email:"));
        assert!(public_schema.contains("pub accountScopeId: String"));
        assert!(public_schema.contains("pub engine: String"));

        let production = source
            .split("#[cfg(test)]")
            .next()
            .expect("production source must precede tests");
        let refresh = production
            .split("fn refresh_member_hdr_engine_state(")
            .nth(1)
            .and_then(|body| body.split("fn member_limit_effective_config(").next())
            .expect("HDR engine refresh must remain inspectable");
        assert!(refresh.contains("if !is_owner"));
        assert!(refresh.contains("client_webgpu_v2"));
        assert!(refresh.contains("clean_hdr_engine(&row.engine)"));
        assert!(refresh.contains("ticketremote_member_hdr_engine_state()"));
    }

    #[test]
    fn member_hdr_engine_reducer_derives_account_and_lifecycle_retains_private_choice() {
        let source = include_str!("lib.rs");
        let production = source
            .split("#[cfg(test)]")
            .next()
            .expect("production source must precede tests");
        let reducer = production
            .split("pub fn ticketremote_owner_set_hdr_engine(")
            .nth(1)
            .and_then(|body| {
                body.split("pub fn ticketremote_member_refresh_hdr_engine_state(")
                    .next()
            })
            .expect("owner HDR engine reducer must remain inspectable");
        assert!(reducer.contains("client_email_from_auth(ctx, &ticket.id)?"));
        assert!(reducer.contains("require_owner(ctx, &ticket.id, &email)?"));
        assert!(!reducer.contains("email: String"));

        let refresh_reducer = production
            .split("pub fn ticketremote_member_refresh_hdr_engine_state(")
            .nth(1)
            .and_then(|body| {
                body.split("pub fn ticketremote_scheduled_policy_boundary(")
                    .next()
            })
            .expect("HDR engine refresh reducer must remain inspectable");
        assert!(refresh_reducer.contains("client_email_from_auth(ctx, &ticket.id)?"));
        assert!(refresh_reducer.contains("if is_owner(ctx, &ticket.id, &email)"));
        assert!(refresh_reducer.contains("migrate_legacy_member_hdr_engine_preference"));

        let migration = production
            .split("fn migrate_legacy_member_hdr_engine_preference(")
            .nth(1)
            .and_then(|body| body.split("fn refresh_member_hdr_engine_state(").next())
            .expect("legacy HDR engine migration must remain inspectable");
        assert!(migration.contains("existing.engine.trim() == \"client_webgpu_v2\""));
        assert!(migration.contains("engine: \"client_webgpu_v2\".into()"));
        assert!(migration.contains("ticketremote_member_hdr_engine_preference()"));
        assert!(migration.contains("table.insert(TicketremoteMemberHDREnginePreference"));
        assert!(migration.contains("createdAt: now.into()"));

        let membership = production
            .split("fn upsert_member_row(")
            .nth(1)
            .and_then(|body| body.split("fn schedule_latest_ticket_reselect(").next())
            .expect("member lifecycle must remain inspectable");
        assert!(membership.contains("refresh_member_hdr_engine_state"));
        assert!(membership.contains("delete(member_hdr_engine_state_id"));
        let deactivate = membership
            .split("fn deactivate_member_row(")
            .nth(1)
            .expect("member deactivation must remain inspectable");
        assert!(!deactivate.contains("ticketremote_member_hdr_engine_preference()"));
        assert!(production.contains("refresh_member_hdr_engine_state(ctx, DEFAULT_TICKET_ID"));
    }

    #[test]
    fn member_hdr_boost_is_member_available_sanitized_and_defaults_to_four() {
        for boost in [2, 3, 4, 5, 6] {
            assert_eq!(clean_hdr_display_boost(boost), boost);
        }
        for boost in [0, 1, 7, 8, 10, 12, 14, 16, 17, u32::MAX] {
            assert_eq!(clean_hdr_display_boost(boost), 4);
        }

        let source = include_str!("lib.rs");
        let private_declaration = source
            .split("accessor = ticketremote_member_hdr_boost_preference")
            .nth(1)
            .and_then(|body| body.split("#[derive(Clone)]").next())
            .expect("private HDR boost declaration must remain inspectable");
        assert!(!private_declaration.contains("public,"));
        let private_schema = source
            .split("pub struct TicketremoteMemberHDRBoostPreference")
            .nth(1)
            .and_then(|body| body.split("pub struct TicketremoteMemberLimitEvent").next())
            .expect("private HDR boost schema must remain inspectable");
        assert!(private_schema.contains("pub email: String"));
        assert!(private_schema.contains("pub selectedDisplayBoost: u32"));

        let public_declaration = source
            .split("accessor = ticketremote_member_hdr_boost_state")
            .nth(1)
            .and_then(|body| body.split("#[derive(Clone)]").next())
            .expect("public HDR boost declaration must remain inspectable");
        assert!(public_declaration.contains("public,"));
        let public_schema = source
            .split("pub struct TicketremoteMemberHDRBoostState")
            .nth(1)
            .and_then(|body| {
                body.split("pub struct TicketremoteTicketSwitchAnchor")
                    .next()
            })
            .expect("public HDR boost state must remain inspectable");
        assert!(!public_schema.contains("pub email:"));
        assert!(public_schema.contains("pub accountScopeId: String"));
        assert!(public_schema.contains("pub selectedDisplayBoost: u32"));

        let production = source
            .split("#[cfg(test)]")
            .next()
            .expect("production source must precede tests");
        let refresh = production
            .split("fn refresh_member_hdr_boost_state(")
            .nth(1)
            .and_then(|body| body.split("fn member_limit_effective_config(").next())
            .expect("HDR boost refresh must remain inspectable");
        assert!(refresh.contains("if !is_member"));
        assert!(refresh.contains("migrate_legacy_member_hdr_boost_preference"));
        assert!(refresh.contains("clean_hdr_display_boost(row.selectedDisplayBoost)"));
        assert!(refresh.contains(".unwrap_or(4)"));
        assert!(refresh.contains("ticketremote_member_hdr_boost_state()"));
    }

    #[test]
    fn member_hdr_boost_reducer_allows_active_member_admin_and_owner_accounts() {
        for role in ["member", "admin", "owner"] {
            assert_eq!(clean_role(role), role);
        }

        let source = include_str!("lib.rs");
        let production = source
            .split("#[cfg(test)]")
            .next()
            .expect("production source must precede tests");
        let reducer = production
            .split("pub fn ticketremote_owner_set_hdr_display_boost(")
            .nth(1)
            .and_then(|body| {
                body.split("pub fn ticketremote_member_refresh_hdr_boost_state(")
                    .next()
            })
            .expect("legacy-named HDR boost reducer must remain inspectable");
        assert!(reducer.contains("client_email_from_auth(ctx, &ticket.id)?"));
        assert!(!reducer.contains("require_owner"));
        assert!(!reducer.contains("require_admin"));
        assert!(reducer.contains("clean_hdr_display_boost(selectedDisplayBoost)"));
        assert!(reducer.contains("ticketremote_member_hdr_boost_preference()"));
        assert!(!reducer.contains("email: String"));

        let member_check = production
            .split("fn is_member(")
            .nth(1)
            .and_then(|body| body.split("fn is_admin(").next())
            .expect("active membership check must remain inspectable");
        assert!(member_check.contains(".map(|row| row.active)"));
        assert!(!member_check.contains("row.role"));

        let auth = production
            .split("fn client_email_from_auth(")
            .nth(1)
            .and_then(|body| body.split("fn ensure_ticket(").next())
            .expect("member authentication must remain inspectable");
        assert!(auth.contains("if !is_member(ctx, ticket_id, &email)"));
        assert!(auth.contains("ticket membership required"));

        let refresh_reducer = production
            .split("pub fn ticketremote_member_refresh_hdr_boost_state(")
            .nth(1)
            .and_then(|body| {
                body.split("pub fn ticketremote_scheduled_policy_boundary(")
                    .next()
            })
            .expect("HDR boost refresh reducer must remain inspectable");
        assert!(refresh_reducer.contains("client_email_from_auth(ctx, &ticket.id)?"));
        assert!(refresh_reducer.contains("refresh_member_hdr_boost_state"));

        let membership = production
            .split("fn upsert_member_row(")
            .nth(1)
            .and_then(|body| body.split("fn schedule_latest_ticket_reselect(").next())
            .expect("member lifecycle must remain inspectable");
        assert!(membership.contains("refresh_member_hdr_boost_state"));
        assert!(membership.contains("delete(member_hdr_boost_state_id"));
        let deactivate = membership
            .split("fn deactivate_member_row(")
            .nth(1)
            .expect("member deactivation must remain inspectable");
        assert!(!deactivate.contains("ticketremote_member_hdr_boost_preference()"));
        assert!(production.contains("refresh_member_hdr_boost_state(ctx, DEFAULT_TICKET_ID"));
    }

    #[test]
    fn member_hdr_boost_refresh_migrates_retired_values_before_projection() {
        let source = include_str!("lib.rs");
        let production = source
            .split("#[cfg(test)]")
            .next()
            .expect("production source must precede tests");
        let migration = production
            .split("fn migrate_legacy_member_hdr_boost_preference(")
            .nth(1)
            .and_then(|body| body.split("fn refresh_member_hdr_boost_state(").next())
            .expect("legacy HDR boost migration must remain inspectable");
        assert!(migration.contains("ticketremote_member_hdr_boost_preference()"));
        assert!(migration.contains("clean_hdr_display_boost(existing.selectedDisplayBoost)"));
        assert!(migration.contains("selectedDisplayBoost: selected_display_boost"));
        assert!(migration.contains("updatedAt: now.into()"));
        assert!(!migration.contains("table.insert("));

        let refresh = production
            .split("fn refresh_member_hdr_boost_state(")
            .nth(1)
            .and_then(|body| body.split("fn member_limit_effective_config(").next())
            .expect("HDR boost refresh must remain inspectable");
        let migrate_at = refresh
            .find("migrate_legacy_member_hdr_boost_preference")
            .expect("refresh must migrate retained preferences");
        let project_at = refresh
            .find("let row = TicketremoteMemberHDRBoostState")
            .expect("refresh must publish a sanitized projection");
        assert!(migrate_at < project_at);
        assert!(refresh.contains(".unwrap_or(4)"));
        assert!(refresh.contains("if !is_member"));
        assert!(refresh.contains("table.id().delete(id)"));
    }

    #[test]
    fn activation_and_control_code_share_the_account_policy_ledger() {
        let source = include_str!("lib.rs");
        let activation = source
            .split("fn activation_admission(")
            .nth(1)
            .and_then(|body| body.split("fn activation_admission_for_action(").next())
            .expect("activation admission must remain inspectable");
        assert!(
            activation.find("existing_decision").unwrap()
                < activation.find("admit_member_limit_event(").unwrap(),
            "exact replay must resolve before consuming an account event"
        );
        let control_code = source
            .split("ticketremote_member_request_control_code(ctx;")
            .nth(1)
            .and_then(|body| {
                body.split("ticketremote_member_request_ticket_reset(ctx;")
                    .next()
            })
            .expect("control-code admission must remain inspectable");
        assert!(control_code.contains("admit_control_code_request_impl("));
        let shared_admission = source
            .split("fn admit_control_code_request_impl(")
            .nth(1)
            .and_then(|body| body.split("member_reducers! {").next())
            .expect("shared control-code admission must remain inspectable");
        assert!(shared_admission.contains("admit_member_limit_event("));
        assert!(shared_admission.contains("\"control_code\""));
    }

    #[test]
    fn ticket_actions_and_control_codes_share_one_deferred_phone_lane_slot() {
        let source = include_str!("lib.rs");
        let ticket_queue = source
            .split("fn queue_ticket_action_v3_intent(")
            .nth(1)
            .and_then(|body| body.split("fn queue_control_code_intent(").next())
            .expect("ticket queue helper");
        let control_queue = source
            .split("fn queue_control_code_intent(")
            .nth(1)
            .and_then(|body| body.split("fn finish_queued_control_code_request(").next())
            .expect("control-code queue helper");
        for helper in [ticket_queue, control_queue] {
            assert!(helper.contains("ticket_action_v3_queue_id("));
            assert!(helper.contains("ticketremote_ticket_action_v3_queued_intent()"));
            assert!(!helper.contains("admit_member_limit_event("));
        }
        assert!(control_queue.contains("privatePayloadJson"));
        let public_command = control_queue
            .split("ticketremote_stream_command()")
            .nth(1)
            .expect("queued public command");
        assert!(!public_command.contains("digits"));

        let member_control = source
            .split("ticketremote_member_request_control_code(ctx;")
            .nth(1)
            .and_then(|body| {
                body.split("ticketremote_member_request_ticket_reset(ctx;")
                    .next()
            })
            .expect("member control-code reducer");
        assert!(member_control.contains("ticket_phone_mutation_lane_conflict("));
        assert!(member_control.contains("queue_control_code_intent("));
        assert!(
            member_control.find("queue_control_code_intent(").unwrap()
                < member_control
                    .find("admit_control_code_request_impl(")
                    .unwrap()
        );

        let promotion = source
            .split("fn promote_ticket_action_v3_queue(")
            .nth(1)
            .and_then(|body| body.split("fn ticket_action_v3_rejection_plan(").next())
            .expect("shared queue promotion");
        assert!(promotion.contains("intent.kind == \"control_code\""));
        assert!(promotion.contains("admit_control_code_request_impl("));
        assert!(promotion.contains("finish_queued_control_code_request("));
        assert!(promotion.contains("!is_member("));
        assert!(promotion.contains("request_ticket_action_v3_impl("));
    }

    #[test]
    fn switch_anchor_requires_a_strictly_later_unactivated_proof() {
        let mut anchor = TicketremoteTicketSwitchAnchor {
            id: "vivi-default:pixel".into(),
            ticketId: "vivi-default".into(),
            backendId: "pixel".into(),
            activationAttemptId: "attempt-1".into(),
            activationRevision: "activation-1".into(),
            activationAt: "2026-08-25T12:00:00Z".into(),
            expiresAt: "2026-08-25T12:15:00Z".into(),
            latestUnactivatedProofActionId: "proof-1".into(),
            latestUnactivatedProofAt: "2026-08-25T12:00:00Z".into(),
            currentView: "latest_unactivated".into(),
            policyRevision: "switch-policy-1".into(),
            updatedAt: "2026-08-25T12:00:00Z".into(),
        };
        assert!(!ticket_switch_anchor_has_later_unactivated_proof(&anchor));
        anchor.latestUnactivatedProofAt = "2026-08-25T12:00:00.001Z".into();
        assert!(ticket_switch_anchor_has_later_unactivated_proof(&anchor));
        assert_eq!(
            parse_time_ms(&anchor.expiresAt) - parse_time_ms(&anchor.activationAt),
            TICKET_ACTION_SWITCH_WINDOW_MS
        );
    }

    #[test]
    fn switch_projection_values_enable_only_the_anchor_current_view() {
        let anchor = TicketremoteTicketSwitchAnchor {
            id: "vivi-default:pixel".into(),
            ticketId: "vivi-default".into(),
            backendId: "pixel".into(),
            activationAttemptId: "attempt-1".into(),
            activationRevision: "activation-1".into(),
            activationAt: "2026-08-25T12:00:00Z".into(),
            expiresAt: "2026-08-25T12:15:00Z".into(),
            latestUnactivatedProofActionId: "proof-1".into(),
            latestUnactivatedProofAt: "2026-08-25T12:00:00.001Z".into(),
            currentView: "latest_unactivated".into(),
            policyRevision: "switch-policy-1".into(),
            updatedAt: "2026-08-25T12:00:00.001Z".into(),
        };
        let row = TicketremoteTicketActionV3 {
            id: "vivi-default:pixel:return-1".into(),
            actionId: "return-1".into(),
            ticketId: "vivi-default".into(),
            backendId: "pixel".into(),
            target: "return_to_latest_unactivated".into(),
            parentActionId: None,
            rootActionId: Some("return-1".into()),
            retryOrdinal: 0,
            status: "succeeded".into(),
            phase: "complete".into(),
            currentView: "latest_unactivated".into(),
            switchAvailable: false,
            switchExpiresAt: String::new(),
            streamEpoch: "101".into(),
            frameSequence: "202".into(),
            reason: "ticket_action_target_visible".into(),
            createdAt: "2026-08-25T12:01:00Z".into(),
            updatedAt: "2026-08-25T12:01:00Z".into(),
            completedAt: "2026-08-25T12:01:00Z".into(),
            expiresAt: "2026-08-25T13:01:00Z".into(),
        };

        assert_eq!(
            ticket_switch_projection_values(&row, Some(&anchor)),
            (true, anchor.expiresAt.clone())
        );
        assert_eq!(
            ticket_switch_projection_values(&row, None),
            (false, String::new())
        );

        let mut opposite_view = row.clone();
        opposite_view.currentView = "recent_activated".into();
        assert_eq!(
            ticket_switch_projection_values(&opposite_view, Some(&anchor)),
            (false, String::new())
        );

        let mut nonterminal = row;
        nonterminal.status = "running".into();
        assert_eq!(
            ticket_switch_projection_values(&nonterminal, Some(&anchor)),
            (false, String::new())
        );
    }

    #[test]
    fn switch_anchor_view_change_refreshes_all_public_action_projections() {
        let source = include_str!("lib.rs");
        let note = source
            .split("fn note_ticket_switch_visual_result(")
            .nth(1)
            .and_then(|body| body.split("fn ticket_switch_projection_for_view(").next())
            .expect("switch visual result handler must remain inspectable");
        let anchor_update = note
            .find(".update(updated);")
            .expect("switch result must update the durable anchor");
        let projection_refresh = note
            .find("refresh_ticket_switch_action_projections(")
            .expect("anchor change must sanitize every public switch projection");
        assert!(
            anchor_update < projection_refresh,
            "public projections must refresh only after the new anchor view is durable"
        );
    }

    #[test]
    fn every_v3_payload_carries_spacetime_switch_policy_fields() {
        let source = include_str!("lib.rs");
        let immediate = source
            .split("fn request_ticket_action_v3_impl(")
            .nth(1)
            .and_then(|body| body.split("fn request_ticket_reset_impl(").next())
            .expect("immediate V3 path must remain inspectable");
        assert!(immediate.contains("\"switchExpiresAt\""));
        assert!(immediate.contains("\"policyRevision\""));
        assert!(immediate.contains("ticket_action_v3_switch_authority("));
        let update = source
            .split("fn update_ticket_action_v3_projection(")
            .nth(1)
            .and_then(|body| {
                body.split("pub fn ticketremote_update_ticket_action_v3(")
                    .next()
            })
            .expect("shared V3 projection path must remain inspectable");
        assert!(update.contains("ticket_switch_projection_for_view("));
        let compatibility = source
            .split("pub fn ticketremote_update_ticket_action_v3(")
            .nth(1)
            .and_then(|body| {
                body.split("pub fn ticketremote_update_ticket_action_v3_with_slider_region(")
                    .next()
            })
            .expect("compatibility V3 update path must remain inspectable");
        assert!(compatibility.contains("Compatibility parameters are deliberately ignored"));
    }

    #[test]
    fn activation_refresh_deadline_is_exactly_one_hour() {
        let activation_at_ms = 1_000_000_i64;
        let refresh_due_ms = activation_refresh_due_at_ms(activation_at_ms);
        assert_eq!(refresh_due_ms - activation_at_ms, 60 * 60 * 1000);
    }

    #[test]
    fn activation_success_requires_the_current_attempt_correlation() {
        assert!(activation_correlation_matches_current(
            "manual_slider",
            "slider-1",
            "slider-1",
            ""
        ));
        assert!(!activation_correlation_matches_current(
            "manual_slider",
            "slider-1",
            "slider-2",
            ""
        ));
        assert!(activation_correlation_matches_current(
            "reset_and_activate",
            "reset-1",
            "",
            "reset-1"
        ));
        assert!(!activation_correlation_matches_current(
            "reset_and_activate",
            "reset-1",
            "",
            "reset-2"
        ));
    }

    #[test]
    fn ticket_action_v3_contract_allowlists_only_public_targets_and_views() {
        for target in [
            "open_latest_unactivated",
            "open_latest_and_register",
            "register_current",
            "show_recent_activated",
            "return_to_latest_unactivated",
            "redetect_latest",
            "prove_current",
        ] {
            assert_eq!(ticket_action_v3_target(target), target);
        }
        assert!(ticket_action_v3_target("force_ticket_reselect").is_empty());
        for view in [
            "latest_unactivated",
            "recent_activated",
            "activated_current",
            "unknown",
        ] {
            assert_eq!(ticket_action_v3_view(view), view);
        }
        assert_eq!(ticket_action_v3_view("ticket_detail"), "unknown");
        assert_eq!(
            ticket_action_v3_public_reason("ticket_action_registered", "ticket_action_updated"),
            "ticket_action_registered"
        );
        for reason in [
            "ticket_action_selected_anchor_missing",
            "ticket_action_transition_anchor_missing",
            "ticket_action_selected_anchor_conflict",
            "ticket_action_detail_identity_conflict",
            "ticket_action_gesture_rejected",
            "ticket_action_gesture_completed_no_transition",
            "ticket_action_post_gesture_visual_unproved",
        ] {
            assert_eq!(
                ticket_action_v3_public_reason(reason, "ticket_action_updated"),
                reason
            );
        }
        assert_eq!(
            ticket_action_v3_public_reason(
                "/data/local/pixel-stack/card=secret 12,34,56,78",
                "ticket_action_updated"
            ),
            "ticket_action_updated"
        );
    }

    #[test]
    fn ticket_action_v3_user_rejection_commits_terminal_projection() {
        let (status, phase, reason, emit_command) =
            ticket_action_v3_rejection_plan("slider_proof_stale");
        assert_eq!(status, "failed");
        assert_eq!(phase, "rejected");
        assert_eq!(reason, "slider_proof_stale");
        assert!(!emit_command);
        assert!(ticket_action_v3_committed_rejection().is_ok());
    }

    #[test]
    fn explicit_v3_action_and_reauth_queue_behind_an_active_read_only_proof() {
        let source = include_str!("lib.rs");
        let production = source
            .split("#[cfg(test)]")
            .next()
            .expect("production module source");
        assert!(!production.contains("supersede_read_only_ticket_actions_for_mutation"));

        let request = source
            .split("fn request_ticket_action_v3_impl(")
            .nth(1)
            .and_then(|body| body.split("fn request_ticket_reset_impl(").next())
            .expect("immediate V3 request path must remain inspectable");
        let lane = request
            .find("ticket_phone_mutation_lane_conflict(")
            .expect("mutating action must use the shared phone lane");
        let queue = request
            .find("return queue_ticket_action_v3_intent(")
            .expect("explicit action must use the durable queue when the lane is occupied");
        assert!(request.contains("if target != \"prove_current\""));
        assert!(lane < queue);

        let reauth = source
            .split("fn owner_request_vivi_reauth(")
            .nth(1)
            .and_then(|body| body.split("#[spacetimedb::reducer]").next())
            .expect("ViVi reauthentication request path must remain inspectable");
        let reauth_lane = reauth
            .find("ticket_phone_mutation_lane_conflict(")
            .expect("ViVi reauthentication must use the shared phone lane");
        let reauth_queue = reauth
            .find("return queue_vivi_reauth_intent(")
            .expect("ViVi reauthentication must queue when the lane is occupied");
        assert!(reauth_lane < reauth_queue);
    }

    #[test]
    fn only_an_uncorrelated_command_free_obsolete_proof_is_an_idempotent_finalization() {
        assert!(ticket_action_v3_obsolete_read_only_finalization(
            "prove_current",
            None,
            false,
            "",
            "",
        ));
        for terminal_status in ["failed", "needs_attention"] {
            assert!(ticket_action_v3_obsolete_read_only_finalization(
                "prove_current",
                Some(("prove_current", terminal_status)),
                false,
                "",
                "",
            ));
        }
        for live_or_successful_status in ["pending", "running", "succeeded"] {
            assert!(!ticket_action_v3_obsolete_read_only_finalization(
                "prove_current",
                Some(("prove_current", live_or_successful_status)),
                false,
                "",
                "",
            ));
        }
        assert!(!ticket_action_v3_obsolete_read_only_finalization(
            "prove_current",
            None,
            true,
            "",
            "",
        ));
        assert!(!ticket_action_v3_obsolete_read_only_finalization(
            "prove_current",
            Some(("register_current", "failed")),
            false,
            "",
            "",
        ));
        assert!(!ticket_action_v3_obsolete_read_only_finalization(
            "prove_current",
            None,
            false,
            "attempt-1",
            "",
        ));
        assert!(!ticket_action_v3_obsolete_read_only_finalization(
            "prove_current",
            None,
            false,
            "",
            "activation-1",
        ));
        for target in [
            "open_latest_unactivated",
            "open_latest_and_register",
            "register_current",
            "redetect_latest",
            "show_recent_activated",
            "return_to_latest_unactivated",
        ] {
            assert!(!ticket_action_v3_obsolete_read_only_finalization(
                target, None, false, "", "",
            ));
        }
    }

    #[test]
    fn ticket_action_v3_registration_uses_only_a_live_visual_action_watermark() {
        let now = "2026-08-24T12:00:00Z";
        let mut row = TicketremoteTicketActionV3 {
            id: "row-1".into(),
            actionId: "open-proof-1".into(),
            ticketId: "vivi-default".into(),
            backendId: "pixel".into(),
            target: "open_latest_unactivated".into(),
            parentActionId: None,
            rootActionId: Some("open-proof-1".into()),
            retryOrdinal: 0,
            status: "succeeded".into(),
            phase: "complete".into(),
            currentView: "latest_unactivated".into(),
            switchAvailable: false,
            switchExpiresAt: String::new(),
            streamEpoch: "101".into(),
            frameSequence: "202".into(),
            reason: "ticket_action_target_visible".into(),
            createdAt: now.into(),
            updatedAt: now.into(),
            completedAt: now.into(),
            expiresAt: "2026-08-24T13:00:00Z".into(),
        };
        assert!(ticket_action_v3_registration_proof_row_valid(
            &row,
            "open-proof-1",
            now,
        ));
        row.currentView = "recent_activated".into();
        assert!(!ticket_action_v3_registration_proof_row_valid(
            &row,
            "open-proof-1",
            now,
        ));
        row.currentView = "latest_unactivated".into();
        row.frameSequence = "0".into();
        assert!(!ticket_action_v3_registration_proof_row_valid(
            &row,
            "open-proof-1",
            now,
        ));
    }

    #[test]
    fn prove_current_geometry_is_normalized_exact_and_short_lived() {
        let now = "2026-08-24T12:00:00Z";
        let action = TicketremoteTicketActionV3 {
            id: "row-proof-current".into(),
            actionId: "proof-current-1".into(),
            ticketId: "vivi-default".into(),
            backendId: "pixel".into(),
            target: "prove_current".into(),
            parentActionId: None,
            rootActionId: Some("proof-current-1".into()),
            retryOrdinal: 0,
            status: "succeeded".into(),
            phase: "complete".into(),
            currentView: "latest_unactivated".into(),
            switchAvailable: false,
            switchExpiresAt: String::new(),
            streamEpoch: "101".into(),
            frameSequence: "202".into(),
            reason: "ticket_action_target_visible".into(),
            createdAt: now.into(),
            updatedAt: now.into(),
            completedAt: now.into(),
            expiresAt: "2026-08-24T12:05:00Z".into(),
        };
        let mut region = TicketremoteTicketSliderRegionV3 {
            id: "vivi-default:pixel".into(),
            ticketId: "vivi-default".into(),
            backendId: "pixel".into(),
            proofActionId: "proof-current-1".into(),
            streamEpoch: "101".into(),
            frameSequence: "202".into(),
            leftBasisPoints: 1200,
            topBasisPoints: 7000,
            rightBasisPoints: 8800,
            bottomBasisPoints: 7600,
            updatedAt: now.into(),
            expiresAt: "2026-08-24T12:05:00Z".into(),
        };
        assert!(ticket_slider_region_v3_matches_action(
            &region, &action, now
        ));
        region.frameSequence = "203".into();
        assert!(!ticket_slider_region_v3_matches_action(
            &region, &action, now
        ));
        region.frameSequence = "202".into();
        region.rightBasisPoints = 10_001;
        assert!(!ticket_slider_region_v3_matches_action(
            &region, &action, now
        ));
        assert!(!ticket_slider_region_v3_bounds_valid(100, 200, 100, 300));
        let atomic = ticket_slider_region_v3_row_for_action(
            "vivi-default",
            "pixel",
            &action,
            TicketSliderRegionV3Input {
                left_basis_points: 1200,
                top_basis_points: 7000,
                right_basis_points: 8800,
                bottom_basis_points: 7600,
            },
            now,
        )
        .expect("matching terminal proof and geometry must form one atomic row");
        assert_eq!(atomic.proofActionId, "proof-current-1");
        assert_eq!(atomic.streamEpoch, "101");
        assert_eq!(atomic.frameSequence, "202");
        assert_eq!(
            parse_time_ms(&atomic.expiresAt) - parse_time_ms(now),
            TICKET_SLIDER_REGION_V3_TTL_MS
        );
    }

    #[test]
    fn pixel_terminal_projection_has_additive_atomic_reducer_and_keeps_legacy_fallback() {
        let source = include_str!("lib.rs");
        let atomic = source
            .split("pub fn ticketremote_update_ticket_action_v3_with_slider_region(")
            .nth(1)
            .and_then(|body| {
                body.split("pub fn ticketremote_update_ticket_slider_region_v3(")
                    .next()
            })
            .expect("atomic Pixel reducer must remain present");
        assert!(atomic.contains("require_service(ctx)?"));
        assert!(atomic.contains("ticket_action_terminal_projection_required"));
        assert!(atomic.contains("update_ticket_action_v3_projection("));
        assert!(atomic.contains("hasSliderRegion.then_some"));
        assert!(source.contains("pub fn ticketremote_update_ticket_action_v3("));
        assert!(source.contains("pub fn ticketremote_update_ticket_slider_region_v3("));
    }

    #[test]
    fn newer_v3_visual_proof_fences_and_clears_legacy_interaction_authority() {
        let now = "2026-08-24T12:00:00Z";
        let mut current =
            default_ticket_interaction("vivi-default", "pixel", "2026-08-24T11:59:00Z");
        current.status = "needs_attention".into();
        current.interactionRevision = "legacy-retry-revision".into();
        current.activationRevision = "old-activation".into();
        current.activationAt = "2026-08-24T11:45:00Z".into();
        current.scheduledResetAt = "2026-08-24T12:45:00Z".into();
        current.sliderRight = 999;
        current.controlId = "old-control".into();
        current.leasePhase = "active".into();
        let action = TicketremoteTicketActionV3 {
            id: "row-open-2".into(),
            actionId: "open-proof-2".into(),
            ticketId: "vivi-default".into(),
            backendId: "pixel".into(),
            target: "open_latest_unactivated".into(),
            parentActionId: None,
            rootActionId: Some("open-proof-2".into()),
            retryOrdinal: 0,
            status: "succeeded".into(),
            phase: "complete".into(),
            currentView: "latest_unactivated".into(),
            switchAvailable: true,
            switchExpiresAt: "2026-08-24T12:15:00Z".into(),
            streamEpoch: "41".into(),
            frameSequence: "52".into(),
            reason: "ticket_action_target_visible".into(),
            createdAt: now.into(),
            updatedAt: now.into(),
            completedAt: now.into(),
            expiresAt: "2026-08-24T13:00:00Z".into(),
        };

        let reconciled =
            reconcile_legacy_interaction_after_ticket_action_v3(&current, &action, None, now)
                .expect("fresh V3 proof supersedes retained compatibility state");
        assert_eq!(reconciled.status, "needs_attention");
        assert_eq!(reconciled.interactionRevision, "open-proof-2");
        assert_eq!(reconciled.streamEpoch, "41");
        assert_eq!(reconciled.frameSequence, "52");
        assert_eq!(reconciled.activationRevision, "old-activation");
        assert_eq!(reconciled.activationAt, "2026-08-24T11:45:00Z");
        assert_eq!(reconciled.scheduledResetAt, "2026-08-24T12:45:00Z");
        assert_eq!(reconciled.sliderRight, 0);
        assert!(reconciled.controlId.is_empty());
        assert_eq!(reconciled.leasePhase, "none");
        assert!(ticket_interaction_update_is_stale(
            &reconciled,
            "unactivated_ready",
            "legacy-retry-revision",
        ));
    }

    #[test]
    fn v3_registration_terminal_state_keeps_exact_revision_without_retry_authority() {
        let now = "2026-08-24T12:01:00Z";
        let mut current =
            default_ticket_interaction("vivi-default", "pixel", "2026-08-24T12:00:30Z");
        current.status = "control_active".into();
        current.interactionRevision = "open-proof-2".into();
        current.controlId = "register-1".into();
        current.leasePhase = "active".into();
        current.latestProgress = 10_000;
        let mut command = stream_command("ticket_action_v3", "ticket_action_requested");
        command.revision = "open-proof-2".into();
        command.payloadJson = r#"{"actionId":"register-1","target":"register_current"}"#.into();
        command.expiresAt = "2026-08-24T12:00:59Z".into();
        assert!(ticket_interaction_v3_activation_command_matches(
            &command, &current,
        ));
        command.payloadJson = r#"{"actionId":"open-3","target":"open_latest_unactivated"}"#.into();
        assert!(!ticket_interaction_v3_activation_command_matches(
            &command, &current,
        ));
        let mut action = TicketremoteTicketActionV3 {
            id: "row-register-1".into(),
            actionId: "register-1".into(),
            ticketId: "vivi-default".into(),
            backendId: "pixel".into(),
            target: "register_current".into(),
            parentActionId: None,
            rootActionId: Some("register-1".into()),
            retryOrdinal: 0,
            status: "needs_attention".into(),
            phase: "needs_attention".into(),
            currentView: "latest_unactivated".into(),
            switchAvailable: false,
            switchExpiresAt: String::new(),
            streamEpoch: "41".into(),
            frameSequence: "53".into(),
            reason: "ticket_action_activation_dispatch_uncertain".into(),
            createdAt: now.into(),
            updatedAt: now.into(),
            completedAt: now.into(),
            expiresAt: "2026-08-24T13:00:00Z".into(),
        };

        let uncertain = reconcile_legacy_interaction_after_ticket_action_v3(
            &current,
            &action,
            Some("open-proof-2"),
            now,
        )
        .expect("exact in-flight revision is reconciled");
        assert_eq!(uncertain.status, "needs_attention");
        assert_eq!(uncertain.interactionRevision, "open-proof-2");
        assert!(uncertain.controlId.is_empty());
        assert_eq!(uncertain.leasePhase, "none");

        action.status = "failed".into();
        action.reason = "ticket_action_slider_unproved".into();
        let failed = reconcile_legacy_interaction_after_ticket_action_v3(
            &current,
            &action,
            Some("open-proof-2"),
            now,
        )
        .expect("safe terminal failure clears the exact attempt");
        assert_eq!(failed.status, "failed");
        assert_eq!(failed.interactionRevision, "open-proof-2");
        assert!(
            reconcile_legacy_interaction_after_ticket_action_v3(
                &current,
                &action,
                Some("different-revision"),
                now,
            )
            .is_none()
        );
    }

    #[test]
    fn terminal_composite_v3_fences_late_compatibility_and_ack_cleans_it_again() {
        let now = "2026-08-24T12:02:00Z";
        let mut current =
            default_ticket_interaction("vivi-default", "pixel", "2026-08-24T12:01:00Z");
        current.status = "reset_queued".into();
        current.interactionRevision = "composite-1".into();
        current.resetRequestId = "composite-1".into();
        current.controlId = "old-control".into();
        current.leasePhase = "active".into();
        current.sliderRight = 900;
        let action = TicketremoteTicketActionV3 {
            id: ticket_action_v3_row_id("vivi-default", "pixel", "composite-1"),
            actionId: "composite-1".into(),
            ticketId: "vivi-default".into(),
            backendId: "pixel".into(),
            target: "open_latest_and_register".into(),
            parentActionId: None,
            rootActionId: Some("composite-1".into()),
            retryOrdinal: 0,
            status: "failed".into(),
            phase: "failed".into(),
            currentView: "latest_unactivated".into(),
            switchAvailable: false,
            switchExpiresAt: String::new(),
            streamEpoch: "61".into(),
            frameSequence: "71".into(),
            reason: "ticket_action_slider_unproved".into(),
            createdAt: now.into(),
            updatedAt: now.into(),
            completedAt: now.into(),
            expiresAt: "2026-08-24T13:02:00Z".into(),
        };

        let after_terminal = reconcile_legacy_interaction_after_ticket_action_v3(
            &current,
            &action,
            Some("composite-1"),
            now,
        )
        .expect("terminal update clears the in-flight compatibility row");
        assert_eq!(after_terminal.status, "failed");
        assert_eq!(after_terminal.interactionRevision, "composite-1");
        assert!(ticket_action_v3_terminal_composite_fences_interaction(
            &action,
            "vivi-default",
            "pixel",
            "composite-1",
        ));

        // Model a publication already in flight when the terminal projection
        // committed. The terminal row fences this update; if it landed before
        // the fence was observed, acknowledgement performs the same cleanup a
        // second time before deleting the command correlation.
        let mut late_compatibility = after_terminal;
        late_compatibility.status = "needs_attention".into();
        late_compatibility.resetRequestId = "composite-1".into();
        late_compatibility.controlId = "legacy-control".into();
        late_compatibility.leasePhase = "active".into();
        late_compatibility.streamEpoch = "999".into();
        late_compatibility.frameSequence = "999".into();
        late_compatibility.phoneDisplayWidth = 540;
        late_compatibility.sliderRight = 999;
        let after_ack = reconcile_legacy_interaction_after_ticket_action_v3(
            &late_compatibility,
            &action,
            Some("composite-1"),
            now,
        )
        .expect("acknowledgement re-cleans an exact terminal compatibility row");
        assert_eq!(after_ack.status, "failed");
        assert_eq!(after_ack.interactionRevision, "composite-1");
        assert!(after_ack.resetRequestId.is_empty());
        assert!(after_ack.controlId.is_empty());
        assert_eq!(after_ack.leasePhase, "none");
        assert_eq!(after_ack.streamEpoch, "0");
        assert_eq!(after_ack.frameSequence, "0");
        assert_eq!(after_ack.phoneDisplayWidth, 0);
        assert_eq!(after_ack.sliderRight, 0);
    }

    #[test]
    fn expired_exact_v3_command_stays_out_of_synthetic_legacy_retry() {
        let now = "2026-08-24T12:02:00Z";
        let mut current =
            default_ticket_interaction("vivi-default", "pixel", "2026-08-24T12:01:00Z");
        current.status = "reset_queued".into();
        current.interactionRevision = "composite-expired".into();
        let mut command = stream_command("ticket_action_v3", "ticket_action_requested");
        command.revision = current.interactionRevision.clone();
        command.payloadJson =
            r#"{"actionId":"composite-expired","target":"open_latest_and_register"}"#.into();
        command.expiresAt = "2026-08-24T12:01:59Z".into();

        assert!(parse_time_ms(&command.expiresAt) <= parse_time_ms(now));
        assert!(ticket_interaction_v3_activation_command_matches(
            &command, &current,
        ));
        let synthetic =
            repair_ticket_interaction_for_retry(&current, now, "ticket_reset_command_expired")
                .expect("legacy repair would otherwise mint a new revision");
        assert_ne!(synthetic.interactionRevision, current.interactionRevision);
    }

    #[test]
    fn ticket_action_v3_activation_targets_require_attempt_correlation() {
        assert!(ticket_action_v3_is_activation("open_latest_and_register"));
        assert!(ticket_action_v3_is_activation("register_current"));
        assert!(!ticket_action_v3_is_activation("open_latest_unactivated"));
        assert!(!ticket_action_v3_is_activation("show_recent_activated"));
        assert!(ticket_action_v3_failure_requires_activation_cleanup(
            "register_current",
            Some("failed")
        ));
        assert!(ticket_action_v3_failure_requires_activation_cleanup(
            "open_latest_and_register",
            Some("needs_attention")
        ));
        assert!(!ticket_action_v3_failure_requires_activation_cleanup(
            "register_current",
            Some("succeeded")
        ));
        assert!(!ticket_action_v3_failure_requires_activation_cleanup(
            "open_latest_unactivated",
            Some("failed")
        ));
        assert!(ticket_reset_command_is_relevant(
            "ticket_action_v3",
            r#"{"target":"open_latest_unactivated"}"#
        ));
        assert!(!ticket_reset_command_is_relevant(
            "ticket_action_v3",
            r#"{"target":"open_latest_and_register"}"#
        ));
    }

    #[test]
    fn ticket_action_v3_duplicate_is_idempotent_without_replay() {
        assert!(ticket_action_v3_duplicate_result("register_current", "register_current").is_ok());
        assert_eq!(
            ticket_action_v3_duplicate_result("register_current", "open_latest_unactivated"),
            Err("ticket_action_id_reused".into())
        );
    }

    #[test]
    fn second_window_queue_defers_admission_and_atomic_cleanup_releases_it() {
        assert_eq!(ticket_action_v3_status("queued"), "queued");
        let source = include_str!("lib.rs");
        let queue_body = source
            .split("fn queue_ticket_action_v3_intent(")
            .nth(1)
            .and_then(|body| body.split("fn promote_ticket_action_v3_queue(").next())
            .expect("queue helper body");
        assert!(queue_body.contains("status: \"queued\""));
        assert!(queue_body.contains("ticketremote_ticket_action_v3_queued_intent"));
        assert!(!queue_body.contains("activation_admission_for_action("));

        let cleanup_body = source
            .split("pub fn ticketremote_complete_control_code_cleanup_ready(")
            .nth(1)
            .and_then(|body| {
                body.split("pub fn ticketremote_update_ticket_interaction(")
                    .next()
            })
            .expect("atomic cleanup reducer body");
        assert!(cleanup_body.contains("cleanupPending: Some(false)"));
        assert!(cleanup_body.contains("\"fast_ready\""));
        assert!(cleanup_body.contains("promote_ticket_action_v3_queue"));

        let expiry_body = source
            .split("fn purge_expired_stream_commands_for_ticket(")
            .nth(1)
            .and_then(|body| {
                body.split("fn purge_expired_stream_viewer_focus_for_ticket_backend(")
                    .next()
            })
            .expect("stream-command expiry body");
        let delete_position = expiry_body
            .rfind("table.id().delete(&row.id)")
            .expect("expired command deletion");
        let promotion_position = expiry_body
            .find("promote_ticket_action_v3_queue(ctx, &ticket_id, backend_id, now)")
            .expect("queued action promotion after expiry");
        assert!(promotion_position > delete_position);

        let promotion = source
            .split("fn promote_ticket_action_v3_queue(")
            .nth(1)
            .and_then(|body| body.split("fn ticket_action_v3_rejection_plan(").next())
            .expect("shared queue promotion body");
        let lane_guard = promotion
            .find("ticket_phone_mutation_lane_conflict_ignoring_control_request(")
            .expect("live owner guard");
        let intent_delete = promotion
            .find("ticketremote_ticket_action_v3_queued_intent()\n            .id()\n            .delete")
            .expect("queued intent deletion");
        assert!(lane_guard < intent_delete);
        assert!(promotion.contains("ignored_control_request_id"));
    }

    #[test]
    fn ticket_action_v3_smart_switch_requires_matching_fresh_authority() {
        let now = "2026-08-24T12:00:00Z";
        let future = "2026-08-24T12:15:00Z";
        assert!(ticket_action_v3_switch_allowed(
            "show_recent_activated",
            "latest_unactivated",
            true,
            future,
            now,
        ));
        assert!(ticket_action_v3_switch_allowed(
            "return_to_latest_unactivated",
            "recent_activated",
            true,
            future,
            now,
        ));
        assert!(!ticket_action_v3_switch_allowed(
            "show_recent_activated",
            "recent_activated",
            true,
            future,
            now,
        ));
        assert!(!ticket_action_v3_switch_allowed(
            "show_recent_activated",
            "latest_unactivated",
            false,
            future,
            now,
        ));
        assert!(!ticket_action_v3_switch_allowed(
            "show_recent_activated",
            "latest_unactivated",
            true,
            now,
            now,
        ));
    }

    #[test]
    fn ticket_action_v3_public_switch_expiry_is_capped_at_fifteen_minutes() {
        let now = "2026-08-24T12:00:00Z";
        assert!(ticket_action_v3_switch_expiry_valid(
            "2026-08-24T12:15:00Z",
            now
        ));
        assert!(!ticket_action_v3_switch_expiry_valid(now, now));
        assert!(!ticket_action_v3_switch_expiry_valid(
            "2026-08-24T12:15:00.001Z",
            now
        ));
    }

    #[test]
    fn control_code_delivery_cannot_regress_a_generated_or_terminal_result() {
        assert!(control_code_status_update_allowed("running", "generated"));
        assert!(control_code_status_update_allowed("generated", "succeeded"));
        assert!(control_code_status_update_allowed("generated", "failed"));
        assert!(control_code_status_update_allowed("succeeded", "succeeded"));
        assert!(control_code_status_update_allowed("succeeded", "failed"));
        for current in ["generated", "succeeded", "failed", "closed", "expired"] {
            for progress in ["queued", "running"] {
                assert!(!control_code_status_update_allowed(current, progress));
            }
        }
        for current in ["failed", "closed", "expired"] {
            assert!(!control_code_status_update_allowed(current, "succeeded"));
        }
    }

    #[test]
    fn control_code_result_proof_accepts_only_mode_specific_generated_tokens() {
        assert_eq!(
            clean_control_code_result_proof("phone_visual_generated_inline"),
            "phone_visual_generated_inline"
        );
        assert_eq!(
            clean_control_code_result_proof("phone_visual_generated_with_close"),
            "phone_visual_generated_with_close"
        );
        assert_eq!(
            clean_control_code_result_proof("phone_visual_generated_unknown"),
            ""
        );
    }
}
