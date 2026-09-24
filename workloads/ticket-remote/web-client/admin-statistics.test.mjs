import test from 'node:test';
import assert from 'node:assert/strict';
import { buildActivityStatisticsModel, activityDayKeys } from './admin-statistics-source.js';

const hourly = (hour, count) => Array.from({ length: 24 }, (_, index) => index === hour ? count : 0);
const member = { accountScopeId: 'scope-a', publicId: 'AB12', email: 'member@example.test', active: true };
const base = { serverTime: '2026-09-09T21:10:00Z', actionStatisticsStartedAt: '2026-09-09T10:30:00Z', members: [member] };
const actions = (overrides = {}) => ({ accountScopeId: member.accountScopeId, day: '2026-09-09',
  registrationAttempts: hourly(23, 2), registrationSuccesses: hourly(23, 1),
  controlCodeAttempts: hourly(23, 3), controlCodeSuccesses: hourly(23, 3), ...overrides });

test('viewing and action entries share accurate user, day and window totals', () => {
  const model = buildActivityStatisticsModel({ ...base,
    pageActivityDaily: [{ accountScopeId: member.accountScopeId, day: '2026-09-09', hourlyTicks: hourly(23, 51) }],
    actionActivityDaily: [actions()] });
  const day = model.activeDays[0];
  const entry = day.activeHours[0].entries[0];
  assert.equal(entry.duration, '4m 15s');
  for (const item of [entry, day, model]) {
    assert.equal(item.registrationAttempts, 2);
    assert.equal(item.registrationSuccesses, 1);
    assert.equal(item.controlCodeAttempts, 3);
    assert.equal(item.controlCodeSuccesses, 3);
    assert.deepEqual(item.metrics.map(x => x.text), ['1/2', '3/3']);
  }
  assert.equal(model.activeUserCount, 1);
  assert.equal(model.totalSeconds, 255);
  assert.equal(day.hours[23].totalSeconds, 255);
  assert.equal(day.maxHourSeconds, 255);
  assert.equal(entry.email, 'member@example.test');
  assert.equal('shortId' in entry, false);
  assert.equal('legend' in model, false);
  assert.equal(model.trackingStartLabel, 'Action counts since 2026-09-09 at 13:30 (Europe/Riga).');
});

test('action-only hours without a member remain visible without invented identity or zero-duration noise', () => {
  const model = buildActivityStatisticsModel({ ...base, members: [], actionActivityDaily: [actions()] });
  assert.equal(model.hasActiveActivity, true);
  assert.equal(model.activeUserCount, 1);
  assert.equal(model.activeDays[0].totalDuration, '');
  assert.equal(model.activeDays[0].maxHourSeconds, 0);
  const entry = model.activeDays[0].activeHours[0].entries[0];
  assert.equal(entry.duration, '');
  assert.equal(entry.email, '');
  assert.equal(entry.active, false);
});

test('inactive members retain their known email', () => {
  const model = buildActivityStatisticsModel({ ...base, members: [{ ...member, active: false }], actionActivityDaily: [actions()] });
  const entry = model.activeDays[0].activeHours[0].entries[0];
  assert.equal(entry.email, 'member@example.test');
  assert.equal(entry.active, false);
});

test('viewing-only entries and empty periods carry no action metrics', () => {
  const empty = buildActivityStatisticsModel(base);
  assert.equal(empty.hasActiveActivity, false);
  assert.deepEqual(empty.metrics, []);
  const model = buildActivityStatisticsModel({ ...base,
    pageActivityDaily: [{ accountScopeId: member.accountScopeId, day: '2026-09-09', hourlyTicks: hourly(5, 1) }] });
  assert.deepEqual(model.activeDays[0].activeHours[0].entries[0].metrics, []);
  assert.equal(model.totalSeconds, 5);
  assert.equal(buildActivityStatisticsModel({}).trackingStartLabel, 'Action tracking has not started yet.');
});

test('each day scales to its own busiest hour, including tied peaks', () => {
  const todayTicks = hourly(8, 12);
  todayTicks[10] = 12;
  const model = buildActivityStatisticsModel({ ...base, pageActivityDaily: [
    { accountScopeId: member.accountScopeId, day: '2026-09-09', hourlyTicks: todayTicks },
    { accountScopeId: member.accountScopeId, day: '2026-09-08', hourlyTicks: hourly(9, 2) }
  ] });
  assert.equal(model.activeDays[0].maxHourSeconds, 60);
  assert.equal(model.activeDays[0].hours[8].totalSeconds, 60);
  assert.equal(model.activeDays[0].hours[10].totalSeconds, 60);
  assert.equal(model.activeDays[1].maxHourSeconds, 10);
  assert.equal(model.activeDays[1].hours[9].totalSeconds, 10);
});

test('only the 30 visible Riga days contribute and old successes stay in their original hour', () => {
  const model = buildActivityStatisticsModel({ ...base, actionActivityDaily: [actions(),
    actions({ day: '2026-08-11' }), actions({ day: '2026-08-10' }), actions({ day: '2026-09-11' })] });
  assert.equal(model.days[0].day, '2026-09-10');
  assert.equal(model.days.at(-1).day, '2026-08-12');
  assert.equal(model.registrationAttempts, 2);
  assert.equal(model.days[0].registrationAttempts, 0);
  assert.equal(model.activeDays[0].activeHours[0].hour, 23);
  assert.equal(activityDayKeys('2026-03-29T21:30:00Z')[0], '2026-03-30');
  assert.equal(activityDayKeys('2026-10-25T22:30:00Z')[0], '2026-10-26');
});

test('counts are bounded, combined by account, and never imply success without an attempt', () => {
  const model = buildActivityStatisticsModel({ ...base, actionActivityDaily: [actions({
    registrationAttempts: hourly(23, 0), registrationSuccesses: hourly(23, 9),
    controlCodeAttempts: hourly(23, 4294967295), controlCodeSuccesses: hourly(23, 2) }),
    actions({ registrationAttempts: hourly(23, -1), registrationSuccesses: ['bad'],
      controlCodeAttempts: hourly(23, 1), controlCodeSuccesses: hourly(23, 1) })] });
  assert.equal(model.registrationAttempts, 0);
  assert.equal(model.registrationSuccesses, 0);
  assert.equal(model.controlCodeAttempts, 4294967296);
  assert.equal(model.controlCodeSuccesses, 3);
  assert.equal(model.activeDays[0].activeHours[0].entries.length, 1);
  assert.equal(model.metrics[0].description, 'Control-code generation: 3 successful, 4294967296 accepted requests');
});
