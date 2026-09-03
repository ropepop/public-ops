import assert from 'node:assert/strict';
import test from 'node:test';
import { performance } from 'node:perf_hooks';
import {
  TICKET_LOCAL_REGISTER_SLIDER_COMPLETION_PERCENT,
  adminRedetectTicketActionV3TerminalMessage,
  adminRedetectTicketActionV3Args,
  adminScheduleTicketActionV3Args,
  beginTicketActionV3LocalRequest,
  beginTicketLocalRegisterSliderSession,
  cancelTicketLocalRegisterSliderSession,
  completeTicketLocalRegisterSliderSession,
  dispatchTicketActionV3ForLocalGate,
  handleTicketLocalRegisterSliderChange,
  isTicketActionV3CurrentProofFresh,
  isTicketActionV3RegistrationProofFresh,
  observeTicketActionV3LocalRequest,
  rebaseTicketCurrentProofDetectorFromAction,
  releaseTicketLocalRegisterSliderOnTerminal,
  resetTicketLocalRegisterSliderState,
  settleTicketActionV3LocalRequest,
  shouldSubmitTicketSliderCompletion,
  ticketActionV3ActionsByAuthority,
  ticketCurrentProofFingerprintChanged,
  ticketCurrentProofRequestNeeded,
  ticketControlCodeVisualRecoveryRequired,
  ticketActionV3ActivationTerminalMessage,
  ticketActionV3ClientId,
  ticketActionV3ExplicitResultForDisplay,
  ticketActionV3IsExpectedEmptyRedetect,
  ticketActionV3LocalRequestBusy,
  ticketActionV3OccupiesPhone,
  ticketActionV3RequestArgs,
  ticketActionV3SmartSwitchAction,
  ticketActionV3SmartSwitchForView,
  ticketMemberLimitBlocks,
  ticketMemberLimitClockNow,
  ticketMemberLimitCountdown,
  ticketLocalRegisterSliderProofMatches,
  ticketLocalRegisterSliderProofSnapshot,
  updateTicketLocalRegisterSliderPointerDirection,
  updateTicketMemberLimitClock,
  ticketActionV3ZonedLocalMillis,
  ticketSliderRegionV3ForAction,
  ticketSliderRegionV3Layout
} from './ticket-action-v3-core.mjs';

test('historical switch projection refresh cannot displace the newest real action', () => {
  const historicalActionWithFreshProjection = {
    id: 'backend:historical',
    actionId: 'historical',
    target: 'open_latest_unactivated',
    status: 'succeeded',
    currentView: 'latest_unactivated',
    switchAvailable: true,
    createdAt: '2026-08-25T12:00:00Z',
    updatedAt: '2026-08-25T12:15:00Z'
  };
  const newestRealAction = {
    id: 'backend:newest',
    actionId: 'newest',
    target: 'show_recent_activated',
    status: 'running',
    currentView: 'latest_unactivated',
    switchAvailable: false,
    createdAt: '2026-08-25T12:10:00Z',
    updatedAt: '2026-08-25T12:10:01Z'
  };

  assert.deepEqual(
    ticketActionV3ActionsByAuthority([historicalActionWithFreshProjection, newestRealAction]),
    [newestRealAction, historicalActionWithFreshProjection]
  );
});

test('current action authority preserves sub-millisecond creation order and deterministic exact ties', () => {
  const olderFraction = {
    id: 'backend:fraction-z', actionId: 'fraction-z',
    createdAt: '2026-08-25T12:00:00.123001Z', updatedAt: '2026-08-25T12:30:00Z'
  };
  const newerFraction = {
    id: 'backend:fraction-a', actionId: 'fraction-a',
    createdAt: '2026-08-25T12:00:00.123999Z', updatedAt: '2026-08-25T12:00:01Z'
  };
  const exactTieA = {
    id: 'backend:tie-a', actionId: 'tie-a', createdAt: '2026-08-25T12:20:00Z'
  };
  const exactTieZ = {
    id: 'backend:tie-z', actionId: 'tie-z', createdAt: '2026-08-25T12:20:00Z'
  };

  assert.deepEqual(
    ticketActionV3ActionsByAuthority([olderFraction, newerFraction]),
    [newerFraction, olderFraction],
    'Spacetime microsecond creation order must survive JavaScript millisecond parsing'
  );
  assert.deepEqual(
    ticketActionV3ActionsByAuthority([exactTieA, exactTieZ]),
    [exactTieZ, exactTieA],
    'the action ID is the stable final authority for an exact creation-time tie'
  );
  assert.deepEqual(
    ticketActionV3ActionsByAuthority([exactTieZ, exactTieA]),
    [exactTieZ, exactTieA],
    'subscription iteration order cannot change the exact-tie result'
  );
});

test('member limit gating is projection-authoritative while countdowns are presentation-only', () => {
  const limited = {
    effectiveLimited: true,
    registrationAllowed: false,
    controlCodeAllowed: true
  };
  assert.equal(ticketMemberLimitBlocks(null, 'registration'), true);
  assert.equal(ticketMemberLimitBlocks(limited, 'registration'), true);
  assert.equal(ticketMemberLimitBlocks(limited, 'control_code'), false);
  assert.equal(ticketMemberLimitBlocks({ ...limited, effectiveLimited: false }, 'registration'), false);
  assert.equal(ticketMemberLimitCountdown('2026-08-25T12:00:30Z', Date.parse('2026-08-25T12:00:00Z')), 'pēc 30 s');
  assert.equal(ticketMemberLimitCountdown('2026-08-25T12:00:00Z', Date.parse('2026-08-25T12:00:01Z')), 'gaida SpaceTime atjauninājumu');
});

test('member limit clock advances monotonically from the newest Spacetime projection', () => {
  const first = {
    updatedAt: '2026-08-25T12:00:00.100Z',
    serverAt: '2026-08-25T12:00:00Z'
  };
  const anchor = updateTicketMemberLimitClock(null, first, 1000);
  assert.equal(ticketMemberLimitClockNow(anchor, 2500), Date.parse(first.serverAt) + 1500);

  const duplicate = updateTicketMemberLimitClock(anchor, first, 9000);
  assert.equal(duplicate, anchor, 'unrelated state publications cannot reset the countdown anchor');
  assert.equal(ticketMemberLimitClockNow(duplicate, 9000), Date.parse(first.serverAt) + 8000);

  const stale = updateTicketMemberLimitClock(anchor, {
    updatedAt: '2026-08-25T11:59:59.999Z',
    serverAt: '2026-08-25T12:00:30Z'
  }, 10_000);
  assert.equal(stale, anchor, 'an older projection cannot move the countdown in either direction');
});

test('newer member limit projections cannot move logical server time backward', () => {
  const anchor = updateTicketMemberLimitClock(null, {
    updatedAt: '2026-08-25T12:00:00Z',
    serverAt: '2026-08-25T12:00:00Z'
  }, 1000);
  const beforeUpdate = ticketMemberLimitClockNow(anchor, 6000);
  const updated = updateTicketMemberLimitClock(anchor, {
    updatedAt: '2026-08-25T12:00:01Z',
    serverAt: '2026-08-25T11:59:59Z'
  }, 6000);
  assert.equal(ticketMemberLimitClockNow(updated, 6000), beforeUpdate);
  assert.equal(ticketMemberLimitClockNow(updated, 7000), beforeUpdate + 1000);
});

test('member limit clock rejects unreadable projections and ignores the local wall clock', () => {
  assert.equal(updateTicketMemberLimitClock(null, { updatedAt: '', serverAt: '' }, 1000), null);
  const anchor = updateTicketMemberLimitClock(null, {
    updatedAt: '2026-08-25T12:00:00Z',
    serverAt: '2026-08-25T12:00:00Z'
  }, 1000);
  assert.equal(ticketMemberLimitClockNow(anchor, 2000), Date.parse('2026-08-25T12:00:01Z'));
  assert.equal(ticketMemberLimitClockNow(anchor, 500), Date.parse('2026-08-25T12:00:00Z'));
});

test('registration authority is the exact fresh v3 visual watermark', () => {
  const action = {
    actionId: 'open-proof-1',
    target: 'open_latest_unactivated',
    status: 'succeeded',
    currentView: 'latest_unactivated',
    streamEpoch: '101',
    frameSequence: '202',
    expiresAt: '2026-08-24T12:05:00Z'
  };
  const now = Date.parse('2026-08-24T12:00:00Z');
  assert.equal(isTicketActionV3RegistrationProofFresh(action, { fresh: true, epoch: 101, sequence: 205 }, now), true);
  assert.equal(isTicketActionV3RegistrationProofFresh(action, { fresh: true, epoch: 102, sequence: 205 }, now), false);
  assert.equal(isTicketActionV3RegistrationProofFresh({ ...action, status: 'failed' }, { fresh: true, epoch: 101, sequence: 205 }, now), false);
  assert.equal(isTicketActionV3RegistrationProofFresh({ ...action, target: 'show_recent_activated' }, { fresh: true, epoch: 101, sequence: 205 }, now), false);
  assert.equal(isTicketActionV3RegistrationProofFresh({ ...action, target: 'prove_current' }, { fresh: true, epoch: 101, sequence: 205 }, now), true);
  assert.equal(isTicketActionV3RegistrationProofFresh({ ...action, target: 'redetect_latest' }, { fresh: true, epoch: 101, sequence: 205 }, now), true);
  assert.equal(isTicketActionV3RegistrationProofFresh(action, { fresh: true, epoch: 101, sequence: 205 }, Date.parse('2026-08-24T12:05:00Z')), false);
});

test('normalized slider geometry requires the exact action watermark and expiry', () => {
  const action = {
    actionId: 'proof-current-1', target: 'prove_current', status: 'succeeded',
    currentView: 'latest_unactivated', streamEpoch: '101', frameSequence: '202',
    expiresAt: '2026-08-24T12:05:00Z'
  };
  const region = {
    proofActionId: 'proof-current-1', streamEpoch: '101', frameSequence: '202',
    leftBasisPoints: 1200, topBasisPoints: 7000, rightBasisPoints: 8800, bottomBasisPoints: 7600,
    expiresAt: '2026-08-24T12:05:00Z'
  };
  const stream = { fresh: true, epoch: 101, sequence: 205 };
  assert.ok(ticketSliderRegionV3ForAction(action, region, stream, Date.parse('2026-08-24T12:00:00Z')));
  assert.equal(ticketSliderRegionV3ForAction(action, { ...region, frameSequence: '203' }, stream, Date.parse('2026-08-24T12:00:00Z')), null);
  assert.equal(ticketSliderRegionV3ForAction(action, { ...region, rightBasisPoints: 10001 }, stream, Date.parse('2026-08-24T12:00:00Z')), null);
  assert.equal(ticketSliderRegionV3ForAction(action, region, stream, Date.parse('2026-08-24T12:05:00Z')), null);
  assert.deepEqual(ticketSliderRegionV3Layout(region,
    { left: 20, top: 30, width: 500, height: 1000 },
    { left: 5, top: 10 }
  ), { left: 75, top: 720, width: 380, height: 60 });
});

test('auto proof is coalesced by stream epoch and two stable change samples', () => {
  const stream = { fresh: true, epoch: 101, sequence: 205 };
  assert.equal(ticketCurrentProofRequestNeeded({ visible: true, stream, requestedEpoch: 0 }), true);
  assert.equal(ticketCurrentProofRequestNeeded({ visible: true, stream, requestedEpoch: 101, stableChangeCount: 1 }), false);
  assert.equal(ticketCurrentProofRequestNeeded({ visible: true, stream, requestedEpoch: 101, stableChangeCount: 2 }), true);
  assert.equal(ticketCurrentProofRequestNeeded({ visible: false, stream, requestedEpoch: 0 }), false);
  assert.equal(ticketCurrentProofRequestNeeded({ visible: true, stream, requestedEpoch: 0, action: { status: 'running' } }), false);
  const unactivatedProof = {
    actionId: 'proof-current-renewal', target: 'prove_current', status: 'succeeded',
    currentView: 'latest_unactivated', streamEpoch: '101', frameSequence: '202',
    expiresAt: '2026-08-24T12:05:00Z'
  };
  const unactivatedRegion = {
    proofActionId: 'proof-current-renewal', streamEpoch: '101', frameSequence: '202',
    leftBasisPoints: 1200, topBasisPoints: 7000, rightBasisPoints: 8800, bottomBasisPoints: 7600,
    expiresAt: '2026-08-24T12:05:00Z'
  };
  assert.equal(ticketCurrentProofRequestNeeded({
    visible: true, stream, action: unactivatedProof, region: unactivatedRegion,
    requestedEpoch: 101, renewBeforeMs: 15_000,
    now: Date.parse('2026-08-24T12:04:44Z')
  }), false, 'fresh slider geometry remains quiet outside its renewal window');
  assert.equal(ticketCurrentProofRequestNeeded({
    visible: true, stream, action: unactivatedProof, region: unactivatedRegion,
    requestedEpoch: 101, renewBeforeMs: 15_000,
    now: Date.parse('2026-08-24T12:04:45Z')
  }), true, 'slider proof renews before geometry expiry instead of disappearing first');
  const currentProof = {
    target: 'prove_current', status: 'succeeded', currentView: 'activated_current',
    streamEpoch: '101', frameSequence: '202', expiresAt: '2026-08-24T12:05:00Z'
  };
  assert.equal(isTicketActionV3CurrentProofFresh(currentProof, stream, Date.parse('2026-08-24T12:00:00Z')), true);
  assert.equal(ticketCurrentProofRequestNeeded({
    visible: true, stream, action: currentProof, requestedEpoch: 101, resumed: true,
    now: Date.parse('2026-08-24T12:00:00Z')
  }), false, 'a visible resume must not repeat a still-fresh activated proof');
  assert.equal(ticketCurrentProofRequestNeeded({
    visible: true, stream, action: currentProof, requestedEpoch: 101, stableChangeCount: 2,
    now: Date.parse('2026-08-24T12:00:00Z')
  }), true, 'two agreeing significant frame changes override a still-fresh proof');
  assert.equal(ticketCurrentProofRequestNeeded({
    visible: true, stream, action: { target: 'prove_current', status: 'running' },
    requestedEpoch: 101, stableChangeCount: 2,
    now: Date.parse('2026-08-24T12:00:00Z')
  }), false, 'the frame-change trigger remains inadmissible while the phone is busy');
  assert.equal(ticketCurrentProofRequestNeeded({
    visible: true, stream, requestedEpoch: 101, stableChangeCount: 2,
    now: Date.parse('2026-08-24T12:00:00Z')
  }), true, 'the same retained frame-change trigger is admitted once the phone is idle');
  assert.equal(ticketCurrentProofRequestNeeded({
    visible: true, stream, action: currentProof, requestedEpoch: 101, resumed: true,
    now: Date.parse('2026-08-24T12:05:00Z')
  }), true, 'an expired proof is re-established on visible resume');
  const unknownProof = { ...currentProof, status: 'failed', currentView: 'unknown' };
  assert.equal(ticketCurrentProofRequestNeeded({
    visible: true, stream, action: unknownProof, requestedEpoch: 0, resumed: true,
    now: Date.parse('2026-08-24T12:05:00Z')
  }), false, 'an unknown proof in the same epoch waits for another meaningful frame change');
  assert.equal(ticketCurrentProofRequestNeeded({
    visible: true, stream, action: unknownProof, requestedEpoch: 101, stableChangeCount: 2,
    now: Date.parse('2026-08-24T12:05:00Z')
  }), true, 'an unknown proof is retried after two agreeing significant changes');
  assert.equal(ticketCurrentProofRequestNeeded({
    visible: true, stream: { ...stream, epoch: 102 }, action: unknownProof,
    requestedEpoch: 101, unknownAwaitingChange: true,
    now: Date.parse('2026-08-24T12:05:00Z')
  }), false, 'a reconnect caused by this page exact unknown proof is not a retry signal');
  assert.equal(ticketCurrentProofRequestNeeded({
    visible: true, stream: { ...stream, epoch: 102 }, action: unknownProof,
    requestedEpoch: 0, unknownAwaitingChange: false,
    now: Date.parse('2026-08-24T12:05:00Z')
  }), true, 'a newly opened page still gets one first-frame proof in the new epoch');
  assert.equal(ticketCurrentProofRequestNeeded({
    visible: true, stream: { ...stream, epoch: 102 }, action: unknownProof,
    requestedEpoch: 101, stableChangeCount: 2, unknownAwaitingChange: true,
    now: Date.parse('2026-08-24T12:05:00Z')
  }), true, 'two agreeing changed frames release the exact unknown-proof guard');
  for (const trigger of [
    { requestedEpoch: 0 },
    { requestedEpoch: 101, resumed: true },
    { requestedEpoch: 101, stableChangeCount: 2 }
  ]) {
    assert.equal(ticketCurrentProofRequestNeeded({
      visible: true, stream, recoveryRequired: true, ...trigger
    }), false, 'visual cleanup must suppress every automatic proof trigger');
  }
  assert.equal(ticketCurrentProofFingerprintChanged([10, 10, 10, 10, 10, 10], [40, 40, 40, 40, 10, 10]), true);
  assert.equal(ticketCurrentProofFingerprintChanged([10, 10, 10, 10, 10, 10], [20, 20, 20, 20, 10, 10]), false);
});

test('a fresh returned-ticket proof consumes its expected canvas change exactly once', () => {
  const now = Date.parse('2026-08-26T02:31:40Z');
  const action = {
    actionId: 'return-proof-2', target: 'return_to_latest_unactivated', status: 'succeeded', phase: 'complete',
    currentView: 'latest_unactivated', streamEpoch: '101', frameSequence: '220',
    expiresAt: '2026-08-26T02:36:35Z'
  };
  const region = {
    proofActionId: 'return-proof-2', streamEpoch: '101', frameSequence: '220',
    leftBasisPoints: 1200, topBasisPoints: 7000, rightBasisPoints: 8800, bottomBasisPoints: 7600,
    expiresAt: '2026-08-26T02:36:35Z'
  };
  const stream = { fresh: true, epoch: 101, sequence: 225 };
  const state = {
    rebasedActionId: '',
    fingerprint: { epoch: 101, sequence: 200, values: [10, 10, 10, 10] },
    candidateFingerprint: { epoch: 101, sequence: 224, values: [40, 40, 40, 40] },
    stableChangeCount: 2,
    changePending: true,
    resumePending: true
  };
  const sample = { epoch: 101, sequence: 225, values: [40, 40, 40, 40] };

  assert.equal(rebaseTicketCurrentProofDetectorFromAction(state, {
    action, region, stream, sample, now
  }), true);
  assert.equal(state.rebasedActionId, 'return-proof-2');
  assert.equal(state.fingerprint, sample);
  assert.equal(state.candidateFingerprint, null);
  assert.equal(state.stableChangeCount, 0);
  assert.equal(state.changePending, false);
  assert.equal(state.resumePending, false);
  assert.equal(ticketCurrentProofRequestNeeded({
    visible: true,
    stream,
    action,
    region,
    requestedEpoch: 101,
    stableChangeCount: state.stableChangeCount,
    resumed: state.resumePending,
    now
  }), false, 'the exact returned-ticket proof keeps its slider ready without redundant prove_current');

  state.stableChangeCount = 2;
  state.changePending = true;
  assert.equal(rebaseTicketCurrentProofDetectorFromAction(state, {
    action,
    region,
    stream: { ...stream, sequence: 230 },
    sample: { ...sample, sequence: 230, values: [70, 70, 70, 70] },
    now
  }), false, 'the same action cannot erase a later meaningful visual change');
  assert.equal(state.changePending, true);
  assert.equal(ticketCurrentProofRequestNeeded({
    visible: true,
    stream: { ...stream, sequence: 230 },
    action,
    region,
    requestedEpoch: 101,
    stableChangeCount: 2,
    now
  }), true, 'a later two-frame change still fails closed through a fresh proof');
});

test('action proof rebasing waits for exact geometry and the rendered proof watermark', () => {
  const now = Date.parse('2026-08-26T02:31:40Z');
  const action = {
    actionId: 'open-proof-3', target: 'open_latest_unactivated', status: 'succeeded', phase: 'complete',
    currentView: 'latest_unactivated', streamEpoch: '101', frameSequence: '220',
    expiresAt: '2026-08-26T02:36:35Z'
  };
  const region = {
    proofActionId: 'open-proof-3', streamEpoch: '101', frameSequence: '220',
    leftBasisPoints: 1200, topBasisPoints: 7000, rightBasisPoints: 8800, bottomBasisPoints: 7600,
    expiresAt: '2026-08-26T02:36:35Z'
  };
  const state = { rebasedActionId: '', changePending: true, stableChangeCount: 2 };
  const snapshot = JSON.stringify(state);

  assert.equal(rebaseTicketCurrentProofDetectorFromAction(state, {
    action,
    region: { ...region, proofActionId: 'different-proof' },
    stream: { fresh: true, epoch: 101, sequence: 225 },
    sample: { epoch: 101, sequence: 225, values: [40, 40, 40, 40] },
    now
  }), false);
  assert.equal(JSON.stringify(state), snapshot, 'mismatched geometry cannot consume a pending visual change');
  assert.equal(rebaseTicketCurrentProofDetectorFromAction(state, {
    action,
    region,
    stream: { fresh: true, epoch: 101, sequence: 225 },
    sample: { epoch: 101, sequence: 219, values: [40, 40, 40, 40] },
    now
  }), false);
  assert.equal(JSON.stringify(state), snapshot, 'a canvas frame before the terminal watermark cannot become the baseline');
  assert.equal(rebaseTicketCurrentProofDetectorFromAction(state, {
    action: { ...action, phase: 'running' },
    region,
    stream: { fresh: true, epoch: 101, sequence: 225 },
    sample: { epoch: 101, sequence: 225, values: [40, 40, 40, 40] },
    now
  }), false);
  assert.equal(JSON.stringify(state), snapshot, 'a non-complete projection cannot consume a pending visual change');
});

test('control-code visual cleanup blocks automatic proof until explicit recovery', () => {
  assert.equal(ticketControlCodeVisualRecoveryRequired([
    { status: 'failed', cleanupPending: true, expiresAt: '2026-08-24T12:00:00Z' }
  ]), true);
  assert.equal(ticketControlCodeVisualRecoveryRequired([
    { status: 'succeeded', cleanupPending: true }
  ]), true);
  assert.equal(ticketControlCodeVisualRecoveryRequired([
    { status: 'running', cleanupPending: true }
  ]), true);
  assert.equal(ticketControlCodeVisualRecoveryRequired([
    { status: 'failed', cleanupPending: false },
    { status: 'closed', cleanupPending: true },
    { status: 'expired', cleanupPending: true }
  ]), false);
  assert.equal(ticketControlCodeVisualRecoveryRequired(null), false);
});

test('v3 activation correlation is exact and non-activation fields stay empty', () => {
  assert.deepEqual(ticketActionV3RequestArgs({
    actionId: 'action-1', target: 'register_current', source: 'browser_slider', reason: 'complete',
    expectedInteractionRevision: 'revision-1'
  }), {
    actionId: 'action-1', target: 'register_current', source: 'browser_slider', reason: 'complete',
    attemptId: 'action-1', expectedInteractionRevision: 'revision-1', scheduleId: ''
  });
  assert.equal(ticketActionV3RequestArgs({ actionId: 'action-2', target: 'open_latest_unactivated' }).attemptId, '');
});

test('admin immediate redetection uses one authenticated v3 action contract', () => {
  const actionId = ticketActionV3ClientId('admin redetect', 123456789, 'AbC-123');
  assert.equal(actionId, 'ticket_action_v3_admin_redetect_123456789_abc-123');
  assert.match(actionId, /^[a-z0-9_-]+$/);
  assert.ok(actionId.length <= 120);
  assert.deepEqual(adminRedetectTicketActionV3Args(actionId), {
    actionId,
    target: 'redetect_latest',
    source: 'ticket_remote_admin',
    reason: 'ticket_action_requested',
    attemptId: '',
    expectedInteractionRevision: '',
    scheduleId: ''
  });
});

test('admin scheduled redetection carries exact v3 schedule fields', () => {
  assert.deepEqual(adminScheduleTicketActionV3Args({
    scheduleId: 'ticket_action_v3_schedule_1',
    scheduledAtMillis: 1_800_000_000_123,
    phoneLocalTime: '2027-01-15T12:30',
    phoneTimeZone: 'Europe/Riga'
  }), {
    scheduleId: 'ticket_action_v3_schedule_1',
    scheduledAtMicros: 1_800_000_000_123_000n,
    phoneLocalTime: '2027-01-15T12:30',
    phoneTimeZone: 'Europe/Riga',
    target: 'redetect_latest'
  });
  assert.throws(() => adminScheduleTicketActionV3Args({ scheduledAtMillis: 0 }), /invalid scheduled time/);
});

test('admin schedule converts an ordinary phone-local minute and rejects a missing DST minute', () => {
  assert.equal(
    ticketActionV3ZonedLocalMillis('2026-07-10', '15:30', 'Europe/Riga'),
    Date.parse('2026-07-10T12:30:00Z')
  );
  assert.throws(
    () => ticketActionV3ZonedLocalMillis('2026-03-29', '03:30', 'Europe/Riga'),
    /laika joslas maiņas/
  );
  assert.equal(
    ticketActionV3ZonedLocalMillis('2026-10-25', '03:30', 'Europe/Riga'),
    Date.parse('2026-10-25T00:30:00Z'),
    'an overlapping phone-local minute must retain the established earliest-occurrence policy'
  );
});

test('slider completion rejects 24%, accepts 25%, and never submits duplicate release events', () => {
  assert.equal(TICKET_LOCAL_REGISTER_SLIDER_COMPLETION_PERCENT, 25);
  const pointerAt24 = { qualified: true, submitted: false };
  assert.equal(shouldSubmitTicketSliderCompletion(pointerAt24, 'up', 2499), false);
  assert.equal(pointerAt24.submitted, false);

  const pointerAt25 = { qualified: true, submitted: false };
  assert.equal(shouldSubmitTicketSliderCompletion(pointerAt25, 'up', 2500), true);
  assert.equal(shouldSubmitTicketSliderCompletion(pointerAt25, 'up', 10000), false);
  assert.equal(pointerAt25.submitted, true);
});

test('slider session binds exact proof, stream epoch, frame, region, viewport, and visual revisions', () => {
  const now = Date.parse('2026-08-25T12:00:00Z');
  const action = {
    actionId: 'proof-1', target: 'prove_current', status: 'succeeded', currentView: 'latest_unactivated',
    streamEpoch: 41, frameSequence: 900, expiresAt: '2026-08-25T12:05:00Z'
  };
  const region = {
    proofActionId: 'proof-1', streamEpoch: 41, frameSequence: 900,
    leftBasisPoints: 1000, topBasisPoints: 7000, rightBasisPoints: 9000, bottomBasisPoints: 7800,
    expiresAt: '2026-08-25T12:05:00Z'
  };
  const stream = { fresh: true, epoch: 41, sequence: 905 };
  const snapshot = ticketLocalRegisterSliderProofSnapshot(action, region, stream, 7, 11, now);
  assert.ok(snapshot);
  assert.equal(ticketLocalRegisterSliderProofMatches(snapshot, action, region, stream, 7, 11, now), true);
  assert.equal(ticketLocalRegisterSliderProofMatches(snapshot, action, region, { ...stream, epoch: 42 }, 7, 11, now), false,
    'a stream restart cancels the session');
  assert.equal(ticketLocalRegisterSliderProofMatches(snapshot, action, { ...region, frameSequence: 901 }, stream, 7, 11, now), false,
    'a replacement proof frame cancels the session');
  assert.equal(ticketLocalRegisterSliderProofMatches(snapshot, action, { ...region, rightBasisPoints: 8999 }, stream, 7, 11, now), false,
    'changed geometry cancels the session');
  assert.equal(ticketLocalRegisterSliderProofMatches(snapshot, action, region, stream, 8, 11, now), false,
    'resize revision cancels the session');
  assert.equal(ticketLocalRegisterSliderProofMatches(snapshot, action, region, stream, 7, 12, now), false,
    'two agreeing significant same-epoch canvas changes cancel before a proof refresh cooldown ends');
  assert.equal(ticketLocalRegisterSliderProofMatches(
    snapshot,
    action,
    region,
    stream,
    7,
    11,
    Date.parse('2026-08-25T12:06:00Z')
  ), false,
    'stale proof cancels the session');
});

test('slider-local direction gate permanently reserves 45-degree movement for scrolling', () => {
  const snapshot = { proofActionId: 'proof-1' };
  const begin = (pointerId) => {
    const state = { inFlight: false, session: null };
    assert.equal(beginTicketLocalRegisterSliderSession(state, {
      kind: 'pointer', pointerId, pointerStartClientX: 100, pointerStartClientY: 200,
      pointerTrackLeftClientX: 0, pointerTrackWidth: 400, snapshot
    }), true);
    return state;
  };

  const right = begin(1);
  assert.equal(updateTicketLocalRegisterSliderPointerDirection(right, {
    pointerId: 1, pointerClientX: 108, pointerClientY: 207.9
  }), 'slider', 'motion just inside 45 degrees stays a slider gesture');

  const exactDiagonal = begin(2);
  assert.equal(updateTicketLocalRegisterSliderPointerDirection(exactDiagonal, {
    pointerId: 2, pointerClientX: 108, pointerClientY: 208
  }), 'scroll', 'exactly 45 degrees is reserved for page scrolling');
  assert.equal(updateTicketLocalRegisterSliderPointerDirection(exactDiagonal, {
    pointerId: 2, pointerClientX: 140, pointerClientY: 200
  }), 'inactive', 'a scroll decision is permanent for the gesture');
  assert.equal(completeTicketLocalRegisterSliderSession(exactDiagonal, {
    pointerId: 2, pointerClientX: 140, pointerClientY: 200, progress: 100, proofMatches: true
  }), null, 'a rejected direction cannot later activate on a horizontal release');

  const vertical = begin(3);
  assert.equal(updateTicketLocalRegisterSliderPointerDirection(vertical, {
    pointerId: 3, pointerClientX: 103, pointerClientY: 209
  }), 'scroll');

  const pending = begin(4);
  assert.equal(updateTicketLocalRegisterSliderPointerDirection(pending, {
    pointerId: 4, pointerClientX: 107, pointerClientY: 200
  }), 'pending', 'sub-8px movement stays neutral');
});

test('pointer completion accepts 8px rightward from any start and keyboard still requires 25%', () => {
  const snapshot = { proofActionId: 'proof-1' };
  for (const [pointerId, startX] of [[10, 0], [11, 250], [12, 500]]) {
    const state = { inFlight: false, session: null };
    assert.equal(beginTicketLocalRegisterSliderSession(state, {
      kind: 'pointer', pointerId, pointerStartClientX: startX, pointerStartClientY: 100,
      pointerTrackLeftClientX: 0, pointerTrackWidth: 500, snapshot
    }), true);
    assert.equal(completeTicketLocalRegisterSliderSession(state, {
      pointerId, pointerClientX: startX + 8, pointerClientY: 100, progress: 0, proofMatches: true
    }), snapshot);
  }

  const insideFortyFiveDegrees = { inFlight: false, session: null };
  assert.equal(beginTicketLocalRegisterSliderSession(insideFortyFiveDegrees, {
    kind: 'pointer', pointerId: 13, pointerStartClientX: 100, pointerStartClientY: 100,
    pointerTrackLeftClientX: 0, pointerTrackWidth: 500, snapshot
  }), true);
  assert.equal(completeTicketLocalRegisterSliderSession(insideFortyFiveDegrees, {
    pointerId: 13, pointerClientX: 108, pointerClientY: 107.9, progress: 0, proofMatches: true
  }), snapshot, 'motion just inside 45 degrees activates on release');

  const upwardInsideFortyFiveDegrees = { inFlight: false, session: null };
  assert.equal(beginTicketLocalRegisterSliderSession(upwardInsideFortyFiveDegrees, {
    kind: 'pointer', pointerId: 14, pointerStartClientX: 100, pointerStartClientY: 100,
    pointerTrackLeftClientX: 0, pointerTrackWidth: 500, snapshot
  }), true);
  assert.equal(completeTicketLocalRegisterSliderSession(upwardInsideFortyFiveDegrees, {
    pointerId: 14, pointerClientX: 108, pointerClientY: 92.1, progress: 0, proofMatches: true
  }), snapshot, 'upward motion just inside 45 degrees also activates on release');

  const keyboardState = { inFlight: false, session: null };
  assert.equal(beginTicketLocalRegisterSliderSession(keyboardState, { kind: 'keyboard', snapshot }), true);
  assert.equal(completeTicketLocalRegisterSliderSession(keyboardState, {
    progress: 24, proofMatches: true
  }), null);
  assert.equal(beginTicketLocalRegisterSliderSession(keyboardState, { kind: 'keyboard', snapshot }), true);
  assert.equal(completeTicketLocalRegisterSliderSession(keyboardState, {
    progress: 25, proofMatches: true
  }), snapshot);
});

test('pointer completion rejects taps, 7px, leftward, and 45-degree motion', () => {
  const snapshot = { proofActionId: 'proof-1' };
  const cases = [
    { pointerId: 20, endX: 100, endY: 100, label: 'tap' },
    { pointerId: 21, endX: 107, endY: 100, label: '7px' },
    { pointerId: 22, endX: 90, endY: 100, label: 'leftward' },
    { pointerId: 23, endX: 108, endY: 108, label: '45-degree' },
    { pointerId: 24, endX: 108, endY: 92, label: 'upward 45-degree' },
    { pointerId: 25, endX: undefined, endY: 100, label: 'missing coordinate' }
  ];
  for (const current of cases) {
    const state = { inFlight: false, session: null };
    assert.equal(beginTicketLocalRegisterSliderSession(state, {
      kind: 'pointer', pointerId: current.pointerId, pointerStartClientX: 100, pointerStartClientY: 100,
      pointerTrackLeftClientX: 0, pointerTrackWidth: 400, snapshot
    }), true);
    assert.equal(completeTicketLocalRegisterSliderSession(state, {
      pointerId: current.pointerId, pointerClientX: current.endX, pointerClientY: current.endY,
      progress: 100, proofMatches: true
    }), null, current.label);
  }

});

test('pointer cancellation and second pointers fail closed', () => {
  const snapshot = { proofActionId: 'proof-1' };
  const state = { inFlight: false, session: null };
  assert.equal(beginTicketLocalRegisterSliderSession(state, {
    kind: 'pointer', pointerId: 40, pointerStartClientX: 100, pointerStartClientY: 100,
    pointerTrackLeftClientX: 0, pointerTrackWidth: 400, snapshot
  }), true);
  assert.equal(beginTicketLocalRegisterSliderSession(state, {
    kind: 'pointer', pointerId: 41, pointerStartClientX: 100, pointerStartClientY: 100,
    pointerTrackLeftClientX: 0, pointerTrackWidth: 400, snapshot
  }), false);
  assert.equal(completeTicketLocalRegisterSliderSession(state, {
    pointerId: 41, pointerClientX: 200, pointerClientY: 100, proofMatches: true
  }), null);
  assert.equal(state.session.pointerId, 40);
  assert.equal(cancelTicketLocalRegisterSliderSession(state, 41), false);
  assert.equal(cancelTicketLocalRegisterSliderSession(state, 40), true);
  assert.equal(state.session, null);
});

test('pointer geometry requires finite x and y coordinates and clamps horizontal start', () => {
  const snapshot = { proofActionId: 'proof-1' };
  for (const input of [
    { pointerId: 50, pointerStartClientX: 0, pointerStartClientY: 0, pointerTrackLeftClientX: 0, pointerTrackWidth: 0 },
    { pointerId: 51, pointerStartClientX: 0, pointerStartClientY: Number.NaN, pointerTrackLeftClientX: 0, pointerTrackWidth: 100 },
    { pointerId: 52, pointerStartClientX: Number.POSITIVE_INFINITY, pointerStartClientY: 0, pointerTrackLeftClientX: 0, pointerTrackWidth: 100 }
  ]) {
    assert.equal(beginTicketLocalRegisterSliderSession({ inFlight: false, session: null }, {
      kind: 'pointer', ...input, snapshot
    }), false);
  }
  const state = { inFlight: false, session: null };
  assert.equal(beginTicketLocalRegisterSliderSession(state, {
    kind: 'pointer', pointerId: 53, pointerStartClientX: -50, pointerStartClientY: 10,
    pointerTrackLeftClientX: 0, pointerTrackWidth: 100, snapshot
  }), true);
  assert.equal(state.session.pointerStartClientX, 0);
  assert.equal(completeTicketLocalRegisterSliderSession(state, {
    pointerId: 53, pointerClientX: 8, pointerClientY: 10, proofMatches: true
  }), snapshot);
});

test('slider change stays idle below the shared 25% threshold', async () => {
  const slider = { value: '24', disabled: false };
  const state = { inFlight: false, session: null, actionId: '', latchedProof: null };
  let submissions = 0;
  const accepted = await handleTicketLocalRegisterSliderChange({
    slider,
    state,
    actionId: 'register-below-threshold',
    proofSnapshot: { proofActionId: 'proof-1' },
    submitRegisterCurrent: async () => {
      submissions += 1;
      return true;
    }
  });
  assert.equal(accepted, false);
  assert.equal(submissions, 0);
  assert.equal(state.inFlight, false);
  assert.equal(state.actionId, '');
  assert.equal(slider.value, '24');
  assert.equal(slider.disabled, false);
});

test('a false reducer outcome is not submitted and releases the browser latch', async () => {
  const slider = { value: '25', disabled: false };
  const state = { inFlight: false, session: null, actionId: '', latchedProof: null };
  let renders = 0;
  const accepted = await handleTicketLocalRegisterSliderChange({
    slider,
    state,
    actionId: 'register-1',
    proofSnapshot: { proofActionId: 'proof-1' },
    submitRegisterCurrent: async () => false,
    render: () => { renders += 1; }
  });
  assert.equal(accepted, false);
  assert.equal(state.inFlight, false);
  assert.equal(state.actionId, '');
  assert.equal(slider.value, '0');
  assert.equal(slider.disabled, false);
  assert.equal(renders, 2);
});

test('accepted slider stays at 100 until its exact durable action is terminal', async () => {
  const slider = { value: '25', disabled: false };
  const state = { inFlight: false, session: null, actionId: '', latchedProof: null };
  const submissions = [];
  const accepted = await handleTicketLocalRegisterSliderChange({
    slider,
    state,
    actionId: 'register-exact',
    proofSnapshot: { proofActionId: 'proof-1' },
    submitRegisterCurrent: async (source, actionId) => {
      submissions.push({ source, actionId });
      return true;
    }
  });
  assert.equal(accepted, true);
  assert.deepEqual(submissions, [{ source: 'browser_slider', actionId: 'register-exact' }]);
  assert.equal(state.inFlight, true);
  assert.equal(state.actionId, 'register-exact');
  assert.equal(slider.value, '100');
  assert.equal(slider.disabled, true);
  assert.equal(await handleTicketLocalRegisterSliderChange({
    slider,
    state,
    actionId: 'register-duplicate',
    proofSnapshot: { proofActionId: 'proof-1' },
    submitRegisterCurrent: async () => {
      submissions.push({ source: 'duplicate', actionId: 'register-duplicate' });
      return true;
    }
  }), false, 'input/change after pointer completion cannot submit a second action');
  assert.deepEqual(submissions, [{ source: 'browser_slider', actionId: 'register-exact' }]);
  assert.deepEqual(releaseTicketLocalRegisterSliderOnTerminal(state, [
    { actionId: 'other', status: 'succeeded' },
    { actionId: 'register-exact', status: 'running' }
  ]), null);
  assert.equal(state.inFlight, true);
  assert.deepEqual(releaseTicketLocalRegisterSliderOnTerminal(state, [
    { actionId: 'register-exact', rootActionId: 'register-exact', status: 'needs_attention', createdAt: '2026-08-26T12:00:00Z' },
    { actionId: 'register-exact-retry-1', rootActionId: 'register-exact', status: 'queued', createdAt: '2026-08-26T12:00:01Z' }
  ]), {
    actionId: 'register-exact', rootActionId: 'register-exact', status: 'needs_attention', createdAt: '2026-08-26T12:00:00Z'
  }, 'only the exact user action controls release; a child row is unrelated');
  assert.equal(state.inFlight, false);
  assert.equal(state.actionId, '');
  assert.equal(resetTicketLocalRegisterSliderState(state), false);
});

test('smart switch labels map to their exact reducer targets', () => {
  assert.deepEqual(ticketActionV3SmartSwitchForView('latest_unactivated'), {
    label: 'Skatīt pēdējo reģistrēto biļeti',
    target: 'show_recent_activated'
  });
  assert.deepEqual(ticketActionV3SmartSwitchForView('recent_activated'), {
    label: 'Atgriezties pie nereģistrētās biļetes',
    target: 'return_to_latest_unactivated'
  });
  assert.deepEqual(ticketActionV3SmartSwitchForView('unknown'), {
    label: 'Skatīt pēdējo reģistrēto biļeti',
    target: ''
  });
});

test('newest automatic activated proof does not hide the database-authorized Return switch', () => {
  const switchProjection = {
    id: 'backend:show',
    actionId: 'show',
    createdAt: '2026-08-26T12:00:00Z',
    target: 'show_recent_activated',
    status: 'succeeded',
    phase: 'complete',
    currentView: 'recent_activated',
    switchAvailable: true,
    switchExpiresAt: '2026-08-26T12:15:00Z'
  };
  const automaticProof = {
    id: 'backend:proof',
    actionId: 'proof',
    createdAt: '2026-08-26T12:01:00Z',
    target: 'prove_current',
    status: 'succeeded',
    phase: 'complete',
    currentView: 'activated_current',
    switchAvailable: false,
    switchExpiresAt: ''
  };
  const actions = [switchProjection, automaticProof];

  assert.equal(ticketActionV3ActionsByAuthority(actions)[0], automaticProof,
    'general status remains grounded in the newest action');
  assert.equal(ticketActionV3SmartSwitchAction(actions, Date.parse('2026-08-26T12:05:00Z')), switchProjection);
  assert.deepEqual(ticketActionV3SmartSwitchForView(
    ticketActionV3SmartSwitchAction(actions, Date.parse('2026-08-26T12:05:00Z')).currentView
  ), {
    label: 'Atgriezties pie nereģistrētās biļetes',
    target: 'return_to_latest_unactivated'
  });
});

test('anchor view flip prefers the newest projection while a stale opposite-view update drains', () => {
  const staleRecentView = {
    id: 'backend:show',
    actionId: 'show',
    createdAt: '2026-08-26T12:00:00Z',
    status: 'succeeded',
    phase: 'complete',
    currentView: 'recent_activated',
    switchAvailable: true,
    switchExpiresAt: '2026-08-26T12:15:00Z'
  };
  const latestUnusedView = {
    id: 'backend:return',
    actionId: 'return',
    createdAt: '2026-08-26T12:02:00Z',
    status: 'succeeded',
    phase: 'complete',
    currentView: 'latest_unactivated',
    switchAvailable: true,
    switchExpiresAt: '2026-08-26T12:15:00Z'
  };

  const selected = ticketActionV3SmartSwitchAction(
    [staleRecentView, latestUnusedView],
    Date.parse('2026-08-26T12:05:00Z')
  );
  assert.equal(selected, latestUnusedView);
  assert.deepEqual(ticketActionV3SmartSwitchForView(selected.currentView), {
    label: 'Skatīt pēdējo reģistrēto biļeti',
    target: 'show_recent_activated'
  });
});

test('expired, unavailable, nonterminal, and incomplete switch rows keep the control disabled', () => {
  const base = {
    status: 'succeeded',
    phase: 'complete',
    currentView: 'recent_activated',
    switchAvailable: true,
    switchExpiresAt: '2026-08-26T12:15:00Z'
  };
  const rows = [
    { ...base, actionId: 'expired', createdAt: '2026-08-26T12:04:00Z', switchExpiresAt: '2026-08-26T12:04:59Z' },
    { ...base, actionId: 'unavailable', createdAt: '2026-08-26T12:03:00Z', switchAvailable: false },
    { ...base, actionId: 'running', createdAt: '2026-08-26T12:02:00Z', status: 'running' },
    { ...base, actionId: 'incomplete', createdAt: '2026-08-26T12:01:00Z', phase: 'finalizing' },
    { ...base, actionId: 'bad-expiry', createdAt: '2026-08-26T12:00:00Z', switchExpiresAt: 'not-a-time' }
  ];

  assert.equal(ticketActionV3SmartSwitchAction(rows, Date.parse('2026-08-26T12:05:00Z')), null);
});

test('browser phone-lane busy state includes every queued, pending, or running v3 target', () => {
  for (const target of [
    'open_latest_unactivated',
    'open_latest_and_register',
    'register_current',
    'show_recent_activated',
    'return_to_latest_unactivated',
    'redetect_latest',
    'prove_current'
  ]) {
    assert.equal(ticketActionV3OccupiesPhone({ target, status: 'queued' }), true);
    assert.equal(ticketActionV3OccupiesPhone({ target, status: 'pending' }), true);
    assert.equal(ticketActionV3OccupiesPhone({ target, status: 'running' }), true);
    assert.equal(ticketActionV3OccupiesPhone({ target, status: 'succeeded' }), false);
  }
});

test('automatic current proof cannot replace the last explicit user action result', () => {
  const explicitFailure = {
    actionId: 'ticket_action_v3_user_open',
    target: 'open_latest_unactivated',
    status: 'failed',
    reason: 'ticket_action_visual_unproved'
  };
  const laterAutomaticProof = {
    actionId: 'ticket_action_v3_auto_proof',
    target: 'prove_current',
    status: 'succeeded',
    currentView: 'latest_unactivated'
  };

  assert.equal(
    ticketActionV3ExplicitResultForDisplay(
      [laterAutomaticProof, explicitFailure],
      explicitFailure.actionId
    ),
    explicitFailure,
    'the exact explicit failure remains the user-facing result even when prove_current is newer'
  );
  assert.equal(
    ticketActionV3ExplicitResultForDisplay(
      [laterAutomaticProof],
      explicitFailure.actionId,
      explicitFailure
    ),
    explicitFailure,
    'the remembered terminal result survives a later snapshot that no longer contains its row'
  );
  assert.equal(
    ticketActionV3ExplicitResultForDisplay([laterAutomaticProof], ''),
    null,
    'an automatic proof is never inferred to be an explicit user result'
  );
  const retryChild = {
    actionId: `${explicitFailure.actionId}-retry-1`,
    rootActionId: explicitFailure.actionId,
    status: 'running',
    createdAt: '2026-08-26T12:00:01Z'
  };
  assert.deepEqual(
    ticketActionV3ExplicitResultForDisplay(
      [{ ...explicitFailure, createdAt: '2026-08-26T12:00:00Z' }, retryChild],
      explicitFailure.actionId
    ),
    { ...explicitFailure, createdAt: '2026-08-26T12:00:00Z' },
    'the browser never follows a child row in place of the exact user action'
  );
});

test('activation attention explains whether a swipe was avoided, unchanged, or ambiguous', () => {
  const base = { target: 'register_current', status: 'needs_attention' };
  assert.match(ticketActionV3ActivationTerminalMessage({ ...base, phase: 'not_dispatched' }), /nekas netika pavilkts/);
  assert.match(ticketActionV3ActivationTerminalMessage({ ...base, phase: 'retry_not_dispatched' }), /otrā vilkšana netika nosūtīta/);
  assert.match(ticketActionV3ActivationTerminalMessage({ ...base, phase: 'no_transition' }), /Abi atļautie vilkšanas mēģinājumi/);
  assert.match(ticketActionV3ActivationTerminalMessage({ ...base, phase: 'outcome_unknown' }), /Pirms jauna mēģinājuma/);
  assert.equal(ticketActionV3ActivationTerminalMessage({ ...base, phase: 'running' }), '');
  assert.equal(ticketActionV3ActivationTerminalMessage({ ...base, target: 'redetect_latest', phase: 'not_dispatched' }), '');
});

test('only the exact terminal empty redetect result is treated as an expected no-ticket outcome', () => {
  const emptyRedetect = {
    actionId: 'ticket_action_v3_empty',
    target: 'redetect_latest',
    status: 'failed',
    phase: 'failed',
    currentView: 'unknown',
    reason: 'ticket_action_latest_not_detected',
    streamEpoch: '41',
    frameSequence: '73'
  };
  assert.equal(ticketActionV3IsExpectedEmptyRedetect(emptyRedetect), true);
  assert.equal(ticketActionV3IsExpectedEmptyRedetect({ ...emptyRedetect, status: 'needs_attention' }), false);
  assert.equal(ticketActionV3IsExpectedEmptyRedetect({ ...emptyRedetect, status: 'running' }), false);
  assert.equal(ticketActionV3IsExpectedEmptyRedetect({ ...emptyRedetect, phase: 'running' }), false);
  assert.equal(ticketActionV3IsExpectedEmptyRedetect({ ...emptyRedetect, phase: undefined }), false);
  assert.equal(ticketActionV3IsExpectedEmptyRedetect({ ...emptyRedetect, target: 'open_latest_unactivated' }), false);
  assert.equal(ticketActionV3IsExpectedEmptyRedetect({ ...emptyRedetect, currentView: 'latest_unactivated' }), false);
  assert.equal(ticketActionV3IsExpectedEmptyRedetect({ ...emptyRedetect, streamEpoch: '0' }), false);
  assert.equal(ticketActionV3IsExpectedEmptyRedetect({ ...emptyRedetect, streamEpoch: '' }), false);
  assert.equal(ticketActionV3IsExpectedEmptyRedetect({ ...emptyRedetect, frameSequence: '0' }), false);
  assert.equal(ticketActionV3IsExpectedEmptyRedetect({ ...emptyRedetect, frameSequence: undefined }), false);
  assert.equal(ticketActionV3IsExpectedEmptyRedetect({ ...emptyRedetect, reason: 'ticket_action_visual_unproved' }), false);
  assert.equal(ticketActionV3IsExpectedEmptyRedetect({ ...emptyRedetect, reason: 'ticket_action_latest_not_detected_extra' }), false);
  assert.equal(ticketActionV3IsExpectedEmptyRedetect(null), false);
});

test('admin redetection terminal copy distinguishes empty, success, attention, and safe failure', () => {
  const base = {
    actionId: 'ticket_action_v3_admin_exact',
    target: 'redetect_latest',
    status: 'failed',
    phase: 'failed',
    currentView: 'unknown',
    reason: 'ticket_action_latest_not_detected',
    streamEpoch: '41',
    frameSequence: '73'
  };
  assert.equal(adminRedetectTicketActionV3TerminalMessage(base), 'No tickets found.');
  assert.equal(
    adminRedetectTicketActionV3TerminalMessage({ ...base, status: 'succeeded', reason: 'ticket_action_latest_redetected' }),
    'Latest ticket redetection completed.'
  );
  assert.equal(
    adminRedetectTicketActionV3TerminalMessage({ ...base, status: 'needs_attention' }),
    'The phone view needs manual attention; the action was not repeated.'
  );
  assert.equal(
    adminRedetectTicketActionV3TerminalMessage({ ...base, streamEpoch: '0' }),
    'Ticket redetection stopped safely without an unproven action.'
  );
  assert.equal(adminRedetectTicketActionV3TerminalMessage({ ...base, status: 'running' }), '');
  assert.equal(adminRedetectTicketActionV3TerminalMessage({ ...base, target: 'prove_current' }), '');
});

test('local v3 request remains latched after reducer acknowledgement until its exact row arrives', () => {
  const state = { actionId: '', reducerSettled: false, observed: false };
  assert.equal(beginTicketActionV3LocalRequest(state, 'ticket_action_v3_exact'), true);
  assert.equal(ticketActionV3LocalRequestBusy(state), true);

  assert.equal(observeTicketActionV3LocalRequest(state, {
    actionId: 'ticket_action_v3_old',
    status: 'succeeded'
  }), false);
  assert.equal(settleTicketActionV3LocalRequest(state, true), false);
  assert.equal(ticketActionV3LocalRequestBusy(state), true);
  assert.equal(beginTicketActionV3LocalRequest(state, 'ticket_action_v3_duplicate'), false);

  assert.equal(observeTicketActionV3LocalRequest(state, {
    actionId: 'ticket_action_v3_exact',
    status: 'pending'
  }), true);
  assert.equal(ticketActionV3LocalRequestBusy(state), false);
});

test('rejected local v3 request releases its latch without an authoritative row', () => {
  const state = { actionId: '', reducerSettled: false, observed: false };
  assert.equal(beginTicketActionV3LocalRequest(state, 'ticket_action_v3_rejected'), true);
  assert.equal(settleTicketActionV3LocalRequest(state, false), true);
  assert.equal(ticketActionV3LocalRequestBusy(state), false);
});

test('local browser dispatch and acknowledgement p95 stays below 250ms', async () => {
  const samples = [];
  let acknowledgements = 0;
  for (let index = 0; index < 200; index += 1) {
    const started = performance.now();
    await dispatchTicketActionV3ForLocalGate(
      async () => Promise.resolve(),
      { actionId: `action-${index}`, target: 'open_latest_unactivated', source: 'browser_button', reason: 'micro_gate' },
      () => { acknowledgements += 1; }
    );
    samples.push(performance.now() - started);
  }
  samples.sort((left, right) => left - right);
  const p95 = samples[Math.floor(samples.length * 0.95)];
  console.log(`local dispatch/ack p95=${p95.toFixed(3)}ms`);
  assert.equal(acknowledgements, 200);
  assert.ok(p95 < 250, `local dispatch/ack p95 ${p95.toFixed(2)}ms exceeded 250ms`);
});
