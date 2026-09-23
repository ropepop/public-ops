import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { runInNewContext } from 'node:vm';
import { build } from 'esbuild';
import { notificationSupport } from './notifications-source.js';
import { findBraveBrowser, renderBraveDOM } from './brave-browser-test-helper.mjs';

test('notification support explains iOS installation and blocked permission without prompting', () => {
  const view = { navigator: { userAgent: 'iPhone', serviceWorker: {} }, isSecureContext: true,
    PushManager: {}, Notification: { permission: 'default' }, matchMedia: () => ({ matches: false }) };
  assert.match(notificationSupport(view), /Home Screen/);
  view.navigator.standalone = true;
  assert.equal(notificationSupport(view), '');
  view.Notification.permission = 'denied';
  assert.match(notificationSupport(view), /blocked/);
  view.navigator.userAgent = 'desktop';
  view.Notification = undefined;
  assert.match(notificationSupport(view), /cannot receive/);
});

test('push worker displays bounded notifications, ignores injected URLs and never caches pages', async () => {
  const handlers = {}, shown = [], opened = [];
  let focused = 0, closed = 0, skipped = 0, windows = [];
  const self = { addEventListener: (type, handler) => { handlers[type] = handler; },
    skipWaiting: async () => { skipped++; }, location: { origin: 'https://ticket.test' },
    registration: { showNotification: async (title, options) => { shown.push({ title, ...options }); } },
    clients: { matchAll: async () => windows, openWindow: async url => opened.push(url) } };
  runInNewContext(await readFile(new URL('./ticket-notifications-sw.js', import.meta.url), 'utf8'), { self, URL });
  const dispatch = async (type, props = {}) => {
    let work;
    handlers[type]({ ...props, waitUntil: promise => { work = promise; } });
    await work;
  };
  await dispatch('install');
  assert.equal(skipped, 1);
  assert.equal(handlers.fetch, undefined, 'no private page caching or request interception');
  await dispatch('push', { data: { json: () => ({ title: 'Ticket needs attention', body: 'Check the phone.', tag: 'incident-1', url: 'https://evil.test' }) } });
  await dispatch('push', { data: { json: () => ({ title: 'Ticket ready', body: 'Ready to use.', tag: 'incident-1' }) } });
  assert.equal(shown[0].tag, shown[1].tag, 'recovery can replace the incident notification');
  await dispatch('push', { data: { json: () => { throw Error('invalid'); } } });
  assert.equal(shown[2].title, 'Ticket');
  await dispatch('push', { data: { json: () => ({ title: 'x'.repeat(1000), body: 'x'.repeat(1000), tag: 'x'.repeat(1000) }) } });
  assert.equal(shown[3].title.length, 100);
  assert.equal(shown[3].body.length, 300);
  const notification = { close: () => { closed++; }, data: { url: 'https://evil.test' } };
  await dispatch('notificationclick', { notification });
  assert.deepEqual(opened, ['/']);
  windows = [{ url: 'https://evil.test/', focus: () => { throw Error('wrong origin'); } },
    { url: 'https://ticket.test/', focus: async () => { focused++; } }];
  await dispatch('notificationclick', { notification });
  assert.equal(focused, 1);
  assert.equal(closed, 2);
});

function fixtureEnvironment() {
  const fixture = window.notificationFixture = { calls: [], permission: 'default', answer: 'default',
    subscribed: false, browserSubscription: false, failures: 0, owner: true, enabled: false, revoked: false };
  const subscription = { endpoint: 'https://push.example/device-private',
    toJSON: () => ({ endpoint: subscription.endpoint, keys: { p256dh: 'fixture-key', auth: 'fixture-auth' } }),
    unsubscribe: async () => { fixture.calls.push('browser-unsubscribe'); fixture.browserSubscription = false; return true; } };
  const registration = { pushManager: {
    getSubscription: async () => fixture.browserSubscription ? subscription : null,
    subscribe: async options => { fixture.calls.push('browser-subscribe'); if (!options.userVisibleOnly || options.applicationServerKey.length !== 3) throw Error('subscription options'); fixture.browserSubscription = true; return subscription; }
  } };
  Object.defineProperty(window, 'Notification', { value: {
    get permission() { return fixture.permission; },
    requestPermission: async () => { fixture.calls.push('permission'); fixture.permission = fixture.answer; return fixture.answer; }
  }, configurable: true });
  Object.defineProperty(window, 'PushManager', { value: function () {}, configurable: true });
  Object.defineProperty(navigator, 'serviceWorker', { value: {
    getRegistration: async () => fixture.browserSubscription ? registration : undefined,
    register: async (path, options) => { fixture.calls.push('worker-register'); if (path !== '/ticket-notifications-sw.js' || options.scope !== '/') throw Error('worker path'); return registration; },
    ready: Promise.resolve(registration)
  }, configurable: true });
  window.fetch = async (path, options) => {
    if (options.cache !== 'no-store' || options.credentials !== 'same-origin') throw Error('private request settings');
    await new Promise(resolve => setTimeout(resolve, 5));
    const body = options.body ? JSON.parse(options.body) : null;
    fixture.calls.push(path + (body ? ':' + (body.action || body.enabled) : ''));
    if (fixture.failures-- > 0) return { ok: false, status: 503 };
    if (fixture.revoked) return { ok: false, status: 403 };
    if (path.endsWith('/monitoring')) {
      if (body) fixture.enabled = body.enabled;
      return { ok: true, json: async () => ({ enabled: fixture.enabled, status: 'ready', lastCheckedAtMs: 1750000000000,
        canManageMonitoring: fixture.owner, pushAvailable: true, vapidPublicKey: 'AQID' }) };
    }
    if (body.action === 'subscribe') {
      if (body.subscription.endpoint !== subscription.endpoint) throw Error('subscription body');
      fixture.subscribed = true;
    }
    if (body.action === 'unsubscribe') fixture.subscribed = false;
    return { ok: true, json: async () => ({ subscribed: fixture.subscribed }) };
  };
}

function browserProbe() {
  const checks = [], errors = [];
  window.addEventListener('error', event => errors.push(event.message));
  window.addEventListener('unhandledrejection', event => errors.push(String(event.reason)));
  const check = (condition, label) => { if (!condition) throw Error(label); checks.push(label); };
  const tick = () => new Promise(resolve => setTimeout(resolve, 30));
  const element = id => document.getElementById(id);
  async function run() {
    const fixture = window.notificationFixture;
    const toggle = element('ticketNotificationToggle'), refresh = element('ticketNotificationRefresh'), monitor = element('ticketMonitoring');
    const settle = async () => { for (let i = 0; i < 200 && refresh.disabled; i++) await tick(); await tick(); };
    await settle();
    check(document.documentElement.dataset.ticketNotificationsUi === 'arrow', 'Arrow island mounted');
    check(!monitor.checked && !monitor.disabled, 'monitoring starts off for owner');
    check(!fixture.calls.includes('permission') && !fixture.calls.includes('worker-register'), 'no automatic permission or worker registration');
    check(element('ticketMonitoringStatus').textContent.includes('Last check:'), 'last observation displayed');
    toggle.click(); toggle.click(); await settle();
    check(fixture.calls.filter(call => call === 'permission').length === 1, 'one prompt per deliberate click');
    check(!fixture.subscribed && element('ticketNotificationMessage').textContent.includes('not granted'), 'dismissed prompt truthful');
    fixture.answer = 'denied'; toggle.click(); await settle();
    check(toggle.disabled && element('ticketNotificationSupport').textContent.includes('blocked'), 'denied permission explains settings');
    fixture.permission = 'default'; fixture.answer = 'granted'; refresh.click(); await settle();
    toggle.click(); await settle();
    check(fixture.subscribed && toggle.textContent.includes('Disable'), 'subscription saved');
    check(fixture.calls.indexOf('permission') < fixture.calls.indexOf('worker-register'), 'permission requested before worker awaits');
    check(fixture.calls.some(call => call.endsWith(':subscribe')), 'subscription reaches server');
    const promptCount = fixture.calls.filter(call => call === 'permission').length;
    refresh.click(); await settle();
    check(fixture.calls.filter(call => call === 'permission').length === promptCount, 'refresh never prompts');
    monitor.click(); await settle();
    check(fixture.enabled && monitor.checked, 'monitoring switch persists accepted value');
    fixture.failures = 1; monitor.click(); await settle();
    check(fixture.enabled && monitor.checked && monitor.disabled, 'uncertain change preserves confirmed value and requires refresh');
    refresh.click(); await settle();
    fixture.failures = 1; toggle.click(); await settle();
    check(fixture.subscribed && fixture.browserSubscription, 'failed server unsubscribe preserves browser subscription');
    refresh.click(); await settle();
    toggle.click(); await settle();
    check(!fixture.subscribed && !fixture.browserSubscription, 'unsubscribe removes server and browser subscription');
    check(fixture.calls.findIndex(call => call.endsWith(':unsubscribe')) < fixture.calls.indexOf('browser-unsubscribe'), 'server unsubscribe happens before browser cleanup');
    fixture.owner = false; refresh.click(); await settle();
    check(monitor.parentElement.hidden && !toggle.disabled, 'administrator can subscribe without owner switch');
    check(element('ticketNotifications').scrollWidth <= element('ticketNotifications').clientWidth && document.documentElement.scrollWidth <= innerWidth, 'no horizontal overflow');
    fixture.revoked = true; refresh.click(); await settle();
    check(toggle.disabled && monitor.disabled && element('ticketNotificationMessage').textContent.includes('access'), 'revoked access disables controls');
    check(errors.length === 0, 'no browser errors');
  }
  run().catch(error => errors.push(error.message)).finally(() => {
    const result = document.createElement('pre'); result.hidden = true; result.id = 'fixtureResult';
    result.textContent = JSON.stringify({ checks, errors }); document.body.append(result);
  });
}

test('owner/admin notification controls handle permissions, saved state and access failure in a browser', { timeout: 120000 }, async () => {
  const browser = await findBraveBrowser();
  assert.ok(browser, 'Brave required');
  const bundle = await build({ entryPoints: [new URL('./notifications-source.js', import.meta.url).pathname], bundle: true, write: false, format: 'iife' });
  const css = await readFile(new URL('../internal/web/static/notifications.css', import.meta.url), 'utf8');
  const server = createServer((req, res) => {
    res.setHeader('Content-Type', 'text/html; charset=utf-8');
    const zoom = req.url.includes('zoom=2') ? 2 : 1;
    res.end(`<!doctype html><html><meta name="viewport" content="width=device-width,initial-scale=1"><style>html{zoom:${zoom}}body{margin:0;font-family:system-ui}*{box-sizing:border-box}#ticketNotifications{width:100%}button{font:inherit}${css}</style><body><section id="ticketNotifications" class="ticket-notifications"></section><script>(${fixtureEnvironment})();</script><script>${bundle.outputFiles[0].text}</script><script>(${browserProbe})();</script></body></html>`);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  try {
    for (const [width, zoom] of [[320,1], [390,2], [1440,1]]) {
      const result = await renderBraveDOM(browser, `http://127.0.0.1:${server.address().port}/?zoom=${zoom}`, {
        windowSize: `${width},900`, waitExpression: '!!document.querySelector("#fixtureResult")', timeoutMillis: 20000
      });
      const match = result.stdout.match(/<pre hidden="" id="fixtureResult">([^<]+)<\/pre>/);
      assert.ok(match, result.stdout);
      const report = JSON.parse(match[1].replaceAll('&quot;', '"').replaceAll('&amp;', '&'));
      assert.deepEqual(report.errors, [], `${width}px/${zoom}x: ${report.checks.join(', ')}`);
      assert.equal(report.checks.length, 20);
    }
  } finally { await new Promise(resolve => server.close(resolve)); }
});
