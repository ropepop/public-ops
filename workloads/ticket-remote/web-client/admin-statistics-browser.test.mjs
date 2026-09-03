import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { fileURLToPath } from 'node:url';

import { build } from 'esbuild';
import { findBraveBrowser, renderBraveDOM } from './brave-browser-test-helper.mjs';

const sourceFile = fileURLToPath(new URL('./admin-statistics-source.js', import.meta.url));
function hourlyTicks(values) {
  return Array.from({ length: 24 }, (_, hour) => values[hour] || 0);
}

function browserFixture(interactive = false) {
  const payload = {
    serverTime: '2026-09-02T12:00:00Z',
    timeZone: 'Europe/Riga',
    days: 30,
    secondsPerTick: 5,
    members: [
      { accountScopeId: 'scope-active', publicId: 'A1B2', email: 'active@example.test', active: true },
      { accountScopeId: 'scope-inactive', publicId: 'C3D4', email: 'inactive@example.test', active: false }
    ],
    pageActivityDaily: [
      { accountScopeId: 'scope-active', day: '2026-09-02', hourlyTicks: hourlyTicks({ 0: 3 }) },
      { accountScopeId: 'scope-inactive', day: '2026-09-02', hourlyTicks: hourlyTicks({ 0: 6 }) },
      { accountScopeId: 'scope-active', day: '2026-09-01', hourlyTicks: hourlyTicks({ 9: 12 }) }
    ]
  };

  const interactionProbe = interactive ? `
  <script>
    window.addEventListener('load', async function () {
      var nextTurn = function () { return new Promise(function (resolve) { setTimeout(resolve, 0); }); };
      await nextTurn();
      var root = document.querySelector('.admin-statistics-view');
      var compact = document.getElementById('adminStatisticsCompactView');
      var detailed = document.getElementById('adminStatisticsDetailedView');
      var viewToggle = document.getElementById('adminStatisticsViewToggle');
      var dayButtons = Array.from(document.querySelectorAll('[data-statistics-day-toggle]'));
      var dayPanels = dayButtons.map(function (button) { return document.getElementById(button.getAttribute('aria-controls')); });
      root.dataset.probeDayCount = String(dayButtons.length);
      root.dataset.probeHourCount = String(document.querySelectorAll('[data-statistics-active-hour]').length);
      root.dataset.probeViewportWidth = String(window.innerWidth);
      root.dataset.probeInitialCompact = String(!compact.hidden && detailed.hidden);
      root.dataset.probeInitialNewestOpen = String(
        dayButtons[0].getAttribute('aria-expanded') === 'true' &&
        !dayPanels[0].hidden &&
        dayButtons[1].getAttribute('aria-expanded') === 'false' &&
        dayPanels[1].hidden
      );

      dayButtons[1].click();
      await nextTurn();
      root.dataset.probeOneOpen = String(
        dayButtons[0].getAttribute('aria-expanded') === 'false' &&
        dayPanels[0].hidden &&
        dayButtons[1].getAttribute('aria-expanded') === 'true' &&
        !dayPanels[1].hidden
      );

      dayButtons[1].click();
      await nextTurn();
      root.dataset.probeAllCollapsed = String(
        dayButtons.every(function (button) { return button.getAttribute('aria-expanded') === 'false'; }) &&
        dayPanels.every(function (panel) { return panel.hidden; })
      );

      viewToggle.click();
      await nextTurn();
      root.dataset.probeDetailed = String(
        compact.hidden && !detailed.hidden && viewToggle.textContent.trim() === 'Compact list'
      );

      viewToggle.click();
      await nextTurn();
      root.dataset.probeCompactAgain = String(
        !compact.hidden && detailed.hidden && viewToggle.textContent.trim() === 'Detailed table'
      );
      root.dataset.probeComplete = 'true';
    });
  </script>` : '';

  return `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <script>
    window.addEventListener('error', function (event) {
      var output = document.getElementById('ticketAdminStatisticsBrowserErrors');
      output.dataset.errorCount = String(Number(output.dataset.errorCount || 0) + 1);
      output.textContent += String(event.error && event.error.stack || event.message || 'browser error') + '\\n';
    });
    window.addEventListener('unhandledrejection', function (event) {
      var output = document.getElementById('ticketAdminStatisticsBrowserErrors');
      output.dataset.errorCount = String(Number(output.dataset.errorCount || 0) + 1);
      output.textContent += String(event.reason && event.reason.stack || event.reason || 'unhandled rejection') + '\\n';
    });
  </script>
  <script defer src="/admin-statistics.js"></script>
</head>
<body>
  <div id="ticketActivityStatistics"></div>
  <script id="ticketActivityStatisticsData" type="application/json">${JSON.stringify(payload)}</script>
  <pre id="ticketAdminStatisticsBrowserErrors" data-error-count="0" hidden></pre>
  ${interactionProbe}
</body>
</html>`;
}

async function listen(server) {
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
}

async function close(server) {
  const closed = new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  if (typeof server.closeAllConnections === 'function') server.closeAllConnections();
  await closed;
}

async function renderFixtureDOM(browser, address, options = {}) {
  return renderBraveDOM(
    browser,
    `http://127.0.0.1:${address.port}/${options.interactive ? '?interactive=1' : ''}`,
    {
      windowSize: options.windowSize,
      waitExpression: options.interactive
        ? 'document.readyState === "complete" && document.querySelector(".admin-statistics-view")?.dataset.probeComplete === "true"'
        : 'document.readyState === "complete" && document.documentElement.dataset.ticketAdminStatisticsUi === "arrow"'
    }
  );
}

test('admin statistics supports responsive defaults and accessible compact interactions in a real browser', async (t) => {
  const browser = await findBraveBrowser();
  if (!browser) {
    t.skip('no Brave executable is available');
    return;
  }

  const bundle = await build({
    entryPoints: [sourceFile],
    bundle: true,
    format: 'iife',
    minifyWhitespace: true,
    target: 'es2020',
    write: false
  });
  const script = bundle.outputFiles[0].text;
  const server = http.createServer((request, response) => {
    if (request.url === '/admin-statistics.js') {
      response.writeHead(200, { 'Content-Type': 'text/javascript; charset=utf-8' });
      response.end(script);
      return;
    }
    if (request.url === '/favicon.ico') {
      response.writeHead(204);
      response.end();
      return;
    }
    response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
    response.end(browserFixture(request.url && request.url.includes('interactive=1')));
  });
  await listen(server);

  const address = server.address();
  assert.ok(address && typeof address === 'object');
  try {
    const desktop = await renderFixtureDOM(browser, address, { windowSize: '1200,800' });
    assert.match(desktop.stdout, /data-ticket-admin-statistics-ui="arrow"/, `Arrow mount marker missing. Browser stderr:\n${desktop.stderr}`);
    assert.match(desktop.stdout, /data-view-mode="table"/, 'desktop did not default to the detailed table');
    assert.match(desktop.stdout, /class="admin-statistics-compact"[^>]*hidden="true"/, 'desktop compact view was not hidden');
    assert.doesNotMatch(desktop.stdout, /class="admin-statistics-table-wrap"[^>]*hidden="true"/, 'desktop detailed table was hidden');
    assert.match(desktop.stdout, /<table class="admin-statistics-table">/, 'statistics table was not mounted');
    assert.match(desktop.stdout, />15s</, 'active-user duration was not rendered');
    assert.match(desktop.stdout, />30s</, 'inactive-user duration was not rendered');
    assert.match(desktop.stdout, />Inactive</, 'inactive-user label was not rendered');
    assert.match(desktop.stdout, /id="ticketAdminStatisticsBrowserErrors" data-error-count="0"/, 'desktop browser reported a runtime error');

    const mobile = await renderFixtureDOM(browser, address, { windowSize: '390,844', interactive: true });
    assert.match(mobile.stdout, /data-probe-complete="true"/, `mobile interaction probe did not finish. Browser stderr:\n${mobile.stderr}`);
    assert.match(mobile.stdout, /data-probe-viewport-width="390"/, 'the 390px test did not use an exact 390px viewport');
    assert.match(mobile.stdout, /data-probe-initial-compact="true"/, 'mobile did not default to the compact view');
    assert.match(mobile.stdout, /data-probe-initial-newest-open="true"/, 'newest active day did not start expanded');
    assert.match(mobile.stdout, /data-probe-one-open="true"/, 'opening a day did not collapse the previous day');
    assert.match(mobile.stdout, /data-probe-all-collapsed="true"/, 'selecting the open day did not collapse it');
    assert.match(mobile.stdout, /data-probe-detailed="true"/, 'compact view did not switch to the detailed table');
    assert.match(mobile.stdout, /data-probe-compact-again="true"/, 'detailed table did not switch back to compact view');
    assert.match(mobile.stdout, /data-probe-day-count="2"/, 'compact view rendered inactive dates');
    assert.match(mobile.stdout, /data-probe-hour-count="2"/, 'compact view rendered inactive hours');
    assert.match(mobile.stdout, />Today · 2026-09-02</, 'Today label was not rendered');
    assert.match(mobile.stdout, />Yesterday · 2026-09-01</, 'Yesterday label was not rendered');
    assert.match(mobile.stdout, />00:00–00:59</, 'active current-day hour was not rendered');
    assert.match(mobile.stdout, />09:00–09:59</, 'active previous-day hour was not rendered');
    assert.match(mobile.stdout, /aria-controls="adminStatisticsDayPanel20260902" aria-expanded="false"/, 'day button accessibility state was not rendered');
    assert.match(mobile.stdout, /role="region" aria-labelledby="adminStatisticsDayButton20260902" hidden="true"/, 'collapsed day panel accessibility state was not rendered');
    assert.match(mobile.stdout, /id="ticketAdminStatisticsBrowserErrors" data-error-count="0"/, 'mobile browser reported a runtime error');

    const narrow = await renderFixtureDOM(browser, address, { windowSize: '320,720', interactive: true });
    assert.match(narrow.stdout, /data-probe-viewport-width="320"/, 'the 320px test did not use an exact 320px viewport');
    assert.match(narrow.stdout, /data-probe-initial-compact="true"/, '320px did not default to the compact view');
    assert.match(narrow.stdout, /data-probe-complete="true"/, '320px interaction probe did not finish');
  } finally {
    await close(server);
  }
});
