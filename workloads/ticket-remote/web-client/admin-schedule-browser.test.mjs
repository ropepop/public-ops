import assert from 'node:assert/strict';
import http from 'node:http';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import { build } from 'esbuild';
import { findBraveBrowser, renderBraveDOM } from './brave-browser-test-helper.mjs';

const sourceFile = fileURLToPath(new URL('./admin-schedule-source.js', import.meta.url));

function listen(server) {
  return new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      server.off('error', reject);
      resolve();
    });
  });
}

function close(server) {
  const closed = new Promise((resolve) => server.close(resolve));
  if (typeof server.closeAllConnections === 'function') server.closeAllConnections();
  return closed;
}

function fixture() {
  return `<!doctype html><html><body>
    <form class="admin-redetect-form" data-direct-ticket-action-v3="true">
      <button type="submit">Force latest ticket</button>
      <span class="admin-action-status" role="status"></span>
    </form>
    <script id="ticketAdminConfig" type="application/json">{"ticketId":"vivi-default"}</script>
    <script>
      window.__calls = [];
      window.__disconnects = 0;
      window.__terminalPublished = false;
      window.TicketSpacetime = { create: function (_, handlers) {
        return {
          connect: function () {
            handlers.onStatus('live');
            handlers.onState({ ticketActions: [] });
          },
          disconnect: function () {
            if (!window.__terminalPublished) document.documentElement.dataset.disconnectedEarly = 'true';
            window.__disconnects += 1;
          },
          requestTicketActionV3: function (args) {
            window.__calls.push(args);
            handlers.onState({ ticketActions: [{
              actionId: args.actionId, target: 'redetect_latest', status: 'running',
              currentView: 'unknown', reason: 'ticket_action_v3_running',
              streamEpoch: '0', frameSequence: '0'
            }] });
            setTimeout(function () {
              handlers.onState({ ticketActions: [{
                actionId: 'unrelated-action', target: 'redetect_latest', status: 'failed',
                phase: 'failed',
                currentView: 'unknown', reason: 'ticket_action_latest_not_detected',
                streamEpoch: '8', frameSequence: '9'
              }] });
              var button = document.querySelector('.admin-redetect-form button');
              var status = document.querySelector('.admin-redetect-form .admin-action-status');
              document.documentElement.dataset.unrelatedIgnored = String(button.disabled && status.textContent !== 'No tickets found.');
            }, 20);
            setTimeout(function () {
              window.__terminalPublished = true;
              handlers.onState({ ticketActions: [{
                actionId: args.actionId, target: 'redetect_latest', status: 'failed',
                phase: 'failed',
                currentView: 'unknown', reason: 'ticket_action_latest_not_detected',
                streamEpoch: '41', frameSequence: '73'
              }] });
            }, 60);
            return Promise.resolve();
          }
        };
      }};
      window.addEventListener('error', function () { document.documentElement.dataset.browserError = 'true'; });
      window.addEventListener('unhandledrejection', function () { document.documentElement.dataset.browserError = 'true'; });
    </script>
    <script src="/admin-schedule.js"></script>
    <script>
      window.addEventListener('load', function () {
        var form = document.querySelector('.admin-redetect-form');
        form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
        document.documentElement.dataset.disabledWhilePending = String(form.querySelector('button').disabled);
        setTimeout(function () {
          var button = form.querySelector('button');
          var status = form.querySelector('.admin-action-status');
          document.documentElement.dataset.finalMessage = status.textContent;
          document.documentElement.dataset.buttonReenabled = String(!button.disabled);
          document.documentElement.dataset.disconnectedOnce = String(window.__disconnects === 1);
          document.documentElement.dataset.oneRequest = String(window.__calls.length === 1);
          document.documentElement.dataset.probeComplete = 'true';
        }, 120);
      });
    </script>
  </body></html>`;
}

test('immediate admin redetection waits for its exact terminal result and shows expected empty state', async (t) => {
  const browser = await findBraveBrowser();
  if (!browser) {
    t.skip('no Brave browser is installed');
    return;
  }
  const result = await build({
    entryPoints: [sourceFile],
    bundle: true,
    format: 'iife',
    target: 'es2020',
    write: false
  });
  const script = result.outputFiles[0].text;
  let pageRequests = 0;
  const server = http.createServer((request, response) => {
    if (request.url === '/api/v1/auth/session') {
      response.writeHead(200, { 'Content-Type': 'application/json; charset=utf-8' });
      response.end(JSON.stringify({
        email: 'admin@example.test',
        accountScopeId: 'a'.repeat(64),
        sessionId: 'test-session',
        state: { ticket: { id: 'vivi-default' }, phone: { id: 'pixel' } },
        spacetime: { host: 'http://spacetime.test', database: 'ticket-test', token: 'test-token' }
      }));
      return;
    }
    if (request.url === '/admin-schedule.js') {
      response.writeHead(200, { 'Content-Type': 'text/javascript; charset=utf-8' });
      response.end(script);
      return;
    }
    if (request.url === '/') pageRequests += 1;
    response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
    response.end(fixture());
  });
  await listen(server);
  const address = server.address();
  try {
    const { stdout: output } = await renderBraveDOM(
      browser,
      `http://127.0.0.1:${address.port}/`
    );
    for (const expected of [
      'data-disabled-while-pending="true"',
      'data-unrelated-ignored="true"',
      'data-final-message="No tickets found."',
      'data-button-reenabled="true"',
      'data-disconnected-once="true"',
      'data-one-request="true"',
      'data-probe-complete="true"'
    ]) {
      assert.match(output, new RegExp(expected.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
    }
    assert.doesNotMatch(output, /data-browser-error="true"|data-disconnected-early="true"/);
    assert.equal(pageRequests, 1, 'the immediate result must render inline without a page reload');
  } finally {
    await close(server);
  }
});
