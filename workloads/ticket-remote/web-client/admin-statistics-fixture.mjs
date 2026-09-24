// Synthetic, loopback-only statistics page for responsive and performance checks.
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const directory = path.dirname(fileURLToPath(import.meta.url));
const staticDirectory = path.resolve(directory, '../internal/web/static');
const hourly = (hour, value) => Array.from({ length: 24 }, (_, index) => index === hour ? value : 0);

export function statisticsFixturePayload(scenario = 'mixed') {
  const members = Array.from({ length: scenario === 'crowded' ? 12 : 3 }, (_, index) => ({
    accountScopeId: `scope-${index}`, publicId: index === 0 && scenario === 'large' ? 'LONGUSER1234' : ['AB12', 'CD34', 'EF56'][index] || `U${index}`,
    email: index === 0 && scenario === 'large' ? 'very.long.email.address.with.many.parts.and.delivery.alias+ticket.viewer@example.test' : `member${index}@example.test`, active: index !== 2
  }));
  const payload = { serverTime: '2026-09-09T18:00:00Z', days: 30, timeZone: 'Europe/Riga', secondsPerTick: 5,
    actionStatisticsStartedAt: '2026-09-09T08:00:00Z', members: scenario === 'missing' ? members.slice(0, 2) : members, pageActivityDaily: [], actionActivityDaily: [] };
  if (scenario === 'empty') return payload;
  for (let day = 0; day < (scenario === 'representative' ? 8 : 2); day += 1) {
    for (const [index, member] of members.entries()) {
      const date = `2026-09-${String(9 - day).padStart(2, '0')}`;
      if (scenario !== 'actions' && index !== 2) payload.pageActivityDaily.push({ accountScopeId: member.accountScopeId, day: date,
        hourlyTicks: scenario === 'representative' ? Array.from({ length: 24 }, (_, hour) => (hour + day + index) % 3 ? 0 : 17 + hour) : hourly(14, 51 + index) });
      if (scenario !== 'viewing' && day === 0) payload.actionActivityDaily.push({ accountScopeId: member.accountScopeId, day: date,
        registrationAttempts: hourly(14, scenario === 'large' ? 4294967295 : 2), registrationSuccesses: hourly(14, 1),
        controlCodeAttempts: hourly(14, scenario === 'large' ? 4294967295 : 3), controlCodeSuccesses: hourly(14, 3) });
    }
  }
  return payload;
}

export async function startStatisticsFixture({ baselineDirectory = '' } = {}) {
  const server = createServer(async (request, response) => {
    try {
      const url = new URL(request.url, 'http://127.0.0.1');
      const baseline = url.searchParams.get('baseline') === '1' && baselineDirectory;
      const file = url.pathname === '/app.css' ? path.join(staticDirectory, 'app.css')
        : url.pathname === '/statistics.css' ? path.join(baseline || staticDirectory, baseline ? 'admin-statistics-before.css' : 'admin-statistics.css')
        : url.pathname === '/statistics.js' ? path.join(baseline || staticDirectory, baseline ? 'admin-statistics-before.bundle.js' : 'admin-statistics.js') : '';
      if (file) {
        response.setHeader('Content-Type', file.endsWith('.css') ? 'text/css' : 'text/javascript');
        const source = await readFile(file, 'utf8');
        response.end(file.endsWith('.js') ? `window.statsRenderStart=performance.now();\n${source}\nwindow.statsRenderMs=performance.now()-window.statsRenderStart;` : source);
        return;
      }
      if (url.pathname === '/api/v1/admin/statistics') { response.setHeader('Content-Type', 'application/json'); response.end(JSON.stringify(statisticsFixturePayload())); return; }
      if (url.pathname !== '/') { response.writeHead(404); response.end(); return; }
      const payload = statisticsFixturePayload(url.searchParams.get('scenario') || 'mixed');
      if (baseline) { delete payload.actionActivityDaily; delete payload.actionStatisticsStartedAt; }
      const suffix = baseline ? '?baseline=1' : '';
      response.setHeader('Content-Type', 'text/html');
      response.end(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
        <title>Ticket statistics fixture</title><link rel="icon" href="data:,"><link rel="stylesheet" href="/app.css"><link rel="stylesheet" href="/statistics.css${suffix}">
        ${url.searchParams.get('zoom') === '2' ? '<style>body{zoom:2}</style>' : ''}
        <script>window.statsErrors=[];addEventListener('error', e=>statsErrors.push(e.message));addEventListener('unhandledrejection', e=>statsErrors.push(String(e.reason)));</script>
        ${url.searchParams.has('live') ? `<script>
          addEventListener('unhandledrejection',e=>{document.getElementById('fixtureResult').textContent=JSON.stringify({errors:[String(e.reason)],checks:[]});document.documentElement.dataset.probeComplete='true';});
          window.statsIntervals=new Map();window.statsTimeouts=new Map();window.statsSequence=0;
          window.setInterval=(fn,ms)=>{const id=++statsSequence;statsIntervals.set(id,fn);return id;};
          window.clearInterval=id=>statsIntervals.delete(id);
          const realTimeout=window.setTimeout.bind(window),realClearTimeout=window.clearTimeout.bind(window);
          window.setTimeout=(fn,ms)=>{if(ms!==10000)return realTimeout(fn,ms);const id=++statsSequence;statsTimeouts.set(id,fn);return id;};
          window.clearTimeout=id=>{statsTimeouts.delete(id);realClearTimeout(id);};
          window.statsHidden=false;Object.defineProperty(document,'hidden',{get:()=>statsHidden});
          window.statsFetches=0;window.statsMode='ok';window.statsPayload=${JSON.stringify(payload)};
          window.fetch=async (url,options)=>{statsFetches++;if(statsMode==='hang')return new Promise((resolve,reject)=>options.signal.addEventListener('abort',()=>reject(Error('aborted'))));return {ok:statsMode==='ok',status:statsMode==='denied'?403:statsMode==='ok'?200:503,json:async()=>structuredClone(statsPayload)};};
        </script>` : ''}
        <script defer src="/statistics.js${suffix}"></script></head><body class="admin-page admin-statistics-page">
        <main class="admin-shell"><header class="admin-header"><div><p class="admin-eyebrow">Ticket remote</p><h1>Admin</h1></div><a class="admin-stream-link" href="#">Stream</a></header>
        <nav class="admin-tabs"><a class="admin-tab" href="#">Overview</a><a class="admin-tab is-active" href="#">Statistics</a></nav>
        <section class="admin-section admin-statistics-section"><div class="admin-section-header"><div><h2>User activity</h2><p class="admin-muted">Page use and actions for today and the previous 29 Europe/Riga calendar days. Viewing time is measured in five-second intervals.</p></div></div>
        <div id="ticketActivityStatistics"></div></section></main>
        <script id="ticketActivityStatisticsData" type="application/json">${JSON.stringify(payload).replaceAll('<', '\\u003c')}</script>
        <pre id="fixtureResult" hidden></pre>
        <script>
        addEventListener('load', async () => {
          const frame=()=>new Promise(resolve=>requestAnimationFrame(resolve));
          await frame();
          const toggle=document.getElementById('adminStatisticsViewToggle');
          if (${JSON.stringify(url.searchParams.has('live'))}) {
            const checks=[];const check=(name,ok)=>{checks.push(name);if(!ok)throw Error(name);};
            const flush=async()=>{await Promise.resolve();await frame();await frame();await frame();};
            const refresh=async()=>{await [...statsIntervals.values()][0]?.();await flush();};
            const first=document.querySelector('[data-statistics-day-toggle]');first.click();await frame();
            const expanded=first.getAttribute('aria-expanded'),mode=document.querySelector('.admin-statistics-view').dataset.viewMode;
            statsPayload.actionActivityDaily[0].registrationAttempts[14]=9;
            statsPayload.actionActivityDaily[0].registrationSuccesses[14]=8;
            statsPayload.pageActivityDaily[0].hourlyTicks[14]=120;
            statsPayload.pageActivityDaily[0].hourlyTicks[10]=200;
            await refresh();
            check('existing account counters and duration update',document.querySelector('.admin-statistics-compact').textContent.includes('8/9')&&document.querySelector('.admin-statistics-compact').textContent.includes('10m'));
            check('hourly bars update with activity',document.querySelector('.admin-statistics-bars li:nth-child(15)').textContent.includes('14m 20s'));
            check('new daily peak reaches full height',document.querySelector('.admin-statistics-bars li:nth-child(11) .admin-statistics-bar').classList.contains('level-10')&&!document.querySelector('.admin-statistics-bars li:nth-child(15) .admin-statistics-bar').classList.contains('level-10'));
            check('view and expanded day preserved',document.querySelector('.admin-statistics-view').dataset.viewMode===mode&&document.querySelector('[data-statistics-day-toggle]').getAttribute('aria-expanded')===expanded);
            check('detailed table updates too',document.querySelector('.admin-statistics-table').textContent.includes('8/9')&&document.querySelector('.admin-statistics-table').textContent.includes('10m'));
            const before=document.querySelector('.admin-statistics-action-summary').textContent;
            statsMode='failed';await refresh();check('failure retains figures and marks stale',document.querySelector('.admin-statistics-action-summary').textContent===before&&document.querySelector('[role=status]').textContent.includes('unavailable'));
            statsMode='hang';const pending=[...statsIntervals.values()][0]();await flush();const calls=statsFetches;
            await refresh();check('no overlapping refresh',statsFetches===calls);
            for(const timeout of statsTimeouts.values())timeout();await pending;await flush();
            statsMode='ok';await refresh();check('timeout recovers on next refresh',!document.querySelector('[role=status]').textContent.includes('unavailable'));
            statsHidden=true;document.dispatchEvent(new Event('visibilitychange'));const hiddenCalls=statsFetches;await refresh();check('hidden page does not refresh',statsFetches===hiddenCalls);
            statsHidden=false;document.dispatchEvent(new Event('visibilitychange'));await flush();check('return refreshes immediately',statsFetches===hiddenCalls+1);
            window.dispatchEvent(new PageTransitionEvent('pagehide',{persisted:true}));
            window.dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true}));await flush();
            check('cached return refreshes without resetting view',statsIntervals.size===1&&document.querySelector('[data-statistics-day-toggle]').getAttribute('aria-expanded')===expanded);
            statsPayload.serverTime='2026-09-09T21:00:01Z';await refresh();
            check('Riga midnight advances the displayed day',document.querySelector('.admin-statistics-table tbody th').textContent.includes('2026-09-10'));
            statsMode='denied';await refresh();check('access revocation stops refresh',statsIntervals.size===0&&document.querySelector('[role=status]').textContent.includes('Access expired'));
            window.dispatchEvent(new PageTransitionEvent('pagehide'));check('cleanup clears requests and timers',statsIntervals.size===0&&statsTimeouts.size===0);
            document.getElementById('fixtureResult').textContent=JSON.stringify({checks,errors:statsErrors});document.documentElement.dataset.probeComplete='true';return;
          }
          const initialMode=document.querySelector('.admin-statistics-view')?.dataset.viewMode;
          if (${JSON.stringify(url.searchParams.get('probe') === '1')}) {
            if (initialMode==='table') toggle.click();
            await frame();
            const compactOverflow=document.documentElement.scrollWidth>innerWidth+1;
            const firstDay=document.querySelector('[data-statistics-day-toggle]');
            let dayWorks=true;
            if(firstDay){const before=firstDay.getAttribute('aria-expanded');firstDay.click();await frame();dayWorks=firstDay.getAttribute('aria-expanded')!==before;firstDay.click();await frame();}
            const metricClips=[...document.querySelectorAll('.admin-statistics-compact .admin-statistics-metric')].filter(el=>el.getClientRects().length).filter(el=>el.getBoundingClientRect().right>el.closest('.admin-statistics-entry,.admin-statistics-day-toggle').getBoundingClientRect().right+1).length;
            const entryHeights=[...document.querySelectorAll('.admin-statistics-compact .admin-statistics-entry')].filter(el=>el.getClientRects().length).map(el=>el.getBoundingClientRect().height);
            toggle.click();await frame();
            const tableWorks=document.querySelector('.admin-statistics-view').dataset.viewMode==='table';
            toggle.click();await frame();
            const emails=[...document.querySelectorAll('.admin-statistics-compact .admin-statistics-entry-email')];
            const charts=[...document.querySelectorAll('.admin-statistics-chart')];
            const result={initialMode,compactOverflow,metricClips,dayWorks,tableWorks,entryHeights,
              chartCount:charts.length,chartHoursValid:charts.every(chart=>chart.querySelectorAll('.admin-statistics-bars li').length===24),
              chartPeaksValid:charts.every(chart=>!chart.querySelector('.admin-statistics-bar.is-active')||!!chart.querySelector('.admin-statistics-bar.level-10')),
              chartOverflow:charts.some(chart=>chart.scrollWidth>chart.clientWidth+1),
              entryLabels:emails.map(el=>el.textContent),emailClips:emails.filter(el=>el.getClientRects().length&&el.scrollWidth>el.clientWidth+1).length,
              legendPresent:!!document.querySelector('.admin-statistics-legend'),errors:window.statsErrors,renderMs:window.statsRenderMs,
              resources:performance.getEntriesByType('resource').map(e=>({name:new URL(e.name).pathname,bytes:e.encodedBodySize})),payloadBytes:document.getElementById('ticketActivityStatisticsData').textContent.length,
              countDescriptions:[...document.querySelectorAll('.admin-statistics-metric')].map(el=>el.getAttribute('aria-label'))};
            document.getElementById('fixtureResult').textContent=JSON.stringify(result);
          }
          document.documentElement.dataset.probeComplete='true';
        });</script></body></html>`);
    } catch (error) { response.writeHead(500); response.end(String(error)); }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  return { url: `http://127.0.0.1:${server.address().port}`, close: () => new Promise(resolve => server.close(resolve)) };
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const fixture = await startStatisticsFixture({ baselineDirectory: process.env.TICKET_STATS_BASELINE_DIR });
  console.log(fixture.url);
}
