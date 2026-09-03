import test from 'node:test';
import assert from 'node:assert/strict';

import {
  activityDayKeys,
  buildActivityStatisticsModel,
  formatActivityDuration
} from './admin-statistics-source.js';

function hourlyTicks(values) {
  return Array.from({ length: 24 }, (_, hour) => values[hour] || 0);
}

test('activity day keys use Riga calendar dates across daylight-saving changes', () => {
  assert.deepEqual(
    activityDayKeys('2026-10-25T12:00:00Z', 'Europe/Riga', 4),
    ['2026-10-25', '2026-10-24', '2026-10-23', '2026-10-22']
  );
  assert.deepEqual(
    activityDayKeys('2026-03-29T12:00:00Z', 'Europe/Riga', 3),
    ['2026-03-29', '2026-03-28', '2026-03-27']
  );
});

test('activity duration formatting keeps exact five-second precision', () => {
  assert.equal(formatActivityDuration(0), '0s');
  assert.equal(formatActivityDuration(5), '5s');
  assert.equal(formatActivityDuration(125), '2m 5s');
  assert.equal(formatActivityDuration(3725), '1h 2m 5s');
});

test('statistics model combines rows and retains deactivated-user history with an inactive label', () => {
  const model = buildActivityStatisticsModel({
    serverTime: '2026-09-02T12:00:00Z',
    timeZone: 'Europe/Riga',
    days: 30,
    secondsPerTick: 5,
    members: [
      { accountScopeId: 'scope-active', publicId: 'A1B2', email: 'active@example.test', active: true },
      { accountScopeId: 'scope-inactive', publicId: 'C3D4', email: 'inactive@example.test', active: false },
      { accountScopeId: 'scope-idle', publicId: 'E5F6', email: 'idle@example.test', active: true }
    ],
    pageActivityDaily: [
      { accountScopeId: 'scope-active', day: '2026-09-02', hourlyTicks: hourlyTicks({ 0: 2, 1: 12 }) },
      { accountScopeId: 'SCOPE-ACTIVE', day: '2026-09-02', hourlyTicks: hourlyTicks({ 0: 1 }) },
      { accountScopeId: 'scope-inactive', day: '2026-09-02', hourlyTicks: hourlyTicks({ 0: 6 }) },
      { accountScopeId: 'scope-unknown', day: '2026-09-02', hourlyTicks: hourlyTicks({ 0: 100 }) },
      { accountScopeId: 'scope-active', day: '2026-07-01', hourlyTicks: hourlyTicks({ 0: 100 }) }
    ]
  });

  assert.equal(model.days.length, 30);
  assert.equal(model.days[0].day, '2026-09-02');
  assert.equal(model.days[0].hours.length, 24);
  assert.deepEqual(model.days[0].hours[0].entries, [
    {
      accountScopeId: 'scope-active',
      shortId: 'A1B2',
      email: 'active@example.test',
      active: true,
      ticks: 3,
      seconds: 15,
      duration: '15s'
    },
    {
      accountScopeId: 'scope-inactive',
      shortId: 'C3D4',
      email: 'inactive@example.test',
      active: false,
      ticks: 6,
      seconds: 30,
      duration: '30s'
    }
  ]);
  assert.equal(model.days[0].hours[1].entries[0].duration, '1m');
  assert.equal(model.activeDays.length, 1);
  assert.equal(model.activeDays[0].displayLabel, 'Today · 2026-09-02');
  assert.deepEqual(model.activeDays[0].activeHours.map(({ hour }) => hour), [0, 1]);
  assert.equal(model.activeDays[0].totalSeconds, 105);
  assert.equal(model.activeDays[0].totalDuration, '1m 45s');
  assert.equal(model.totalSeconds, 105);
  assert.equal(model.totalDuration, '1m 45s');
  assert.equal(model.activeUserCount, 2);
  assert.equal(model.hasActiveActivity, true);
  assert.deepEqual(model.legend.map(({ shortId, email, active }) => ({ shortId, email, active })), [
    { shortId: 'A1B2', email: 'active@example.test', active: true },
    { shortId: 'C3D4', email: 'inactive@example.test', active: false }
  ]);
});

test('inactive-only retained activity remains visible and labelled', () => {
  const model = buildActivityStatisticsModel({
    serverTime: '2026-09-02T12:00:00Z',
    members: [
      { accountScopeId: 'scope-inactive', publicId: 'C3D4', email: 'inactive@example.test', active: false }
    ],
    pageActivityDaily: [
      { accountScopeId: 'scope-inactive', day: '2026-09-02', hourlyTicks: hourlyTicks({ 9: 3 }) }
    ]
  });

  assert.equal(model.hasActiveActivity, true);
  assert.equal(model.activeUserCount, 1);
  assert.equal(model.totalSeconds, 15);
  assert.equal(model.days[0].hours[9].entries.length, 1);
  assert.equal(model.days[0].hours[9].entries[0].active, false);
  assert.equal(model.legend.length, 1);
  assert.equal(model.legend[0].active, false);
});

test('compact activity days omit empty dates and hours while preserving relative labels and daily totals', () => {
  const model = buildActivityStatisticsModel({
    serverTime: '2026-09-02T12:00:00Z',
    timeZone: 'Europe/Riga',
    days: 30,
    secondsPerTick: 5,
    members: [
      { accountScopeId: 'scope-active', publicId: 'A1B2', email: 'active@example.test', active: true },
      { accountScopeId: 'scope-inactive', publicId: 'C3D4', email: 'inactive@example.test', active: false }
    ],
    pageActivityDaily: [
      { accountScopeId: 'scope-active', day: '2026-09-02', hourlyTicks: hourlyTicks({ 1: 2 }) },
      { accountScopeId: 'scope-inactive', day: '2026-09-02', hourlyTicks: hourlyTicks({ 1: 1 }) },
      { accountScopeId: 'scope-active', day: '2026-09-01', hourlyTicks: hourlyTicks({ 8: 12 }) },
      { accountScopeId: 'scope-inactive', day: '2026-08-30', hourlyTicks: hourlyTicks({ 23: 3 }) }
    ]
  });

  assert.deepEqual(model.activeDays.map(({ day }) => day), [
    '2026-09-02',
    '2026-09-01',
    '2026-08-30'
  ]);
  assert.deepEqual(model.activeDays.map(({ displayLabel }) => displayLabel), [
    'Today · 2026-09-02',
    'Yesterday · 2026-09-01',
    '2026-08-30'
  ]);
  assert.deepEqual(model.activeDays.map(({ totalDuration }) => totalDuration), ['15s', '1m', '15s']);
  assert.deepEqual(model.activeDays[0].activeHours.map(({ hour }) => hour), [1]);
  assert.deepEqual(model.activeDays[0].activeHours[0].entries.map(({ shortId, active }) => ({ shortId, active })), [
    { shortId: 'A1B2', active: true },
    { shortId: 'C3D4', active: false }
  ]);
  assert.equal(model.days.find((day) => day.day === '2026-08-31').activeHours.length, 0);
});

test('compact activity days are empty when the retained window has no measured use', () => {
  const model = buildActivityStatisticsModel({
    serverTime: '2026-09-02T12:00:00Z',
    members: [
      { accountScopeId: 'scope-idle', publicId: 'E5F6', email: 'idle@example.test', active: true }
    ]
  });

  assert.equal(model.hasActiveActivity, false);
  assert.deepEqual(model.activeDays, []);
});
