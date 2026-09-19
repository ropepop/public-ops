package spacetime

import (
	"os/exec"
	"strings"
	"testing"

	"telegramtrainapp/internal/domain"
)

func TestAreaPublicProjectionAndMutationPrivacy(t *testing.T) {
	const subject = "5695721,2368939,privāts—apraksts-🚆"
	cmd := exec.Command("node", "--disable-warning=ExperimentalWarning", "-e", `
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(0, 'utf8');
const strip = require('node:module').stripTypeScriptTypes;
function take(name) {
  const start = source.indexOf('function ' + name + '(');
  assert(start >= 0, name + ' missing');
  const end = source.indexOf('\n}\n', start);
  assert(end > start, name + ' end missing');
  return source.slice(start, end + 3);
}
function exported(name) {
  const start = source.indexOf('export const ' + name + ' =');
  assert(start >= 0, name + ' missing');
  const end = source.indexOf('\nexport const ', start + 1);
  return source.slice(start, end).replace('export ', '');
}
function table() {
  const items = new Map();
  return {
    items, iter: () => items.values(), insert: row => { items.set(row.id, row); return row; },
    delete: row => items.delete(row.id),
    id: {find: id => items.get(id), delete: id => items.delete(id)},
    incidentId: {filter: id => [...items.values()].filter(row => row.incidentId === id)},
    scopeType: {filter: scope => [...items.values()].filter(row => row.scopeType === scope)},
    stableId: {filter: id => [...items.values()].filter(row => row.stableId === id)},
  };
}
const db = {};
for (const name of ['activity', 'incident_summary', 'incident_event', 'incident_comment', 'public_sighting', 'incident_vote', 'incident_vote_event']) db['trainbot_' + name] = table();
const now = '2026-09-16T22:40:00.000Z'; // Already Sep 17 in Riga; timetable still Sep 16.
const subject = process.argv[1];
const expected = process.argv[2];
const priorAlias = process.argv[3];
const rawID = 'area:' + subject + ':2026-09-16';
const activity = {
  id: rawID, scopeType: 'area', subjectId: subject, subjectName: 'privāts—apraksts-🚆',
  serviceDate: '2026-09-16', active: true, summary: {},
  timeline: [{id: 'private-event', kind: 'location_report', name: 'Inspection near this location', detail: 'privāts—apraksts-🚆', signal: 'LOC:56.95721,23.68939,100', createdAt: now},
    {id: 'old-private-event', kind: 'location_report', detail: 'privāts—apraksts-🚆', signal: 'LOC:56.95721,23.68939,100', createdAt: '2026-09-15T22:40:00.000Z'}],
  comments: [], votes: [],
};
db.trainbot_activity.insert(activity);
let cooldownID;
const reducers = {}, views = {};
const scope = {
  TextEncoder, Date, Set, Map, SenderError: Error,
  asString: value => String(value ?? ''), trimOptional: value => String(value ?? '').trim() || undefined,
  rowsFrom: rows => [...rows], parseISO: value => { const d = new Date(value); return Number.isNaN(d.getTime()) ? undefined : d; },
  compareTimeDescending: (left, right) => Date.parse(right) - Date.parse(left),
  rigaScheduleFormatter: new Intl.DateTimeFormat('en-GB', {timeZone: 'Europe/Riga', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', hourCycle: 'h23'}),
  optionalViewerStableId: () => '', nowDate: () => new Date(now), nowISO: () => now,
  PUBLIC_INCIDENT_ACTOR_LABEL: 'Anonymous',
  VOTE_ACTION_WINDOW_MS: 3600000, VOTE_ACTION_LIMIT: 20,
  COMMENT_ACTION_WINDOW_MS: 3600000, COMMENT_ACTION_LIMIT: 10, INCIDENT_COMMENT_ACTION_LIMIT: 50,
  voteChangeCooldownSeconds: (_, id) => { cooldownID = id; return 0; },
  countVoteActionsForStableIdSince: () => 0, countCommentsForStableIdSince: () => 0, countCommentsForIncidentSince: () => 0,
  putActivityRow: (_, row) => db.trainbot_activity.insert(row),
  syncIncidentVoteProjection() {}, scheduleActivityRefreshJobs() {}, refreshTripProjection() {},
  ensureRider: tx => { if (!tx.authorized) throw new Error('unauthorized'); return {session: {stableId: 'telegram:42'}, rider: {nickname: 'QA'}}; },
  riderForSenderIdentity: () => ({stableId: 'telegram:42'}),
  named: value => value, t: {string() {}, array() {}}, myIncidentVoteView: {},
  spacetimedb: {reducer: ({name}, _, body) => { reducers[name] = body; }, view: ({name}, _, body) => { views[name] = body; }},
};
const functions = ['rigaDateParts', 'formatServiceDateFor', 'publicOpaqueId', 'publicIncidentId', 'findIncidentActivity', 'latestReportEvent', 'activityVoteSummary', 'incidentLocationPayload', 'parseLocationSignal', 'publicIncidentLocationPayload', 'publicIncidentEventDetail', 'incidentMapTargetPayload', 'publicIncidentSubjectId', 'publicIncidentSubjectName', 'incidentSummaryPayload', 'incidentCommentActivityLabel', 'incidentVoteEventLabel', 'incidentDetailPayload', 'clearRowsByIncident', 'clearIncidentPublicProjections', 'refreshActivityProjection', 'submitIncidentVoteAtomic', 'submitIncidentCommentAtomic'];
vm.runInNewContext(strip(functions.map(take).join('\n') + '\n' + ['voteIncident', 'commentIncident', 'myIncidentVotes'].map(exported).join('\n')), scope);
const tx = {db, authorized: true, newUuidV7: () => 'qa-id'};
assert.equal(scope.publicIncidentId(activity), expected);
assert.equal(scope.findIncidentActivity(tx, expected).id, rawID);
assert.equal(scope.findIncidentActivity(tx, priorAlias).id, rawID);
assert.equal(scope.findIncidentActivity(tx, 'area:pub-missing'), null);
for (const prefix of ['train', 'station']) {
  const row = {...activity, id: prefix + ':riga:2026-09-17', scopeType: prefix, subjectId: 'riga'};
  assert.equal(scope.publicIncidentId(row), row.id);
}
// A refresh must clear both old raw keys and yesterday's public aliases.
for (const id of [rawID, priorAlias]) {
  db.trainbot_incident_summary.insert({id});
  db.trainbot_incident_event.insert({id: 'event-' + id, incidentId: id});
  db.trainbot_incident_comment.insert({id: 'comment-' + id, incidentId: id});
}
scope.refreshActivityProjection(tx, rawID);
assert.deepEqual([...db.trainbot_incident_summary.items.keys()], [expected]);
function assertPublic() {
  const detail = scope.incidentDetailPayload(tx, expected);
  assert.equal(detail.summary.id, expected);
  assert.equal(detail.summary.mapTarget.incidentId, expected);
  const rows = ['incident_summary', 'incident_event', 'incident_comment', 'public_sighting'].flatMap(name => [...db['trainbot_' + name].items.values()]);
  const serialized = JSON.stringify({detail, rows});
  for (const secret of [rawID, subject, 'privāts—apraksts-🚆', '56.95721', '23.68939']) assert(!serialized.includes(secret), 'private area data leaked: ' + secret);
  for (const row of rows) if ('incidentId' in row) assert.equal(row.incidentId, expected);
}
assertPublic();
for (const name of ['vote_incident', 'comment_incident']) assert.throws(() => reducers[name]({db}, {incidentId: expected, value: 'ONGOING', body: 'QA comment'}), /unauthorized/);
assert.equal(db.trainbot_activity.id.find(rawID).comments.length, 0);
reducers.comment_incident(tx, {incidentId: expected, body: 'QA comment'});
assert.equal(db.trainbot_activity.id.find(rawID).comments[0].body, 'QA comment');
reducers.vote_incident(tx, {incidentId: expected, value: 'ONGOING'});
assert.equal(cooldownID, rawID);
assert.equal([...db.trainbot_incident_vote_event.items.values()][0].incidentId, rawID);
db.trainbot_incident_vote.insert({id: 'private-vote', incidentId: rawID, stableId: 'telegram:42', value: 'ONGOING'});
assert.equal(views.my_incident_votes(tx)[0].incidentId, expected);
assertPublic();
scope.clearIncidentPublicProjections(tx, rawID, db.trainbot_activity.id.find(rawID));
for (const name of ['incident_summary', 'incident_event', 'incident_comment', 'public_sighting']) assert.equal(db['trainbot_' + name].items.size, 0, name + ' retained public rows');
`, subject, domain.AreaIncidentID(subject, "2026-09-17"), domain.AreaIncidentID(subject, "2026-09-16"))
	cmd.Stdin = strings.NewReader(readSpacetimeSource(t))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("area public privacy round-trip: %v\n%s", err, output)
	}
}
