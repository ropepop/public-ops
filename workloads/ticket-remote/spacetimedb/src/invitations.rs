//! Invitations are capabilities; guest principals never become email members.
use super::*;

const GUEST_ROLE: &str = "ticketremote_guest_proxy";
const STREAM_SLICE_MS: i64 = 5_000;
const ACTION_ALLOWANCE: u8 = 5;

#[spacetimedb::table(accessor = ticketremote_invitation)]
#[derive(Clone, Debug)]
pub struct TicketremoteInvitation {
    #[primary_key] pub id: String,
    #[index(btree)] pub ticketId: String,
    #[unique] pub tokenHash: String,
    pub label: String,
    pub createdBy: String,
    pub createdAt: String,
    pub expiresAt: String,
    pub streamAllowanceMs: u32,
    pub streamUsedMs: u32,
    // Reserved and successful actions both occupy an allowance. Only proven
    // non-success releases it; ambiguous phone outcomes remain reserved.
    pub activationsUsed: u8,
    pub controlCodesUsed: u8,
    pub activationsReserved: u8,
    pub controlCodesReserved: u8,
    pub resultDeliveryUntilMs: i64,
    pub startedAt: String,
    pub activeSessionId: String,
    pub leaseSequence: u64,
    pub leaseStartedAtMs: i64,
    pub leaseUntilMs: i64,
    pub leaseCharged: bool,
    pub revokedAt: String,
    pub redeemedAt: String,
    pub redeemedEmail: String,
}

#[spacetimedb::table(accessor = ticketremote_guest_identity)]
#[derive(Clone)]
pub struct TicketremoteGuestIdentity {
    #[primary_key] pub id: String,
    #[index(btree)] pub identity: Identity,
    pub invitationId: String,
    pub sessionId: String,
}

#[spacetimedb::table(accessor = ticketremote_invitation_action,
    index(accessor = ticketCommand, btree(columns = [ticketId, commandId])))]
#[derive(Clone)]
pub struct TicketremoteInvitationAction {
    #[primary_key] pub id: String,
    pub ticketId: String,
    #[index(btree)] pub invitationId: String,
    pub commandId: String,
    pub kind: String,
    pub status: String,
    pub createdAt: String,
    pub completedAt: String,
}

#[spacetimedb::table(accessor = ticketremote_member_source)]
#[derive(Clone)]
pub struct TicketremoteMemberSource {
    #[primary_key] pub id: String,
    #[index(btree)] pub ticketId: String,
    pub email: String,
    pub source: String,
    pub invitationId: String,
    pub invitationLabel: String,
    pub invitedBy: String,
    pub registeredAt: String,
}

#[spacetimedb::view(accessor = ticketremote_service_invitation, public, primary_key = id)]
pub fn invitation_view(ctx: &ViewContext) -> Vec<TicketremoteInvitation> {
    let Some(ticket) = service_ticket_id_for_viewer(ctx) else { return vec![]; };
    ctx.db.ticketremote_invitation().ticketId().filter(&ticket).collect()
}

#[spacetimedb::view(accessor = ticketremote_service_member_source, public, primary_key = id)]
pub fn source_view(ctx: &ViewContext) -> Vec<TicketremoteMemberSource> {
    let Some(ticket) = service_ticket_id_for_viewer(ctx) else { return vec![]; };
    ctx.db.ticketremote_member_source().ticketId().filter(&ticket).collect()
}

fn valid_identifier(value: &str) -> bool {
    !value.is_empty() && value.len() <= 80 && value.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
}
fn valid_hash(value: &str) -> bool {
    value.len() == 64 && value.bytes().all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}
fn available(row: &TicketremoteInvitation) -> bool { row.revokedAt.is_empty() && row.redeemedAt.is_empty() }
fn trial_available(row: &TicketremoteInvitation, millis: i64) -> bool {
    let unelapsed = if row.leaseCharged { row.leaseUntilMs.saturating_sub(millis).max(0) } else { 0 };
    available(row) && millis < parse_time_ms(&row.expiresAt) && i64::from(row.streamUsedMs) - unelapsed < i64::from(row.streamAllowanceMs)
        && row.activationsUsed + row.activationsReserved < ACTION_ALLOWANCE && row.controlCodesUsed + row.controlCodesReserved < ACTION_ALLOWANCE
}
fn invitation(ctx: &ReducerContext, ticket: &str, id: &str) -> Result<TicketremoteInvitation, String> {
    ctx.db.ticketremote_invitation().id().find(id.to_string())
        .filter(|row| row.ticketId == clean_ticket_id(ticket)).ok_or_else(|| "invitation_invalid".into())
}
fn invitation_by_hash(ctx: &ReducerContext, ticket: &str, hash: &str) -> Result<TicketremoteInvitation, String> {
    if !valid_hash(hash) { return Err("invitation_invalid".into()); }
    ctx.db.ticketremote_invitation().tokenHash().find(hash.to_string())
        .filter(|row| row.ticketId == clean_ticket_id(ticket)).ok_or_else(|| "invitation_invalid".into())
}
fn release_unused(row: &mut TicketremoteInvitation, millis: i64) {
    let unused = row.leaseUntilMs.saturating_sub(millis.max(row.leaseStartedAtMs)).max(0) as u32;
    if row.leaseCharged { row.streamUsedMs = row.streamUsedMs.saturating_sub(unused); }
    row.leaseCharged = false;
    row.leaseUntilMs = millis;
}

#[spacetimedb::reducer]
pub fn ticketremote_create_invitation(ctx: &ReducerContext, ticketId: String, invitationId: String,
    tokenHash: String, label: String, actorEmail: String, durationMinutes: u32, streamMinutes: u32) -> Result<(), String> {
    require_service(ctx)?;
    let ticket = clean_ticket_id(&ticketId);
    require_admin(ctx, &ticket, &actorEmail)?;
    if !valid_identifier(&invitationId) || !valid_hash(&tokenHash) || label.chars().count() > 120
        || !(1..=525_600).contains(&durationMinutes) || !matches!(streamMinutes, 5 | 15 | 30) {
        return Err("invitation_invalid_settings".into());
    }
    if let Some(existing) = ctx.db.ticketremote_invitation().id().find(&invitationId) {
        return if existing.ticketId == ticket && existing.tokenHash == tokenHash
            && existing.createdBy == clean_email(&actorEmail) && existing.label == label.trim()
            && existing.streamAllowanceMs == streamMinutes * 60_000
            && existing.expiresAt == add_ms(&existing.createdAt, i64::from(durationMinutes) * 60_000) { Ok(()) } else { Err("invitation_id_reused".into()) };
    }
    if ctx.db.ticketremote_invitation().tokenHash().find(&tokenHash).is_some() { return Err("invitation_token_reused".into()); }
    let clock = now(ctx);
    ctx.db.ticketremote_invitation().insert(TicketremoteInvitation {
        id: invitationId, ticketId: ticket, tokenHash, label: label.trim().into(), createdBy: clean_email(&actorEmail),
        createdAt: clock.clone(), expiresAt: add_ms(&clock, i64::from(durationMinutes) * 60_000),
        streamAllowanceMs: streamMinutes * 60_000, streamUsedMs: 0, activationsUsed: 0, controlCodesUsed: 0, activationsReserved: 0, controlCodesReserved: 0, resultDeliveryUntilMs: 0,
        startedAt: String::new(), activeSessionId: String::new(), leaseSequence: 0,
        leaseStartedAtMs: 0, leaseUntilMs: 0, leaseCharged: false, revokedAt: String::new(), redeemedAt: String::new(), redeemedEmail: String::new(),
    });
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_revoke_invitation(ctx: &ReducerContext, ticketId: String, invitationId: String, actorEmail: String) -> Result<(), String> {
    require_service(ctx)?;
    require_admin(ctx, &ticketId, &actorEmail)?;
    let mut row = invitation(ctx, &ticketId, &invitationId)?;
    if row.revokedAt.is_empty() { row.revokedAt = now(ctx); }
    release_unused(&mut row, ctx.timestamp.to_micros_since_unix_epoch() / 1_000);
    ctx.db.ticketremote_invitation().id().update(row);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_start_invitation(ctx: &ReducerContext, ticketId: String, tokenHash: String, sessionId: String, takeover: bool) -> Result<(), String> {
    require_service(ctx)?;
    if !valid_identifier(&sessionId) { return Err("invitation_invalid_session".into()); }
    let mut row = invitation_by_hash(ctx, &ticketId, &tokenHash)?;
    let millis = ctx.timestamp.to_micros_since_unix_epoch() / 1_000;
    if !available(&row) { return Err("invitation_invalid".into()); }
    if row.activeSessionId != sessionId {
        if !row.activeSessionId.is_empty() && !takeover { return Err("invitation_continue_here_required".into()); }
        release_unused(&mut row, millis);
        row.activeSessionId = sessionId;
    }
    if !trial_available(&row, millis) { return Err("invitation_registration_required".into()); }
    if row.startedAt.is_empty() { row.startedAt = now(ctx); }
    ctx.db.ticketremote_invitation().id().update(row);
    Ok(())
}

fn reserve_stream(row: &mut TicketremoteInvitation, session: &str, sequence: u64, millis: i64, result_only: bool) -> Result<(), String> {
    if !available(row) || row.activeSessionId != session { return Err("invitation_session_changed".into()); }
    if sequence == row.leaseSequence && sequence != 0 { return Ok(()); }
    if sequence <= row.leaseSequence { return Err("invitation_stream_sequence_stale".into()); }
    if row.leaseUntilMs > millis { return Err("invitation_stream_lease_active".into()); }
    if result_only {
        if row.resultDeliveryUntilMs <= millis { return Err("invitation_result_expired".into()); }
        row.leaseSequence = sequence; row.leaseStartedAtMs = millis;
        row.leaseUntilMs = (millis + STREAM_SLICE_MS).min(row.resultDeliveryUntilMs); row.leaseCharged = false;
        return Ok(());
    }
    if !trial_available(row, millis) { return Err("invitation_registration_required".into()); }
    let millis_reserved = STREAM_SLICE_MS.min(i64::from(row.streamAllowanceMs - row.streamUsedMs))
        .min(parse_time_ms(&row.expiresAt) - millis).max(0);
    row.streamUsedMs += millis_reserved as u32;
    row.leaseCharged = true;
    row.leaseSequence = sequence;
    row.leaseStartedAtMs = millis;
    row.leaseUntilMs = millis + millis_reserved;
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_reserve_invitation_stream(ctx: &ReducerContext, ticketId: String, invitationId: String, sessionId: String, sequence: u64, resultOnly: bool) -> Result<(), String> {
    require_service(ctx)?;
    let mut row = invitation(ctx, &ticketId, &invitationId)?;
    reserve_stream(&mut row, &sessionId, sequence, ctx.timestamp.to_micros_since_unix_epoch() / 1_000, resultOnly)?;
    ctx.db.ticketremote_invitation().id().update(row);
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_release_invitation_stream(ctx: &ReducerContext, ticketId: String, invitationId: String, sessionId: String, sequence: u64) -> Result<(), String> {
    require_service(ctx)?;
    let mut row = invitation(ctx, &ticketId, &invitationId)?;
    if row.activeSessionId == sessionId && row.leaseSequence == sequence {
        release_unused(&mut row, ctx.timestamp.to_micros_since_unix_epoch() / 1_000);
        ctx.db.ticketremote_invitation().id().update(row);
    }
    Ok(())
}

#[spacetimedb::reducer]
pub fn ticketremote_redeem_invitation(ctx: &ReducerContext, ticketId: String, tokenHash: String, email: String) -> Result<(), String> {
    // Only the web auth callback, after verifying the provider's email, calls this
    // service reducer. No unverified browser email may cross that boundary.
    require_service(ctx)?;
    let email = clean_email(&email);
    if email.is_empty() || !email.contains('@') || email.starts_with("guest:") { return Err("verified_email_required".into()); }
    let mut row = invitation_by_hash(ctx, &ticketId, &tokenHash)?;
    let id = member_id(&ticketId, &email);
    if let Some(member) = ctx.db.ticketremote_ticket_member().id().find(&id) {
        return if member.active { Ok(()) } else { Err("invitation_member_removed".into()) };
    }
    if !available(&row) { return Err("invitation_invalid".into()); }
    let clock = now(ctx);
    upsert_member_row(ctx, &ticketId, &email, "member", &clock);
    ctx.db.ticketremote_member_source().insert(TicketremoteMemberSource {
        id, ticketId: clean_ticket_id(&ticketId), email: email.clone(), source: "invite".into(),
        invitationId: row.id.clone(), invitationLabel: row.label.clone(), invitedBy: row.createdBy.clone(), registeredAt: clock.clone(),
    });
    row.redeemedAt = clock;
    row.redeemedEmail = email;
    release_unused(&mut row, ctx.timestamp.to_micros_since_unix_epoch() / 1_000);
    row.activeSessionId.clear();
    ctx.db.ticketremote_invitation().id().update(row);
    Ok(())
}

pub(super) fn record_manual_source(ctx: &ReducerContext, ticket: &str, email: &str, actor: &str, clock: &str) {
    let id = member_id(ticket, email);
    if ctx.db.ticketremote_ticket_member().id().find(&id).is_some() { return; }
    ctx.db.ticketremote_member_source().insert(TicketremoteMemberSource {
        id, ticketId: clean_ticket_id(ticket), email: clean_email(email), source: "manual".into(),
        invitationId: String::new(), invitationLabel: String::new(), invitedBy: clean_email(actor), registeredAt: clock.into(),
    });
}

fn guest_claims(payload: &serde_json::Value) -> Option<(&str, &str)> {
    let id = payload.get("invitation_id")?.as_str()?;
    let session = payload.get("trial_session_id")?.as_str()?;
    (valid_identifier(id) && valid_identifier(session)
        && payload.get("iss").and_then(|v| v.as_str()) == Some(SERVICE_OIDC_ISSUER)
        && jwt_audience_includes(payload, SERVICE_OIDC_AUDIENCE)
        && payload.get("sub").and_then(|v| v.as_str()) == Some(format!("guest:{id}:{session}").as_str())
        && jwt_roles_include(payload, GUEST_ROLE)
        && !jwt_roles_include(payload, SERVICE_ROLE)
        && !jwt_roles_include(payload, MEMBER_PROXY_ROLE)).then_some((id, session))
}

pub(super) fn guest_actor(ctx: &ReducerContext, ticket: &str, require_trial: bool) -> Result<String, String> {
    let payload = jwt_payload(ctx)?;
    let (id, session) = guest_claims(&payload).ok_or("invitation_guest_auth_required")?;
    let row = invitation(ctx, ticket, id)?;
    if !available(&row) || row.activeSessionId != session { return Err("invitation_session_changed".into()); }
    if require_trial && !trial_available(&row, ctx.timestamp.to_micros_since_unix_epoch() / 1_000) {
        return Err("invitation_registration_required".into());
    }
    Ok(format!("guest:{id}"))
}

pub(super) fn viewer_actor(ctx: &ReducerContext, ticket: &str) -> Result<String, String> {
    if jwt_payload(ctx).ok().as_ref().and_then(guest_claims).is_some() { guest_actor(ctx, ticket, true) }
    else { client_email_from_auth(ctx, ticket) }
}

pub(super) fn result_actor(ctx: &ReducerContext, ticket: &str) -> Result<String, String> {
    if jwt_payload(ctx).ok().as_ref().and_then(guest_claims).is_some() { guest_actor(ctx, ticket, false) }
    else { client_email_from_auth(ctx, ticket) }
}

pub(super) fn connect_guest(ctx: &ReducerContext) -> Result<bool, String> {
    let payload = jwt_payload(ctx)?;
    let Some((id, session)) = guest_claims(&payload) else { return Ok(false); };
    guest_actor(ctx, DEFAULT_TICKET_ID, false)?;
    upsert_row!(ctx, ticketremote_guest_identity, TicketremoteGuestIdentity {
        id: ctx.sender().to_string(), identity: ctx.sender(), invitationId: id.into(), sessionId: session.into(),
    });
    Ok(true)
}

pub(super) fn guest_view_binding(ctx: &ViewContext) -> Option<TicketremoteMemberIdentity> {
    let binding = ctx.db.ticketremote_guest_identity().identity().filter(&ctx.sender()).next()?;
    let row = ctx.db.ticketremote_invitation().id().find(&binding.invitationId)?;
    if !available(&row) || row.activeSessionId != binding.sessionId { return None; }
    Some(TicketremoteMemberIdentity { id: binding.id, identity: binding.identity, ticketId: row.ticketId,
        email: format!("guest:{}", row.id), updatedAt: row.startedAt })
}

pub(super) fn is_guest_actor(ctx: &ReducerContext, ticket: &str, actor: &str) -> bool {
    actor.strip_prefix("guest:").and_then(|id| invitation(ctx, ticket, id).ok()).is_some_and(|row| available(&row))
}

pub(super) fn reserve_action(ctx: &ReducerContext, ticket: &str, backend: &str, command: &str, actor: &str, operation: &str) -> Result<(), String> {
    let Some(invitation_id) = actor.strip_prefix("guest:") else { return Ok(()); };
    // New effects are always checked against the current guest session and all
    // quotas, even when a malicious client invokes the reducer directly.
    guest_actor(ctx, ticket, true)?;
    if !matches!(operation, "control_code" | "register_current" | "open_latest_and_register" | "open_latest_unactivated"
        | "show_recent_activated" | "return_to_latest_unactivated" | "redetect_latest") { return Err("invitation_operation_forbidden".into()); }
    let kind = match operation { "control_code" => "control_code", "register_current" | "open_latest_and_register" => "activation", _ => "view" };
    let id = ticket_action_v3_row_id(ticket, backend, command);
    if ctx.db.ticketremote_invitation_action().id().find(&id).is_some() { return Ok(()); }
    let mut row = invitation(ctx, ticket, invitation_id)?;
    if kind == "activation" { row.activationsReserved += 1; } else if kind == "control_code" { row.controlCodesReserved += 1; }
    row.resultDeliveryUntilMs = row.resultDeliveryUntilMs.max(ctx.timestamp.to_micros_since_unix_epoch() / 1_000
        + if kind == "control_code" { CONTROL_CODE_PHONE_TTL_MS } else { TICKET_ACTIVATION_COMMAND_TTL_MS } + 60_000);
    ctx.db.ticketremote_invitation().id().update(row);
    ctx.db.ticketremote_invitation_action().insert(TicketremoteInvitationAction {
        id, ticketId: clean_ticket_id(ticket), invitationId: invitation_id.into(), commandId: command.into(),
        kind: kind.into(), status: "reserved".into(), createdAt: now(ctx), completedAt: String::new(),
    });
    Ok(())
}

pub(super) fn settle_action(ctx: &ReducerContext, ticket: &str, command: &str, succeeded: bool, proven_failure: bool) {
    if !succeeded && !proven_failure { return; }
    let reservations: Vec<_> = ctx.db.ticketremote_invitation_action().ticketCommand().filter((ticket, command)).collect();
    for mut reservation in reservations {
        if reservation.status != "reserved" { continue; }
        if let Some(mut row) = ctx.db.ticketremote_invitation().id().find(&reservation.invitationId) {
            if reservation.kind == "activation" {
                row.activationsReserved = row.activationsReserved.saturating_sub(1);
                if succeeded { row.activationsUsed += 1; }
            } else if reservation.kind == "control_code" {
                row.controlCodesReserved = row.controlCodesReserved.saturating_sub(1);
                if succeeded { row.controlCodesUsed += 1; }
            }
            if !ctx.db.ticketremote_invitation_action().invitationId().filter(&row.id)
                .any(|other| other.id != reservation.id && other.status == "reserved") {
                row.resultDeliveryUntilMs = ctx.timestamp.to_micros_since_unix_epoch() / 1_000 + 60_000;
            }
            ctx.db.ticketremote_invitation().id().update(row);
        }
        reservation.status = if succeeded { "succeeded" } else { "refunded" }.into();
        reservation.completedAt = now(ctx);
        ctx.db.ticketremote_invitation_action().id().update(reservation);
    }
}

pub(super) fn conclusive_activation_failure(_status: &str, phase: &str, _reason: &str) -> bool {
    // These typed phone outcomes prove no activation. A generic "failed" can
    // also mean a lost result or journal failure and therefore cannot refund.
    matches!(phase, "not_dispatched" | "no_transition" | "retry_not_dispatched")
}
pub(super) fn conclusive_code_failure(status: &str, reason: &str) -> bool {
    // The installed phone emits these before dispatching this request's input.
    // Cleanup/visual failures after dispatch retain their reservation.
    status == "failed" && matches!(reason, "missing_request_id" | "invalid_code"
        | "phone_control_context_changed" | "control_code_cleanup_pending"
        | "control_code_keyboard_cleanup_pending" | "control_code_panel_dark_unavailable"
        | "control_code_keyboard_clamp_unavailable")
}

pub(super) fn active_guest_for_view(ctx: &ViewContext, ticket: &str, actor: &str) -> bool {
    actor.strip_prefix("guest:").and_then(|id| ctx.db.ticketremote_invitation().id().find(id.to_string()))
        .is_some_and(|row| row.ticketId == ticket && available(&row))
}

#[cfg(test)]
mod tests {
    use super::*;
    fn row() -> TicketremoteInvitation {
        TicketremoteInvitation { id: "invite-test".into(), ticketId: "vivi-default".into(), tokenHash: "a".repeat(64),
            label: String::new(), createdBy: "admin@example.test".into(), createdAt: "2026-09-22T00:00:00Z".into(),
            expiresAt: "2026-09-25T00:00:00Z".into(), streamAllowanceMs: 900_000, streamUsedMs: 0,
            activationsUsed: 0, controlCodesUsed: 0, activationsReserved: 0, controlCodesReserved: 0, resultDeliveryUntilMs: 0, startedAt: String::new(), activeSessionId: "browser-one".into(),
            leaseSequence: 0, leaseStartedAtMs: 0, leaseUntilMs: 0, leaseCharged: false, revokedAt: String::new(), redeemedAt: String::new(), redeemedEmail: String::new() }
    }
    #[test]
    fn stream_reservations_are_bounded_idempotent_and_fenced() {
        let mut row = row(); let clock = parse_time_ms(&row.createdAt);
        reserve_stream(&mut row, "browser-one", 1, clock, false).unwrap();
        assert_eq!(row.streamUsedMs, 5_000);
        reserve_stream(&mut row, "browser-one", 1, clock + 2000, false).unwrap();
        assert_eq!(row.streamUsedMs, 5_000);
        assert!(reserve_stream(&mut row, "browser-two", 2, clock + 5000, false).is_err());
        assert!(reserve_stream(&mut row, "browser-one", 2, clock + 2000, false).is_err());
        release_unused(&mut row, clock + 2_000); assert_eq!(row.streamUsedMs, 2_000);
        release_unused(&mut row, clock + 2_500); assert_eq!(row.streamUsedMs, 2_000);
        reserve_stream(&mut row, "browser-one", 2, clock + 3_000, false).unwrap(); assert_eq!(row.streamUsedMs, 7_000);
        assert!(reserve_stream(&mut row, "browser-one", 1, clock + 9_000, false).is_err());
    }
    #[test]
    fn expiry_and_any_allowance_end_trial_but_preserve_registration() {
        let mut row = row(); let expiry = parse_time_ms(&row.expiresAt);
        assert!(trial_available(&row, expiry - 1)); assert!(!trial_available(&row, expiry)); assert!(available(&row));
        for kind in 0..3 { let mut exhausted = row.clone();
            match kind { 0 => exhausted.streamUsedMs = exhausted.streamAllowanceMs, 1 => exhausted.activationsUsed = 5, _ => exhausted.controlCodesUsed = 5 };
            assert!(!trial_available(&exhausted, expiry - 1)); assert!(available(&exhausted));
        }
        reserve_stream(&mut row, "browser-one", 1, expiry - 2_000, false).unwrap(); assert_eq!(row.streamUsedMs, 2_000);
        row.revokedAt = row.createdAt.clone(); assert!(!available(&row));
        row.revokedAt.clear(); row.redeemedAt = row.createdAt.clone(); assert!(!available(&row));
    }
    #[test]
    fn guest_auth_never_accepts_member_service_or_unpinned_claims() {
        let valid = serde_json::json!({"iss":SERVICE_OIDC_ISSUER,"aud":SERVICE_OIDC_AUDIENCE,"sub":"guest:invite-test:browser-one",
            "roles":[GUEST_ROLE],"invitation_id":"invite-test","trial_session_id":"browser-one"});
        assert!(guest_claims(&valid).is_some()); assert!(!service_claims_are_valid(&valid));
        for (key, value) in [("iss", serde_json::json!("https://other.invalid")), ("aud", serde_json::json!("other")),
            ("sub", serde_json::json!("guest:other")), ("roles", serde_json::json!([GUEST_ROLE,SERVICE_ROLE])),
            ("roles", serde_json::json!([MEMBER_PROXY_ROLE])), ("trial_session_id", serde_json::json!(""))] {
            let mut invalid = valid.clone(); invalid[key] = value; assert!(guest_claims(&invalid).is_none());
        }
        assert!(!valid_hash(&"A".repeat(64))); assert!(!valid_identifier("guest:abc"));
    }
    #[test]
    fn final_result_delivery_is_bounded_without_refunding_earlier_viewing() {
        let mut row = row(); let clock = parse_time_ms(&row.createdAt);
        row.streamUsedMs = row.streamAllowanceMs; row.resultDeliveryUntilMs = clock + 60_000;
        reserve_stream(&mut row, "browser-one", 1, clock, true).unwrap();
        release_unused(&mut row, clock + 1_000);
        assert_eq!(row.streamUsedMs, row.streamAllowanceMs);
        assert!(reserve_stream(&mut row, "browser-one", 2, clock + 60_000, true).is_err());
        assert!(reserve_stream(&mut row, "browser-one", 2, clock + 1_000, false).is_err());
    }
    #[test]
    fn ambiguous_results_never_refund_action_allowances() {
        for reason in ["outcome_unknown", "timeout", "phone_unavailable", "startup_reconcile", "interrupted"] {
            assert!(!conclusive_activation_failure("failed", "failed", reason));
            assert!(!conclusive_code_failure("failed", reason));
        }
        assert!(conclusive_activation_failure("needs_attention", "no_transition", "no_transition"));
        assert!(conclusive_activation_failure("failed", "not_dispatched", "expired"));
        assert!(conclusive_code_failure("failed", "invalid_code"));
        assert!(!conclusive_code_failure("expired", "expired"));
        assert!(!conclusive_code_failure("closed", "browser_closed"));
    }

    #[test]
    fn last_prepaid_slice_remains_available_only_until_server_deadline() {
        let mut row = row(); let clock = parse_time_ms(&row.createdAt);
        row.streamUsedMs = row.streamAllowanceMs - 5_000;
        reserve_stream(&mut row, "browser-one", 1, clock, false).unwrap();
        assert!(trial_available(&row, clock + 4_999));
        assert!(!trial_available(&row, clock + 5_000));
        assert!(reserve_stream(&mut row, "browser-one", 2, clock + 5_000, false).is_err());
    }

}
