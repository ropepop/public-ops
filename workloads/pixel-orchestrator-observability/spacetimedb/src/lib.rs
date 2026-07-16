#![allow(non_snake_case)]

use spacetimedb::{
    CaseConversionPolicy, ReducerContext, ScheduleAt, Table, TimeDuration, Timestamp,
};

// Existing production Spacetime modules are owned by this identity. The owner
// explicitly enrolls the Pixel's authenticated writer identity after publish.
const OPERATOR_IDENTITY: &str = "c200ba2b19cf478fbb75ce99bd969ebe47cb313909a7ebf4d5f19c6bf3e325f9";
const RETENTION_HOURS: i64 = 24;
const CLEANUP_INTERVAL_SECS: u64 = 60 * 60;
const CLEANUP_BATCH_SIZE: u32 = 1_000;
const CORRELATION_ID_LEN: usize = 24;
const MAX_BUILD_ID_LEN: usize = 96;
const MAX_DURATION_MILLIS: u64 = 7 * 24 * 60 * 60 * 1_000;
const MAX_COUNT: u64 = 1_000_000_000;
const MAX_BYTE_COUNT: u64 = 1024 * 1024 * 1024 * 1024;

#[spacetimedb::settings]
const CASE_CONVERSION_POLICY: CaseConversionPolicy = CaseConversionPolicy::None;

// This is access-control state, not telemetry. The authenticated identity is
// deliberately not copied into event rows, which avoids storing a stable phone
// identifier in operational history.
#[spacetimedb::table(accessor = pixelorchestrator_reporter)]
#[derive(Clone)]
pub struct PixelorchestratorReporter {
    #[primary_key]
    pub identity: String,
    pub enabled: bool,
    pub updatedAt: Timestamp,
}

// Private by default: there is intentionally no `public` table attribute.
// Every string except buildId is selected from a fixed vocabulary. buildId is
// a tightly bounded release token, never a device, account, or user identifier.
#[spacetimedb::table(
    accessor = pixelorchestrator_event,
    index(accessor = occurredAt, btree(columns = [occurredAt])),
    index(accessor = expiresAt, btree(columns = [expiresAt]))
)]
#[derive(Clone)]
pub struct PixelorchestratorEvent {
    #[primary_key]
    pub correlationId: String,
    pub eventType: String,
    pub component: String,
    pub cleanupCategory: String,
    pub status: String,
    pub result: String,
    pub priority: String,
    pub buildId: String,
    pub durationMillis: u64,
    pub count: u64,
    pub byteCount: u64,
    pub occurredAt: Timestamp,
    pub expiresAt: Timestamp,
}

#[spacetimedb::table(
    accessor = pixelorchestrator_retention_schedule,
    scheduled(pixelorchestrator_scheduled_cleanup)
)]
#[derive(Clone)]
pub struct PixelorchestratorRetentionSchedule {
    #[primary_key]
    #[auto_inc]
    pub scheduled_id: u64,
    pub scheduled_at: ScheduleAt,
    pub batchSize: u32,
    pub updatedAt: Timestamp,
}

#[spacetimedb::reducer(init)]
pub fn init(ctx: &ReducerContext) {
    ensure_retention_schedule(ctx);
}

#[spacetimedb::reducer]
pub fn pixelorchestrator_set_reporter(
    ctx: &ReducerContext,
    reporterIdentity: String,
    enabled: bool,
) -> Result<(), String> {
    require_operator(ctx)?;
    let identity = clean_identity(&reporterIdentity)?;
    let row = PixelorchestratorReporter {
        identity: identity.clone(),
        enabled,
        updatedAt: ctx.timestamp,
    };
    let table = ctx.db.pixelorchestrator_reporter();
    if table.identity().find(&identity).is_some() {
        table.identity().update(row);
    } else {
        table.insert(row);
    }
    Ok(())
}

#[spacetimedb::reducer]
// Reducer parameters intentionally remain separate scalar fields: accepting a
// bundle or JSON object would reopen the free-form telemetry channel this
// module is designed to prevent.
#[allow(clippy::too_many_arguments)]
pub fn pixelorchestrator_append_event(
    ctx: &ReducerContext,
    correlationId: String,
    eventType: String,
    component: String,
    cleanupCategory: String,
    status: String,
    result: String,
    priority: String,
    buildId: String,
    durationMillis: u64,
    count: u64,
    byteCount: u64,
) -> Result<(), String> {
    require_reporter(ctx)?;
    let correlation_id = clean_correlation_id(&correlationId)?;
    let event_type = clean_event_type(&eventType)?;
    let component = clean_component(&component)?;
    let cleanup_category = clean_cleanup_category(&cleanupCategory, &event_type)?;
    let status = clean_status(&status)?;
    let result = clean_result(&result)?;
    let priority = clean_priority(&priority)?;
    let build_id = clean_build_id(&buildId)?;
    let duration_millis = clean_duration(durationMillis)?;
    let count = clean_count(count)?;
    let byte_count = clean_byte_count(byteCount)?;

    let table = ctx.db.pixelorchestrator_event();
    if table.correlationId().find(&correlation_id).is_some() {
        return Ok(());
    }

    let now = ctx.timestamp;
    table.insert(PixelorchestratorEvent {
        correlationId: correlation_id,
        eventType: event_type,
        component,
        cleanupCategory: cleanup_category,
        status,
        result,
        priority,
        buildId: build_id,
        durationMillis: duration_millis,
        count,
        byteCount: byte_count,
        occurredAt: now,
        expiresAt: now + retention_duration(),
    });
    Ok(())
}

#[spacetimedb::reducer]
pub fn pixelorchestrator_scheduled_cleanup(
    ctx: &ReducerContext,
    arg: PixelorchestratorRetentionSchedule,
) -> Result<(), String> {
    if ctx.sender() != ctx.database_identity() {
        return Err("scheduled cleanup is internal only".into());
    }
    cleanup_all_expired(ctx, arg.batchSize);
    Ok(())
}

fn ensure_retention_schedule(ctx: &ReducerContext) {
    let table = ctx.db.pixelorchestrator_retention_schedule();
    let schedule =
        ScheduleAt::Interval(std::time::Duration::from_secs(CLEANUP_INTERVAL_SECS).into());
    if let Some(existing) = table.iter().next() {
        table
            .scheduled_id()
            .update(PixelorchestratorRetentionSchedule {
                scheduled_at: schedule,
                batchSize: CLEANUP_BATCH_SIZE,
                updatedAt: ctx.timestamp,
                ..existing
            });
    } else {
        table.insert(PixelorchestratorRetentionSchedule {
            scheduled_id: 0,
            scheduled_at: schedule,
            batchSize: CLEANUP_BATCH_SIZE,
            updatedAt: ctx.timestamp,
        });
    }
}

fn cleanup_all_expired(ctx: &ReducerContext, requested_batch_size: u32) -> u64 {
    drain_expired_batches(requested_batch_size, |batch_size| {
        cleanup_expired_batch(ctx, batch_size)
    })
}

fn cleanup_expired_batch(ctx: &ReducerContext, batch_size: u32) -> u32 {
    let batch_size = batch_size.clamp(1, CLEANUP_BATCH_SIZE) as usize;
    let now = ctx.timestamp;
    let rows: Vec<_> = ctx
        .db
        .pixelorchestrator_event()
        .expiresAt()
        .filter(..=now)
        .take(batch_size)
        .collect();
    let deleted = rows.len() as u32;
    for row in rows {
        ctx.db
            .pixelorchestrator_event()
            .correlationId()
            .delete(&row.correlationId);
    }
    deleted
}

fn drain_expired_batches<F>(requested_batch_size: u32, mut delete_batch: F) -> u64
where
    F: FnMut(u32) -> u32,
{
    let batch_size = requested_batch_size.clamp(1, CLEANUP_BATCH_SIZE);
    let mut total_deleted = 0u64;
    loop {
        let deleted = delete_batch(batch_size);
        total_deleted = total_deleted.saturating_add(deleted as u64);
        if deleted < batch_size {
            return total_deleted;
        }
    }
}

fn require_operator(ctx: &ReducerContext) -> Result<(), String> {
    if !ctx.sender_auth().has_jwt() {
        return Err("authenticated operator required".into());
    }
    if ctx.sender().to_string() == OPERATOR_IDENTITY {
        Ok(())
    } else {
        Err("operator identity required".into())
    }
}

fn require_reporter(ctx: &ReducerContext) -> Result<(), String> {
    if !ctx.sender_auth().has_jwt() {
        return Err("authenticated reporter required".into());
    }
    let identity = ctx.sender().to_string();
    if identity == OPERATOR_IDENTITY {
        return Ok(());
    }
    let Some(reporter) = ctx
        .db
        .pixelorchestrator_reporter()
        .identity()
        .find(&identity)
    else {
        return Err("authorized reporter required".into());
    };
    if reporter.enabled {
        Ok(())
    } else {
        Err("reporter disabled".into())
    }
}

fn retention_duration() -> TimeDuration {
    TimeDuration::from_micros(RETENTION_HOURS * 60 * 60 * 1_000_000)
}

fn clean_identity(value: &str) -> Result<String, String> {
    let identity = value.trim();
    if identity.len() != 64 || !identity.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return Err(
            "reporter identity must be a 64-character hexadecimal Spacetime identity".into(),
        );
    }
    Ok(identity.to_ascii_lowercase())
}

fn clean_correlation_id(value: &str) -> Result<String, String> {
    let value = value.trim();
    if value.len() != CORRELATION_ID_LEN
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || matches!(byte, b'a'..=b'f'))
    {
        return Err("correlation id must be 24 lowercase hexadecimal characters".into());
    }
    Ok(value.to_string())
}

fn clean_event_type(value: &str) -> Result<String, String> {
    clean_fixed_token(
        value,
        "event type",
        &[
            "app_session",
            "manual_action",
            "component_transition",
            "health_change",
            "setting_change",
            "cleanup_result",
            "scheduling_failure",
            "permission_change",
            "dropped_event_summary",
        ],
    )
}

fn clean_component(value: &str) -> Result<String, String> {
    clean_fixed_token(
        value,
        "component",
        &[
            "orchestrator",
            "stack",
            "automation",
            "speedtest",
            "cellmapper",
            "ticket_readiness",
            "touch_brightness",
            "cpu",
            "gpu",
            "thermal",
            "permissions",
            "cleanup",
            "scheduler",
            "diagnostics",
            "supervisor",
            "management",
            "ssh",
            "vpn",
            "telemetry",
        ],
    )
}

fn clean_cleanup_category(value: &str, event_type: &str) -> Result<String, String> {
    let category = clean_fixed_token(
        value,
        "cleanup category",
        &[
            "none",
            "ticket_hierarchy_xml",
            "deployment_action_results",
            "support_bundles",
            "root_command_history",
            "stack_logs",
            "dns_history",
            "retired_artifacts",
            "deployment_archives",
            "app_cache",
        ],
    )?;
    match (event_type, category.as_str()) {
        ("cleanup_result", "none") => {
            Err("cleanup result events require a cleanup category".into())
        }
        ("cleanup_result", _) => Ok(category),
        (_, "none") => Ok(category),
        _ => Err("cleanup categories are only valid for cleanup result events".into()),
    }
}

fn clean_status(value: &str) -> Result<String, String> {
    clean_fixed_token(
        value,
        "status",
        &[
            "unknown",
            "healthy",
            "degraded",
            "failed",
            "stale",
            "enabled",
            "disabled",
            "running",
            "completed",
            "skipped",
            "unavailable",
        ],
    )
}

fn clean_result(value: &str) -> Result<String, String> {
    clean_fixed_token(
        value,
        "result",
        &[
            "none",
            "ok",
            "failed",
            "cancelled",
            "dropped",
            "rejected",
            "retrying",
        ],
    )
}

fn clean_priority(value: &str) -> Result<String, String> {
    clean_fixed_token(value, "priority", &["low", "normal", "high", "critical"])
}

fn clean_fixed_token(value: &str, label: &str, allowed: &[&str]) -> Result<String, String> {
    let value = value.trim();
    if allowed.contains(&value) {
        Ok(value.to_string())
    } else {
        Err(format!("unsupported {label}"))
    }
}

fn clean_build_id(value: &str) -> Result<String, String> {
    let value = value.trim();
    if value.is_empty() || value.len() > MAX_BUILD_ID_LEN {
        return Err(format!("build id must be 1-{MAX_BUILD_ID_LEN} characters"));
    }
    if !value
        .bytes()
        .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'-'))
    {
        return Err("build id contains unsupported characters".into());
    }
    if looks_like_ipv4(value) {
        return Err("build id must not be an IP address".into());
    }
    Ok(value.to_string())
}

fn looks_like_ipv4(value: &str) -> bool {
    let parts: Vec<_> = value.split('.').collect();
    parts.len() == 4
        && parts.iter().all(|part| {
            !part.is_empty()
                && part.bytes().all(|byte| byte.is_ascii_digit())
                && part.parse::<u8>().is_ok()
        })
}

fn clean_duration(value: u64) -> Result<u64, String> {
    if value <= MAX_DURATION_MILLIS {
        Ok(value)
    } else {
        Err("duration exceeds seven days".into())
    }
}

fn clean_count(value: u64) -> Result<u64, String> {
    if value <= MAX_COUNT {
        Ok(value)
    } else {
        Err("count exceeds the safe limit".into())
    }
}

fn clean_byte_count(value: u64) -> Result<u64, String> {
    if value <= MAX_BYTE_COUNT {
        Ok(value)
    } else {
        Err("byte count exceeds one tebibyte".into())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn retention_is_exactly_twenty_four_hours() {
        assert_eq!(
            retention_duration(),
            TimeDuration::from_micros(24 * 60 * 60 * 1_000_000)
        );
    }

    #[test]
    fn event_vocabulary_is_fixed() {
        assert_eq!(
            clean_event_type("health_change").as_deref(),
            Ok("health_change")
        );
        assert_eq!(
            clean_component("ticket_readiness").as_deref(),
            Ok("ticket_readiness")
        );
        assert_eq!(clean_status("degraded").as_deref(), Ok("degraded"));
        assert_eq!(clean_result("retrying").as_deref(), Ok("retrying"));
        assert_eq!(clean_priority("critical").as_deref(), Ok("critical"));
        assert!(clean_event_type("ticket_control_code").is_err());
        assert!(clean_component("chatgpt_prompt").is_err());
        assert!(clean_status("arbitrary user text").is_err());
        assert!(clean_result("shell output").is_err());
        assert!(clean_priority("urgent-ish").is_err());
    }

    #[test]
    fn cleanup_categories_are_fixed_and_bound_to_cleanup_events() {
        assert_eq!(
            clean_cleanup_category("root_command_history", "cleanup_result").as_deref(),
            Ok("root_command_history")
        );
        assert_eq!(
            clean_cleanup_category("none", "health_change").as_deref(),
            Ok("none")
        );
        assert!(clean_cleanup_category("none", "cleanup_result").is_err());
        assert!(clean_cleanup_category("root_command_history", "health_change").is_err());
        assert!(clean_cleanup_category("arbitrary_path", "cleanup_result").is_err());
    }

    #[test]
    fn scheduled_cleanup_drains_backlogs_larger_than_one_batch() {
        let mut remaining = 3_507u32;
        let mut calls = 0u32;
        let deleted = drain_expired_batches(CLEANUP_BATCH_SIZE, |batch_size| {
            calls += 1;
            let current = remaining.min(batch_size);
            remaining -= current;
            current
        });
        assert_eq!(deleted, 3_507);
        assert_eq!(remaining, 0);
        assert_eq!(calls, 4);

        let mut exact_boundary_remaining = 3_000u32;
        let mut boundary_calls = 0u32;
        let boundary_deleted = drain_expired_batches(CLEANUP_BATCH_SIZE, |batch_size| {
            boundary_calls += 1;
            let current = exact_boundary_remaining.min(batch_size);
            exact_boundary_remaining -= current;
            current
        });
        assert_eq!(boundary_deleted, 3_000);
        assert_eq!(exact_boundary_remaining, 0);
        assert_eq!(boundary_calls, 4);
    }

    #[test]
    fn identifiers_cannot_carry_paths_ips_or_free_text() {
        assert_eq!(
            clean_correlation_id("0123456789abcdef01234567").as_deref(),
            Ok("0123456789abcdef01234567")
        );
        assert!(clean_correlation_id("0123456789ABCDEF01234567").is_err());
        assert!(clean_correlation_id("/data/local/pixel-stack").is_err());
        assert_eq!(
            clean_build_id("20260713T120000Z-a1b2c3d").as_deref(),
            Ok("20260713T120000Z-a1b2c3d")
        );
        assert!(clean_build_id("/data/local/build").is_err());
        assert!(clean_build_id("the user-facing status text").is_err());
        assert!(clean_build_id("100.76.50.43").is_err());
        assert!(clean_build_id(&"a".repeat(MAX_BUILD_ID_LEN + 1)).is_err());
    }

    #[test]
    fn scalar_measurements_are_bounded() {
        assert_eq!(clean_duration(MAX_DURATION_MILLIS), Ok(MAX_DURATION_MILLIS));
        assert!(clean_duration(MAX_DURATION_MILLIS + 1).is_err());
        assert_eq!(clean_count(MAX_COUNT), Ok(MAX_COUNT));
        assert!(clean_count(MAX_COUNT + 1).is_err());
        assert_eq!(clean_byte_count(MAX_BYTE_COUNT), Ok(MAX_BYTE_COUNT));
        assert!(clean_byte_count(MAX_BYTE_COUNT + 1).is_err());
    }

    #[test]
    fn reporter_identity_is_strict_and_normalized() {
        let upper = "A".repeat(64);
        assert_eq!(clean_identity(&upper).unwrap(), "a".repeat(64));
        assert!(clean_identity("phone").is_err());
        assert!(clean_identity(&"g".repeat(64)).is_err());
    }
}
