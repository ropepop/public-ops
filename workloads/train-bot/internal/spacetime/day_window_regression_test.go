package spacetime

import (
	"os/exec"
	"strings"
	"testing"
)

func TestReadWindowsUseRigaCalendarDay(t *testing.T) {
	source := readSpacetimeSource(t)
	start := strings.Index(source, "function rigaDateParts(")
	end := strings.Index(source, "function genericNickname(")
	if start < 0 || end <= start {
		t.Fatal("missing calendar helpers")
	}
	cmd := exec.Command("node", "--disable-warning=ExperimentalWarning", "-e", `
const assert = require('node:assert/strict');
const source = require('node:module').stripTypeScriptTypes(require('node:fs').readFileSync(0,'utf8'));
const rigaScheduleFormatter = new Intl.DateTimeFormat('en-CA',{timeZone:'Europe/Riga',year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',hourCycle:'h23'});
eval(source + '\nglobalThis.start=rigaDayStart;globalThis.end=rigaDayEnd;');
for (const [now,from,to,hours] of [
 ['2026-09-16T22:00:00Z','2026-09-16T21:00:00.000Z','2026-09-17T20:59:59.999Z',24],
 ['2026-01-16T23:00:00Z','2026-01-16T22:00:00.000Z','2026-01-17T21:59:59.999Z',24],
 ['2026-03-29T12:00:00Z','2026-03-28T22:00:00.000Z','2026-03-29T20:59:59.999Z',23],
 ['2026-10-25T12:00:00Z','2026-10-24T21:00:00.000Z','2026-10-25T21:59:59.999Z',25],
 ['2026-12-31T23:00:00Z','2026-12-31T22:00:00.000Z','2027-01-01T21:59:59.999Z',24],
]) {
 assert.equal(start(new Date(now)).toISOString(),from);
 assert.equal(end(new Date(now)).toISOString(),to);
 assert.equal(end(new Date(now))-start(new Date(now))+1,hours*3600000);
}
`)
	cmd.Stdin = strings.NewReader(source[start:end])
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Riga calendar windows: %v\n%s", err, output)
	}
}
