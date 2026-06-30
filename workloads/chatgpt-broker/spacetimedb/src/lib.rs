#![allow(non_snake_case)]

use spacetimedb::{
    CaseConversionPolicy, Identity, ReducerContext, SpacetimeType, Table, TimeDuration, Timestamp,
    ViewContext,
};

#[spacetimedb::settings]
const CASE_CONVERSION_POLICY: CaseConversionPolicy = CaseConversionPolicy::None;

#[spacetimedb::table(accessor = chatgptbroker_service_identity)]
#[derive(Clone)]
pub struct ChatgptbrokerServiceIdentity {
    #[primary_key]
    pub id: String,
    pub identity: Identity,
    pub serviceName: String,
    pub role: String,
    pub enabled: bool,
    pub updatedAt: Timestamp,
}

#[spacetimedb::table(accessor = chatgptbroker_job, public,
    index(accessor = statusCreatedAt, btree(columns = [status, createdAt])),
    index(accessor = telegramUserStatus, btree(columns = [telegramUserIdHash, status])),
    index(accessor = claimedStatus, btree(columns = [claimedBy, status]))
)]
#[derive(Clone)]
pub struct ChatgptbrokerJob {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub status: String,
    pub telegramChatIdHash: String,
    #[index(btree)]
    pub telegramUserIdHash: String,
    pub projectName: String,
    pub publicStatus: String,
    pub activeAttemptId: String,
    #[index(btree)]
    pub claimedBy: String,
    pub backendId: String,
    pub resultRef: String,
    pub failureCode: String,
    pub cancelRequested: bool,
    pub createdAt: Timestamp,
    #[index(btree)]
    pub updatedAt: Timestamp,
    pub retentionDeleteAfter: Timestamp,
}

#[spacetimedb::table(accessor = chatgptbroker_job_secret,
    index(accessor = notifiedStatus, btree(columns = [notified, status]))
)]
#[derive(Clone)]
pub struct ChatgptbrokerJobSecret {
    #[primary_key]
    pub id: String,
    pub telegramChatId: String,
    pub telegramUserId: String,
    pub prompt: String,
    pub resultText: String,
    #[index(btree)]
    pub status: String,
    pub failureCode: String,
    pub notified: bool,
    pub cancelRequested: bool,
    pub createdAt: Timestamp,
    pub updatedAt: Timestamp,
    pub retentionDeleteAfter: Timestamp,
}

#[spacetimedb::table(accessor = chatgptbroker_attempt,
    index(accessor = jobCreatedAt, btree(columns = [jobId, createdAt])),
    index(accessor = workerStatus, btree(columns = [workerId, status]))
)]
#[derive(Clone)]
pub struct ChatgptbrokerAttempt {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub jobId: String,
    pub status: String,
    pub workerId: String,
    pub backendId: String,
    pub failureCode: String,
    pub createdAt: Timestamp,
    pub updatedAt: Timestamp,
}

#[spacetimedb::table(accessor = chatgptbroker_ocr_input,
    index(accessor = statusUpdatedAt, btree(columns = [status, updatedAt]))
)]
#[derive(Clone)]
pub struct ChatgptbrokerOcrInput {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub jobId: String,
    pub attemptId: String,
    pub workerId: String,
    #[index(btree)]
    pub status: String,
    pub screenshotPngBase64: String,
    pub createdAt: Timestamp,
    pub updatedAt: Timestamp,
}

#[spacetimedb::table(accessor = chatgptbroker_phone_status, public)]
#[derive(Clone)]
pub struct ChatgptbrokerPhoneStatus {
    #[primary_key]
    pub id: String,
    pub workerId: String,
    pub backendId: String,
    pub status: String,
    pub safeDetailsJson: String,
    pub lastSeenAt: Timestamp,
}

#[spacetimedb::table(accessor = chatgptbroker_event,
    index(accessor = jobCreatedAt, btree(columns = [jobId, createdAt]))
)]
#[derive(Clone)]
pub struct ChatgptbrokerEvent {
    #[primary_key]
    pub id: String,
    #[index(btree)]
    pub jobId: String,
    pub attemptId: String,
    pub visibility: String,
    pub kind: String,
    pub publicText: String,
    pub safeDetailsJson: String,
    pub createdAt: Timestamp,
}

#[derive(Clone, SpacetimeType)]
pub struct ChatgptbrokerPhoneWorkRow {
    pub id: String,
    pub status: String,
    pub projectName: String,
    pub prompt: String,
    pub activeAttemptId: String,
    pub claimedBy: String,
    pub cancelRequested: bool,
    pub createdAt: Timestamp,
    pub updatedAt: Timestamp,
}

#[derive(Clone, SpacetimeType)]
pub struct ChatgptbrokerOcrWorkRow {
    pub id: String,
    pub jobId: String,
    pub attemptId: String,
    pub screenshotPngBase64: String,
    pub createdAt: Timestamp,
    pub updatedAt: Timestamp,
}

#[derive(Clone, SpacetimeType)]
pub struct ChatgptbrokerNotificationRow {
    pub id: String,
    pub telegramChatId: String,
    pub telegramUserId: String,
    pub status: String,
    pub publicStatus: String,
    pub resultText: String,
    pub failureCode: String,
    pub updatedAt: Timestamp,
}

#[spacetimedb::reducer(init)]
pub fn init(_ctx: &ReducerContext) {}

#[spacetimedb::reducer(client_connected)]
pub fn identity_connected(_ctx: &ReducerContext) {}

#[spacetimedb::reducer(client_disconnected)]
pub fn identity_disconnected(_ctx: &ReducerContext) {}

#[spacetimedb::reducer]
pub fn chatgptbroker_register_service_identity(
    ctx: &ReducerContext,
    serviceName: String,
    role: String,
) -> Result<(), String> {
    require_any_service(ctx)?;
    let clean_role = bounded(&role, 40);
    match clean_role.as_str() {
        "bot" | "broker" | "phone" | "admin" => {}
        _ => return Err("unsupported service role".into()),
    }
    let id = ctx.sender().to_string();
    let now = ctx.timestamp;
    let row = ChatgptbrokerServiceIdentity {
        id: id.clone(),
        identity: ctx.sender(),
        serviceName: bounded(&serviceName, 80),
        role: clean_role,
        enabled: true,
        updatedAt: now,
    };
    if ctx
        .db
        .chatgptbroker_service_identity()
        .id()
        .find(&id)
        .is_some()
    {
        ctx.db.chatgptbroker_service_identity().id().update(row);
    } else {
        ctx.db.chatgptbroker_service_identity().insert(row);
    }
    Ok(())
}

#[spacetimedb::reducer]
pub fn chatgptbroker_submit_job(
    ctx: &ReducerContext,
    id: String,
    telegramChatId: String,
    telegramUserId: String,
    telegramChatIdHash: String,
    telegramUserIdHash: String,
    projectName: String,
    prompt: String,
    retentionMillis: u64,
) -> Result<(), String> {
    require_service(ctx, "bot")?;
    let now = ctx.timestamp;
    let clean_id = required(&id, "job id required")?;
    let clean_prompt = bounded_nonempty(&prompt, 12000, "prompt is required")?;
    let retentionDeleteAfter = now + retention_duration(retentionMillis);
    if ctx.db.chatgptbroker_job().id().find(&clean_id).is_some() {
        return Err("job already exists".into());
    }
    ctx.db.chatgptbroker_job().insert(ChatgptbrokerJob {
        id: clean_id.clone(),
        status: "queued".into(),
        telegramChatIdHash: bounded(&telegramChatIdHash, 128),
        telegramUserIdHash: bounded(&telegramUserIdHash, 128),
        projectName: bounded(&projectName, 120),
        publicStatus: "Queued".into(),
        activeAttemptId: String::new(),
        claimedBy: String::new(),
        backendId: String::new(),
        resultRef: String::new(),
        failureCode: String::new(),
        cancelRequested: false,
        createdAt: now,
        updatedAt: now,
        retentionDeleteAfter,
    });
    ctx.db
        .chatgptbroker_job_secret()
        .insert(ChatgptbrokerJobSecret {
            id: clean_id.clone(),
            telegramChatId: bounded(&telegramChatId, 80),
            telegramUserId: bounded(&telegramUserId, 80),
            prompt: clean_prompt,
            resultText: String::new(),
            status: "queued".into(),
            failureCode: String::new(),
            notified: false,
            cancelRequested: false,
            createdAt: now,
            updatedAt: now,
            retentionDeleteAfter,
        });
    insert_event(
        ctx,
        &clean_id,
        "",
        "public",
        "job_queued",
        "Queued",
        "{}",
        now,
    );
    Ok(())
}

#[spacetimedb::reducer]
pub fn chatgptbroker_request_cancel(ctx: &ReducerContext, jobId: String) -> Result<(), String> {
    require_service(ctx, "bot")?;
    let clean_job_id = required(&jobId, "job id required")?;
    let Some(existing) = ctx.db.chatgptbroker_job().id().find(&clean_job_id) else {
        return Err("job not found".into());
    };
    let next_status = match existing.status.as_str() {
        "queued" | "waiting_phone" | "failed_retryable" => "cancelled",
        "running" | "ocr_pending" => "cancel_requested",
        "succeeded" | "failed_final" | "cancelled" => return Err("job already finished".into()),
        _ => "cancel_requested",
    };
    let public = if next_status == "cancelled" {
        "Cancelled"
    } else {
        "Cancellation requested"
    };
    set_job_status(
        ctx,
        &clean_job_id,
        next_status,
        public,
        &existing.activeAttemptId,
        "",
        "",
        existing.claimedBy.clone(),
        existing.backendId.clone(),
        true,
    )
}

#[spacetimedb::reducer]
pub fn chatgptbroker_phone_heartbeat(
    ctx: &ReducerContext,
    workerId: String,
    backendId: String,
    status: String,
    safeDetailsJson: String,
) -> Result<(), String> {
    require_service(ctx, "phone")?;
    let clean_worker_id = bounded_nonempty(&workerId, 120, "worker id required")?;
    let id = ctx.sender().to_string();
    let row = ChatgptbrokerPhoneStatus {
        id: id.clone(),
        workerId: clean_worker_id,
        backendId: bounded(&backendId, 80),
        status: bounded(&status, 80),
        safeDetailsJson: bounded(&safeDetailsJson, 2048),
        lastSeenAt: ctx.timestamp,
    };
    if ctx.db.chatgptbroker_phone_status().id().find(&id).is_some() {
        ctx.db.chatgptbroker_phone_status().id().update(row);
    } else {
        ctx.db.chatgptbroker_phone_status().insert(row);
    }
    Ok(())
}

#[spacetimedb::reducer]
pub fn chatgptbroker_claim_next_job(
    ctx: &ReducerContext,
    workerId: String,
    backendId: String,
    attemptId: String,
) -> Result<(), String> {
    require_service(ctx, "phone")?;
    let clean_worker_id = bounded_nonempty(&workerId, 120, "worker id required")?;
    let clean_backend_id = bounded_nonempty(&backendId, 80, "backend id required")?;
    let clean_attempt_id = bounded_nonempty(&attemptId, 120, "attempt id required")?;
    if ctx
        .db
        .chatgptbroker_attempt()
        .id()
        .find(&clean_attempt_id)
        .is_some()
    {
        return Err("attempt already exists".into());
    }
    let mut selected: Option<ChatgptbrokerJob> = None;
    for job in ctx.db.chatgptbroker_job().iter() {
        if !claimable_status(&job.status) || !job.claimedBy.trim().is_empty() {
            continue;
        }
        if selected
            .as_ref()
            .map(|current| job.createdAt < current.createdAt)
            .unwrap_or(true)
        {
            selected = Some(job);
        }
    }
    let Some(job) = selected else {
        return Ok(());
    };
    let now = ctx.timestamp;
    ctx.db.chatgptbroker_attempt().insert(ChatgptbrokerAttempt {
        id: clean_attempt_id.clone(),
        jobId: job.id.clone(),
        status: "running".into(),
        workerId: clean_worker_id.clone(),
        backendId: clean_backend_id.clone(),
        failureCode: String::new(),
        createdAt: now,
        updatedAt: now,
    });
    set_job_status(
        ctx,
        &job.id,
        "running",
        "Running on Pixel",
        &clean_attempt_id,
        "",
        "",
        clean_worker_id,
        clean_backend_id,
        job.cancelRequested,
    )?;
    insert_event(
        ctx,
        &job.id,
        &clean_attempt_id,
        "public",
        "attempt_started",
        "Running on Pixel",
        "{}",
        now,
    );
    Ok(())
}

#[spacetimedb::reducer]
pub fn chatgptbroker_mark_waiting_phone(ctx: &ReducerContext, jobId: String) -> Result<(), String> {
    require_service(ctx, "broker")?;
    set_job_status(
        ctx,
        &jobId,
        "waiting_phone",
        "Waiting for phone",
        "",
        "",
        "",
        String::new(),
        String::new(),
        false,
    )
}

#[spacetimedb::reducer]
pub fn chatgptbroker_mark_screenshot_ready(
    ctx: &ReducerContext,
    jobId: String,
    attemptId: String,
    screenshotPngBase64: String,
) -> Result<(), String> {
    require_service(ctx, "phone")?;
    let clean_job_id = required(&jobId, "job id required")?;
    let clean_attempt_id = required(&attemptId, "attempt id required")?;
    let Some(job) = ctx.db.chatgptbroker_job().id().find(&clean_job_id) else {
        return Err("job not found".into());
    };
    if job.status != "running" && job.status != "cancel_requested" {
        return Err("job is not running".into());
    }
    if job.activeAttemptId != clean_attempt_id {
        return Err("attempt mismatch".into());
    }
    let now = ctx.timestamp;
    let ocr_id = format!("ocr:{}", clean_attempt_id);
    let row = ChatgptbrokerOcrInput {
        id: ocr_id.clone(),
        jobId: clean_job_id.clone(),
        attemptId: clean_attempt_id.clone(),
        workerId: job.claimedBy.clone(),
        status: "queued".into(),
        screenshotPngBase64: bounded(&screenshotPngBase64, 16_000_000),
        createdAt: now,
        updatedAt: now,
    };
    if ctx.db.chatgptbroker_ocr_input().id().find(&ocr_id).is_some() {
        ctx.db.chatgptbroker_ocr_input().id().update(row);
    } else {
        ctx.db.chatgptbroker_ocr_input().insert(row);
    }
    set_job_status(
        ctx,
        &clean_job_id,
        "ocr_pending",
        "Reading result",
        &clean_attempt_id,
        "",
        "",
        job.claimedBy,
        job.backendId,
        job.cancelRequested,
    )
}

#[spacetimedb::reducer]
pub fn chatgptbroker_mark_succeeded(
    ctx: &ReducerContext,
    jobId: String,
    attemptId: String,
    resultText: String,
) -> Result<(), String> {
    require_phone_or_broker(ctx)?;
    let clean_job_id = required(&jobId, "job id required")?;
    let clean_attempt_id = required(&attemptId, "attempt id required")?;
    update_attempt_status(ctx, &clean_attempt_id, "succeeded", "")?;
    if let Some(ocr) = ctx
        .db
        .chatgptbroker_ocr_input()
        .id()
        .find(&format!("ocr:{}", clean_attempt_id))
    {
        ctx.db
            .chatgptbroker_ocr_input()
            .id()
            .update(ChatgptbrokerOcrInput {
                status: "consumed".into(),
                screenshotPngBase64: String::new(),
                updatedAt: ctx.timestamp,
                ..ocr
            });
    }
    let Some(job) = ctx.db.chatgptbroker_job().id().find(&clean_job_id) else {
        return Err("job not found".into());
    };
    if let Some(secret) = ctx.db.chatgptbroker_job_secret().id().find(&clean_job_id) {
        ctx.db
            .chatgptbroker_job_secret()
            .id()
            .update(ChatgptbrokerJobSecret {
                resultText: bounded(&resultText, 120_000),
                status: "succeeded".into(),
                failureCode: String::new(),
                cancelRequested: false,
                updatedAt: ctx.timestamp,
                ..secret
            });
    }
    set_job_status(
        ctx,
        &clean_job_id,
        "succeeded",
        "Done",
        &clean_attempt_id,
        "spacetime_result",
        "",
        job.claimedBy,
        job.backendId,
        false,
    )
}

#[spacetimedb::reducer]
pub fn chatgptbroker_mark_failed(
    ctx: &ReducerContext,
    jobId: String,
    attemptId: String,
    failureCode: String,
    retryable: bool,
    publicStatus: String,
) -> Result<(), String> {
    require_phone_or_broker(ctx)?;
    let clean_job_id = required(&jobId, "job id required")?;
    let clean_attempt_id = required(&attemptId, "attempt id required")?;
    let status = if retryable {
        "failed_retryable"
    } else {
        "failed_final"
    };
    let clean_failure = bounded(&failureCode, 80);
    update_attempt_status(ctx, &clean_attempt_id, status, &clean_failure)?;
    let Some(job) = ctx.db.chatgptbroker_job().id().find(&clean_job_id) else {
        return Err("job not found".into());
    };
    if let Some(secret) = ctx.db.chatgptbroker_job_secret().id().find(&clean_job_id) {
        ctx.db
            .chatgptbroker_job_secret()
            .id()
            .update(ChatgptbrokerJobSecret {
                status: status.into(),
                failureCode: clean_failure.clone(),
                cancelRequested: false,
                updatedAt: ctx.timestamp,
                ..secret
            });
    }
    set_job_status(
        ctx,
        &clean_job_id,
        status,
        &non_empty(&publicStatus, "Failed"),
        &clean_attempt_id,
        "",
        &clean_failure,
        if retryable {
            String::new()
        } else {
            job.claimedBy
        },
        if retryable {
            String::new()
        } else {
            job.backendId
        },
        false,
    )
}

#[spacetimedb::reducer]
pub fn chatgptbroker_mark_preempted(
    ctx: &ReducerContext,
    jobId: String,
    attemptId: String,
    reason: String,
) -> Result<(), String> {
    require_service(ctx, "phone")?;
    let clean_job_id = required(&jobId, "job id required")?;
    let clean_attempt_id = required(&attemptId, "attempt id required")?;
    update_attempt_status(ctx, &clean_attempt_id, "preempted", &bounded(&reason, 80))?;
    set_job_status(
        ctx,
        &clean_job_id,
        "waiting_phone",
        "Waiting for phone",
        "",
        "",
        "preempted",
        String::new(),
        String::new(),
        false,
    )
}

#[spacetimedb::reducer]
pub fn chatgptbroker_mark_notified(ctx: &ReducerContext, jobId: String) -> Result<(), String> {
    require_service(ctx, "bot")?;
    let clean_job_id = required(&jobId, "job id required")?;
    let Some(secret) = ctx.db.chatgptbroker_job_secret().id().find(&clean_job_id) else {
        return Err("job not found".into());
    };
    ctx.db
        .chatgptbroker_job_secret()
        .id()
        .update(ChatgptbrokerJobSecret {
            notified: true,
            updatedAt: ctx.timestamp,
            ..secret
        });
    Ok(())
}

#[spacetimedb::reducer]
pub fn chatgptbroker_cleanup_expired(ctx: &ReducerContext) -> Result<(), String> {
    require_service(ctx, "admin")?;
    let now = ctx.timestamp;
    for job in ctx.db.chatgptbroker_job().iter() {
        if job.retentionDeleteAfter <= now {
            ctx.db.chatgptbroker_job().id().delete(&job.id);
        }
    }
    for secret in ctx.db.chatgptbroker_job_secret().iter() {
        if secret.retentionDeleteAfter <= now {
            ctx.db.chatgptbroker_job_secret().id().delete(&secret.id);
        }
    }
    for ocr in ctx.db.chatgptbroker_ocr_input().iter() {
        if ocr.updatedAt <= now && ocr.status == "consumed" {
            ctx.db.chatgptbroker_ocr_input().id().delete(&ocr.id);
        }
    }
    Ok(())
}

#[spacetimedb::view(accessor = chatgptbroker_phone_work, public, primary_key = id)]
pub fn chatgptbroker_phone_work(ctx: &ViewContext) -> Vec<ChatgptbrokerPhoneWorkRow> {
    if !view_has_role(ctx, "phone") {
        return Vec::new();
    }
    let worker_id = view_worker_id(ctx);
    let mut out = Vec::new();
    collect_phone_work_status(ctx, "queued", &mut out);
    collect_phone_work_status(ctx, "waiting_phone", &mut out);
    collect_phone_work_status(ctx, "failed_retryable", &mut out);
    collect_claimed_phone_work_status(ctx, &worker_id, "running", &mut out);
    collect_claimed_phone_work_status(ctx, &worker_id, "cancel_requested", &mut out);
    out
}

#[spacetimedb::view(accessor = chatgptbroker_ocr_work, public, primary_key = id)]
pub fn chatgptbroker_ocr_work(ctx: &ViewContext) -> Vec<ChatgptbrokerOcrWorkRow> {
    if !view_has_role(ctx, "broker") {
        return Vec::new();
    }
    let mut out = Vec::new();
    for ocr in ctx.db.chatgptbroker_ocr_input().status().filter("queued") {
        if ocr.screenshotPngBase64.trim().is_empty() {
            continue;
        }
        out.push(ChatgptbrokerOcrWorkRow {
            id: ocr.id,
            jobId: ocr.jobId,
            attemptId: ocr.attemptId,
            screenshotPngBase64: ocr.screenshotPngBase64,
            createdAt: ocr.createdAt,
            updatedAt: ocr.updatedAt,
        });
    }
    out
}

#[spacetimedb::view(accessor = chatgptbroker_bot_notifications, public, primary_key = id)]
pub fn chatgptbroker_bot_notifications(ctx: &ViewContext) -> Vec<ChatgptbrokerNotificationRow> {
    if !view_has_role(ctx, "bot") && !view_has_role(ctx, "broker") {
        return Vec::new();
    }
    let mut out = Vec::new();
    collect_notification_status(ctx, "succeeded", &mut out);
    collect_notification_status(ctx, "failed_final", &mut out);
    collect_notification_status(ctx, "cancelled", &mut out);
    out
}

fn collect_phone_work_status(
    ctx: &ViewContext,
    status: &str,
    out: &mut Vec<ChatgptbrokerPhoneWorkRow>,
) {
    for job in ctx.db.chatgptbroker_job().status().filter(status) {
        if !job.claimedBy.trim().is_empty() {
            continue;
        }
        push_phone_work_row(ctx, job, out);
    }
}

fn collect_claimed_phone_work_status(
    ctx: &ViewContext,
    worker_id: &str,
    status: &str,
    out: &mut Vec<ChatgptbrokerPhoneWorkRow>,
) {
    for job in ctx
        .db
        .chatgptbroker_job()
        .claimedStatus()
        .filter((worker_id, status))
    {
        push_phone_work_row(ctx, job, out);
    }
}

fn push_phone_work_row(
    ctx: &ViewContext,
    job: ChatgptbrokerJob,
    out: &mut Vec<ChatgptbrokerPhoneWorkRow>,
) {
    let Some(secret) = ctx.db.chatgptbroker_job_secret().id().find(&job.id) else {
        return;
    };
    out.push(ChatgptbrokerPhoneWorkRow {
        id: job.id,
        status: job.status,
        projectName: job.projectName,
        prompt: secret.prompt,
        activeAttemptId: job.activeAttemptId,
        claimedBy: job.claimedBy,
        cancelRequested: job.cancelRequested || secret.cancelRequested,
        createdAt: job.createdAt,
        updatedAt: job.updatedAt,
    });
}

fn collect_notification_status(
    ctx: &ViewContext,
    status: &str,
    out: &mut Vec<ChatgptbrokerNotificationRow>,
) {
    for secret in ctx
        .db
        .chatgptbroker_job_secret()
        .notifiedStatus()
        .filter((false, status))
    {
        let Some(job) = ctx.db.chatgptbroker_job().id().find(&secret.id) else {
            continue;
        };
        out.push(ChatgptbrokerNotificationRow {
            id: secret.id,
            telegramChatId: secret.telegramChatId,
            telegramUserId: secret.telegramUserId,
            status: secret.status,
            publicStatus: job.publicStatus,
            resultText: secret.resultText,
            failureCode: secret.failureCode,
            updatedAt: secret.updatedAt,
        });
    }
}

fn set_job_status(
    ctx: &ReducerContext,
    job_id: &str,
    status: &str,
    public_status: &str,
    active_attempt_id: &str,
    result_ref: &str,
    failure_code: &str,
    claimed_by: String,
    backend_id: String,
    cancel_requested: bool,
) -> Result<(), String> {
    let clean_job_id = required(job_id, "job id required")?;
    let Some(existing) = ctx.db.chatgptbroker_job().id().find(&clean_job_id) else {
        return Err("job not found".into());
    };
    let now = ctx.timestamp;
    ctx.db.chatgptbroker_job().id().update(ChatgptbrokerJob {
        status: status.into(),
        publicStatus: bounded(public_status, 160),
        activeAttemptId: active_attempt_id.trim().to_string(),
        claimedBy: claimed_by,
        backendId: backend_id,
        resultRef: non_empty(result_ref, &existing.resultRef),
        failureCode: non_empty(failure_code, &existing.failureCode),
        cancelRequested: cancel_requested,
        updatedAt: now,
        ..existing
    });
    if let Some(secret) = ctx.db.chatgptbroker_job_secret().id().find(&clean_job_id) {
        ctx.db
            .chatgptbroker_job_secret()
            .id()
            .update(ChatgptbrokerJobSecret {
                status: status.into(),
                failureCode: non_empty(failure_code, &secret.failureCode),
                cancelRequested: cancel_requested,
                updatedAt: now,
                ..secret
            });
    }
    insert_event(
        ctx,
        &clean_job_id,
        active_attempt_id,
        "public",
        status,
        public_status,
        "{}",
        now,
    );
    Ok(())
}

fn update_attempt_status(
    ctx: &ReducerContext,
    attempt_id: &str,
    status: &str,
    failure_code: &str,
) -> Result<(), String> {
    let clean_attempt_id = required(attempt_id, "attempt id required")?;
    let Some(existing) = ctx.db.chatgptbroker_attempt().id().find(&clean_attempt_id) else {
        return Err("attempt not found".into());
    };
    ctx.db
        .chatgptbroker_attempt()
        .id()
        .update(ChatgptbrokerAttempt {
            status: status.into(),
            failureCode: bounded(failure_code, 80),
            updatedAt: ctx.timestamp,
            ..existing
        });
    Ok(())
}

fn insert_event(
    ctx: &ReducerContext,
    job_id: &str,
    attempt_id: &str,
    visibility: &str,
    kind: &str,
    public_text: &str,
    safe_details_json: &str,
    now: Timestamp,
) {
    let id = format!("{}:{}:{}", job_id, kind, now.to_micros_since_unix_epoch());
    ctx.db.chatgptbroker_event().insert(ChatgptbrokerEvent {
        id,
        jobId: bounded(job_id, 120),
        attemptId: bounded(attempt_id, 120),
        visibility: bounded(visibility, 40),
        kind: bounded(kind, 80),
        publicText: bounded(public_text, 300),
        safeDetailsJson: bounded(safe_details_json, 2048),
        createdAt: now,
    });
}

fn require_phone_or_broker(ctx: &ReducerContext) -> Result<(), String> {
    if require_service(ctx, "phone").is_ok() || require_service(ctx, "broker").is_ok() {
        Ok(())
    } else {
        Err("phone or broker service role required".into())
    }
}

fn require_service(ctx: &ReducerContext, role: &str) -> Result<(), String> {
    if ctx.sender_auth().is_internal() {
        return Ok(());
    }
    if auth_has_role(ctx, &format!("chatgptbroker_{}", role))
        || auth_has_role(ctx, "chatgptbroker_admin")
    {
        return Ok(());
    }
    let id = ctx.sender().to_string();
    let Some(row) = ctx.db.chatgptbroker_service_identity().id().find(&id) else {
        return Err("service role required".into());
    };
    if !row.enabled {
        return Err("service disabled".into());
    }
    if row.role != role && row.role != "admin" {
        return Err("forbidden".into());
    }
    Ok(())
}

fn require_any_service(ctx: &ReducerContext) -> Result<(), String> {
    if ctx.sender_auth().is_internal()
        || auth_has_role(ctx, "chatgptbroker_bot")
        || auth_has_role(ctx, "chatgptbroker_broker")
        || auth_has_role(ctx, "chatgptbroker_phone")
        || auth_has_role(ctx, "chatgptbroker_admin")
    {
        Ok(())
    } else {
        Err("service role required".into())
    }
}

fn auth_has_role(ctx: &ReducerContext, expected: &str) -> bool {
    let Some(jwt) = ctx.sender_auth().jwt() else {
        return false;
    };
    let Ok(payload) = serde_json::from_str::<serde_json::Value>(jwt.raw_payload()) else {
        return false;
    };
    json_payload_has_role(&payload, expected)
}

fn view_has_role(ctx: &ViewContext, role: &str) -> bool {
    let id = ctx.sender().to_string();
    let Some(row) = ctx.db.chatgptbroker_service_identity().id().find(&id) else {
        return false;
    };
    row.enabled && (row.role == role || row.role == "admin")
}

fn view_worker_id(ctx: &ViewContext) -> String {
    let id = ctx.sender().to_string();
    ctx.db
        .chatgptbroker_service_identity()
        .id()
        .find(&id)
        .map(|row| row.serviceName)
        .unwrap_or(id)
}

fn json_payload_has_role(payload: &serde_json::Value, expected: &str) -> bool {
    match payload.get("roles") {
        Some(serde_json::Value::String(value)) => {
            value.split(',').any(|role| role.trim() == expected)
        }
        Some(serde_json::Value::Array(values)) => values.iter().any(|value| {
            value
                .as_str()
                .map(|role| role.trim() == expected)
                .unwrap_or(false)
        }),
        _ => false,
    }
}

fn claimable_status(status: &str) -> bool {
    matches!(status, "queued" | "waiting_phone" | "failed_retryable")
}

fn required(value: &str, message: &str) -> Result<String, String> {
    bounded_nonempty(value, 120, message)
}

fn bounded_nonempty(value: &str, max_len: usize, message: &str) -> Result<String, String> {
    let clean = value.trim();
    if clean.is_empty() {
        return Err(message.into());
    }
    Ok(clean.chars().take(max_len).collect())
}

fn non_empty(value: &str, fallback: &str) -> String {
    let clean = value.trim();
    if clean.is_empty() {
        fallback.to_string()
    } else {
        clean.to_string()
    }
}

fn bounded(value: &str, max_len: usize) -> String {
    value.trim().chars().take(max_len).collect()
}

fn retention_duration(retention_millis: u64) -> TimeDuration {
    let millis = if retention_millis == 0 || retention_millis > 30 * 24 * 60 * 60 * 1000 {
        24 * 60 * 60 * 1000
    } else {
        retention_millis
    };
    TimeDuration::from_micros((millis as i64).saturating_mul(1000))
}
