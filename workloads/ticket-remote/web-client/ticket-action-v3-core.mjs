const ACTIVATION_TARGETS = new Set(['open_latest_and_register', 'register_current']);
const REGISTRATION_PROOF_TARGETS = new Set([
  'open_latest_unactivated',
  'return_to_latest_unactivated',
  'redetect_latest',
  'prove_current'
]);
export const TICKET_LOCAL_REGISTER_SLIDER_COMPLETION_PERCENT = 25;
export const TICKET_LOCAL_REGISTER_SLIDER_MIN_POINTER_PX = 8;

export function ticketActionV3ClientId(scope, now = Date.now(), entropy = Math.random().toString(36).slice(2, 10)) {
  const cleanScope = String(scope || 'action').toLowerCase().replace(/[^a-z0-9_-]+/g, '_').slice(0, 32) || 'action';
  const cleanEntropy = String(entropy || '').toLowerCase().replace(/[^a-z0-9_-]+/g, '').slice(0, 24) || 'client';
  return `ticket_action_v3_${cleanScope}_${Math.max(0, Math.trunc(Number(now) || 0))}_${cleanEntropy}`;
}

export function ticketActionV3RequestArgs(input) {
  const target = String(input && input.target || '');
  const actionId = String(input && input.actionId || '');
  return {
    actionId,
    target,
    source: String(input && input.source || ''),
    reason: String(input && input.reason || ''),
    attemptId: ACTIVATION_TARGETS.has(target) ? actionId : '',
    expectedInteractionRevision: target === 'register_current'
      ? String(input && input.expectedInteractionRevision || '')
      : '',
    scheduleId: String(input && input.scheduleId || '')
  };
}

export function adminRedetectTicketActionV3Args(actionId) {
  return ticketActionV3RequestArgs({
    actionId,
    target: 'redetect_latest',
    source: 'ticket_remote_admin',
    reason: 'ticket_action_requested'
  });
}

export function adminScheduleTicketActionV3Args(input) {
  const scheduledAtMillis = Math.trunc(Number(input && input.scheduledAtMillis));
  if (!Number.isFinite(scheduledAtMillis) || scheduledAtMillis <= 0) {
    throw new Error('invalid scheduled time');
  }
  return {
    scheduleId: String(input && input.scheduleId || ''),
    scheduledAtMicros: BigInt(scheduledAtMillis) * 1000n,
    phoneLocalTime: String(input && input.phoneLocalTime || ''),
    phoneTimeZone: String(input && input.phoneTimeZone || ''),
    target: 'redetect_latest'
  };
}

export function ticketActionV3ZonedLocalMillis(dateValue, timeValue, timeZone) {
  const match = `${dateValue}T${timeValue}`.match(/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/);
  if (!match) throw new Error('Izvēlies derīgu datumu un laiku.');
  const desired = match.slice(1).map(Number);
  const desiredUtc = Date.UTC(desired[0], desired[1] - 1, desired[2], desired[3], desired[4]);
  const formatter = new Intl.DateTimeFormat('en-CA', {
    timeZone, year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', hourCycle: 'h23'
  });
  const expectedLocal = `${dateValue}T${timeValue}`;
  const candidates = [];
  for (let offsetMinutes = -14 * 60; offsetMinutes <= 14 * 60; offsetMinutes += 15) {
    const candidate = desiredUtc - offsetMinutes * 60_000;
    const parts = Object.fromEntries(formatter.formatToParts(new Date(candidate)).map((part) => [part.type, part.value]));
    const renderedLocal = `${parts.year}-${parts.month}-${parts.day}T${parts.hour}:${parts.minute}`;
    if (renderedLocal === expectedLocal) candidates.push(candidate);
  }
  if (candidates.length === 0) {
    throw new Error('Šis vietējais laiks nepastāv laika joslas maiņas dēļ.');
  }
  return Math.min(...candidates);
}

export function shouldSubmitTicketSliderCompletion(pointer, phase, progress) {
  if (!pointer || pointer.submitted || phase !== 'up' || pointer.qualified !== true ||
    Number(progress) < TICKET_LOCAL_REGISTER_SLIDER_COMPLETION_PERCENT * 100
  ) return false;
  pointer.submitted = true;
  return true;
}

export function ticketLocalRegisterSliderProofSnapshot(
  action,
  region,
  stream,
  layoutRevision = 0,
  visualRevision = 0,
  now = Date.now()
) {
  const currentRegion = ticketSliderRegionV3ForAction(action, region, stream, now);
  if (!currentRegion) return null;
  return {
    proofActionId: String(currentRegion.proofActionId || ''),
    streamEpoch: Number(currentRegion.streamEpoch || 0),
    frameSequence: Number(currentRegion.frameSequence || 0),
    expiresAt: String(currentRegion.expiresAt || ''),
    leftBasisPoints: Number(currentRegion.leftBasisPoints),
    topBasisPoints: Number(currentRegion.topBasisPoints),
    rightBasisPoints: Number(currentRegion.rightBasisPoints),
    bottomBasisPoints: Number(currentRegion.bottomBasisPoints),
    layoutRevision: Math.max(0, Math.trunc(Number(layoutRevision) || 0)),
    visualRevision: Math.max(0, Math.trunc(Number(visualRevision) || 0))
  };
}

export function ticketLocalRegisterSliderProofMatches(
  snapshot,
  action,
  region,
  stream,
  layoutRevision = 0,
  visualRevision = 0,
  now = Date.now()
) {
  if (!snapshot) return false;
  const current = ticketLocalRegisterSliderProofSnapshot(
    action,
    region,
    stream,
    layoutRevision,
    visualRevision,
    now
  );
  if (!current) return false;
  return [
    'proofActionId',
    'streamEpoch',
    'frameSequence',
    'expiresAt',
    'leftBasisPoints',
    'topBasisPoints',
    'rightBasisPoints',
    'bottomBasisPoints',
    'layoutRevision',
    'visualRevision'
  ].every((field) => current[field] === snapshot[field]);
}

export function beginTicketLocalRegisterSliderSession(state, input) {
  if (!state || state.inFlight || state.session) return false;
  const snapshot = input && input.snapshot;
  if (!snapshot || !String(snapshot.proofActionId || '').trim()) return false;
  const kind = String(input && input.kind || 'pointer');
  const pointerId = kind === 'pointer' ? Number(input && input.pointerId) : null;
  const pointerStartClientX = kind === 'pointer' ? Number(input && input.pointerStartClientX) : null;
  const pointerStartClientY = kind === 'pointer' ? Number(input && input.pointerStartClientY) : null;
  const pointerTrackLeftClientX = kind === 'pointer' ? Number(input && input.pointerTrackLeftClientX) : null;
  const pointerTrackWidth = kind === 'pointer' ? Number(input && input.pointerTrackWidth) : null;
  const pointerTrackRightClientX = kind === 'pointer' ? pointerTrackLeftClientX + pointerTrackWidth : null;
  if (kind === 'pointer' && (
    !Number.isFinite(pointerId) || !Number.isFinite(pointerStartClientX) || !Number.isFinite(pointerStartClientY) ||
    !Number.isFinite(pointerTrackLeftClientX) ||
    !Number.isFinite(pointerTrackWidth) || pointerTrackWidth <= 0 ||
    !Number.isFinite(pointerTrackRightClientX)
  )) return false;
  state.session = {
    kind,
    pointerId,
    pointerStartClientX: kind === 'pointer'
      ? Math.max(pointerTrackLeftClientX, Math.min(pointerTrackRightClientX, pointerStartClientX))
      : null,
    pointerStartClientY,
    pointerTrackLeftClientX,
    pointerTrackWidth,
    snapshot,
    directionRejected: false,
    qualified: true,
    submitted: false
  };
  return true;
}

export function cancelTicketLocalRegisterSliderSession(state, pointerId = null) {
  if (!state || !state.session || state.inFlight) return false;
  if (pointerId != null && state.session.kind === 'pointer' && Number(pointerId) !== state.session.pointerId) return false;
  state.session = null;
  return true;
}

export function updateTicketLocalRegisterSliderPointerDirection(state, input) {
  const session = state && state.session;
  if (!session || session.kind !== 'pointer' || state.inFlight || session.directionRejected) return 'inactive';
  if (Number(input && input.pointerId) !== session.pointerId) return 'inactive';
  const deltaX = Number(input && input.pointerClientX) - Number(session.pointerStartClientX);
  const deltaY = Number(input && input.pointerClientY) - Number(session.pointerStartClientY);
  if (![deltaX, deltaY].every(Number.isFinite) || Math.max(Math.abs(deltaX), Math.abs(deltaY)) < TICKET_LOCAL_REGISTER_SLIDER_MIN_POINTER_PX) {
    return 'pending';
  }
  if (deltaX <= 0 || Math.abs(deltaY) >= deltaX) {
    session.directionRejected = true;
    return 'scroll';
  }
  return 'slider';
}

function ticketLocalRegisterSliderPointerCompletes(session, endClientX, endClientY) {
  const start = Number(session && session.pointerStartClientX);
  const startY = Number(session && session.pointerStartClientY);
  const end = Number(endClientX);
  const endY = Number(endClientY);
  if (session && session.directionRejected) return false;
  if (![start, startY, end, endY].every(Number.isFinite)) return false;
  const travelX = end - start;
  const travelY = Math.abs(endY - startY);
  return travelX >= TICKET_LOCAL_REGISTER_SLIDER_MIN_POINTER_PX && travelY < travelX;
}

export function completeTicketLocalRegisterSliderSession(state, input) {
  if (!state || !state.session || state.inFlight) return null;
  const session = state.session;
  if (session.kind === 'pointer' && Number(input && input.pointerId) !== session.pointerId) return null;
  if (input && input.proofMatches !== true) {
    state.session = null;
    return null;
  }
  const completed = session.kind === 'pointer'
    ? ticketLocalRegisterSliderPointerCompletes(
      session,
      input && input.pointerClientX,
      input && input.pointerClientY
    ) &&
      shouldSubmitTicketSliderCompletion(session, 'up', 10000)
    : shouldSubmitTicketSliderCompletion(
      session,
      'up',
      Math.max(0, Math.min(10000, Math.round(Number(input && input.progress || 0) * 100)))
    );
  state.session = null;
  return completed ? session.snapshot : null;
}

export function resetTicketLocalRegisterSliderState(state) {
  if (!state) return false;
  const changed = Boolean(state.inFlight || state.session || state.actionId || state.latchedProof);
  state.inFlight = false;
  state.session = null;
  state.actionId = '';
  state.latchedProof = null;
  return changed;
}

function ticketActionV3Family(actions, rootActionId) {
  return ticketActionV3ActionsByAuthority((Array.isArray(actions) ? actions : []).filter((action) =>
    [action && action.actionId, action && action.rootActionId].some((id) => String(id || '').trim() === rootActionId)
  ));
}

export function releaseTicketLocalRegisterSliderOnTerminal(state, actions) {
  if (!state || !state.inFlight || !String(state.actionId || '').trim()) return null;
  const rootActionId = String(state.actionId || '').trim();
  const family = ticketActionV3Family(actions, rootActionId);
  if (family.some((action) => ticketActionV3OccupiesPhone(action))) return null;
  const exact = family[0];
  if (!exact || !['succeeded', 'failed', 'needs_attention'].includes(String(exact.status || ''))) return null;
  resetTicketLocalRegisterSliderState(state);
  return exact;
}

export function ticketActionV3OccupiesPhone(action) {
  return Boolean(action && ['queued', 'pending', 'running'].includes(String(action.status || '')));
}

function ticketActionV3CreatedOrder(action) {
  const value = String(action && action.createdAt || '').trim();
  const millis = Date.parse(value);
  if (!Number.isFinite(millis)) return null;
  const fractional = value.match(/T\d{2}:\d{2}:\d{2}(?:\.(\d+))?(?:Z|[+-]\d{2}:\d{2})$/i)?.[1] || '';
  const subMillis = Number(`${fractional.slice(3, 9)}000000`.slice(0, 6));
  return { millis, subMillis };
}

export function compareTicketActionV3Authority(left, right) {
  const leftCreated = ticketActionV3CreatedOrder(left);
  const rightCreated = ticketActionV3CreatedOrder(right);
  if (leftCreated && rightCreated) {
    if (leftCreated.millis !== rightCreated.millis) return rightCreated.millis - leftCreated.millis;
    if (leftCreated.subMillis !== rightCreated.subMillis) return rightCreated.subMillis - leftCreated.subMillis;
  } else if (leftCreated || rightCreated) {
    return leftCreated ? -1 : 1;
  }
  const actionIdOrder = String(right && right.actionId || '').localeCompare(String(left && left.actionId || ''));
  if (actionIdOrder) return actionIdOrder;
  return String(right && right.id || '').localeCompare(String(left && left.id || ''));
}

export function ticketActionV3ActionsByAuthority(actions) {
  return (Array.isArray(actions) ? [...actions] : []).sort(compareTicketActionV3Authority);
}

export function ticketActionV3ExplicitResultForDisplay(actions, actionId, rememberedAction = null) {
  const stableActionId = String(actionId || '').trim();
  if (!stableActionId) return null;
  const exactAction = ticketActionV3Family(actions, stableActionId)[0];
  if (exactAction) return exactAction;
  return String(rememberedAction && rememberedAction.actionId || '').trim() === stableActionId
    ? rememberedAction
    : null;
}

export function ticketControlCodeVisualRecoveryRequired(requests) {
  if (!Array.isArray(requests)) return false;
  return requests.some((request) => {
    if (!request || request.cleanupPending !== true) return false;
    const status = String(request.status || '');
    return status !== 'closed' && status !== 'expired';
  });
}

export function ticketActionV3LocalRequestBusy(state) {
  return Boolean(state && String(state.actionId || '').trim());
}

function clearTicketActionV3LocalRequest(state) {
  state.actionId = '';
  state.reducerSettled = false;
  state.observed = false;
}

function finishTicketActionV3LocalRequestIfReady(state) {
  if (!ticketActionV3LocalRequestBusy(state) || state.reducerSettled !== true || state.observed !== true) {
    return false;
  }
  clearTicketActionV3LocalRequest(state);
  return true;
}

export function beginTicketActionV3LocalRequest(state, actionId) {
  if (!state || ticketActionV3LocalRequestBusy(state)) return false;
  const stableActionId = String(actionId || '').trim();
  if (!stableActionId) return false;
  state.actionId = stableActionId;
  state.reducerSettled = false;
  state.observed = false;
  return true;
}

export function settleTicketActionV3LocalRequest(state, accepted) {
  if (!ticketActionV3LocalRequestBusy(state)) return false;
  if (accepted !== true) {
    clearTicketActionV3LocalRequest(state);
    return true;
  }
  state.reducerSettled = true;
  return finishTicketActionV3LocalRequestIfReady(state);
}

export function observeTicketActionV3LocalRequest(state, action) {
  if (!ticketActionV3LocalRequestBusy(state)) return false;
  if (String(action && action.actionId || '').trim() === String(state.actionId || '').trim()) {
    state.observed = true;
  }
  return finishTicketActionV3LocalRequestIfReady(state);
}

export function ticketActionV3SmartSwitchAction(actions, now = Date.now()) {
  const currentMillis = Number(now);
  if (!Number.isFinite(currentMillis)) return null;
  return ticketActionV3ActionsByAuthority(actions).find((action) => {
    if (!action || action.switchAvailable !== true ||
      String(action.status || '') !== 'succeeded' || String(action.phase || '') !== 'complete'
    ) return false;
    if (!['latest_unactivated', 'recent_activated'].includes(String(action.currentView || ''))) return false;
    const expiresAt = Date.parse(String(action.switchExpiresAt || ''));
    return Number.isFinite(expiresAt) && expiresAt > currentMillis;
  }) || null;
}

export function ticketActionV3SmartSwitchForView(currentView) {
  if (String(currentView || '') === 'latest_unactivated') {
    return {
      label: 'Skatīt pēdējo reģistrēto biļeti',
      target: 'show_recent_activated'
    };
  }
  if (String(currentView || '') === 'recent_activated') {
    return {
      label: 'Atgriezties pie nereģistrētās biļetes',
      target: 'return_to_latest_unactivated'
    };
  }
  return {
    label: 'Skatīt pēdējo reģistrēto biļeti',
    target: ''
  };
}

export function ticketMemberLimitBlocks(limits, kind) {
  if (!limits) return true;
  if (limits.effectiveLimited === false) return false;
  if (kind === 'registration') return limits.registrationAllowed !== true;
  if (kind === 'control_code') return limits.controlCodeAllowed !== true;
  return true;
}

export function ticketMemberLimitCountdown(targetAt, now = Date.now()) {
  const target = Date.parse(String(targetAt || ''));
  if (!Number.isFinite(target)) return '';
  const remainingSeconds = Math.max(0, Math.ceil((target - Number(now)) / 1000));
  if (remainingSeconds <= 0) return 'gaida SpaceTime atjauninājumu';
  if (remainingSeconds < 60) return `pēc ${remainingSeconds} s`;
  const minutes = Math.floor(remainingSeconds / 60);
  const seconds = remainingSeconds % 60;
  return seconds ? `pēc ${minutes} min ${seconds} s` : `pēc ${minutes} min`;
}

function ticketMemberLimitProjectionOrder(limits) {
  const updatedAtText = String(limits && limits.updatedAt || '').trim();
  const serverAtText = String(limits && limits.serverAt || '').trim();
  const updatedAt = Date.parse(updatedAtText);
  const serverAt = Date.parse(serverAtText);
  if (!Number.isFinite(updatedAt) || !Number.isFinite(serverAt)) return null;
  return { updatedAtText, serverAtText, updatedAt, serverAt };
}

function compareTicketMemberLimitProjectionOrder(left, right) {
  if (left.updatedAt !== right.updatedAt) return left.updatedAt - right.updatedAt;
  const updatedTextOrder = left.updatedAtText.localeCompare(right.updatedAtText);
  if (updatedTextOrder) return updatedTextOrder;
  if (left.serverAt !== right.serverAt) return left.serverAt - right.serverAt;
  return left.serverAtText.localeCompare(right.serverAtText);
}

export function ticketMemberLimitClockNow(clock, monotonicNow) {
  if (!clock) return Number.NaN;
  const now = Number(monotonicNow);
  const anchoredAt = Number(clock.monotonicAt);
  const serverAt = Number(clock.serverAt);
  if (![now, anchoredAt, serverAt].every(Number.isFinite)) return Number.NaN;
  return serverAt + Math.max(0, now - anchoredAt);
}

export function updateTicketMemberLimitClock(clock, limits, monotonicNow) {
  const projection = ticketMemberLimitProjectionOrder(limits);
  const now = Number(monotonicNow);
  if (!projection || !Number.isFinite(now)) return clock || null;
  if (clock && compareTicketMemberLimitProjectionOrder(projection, clock.projection) <= 0) {
    return clock;
  }
  const previousNow = ticketMemberLimitClockNow(clock, now);
  return {
    projection,
    monotonicAt: now,
    serverAt: Number.isFinite(previousNow)
      ? Math.max(previousNow, projection.serverAt)
      : projection.serverAt
  };
}

export async function handleTicketLocalRegisterSliderChange({
  slider,
  state,
  actionId,
  proofSnapshot,
  submitRegisterCurrent,
  render
}) {
  const stableActionId = String(actionId || '').trim();
  if (!slider || !state || state.inFlight || !stableActionId || !proofSnapshot ||
    Number(slider.value || 0) < TICKET_LOCAL_REGISTER_SLIDER_COMPLETION_PERCENT
  ) return false;
  state.inFlight = true;
  state.actionId = stableActionId;
  state.latchedProof = proofSnapshot;
  slider.disabled = true;
  slider.value = '100';
  if (typeof render === 'function') render();
  try {
    const accepted = await submitRegisterCurrent('browser_slider', stableActionId, proofSnapshot);
    if (accepted === true) return true;
    resetTicketLocalRegisterSliderState(state);
    slider.value = '0';
    slider.disabled = false;
    if (typeof render === 'function') render();
    return false;
  } catch (error) {
    resetTicketLocalRegisterSliderState(state);
    slider.value = '0';
    slider.disabled = false;
    if (typeof render === 'function') render();
    throw error;
  }
}

export function isTicketActionV3RegistrationProofFresh(action, stream, now = Date.now()) {
  if (!action || String(action.status || '') !== 'succeeded' ||
    String(action.currentView || '') !== 'latest_unactivated' ||
    !REGISTRATION_PROOF_TARGETS.has(String(action.target || '')) ||
    !String(action.actionId || '').trim() || !stream || stream.fresh !== true
  ) return false;
  const proofEpoch = Number(action.streamEpoch || 0);
  const proofSequence = Number(action.frameSequence || 0);
  const liveEpoch = Number(stream.epoch || 0);
  const liveSequence = Number(stream.sequence || 0);
  const expiresAt = Date.parse(String(action.expiresAt || ''));
  return Number.isFinite(expiresAt) && expiresAt > Number(now) && proofEpoch > 0 && proofEpoch === liveEpoch && proofSequence > 0 &&
    liveSequence > 0 && proofSequence <= liveSequence;
}

export function ticketSliderRegionV3ForAction(action, region, stream, now = Date.now()) {
  if (!isTicketActionV3RegistrationProofFresh(action, stream, now) || !region) return null;
  const expiresAt = Date.parse(String(region.expiresAt || ''));
  const left = Number(region.leftBasisPoints);
  const top = Number(region.topBasisPoints);
  const right = Number(region.rightBasisPoints);
  const bottom = Number(region.bottomBasisPoints);
  if (String(region.proofActionId || '') !== String(action.actionId || '') ||
    String(region.streamEpoch || '') !== String(action.streamEpoch || '') ||
    String(region.frameSequence || '') !== String(action.frameSequence || '') ||
    !Number.isFinite(expiresAt) || expiresAt <= Number(now) ||
    ![left, top, right, bottom].every((value) => Number.isInteger(value) && value >= 0 && value <= 10000) ||
    !(left < right && top < bottom)
  ) return null;
  return { ...region, leftBasisPoints: left, topBasisPoints: top, rightBasisPoints: right, bottomBasisPoints: bottom };
}

export function ticketSliderRegionV3Layout(region, canvasRect, stageRect) {
  if (!region || !canvasRect || !stageRect) return null;
  const left = Number(canvasRect.left) - Number(stageRect.left) +
    Number(region.leftBasisPoints) / 10000 * Number(canvasRect.width);
  const top = Number(canvasRect.top) - Number(stageRect.top) +
    Number(region.topBasisPoints) / 10000 * Number(canvasRect.height);
  const width = (Number(region.rightBasisPoints) - Number(region.leftBasisPoints)) / 10000 * Number(canvasRect.width);
  const height = (Number(region.bottomBasisPoints) - Number(region.topBasisPoints)) / 10000 * Number(canvasRect.height);
  if (![left, top, width, height].every(Number.isFinite) || width <= 0 || height <= 0) return null;
  return { left, top, width, height };
}

export function ticketCurrentProofFingerprintChanged(previous, current, threshold = 18) {
  if (!Array.isArray(previous) || !Array.isArray(current) || previous.length !== current.length || previous.length === 0) {
    return false;
  }
  let changed = 0;
  for (let index = 0; index < previous.length; index += 1) {
    if (Math.abs(Number(previous[index]) - Number(current[index])) >= threshold) changed += 1;
  }
  return changed >= Math.max(4, Math.ceil(current.length * 0.18));
}

export function rebaseTicketCurrentProofDetectorFromAction(state, input) {
  if (!state || !input) return false;
  const action = input.action;
  const stream = input.stream;
  const sample = input.sample;
  const currentRegion = ticketSliderRegionV3ForAction(
    action,
    input.region,
    stream,
    input.now == null ? Date.now() : input.now
  );
  const actionId = String(action && action.actionId || '').trim();
  if (!currentRegion || String(action && action.phase || '') !== 'complete' ||
    !actionId || actionId === String(state.rebasedActionId || '').trim() ||
    !sample || !Array.isArray(sample.values) || sample.values.length === 0
  ) return false;
  const proofEpoch = Number(currentRegion.streamEpoch || 0);
  const proofSequence = Number(currentRegion.frameSequence || 0);
  const sampleEpoch = Number(sample.epoch || 0);
  const sampleSequence = Number(sample.sequence || 0);
  if (!(proofEpoch > 0) || sampleEpoch !== proofEpoch || sampleSequence < proofSequence) return false;
  state.rebasedActionId = actionId;
  state.fingerprint = sample;
  state.candidateFingerprint = null;
  state.stableChangeCount = 0;
  state.changePending = false;
  state.resumePending = false;
  return true;
}

export function isTicketActionV3CurrentProofFresh(action, stream, now = Date.now()) {
  if (!action || String(action.target || '') !== 'prove_current' ||
    !['succeeded', 'failed', 'needs_attention'].includes(String(action.status || '')) ||
    !stream || stream.fresh !== true
  ) return false;
  const expiresAt = Date.parse(String(action.expiresAt || ''));
  const proofEpoch = Number(action.streamEpoch || 0);
  const proofSequence = Number(action.frameSequence || 0);
  const liveEpoch = Number(stream.epoch || 0);
  const liveSequence = Number(stream.sequence || 0);
  return Number.isFinite(expiresAt) && expiresAt > Number(now) && proofEpoch > 0 &&
    proofEpoch === liveEpoch && proofSequence > 0 && liveSequence > 0 && proofSequence <= liveSequence;
}

export function ticketCurrentProofRequestNeeded({
  visible,
  stream,
  action,
  region,
  now = Date.now(),
  requestedEpoch = 0,
  stableChangeCount = 0,
  resumed = false,
  renewBeforeMs = 0,
  recoveryRequired = false,
  unknownAwaitingChange = false
}) {
  if (recoveryRequired === true) return false;
  if (!visible || !stream || stream.fresh !== true || !(Number(stream.epoch) > 0) || !(Number(stream.sequence) > 0)) return false;
  if (ticketActionV3OccupiesPhone(action)) return false;
  if (stableChangeCount >= 2) return true;
  if (unknownAwaitingChange === true) return false;
  const freshRegion = ticketSliderRegionV3ForAction(action, region, stream, now);
  if (freshRegion) {
    const expiresAt = Date.parse(String(freshRegion.expiresAt || ''));
    const renewalWindow = Math.max(0, Number(renewBeforeMs) || 0);
    return renewalWindow > 0 && Number.isFinite(expiresAt) && expiresAt - Number(now) <= renewalWindow;
  }
  if (isTicketActionV3CurrentProofFresh(action, stream, now)) return false;
  const priorUnknownInThisEpoch = action && String(action.target || '') === 'prove_current' &&
    ['succeeded', 'failed', 'needs_attention'].includes(String(action.status || '')) &&
    String(action.currentView || '') === 'unknown' &&
    Number(action.streamEpoch || 0) === Number(stream.epoch || 0);
  if (priorUnknownInThisEpoch) return false;
  const firstFreshFrame = Number(requestedEpoch || 0) !== Number(stream.epoch);
  return firstFreshFrame || resumed === true;
}

export async function dispatchTicketActionV3ForLocalGate(callReducer, input, acknowledge) {
  const payload = ticketActionV3RequestArgs(input);
  await callReducer(payload);
  acknowledge(payload);
  return payload;
}
