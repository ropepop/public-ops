import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { build } from 'esbuild';
import { findBraveBrowser, renderBraveDOM } from './brave-browser-test-helper.mjs';

test('HDR preserves source contrast at all brightness levels and the comparison holds one local picture', { timeout: 150000 }, async () => {
  const browser = await findBraveBrowser();
  assert.ok(browser, 'Brave is required for HDR verification');
  const bundle = await build({ stdin: { contents: `import { mountHDRComparison } from './client-hdr-comparison.mjs'; mountHDRComparison(document.getElementById('hdrContrastMount'));`, resolveDir: new URL('.', import.meta.url).pathname }, bundle: true, write: false, format: 'iife' });
  const template = await readFile(new URL('../internal/web/diagnostic/hdr-diagnostic.html.tmpl', import.meta.url), 'utf8');
  const probe = `
    const wait = async () => {
      for (let count = 0; count < 240; count++) {
        await new Promise(requestAnimationFrame);
        if (!document.getElementById('hdrContrastToggle').disabled) return;
      }
      throw new Error('comparison did not settle');
    };
    const check = (ok, reason) => { if (!ok) throw new Error(reason); };
    const result = {};
    try {
      await wait();
      const toggle = document.getElementById('hdrContrastToggle');
      const boost = document.getElementById('hdrContrastBoost');
      const source = document.getElementById('hdrContrastSDR');
      const fingerprint = () => source.toDataURL(); // synthetic test pixels only
      const original = fingerprint();
      check(source.width === 360 && source.height === 300, 'fixture dimensions');
      for (const value of [1, 2, 3, 4, 5, 6]) {
        boost.value = value;
        boost.dispatchEvent(new Event('change', { bubbles: true }));
        toggle.click();
        await wait();
        check(document.getElementById('hdrContrastStatus').textContent.startsWith('HDR ' + value + '×'), 'HDR presentation ' + value);
        check(fingerprint() === original, 'SDR source changed');
        toggle.click();
        await wait();
        check(document.getElementById('hdrContrastHDR').style.opacity === '0', 'SDR reveal');
      }
      const input = document.getElementById('hdrContrastFile');
      const local = document.createElement('canvas'); local.width = 72; local.height = 120;
      local.getContext('2d').fillRect(0, 0, 72, 120);
      const data = new DataTransfer(); data.items.add(new File([await new Promise(resolve => local.toBlob(resolve))], 'synthetic.png', { type: 'image/png' }));
      input.files = data.files; input.dispatchEvent(new Event('change', { bubbles: true }));
      await wait();
      check(source.width === 72 && source.height === 120, 'local source replacement');
      toggle.click(); await wait();
      check(document.getElementById('hdrContrastStatus').textContent.startsWith('HDR 6×'), 'local source HDR');
      const localFingerprint = fingerprint();
      for (let index = 0; index < 3; index++) {
        window.dispatchEvent(new PageTransitionEvent('pagehide', { persisted: true }));
        window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: true }));
        await wait();
        check(fingerprint() === localFingerprint, 'cached return preserves source');
        check(document.getElementById('hdrContrastStatus').textContent.startsWith('HDR 6×'), 'cached return restores HDR');
      }
      window.dispatchEvent(new PageTransitionEvent('pagehide'));
      check(source.width === 1 && input.files.length === 0, 'private source released');
      window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: true }));
      await wait();
      check(source.width === 360, 'restored page sample');
      result.passed = true;
    } catch (error) { result.error = String(error.stack || error); }
    document.getElementById('comparisonResult').textContent = JSON.stringify(result);
    document.documentElement.dataset.probeComplete = 'true';
  `;
  const server = createServer(async (request, response) => {
    try {
      const pathname = new URL(request.url, 'http://127.0.0.1').pathname;
      if (pathname === '/comparison') {
        response.setHeader('Content-Type', 'text/html');
        response.end(template.replace(/<script[^>]*>.*?<\/script>/s, '')
          .replace('<div id="hdrDiagnosticMount"></div>', '<section class="card" id="hdrContrastMount"></section><pre id="comparisonResult"></pre>')
          .replace('</body>', '<script src="/comparison.js"></script><script type="module">' + probe + '</script></body>'));
      } else if (pathname === '/comparison.js') {
        response.setHeader('Content-Type', 'text/javascript'); response.end(bundle.outputFiles[0].text);
      } else if (/^\/[a-z0-9-]+\.(mjs|html)$/.test(pathname)) {
        response.setHeader('Content-Type', pathname.endsWith('.mjs') ? 'text/javascript' : 'text/html');
        const body = await readFile(new URL('.' + pathname, import.meta.url), 'utf8');
        response.end(pathname === '/presentation-real-gpu.html'
          // Headless has no HDR display. Exercise the real GPU/controller with
          // the capability signal simulated; this is not physical HDR evidence.
          ? '<script>const nativeMatchMedia = matchMedia; window.matchMedia = query => query === "(dynamic-range: high)" ? {matches:true} : nativeMatchMedia(query);</script>' + body + '<script type="module">import "./presentation-real-gpu.mjs"; document.getElementById("run").click();</script>'
          : body);
      } else { response.writeHead(404); response.end(); }
    } catch (_) { response.writeHead(404); response.end(); }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const url = `http://127.0.0.1:${server.address().port}`;
  const report = (html, id) => JSON.parse(html.match(new RegExp('<pre id="' + id + '"[^>]*>([^<]+)</pre>'))[1].replaceAll('&quot;', '"').replaceAll('&amp;', '&').replaceAll('&gt;', '>').replaceAll('&lt;', '<'));
  try {
    const gpu = await renderBraveDOM(browser, url + '/client-hdr-real-gpu.html', { gpu: true, timeoutMillis: 30000, waitExpression: 'Boolean(globalThis.clientHDRRealGPUResult)' });
    const result = report(gpu.stdout, 'result');
    assert.equal(result.usable, true, JSON.stringify(result));
    assert.ok(result.results.every(path => path.result === 'passed' || path.result === 'unsupported'), JSON.stringify(result));
    console.log(JSON.stringify(result.results.map(({ label, result, reason, contrast, levels }) => ({ label, result, reason, contrast, levels }))));
    const presentation = await renderBraveDOM(browser, url + '/presentation-real-gpu.html', { gpu: true, timeoutMillis: 30000, waitExpression: 'document.getElementById("result")?.textContent.includes("\\"passed\\"")' });
    const lifecycle = report(presentation.stdout, 'result');
    assert.equal(lifecycle.passed, true, JSON.stringify(lifecycle));
    console.log(JSON.stringify(lifecycle));
    for (const width of [390, 1200]) {
      const comparison = await renderBraveDOM(browser, url + '/comparison', { gpu: true, windowSize: width + ',900', timeoutMillis: 30000 });
      assert.deepEqual(report(comparison.stdout, 'comparisonResult'), { passed: true });
    }
  } finally { await new Promise(resolve => server.close(resolve)); }
});
