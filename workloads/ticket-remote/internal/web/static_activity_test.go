package web

import (
	"strings"
	"testing"
)

func TestTicketSpacetimeClientRecordsActivityWithTicketIDOnly(t *testing.T) {
	source := readTicketWebClientSource(t, "src/index.ts")
	method := substringBetween(t, source,
		"  recordActivityTick(): Promise<void> {",
		"  setLimitPreference(obeyLimits: boolean): Promise<void> {")
	for _, required := range []string{
		`return this.callReducer("memberRecordActivityTick", {`,
		"ticketId: this.cfg.ticketId,",
	} {
		if !strings.Contains(method, required) {
			t.Fatalf("activity tick reducer call is missing %q", required)
		}
	}
	for _, forbidden := range []string{"backendId", "sessionId", "email", "accountScopeId"} {
		if strings.Contains(method, forbidden) {
			t.Fatalf("activity tick must derive identity server-side, found browser field %q", forbidden)
		}
	}

	buildSource := readTicketWebClientSource(t, "build.mjs")
	if !strings.Contains(buildSource, `"record_activity_tick"`) {
		t.Fatal("browser binding pruning must retain the activity tick reducer")
	}
}

func TestTicketActivityTickUsesOneQuietForegroundQualifyingTimer(t *testing.T) {
	source := ticketAppSource(t)
	functions := substringBetween(t, source,
		"  function userActivityTickEligible() {",
		"  function publishStreamFocus(active, reason) {")

	for _, required := range []string{
		"const activityTickIntervalMs = 5000;",
		"const activityTickMaximumDelayMs = 1000;",
		"document.visibilityState === 'visible'",
		"!idleDisconnected",
		"window.navigator.onLine !== false",
		"spacetimeClientStatus === 'live'",
		"activityTickInFlight = true;",
		"await client.recordActivityTick();",
		"catch (_) {",
		"activityTickInFlight = false;",
		"const elapsed = Date.now() - scheduledAt;",
		"void recordUserActivityTick();",
		"window.addEventListener('offline', () => {",
		"window.addEventListener('pagehide', (event) => {",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("activity heartbeat is missing %q", required)
		}
	}
	if strings.Count(source, "}, activityTickIntervalMs);") != 1 {
		t.Fatal("the main Ticket page must install exactly one five-second activity timer")
	}
	if strings.Count(source, "recordUserActivityTick()") != 2 {
		t.Fatal("activity recording must occur only in its function declaration and five-second interval")
	}
	for _, forbidden := range []string{
		"recordUserActivityTick();\n  setInterval",
		"setInterval(() => {\n    void recordUserActivityTick();",
		"pagehide_activity",
		"unload_activity",
		"activity_tick_failed",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("activity ticks must not be immediate, lifecycle-flushed, or logged, found %q", forbidden)
		}
	}

	runTicketJavaScript(t, `
const document = { visibilityState: 'visible' };
const window = { navigator: { onLine: true } };
let idleDisconnected = false;
let spacetimeClientStatus = 'live';
let activityTickInFlight = false;
let activityTickTimer = null;
const activityTickIntervalMs = 5000;
const activityTickMaximumDelayMs = 1000;
let calls = 0;
let releaseTick = null;
let rejectNext = false;
let spacetimeClient = {
  recordActivityTick() {
    calls += 1;
    if (rejectNext) {
      rejectNext = false;
      return Promise.reject(new Error('offline'));
    }
    return new Promise((resolve) => { releaseTick = resolve; });
  }
};
function check(value, message) { if (!value) throw new Error(message); }
`+functions+`
(async () => {
  const first = recordUserActivityTick();
  await Promise.resolve();
  check(calls === 1, 'first eligible interval must submit one tick');
  check(await recordUserActivityTick() === false && calls === 1,
    'an in-flight tick must suppress overlapping intervals');
  releaseTick();
  check(await first === true, 'the completed eligible tick must succeed');

  document.visibilityState = 'hidden';
  check(await recordUserActivityTick() === false && calls === 1, 'hidden pages must not tick');
  document.visibilityState = 'visible';
  window.navigator.onLine = false;
  check(await recordUserActivityTick() === false && calls === 1, 'offline pages must not tick');
  window.navigator.onLine = true;
  idleDisconnected = true;
  check(await recordUserActivityTick() === false && calls === 1, 'idle-disconnected pages must not tick');
  idleDisconnected = false;
  spacetimeClientStatus = 'reconnecting';
  check(await recordUserActivityTick() === false && calls === 1, 'reconnecting clients must not tick');
  spacetimeClientStatus = 'live';

  rejectNext = true;
  check(await recordUserActivityTick() === false && calls === 2, 'failed ticks must be dropped');
  const afterFailure = recordUserActivityTick();
  await Promise.resolve();
  check(calls === 3, 'a dropped tick must release the in-flight gate for the next interval');
  releaseTick();
  check(await afterFailure === true, 'the later interval must remain usable');
})().catch((error) => { console.error(error); process.exitCode = 1; });
`)

	runTicketJavaScript(t, `
const document = { visibilityState: 'visible' };
const window = { navigator: { onLine: true } };
let idleDisconnected = false;
let spacetimeClientStatus = 'live';
let activityTickInFlight = false;
let activityTickTimer = null;
const activityTickIntervalMs = 5000;
const activityTickMaximumDelayMs = 1000;
let calls = 0;
let now = 100000;
let nextTimerID = 1;
const timers = new Map();
const Date = { now: () => now };
function setTimeout(callback, delay) {
  const id = nextTimerID++;
  timers.set(id, { callback, dueAt: now + delay });
  return id;
}
function clearTimeout(id) { timers.delete(id); }
async function advance(milliseconds) {
  now += milliseconds;
  while (true) {
    const due = [...timers.entries()]
      .filter(([, timer]) => timer.dueAt <= now)
      .sort((left, right) => left[1].dueAt - right[1].dueAt)[0];
    if (!due) return;
    timers.delete(due[0]);
    await due[1].callback();
  }
}
const spacetimeClient = {
  recordActivityTick() {
    calls += 1;
    return Promise.resolve();
  }
};
function check(value, message) { if (!value) throw new Error(message); }
`+functions+`
(async () => {
  check(refreshUserActivityTickSchedule() === true, 'eligible page must schedule a qualifying window');
  check(calls === 0, 'opening must not send an immediate tick');
  await advance(4999);
  check(calls === 0, 'less than five visible seconds must not tick');
  await advance(1);
  check(calls === 1, 'five complete visible seconds must send one tick');

  document.visibilityState = 'hidden';
  refreshUserActivityTickSchedule();
  await advance(60000);
  check(calls === 1, 'hidden time must not tick');
  document.visibilityState = 'visible';
  refreshUserActivityTickSchedule();
  await advance(4999);
  check(calls === 1, 'resume must require a new complete five-second window');
  await advance(1);
  check(calls === 2, 'visible time may resume after a complete window');

  await advance(20000);
  check(calls === 2, 'a suspended late timer must be dropped without catch-up');
  await advance(5000);
  check(calls === 3, 'a fresh window after suspension may tick normally');

  window.navigator.onLine = false;
  refreshUserActivityTickSchedule();
  await advance(10000);
  check(calls === 3, 'offline time must not tick');
  window.navigator.onLine = true;
  refreshUserActivityTickSchedule();
  await advance(5000);
  check(calls === 4, 'reconnected time must qualify for a full window');

  spacetimeClientStatus = 'reconnecting';
  refreshUserActivityTickSchedule();
  await advance(10000);
  check(calls === 4, 'reconnecting time must not tick');
  spacetimeClientStatus = 'live';
  refreshUserActivityTickSchedule();
  await advance(5000);
  check(calls === 5, 'a live connection must qualify again from zero');
})().catch((error) => { console.error(error); process.exitCode = 1; });
`)

	bundle := ticketRemoteSourceFile(t, "internal", "web", "static", "app.js")
	for _, required := range []string{
		"activityTickIntervalMs",
		"activityTickMaximumDelayMs",
		"recordUserActivityTick",
		"refreshUserActivityTickSchedule",
		"recordActivityTick",
	} {
		if !strings.Contains(bundle, required) {
			t.Fatalf("shipped main-page bundle is missing activity heartbeat marker %q", required)
		}
	}
	if strings.Contains(bundle, "setInterval(()=>{void recordUserActivityTick()},activityTickIntervalMs)") {
		t.Fatal("shipped main-page bundle still uses the old page-lifetime activity interval")
	}
}

func TestTicketAdminDoesNotLoadMainActivityScript(t *testing.T) {
	admin := ticketRemoteSourceFile(t, "internal", "web", "static", "admin.html.tmpl")
	if strings.Contains(admin, "/static/app.js") {
		t.Fatal("the admin page must not load the main Ticket activity heartbeat")
	}
}
