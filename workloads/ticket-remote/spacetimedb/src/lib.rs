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
mod maintenance;
use maintenance::{maintenance_paused, require_new_phone_admission};
mod cold_restart;
mod idle_refresh;
mod monitoring;
mod member_activity;
use member_activity::*;
mod action_statistics;
use action_statistics::*;
mod viewer_privacy;

fn account_scope_id(email: &str) -> String {
    let normalized = email.trim().to_ascii_lowercase();
    format!("{:x}", Sha256::digest(normalized.as_bytes()))
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
// The database owner may use operational SQL and the narrow maintenance gate.
// All other reducer writes require member or service authorization below.
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
const LATEST_TICKET_RESELECT_MAX_HORIZON_MS: i64 = 90 * 24 * 60 * 60 * 1000;
const TICKET_ACTIVATION_RESET_DELAY_MS: i64 = 60 * 60 * 1000;
const TICKET_ACTION_SWITCH_WINDOW_MS: i64 = 15 * 60 * 1000;
// Slider geometry is an ephemeral capability tied to one exact visual proof.
// The browser refreshes the non-mutating proof before this expires; raw phone
// coordinates never enter the public row.
const SCHEDULED_REDETECT_RETRY_BASE_MS: i64 = 5_000;
const SCHEDULED_REDETECT_RETRY_MAX_MS: i64 = 60_000;
const TICKET_ACTIVATION_LEDGER_TTL_MS: i64 = 30 * 24 * 60 * 60 * 1000;
const TICKET_ACTIVATION_CLEANUP_BATCH_SIZE: u32 = 10_000;
const TICKET_ACTIVATION_CLEANUP_INTERVAL_SECS: u64 = 24 * 60 * 60;
const TICKET_ACTIVATION_CATCHUP_DELAY_SECS: u64 = 60;
const CONTROL_CODE_PHONE_TTL_MS: i64 = 105_000;
const VIVI_REAUTH_COMMAND_TTL_MS: i64 = 3 * 60_000;
const VIVI_REAUTH_FULL_RESET_REQUEST_PREFIX: &str = "vivi-full-reset-";
const VIVI_REAUTH_LOGOUT_LOGIN_REQUEST_PREFIX: &str = "vivi-logout-login-";
const VIVI_REAUTH_LOGOUT_REDETECT_LOGIN_REQUEST_PREFIX: &str = "vivi-logout-redetect-login-";
const STREAM_VIEWER_FOCUS_TTL_MS: i64 = 90_000;
const SAFE_JSON_MAX_BYTES: usize = 4096;
// Public/durable "live" authority is intentionally narrower than visual
// continuity. LIVE_OK and DEGRADED frames may remain visible, but only a
// conservatively timed LIVE_FRESH frame may suppress recovery work.
const STREAM_BACKGROUND_SUPPRESS_FALLBACK_MAX_AGE_MS: i64 = 3_000;
const STREAM_BACKGROUND_REPORT_MAX_AGE_MS: i64 = 5_000;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum ViviReauthMode {
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
        let table = $ctx.db.$table();
        if table.id().find(&row.id).is_some() {
            table.id().update(row)
        } else {
            table.insert(row)
        }
    }};
}

macro_rules! apply_changes {
    ($row:expr, $changes:expr; $($field:ident),+ $(,)?) => {
        $(if let Some(value) = $changes.$field { $row.$field = value; })+
    };
}

mod checkin;
mod invitations;

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
    ($ctx:expr, $ticket:expr, $bound:expr, $limit:expr, $deleted:expr; $($table:ident),+ $(,)?) => {$({
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
    })+};
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
        // Keep terminal actions ahead of their schedules: replay requires that correlation.
        purge_expired_rows!(
            $ctx, $ticket, $bound, $limit, $deleted;
            ticketremote_control_code_fast_state,
            ticketremote_safe_operational_log,
            ticketremote_ticket_interaction,
            ticketremote_ticket_action_v3_queued_intent,
            ticketremote_ticket_action_v3,
            ticketremote_latest_ticket_reselect_schedule,
            ticketremote_vivi_reauth_attempt,
            ticketremote_vivi_reauth_owner,
            ticketremote_ticket_slider_region_v3,
            ticketremote_member_limit_event,
            ticketremote_member_daily_activity,
            ticketremote_member_daily_actions,
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
            let $email = match stringify!($name) {
                "ticketremote_member_set_stream_focus" => invitations::viewer_actor($ctx, &$ticket.id)?,
                "ticketremote_member_confirm_control_code_browser_capture" | "ticketremote_member_close_control_code" => invitations::result_actor($ctx, &$ticket.id)?,
                _ => client_email_from_auth($ctx, &$ticket.id)?,
            };
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
    // Old totals contain no slot history. Their first new write establishes
    // a coverage floor after lastTickSlot, preserving counts without guessing.
    #[default(None::<i64>)]
    pub coverageFloorSlot: Option<i64>,
    #[default(None::<Vec<u8>>)]
    pub slotCoverage: Option<Vec<u8>>,
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

#[spacetimedb::table(accessor = ticketremote_stream_desired_state,
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
    #[default(None::<String>)]
    pub coldRestartId: Option<String>,
    #[default(None::<String>)]
    pub coldRestartPhase: Option<String>,
    #[default(None::<String>)]
    pub coldRestartStartedAt: Option<String>,
    #[default(None::<String>)]
    pub coldRestartError: Option<String>,
}

#[spacetimedb::table(accessor = ticketremote_stream_viewer_focus,
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
    #[default(None::<String>)]
    pub email: Option<String>,
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

#[spacetimedb::table(accessor = ticketremote_phone_current_report)]
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
    #[default(None::<String>)]
    pub terminalFingerprint: Option<String>,
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

#[spacetimedb::table(accessor = ticketremote_relay_current_report)]
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
    #[default(None::<String>)]
    pub resultMarkerRevision: Option<String>,
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
    TicketremoteServiceMemberDailyActions from TicketremoteMemberDailyActions
        with service_member_daily_actions_from_row {
        id: String, ticketId: String, accountScopeId: String, day: String,
        registrationAttempts: Vec<u32>, registrationSuccesses: Vec<u32>,
        controlCodeAttempts: Vec<u32>, controlCodeSuccesses: Vec<u32>, expiresAt: String
    }
}
cloned_projection! {
    TicketremoteServiceActionStatisticsTracking from TicketremoteActionStatisticsTracking
        with service_action_statistics_tracking_from_row {
        id: String, startedAt: String
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
    let Some(binding) = member_view_binding(ctx, true) else {
        return Vec::new();
    };
    ctx.db
        .ticketremote_vivi_credentials()
        .ticketBackend()
        .filter((&binding.ticketId,))
        .map(|row| vivi_credential_view_from_row(&row))
        .collect()
}

fn member_view_binding(ctx: &ViewContext, owner_only: bool) -> Option<TicketremoteMemberIdentity> {
    if !owner_only {
        if let Some(guest) = invitations::guest_view_binding(ctx) { return Some(guest); }
    }
    let binding = ctx
        .db
        .ticketremote_member_identity()
        .byIdentity()
        .filter(&ctx.sender())
        .next()?;
    ctx.db
        .ticketremote_ticket_member()
        .id()
        .find(member_id(&binding.ticketId, &binding.email))
        .filter(|member| member.active && (!owner_only || member.role == "owner"))
        .map(|_| binding)
}

cloned_projection! {
    TicketremoteMemberTicketSwitch from TicketremoteTicketSwitchAnchor
        with member_ticket_switch_from_row {
        id: String, ticketId: String, backendId: String, currentView: String, expiresAt: String
    }
}

#[spacetimedb::view(accessor = ticketremote_member_ticket_switch, public, primary_key = id)]
pub fn ticketremote_member_ticket_switch_view(
    ctx: &ViewContext,
) -> Vec<TicketremoteMemberTicketSwitch> {
    let Some(binding) = member_view_binding(ctx, false) else {
        return Vec::new();
    };
    ctx.db
        .ticketremote_ticket_switch_anchor()
        .ticketId()
        .filter(&binding.ticketId)
        .filter(|anchor| {
            ticket_switch_anchor_policy_valid(anchor)
                && ticket_switch_anchor_has_later_unactivated_proof(anchor)
        })
        .map(|row| member_ticket_switch_from_row(&row))
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
    ticketremote_service_member_daily_actions =>
        ticketremote_service_member_daily_actions_view -> TicketremoteServiceMemberDailyActions
    |ctx, ticket| {
        ctx.db.ticketremote_member_daily_actions().ticketDay().filter((&ticket,))
            .map(|row| service_member_daily_actions_from_row(&row)).collect()
    }
    ticketremote_service_action_statistics_tracking =>
        ticketremote_service_action_statistics_tracking_view -> TicketremoteServiceActionStatisticsTracking
    |ctx, ticket| {
        ctx.db.ticketremote_action_statistics_tracking().id().find(&ticket)
            .map(|row| vec![service_action_statistics_tracking_from_row(&row)]).unwrap_or_default()
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
            "refresh_current_ticket",
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
            "idle_refresh_requested",
            "idle_refresh_viewer_returned",
            "idle_refresh_missed_cycle",
            "idle_refresh_phone_busy",
            "idle_refresh_blocked",
            "ticket_action_current_ticket_refreshed",
            "ticket_action_idle_refresh_cancelled",
            "ticket_action_idle_refresh_unavailable",
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
            ticket_switch_anchor_policy_valid(anchor)
                && parse_time_micros(&anchor.expiresAt) > parse_time_micros(now)
        })
}

fn ticket_switch_anchor_policy_valid(anchor: &TicketremoteTicketSwitchAnchor) -> bool {
    !anchor.policyRevision.trim().is_empty()
        && parse_time_micros(&anchor.expiresAt)
            <= parse_time_micros(&anchor.activationAt)
                .saturating_add(TICKET_ACTION_SWITCH_WINDOW_MS.saturating_mul(1_000))
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

fn ticket_action_v3_has_registration_authority(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    context: &str,
    now: &str,
) -> bool {
    context.starts_with("pc-")
        && ctx
            .db
            .ticketremote_phone_control_state()
            .id()
            .find(phone_row_id(ticket_id, backend_id))
            .is_some_and(|row| phone_control_registration_ready(&row, context, now))
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
) -> Option<&'static str> {
    if control_code_busy {
        Some("control_code_in_progress")
    } else if ticket_action_v3_busy {
        Some("ticket_action_in_progress")
    } else if vivi_reauth_busy {
        Some("vivi_reauth_in_progress")
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
    );
    conflict
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
        terminalFingerprint: None,
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
    ctx.db.ticketremote_ticket_action_v3().insert(row)
}

fn ticket_action_v3_finish_without_command(
    ctx: &ReducerContext,
    row: TicketremoteTicketActionV3,
    reason: &str,
    now: &str,
) {
    invitations::settle_action(ctx, &row.ticketId, &row.actionId, false, true);
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
    let row = table.insert(row);
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
    let mut payload = vivi_reauth_intent(credential_revision, mode);
    payload["requestId"] = request_id.into();
    payload["version"] = match mode {
        ViviReauthMode::FullResetV2 => 2,
        ViviReauthMode::LogoutLoginV3 => 3,
        ViviReauthMode::LogoutLoginRedetectV4 => 4,
    }
    .into();
    payload.to_string()
}

fn vivi_reauth_intent(credential_revision: &str, mode: ViviReauthMode) -> serde_json::Value {
    let mut payload = serde_json::json!({ "credentialRevision": credential_revision });
    let action = if mode == ViviReauthMode::FullResetV2 {
        "resetAppData"
    } else {
        "logoutInApp"
    };
    payload[action] = true.into();
    if mode == ViviReauthMode::LogoutLoginRedetectV4 {
        payload["redetectAfterLogin"] = true.into();
    }
    payload
}

fn vivi_reauth_queued_intent_payload(credential_revision: &str, mode: ViviReauthMode) -> String {
    vivi_reauth_intent(credential_revision, mode).to_string()
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

enum QueuedPhoneIntent<'a> {
    Reauth {
        revision: &'a str,
        mode: ViviReauthMode,
    },
    Ticket {
        target: &'a str,
        source: &'a str,
        reason: &'a str,
        attempt: &'a str,
        expected_revision: &'a str,
        schedule: &'a str,
    },
    Code {
        session: &'a str,
        digits: &'a str,
        expected_revision: &'a str,
    },
}

fn queued_phone_command_id(ticket: &str, backend: &str, action: &str, kind: &str) -> String {
    match kind {
        "vivi_reauth" => vivi_reauth_command_id(ticket, backend, action),
        "control_code" => format!("{action}:generate_control_code"),
        _ => ticket_action_v3_command_id(ticket, backend, action),
    }
}

// Reservation, durable status and command placeholder are one reducer transaction.
// The private intent is re-authorized when the phone lane becomes available.
fn queue_phone_intent(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    action_id: &str,
    email: &str,
    now: &str,
    intent: QueuedPhoneIntent<'_>,
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
    let mut row = TicketremoteTicketActionV3QueuedIntent {
        id: queue_id,
        ticketId: clean_ticket_id(ticket_id),
        backendId: clean_backend_id(backend_id),
        actionId: action_id.into(),
        requestedEmail: email.into(),
        createdAt: now.into(),
        kind: String::new(),
        target: String::new(),
        source: String::new(),
        reason: String::new(),
        attemptId: String::new(),
        expectedInteractionRevision: String::new(),
        scheduleId: String::new(),
        privatePayloadJson: "{}".into(),
        expiresAt: String::new(),
    };
    let (command_type, revision, reason, payload, ttl) = match intent {
        QueuedPhoneIntent::Reauth { revision, mode } => {
            insert_vivi_reauth_attempt(
                ctx,
                ticket_id,
                backend_id,
                action_id,
                revision,
                email,
                "queued",
                "waiting_for_phone_lane",
                "queued",
                now,
            )?;
            row.kind = "vivi_reauth".into();
            row.source = "ticket_remote_admin".into();
            row.reason = "vivi_reauth_queued".into();
            row.privatePayloadJson = vivi_reauth_queued_intent_payload(revision, mode);
            (
                "vivi_reauth",
                revision,
                "vivi_reauth_queued",
                json_object(&[
                    ("requestId", action_id),
                    ("credentialRevision", revision),
                    ("queueSlot", "1"),
                ]),
                VIVI_REAUTH_COMMAND_TTL_MS,
            )
        }
        QueuedPhoneIntent::Ticket {
            target,
            source,
            reason,
            attempt,
            expected_revision,
            schedule,
        } => {
            let action = ticket_action_v3_upsert_pending(
                ctx, ticket_id, backend_id, action_id, target, reason, now,
            );
            ctx.db
                .ticketremote_ticket_action_v3()
                .id()
                .update(TicketremoteTicketActionV3 {
                    status: "queued".into(),
                    phase: "waiting_for_phone_lane".into(),
                    reason: "ticket_action_queued".into(),
                    ..action
                });
            row.kind = "ticket_action_v3".into();
            row.target = target.into();
            row.source = source.into();
            row.reason = reason.into();
            row.attemptId = attempt.into();
            row.expectedInteractionRevision = expected_revision.into();
            row.scheduleId = schedule.into();
            ("ticket_action_v3", action_id, "ticket_action_queued",
                serde_json::json!({
                    "version": 3, "actionId": action_id, "target": target, "source": source,
                    "reason": "ticket_action_queued", "attemptId": attempt,
                    "expectedInteractionRevision": expected_revision, "scheduleId": schedule, "queueSlot": 1,
                }).to_string(), TICKET_ACTIVATION_COMMAND_TTL_MS)
        }
        QueuedPhoneIntent::Code {
            session,
            digits,
            expected_revision,
        } => {
            row.kind = "control_code".into();
            row.source = "browser_spacetime".into();
            row.reason = "control_code_queued".into();
            row.privatePayloadJson = safe_json_string(&serde_json::json!({
                "sessionId": session, "digits": digits, "expectedFastRevision": expected_revision,
            }).to_string(), SAFE_JSON_MAX_BYTES);
            insert_control_code_public_request(
                ctx,
                ticket_id,
                action_id,
                &account_public_id(email),
                now,
            );
            (
                "generate_control_code",
                action_id,
                "control_code_queued",
                json_object(&[("requestId", action_id), ("queueSlot", "1")]),
                CONTROL_CODE_PHONE_TTL_MS,
            )
        }
    };
    row.expiresAt = command_expires_at(now, ttl);
    ctx.db
        .ticketremote_stream_command()
        .insert(TicketremoteStreamCommand {
            id: queued_phone_command_id(ticket_id, backend_id, action_id, &row.kind),
            ticketId: row.ticketId.clone(),
            backendId: row.backendId.clone(),
            commandType: command_type.into(),
            status: "queued".into(),
            revision: revision.into(),
            reason: reason.into(),
            payloadJson: safe_json_string(&payload, SAFE_JSON_MAX_BYTES),
            createdAt: now.into(),
            updatedAt: now.into(),
            expiresAt: row.expiresAt.clone(),
        });
    ctx.db
        .ticketremote_ticket_action_v3_queued_intent()
        .insert(row);
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
    invitations::settle_action(ctx, ticket_id, request_id, false, true);
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
    if cold_restart::ticket_blocked(ctx, ticket_id) { return; }
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
    let command_id = queued_phone_command_id(ticket_id, backend_id, &intent.actionId, &intent.kind);
    ctx.db
        .ticketremote_ticket_action_v3_queued_intent()
        .id()
        .delete(&queue_id);
    ctx.db
        .ticketremote_stream_command()
        .id()
        .delete(&command_id);
    if intent.kind == "vivi_reauth" {
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
    let rejection = if parse_time_ms(&intent.expiresAt) <= parse_time_ms(now) {
        Some("command_expired")
    } else if !is_member(ctx, ticket_id, &intent.requestedEmail)
        && !invitations::is_guest_actor(ctx, ticket_id, &intent.requestedEmail) {
        Some("membership_required")
    } else {
        None
    };
    if intent.kind == "control_code" {
        delete_control_code_request(ctx, &intent.actionId);
        if let Some(reason) = rejection {
            finish_queued_control_code_request(
                ctx,
                ticket_id,
                &intent.actionId,
                &intent.requestedEmail,
                reason,
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
    ctx.db
        .ticketremote_ticket_action_v3()
        .id()
        .delete(ticket_action_v3_row_id(
            ticket_id,
            backend_id,
            &intent.actionId,
        ));
    let rejected = match rejection {
        Some(reason) => Some(reason),
        None => request_ticket_action_v3_impl(
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
        .err()
        .map(|_| "ticket_action_rejected"),
    };
    if let Some(reason) = rejected {
        let row = ticket_action_v3_upsert_pending(
            ctx,
            ticket_id,
            backend_id,
            &intent.actionId,
            &intent.target,
            reason,
            now,
        );
        ticket_action_v3_finish_without_command(ctx, row, reason, now);
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

// Both presentation choices have the same private persistence and public
// projection lifecycle. Only their field, normalization and default differ.
macro_rules! hdr_preference {
    ($state_id:ident, $refresh:ident,
     $preference:ident => $Preference:ident, $state:ident => $State:ident,
     $setter:ident, $refresher:ident,
     $suffix:literal, $field:ident: $kind:ty = $default:expr, $normalize:path) => {
        fn $state_id(ticket_id: &str, email: &str) -> String {
            format!(
                "{}:{}:{}",
                clean_ticket_id(ticket_id),
                account_scope_id(email),
                $suffix
            )
        }

        fn $refresh(ctx: &ReducerContext, ticket_id: &str, email: &str, now: &str) {
            let ticket_id = clean_ticket_id(ticket_id);
            let email = clean_email(email);
            let id = $state_id(&ticket_id, &email);
            if !is_member(ctx, &ticket_id, &email) && !invitations::is_guest_actor(ctx, &ticket_id, &email) {
                ctx.db.$state().id().delete(id);
                return;
            }
            let value = ctx
                .db
                .$preference()
                .id()
                .find(member_id(&ticket_id, &email))
                .map(|row| $normalize(row.$field))
                .unwrap_or($default);
            upsert_row!(
                ctx,
                $state,
                $State {
                    id,
                    ticketId: ticket_id,
                    accountScopeId: account_scope_id(&email),
                    $field: value,
                    updatedAt: now.into(),
                    serverAt: now.into(),
                }
            );
        }

        #[spacetimedb::reducer]
        pub fn $setter(
            ctx: &ReducerContext,
            ticketId: String,
            $field: $kind,
        ) -> Result<(), String> {
            let now = now(ctx);
            let ticket = ensure_ticket(ctx, &ticketId, "", &now);
            let email = invitations::viewer_actor(ctx, &ticket.id)?;
            let id = member_id(&ticket.id, &email);
            let table = ctx.db.$preference();
            let $field = $normalize($field);
            if let Some(existing) = table.id().find(&id) {
                table.id().update($Preference {
                    $field,
                    updatedAt: now.clone(),
                    ..existing
                });
            } else {
                table.insert($Preference {
                    id,
                    ticketId: ticket.id.clone(),
                    email: email.clone(),
                    $field,
                    createdAt: now.clone(),
                    updatedAt: now.clone(),
                });
            }
            $refresh(ctx, &ticket.id, &email, &now);
            Ok(())
        }

        #[spacetimedb::reducer]
        pub fn $refresher(ctx: &ReducerContext, ticketId: String) -> Result<(), String> {
            let now = now(ctx);
            let ticket = ensure_ticket(ctx, &ticketId, "", &now);
            let email = invitations::viewer_actor(ctx, &ticket.id)?;
            $refresh(ctx, &ticket.id, &email, &now);
            Ok(())
        }
    };
}

hdr_preference!(
    member_hdr_state_id, refresh_member_hdr_state,
    ticketremote_member_hdr_preference => TicketremoteMemberHDRPreference,
    ticketremote_member_hdr_state => TicketremoteMemberHDRState,
    ticketremote_member_set_hdr_preference, ticketremote_member_refresh_hdr_state,
    "member-hdr", enabled: bool = false, std::convert::identity
);
hdr_preference!(
    member_hdr_boost_state_id, refresh_member_hdr_boost_state,
    ticketremote_member_hdr_boost_preference => TicketremoteMemberHDRBoostPreference,
    ticketremote_member_hdr_boost_state => TicketremoteMemberHDRBoostState,
    ticketremote_owner_set_hdr_display_boost, ticketremote_member_refresh_hdr_boost_state,
    "member-hdr-boost", selectedDisplayBoost: u32 = 4, clean_hdr_display_boost
);

fn member_hdr_engine_state_id(ticket_id: &str, email: &str) -> String {
    format!(
        "{}:{}:member-hdr-engine",
        clean_ticket_id(ticket_id),
        account_scope_id(email)
    )
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
    ctx.db
        .ticketremote_member_limit_event()
        .ticketEmailKindAt()
        .filter((&ticket_id, &email, kind))
        .filter(|row| row.counted)
        .map(|row| parse_time_ms(&row.admittedAt))
        .filter(|at| *at > cutoff)
        .collect()
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
    let row = upsert_row!(ctx, ticketremote_member_limit_state, row);
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
    let current = refresh_member_limit_state(ctx, &ticket_id, &email, now);
    let effective_limited = current.effectiveLimited;
    let (allowed, reason, retry_at) = if kind == "registration" {
        (
            current.registrationAllowed,
            current.registrationReason,
            current.registrationRetryAt,
        )
    } else {
        (
            current.controlCodeAllowed,
            current.controlCodeReason,
            current.controlCodeRetryAt,
        )
    };
    if !allowed {
        return Ok(MemberLimitAdmission {
            allowed,
            reason,
            retry_at,
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

fn activation_admission(
    ctx: &ReducerContext,
    ticket_id: &str,
    backend_id: &str,
    email: &str,
    flow: &str,
    attempt_id: &str,
    context: &str,
    now: &str,
) -> Result<MemberLimitAdmission, String> {
    let fingerprint = terminal_result_fingerprint(&serde_json::json!([
        ticket_id, backend_id, email, flow, attempt_id, context
    ]));
    if let Some(history) = activation_history_for_attempt(ctx, ticket_id, attempt_id) {
        if history.inputFingerprint != fingerprint {
            return Err("activation_attempt_id_reused".into());
        }
        return Ok(MemberLimitAdmission {
            allowed: history.admission == "admitted",
            reason: history.reason,
            retry_at: String::new(),
        });
    }
    let admission =
        admit_member_limit_event(ctx, ticket_id, email, "registration", attempt_id, now)?;
    if !admission.allowed {
        record_activation_rejection(ctx, ticket_id, backend_id, flow, &admission.reason, now);
        return Ok(admission);
    }
    ctx.db
        .ticketremote_activation_history()
        .insert(TicketremoteActivationHistory {
            id: activation_history_id(ticket_id, backend_id, attempt_id),
            ticketId: ticket_id.into(),
            backendId: backend_id.into(),
            flow: flow.into(),
            admission: "admitted".into(),
            outcome: "pending".into(),
            reason: "activation_admitted".into(),
            occurredAt: now.into(),
            occurrenceDay: activation_day_bucket(now),
            admittedAt: now.into(),
            updatedAt: now.into(),
            completedAt: String::new(),
            attemptId: attempt_id.into(),
            interactionRevision: context.into(),
            interactionCorrelation: attempt_id.into(),
            activationRevision: String::new(),
            inputFingerprint: fingerprint,
            refreshDueAt: String::new(),
            refreshCompletedAt: String::new(),
            refreshOutcome: String::new(),
            refreshRetryAt: String::new(),
            refreshAttempt: 0,
            occurrenceCount: 1,
            expiresAt: add_ms(now, TICKET_ACTIVATION_LEDGER_TTL_MS),
        });
    Ok(admission)
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
            return;
        }
    }
    upsert_row!(
        ctx,
        ticketremote_ticket_switch_anchor,
        TicketremoteTicketSwitchAnchor {
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
        }
    );
    replace_policy_boundary_timer(
        ctx,
        &ticket_id,
        "switch",
        &backend_id,
        parse_time_ms(&expires_at),
        now,
    );
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
            "open_latest_unactivated" | "return_to_latest_unactivated" | "redetect_latest"
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
        return Ok(());
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
    Ok(())
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
    if target.is_empty() || target == "refresh_current_ticket" {
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
    if ticket_phone_mutation_lane_conflict(ctx, &ticket.id, &backend_id, now).is_some() {
        return queue_phone_intent(
            ctx,
            &ticket.id,
            &backend_id,
            action_id,
            email,
            now,
            QueuedPhoneIntent::Ticket {
                target: &target,
                source: &source,
                reason: &public_reason,
                attempt: attempt_id,
                expected_revision: &expected_revision,
                schedule: schedule_id.trim(),
            },
        );
    }

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
        command_revision = expected_revision.clone();
    }
    if activation_target {
        let flow = if target == "register_current" {
            "menu_activate"
        } else {
            "reset_and_activate"
        };
        let admission = activation_admission(
            ctx,
            &ticket.id,
            &backend_id,
            email,
            flow,
            attempt_id,
            &command_revision,
            now,
        )?;
        if !admission.allowed {
            ticket_action_v3_finish_without_command(ctx, action_row, &admission.reason, now);
            return ticket_action_v3_committed_rejection();
        }
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
    if invitations::connect_guest(ctx)? { return Ok(()); }
    let email = client_email_from_auth(ctx, DEFAULT_TICKET_ID)?;
    let now = now(ctx);
    upsert_member_identity(ctx, DEFAULT_TICKET_ID, &email, &now);
    refresh_member_limit_state(ctx, DEFAULT_TICKET_ID, &email, &now);
    refresh_member_hdr_state(ctx, DEFAULT_TICKET_ID, &email, &now);

    refresh_member_hdr_boost_state(ctx, DEFAULT_TICKET_ID, &email, &now);
    Ok(())
}

#[spacetimedb::reducer(client_disconnected)]
pub fn identity_disconnected(_ctx: &ReducerContext) {}

// Retained for open pre-v213 pages; remove after those deployed callers drain.
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
pub fn ticketremote_record_member_activity_slots(
    ctx: &ReducerContext,
    ticketId: String,
    email: String,
    slots: Vec<i64>,
) -> Result<(), String> {
    require_service(ctx)?;
    let ticket_id = clean_ticket_id(&ticketId);
    let email = clean_email(&email);
    if email.is_empty() || !is_member(ctx, &ticket_id, &email) {
        return Err("member_not_active".into());
    }
    // Validate the whole batch before changing any day. Reducer transactions
    // also make a lost acknowledgement safe to retry without counting twice.
    let buckets = member_activity_slots(ctx.timestamp, &slots)?;
    upsert_member_activity_slots(ctx, &ticket_id, &email, &now(ctx), &buckets);
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
    let email = invitations::result_actor(ctx, &ticket.id)?;
    refresh_member_limit_state(ctx, &ticket.id, &email, &now);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_owner_set_hdr_engine(
    ctx: &ReducerContext,
    ticketId: String,
    engine: String,
) -> Result<(), String> {
    let _ = engine;
    let ticket_id = clean_ticket_id(&ticketId);
    let email = client_email_from_auth(ctx, &ticket_id)?;
    require_owner(ctx, &ticket_id, &email)
}

#[spacetimedb::reducer]
pub fn ticketremote_member_refresh_hdr_engine_state(
    ctx: &ReducerContext,
    ticketId: String,
) -> Result<(), String> {
    let ticket_id = clean_ticket_id(&ticketId);
    let email = client_email_from_auth(ctx, &ticket_id)?;
    if is_member(ctx, &ticket_id, &email) {
        Ok(())
    } else {
        Err("membership_required".into())
    }
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
        "checkin" => checkin::expire(ctx, &arg.ticketId, &arg.subjectId),
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

// Old clients keep their exact wire signatures and can only receive a reload rejection.
macro_rules! rejected_phone_reducers {
    ($gate:ident; $($name:ident [$($before:ident: $before_ty:ty),*] {
        $($arg:ident: $kind:ty,)*
    })+) => {$(
        #[spacetimedb::reducer]
        #[allow(unused_variables)]
        pub fn $name(
            ctx: &ReducerContext,
            $($before: $before_ty,)*
            ticketId: String,
            backendId: String,
            $($arg: $kind,)*
        ) -> Result<(), String> {
            rejected_phone_reducers!(@reject $gate, ctx, ticketId, backendId)
        }
    )+};
    (@reject member, $ctx:expr, $ticket:expr, $backend:expr) => {
        require_legacy_phone_admission($ctx, &$ticket, &$backend)
    };
    (@reject worker, $ctx:expr, $ticket:expr, $backend:expr) => {{
        require_service($ctx)?;
        Err("ticket_worker_reload_required".into())
    }};
    (@reject service_client, $ctx:expr, $ticket:expr, $backend:expr) => {{
        require_service($ctx)?;
        Err("ticket_client_reload_required".into())
    }};
}

rejected_phone_reducers! { member;
    ticketremote_member_request_control_code [] {
        sessionId: String,
        digits: String,
        expectedFastRevision: String,
    }
    ticketremote_member_request_ticket_reset [] {
        resetRequestId: String,
        reason: String,
    }
    ticketremote_member_request_ticket_reset_v2 [] {
        resetRequestId: String,
        reason: String,
        attemptId: String,
    }
    ticketremote_member_request_ticket_action_v3 [version: u32] {
        actionId: String,
        target: String,
        source: String,
        reason: String,
        attemptId: String,
        expectedInteractionRevision: String,
        scheduleId: String,
    }
    ticketremote_member_activate_ticket_button [] {
        interactionRevision: String,
        controlId: String,
        inputSequence: String,
    }
    ticketremote_member_activate_ticket_button_v2 [] {
        interactionRevision: String,
        controlId: String,
        inputSequence: String,
        attemptId: String,
    }
    ticketremote_member_claim_ticket_slider [] {
        interactionRevision: String,
        controlId: String,
        initialInputSequence: String,
        holdDurationMillis: u32,
        horizontalTravelCss: u32,
        verticalTravelCss: u32,
        initialProgress: u32,
    }
    ticketremote_member_claim_ticket_slider_v2 [] {
        interactionRevision: String,
        controlId: String,
        initialInputSequence: String,
        holdDurationMillis: u32,
        horizontalTravelCss: u32,
        verticalTravelCss: u32,
        initialProgress: u32,
        attemptId: String,
    }
    ticketremote_member_update_ticket_slider [] {
        interactionRevision: String,
        controlId: String,
        inputSequence: String,
        inputPhase: String,
        progress: u32,
    }
    ticketremote_owner_request_vivi_reauth [version: u32] {
        requestId: String,
        credentialRevision: String,
    }
    ticketremote_owner_request_vivi_reauth_full_reset [version: u32] {
        requestId: String,
        credentialRevision: String,
    }
    ticketremote_owner_request_vivi_reauth_logout_login [version: u32] {
        requestId: String,
        credentialRevision: String,
    }
    ticketremote_member_request_keyframe [] {
        reason: String,
    }
    ticketremote_member_recover_stream [] {
        reason: String,
    }
}

rejected_phone_reducers! { service_client;
    ticketremote_schedule_latest_ticket_reselect [] {
        scheduleId: String,
        scheduledAtMicros: i64,
        phoneLocalTime: String,
        phoneTimeZone: String,
        requestedBy: String,
        nowArg: String,
    }
    ticketremote_schedule_activation_expiry_reset [] {
        activationRevision: String,
        nowArg: String,
    }
}

rejected_phone_reducers! { worker;
    ticketremote_commit_ticket_activation [] {
        attemptId: String,
        interactionRevision: String,
        activationRevision: String,
    }
    ticketremote_finalize_ticket_activation_refresh [] {
        activationRevision: String,
        interactionRevision: String,
        reason: String,
    }
    ticketremote_finalize_ticket_activation_attempt [] {
        attemptId: String,
        outcome: String,
        reason: String,
    }
    ticketremote_update_ticket_interaction [] {
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
    }
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
    if !expected_fast_revision.starts_with("pc-")
        || !ctx
            .db
            .ticketremote_phone_control_state()
            .id()
            .find(phone_row_id(ticket_id, &backend_id))
            .map(|row| phone_control::phone_control_ready(&row, expected_fast_revision, now))
            .unwrap_or(false)
    {
        return Err("phone_control_context_changed".into());
    }
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
        "requestId": request_id,
        "digits": clean_digits,
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
    Ok(())
}

member_reducers! {
    ticketremote_member_confirm_control_code_browser_capture(ctx; ticketId: String,
        backendId: String, sessionId: String, requestId: String, candidateFrameEpoch: String,
        candidateFrameSequence: String, markerRevision: String, acceptedReason: String; ticket = ticketId)
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
    let owner = ctx.db.ticketremote_control_code_owner().id().find(&request_id)
        .ok_or("not_found")?;
    require_exact_result_ack(
        &owner.sessionId, &sessionId, &marker_epoch, &marker_sequence,
        current.resultMarkerRevision.as_deref().unwrap_or(""),
        &frame_epoch, &frame_sequence, &markerRevision,
    )?;
    // A replay cannot extend the display lifetime or deliver a second cleanup.
    if current.captureAcknowledged { return Ok(()); }
    if !current.captureRequired || parse_time_ms(&current.resultExpiresAt) <= parse_time_ms(&now) {
        return Err("result_expired".into());
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
        return queue_phone_intent(
            ctx,
            &ticket.id,
            &backend_id,
            &request_id,
            &owner_email,
            &now,
            QueuedPhoneIntent::Reauth {
                revision: &credential_revision,
                mode,
            },
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
    action_statistics::ensure_tracking(ctx, &ticket.id);
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
    reconcile_activation_refresh_timers(ctx, &now);
    cleanup_expired(ctx, &ticket.id, &now, CLEANUP_BATCH_SIZE);
    idle_refresh::reconcile_ticket(ctx, &ticket.id, &now);
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
    idle_refresh::reconcile_ticket(ctx, &arg.ticketId, &now);
    monitoring::check_due(ctx, &arg.ticketId, parse_time_ms(&now));
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
    require_new_phone_admission(ctx, &ticketId)?;
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
    require_new_phone_admission(ctx, &ticket.id)?;
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

#[allow(unused_variables)]
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
    if maintenance_paused(ctx, &ticketId)
        && !matches!(
            commandType.as_str(),
            "stop" | "control_exit" | "control_code_browser_capture"
        )
    {
        return Ok(());
    }
    if cold_restart::ticket_blocked(ctx, &ticketId) {
        return Ok(());
    }
    let now = now_or(ctx, &nowArg);
    let command_type = non_empty(&commandType, "command");
    if cold_restart::predates_boundary(ctx, &ticketId, &backendId, &now) { return Ok(()); }
    let command_reason = non_empty(&reason, "stream_command");
    if !stream_start_admitted(
        &command_type,
        &command_reason,
        authoritative_stream_is_idle(ctx, &ticketId, &backendId),
        relay_current_report_suppresses_background_stream_command(
            ctx,
            &phone_row_id(&ticketId, &backendId),
            &now,
        ),
    )? {
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
    ticketremote_update_relay_current_report(ctx; ticketId;
        backendId: String, videoClients: u32, streamVerdict: String, lastFrameAt: String,
        framesForwarded: String, statusJson: String; nowArg
    ) |ticket, now| {
        upsert_relay_current_report(ctx, &ticket.id, &backendId, videoClients, &streamVerdict,
            &lastFrameAt, &framesForwarded, &statusJson, &now);
        cold_restart::note_live_report(ctx, &ticket.id, &backendId, &streamVerdict, &lastFrameAt, &now);
    }
}

/// Pixel-only control-code cleanup handoff. The request barrier and the
/// short-lived ready watermark change in one transaction, so browsers never
/// observe cleanup as complete while the phone lane still projects blocked.
#[spacetimedb::reducer]
#[allow(unused_variables)] // Retained arguments keep the installed cleanup wire shape stable.
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
        let current = ctx
            .db
            .ticketremote_phone_control_state()
            .id()
            .find(phone_row_id(&ticket.id, &backend_id))
            .ok_or("phone_control_session_required")?;
        if !revision.starts_with(&format!("{}:", current.sessionId)) {
            return Err("phone_control_session_changed".into());
        }
        // The service emits this only after raw-detail proof, durable checkpoint
        // clearance and panel finalization. This is a result, not a readiness lease.
        // The independent observation publisher remains the only readiness owner.
        update_control_code_public_request(
            ctx,
            request_id,
            ControlCodeChanges {
                captureRequired: Some(false),
                cleanupPending: Some(false),
                reason: Some("phone_visual_cleanup_complete".into()),
                expiresAt: Some(control_code_result_expires_at(&now)),
                ..Default::default()
            },
            &now,
        );
        promote_ticket_action_v3_queue(ctx, &ticket.id, &backend_id, &now);
        return Ok(());
    }
    Err("ticket_worker_reload_required".into())
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

fn finalize_ticket_action_v3_refresh(
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
    let succeeded = facts.status == "succeeded";
    if succeeded
        && (facts.target != "open_latest_unactivated" || facts.current_view != "latest_unactivated")
    {
        return Err("activation_refresh_visual_proof_invalid".into());
    }
    let outcome = if succeeded { "succeeded" } else { "failed" };
    let history = activation_refresh_history_for_schedule(ctx, &schedule)
        .ok_or_else(|| "activation_refresh_history_mismatch".to_string())?;
    ctx.db
        .ticketremote_activation_history()
        .id()
        .update(TicketremoteActivationHistory {
            refreshOutcome: outcome.into(),
            refreshCompletedAt: facts.completed_at.clone(),
            refreshRetryAt: String::new(),
            updatedAt: now.into(),
            ..history
        });
    settle_ticket_schedule(
        ctx,
        schedule,
        outcome,
        &facts.reason,
        &facts.phase,
        "phone_worker",
        &facts.completed_at,
        now,
    );
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
    let schedule = ctx
        .db
        .ticketremote_latest_ticket_reselect_schedule()
        .commandId()
        .filter(&command.id)
        .find(|row| matches!(row.status.as_str(), "queued" | "running"))
        .ok_or_else(|| "ticket_action_schedule_not_active".to_string())?;
    if schedule.id != schedule_id
        || schedule.ticketId != action.ticketId
        || schedule.backendId != action.backendId
        || scheduled_ticket_action_v3_target(schedule.purpose.as_deref().unwrap_or(""))
            != "redetect_latest"
    {
        return Err("ticket_action_schedule_mismatch".into());
    }
    settle_ticket_schedule(
        ctx,
        schedule,
        if facts.status == "succeeded" {
            "succeeded"
        } else {
            "failed"
        },
        &facts.reason,
        &facts.phase,
        "phone_worker",
        &facts.completed_at,
        now,
    );
    Ok(())
}

fn terminal_result_fingerprint(value: &serde_json::Value) -> String {
    Sha256::digest(value.to_string().as_bytes())
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
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
    // History retention must not strand an old phone transport receipt. With
    // neither durable row remaining, acknowledge only the fixed, aged-out idle
    // command identity; no outcome or current phone state is recreated.
    if action.is_none() && command.is_none()
        && idle_refresh::purged_result_matches(action_id, &facts, &now)
    {
        return Ok(());
    }
    let action = action.ok_or_else(|| "ticket_action_not_found".to_string())?;
    if action.target != facts.target {
        return Err("ticket_action_target_mismatch".into());
    }
    let terminal_fingerprint = terminal_result_fingerprint(&serde_json::json!([
        expected_command_id,
        action_id,
        facts.target,
        facts.status,
        facts.phase,
        facts.current_view,
        facts.stream_epoch,
        facts.frame_sequence,
        facts.reason,
        facts.completed_at,
        facts.attempt_id,
        facts.interaction_revision,
        facts.activation_revision,
        hasSliderRegion,
        leftBasisPoints,
        topBasisPoints,
        rightBasisPoints,
        bottomBasisPoints
    ]));
    if ticket_action_v3_terminal(&action.status) {
        // A command can expire or be cancelled after Pixel fetched it. Its
        // correlated receipt must drain the phone's durable result journal even
        // after command TTL cleanup; acknowledgement grants no physical action.
        if idle_refresh::cancelled_result_matches(&action, &facts) {
            ctx.db.ticketremote_ticket_action_v3().id().update(TicketremoteTicketActionV3 {
                terminalFingerprint: Some(terminal_fingerprint),
                updatedAt: now.clone(),
                expiresAt: add_ms(&now, HISTORY_TTL_MS),
                ..action
            });
            ctx.db.ticketremote_stream_command().id().delete(&expected_command_id);
            upsert_stream_command_signal(ctx, &ticket.id, &backend_id, action_id, &now);
            promote_ticket_action_v3_queue(ctx, &ticket.id, &backend_id, &now);
            return Ok(());
        }
        return if command.is_none()
            && action.terminalFingerprint.as_deref() == Some(&terminal_fingerprint)
        {
            Ok(())
        } else {
            Err("ticket_action_terminal_conflict".into())
        };
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
        finalize_ticket_action_v3_refresh(ctx, &action, &facts, &now)?;
    } else {
        finalize_ticket_action_v3_scheduled_result(ctx, &command, &action, &facts, &now)?;
    }

    invitations::settle_action(ctx, &ticket.id, action_id, facts.status == "succeeded",
        invitations::conclusive_activation_failure(&facts.status, &facts.phase, &facts.reason));
    if facts.status == "succeeded" {
        action_statistics::record_success(ctx, &ticket.id, &backend_id, action_id);
    }
    let terminal = TicketremoteTicketActionV3 {
        status: facts.status,
        phase: facts.phase,
        currentView: facts.current_view,
        streamEpoch: facts.stream_epoch,
        frameSequence: facts.frame_sequence,
        reason: facts.reason,
        completedAt: facts.completed_at,
        terminalFingerprint: Some(terminal_fingerprint),
        updatedAt: now.clone(),
        expiresAt: add_ms(&now, HISTORY_TTL_MS),
        ..action
    };
    note_ticket_switch_visual_result(ctx, &terminal, &now);
    ctx.db.ticketremote_ticket_action_v3().id().update(terminal);
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
    if !matches!(status.as_str(), "pending" | "running") {
        return Err("ticket_action_atomic_finalization_required".into());
    }
    let now = now_or(ctx, &nowArg);
    let ticket = ensure_ticket(ctx, &ticketId, "", &now);
    let backend_id = clean_backend_id(&backendId);
    // Retained reducer fields cannot supply switch authority or terminal facts.
    let _ = (switchAvailable, switchExpiresAt, completedAt);
    let action_id = actionId.trim();
    if !valid_schedule_identifier(action_id) {
        return Err("invalid_ticket_action_id".into());
    }
    let target = ticket_action_v3_target(&target);
    if target.is_empty() {
        return Err("invalid_ticket_action_update".into());
    }
    let existing = ctx
        .db
        .ticketremote_ticket_action_v3()
        .id()
        .find(ticket_action_v3_row_id(&ticket.id, &backend_id, action_id))
        .ok_or("ticket_action_not_found")?;
    if existing.target != target {
        return Err("ticket_action_target_mismatch".into());
    }
    if ticket_action_v3_terminal(&existing.status) {
        return Err("ticket_action_already_terminal".into());
    }
    ctx.db
        .ticketremote_ticket_action_v3()
        .id()
        .update(TicketremoteTicketActionV3 {
            status,
            phase: bounded_text(&safe_token(&phase, "running"), 80),
            currentView: ticket_action_v3_view(&currentView),
            streamEpoch: bounded_frame_ordinal(&streamEpoch),
            frameSequence: bounded_frame_ordinal(&frameSequence),
            reason: ticket_action_v3_public_reason(&reason, "ticket_action_updated"),
            completedAt: String::new(),
            updatedAt: now.clone(),
            expiresAt: add_ms(&now, HISTORY_TTL_MS),
            ..existing
        });
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
    let incoming_marker = (
        bounded_frame_ordinal(&resultFrameEpoch),
        bounded_frame_ordinal(&resultMinFrameSequence),
    );
    let previous_marker = control_code_result_marker(&existing);
    let has_marker = incoming_marker.0 != "0" && incoming_marker.1 != "0";
    if has_marker && incoming_marker != previous_marker {
        // Capture settles this request permanently. Delayed bridge updates must
        // neither replace its picture nor rewind a newer unacknowledged marker.
        if existing.captureAcknowledged
            || (existing.status == "succeeded"
                && parse_time_ms(&resultProofAt)
                    <= parse_time_ms(existing.resultProofAt.as_deref().unwrap_or("")))
        {
            return Ok(());
        }
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
    invitations::settle_action(ctx, &ticket.id, requestId.trim(), succeeded,
        invitations::conclusive_code_failure(&clean_status, &incoming_reason));
    let clean_result_proof = clean_control_code_result_proof(&resultProof);
    let clean_result_proof_at = bounded_text(resultProofAt.trim(), 80);
    if succeeded {
        if let Some(command) = ctx.db.ticketremote_stream_command().id()
            .find(format!("{}:generate_control_code", requestId.trim())) {
            action_statistics::record_success(ctx, &ticket.id, &command.backendId, requestId.trim());
        }
    }
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
            resultExpiresAt: Some(if existing.captureAcknowledged {
                existing.resultExpiresAt.clone()
            } else if succeeded {
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

fn require_exact_result_ack(
    owner_session: &str,
    candidate_session: &str,
    marker_epoch: &str,
    marker_sequence: &str,
    marker_revision: &str,
    frame_epoch: &str,
    frame_sequence: &str,
    candidate_revision: &str,
) -> Result<(), String> {
    if owner_session != candidate_session {
        return Err("result_session_mismatch".into());
    }
    if marker_epoch.parse::<u64>().unwrap_or(0) == 0
        || marker_sequence.parse::<u64>().unwrap_or(0) == 0
        || marker_revision.is_empty()
        || frame_epoch != marker_epoch
        || frame_sequence != marker_sequence
        || candidate_revision != marker_revision
    {
        return Err("result_marker_mismatch".into());
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
        "requestId": request_id, "accepted": accepted, "reason": reason,
        "candidateFrameEpoch": frame_ordinal_number(frame_epoch),
        "candidateFrameSequence": frame_ordinal_number(frame_sequence)
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
        && (phase != "complete" || !v4_reason || proof_source != "phone_visual")
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

fn now(ctx: &ReducerContext) -> String {
    iso(ctx.timestamp)
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

fn bounded_text(value: &str, max: usize) -> String {
    value.chars().take(max).collect()
}

fn member_id(ticket: &str, email: &str) -> String {
    format!("{}:{}", clean_ticket_id(ticket), clean_email(email))
}

fn phone_row_id(ticket: &str, backend: &str) -> String {
    format!("{}:{}", clean_ticket_id(ticket), clean_backend_id(backend))
}

fn stream_viewer_focus_expires_at(clock: &str) -> String {
    add_ms(clock, STREAM_VIEWER_FOCUS_TTL_MS)
}

fn control_code_request_expires_at(clock: &str) -> String {
    add_ms(clock, CONTROL_CODE_REQUEST_TTL_MS)
}

fn control_code_result_expires_at(clock: &str) -> String {
    add_ms(clock, CONTROL_CODE_RESULT_TTL_MS)
}

fn valid_control_code_digits(value: &str) -> bool {
    (2..=8).contains(&value.len()) && value.chars().all(|c| c.is_ascii_digit())
}

fn frame_ordinal_number(value: &str) -> i64 {
    value.trim().parse::<i64>().unwrap_or(0)
}

fn operator_identity_is_valid(identity: &str) -> bool {
    identity.trim() == OPERATOR_IDENTITY
}

fn cleanup_remaining(limit: u32, deleted: u32) -> u32 {
    if limit == 0 {
        0
    } else {
        limit.saturating_sub(deleted)
    }
}

fn stream_viewer_focus_expired(row: &TicketremoteStreamViewerFocus, clock: &str) -> bool {
    !row.active || parse_time_ms(&row.expiresAt) <= parse_time_ms(clock)
}

fn control_code_terminal_failure_status(status: &str) -> bool {
    matches!(status, "failed" | "expired" | "closed")
}

fn canonical_time(value: &str) -> String {
    iso(Timestamp::from_micros_since_unix_epoch(parse_time_micros(
        value,
    )))
}

fn account_public_id(email: &str) -> String {
    public_hash(&clean_email(email), 4)
}

fn auth_config(ctx: &ReducerContext, ticket: &str) -> Option<TicketremoteAuthConfig> {
    ctx.db
        .ticketremote_auth_config()
        .ticketId()
        .find(clean_ticket_id(ticket))
}

fn has_valid_service_identity(ctx: &ReducerContext) -> bool {
    jwt_payload(ctx)
        .map(|claims| service_claims_are_valid(&claims))
        .unwrap_or(false)
}

fn is_member(ctx: &ReducerContext, ticket: &str, email: &str) -> bool {
    ctx.db
        .ticketremote_ticket_member()
        .id()
        .find(member_id(ticket, email))
        .map(|row| row.active)
        .unwrap_or(false)
}

fn is_admin(ctx: &ReducerContext, ticket: &str, email: &str) -> bool {
    ctx.db
        .ticketremote_ticket_member()
        .id()
        .find(member_id(ticket, email))
        .map(|row| row.active && matches!(row.role.as_str(), "owner" | "admin"))
        .unwrap_or(false)
}

fn is_owner(ctx: &ReducerContext, ticket: &str, email: &str) -> bool {
    ctx.db
        .ticketremote_ticket_member()
        .id()
        .find(member_id(ticket, email))
        .map(|row| row.active && row.role == "owner")
        .unwrap_or(false)
}

fn control_code_cleanup_preserves_terminal_failure(
    existing: &TicketremoteControlCodeRequest,
    status: &str,
    reason: &str,
) -> bool {
    control_code_terminal_failure_status(&existing.status)
        && status == existing.status
        && control_code_cleanup_reason(reason)
}

fn updated_ordinal(value: &str, fallback: &str) -> Option<String> {
    Some(bounded_frame_ordinal(&non_empty(value, fallback)))
}

fn optional_text(value: String) -> Option<String> {
    (!value.is_empty()).then_some(value)
}

fn control_code_request_same_payload(
    left: &TicketremoteControlCodeRequest,
    right: &TicketremoteControlCodeRequest,
) -> bool {
    same_fields!(left, right;
            status, reason, message, resultProof, resultProofAt, captureRequired,
            captureAcknowledged, cleanupPending, streamEpoch, frameSequence, minFrameSequence,
            resultFrameEpoch, resultMinFrameSequence, captureFrameEpoch, captureFrameSequence)
}

fn now_or(ctx: &ReducerContext, value: &str) -> String {
    let value = value.trim();
    if value.is_empty() {
        now(ctx)
    } else {
        value.into()
    }
}

fn iso(timestamp: Timestamp) -> String {
    timestamp
        .to_rfc3339()
        .unwrap_or_else(|_| "1970-01-01T00:00:00Z".into())
}

fn add_ms(value: &str, ms: i64) -> String {
    iso(Timestamp::from_micros_since_unix_epoch(
        parse_time_micros(value).saturating_add(ms.saturating_mul(1000)),
    ))
}

fn json_i64(value: &serde_json::Value) -> Option<i64> {
    value
        .as_i64()
        .or_else(|| value.as_u64().and_then(|raw| i64::try_from(raw).ok()))
        .or_else(|| value.as_str().and_then(|raw| raw.trim().parse().ok()))
}

fn clean_role(value: &str) -> String {
    allowlisted(value, &["owner", "admin"], "member")
}

fn clean_hdr_display_boost(value: u32) -> u32 {
    match value {
        2 | 3 | 4 | 5 | 6 => value,
        _ => 4,
    }
}

fn non_empty(value: &str, fallback: &str) -> String {
    let value = value.trim();
    if value.is_empty() {
        fallback.into()
    } else {
        value.into()
    }
}

fn safe_token(value: &str, fallback: &str) -> String {
    non_empty(value, fallback).replace(
        |c: char| !c.is_ascii_alphanumeric() && c != '_' && c != '-',
        "_",
    )
}

fn safe_json_string(value: &str, max: usize) -> String {
    let raw = match value.trim() {
        "" => "{}",
        value => value,
    };
    let valid = serde_json::from_str::<serde_json::Value>(raw).is_ok();
    (if valid { raw } else { "{}" }).chars().take(max).collect()
}

fn command_expires_at(clock: &str, ttl: i64) -> String {
    add_ms(
        clock,
        if ttl <= 0 || ttl > HISTORY_TTL_MS {
            HISTORY_TTL_MS
        } else {
            ttl
        },
    )
}

fn clean_control_code_result_proof(value: &str) -> String {
    allowlisted(
        value,
        &[
            "phone_root",
            "phone_visual",
            "phone_visual_root_confirmed",
            "phone_visual_generated_inline",
            "phone_visual_generated_with_close",
            "phone_visual_raw_ticket_after_submit",
            "phone_root_image",
            "browser_frame",
        ],
        "",
    )
}

fn bounded_frame_ordinal(value: &str) -> String {
    non_empty(
        &value
            .chars()
            .filter(|c| c.is_ascii_digit())
            .take(24)
            .collect::<String>(),
        "0",
    )
}

fn connection_session_id(ctx: &ReducerContext) -> String {
    ctx.connection_id()
        .map(|id| format!("{id:?}"))
        .unwrap_or_else(|| ctx.sender().to_string())
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

fn control_code_result_marker(request: &TicketremoteControlCodeRequest) -> (String, String) {
    let epoch = if request.resultFrameEpoch != "0" {
        &request.resultFrameEpoch
    } else {
        &request.streamEpoch
    };
    let sequence = if request.resultMinFrameSequence != "0" {
        &request.resultMinFrameSequence
    } else if request.minFrameSequence != "0" {
        &request.minFrameSequence
    } else {
        &request.frameSequence
    };
    (
        bounded_frame_ordinal(epoch),
        bounded_frame_ordinal(sequence),
    )
}

fn control_code_close_is_idempotent(request: Option<&TicketremoteControlCodeRequest>) -> bool {
    request.is_none_or(|row| {
        control_code_terminal_failure_status(&row.status)
            || (row.status == "succeeded" && row.captureAcknowledged)
    })
}

fn allowlisted(value: &str, allowed: &[&str], fallback: &str) -> String {
    let value = value.trim();
    if allowed.contains(&value) {
        value.into()
    } else {
        fallback.into()
    }
}

fn json_object(pairs: &[(&str, &str)]) -> String {
    serde_json::Value::Object(
        pairs
            .iter()
            .map(|(key, value)| ((*key).into(), (*value).into()))
            .collect(),
    )
    .to_string()
}

fn stable_stamp(value: &str) -> String {
    non_empty(
        &value
            .chars()
            .filter(|c| c.is_ascii_alphanumeric())
            .collect::<String>(),
        "time",
    )
}

fn control_code_request_id(ticket: &str, session: &str, clock: &str) -> String {
    format!(
        "{}:{}:{}:control_code",
        clean_ticket_id(ticket),
        session.trim(),
        stable_stamp(clock)
    )
}

fn jwt_payload(ctx: &ReducerContext) -> Result<serde_json::Value, String> {
    let Some(jwt) = ctx.sender_auth().jwt() else {
        return Err("auth required".into());
    };
    serde_json::from_str(jwt.raw_payload()).map_err(|_| "invalid auth payload".into())
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

fn member_proxy_claims_are_valid(payload: &serde_json::Value, email: &str) -> bool {
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
            == Some(format!("member:{email}").as_str())
        && jwt_roles_include(payload, MEMBER_PROXY_ROLE)
}

fn require_service(ctx: &ReducerContext) -> Result<(), String> {
    has_valid_service_identity(ctx)
        .then_some(())
        .ok_or_else(|| "service role required".into())
}

fn service_ticket_id_for_viewer(ctx: &ViewContext) -> Option<String> {
    ctx.db
        .ticketremote_service_identity()
        .identity()
        .filter(&ctx.sender())
        .next()
        .map(|row| clean_ticket_id(&row.ticketId))
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

fn service_member_account_from_row(
    row: &TicketremoteTicketMember,
) -> TicketremoteServiceMemberAccount {
    let email = clean_email(&row.email);
    TicketremoteServiceMemberAccount {
        id: row.id.clone(),
        ticketId: row.ticketId.clone(),
        email: email.clone(),
        publicId: account_public_id(&email),
        accountScopeId: account_scope_id(&email),
        role: clean_role(&row.role),
        active: row.active,
        updatedAt: row.updatedAt.clone(),
    }
}

fn public_hash(value: &str, len: usize) -> String {
    let mut out = to_base36(fnv32(value.trim()));
    if out.len() < len {
        out = format!("{:0>width$}", out, width = len);
    }
    out.chars().take(len).collect()
}

fn require_admin(ctx: &ReducerContext, ticket: &str, email: &str) -> Result<(), String> {
    is_admin(ctx, ticket, email)
        .then_some(())
        .ok_or_else(|| "forbidden".into())
}

fn require_owner(ctx: &ReducerContext, ticket: &str, email: &str) -> Result<(), String> {
    is_owner(ctx, ticket, email)
        .then_some(())
        .ok_or_else(|| "owner role required".into())
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
            .any(|value| value.as_str().is_some_and(|raw| raw.trim() == expected)),
        _ => false,
    }
}

fn jwt_roles_include(payload: &serde_json::Value, expected: &str) -> bool {
    match payload.get("roles") {
        Some(serde_json::Value::String(value)) => {
            value.split(',').any(|raw| raw.trim() == expected)
        }
        Some(serde_json::Value::Array(values)) => values
            .iter()
            .any(|value| value.as_str().is_some_and(|raw| raw.trim() == expected)),
        _ => false,
    }
}

fn control_code_request_occupies_phone(row: &TicketremoteControlCodeRequest, clock: &str) -> bool {
    if parse_time_ms(&row.expiresAt) <= parse_time_ms(clock) {
        return false;
    }
    if matches!(row.status.as_str(), "closed" | "expired" | "failed") {
        return false;
    }
    matches!(row.status.as_str(), "queued" | "running" | "generated")
        || (row.status == "succeeded"
            && (row.cleanupPending || (row.captureRequired && !row.captureAcknowledged)))
}

fn control_code_request_ttl_is_healthy(row: &TicketremoteControlCodeRequest, clock: &str) -> bool {
    let now = parse_time_ms(clock);
    if parse_time_ms(&row.expiresAt).saturating_sub(now) <= CONTROL_CODE_COMMAND_TTL_MS / 2 {
        return false;
    }
    row.status != "succeeded"
        || parse_time_ms(&row.resultExpiresAt).saturating_sub(now) > CONTROL_CODE_RESULT_TTL_MS / 2
}

fn fnv32(value: &str) -> u32 {
    value.as_bytes().iter().fold(0x811c9dc5, |hash, byte| {
        (hash ^ *byte as u32).wrapping_mul(0x01000193)
    })
}

fn refresh_touched_signals(
    ctx: &ReducerContext,
    ticket: &str,
    backends: &[String],
    clock: &str,
) -> () {
    for backend in backends {
        upsert_stream_command_signal(ctx, ticket, backend, clock, clock);
    }
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


fn stream_start_admitted(
    command: &str,
    reason: &str,
    idle: bool,
    relay_live: bool,
) -> Result<bool, String> {
    if command != "start" {
        return Err("ticket_client_reload_required".into());
    }
    // An authenticated opening may precede the asynchronous desired-state write.
    // Other starts need current demand; media recovery and code requests have their own owners.
    let page_opening = matches!(
        reason,
        "stream_prewarm" | "index_auth_prewarm" | "video_socket_open"
    );
    Ok(!relay_live && (!idle || page_opening))
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
            return table.id().update(updated);
        }
        return existing;
    }
    let ticket = TicketremoteTicket {
        id,
        displayName: non_empty(display_name, DEFAULT_TICKET_NAME),
        createdAt: now.into(),
        updatedAt: now.into(),
    };
    table.insert(ticket)
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
    invitations::record_manual_source(ctx, ticket_id, target_email, actor_email, now);
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
        settle_ticket_schedule(
            ctx,
            existing,
            "replaced",
            "replaced_by_new_schedule",
            "replaced",
            "admin",
            now,
            now,
        );
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
    settle_ticket_schedule(
        ctx,
        existing,
        "canceled",
        "canceled_by_admin",
        "canceled",
        "admin",
        now,
        now,
    );
    Ok(())
}

fn latest_ticket_reselect_admin_cancellable(
    schedule: &TicketremoteLatestTicketReselectSchedule,
) -> bool {
    scheduled_ticket_action_v3_target(schedule.purpose.as_deref().unwrap_or(""))
        == "redetect_latest"
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
        settle_ticket_schedule(
            ctx,
            existing,
            "canceled",
            "activation_state_replaced",
            "canceled",
            "spacetimedb",
            &now,
            &now,
        );
        return Ok(());
    }
    let now = now(ctx);
    let purpose = existing.purpose.as_deref().unwrap_or("");
    if maintenance_paused(ctx, &existing.ticketId) || cold_restart::ticket_blocked(ctx, &existing.ticketId) {
        defer_pending_scheduled_redetect(ctx, existing, "ticket_maintenance", &now);
        return Ok(());
    }
    let v3_target = scheduled_ticket_action_v3_target(purpose);
    if v3_target.is_empty() {
        return Err("invalid_scheduled_ticket_action_target".into());
    }
    if v3_target == "redetect_latest" {
        if let Some(conflict_reason) =
            ticket_phone_mutation_lane_conflict(ctx, &existing.ticketId, &existing.backendId, &now)
        {
            defer_pending_scheduled_redetect(ctx, existing, conflict_reason, &now);
            return Ok(());
        }
    }
    let command_id =
        ticket_action_v3_command_id(&existing.ticketId, &existing.backendId, &existing.id);
    let activation_revision = existing.activationRevision.as_deref().unwrap_or("");
    let activation_expiry = purpose == "activation_expiry_reset";
    let command_reason = if activation_expiry {
        "activation_expiry_reset"
    } else {
        "scheduled_latest_ticket_reselect"
    };
    let command_revision = format!("schedule:{}", existing.id);
    let action_id = ticket_action_v3_row_id(&existing.ticketId, &existing.backendId, &existing.id);
    if ctx
        .db
        .ticketremote_ticket_action_v3()
        .id()
        .find(action_id)
        .is_some_and(|action| ticket_action_v3_terminal(&action.status))
    {
        // A consumed action ID is never turned into a new command. The coordinated
        // cutover drains V1 work; V2 finalizes the action and schedule atomically.
        return Err("ticket_action_already_terminal".into());
    }
    let payload = {
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
    };
    let command = insert_stream_command(
        ctx,
        &existing.ticketId,
        &existing.backendId,
        &command_id,
        "ticket_action_v3",
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
    // Old stored schedules keep their identity and deadline but use the current executor.
    if matches!(
        purpose,
        "" | "latest_ticket_reselect" | "ticket_action_v3_redetect_latest"
    ) {
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
            scheduled_ticket_action_v3_target(row.purpose.as_deref().unwrap_or(""))
                == "redetect_latest"
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
        let (status, reason) = if !activation_refresh_is_current(ctx, &schedule) {
            cancel_queued_activation_refresh_command(ctx, &schedule, now);
            ("canceled", "activation_state_replaced")
        } else {
            let has_timer = ctx
                .db
                .ticketremote_latest_ticket_reselect_timer()
                .scheduleId()
                .filter(&schedule.id)
                .next()
                .is_some();
            let command_active = ctx
                .db
                .ticketremote_stream_command()
                .id()
                .find(&schedule.commandId)
                .is_some_and(|command| {
                    matches!(
                        command.status.as_str(),
                        "pending" | "queued" | "dispatched" | "running"
                    )
                });
            if has_timer || command_active {
                continue;
            }
            ("failed", "activation_refresh_command_missing")
        };
        mark_activation_refresh_terminal(
            ctx,
            &schedule.ticketId,
            &schedule.backendId,
            schedule.activationRevision.as_deref().unwrap_or(""),
            status,
            now,
        );
        settle_ticket_schedule(
            ctx,
            schedule,
            status,
            reason,
            status,
            "spacetimedb_bootstrap",
            now,
            now,
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
        idle_refresh::reconcile(ctx, &ticket_id, &backend_id, now);
        return;
    }
    upsert_row!(
        ctx,
        ticketremote_stream_viewer_focus,
        TicketremoteStreamViewerFocus {
            id,
            ticketId: ticket_id.clone(),
            backendId: backend_id.clone(),
            publicId: public_id,
            active: true,
            lastSeenAt: now.into(),
            expiresAt: stream_viewer_focus_expires_at(now),
            email: Some(clean_email(email)),
        }
    );
    idle_refresh::reconcile(ctx, &ticket_id, &backend_id, now);
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
    let previous = ctx.db.ticketremote_stream_desired_state().id().find(&id);
    if let Some(existing) = &previous
        && (cold_restart::blocks(existing.coldRestartPhase.as_deref()) ||
            existing.coldRestartStartedAt.as_deref().is_some_and(|start| parse_time_ms(now) <= parse_time_ms(start)))
    {
        return existing.clone();
    }
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
        coldRestartId: previous.as_ref().and_then(|row| row.coldRestartId.clone()),
        coldRestartPhase: previous.as_ref().and_then(|row| row.coldRestartPhase.clone()),
        coldRestartStartedAt: previous.as_ref().and_then(|row| row.coldRestartStartedAt.clone()),
        coldRestartError: previous.as_ref().and_then(|row| row.coldRestartError.clone()),
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
        .filter(|row| row.commandType == "start")
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
    if command_type == "start" {
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
    let row = table.insert(row);
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

fn update_stream_command_status(
    ctx: &ReducerContext,
    command_id: &str,
    status: &str,
    reason: &str,
    now: &str,
) {
    if !matches!(status, "acknowledged" | "dispatched" | "failed" | "expired") {
        return;
    }
    let table = ctx.db.ticketremote_stream_command();
    let Some(command) = table.id().find(command_id.trim().to_string()) else {
        return;
    };
    if status == "dispatched" && !matches!(command.status.as_str(), "pending" | "dispatched") {
        return;
    }
    cold_restart::acknowledge(ctx, &command, status, reason, now);
    let terminal = status != "dispatched";
    if terminal {
        let missing_result = status == "acknowledged";
        let failure_reason = if missing_result {
            "ticket_action_result_missing"
        } else {
            reason
        };
        fail_ticket_action_v3_for_command(ctx, &command, failure_reason, now);
        if command.commandType == "vivi_reauth" {
            if let Some(attempt) = vivi_reauth_attempt_for_command(ctx, &command)
                && !vivi_reauth_terminal(&attempt.status)
            {
                let terminal_status = vivi_reauth_interrupted_terminal_status(&attempt.status);
                let terminal_reason = match status {
                    "acknowledged" => "visual_proof_failed",
                    "expired" => "command_expired",
                    _ => "internal_failure",
                };
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
        // Success is committed only by the atomic action finalizer, which retires
        // its command in the same transaction. A leftover receipt cannot invent it.
        update_latest_ticket_reselect_result(
            ctx,
            &command.id,
            if status == "expired" {
                "expired"
            } else {
                "failed"
            },
            failure_reason,
            "failed",
            "spacetimedb",
            now,
            true,
        );
        if status == "acknowledged" {
            table.id().delete(&command.id);
        } else {
            table.id().update(TicketremoteStreamCommand {
                status: status.into(),
                reason: bounded_text(reason, 240),
                payloadJson: "{}".into(),
                updatedAt: now.into(),
                expiresAt: command_expires_at(now, CONTROL_CODE_COMMAND_TTL_MS),
                ..command.clone()
            });
        }
    } else {
        update_latest_ticket_reselect_result(
            ctx,
            &command.id,
            "running",
            &non_empty(reason, "dispatched"),
            "running",
            "phone_worker",
            now,
            false,
        );
        if matches!(
            command.commandType.as_str(),
            "ticket_action_v3" | "vivi_reauth"
        ) {
            table.id().update(TicketremoteStreamCommand {
                status: "dispatched".into(),
                reason: bounded_text(&non_empty(reason, &command.reason), 240),
                updatedAt: now.into(),
                ..command.clone()
            });
        } else {
            table.id().delete(&command.id);
        }
    }
    upsert_stream_command_signal(
        ctx,
        &command.ticketId,
        &command.backendId,
        &command.revision,
        now,
    );
    if terminal {
        promote_ticket_action_v3_queue(ctx, &command.ticketId, &command.backendId, now);
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

fn settle_ticket_schedule(
    ctx: &ReducerContext,
    schedule: TicketremoteLatestTicketReselectSchedule,
    status: &str,
    reason: &str,
    phase: &str,
    proof: &str,
    completed_at: &str,
    now: &str,
) {
    delete_latest_ticket_reselect_timers(ctx, &schedule.id);
    ctx.db
        .ticketremote_latest_ticket_reselect_schedule()
        .id()
        .update(TicketremoteLatestTicketReselectSchedule {
            status: status.into(),
            resultReason: reason.into(),
            resultPhase: phase.into(),
            proofSource: proof.into(),
            updatedAt: now.into(),
            completedAt: completed_at.into(),
            expiresAt: add_ms(now, HISTORY_TTL_MS),
            ..schedule
        });
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
        if activation_refresh && terminal && matches!(status, "failed" | "expired") {
            let history = activation_refresh_history_for_schedule(ctx, &existing);
            let (outcome, reason, proof) =
                if activation_refresh_failure_has_history_authority(history.as_ref(), &existing) {
                    (
                        "failed",
                        bounded_text(&non_empty(result_reason, "activation_refresh_failed"), 240),
                        safe_token(proof_source, "phone_worker"),
                    )
                } else {
                    (
                        "canceled",
                        "activation_state_replaced".into(),
                        "spacetimedb".into(),
                    )
                };
            mark_activation_refresh_terminal(
                ctx,
                &existing.ticketId,
                &existing.backendId,
                existing.activationRevision.as_deref().unwrap_or(""),
                outcome,
                now,
            );
            settle_ticket_schedule(ctx, existing, outcome, &reason, outcome, &proof, now, now);
            continue;
        }
        if terminal {
            settle_ticket_schedule(
                ctx,
                existing,
                status,
                &bounded_text(result_reason, 240),
                &safe_token(result_phase, status),
                &safe_token(proof_source, ""),
                now,
                now,
            );
            continue;
        }
        table.id().update(TicketremoteLatestTicketReselectSchedule {
            status: status.into(),
            resultReason: bounded_text(result_reason, 240),
            resultPhase: safe_token(result_phase, status),
            proofSource: safe_token(proof_source, ""),
            updatedAt: now.into(),
            completedAt: String::new(),
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
        resultMarkerRevision: None,
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
    ctx.db.ticketremote_control_code_request().insert(row)
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
    if row.resultFrameEpoch != "0" && row.resultMinFrameSequence != "0" {
        row.resultMarkerRevision = Some(format!(
            "{}:{}",
            row.resultFrameEpoch, row.resultMinFrameSequence
        ));
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
    deleted += command::purge_command_receipts(
        ctx,
        &ticket.id,
        expiry_bound.as_str(),
        cleanup_remaining(limit, deleted),
    );
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn only_current_stream_starts_can_cross_the_idle_opening_boundary() {
        for command in [
            "keyframe",
            "recover_stream",
            "activity",
            "generate_control_code",
        ] {
            assert_eq!(
                stream_start_admitted(command, "video_socket_open", false, false),
                Err("ticket_client_reload_required".into())
            );
        }
        for reason in ["stream_prewarm", "index_auth_prewarm", "video_socket_open"] {
            assert_eq!(
                stream_start_admitted("start", reason, true, false),
                Ok(true)
            );
            assert_eq!(
                stream_start_admitted("start", reason, true, true),
                Ok(false)
            );
        }
        assert_eq!(
            stream_start_admitted("start", "background", true, false),
            Ok(false)
        );
        assert_eq!(
            stream_start_admitted("start", "control_code", true, false),
            Ok(false)
        );
        assert_eq!(
            stream_start_admitted("start", "background", false, false),
            Ok(true)
        );
    }

    #[test]
    fn terminal_action_must_match_admitted_command_and_cannot_accept_a_retry() {
        let facts = ticket_action_v3_terminal_facts(
            "register_current",
            "succeeded",
            "activation_proven",
            "activated_current",
            "0",
            "0",
            "ticket_action_registered",
            "2026-09-07T00:00:00Z",
            "action-a",
            "pc-session:1",
            "activation-a",
            "2026-09-07T00:00:01Z",
        )
        .unwrap();
        let mut action = TicketremoteTicketActionV3 {
            id: "row-a".into(),
            actionId: "action-a".into(),
            ticketId: "ticket-a".into(),
            backendId: "pixel".into(),
            target: "register_current".into(),
            status: "pending".into(),
            phase: "pending".into(),
            currentView: "unknown".into(),
            switchAvailable: false,
            switchExpiresAt: String::new(),
            streamEpoch: "0".into(),
            frameSequence: "0".into(),
            reason: String::new(),
            createdAt: String::new(),
            updatedAt: String::new(),
            completedAt: String::new(),
            expiresAt: String::new(),
            parentActionId: None,
            rootActionId: Some("action-a".into()),
            retryOrdinal: 0,
            terminalFingerprint: None,
        };
        let payload = serde_json::json!({
            "version": 3, "actionId": "action-a", "target": "register_current",
            "attemptId": "action-a", "expectedInteractionRevision": "pc-session:1",
        });
        let mut command = TicketremoteStreamCommand {
            id: "command-a".into(),
            ticketId: "ticket-a".into(),
            backendId: "pixel".into(),
            commandType: "ticket_action_v3".into(),
            status: "dispatched".into(),
            revision: "pc-session:1".into(),
            payloadJson: payload.to_string(),
            reason: String::new(),
            createdAt: String::new(),
            updatedAt: String::new(),
            expiresAt: String::new(),
        };
        assert_eq!(
            ticket_action_v3_command_matches_finalization(&command, &action, &facts),
            Ok(true)
        );
        for (field, changed) in [
            ("actionId", "action-b"),
            ("attemptId", "action-b"),
            ("expectedInteractionRevision", "pc-session:2"),
            ("scheduleId", "schedule-b"),
        ] {
            let mut invalid = payload.clone();
            invalid[field] = changed.into();
            command.payloadJson = invalid.to_string();
            assert_eq!(
                ticket_action_v3_command_matches_finalization(&command, &action, &facts),
                Ok(false)
            );
        }
        command.payloadJson = payload.to_string();
        action.retryOrdinal = 1;
        assert_eq!(
            ticket_action_v3_command_matches_finalization(&command, &action, &facts),
            Ok(false)
        );
        action.retryOrdinal = 0;
        command.revision = "pc-replacement:1".into();
        assert_eq!(
            ticket_action_v3_command_matches_finalization(&command, &action, &facts),
            Ok(false)
        );
    }

    #[test]
    fn limits_expire_at_source_boundaries_and_bypass_never_consumes_authority() {
        let now = 10_000_000;
        let ready = member_limit_evaluation(now, &[now - 30_000], &[now - 60_000, now], true);
        assert!(ready.registration_allowed && ready.control_code_allowed);
        assert_eq!(ready.registration_count, 1);
        assert_eq!(ready.control_code_count, 1);
        let registrations: Vec<_> = (0..10).map(|i| now - i * 31_000).collect();
        let blocked = member_limit_evaluation(now, &registrations, &[now - 1, now], true);
        assert!(!blocked.registration_allowed && !blocked.control_code_allowed);
        assert_eq!(blocked.registration_reason, "registration_hour_limit");
        assert_eq!(
            blocked.registration_retry_at_ms,
            now - 9 * 31_000 + 3_600_000
        );
        assert_eq!(blocked.control_code_retry_at_ms, now - 1 + 60_000);
        let bypass = member_limit_evaluation(now, &registrations, &[now - 1, now], false);
        assert!(bypass.registration_allowed && bypass.control_code_allowed);
        assert_eq!(bypass.registration_count, 10);
        assert_eq!(bypass.registration_reason, "limits_bypassed");
        let old_or_future =
            member_limit_evaluation(now, &[now - 3_600_000, now + 1], &[now + 1], true);
        assert_eq!(old_or_future.registration_count, 0);
        assert_eq!(old_or_future.control_code_count, 0);
    }

    #[test]
    fn account_modes_preserve_exact_flags_and_reject_retired_or_mixed_intents() {
        for (mode, version) in [
            (ViviReauthMode::FullResetV2, 2),
            (ViviReauthMode::LogoutLoginV3, 3),
            (ViviReauthMode::LogoutLoginRedetectV4, 4),
        ] {
            let queued = vivi_reauth_queued_intent_payload("revision-a", mode);
            assert_eq!(
                vivi_reauth_queued_intent_fields(&queued),
                Some(("revision-a".into(), mode))
            );
            let command: serde_json::Value = serde_json::from_str(&vivi_reauth_command_payload(
                "request-a",
                "revision-a",
                mode,
            ))
            .unwrap();
            assert_eq!(command["version"], version);
            assert_eq!(command["requestId"], "request-a");
            assert_eq!(
                command.get("resetAppData").is_some(),
                mode == ViviReauthMode::FullResetV2
            );
            assert_eq!(
                command.get("logoutInApp").is_some(),
                mode != ViviReauthMode::FullResetV2
            );
            assert_eq!(
                command.get("redetectAfterLogin").is_some(),
                mode == ViviReauthMode::LogoutLoginRedetectV4
            );
        }
        for intent in [
            serde_json::json!({"credentialRevision": "revision-a"}),
            serde_json::json!({"credentialRevision": "revision-a", "resetAppData": true, "logoutInApp": true}),
            serde_json::json!({"credentialRevision": "revision-a", "logoutInApp": false}),
        ] {
            assert!(vivi_reauth_queued_intent_fields(&intent.to_string()).is_none());
        }
        assert_eq!(vivi_reauth_request_mode("vivi-reauth-old"), None);
    }

    #[test]
    fn membership_authority_preserves_owner_boundary_and_last_owner() {
        assert!(member_upsert_policy(Some("admin"), None, "member", 1).is_ok());
        assert!(member_upsert_policy(Some("owner"), None, "owner", 1).is_ok());
        for role in ["admin", "owner"] {
            assert!(member_upsert_policy(Some("admin"), None, role, 1).is_err());
            assert!(member_upsert_policy(Some("admin"), Some(role), "member", 2).is_err());
            assert!(member_remove_policy(Some("admin"), Some(role), 2).is_err());
        }
        for actor in [None, Some("member")] {
            assert!(member_upsert_policy(actor, None, "member", 2).is_err());
            assert!(member_remove_policy(actor, Some("member"), 2).is_err());
        }
        assert!(member_upsert_policy(Some("owner"), Some("owner"), "member", 1).is_err());
        assert!(member_remove_policy(Some("owner"), Some("owner"), 1).is_err());
        assert!(member_upsert_policy(Some("owner"), Some("owner"), "member", 2).is_ok());
        assert!(member_remove_policy(Some("owner"), Some("owner"), 2).is_ok());
    }

    #[test]
    fn result_ack_requires_requester_session_and_exact_current_marker() {
        assert!(
            require_exact_result_ack("session-a", "session-a", "7", "42", "r2", "7", "42", "r2")
                .is_ok()
        );
        for (session, epoch, sequence, revision) in [
            ("session-b", "7", "42", "r2"),
            ("session-a", "8", "42", "r2"),
            ("session-a", "7", "43", "r2"),
            ("session-a", "7", "42", "r1"),
        ] {
            assert!(
                require_exact_result_ack(
                    "session-a",
                    session,
                    "7",
                    "42",
                    "r2",
                    epoch,
                    sequence,
                    revision
                )
                .is_err()
            );
        }
        assert!(require_exact_result_ack("a", "a", "", "", "r", "", "", "r").is_err());
    }

    #[test]
    fn terminal_result_identity_binds_every_outcome_field() {
        let first = serde_json::json!(["command-a", "succeeded", "frame-7", [1, 2, 3, 4]]);
        assert_eq!(
            terminal_result_fingerprint(&first),
            terminal_result_fingerprint(&first.clone())
        );
        for changed in [
            serde_json::json!(["command-b", "succeeded", "frame-7", [1, 2, 3, 4]]),
            serde_json::json!(["command-a", "failed", "frame-7", [1, 2, 3, 4]]),
            serde_json::json!(["command-a", "succeeded", "frame-8", [1, 2, 3, 4]]),
            serde_json::json!(["command-a", "succeeded", "frame-7", [1, 2, 3, 5]]),
        ] {
            assert_ne!(
                terminal_result_fingerprint(&first),
                terminal_result_fingerprint(&changed)
            );
        }
    }
}
