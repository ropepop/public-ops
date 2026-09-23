import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { build } from 'esbuild';
import { findBraveBrowser, renderBraveDOM } from './brave-browser-test-helper.mjs';

function setup() {
  const fixture = window.fixture = { connections: 0, disconnects: [], requests: [], saves: [] };
  window.fetch = async path => {
    fixture.requests.push(path);
    if (path !== '/api/v1/auth/session') throw Error('Unexpected request: ' + path);
    return { ok: true, json: async () => ({
      spacetime: { token: 'fixture', host: 'fixture', database: 'fixture' },
      accountScopeId: 'a'.repeat(64), state: { ticket: { id: 'ticket' }, phone: { id: 'phone' } }
    }) };
  };
  window.TicketSpacetime = { create: (_, handlers) => {
    fixture.handlers = handlers;
    return {
      connect: () => { fixture.connections++; },
      disconnect: value => { fixture.disconnects.push(value); },
      setLimitPreference: value => {
        fixture.saves.push(value);
        return new Promise((resolve, reject) => { fixture.resolveSave = resolve; fixture.rejectSave = reject; });
      }
    };
  } };
}

function probe() {
  const checks = [], errors = [];
  addEventListener('error', event => errors.push(event.message));
  addEventListener('unhandledrejection', event => errors.push(String(event.reason)));
  const pause = () => new Promise(resolve => setTimeout(resolve, 20));
  const check = (value, name) => { if (!value) throw Error(name); checks.push(name); };
  const state = obeyLimits => ({ memberLimits: { canBypass: true, obeyLimits }, phoneControlState: { busy: false } });
  async function run() {
    await pause();
    const cold = document.querySelector('#ticketAdminColdRestart button');
    const toggle = document.querySelector('#adminObeyMemberLimits');
    check(fixture.connections === 1 && fixture.requests.length === 1, 'one shared authenticated state connection');
    if (cold) {
      check(cold.disabled, 'cold control waits for state');
      fixture.handlers.onState({ phoneControlState: { busy: false } }); await pause();
      check(!cold.disabled, 'real cold mount becomes ready without limit state');
      fixture.handlers.onState({ phoneControlState: { busy: true } }); await pause();
      check(cold.disabled, 'phone busy keeps cold control disabled');
      fixture.handlers.onStatus('offline'); await pause();
      check(cold.disabled && document.querySelector('#ticketAdminColdRestart').textContent.includes('temporarily unavailable'), 'offline disables cold control');
      fixture.handlers.onState({ phoneControlState: { busy: false } }); await pause();
      check(!cold.disabled, 'new state restores cold control');
    }
    if (toggle) {
      check(toggle.disabled, 'preference waits for authorized state');
      fixture.handlers.onState(state(false)); await pause();
      check(!toggle.disabled && !toggle.checked, 'settings reflect saved preference independently');
      toggle.click(); await pause();
      check(toggle.disabled && fixture.saves.length === 1 && fixture.saves[0], 'preference submits once and disables while saving');
      fixture.resolveSave(); await pause();
      check(!toggle.disabled && toggle.checked, 'confirmed preference remains selected');
      toggle.click(); await pause();
      fixture.rejectSave(Error('fixture rejected')); await pause();
      check(!toggle.disabled && toggle.checked, 'failed save restores confirmed preference');
      toggle.click(); await pause();
      fixture.handlers.onStatus('reconnecting'); await pause();
      fixture.resolveSave(); await pause();
      check(toggle.disabled, 'save completion cannot enable an offline control');
      fixture.handlers.onState(state(false)); await pause();
      check(!toggle.disabled && !toggle.checked, 'fresh subscribed state restores preference control');
      fixture.handlers.onState({ memberLimits: { canBypass: false } }); await pause();
      check(toggle.disabled, 'privilege loss disables preference');
    }
    check(fixture.requests.length === 1, 'navigation and state never submit a phone action');
    dispatchEvent(new Event('pagehide')); await pause();
    check(fixture.disconnects.length === 1 && fixture.disconnects[0] === false, 'pagehide disconnects the shared state connection');
    return { checks, errors };
  }
  run().catch(error => ({ checks, errors: [...errors, error.message] })).then(result => {
    const node = document.createElement('pre'); node.id = 'fixtureResult'; node.hidden = true;
    node.textContent = JSON.stringify(result); document.body.append(node);
  });
}

test('admin ticket and settings tabs receive state without each other’s controls', { timeout: 60000 }, async () => {
  const browser = await findBraveBrowser();
  assert.ok(browser, 'Brave is required');
  const bundle = await build({ entryPoints: [new URL('./admin-schedule-source.js', import.meta.url).pathname], bundle: true, write: false, format: 'iife' });
  const server = createServer((req, res) => {
    const mode = new URL(req.url, 'http://fixture').searchParams.get('mode');
    const cold = mode !== 'settings' ? '<section id="ticketAdminColdRestart"></section>' : '';
    const limits = mode !== 'tickets' ? '<input id="adminObeyMemberLimits" type="checkbox" checked disabled><p id="adminLimitPreferenceStatus"></p>' : '';
    res.setHeader('Content-Type', 'text/html; charset=utf-8');
    res.end(`<!doctype html><html><body>${cold}${limits}<script>(${setup})();</script><script>${bundle.outputFiles[0].text}</script><script>(${probe})();</script></body></html>`);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  try {
    for (const mode of ['tickets', 'settings', 'both']) {
      const result = await renderBraveDOM(browser, `http://127.0.0.1:${server.address().port}/?mode=${mode}`, { windowSize: '390,850', waitExpression: '!!document.querySelector("#fixtureResult")' });
      const match = result.stdout.match(/<pre id="fixtureResult" hidden="">([^<]+)<\/pre>/);
      assert.ok(match, `${mode}: missing browser report`);
      const report = JSON.parse(match[1].replaceAll('&quot;', '"').replaceAll('&amp;', '&'));
      assert.deepEqual(report.errors, [], `${mode}: ${JSON.stringify(report)}`);
      console.log(`${mode}: ${report.checks.length} checks passed`);
    }
  } finally { await new Promise(resolve => server.close(resolve)); }
});
