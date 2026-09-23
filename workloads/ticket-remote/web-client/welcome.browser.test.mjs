import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { build } from 'esbuild';
import { findBraveBrowser, renderBraveDOM } from './brave-browser-test-helper.mjs';

const returnTo = '/?view=ticket&from=welcome';
const authURL = '/api/v1/auth/start?returnTo=' + encodeURIComponent(returnTo);
const json = value => JSON.stringify(value).replaceAll('<', '\\u003c');

function prepare(config) {
  window.fixture = config;
  window.fixtureReport = JSON.parse(sessionStorage.getItem('welcomeFixtureReport') || '{"checks":[],"errors":[]}');
  sessionStorage.setItem('welcomeFixtureConfig', JSON.stringify(config));
  window.fixtureSave = () => sessionStorage.setItem('welcomeFixtureReport', JSON.stringify(window.fixtureReport));
  addEventListener('error', event => window.fixtureReport.errors.push(event.message));
  addEventListener('unhandledrejection', event => window.fixtureReport.errors.push(String(event.reason)));
  addEventListener('pagehide', window.fixtureSave);
  const agents = {
    android: 'Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 Chrome/140.0.0.0 Mobile Safari/537.36',
    iphone: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Version/18.0 Mobile/15E148 Safari/604.1',
    ipad: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15) AppleWebKit/605.1.15 Version/18.0 Safari/605.1.15'
  };
  if (agents[config.platform]) Object.defineProperty(navigator, 'userAgent', { value: agents[config.platform], configurable: true });
  if (config.platform === 'ipad') {
    Object.defineProperty(navigator, 'platform', { value: 'MacIntel', configurable: true });
    Object.defineProperty(navigator, 'maxTouchPoints', { value: 5, configurable: true });
  }
  Object.defineProperty(navigator, 'languages', { value: config.languages, configurable: true });
  Object.defineProperty(navigator, 'language', { value: config.languages[0], configurable: true });
  if (!sessionStorage.getItem('welcomeFixtureSeeded')) {
    if (config.saved) localStorage.setItem('ticket.language', config.saved);
    if (config.ack) localStorage.setItem('ticket.welcomeAcknowledged', '1');
    sessionStorage.setItem('welcomeFixtureSeeded', '1');
  }
  if (config.standalone === 'ios') Object.defineProperty(navigator, 'standalone', { value: true, configurable: true });
  else if (config.standalone) {
    const original = window.matchMedia.bind(window);
    window.matchMedia = query => query.includes('display-mode: ' + config.standalone)
      ? { matches: true, addEventListener() {}, removeEventListener() {} } : original(query);
  }
  if (config.blocked) Object.defineProperty(window, 'localStorage', { configurable: true, get() { throw new DOMException('Storage blocked', 'SecurityError'); } });
}

function probe() {
  const { fixture: config, fixtureReport: report } = window;
  const pause = () => new Promise(resolve => setTimeout(resolve, 20));
  const check = (value, message) => { if (!value) throw Error(message); report.checks.push(message); window.fixtureSave(); };
  const finish = error => {
    if (error) report.errors.push(error.message);
    const node = document.createElement('pre'); node.id = 'fixtureResult'; node.hidden = true;
    node.textContent = JSON.stringify(report); document.body.append(node);
  };
  const language = async value => {
    const selector = document.querySelector('#viewerLanguage');
    selector.value = value; selector.dispatchEvent(new Event('change', { bubbles: true })); await pause();
    check(document.documentElement.lang === value, value + ' language selected');
  };
  const fits = () => check(document.documentElement.scrollWidth <= innerWidth, 'page fits viewport');
  async function run() {
    if (config.bypass || (!config.blocked && localStorage.getItem('ticket.welcomeAcknowledged') === '1')) {
      await new Promise(resolve => setTimeout(resolve, 300));
      throw Error('expected direct navigation to authorization');
    }
    await pause();
    const content = document.querySelector('#welcomeContent');
    if (config.standalone) {
      check(!content.hidden, 'installed first launch shows authorization');
      check(!document.querySelector('#welcomeInstructions, #installTicketDialog'), 'installed app has no installation prompt');
      check(document.querySelector('#continueToAuth')?.href.includes('/api/v1/auth/start'), 'installed app offers sign in');
      check(document.querySelector('a[href="/?enterInvite=1"]'), 'installed app offers invitation entry');
      fits(); finish(); return;
    }
    check(content && !content.hidden && getComputedStyle(content).display !== 'none', 'fresh mobile visit shows welcome');
    check(document.querySelector('#welcomeMessage').textContent.trim().length > 20, 'welcome has guidance');
    check(document.querySelector('#viewerLanguage').value === config.expectedLanguage, 'browser or saved language selected');
    check(!document.querySelector('#welcomeFallback') || document.querySelector('#welcomeFallback').hidden || getComputedStyle(document.querySelector('#welcomeFallback')).display === 'none', 'fallback is hidden after enhancement');
    check(!document.querySelector('video, canvas, #ticketStage, #presence'), 'welcome contains no private viewer');
    check([...document.scripts].every(script => !/\/static\/(?:app|spacetime-client)\.js/.test(script.src)), 'private viewer bundles are absent');
    const continueLink = document.querySelector('#continueToAuth');
    check(continueLink && new URL(continueLink.href).pathname === '/api/v1/auth/start', 'continue uses existing authorization');
    check(new URL(continueLink.href).searchParams.get('returnTo') === config.returnTo, 'authorization preserves return destination');
    for (const node of [continueLink, document.querySelector('#welcomeInstructions'), document.querySelector('#viewerLanguage')]) {
      check(node.getBoundingClientRect().height >= 44, 'welcome control has touch target');
    }
    fits();
    if (config.action === 'inspect') { finish(); return; }
    if (config.action === 'continue') { continueLink.click(); return; }

    const instructionButton = document.querySelector('#welcomeInstructions');
    instructionButton.click(); await pause();
    let dialog = document.querySelector('#installTicketDialog');
    check(dialog.open && dialog.dataset.installPage === (config.platform === 'android' ? 'android' : 'ios'), 'instructions open at detected device');
    check(dialog.querySelector('#installContinueAuth')?.href === continueLink.href, 'guide offers same authorization destination');
    if (!config.blocked) check(localStorage.getItem('ticket.welcomeAcknowledged') === '1', 'opening instructions remembers acknowledgement');
    if (config.action === 'guide-continue') { dialog.querySelector('#installContinueAuth').click(); return; }
    if (config.blocked) { dialog.close(); await pause(); continueLink.click(); return; }

    const close = () => { dialog.close(); };
    close(); await pause();
    check(!dialog.open && document.activeElement === instructionButton && !content.hidden, 'closing guide returns to welcome and opener');
    for (const value of ['lv', 'ru', 'en']) {
      await language(value);
      const message = document.querySelector('#welcomeMessage').textContent;
      check(value === 'ru' ? /[А-Яа-яЁё]/.test(message) : value === 'lv' ? /[āēīūšģķļņčž]/i.test(message) : /home screen/i.test(message), value + ' guidance is translated');
      fits();
    }
    instructionButton.click(); await pause();
    check(dialog.lang === 'en', 'guide shares welcome language');
    dialog.querySelector('.install-back').click(); await pause();
    check(dialog.dataset.installPage === 'os', 'Back allows changing device');
    const iosChoice = [...dialog.querySelectorAll('.install-choices button')].find(button => button.textContent.includes('iPhone'));
    iosChoice.click(); await pause();
    check(dialog.dataset.installPage === 'ios', 'can switch to iPhone instructions');
    for (const image of dialog.querySelectorAll('image')) {
      const bitmap = new Image(); bitmap.src = image.getAttribute('href'); await bitmap.decode();
      check(bitmap.naturalWidth > 300 && new URL(bitmap.src).pathname.startsWith('/pwa/install-'), 'public installation illustration loads');
    }
    check(dialog.scrollWidth <= dialog.clientWidth, 'guide fits small viewport');
    const selector = dialog.querySelector('.install-language'); selector.value = 'ru'; selector.dispatchEvent(new Event('change', { bubbles: true })); await pause();
    check(document.querySelector('#viewerLanguage').value === 'ru' && localStorage.getItem('ticket.language') === 'ru', 'guide updates shared saved language');
    close(); await pause();
    window.fixtureSave();
    location.reload();
  }
  run().catch(finish);
}

function authorizationProbe() {
  const config = JSON.parse(sessionStorage.getItem('welcomeFixtureConfig'));
  const report = JSON.parse(sessionStorage.getItem('welcomeFixtureReport') || '{"checks":[],"errors":[]}');
  const check = (value, name) => { if (!value) report.errors.push(name); else report.checks.push(name); };
  check(new URL(location.href).searchParams.get('returnTo') === config.returnTo, 'arrived at authorization with return destination');
  if (!config.bypass && !config.blocked) check(localStorage.getItem('ticket.welcomeAcknowledged') === '1', 'acknowledgement persists through navigation');
  if (config.action === 'journey') check(localStorage.getItem('ticket.language') === 'ru', 'language persists through reload');
  check(!document.querySelector('#welcomeContent'), 'authorization has no installation prompt');
  const node = document.createElement('pre'); node.id = 'fixtureResult'; node.hidden = true;
  node.textContent = JSON.stringify(report); document.body.append(node);
}

async function startFixture() {
  const template = await readFile(new URL('../internal/web/welcome/index.html.tmpl', import.meta.url), 'utf8');
  const bundle = await build({ entryPoints: [new URL('./welcome-source.js', import.meta.url).pathname], bundle: true, write: false, format: 'iife' });
  const requests = [];
  const server = createServer(async (req, res) => {
    const url = new URL(req.url, 'http://localhost');
    requests.push(url.pathname);
    const config = {
      platform: url.searchParams.get('platform') || 'android', languages: (url.searchParams.get('lang') || 'en').split(','),
      saved: url.searchParams.get('saved') || '', ack: url.searchParams.has('ack'), standalone: url.searchParams.get('standalone') || '',
      blocked: url.searchParams.has('blocked'), action: url.searchParams.get('action') || 'inspect',
      bypass: url.searchParams.has('bypass'), expectedLanguage: url.searchParams.get('expected') || 'en', returnTo
    };
    try {
      if (url.pathname === '/api/v1/auth/start') {
        res.setHeader('Content-Type', 'text/html; charset=utf-8');
        res.end(`<!doctype html><html lang="en"><body><h1>Fixture sign-in</h1><script>(${authorizationProbe})();</script></body></html>`); return;
      }
      if (url.pathname === '/pwa/welcome.js') { res.setHeader('Content-Type', 'text/javascript'); res.end(bundle.outputFiles[0].text); return; }
      if (url.pathname === '/manifest.webmanifest' || /^\/pwa\/(?:install-guide\.css|install-[a-z-]+\.(?:png|jpg)|icon-[a-z0-9-]+\.png|apple-touch-icon\.png)$/.test(url.pathname)) {
        const file = url.pathname === '/manifest.webmanifest' ? '/pwa/manifest.webmanifest' : url.pathname.replace('/pwa/install-', '/static/install-').replace('/static/install-guide.css', '/pwa/install-guide.css');
        res.setHeader('Content-Type', url.pathname.endsWith('.css') ? 'text/css' : url.pathname.endsWith('.png') ? 'image/png' : url.pathname.endsWith('.webmanifest') ? 'application/manifest+json' : 'image/jpeg');
        res.end(await readFile(new URL('../internal/web' + file, import.meta.url))); return;
      }
      if (url.pathname !== '/') { res.writeHead(404); res.end(); return; }
      res.setHeader('Content-Type', 'text/html; charset=utf-8');
      const prepareScript = `(${prepare})(${json(config)});`;
      const page = template.replace(/data-auth-url="[^"]*"/, `data-auth-url="${authURL.replaceAll('&', '&amp;')}"`)
        .replace(/\{\{\s*\.AuthURL\s*\}\}/g, authURL.replaceAll('&', '&amp;'))
        .replace(/\{\{[^}]+\}\}/g, '')
        .replace('</head>', `<script>${prepareScript}</script></head>`)
        .replace('</body>', url.searchParams.has('probe') ? `<script>addEventListener('DOMContentLoaded', () => (${probe})());</script></body>` : '</body>');
      res.end(page);
    } catch (error) { res.writeHead(500); res.end(error.message); }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  return { url: `http://127.0.0.1:${server.address().port}`, requests, close: () => new Promise(resolve => server.close(resolve)) };
}

if (process.argv.includes('--serve')) {
  const fixture = await startFixture(); console.log(fixture.url + '/?platform=android&lang=en');
} else {
  test('first-visit welcome, platform guidance and quiet return journeys', { timeout: 120000 }, async () => {
    const browser = await findBraveBrowser(); assert.ok(browser, 'Brave required');
    const fixture = await startFixture();
    try {
      const cases = [
        ['Android and shared guide', 'platform=android&lang=de-DE,ru-RU,en&expected=ru&action=journey', 320],
        ['iPhone continue', 'platform=iphone&lang=lv-LV&expected=lv&action=continue', 390],
        ['iPad desktop agent and saved language', 'platform=ipad&lang=en&saved=ru&expected=ru&action=guide-continue', 768],
        ['unsupported languages', 'platform=android&lang=de-DE,fr&expected=en', 390],
        ['blocked storage', 'platform=android&lang=ru&expected=ru&blocked&action=guide-continue', 320],
        ['acknowledged browser', 'platform=android&ack&bypass', 390],
        ['desktop', 'platform=desktop&bypass', 1440],
        ['iOS installed', 'platform=iphone&standalone=ios', 390],
        ['standalone installed', 'platform=android&standalone=standalone', 390],
        ['fullscreen installed', 'platform=android&standalone=fullscreen', 390]
      ];
      for (const [name, query, width] of cases) {
        const rendered = await renderBraveDOM(browser, fixture.url + '/?probe&' + query, { windowSize: `${width},850`, waitExpression: '!!document.querySelector("#fixtureResult")' });
        const match = rendered.stdout.match(/<pre id="fixtureResult" hidden="">([^<]+)<\/pre>/);
        assert.ok(match, name + ': missing browser report');
        const result = JSON.parse(match[1].replaceAll('&quot;', '"').replaceAll('&amp;', '&'));
        assert.deepEqual(result.errors, [], name + ': ' + result.checks.join(', '));
        console.log(name + ': ' + result.checks.length + ' checks passed');
      }
      assert.ok(fixture.requests.every(path => path === '/' || path === '/api/v1/auth/start' || path === '/manifest.webmanifest' || path === '/favicon.ico' || path.startsWith('/pwa/')), 'welcome never requests private assets or APIs');
    } finally { await fixture.close(); }
  });
}
