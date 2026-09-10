import test from 'node:test';
import assert from 'node:assert/strict';
import { ColdRestartPage } from './cold-restart.mjs';

function page(openedAt = 1000) {
  const events = [];
  let hidden = false, remembered = '';
  const controller = new ColdRestartPage({ openedAt, hidden: () => hidden,
    pause: () => events.push('pause'), restart: () => events.push('restart'),
    recall: () => remembered, remember: id => { remembered = id; } });
  return { controller, events, hide: value => { hidden = value; } };
}
const row = phase => ({ coldRestartId: 'operation-a', coldRestartPhase: phase,
  coldRestartStartedAt: new Date(2000).toISOString() });

test('active page pauses once and restarts only after both cold proofs', () => {
  const { controller: c, events } = page();
  for (const phase of ['quiescing','stopping','stopping','confirmed']) c.update(row(phase));
  assert.deepEqual(events, ['pause']);
  assert.equal(c.blocked,true);
  for (const phase of ['restarting','restarting','live']) c.update(row(phase));
  assert.deepEqual(events,['pause','restart']);
});
test('hidden page waits for visibility, including one that missed the stop updates', () => {
  const { controller:c, events, hide } = page();
  hide(true);
  c.update(row('asleep'));
  assert.deepEqual(events,[]);
  hide(false);
  assert.equal(c.resume(),true);
  assert.equal(c.resume(),false);
  assert.deepEqual(events,['restart']);
});
test('failed shutdown stays paused and never invents a cold completion', () => {
  const { controller:c, events } = page();
  c.update(row('stopping'));
  c.update(row('failed'));
  assert.equal(c.resume(),false);
  assert.equal(c.blocked,true);
  assert.deepEqual(events,['pause']);
});
test('a new navigation does not restart for an older completed operation', () => {
  const { controller:c, events } = page(3000);
  c.update(row('live'));
  assert.deepEqual(events,[]);
});
