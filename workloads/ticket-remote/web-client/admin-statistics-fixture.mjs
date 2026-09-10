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
    email: `member${index}@example.test`, active: index !== 2
  }));
  const payload = { serverTime: '2026-09-09T18:00:00Z', days: 30, timeZone: 'Europe/Riga', secondsPerTick: 5,
    actionStatisticsStartedAt: '2026-09-09T08:00:00Z', members, pageActivityDaily: [], actionActivityDaily: [] };
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
      if (url.pathname !== '/') { response.writeHead(404); response.end(); return; }
      const payload = statisticsFixturePayload(url.searchParams.get('scenario') || 'mixed');
      if (baseline) { delete payload.actionActivityDaily; delete payload.actionStatisticsStartedAt; }
      const suffix = baseline ? '?baseline=1' : '';
      response.setHeader('Content-Type', 'text/html');
      response.end(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
        <title>Ticket statistics fixture</title><link rel="icon" href="data:,"><link rel="stylesheet" href="/app.css"><link rel="stylesheet" href="/statistics.css${suffix}">
        ${url.searchParams.get('zoom') === '2' ? '<style>body{zoom:2}</style>' : ''}
        <script>window.statsErrors=[];addEventListener('error', e=>statsErrors.push(e.message));addEventListener('unhandledrejection', e=>statsErrors.push(String(e.reason)));</script>
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
            const result={initialMode,compactOverflow,metricClips,dayWorks,tableWorks,entryHeights,errors:window.statsErrors,renderMs:window.statsRenderMs,
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
