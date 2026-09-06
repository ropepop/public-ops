//! Current phone observation is control state, not evidence that video was delivered.
//! The single row survives observation expiry so old sessions cannot reclaim ownership.
use super::*;

const PHONE_OBSERVATION_TTL_MS: i64 = 3_000;

#[spacetimedb::table(accessor = ticketremote_phone_control_state, public,
    index(accessor = ticketBackend, btree(columns = [ticketId, backendId]))
)]
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct TicketremotePhoneControlState {
    #[primary_key]
    pub id: String,
    pub ticketId: String,
    pub backendId: String,
    pub sessionId: String,
    pub sessionGeneration: u64,
    pub contextRevision: String,
    pub observationSequence: u64,
    pub view: String,
    pub ready: bool,
    pub busy: bool,
    pub reason: String,
    pub leftBasisPoints: u32,
    pub topBasisPoints: u32,
    pub rightBasisPoints: u32,
    pub bottomBasisPoints: u32,
    pub observedAt: String,
    pub expiresAt: String,
    pub updatedAt: String,
    /// A server timestamp used for a conservative, phone-monotonic clock anchor.
    /// Refreshing this field never refreshes an observation or its lease.
    pub clockAt: String,
}

fn control_identifier(value: &str) -> bool {
    !value.is_empty() && value.len() <= 160 && value.bytes().all(|c| {
        c.is_ascii_alphanumeric() || matches!(c, b'-' | b'_' | b':')
    })
}

fn begin_session(
    current: Option<TicketremotePhoneControlState>,
    ticket: &str,
    backend: &str,
    session: &str,
    expected_previous: &str,
    clock: &str,
) -> Result<TicketremotePhoneControlState, String> {
    if !control_identifier(session) {
        return Err("invalid_phone_control_session".into());
    }
    if let Some(mut row) = current {
        if row.sessionId == session {
            row.clockAt = clock.into();
            return Ok(row);
        }
        if row.sessionId != expected_previous {
            return Err("phone_control_session_changed".into());
        }
        row.sessionId = session.into();
        row.sessionGeneration = row.sessionGeneration.checked_add(1)
            .ok_or("phone_control_session_exhausted")?;
        row.contextRevision.clear();
        row.observationSequence = 0;
        row.view = "unknown".into();
        row.ready = false;
        row.busy = false;
        row.reason = "phone_session_started".into();
        row.leftBasisPoints = 0;
        row.topBasisPoints = 0;
        row.rightBasisPoints = 0;
        row.bottomBasisPoints = 0;
        row.observedAt.clear();
        row.expiresAt = clock.into();
        row.updatedAt = clock.into();
        row.clockAt = clock.into();
        return Ok(row);
    }
    if !expected_previous.is_empty() {
        return Err("phone_control_session_changed".into());
    }
    Ok(TicketremotePhoneControlState {
        id: phone_row_id(ticket, backend), ticketId: ticket.into(), backendId: backend.into(),
        sessionId: session.into(), sessionGeneration: 1, contextRevision: String::new(),
        observationSequence: 0, view: "unknown".into(), ready: false, busy: false,
        reason: "phone_session_started".into(), leftBasisPoints: 0, topBasisPoints: 0,
        rightBasisPoints: 0, bottomBasisPoints: 0, observedAt: String::new(),
        expiresAt: clock.into(), updatedAt: clock.into(), clockAt: clock.into(),
    })
}

/// Compare-and-set session ownership. A delayed former publisher cannot replace
/// a newer session; reconnecting the same session only obtains a new clock anchor.
#[spacetimedb::reducer]
pub fn ticketremote_begin_phone_control_session(
    ctx: &ReducerContext, ticketId: String, backendId: String,
    sessionId: String, expectedPreviousSessionId: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let ticket = clean_ticket_id(&ticketId);
    let backend = clean_backend_id(&backendId);
    let table = ctx.db.ticketremote_phone_control_state();
    let previous = table.id().find(phone_row_id(&ticket, &backend));
    let row = begin_session(previous.clone(), &ticket, &backend, &sessionId,
        &expectedPreviousSessionId, &now(ctx))?;
    if previous.is_some() { table.id().update(row); } else { table.insert(row); }
    Ok(())
}

fn validate_observation(
    current: &TicketremotePhoneControlState,
    candidate: &TicketremotePhoneControlState,
    clock: &str,
) -> Result<(), String> {
    if candidate.sessionId != current.sessionId {
        return Err("phone_control_session_changed".into());
    }
    if candidate.observationSequence <= current.observationSequence {
        return Err("phone_control_observation_superseded".into());
    }
    if !control_identifier(&candidate.contextRevision) ||
        !candidate.contextRevision.starts_with(&format!("{}:", candidate.sessionId)) {
        return Err("invalid_phone_control_context".into());
    }
    if !matches!(candidate.view.as_str(), "unactivated_detail" | "activated_detail" |
        "ticket_list" | "login_required" | "blocked" | "unknown") {
        return Err("invalid_phone_control_view".into());
    }
    if candidate.reason.len() > 120 || !candidate.reason.bytes().all(|c| {
        c.is_ascii_lowercase() || c.is_ascii_digit() || c == b'_'
    }) { return Err("invalid_phone_control_reason".into()); }
    if candidate.ready {
        let observed = parse_time_ms(&candidate.observedAt);
        let previous = parse_time_ms(&current.observedAt);
        if observed <= 0 || observed <= previous || observed > parse_time_ms(clock) ||
            parse_time_ms(clock).saturating_sub(observed) >= PHONE_OBSERVATION_TTL_MS {
            return Err("phone_control_observation_expired".into());
        }
        if candidate.busy || !matches!(candidate.view.as_str(), "unactivated_detail" | "activated_detail") {
            return Err("phone_control_not_ready".into());
        }
        if candidate.view == "unactivated_detail" && !ticket_slider_region_v3_bounds_valid(
            candidate.leftBasisPoints, candidate.topBasisPoints,
            candidate.rightBasisPoints, candidate.bottomBasisPoints,
        ) { return Err("invalid_phone_control_geometry".into()); }
    }
    Ok(())
}

/// Only a newly captured observation may renew readiness. The producer supplies
/// a conservative database-time lower bound derived from the session handshake;
/// source wall-clock skew and retried publication cannot grant extra lifetime.
#[spacetimedb::reducer]
pub fn ticketremote_publish_phone_control_state(
    ctx: &ReducerContext, ticketId: String, backendId: String, sessionId: String,
    contextRevision: String, observationSequence: u64, view: String,
    ready: bool, busy: bool, reason: String,
    leftBasisPoints: u32, topBasisPoints: u32, rightBasisPoints: u32, bottomBasisPoints: u32,
    observedAt: String,
) -> Result<(), String> {
    require_service(ctx)?;
    let ticket = clean_ticket_id(&ticketId);
    let backend = clean_backend_id(&backendId);
    let table = ctx.db.ticketremote_phone_control_state();
    let previous = table.id().find(phone_row_id(&ticket, &backend))
        .ok_or("phone_control_session_required")?;
    let clock = now(ctx);
    let mut candidate = TicketremotePhoneControlState {
        sessionId, contextRevision, observationSequence, view, ready, busy, reason,
        leftBasisPoints, topBasisPoints, rightBasisPoints, bottomBasisPoints,
        observedAt, ..previous.clone()
    };
    // Exact duplicate delivery is acknowledgement-only, including its old expiry.
    if candidate == previous { return Ok(()); }
    validate_observation(&previous, &candidate, &clock)?;
    candidate.busy |= ticket_phone_mutation_lane_conflict(ctx, &ticket, &backend, &clock).is_some();
    candidate.ready &= !candidate.busy;
    candidate.expiresAt = if candidate.ready {
        add_ms(&candidate.observedAt, PHONE_OBSERVATION_TTL_MS)
    } else { clock.clone() };
    candidate.updatedAt = clock;
    table.id().update(candidate);
    Ok(())
}

pub(crate) fn phone_control_registration_ready(
    row: &TicketremotePhoneControlState, expected_context: &str, clock: &str,
) -> bool {
    phone_control_ready(row, expected_context, clock) && row.view == "unactivated_detail" &&
        ticket_slider_region_v3_bounds_valid(row.leftBasisPoints, row.topBasisPoints,
            row.rightBasisPoints, row.bottomBasisPoints)
}

pub(crate) fn phone_control_ready(
    row: &TicketremotePhoneControlState, expected_context: &str, clock: &str,
) -> bool {
    row.ready && !row.busy && matches!(row.view.as_str(), "unactivated_detail" | "activated_detail") &&
        row.contextRevision == expected_context &&
        parse_time_ms(&row.observedAt) > 0 &&
        parse_time_ms(&row.observedAt) <= parse_time_ms(clock) &&
        parse_time_ms(&row.expiresAt) > parse_time_ms(clock)
}

#[cfg(test)]
mod tests {
    use super::*;
    const START: &str = "2026-09-06T12:00:00Z";
    fn initial() -> TicketremotePhoneControlState {
        begin_session(None, "ticket", "pixel", "session-a", "", START).unwrap()
    }
    fn observation() -> TicketremotePhoneControlState {
        TicketremotePhoneControlState {
            contextRevision: "session-a:1".into(), observationSequence: 1,
            view: "unactivated_detail".into(), ready: true,
            observedAt: add_ms(START, 100), expiresAt: add_ms(START, 3_100),
            leftBasisPoints: 100, topBasisPoints: 7000, rightBasisPoints: 9000,
            bottomBasisPoints: 8500, ..initial()
        }
    }
    #[test]
    fn context_authority_does_not_need_video_or_hdr() {
        let state = observation();
        assert!(phone_control_registration_ready(&state, "session-a:1", &add_ms(START, 200)));
        assert!(!phone_control_registration_ready(&state, "session-a:2", &add_ms(START, 200)));
        assert!(!phone_control_registration_ready(&state, "session-a:1", &add_ms(START, 3_100)));
    }
    #[test]
    fn old_or_changed_delivery_cannot_renew_readiness() {
        let previous = observation();
        let mut replay = previous.clone();
        replay.observationSequence += 1;
        assert!(validate_observation(&previous, &replay, &add_ms(START, 200)).is_err());
        replay.observedAt = add_ms(START, 150);
        assert!(validate_observation(&previous, &replay, &add_ms(START, 200)).is_ok());
        replay.observationSequence = previous.observationSequence;
        assert!(validate_observation(&previous, &replay, &add_ms(START, 200)).is_err());
    }
    #[test]
    fn clock_refresh_cannot_refresh_observation() {
        let previous = observation();
        let refreshed = begin_session(Some(previous.clone()), "ticket", "pixel",
            "session-a", "", &add_ms(START, 10_000)).unwrap();
        assert_eq!(refreshed.observedAt, previous.observedAt);
        assert_eq!(refreshed.expiresAt, previous.expiresAt);
        assert!(!phone_control_registration_ready(&refreshed, "session-a:1", &add_ms(START, 10_000)));
    }
    #[test]
    fn replacement_fences_old_publishers_and_late_session_starts() {
        let replacement = begin_session(Some(observation()), "ticket", "pixel",
            "session-b", "session-a", &add_ms(START, 200)).unwrap();
        assert!(!replacement.ready);
        assert_eq!(replacement.observationSequence, 0);
        assert!(validate_observation(&replacement, &observation(), &add_ms(START, 200)).is_err());
        assert!(begin_session(Some(replacement), "ticket", "pixel", "session-c",
            "session-a", &add_ms(START, 300)).is_err());
    }
    #[test]
    fn expired_future_unknown_and_invalid_geometry_fail_closed() {
        let original = observation();
        for variant in 0..5 {
            let mut state = original.clone();
            match variant {
                0 => state.observedAt = add_ms(START, -4_000),
                1 => state.observedAt = add_ms(START, 400),
                2 => state.view = "unknown".into(),
                3 => state.rightBasisPoints = state.leftBasisPoints,
                _ => state.busy = true,
            }
            assert!(validate_observation(&initial(), &state, &add_ms(START, 200)).is_err());
        }
    }
}
