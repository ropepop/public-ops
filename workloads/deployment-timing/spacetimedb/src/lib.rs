#![allow(non_snake_case)]

use spacetimedb::{
    CaseConversionPolicy, ReducerContext, ScheduleAt, Table, TimeDuration, Timestamp,
};

// The database owner used by the existing production Spacetime modules. It can
// report immediately after publishing this standalone module and can enroll a
// separate deployment identity later without opening writes to arbitrary users.
const OPERATOR_IDENTITY: &str = "c200ba2b19cf478fbb75ce99bd969ebe47cb313909a7ebf4d5f19c6bf3e325f9";
const RETENTION_DAYS: i64 = 30;
const CLEANUP_INTERVAL_SECS: u64 = 60 * 60;
const CLEANUP_BATCH_SIZE: u32 = 1_000;
const MAX_DURATION_MILLIS: u64 = 7 * 24 * 60 * 60 * 1_000;
const MAX_COMPLETION_PHASES: usize = 64;
const MAX_PHASE_BUNDLE_BYTES: usize = 8_256;
const MAX_EVENT_ID_LEN: usize = 180;

#[spacetimedb::settings]
const CASE_CONVERSION_POLICY: CaseConversionPolicy = CaseConversionPolicy::None;

#[spacetimedb::table(accessor = deploymenttiming_reporter)]
#[derive(Clone)]
pub struct DeploymenttimingReporter {
    #[primary_key]
    pub identity: String,
    pub label: String,
    pub enabled: bool,
    pub updatedAt: Timestamp,
}

// Each lifecycle notice is a new row. In particular, a completed run never
// mutates its earlier started row, so retry-safe deployment diagnostics remain
// append-only.
#[spacetimedb::table(
    accessor = deploymenttiming_run,
    index(accessor = runOccurredAt, btree(columns = [runId, occurredAt])),
    index(accessor = sourceOccurredAt, btree(columns = [source, occurredAt])),
    index(accessor = expiresAt, btree(columns = [expiresAt]))
)]
#[derive(Clone)]
pub struct DeploymenttimingRun {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub runId: String,
    pub source: String,
    pub action: String,
    pub lifecycle: String,
    pub status: String,
    pub releaseId: String,
    pub profile: String,
    pub target: String,
    pub totalDurationMillis: u64,
    pub reporterIdentity: String,
    pub occurredAt: Timestamp,
    pub expiresAt: Timestamp,
}

// Phase rows duplicate the run metadata deliberately: operators can query a
// single phase table without joining or depending on a mutable parent record.
#[spacetimedb::table(
    accessor = deploymenttiming_phase,
    index(accessor = runOccurredAt, btree(columns = [runId, occurredAt])),
    index(accessor = sourceOccurredAt, btree(columns = [source, occurredAt])),
    index(accessor = expiresAt, btree(columns = [expiresAt]))
)]
#[derive(Clone)]
pub struct DeploymenttimingPhase {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub runId: String,
    pub source: String,
    pub action: String,
    pub phase: String,
    pub status: String,
    pub releaseId: String,
    pub profile: String,
    pub target: String,
    pub durationMillis: u64,
    pub totalDurationMillis: u64,
    pub reporterIdentity: String,
    pub occurredAt: Timestamp,
    pub expiresAt: Timestamp,
}

#[spacetimedb::table(
    accessor = deploymenttiming_retention_schedule,
    scheduled(deploymenttiming_scheduled_cleanup)
)]
#[derive(Clone)]
pub struct DeploymenttimingRetentionSchedule {
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
pub fn deploymenttiming_set_reporter(
    ctx: &ReducerContext,
    reporterIdentity: String,
    label: String,
    enabled: bool,
) -> Result<(), String> {
    require_operator(ctx)?;
    let identity = clean_identity(&reporterIdentity)?;
    let label = clean_token(&label, "reporter label", 80)?;
    let row = DeploymenttimingReporter {
        identity: identity.clone(),
        label,
        enabled,
        updatedAt: ctx.timestamp,
    };
    let table = ctx.db.deploymenttiming_reporter();
    if table.identity().find(&identity).is_some() {
        table.identity().update(row);
    } else {
        table.insert(row);
    }
    Ok(())
}

#[spacetimedb::reducer]
pub fn deploymenttiming_append_run(
    ctx: &ReducerContext,
    eventId: String,
    runId: String,
    source: String,
    action: String,
    lifecycle: String,
    status: String,
    releaseId: String,
    profile: String,
    target: String,
    totalDurationMillis: u64,
) -> Result<(), String> {
    let reporter_identity = require_reporter(ctx)?;
    let id = clean_token(&eventId, "event id", MAX_EVENT_ID_LEN)?;
    let run_id = clean_token(&runId, "run id", 120)?;
    let source = clean_source(&source)?;
    let action = clean_token(&action, "action", 80)?;
    let lifecycle = clean_run_lifecycle(&lifecycle)?;
    let status = clean_run_status(&status, &lifecycle)?;
    let release_id = clean_optional_token(&releaseId, 160)?;
    let profile = clean_optional_token(&profile, 48)?;
    let target = clean_optional_token(&target, 160)?;
    let total_duration_millis = clean_duration(totalDurationMillis)?;
    let table = ctx.db.deploymenttiming_run();
    if table.id().find(&id).is_some() {
        return Ok(());
    }
    let now = ctx.timestamp;
    table.insert(DeploymenttimingRun {
        id,
        runId: run_id,
        source,
        action,
        lifecycle,
        status,
        releaseId: release_id,
        profile,
        target,
        totalDurationMillis: total_duration_millis,
        reporterIdentity: reporter_identity,
        occurredAt: now,
        expiresAt: now + retention_duration(),
    });
    Ok(())
}

#[spacetimedb::reducer]
pub fn deploymenttiming_append_phase(
    ctx: &ReducerContext,
    eventId: String,
    runId: String,
    source: String,
    action: String,
    phase: String,
    status: String,
    releaseId: String,
    profile: String,
    target: String,
    durationMillis: u64,
    totalDurationMillis: u64,
) -> Result<(), String> {
    let reporter_identity = require_reporter(ctx)?;
    let id = clean_token(&eventId, "event id", MAX_EVENT_ID_LEN)?;
    let run_id = clean_token(&runId, "run id", 120)?;
    let source = clean_source(&source)?;
    let action = clean_token(&action, "action", 80)?;
    let phase = clean_token(&phase, "phase", 100)?;
    let status = clean_phase_status(&status)?;
    let release_id = clean_optional_token(&releaseId, 160)?;
    let profile = clean_optional_token(&profile, 48)?;
    let target = clean_optional_token(&target, 160)?;
    let duration_millis = clean_duration(durationMillis)?;
    let total_duration_millis = clean_duration(totalDurationMillis)?;
    let table = ctx.db.deploymenttiming_phase();
    if table.id().find(&id).is_some() {
        return Ok(());
    }
    let now = ctx.timestamp;
    table.insert(DeploymenttimingPhase {
        id,
        runId: run_id,
        source,
        action,
        phase,
        status,
        releaseId: release_id,
        profile,
        target,
        durationMillis: duration_millis,
        totalDurationMillis: total_duration_millis,
        reporterIdentity: reporter_identity,
        occurredAt: now,
        expiresAt: now + retention_duration(),
    });
    Ok(())
}

/// Appends a completed run and its phase rows in one reducer transaction.
///
/// `phaseBundle` is compact, bounded transport data rather than log output:
/// `phase=status=durationMillis=totalDurationMillis@...`. All entries are
/// parsed and validated before any table mutation. Replaying the same bundle
/// is safe because the phase and finished-run IDs are deterministic.
#[spacetimedb::reducer]
pub fn deploymenttiming_append_completed_run(
    ctx: &ReducerContext,
    eventId: String,
    runId: String,
    source: String,
    action: String,
    status: String,
    releaseId: String,
    profile: String,
    target: String,
    totalDurationMillis: u64,
    phaseBundle: String,
) -> Result<(), String> {
    let reporter_identity = require_reporter(ctx)?;
    let id = clean_token(&eventId, "event id", MAX_EVENT_ID_LEN)?;
    let run_id = clean_token(&runId, "run id", 120)?;
    let source = clean_source(&source)?;
    let action = clean_token(&action, "action", 80)?;
    let status = clean_run_status(&status, "finished")?;
    let release_id = clean_optional_token(&releaseId, 160)?;
    let profile = clean_optional_token(&profile, 48)?;
    let target = clean_optional_token(&target, 160)?;
    let total_duration_millis = clean_duration(totalDurationMillis)?;
    let phases = parse_completed_phase_bundle(&phaseBundle, &run_id)?;
    let now = ctx.timestamp;
    let expires_at = now + retention_duration();

    // Reducers are transactional. The parser above completes all fallible
    // validation before this loop, so this reducer exposes either the entire
    // completion batch or none of it.
    let phase_table = ctx.db.deploymenttiming_phase();
    for phase in phases {
        if phase_table.id().find(&phase.event_id).is_some() {
            continue;
        }
        phase_table.insert(DeploymenttimingPhase {
            id: phase.event_id,
            runId: run_id.clone(),
            source: source.clone(),
            action: action.clone(),
            phase: phase.name,
            status: phase.status,
            releaseId: release_id.clone(),
            profile: profile.clone(),
            target: target.clone(),
            durationMillis: phase.duration_millis,
            totalDurationMillis: phase.total_duration_millis,
            reporterIdentity: reporter_identity.clone(),
            occurredAt: now,
            expiresAt: expires_at,
        });
    }

    let run_table = ctx.db.deploymenttiming_run();
    if run_table.id().find(&id).is_none() {
        run_table.insert(DeploymenttimingRun {
            id,
            runId: run_id,
            source,
            action,
            lifecycle: "finished".into(),
            status,
            releaseId: release_id,
            profile,
            target,
            totalDurationMillis: total_duration_millis,
            reporterIdentity: reporter_identity,
            occurredAt: now,
            expiresAt: expires_at,
        });
    }
    Ok(())
}

#[spacetimedb::reducer]
pub fn deploymenttiming_scheduled_cleanup(
    ctx: &ReducerContext,
    arg: DeploymenttimingRetentionSchedule,
) -> Result<(), String> {
    if ctx.sender() != ctx.database_identity() {
        return Err("scheduled cleanup is internal only".into());
    }
    cleanup_expired(ctx, arg.batchSize);
    Ok(())
}

fn ensure_retention_schedule(ctx: &ReducerContext) {
    let table = ctx.db.deploymenttiming_retention_schedule();
    let schedule =
        ScheduleAt::Interval(std::time::Duration::from_secs(CLEANUP_INTERVAL_SECS).into());
    if let Some(existing) = table.iter().next() {
        table
            .scheduled_id()
            .update(DeploymenttimingRetentionSchedule {
                scheduled_at: schedule,
                batchSize: CLEANUP_BATCH_SIZE,
                updatedAt: ctx.timestamp,
                ..existing
            });
    } else {
        table.insert(DeploymenttimingRetentionSchedule {
            scheduled_id: 0,
            scheduled_at: schedule,
            batchSize: CLEANUP_BATCH_SIZE,
            updatedAt: ctx.timestamp,
        });
    }
}

fn cleanup_expired(ctx: &ReducerContext, requested_batch_size: u32) -> u32 {
    let batch_size = requested_batch_size.clamp(1, CLEANUP_BATCH_SIZE) as usize;
    let now = ctx.timestamp;
    let run_rows: Vec<_> = ctx
        .db
        .deploymenttiming_run()
        .expiresAt()
        .filter(..=now)
        .take(batch_size)
        .collect();
    let mut deleted = 0u32;
    for row in run_rows {
        ctx.db.deploymenttiming_run().id().delete(&row.id);
        deleted += 1;
    }
    let remaining = batch_size.saturating_sub(deleted as usize);
    if remaining == 0 {
        return deleted;
    }
    let phase_rows: Vec<_> = ctx
        .db
        .deploymenttiming_phase()
        .expiresAt()
        .filter(..=now)
        .take(remaining)
        .collect();
    for row in phase_rows {
        ctx.db.deploymenttiming_phase().id().delete(&row.id);
        deleted += 1;
    }
    deleted
}

fn require_operator(ctx: &ReducerContext) -> Result<(), String> {
    if ctx.sender().to_string() == OPERATOR_IDENTITY {
        Ok(())
    } else {
        Err("operator identity required".into())
    }
}

fn require_reporter(ctx: &ReducerContext) -> Result<String, String> {
    let identity = ctx.sender().to_string();
    if identity == OPERATOR_IDENTITY {
        return Ok(identity);
    }
    let Some(reporter) = ctx
        .db
        .deploymenttiming_reporter()
        .identity()
        .find(&identity)
    else {
        return Err("authorized reporter required".into());
    };
    if !reporter.enabled {
        return Err("reporter disabled".into());
    }
    Ok(identity)
}

fn retention_duration() -> TimeDuration {
    TimeDuration::from_micros(RETENTION_DAYS * 24 * 60 * 60 * 1_000_000)
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

fn clean_source(value: &str) -> Result<String, String> {
    let source = clean_token(value, "source", 16)?;
    match source.as_str() {
        "ops" | "pixel" => Ok(source),
        _ => Err("source must be ops or pixel".into()),
    }
}

fn clean_run_lifecycle(value: &str) -> Result<String, String> {
    let lifecycle = clean_token(value, "run lifecycle", 16)?;
    match lifecycle.as_str() {
        "started" | "finished" => Ok(lifecycle),
        _ => Err("run lifecycle must be started or finished".into()),
    }
}

fn clean_run_status(value: &str, lifecycle: &str) -> Result<String, String> {
    let status = clean_token(value, "run status", 16)?;
    match (lifecycle, status.as_str()) {
        ("started", "running") | ("finished", "ok" | "failed" | "cancelled") => Ok(status),
        _ => Err("run status does not match its lifecycle".into()),
    }
}

fn clean_phase_status(value: &str) -> Result<String, String> {
    let status = clean_token(value, "phase status", 16)?;
    match status.as_str() {
        "ok" | "failed" | "skipped" => Ok(status),
        _ => Err("phase status must be ok, failed, or skipped".into()),
    }
}

fn clean_duration(value: u64) -> Result<u64, String> {
    if value > MAX_DURATION_MILLIS {
        Err("duration exceeds seven days".into())
    } else {
        Ok(value)
    }
}

#[derive(Debug, PartialEq, Eq)]
struct CompletedPhase {
    event_id: String,
    name: String,
    status: String,
    duration_millis: u64,
    total_duration_millis: u64,
}

fn parse_completed_phase_bundle(
    phase_bundle: &str,
    run_id: &str,
) -> Result<Vec<CompletedPhase>, String> {
    let phase_bundle = phase_bundle.trim();
    if phase_bundle.is_empty() || phase_bundle == "-" {
        return Ok(Vec::new());
    }
    if phase_bundle.len() > MAX_PHASE_BUNDLE_BYTES {
        return Err("phase bundle exceeds the safe size limit".into());
    }

    let mut phases = Vec::new();
    for entry in phase_bundle.split('@') {
        if phases.len() >= MAX_COMPLETION_PHASES {
            return Err("phase bundle exceeds 64 entries".into());
        }
        let mut fields = entry.split('=');
        let Some(name) = fields.next() else {
            return Err("phase bundle entry is missing a phase name".into());
        };
        let Some(status) = fields.next() else {
            return Err("phase bundle entry is missing a status".into());
        };
        let Some(duration) = fields.next() else {
            return Err("phase bundle entry is missing a duration".into());
        };
        let Some(total_duration) = fields.next() else {
            return Err("phase bundle entry is missing a total duration".into());
        };
        if fields.next().is_some() {
            return Err("phase bundle entry has too many fields".into());
        }

        let name = clean_completed_phase_name(name)?;
        let status = clean_phase_status(status)?;
        let duration_millis = clean_phase_bundle_duration(duration, "phase duration")?;
        let total_duration_millis =
            clean_phase_bundle_duration(total_duration, "phase total duration")?;
        let event_id = clean_token(
            &format!("{run_id}:phase:{name}:{total_duration_millis}"),
            "phase event id",
            MAX_EVENT_ID_LEN,
        )?;
        phases.push(CompletedPhase {
            event_id,
            name,
            status,
            duration_millis,
            total_duration_millis,
        });
    }
    Ok(phases)
}

fn clean_completed_phase_name(value: &str) -> Result<String, String> {
    if value.bytes().any(|byte| matches!(byte, b'@' | b'=')) {
        return Err("phase bundle phase name contains a reserved separator".into());
    }
    clean_token(value, "phase", 100)
}

fn clean_phase_bundle_duration(value: &str, label: &str) -> Result<u64, String> {
    let value = value.trim();
    if value.is_empty() || !value.bytes().all(|byte| byte.is_ascii_digit()) {
        return Err(format!("{label} must be a non-negative integer"));
    }
    let duration = value
        .parse::<u64>()
        .map_err(|_| format!("{label} must be a non-negative integer"))?;
    clean_duration(duration)
}

fn clean_optional_token(value: &str, max_len: usize) -> Result<String, String> {
    let value = value.trim();
    if value.is_empty() || value == "-" {
        return Ok("none".into());
    }
    clean_token(value, "optional value", max_len)
}

fn clean_token(value: &str, label: &str, max_len: usize) -> Result<String, String> {
    let value = value.trim();
    if value.is_empty() || value.len() > max_len {
        return Err(format!("{label} must be 1-{max_len} characters"));
    }
    if !value.bytes().all(is_safe_token_byte) {
        return Err(format!("{label} contains unsupported characters"));
    }
    Ok(value.to_string())
}

fn is_safe_token_byte(byte: u8) -> bool {
    byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'-' | b':' | b'/' | b'@' | b'=')
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn retention_is_exactly_thirty_days() {
        assert_eq!(
            retention_duration(),
            TimeDuration::from_micros(30 * 24 * 60 * 60 * 1_000_000)
        );
    }

    #[test]
    fn reporter_tokens_reject_unbounded_or_unsafe_values() {
        assert_eq!(clean_source("ops").as_deref(), Ok("ops"));
        assert!(clean_source("browser").is_err());
        assert!(clean_token("has space", "value", 20).is_err());
        assert!(clean_optional_token("untrusted output", 40).is_err());
        assert!(clean_duration(MAX_DURATION_MILLIS + 1).is_err());
    }

    #[test]
    fn run_lifecycle_statuses_are_constrained() {
        assert_eq!(
            clean_run_status("running", "started").as_deref(),
            Ok("running")
        );
        assert_eq!(clean_run_status("ok", "finished").as_deref(), Ok("ok"));
        assert!(clean_run_status("ok", "started").is_err());
    }

    #[test]
    fn completed_phase_bundle_is_compact_and_strict() {
        let phases = parse_completed_phase_bundle(
            "connect_device=ok=101=134@runtime_postcheck=skipped=0=28508",
            "pixel-20260711T010203Z",
        )
        .expect("valid completion bundle");
        assert_eq!(phases.len(), 2);
        assert_eq!(phases[0].name, "connect_device");
        assert_eq!(phases[0].status, "ok");
        assert_eq!(phases[0].duration_millis, 101);
        assert_eq!(phases[1].total_duration_millis, 28_508);
        assert_eq!(
            phases[1].event_id,
            "pixel-20260711T010203Z:phase:runtime_postcheck:28508"
        );
        assert!(parse_completed_phase_bundle("phase=invalid=1=2", "run-1").is_err());
        assert!(parse_completed_phase_bundle("phase=ok=1", "run-1").is_err());
        assert!(parse_completed_phase_bundle("phase=ok=1=2=extra", "run-1").is_err());
        assert!(parse_completed_phase_bundle("phase=ok=not-a-number=2", "run-1").is_err());
        assert_eq!(parse_completed_phase_bundle("-", "run-1").unwrap().len(), 0);
    }

    #[test]
    fn completed_phase_bundle_limits_entries_and_event_ids_before_writing() {
        let exact_limit = (0..MAX_COMPLETION_PHASES)
            .map(|index| format!("phase_{index}=ok=1={index}"))
            .collect::<Vec<_>>()
            .join("@");
        assert_eq!(
            parse_completed_phase_bundle(&exact_limit, "run-1")
                .expect("64 phases are allowed")
                .len(),
            MAX_COMPLETION_PHASES
        );
        let too_many = format!("{exact_limit}@phase_64=ok=1=64");
        assert!(parse_completed_phase_bundle(&too_many, "run-1").is_err());
        let long_run_id = "r".repeat(120);
        let long_phase_name = "p".repeat(100);
        assert!(parse_completed_phase_bundle(
            &format!("{long_phase_name}=ok=1=1"),
            &long_run_id,
        )
        .is_err());
    }
}
