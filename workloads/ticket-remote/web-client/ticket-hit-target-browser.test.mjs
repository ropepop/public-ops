import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import http from 'node:http';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import { findBraveBrowser, renderBraveDOM } from './brave-browser-test-helper.mjs';

const appSource = readFileSync(new URL('./ticket-app-source.js', import.meta.url), 'utf8');
const template = readFileSync(new URL('../internal/web/static/index.html.tmpl', import.meta.url), 'utf8');
const css = readFileSync(new URL('../internal/web/static/app.css', import.meta.url), 'utf8');
function sourceBetween(start, end) {
  const from = appSource.indexOf(start);
  const to = appSource.indexOf(end, from);
  assert.ok(from >= 0 && to > from);
  return appSource.slice(from, to);
}

async function fixtureBundle() {
  const result = await build({
    stdin: {
      resolveDir: fileURLToPath(new URL('.', import.meta.url)),
      contents: `
import { html, reactive } from '@arrow-js/core';
const experimentalMediaMount = document.getElementById('experimentalMediaMount');
let experimentalMediaMounted = false;
const CLIENT_HDR_ENGINE = 'client_webgpu_v2';
const CLIENT_HDR_DISPLAY_BOOSTS = [2, 3, 4, 5, 6];
const experimentalMediaState = reactive({
  enabled: false, label: 'HDR', boostSelectorAllowed: true, engine: CLIENT_HDR_ENGINE,
  status: 'Ready', preferenceStatus: 'Saved', engineStatus: 'Ready', boostStatus: 'Saved'
});
const changes = [];
const experimentalMediaPreferenceController = { choose(value) {
  changes.push(value); experimentalMediaState.enabled = value;
} };
function chooseExperimentalHDRBoost() {}
function syncExperimentalMediaSelectors() {}
${sourceBetween('function mountExperimentalMediaControl() {', '  function fetchExperimentalMediaCapability() {')}
const controlCodeHotspot = document.getElementById('controlCodeHotspot');
const ticketRegisterOverlay = document.getElementById('ticketRegisterOverlay');
const codeDialog = document.getElementById('controlCodeDialog');
const codeResultArea = document.getElementById('controlCodeResultArea');
let codeDialogOpen = false;
const pendingBrowserAction = null;
let busy = false;
let opens = 0;
${sourceBetween('function ticketRegisterOverlayOccupiesHotspot() {', '  function requestControlCodeFromHotspot(event) {')}
function controlCodeMutationLaneBusy() { return busy; }
function memberLimitBlocked() { return false; }
function openControlCodeDialog() {
  opens += 1; codeDialogOpen = true; codeDialog.hidden = false;
  document.body.classList.add('code-dialog-open'); return true;
}
${sourceBetween('function requestControlCodeFromHotspot(event) {', '  async function submitControlCodeRequest() {')}
controlCodeHotspot.addEventListener('click', requestControlCodeFromHotspot);
mountExperimentalMediaControl();
const result = { samples: [], changes, errors: [] };
const paint = () => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
function closeDialog() { codeDialogOpen = false; codeDialog.hidden = true; document.body.classList.remove('code-dialog-open'); }
function require(value, message) { if (!value) throw new Error(message); }
async function alignControl(id) {
  const element = document.getElementById(id);
  const targetY = innerHeight * 0.2;
  const initial = element.getBoundingClientRect();
  scrollBy(0, initial.top + initial.height / 2 - targetY);
  await paint();
  const rect = element.getBoundingClientRect();
  const x = rect.left + Math.min(10, rect.width / 2);
  const y = rect.top + rect.height / 2;
  require(x > 0 && (id === 'experimentalMediaHDRBoost' || x < innerWidth / 2) && y > 0 && y < innerHeight / 4,
    id + ' did not overlap the fixed hotspot in the test');
  const hit = document.elementFromPoint(x, y);
  result.samples.push({ expected: id, hit: hit && hit.id, x, y, scrollY });
  require(hit === element, id + ' was intercepted by ' + (hit && hit.id));
  return hit;
}
(async () => {
  try {
    document.body.style.scrollSnapType = 'none';
    document.documentElement.style.scrollSnapType = 'none';
    // More scroll room only; the actual template, CSS and Arrow HDR control
    // determine every tested control's geometry and stacking.
    const tail = document.createElement('div'); tail.style.height = '100vh'; document.body.append(tail);
    await paint();
    const hotspot = controlCodeHotspot.getBoundingClientRect();
    require(hotspot.left === 0 && hotspot.top === 0 && hotspot.width === innerWidth / 2 &&
      hotspot.height === innerHeight / 4, 'hotspot geometry changed');
    require(document.elementFromPoint(10, 10) === controlCodeHotspot, 'stage hotspot lost its target');
    document.elementFromPoint(10, 10).click();
    require(opens === 1 && !codeDialog.hidden, 'stage hotspot did not open its dialog');
    require(document.elementFromPoint(10, 10) !== controlCodeHotspot, 'dialog did not take precedence');
    closeDialog();

    const checkbox = await alignControl('experimentalMediaToggle');
    checkbox.click();
    await paint();
    require(changes.length === 1 && changes[0] === true && checkbox.checked,
      'hit-tested checkbox did not enable its preference');
    checkbox.click(); await paint();
    require(changes.length === 2 && changes[1] === false && !checkbox.checked,
      'hit-tested checkbox did not disable its preference');
    require(opens === 1, 'settings click opened the code dialog');
    await alignControl('requestControlCode');
    await alignControl('experimentalMediaHDRBoost');

    scrollTo(0, 0); await paint();
    busy = true; controlCodeHotspot.disabled = true;
    require(document.elementFromPoint(10, 10) !== controlCodeHotspot, 'busy hotspot intercepted input');
    requestControlCodeFromHotspot(); require(opens === 1, 'busy handler opened a dialog');
    busy = false; controlCodeHotspot.disabled = false;
    ticketRegisterOverlay.hidden = false;
    Object.assign(ticketRegisterOverlay.style, { left: '0px', top: '0px', width: '100px', height: '44px' });
    require(ticketRegisterOverlayOccupiesHotspot(), 'actual slider did not overlap the hotspot');
    controlCodeHotspot.disabled = ticketRegisterOverlayOccupiesHotspot();
    require(document.elementFromPoint(10, 10) !== controlCodeHotspot, 'slider lost pointer precedence');
    requestControlCodeFromHotspot(); require(opens === 1, 'slider overlap opened a dialog');
    ticketRegisterOverlay.hidden = true; controlCodeHotspot.disabled = false;
    codeResultArea.hidden = false; document.body.classList.add('control-code-result-visible');
    require(document.elementFromPoint(10, 10) !== controlCodeHotspot, 'result lost pointer precedence');
    requestControlCodeFromHotspot(); require(opens === 1, 'result handler opened a dialog');
    result.hotspotGeometry = { width: hotspot.width, height: hotspot.height };
    result.opens = opens;
  } catch (error) { result.errors.push(String(error && error.message || error)); }
  document.documentElement.dataset.probeResult = btoa(JSON.stringify(result));
  document.documentElement.dataset.probeComplete = 'true';
})();`
    },
    bundle: true, format: 'iife', write: false, logLevel: 'silent'
  });
  return result.outputFiles[0].text;
}

test('Brave hit testing gives scrolled settings precedence over the fixed code hotspot', async (t) => {
  const browser = await findBraveBrowser();
  if (!browser) { t.skip('no Brave executable is available'); return; }
  const bundle = await fixtureBundle();
  // Product scripts are removed: this fixture has no authenticated session,
  // media connection, reducer or phone path.
  const page = template.replace(/<script\b[^>]*>[\s\S]*?<\/script>/g, '')
    .replace(/{{[^}]*}}/g, '')
    .replace('</body>', '<script src="/fixture.js"></script></body>');
  const server = http.createServer((request, response) => {
    const pathname = new URL(request.url, 'http://127.0.0.1').pathname;
    if (pathname === '/fixture.js') { response.setHeader('Content-Type', 'text/javascript'); response.end(bundle); }
    else if (pathname === '/static/app.css') { response.setHeader('Content-Type', 'text/css'); response.end(css); }
    else if (pathname === '/') { response.setHeader('Content-Type', 'text/html'); response.end(page); }
    else { response.statusCode = 404; response.end(); }
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  try {
    for (const windowSize of ['1250,800', '390,844', '800,390']) {
      const output = await renderBraveDOM(browser, `http://127.0.0.1:${server.address().port}/`, { windowSize });
      const match = /data-probe-result="([^"]+)"/.exec(output.stdout);
      assert.ok(match, 'Brave did not return a completed probe');
      const result = JSON.parse(Buffer.from(match[1], 'base64').toString('utf8'));
      assert.deepEqual(result.errors, [], `${windowSize}: ${JSON.stringify(result)}`);
      assert.equal(result.samples.length, 3);
      assert.equal(result.opens, 1);
      t.diagnostic(JSON.stringify({ windowSize, ...result }));
    }
  } finally {
    const closed = new Promise((resolve) => server.close(resolve));
    server.closeAllConnections();
    await closed;
  }
});
