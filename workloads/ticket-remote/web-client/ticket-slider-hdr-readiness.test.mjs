// Regression: real controller and actual page gate, no browser,
// network, phone action, pixels, durable requests, or private identifiers.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import {
  ClientHDRController, CLIENT_HDR_ENGINE
} from './client-hdr-core.mjs';

const root = new URL('.', import.meta.url);
const controllerTests = readFileSync(new URL('client-hdr-core.test.mjs', root), 'utf8');
const helperSource = controllerTests.slice(controllerTests.indexOf('function deferred()'),
  controllerTests.indexOf("test('v2 engine"));
assert.ok(helperSource.includes('function harness('), 'use the existing real-controller test harness');
const { harness, deferred, tick, fakeFrame } = new Function('ClientHDRController',
  `${helperSource}\nreturn { harness, deferred, tick, fakeFrame };`)(ClientHDRController);
const pageSource = readFileSync(new URL('ticket-app-source.js', root), 'utf8');
const gateStart = pageSource.indexOf('  function clientHDRConsequentialControlProofReady()');
const gateEnd = pageSource.indexOf('  function ticketActionV3RequiresFreshRenderedFrame(', gateStart);
assert.ok(gateStart >= 0 && gateEnd > gateStart, 'exercise the actual page readiness and admission functions');
const makePageGate = new Function('experimentalClientHDRController', 'experimentalMediaState',
  'CLIENT_HDR_ENGINE', 'streamHasFreshRenderedFrame', 'ticketActionV3StreamSnapshot',
  `${pageSource.slice(gateStart, gateEnd)}\nreturn {
    ready: clientHDRConsequentialControlProofReady,
    admit: revealAuthoritativeSDRForConsequentialControl
  };`);

test('a new SDR picture awaiting HDR present completion cannot invite a control or hide HDR', async t => {
  const completion = deferred();
  const state = harness({ autoRender: true, presentCompletionGates: [null, null, completion.promise] });
  const closed = [];
  let sdrSequence = 20;
  const gate = makePageGate(state.controller, { enabled: true, engine: CLIENT_HDR_ENGINE },
    CLIENT_HDR_ENGINE, () => true, () => ({ epoch: 4, sequence: sdrSequence }));
  try {
    state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
    await tick();
    state.controller.noteSDRFrame({ epoch: 4, sequence: 20, presentationOrdinal: 20,
      visualAgeMillis: 700, renderedAt: 100 });
    state.controller.offerFrame(fakeFrame('first', closed), { epoch: 4, sequence: 20,
      presentationOrdinal: 20, visualAgeMillis: 700, offeredAt: 100 });
    await tick();
    assert.equal(gate.ready(), true);
    assert.equal(gate.admit(), true);

    state.controller.offerFrame(fakeFrame('second', closed), { epoch: 4, sequence: 21,
      presentationOrdinal: 21, visualAgeMillis: 700, offeredAt: 100 }, {
      commitSDR: (_frame, candidate) => {
        sdrSequence = 21;
        return { ...candidate, renderedAt: 100 };
      }
    });
    await tick();
    const before = state.controller.snapshot();
    assert.equal(sdrSequence, 21);
    assert.equal(before.sequence, 20, 'the previous presentation remains current until completion');
    assert.equal(before.proofFresh, true, 'this is the observed old-presentation readiness trap');
    const ready = gate.ready();
    const admitted = gate.admit();
    const after = state.controller.snapshot();
    t.diagnostic(JSON.stringify({ pendingPresentation: true, ready, admitted,
      surfaceRemainsVisible: after.surfaceVisible,
      addedSurfaceTransitions: after.surfaceTransitions - before.surfaceTransitions }));
    assert.equal(ready, false, 'the new SDR identity has no matching completed HDR proof yet');
    assert.equal(admitted, false, 'an attempted control must remain local');
    assert.equal(after.surfaceVisible, true, 'checking readiness must not reveal SDR');
    assert.equal(after.surfaceTransitions, before.surfaceTransitions, 'no fallback blink');

    completion.resolve();
    await tick();
    await tick();
    assert.equal(state.controller.snapshot().sequence, 21);
    assert.equal(state.controller.snapshot().proofFresh, true);
    assert.equal(gate.ready(), true, 'matching completed fresh proof restores readiness');
    assert.equal(gate.admit(), true);
  } finally {
    completion.resolve();
    await tick();
    state.controller.dispose('evidence_complete');
  }
});
