package spacetime

import (
	"os/exec"
	"strings"
	"testing"
)

func TestEmptyActivityRemovesIncidentAndPreservesOtherData(t *testing.T) {
	source := readSpacetimeSource(t)
	var snippets []string
	for _, bounds := range [][2]string{
		{"function clearRowsByIncident(", "function clearJobsByServiceDate("},
		{"function clearIncidentPublicProjections(", "function deleteServiceDayData("},
		{"export const servicePutActivity =", "export const serviceSubmitReport ="},
	} {
		start := strings.Index(source, bounds[0])
		if start < 0 {
			t.Fatalf("missing source anchor %s", bounds[0])
		}
		end := strings.Index(source[start:], bounds[1])
		if end < 0 {
			t.Fatalf("missing source boundary %s", bounds[1])
		}
		snippets = append(snippets, strings.ReplaceAll(source[start:start+end], "export const", "const"))
	}
	cmd := exec.Command("node", "--disable-warning=ExperimentalWarning", "-e", `
const assert = require('node:assert/strict');
const vm = require('node:vm');
const source = require('node:module').stripTypeScriptTypes(require('node:fs').readFileSync(0, 'utf8'));
function table(rows) {
  const data = new Map(rows.map(row => [row.id, row]));
  return { data, id: {find: id => data.get(id), delete: id => data.delete(id)},
    incidentId: {filter: id => [...data.values()].filter(row => row.incidentId === id)},
    delete: row => data.delete(row.id) };
}
const id = 'area:private', other = {id:'unrelated', incidentId:'unrelated'};
const previous = {id, scopeType:'area', timeline:[{kind:'location_report', createdAt:'old'}]};
const db = {trainbot_activity: table([previous, other])};
for (const name of ['incident_event','incident_comment','public_sighting','incident_summary','incident_vote','incident_vote_event']) {
  db['trainbot_'+name] = table([id,'area:pub-new','area:pub-old'].map(key => ({id:key, incidentId:key})).concat(other));
}
// Private vote tables use only the private incident ID.
for (const name of ['incident_vote','incident_vote_event']) db['trainbot_'+name] = table([{id,incidentId:id},other]);
let jobs = ['activity:'+id+'|refresh', 'activity:unrelated|refresh'];
let projections = 0, schedules = 0;
const ctx = {db};
const sandbox = {spacetimedb:{reducer:(_name,_args,fn)=>fn}, t:{string:()=>null}, named:x=>x,
 requireServiceRole:()=>{}, parseJSON:JSON.parse, asString:x=>String(x||''), rowsFrom:x=>Array.from(x),
 sanitizeActivityRow:(_tx,row)=>({...row,timeline:row.timeline||[],comments:row.comments||[],votes:row.votes||[]}),
 putActivityRow:(_tx,row)=>{db.trainbot_activity.data.set(row.id,row);return row},
 publicIncidentId:(_row,at)=>at==='old'?'area:pub-old':'area:pub-new',
 deleteJobsWithPrefix:(_tx,prefix)=>{jobs=jobs.filter(job=>!job.startsWith(prefix))},
 refreshTripProjection:()=>{}, refreshActivityProjection:()=>{projections++}, scheduleActivityRefreshJobs:()=>{schedules++}};
vm.runInNewContext(source+';globalThis.run=servicePutActivity', sandbox);
sandbox.run(ctx,{activityJson:JSON.stringify({id,scopeType:'area',subjectId:'subject',serviceDate:'2026-09-17',timeline:[],comments:[],votes:[]})});
for (const [name,view] of Object.entries(db)) assert.deepEqual([...view.data.keys()], ['unrelated'], name+' retains fixture data');
assert.deepEqual(jobs,['activity:unrelated|refresh']);
assert.equal(projections,0);assert.equal(schedules,0);
for (const field of ['timeline','comments','votes']) {
 const row={id,scopeType:'area',subjectId:'subject',serviceDate:'2026-09-17',timeline:[],comments:[],votes:[]};row[field]=[{id:'kept'}];
 sandbox.run(ctx,{activityJson:JSON.stringify(row)});
 assert.equal(db.trainbot_activity.data.get(id)[field].length,1);
}
assert.equal(projections,3);assert.equal(schedules,3);
`)
	cmd.Stdin = strings.NewReader(strings.Join(snippets, "\n"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("empty activity lifecycle: %v\n%s", err, output)
	}
}
