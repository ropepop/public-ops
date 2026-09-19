import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { build } from 'esbuild';
import { findBraveBrowser, renderBraveDOM } from './brave-browser-test-helper.mjs';

function probe() {
  const errors = [], checks = [];
  addEventListener('error', e => errors.push(e.message));
  addEventListener('unhandledrejection', e => errors.push(String(e.reason)));
  const pause = () => new Promise(resolve => setTimeout(resolve, 20));
  const check = (condition, name) => { if (!condition) throw Error(name); checks.push(name); };
  const button = text => [...document.querySelectorAll('button')].find(b => b.textContent.includes(text));
  async function run() {
    const opener = document.querySelector('#installTicket'), dialog = document.querySelector('dialog');
    check(!opener.hidden, 'button visible before install');
    check(getComputedStyle(opener).visibility === 'visible' && opener.getBoundingClientRect().height >= 36, 'button visibly rendered');
    check(document.querySelector('#presence').nextElementSibling.contains(opener), 'button below viewers');
    opener.click(); await pause();
    check(dialog.open && !dialog.querySelector('ol'), 'always asks first');
    check(dialog.lang === 'lv' && opener.textContent.includes('Pievienot'), 'Latvian default');
    button('English').click(); await pause();
    check(dialog.lang === 'en', 'English translation');
    for (const label of ['iPhone / iPad', 'Android · Chrome', 'Android · Firefox']) {
      button(label).click(); await pause();
      check(dialog.querySelectorAll('li').length === 4, label + ' four steps');
      check(dialog.querySelectorAll('.install-picture').length === (label.includes('iPhone') ? 3 : label.includes('Chrome') ? 2 : 1), label + ' real screenshots');
      for (const image of dialog.querySelectorAll('image')) {
        const bitmap = new Image(); bitmap.src = image.getAttribute('href'); await bitmap.decode();
        check(bitmap.naturalWidth > 300, label + ' screenshot loads');
      }
      button('Latviski').click(); await pause();
      check(dialog.lang === 'lv' && dialog.querySelectorAll('li').length === 4 && dialog.textContent.includes('Atver'), label + ' translates in place');
      button('English').click(); await pause();
      check(dialog.scrollWidth <= dialog.clientWidth, label + ' no horizontal overflow');
      check(document.activeElement.classList.contains('install-heading'), label + ' focus moves to heading');
      button('Change phone').click(); await pause();
    }
    button('Android · Chrome').click(); await pause();
    check(button('Install Ticket').hidden, 'no unsupported install button');
    let calls = 0;
    for (const outcome of ['dismissed', 'failure', 'accepted']) {
      const event = new Event('beforeinstallprompt', { cancelable: true });
      event.prompt = async () => { calls++; if (outcome === 'failure') throw Error('fixture'); return { outcome }; };
      window.dispatchEvent(event); await pause();
      check(event.defaultPrevented && !button('Install Ticket').hidden, outcome + ' delayed prompt available');
      button('Install Ticket').click(); button('Install Ticket').click(); await pause();
      check(button('Install Ticket').hidden, outcome + ' prompt consumed');
      check(dialog.querySelector('[role=status]').textContent.includes(outcome === 'dismissed' ? 'cancelled' : outcome === 'failure' ? 'could not open' : 'requested'), outcome + ' truthful status');
    }
    check(calls === 3, 'one call per prompt');
    button('Latviski').click(); await pause();
    check(dialog.querySelector('[role=status]').textContent.includes('pieprasīta'), 'prompt status translates');
    button('English').click(); await pause();
    dialog.close(); await pause();
    check(document.activeElement === opener, 'focus returns on close');
    opener.click(); await pause();
    check(!dialog.querySelector('ol'), 'reopen asks again');
    window.dispatchEvent(new Event('appinstalled')); await pause();
    check(opener.hidden, 'installed event hides invitation');
    dialog.close();
    check(errors.length === 0, 'no browser errors');
    return { checks, errors };
  }
  run().then(result => { const node = document.createElement('pre'); node.id = 'fixtureResult'; node.hidden = true; node.textContent = JSON.stringify(result); document.body.append(node); })
    .catch(error => { const node = document.createElement('pre'); node.id = 'fixtureResult'; node.hidden = true; node.textContent = JSON.stringify({ checks, errors: [...errors, error.message] }); document.body.append(node); });
}

async function startFixture() {
  const template = await readFile(new URL('../internal/web/static/index.html.tmpl', import.meta.url), 'utf8');
  const style = await readFile(new URL('../internal/web/static/app.css', import.meta.url), 'utf8') + template.match(/<style[^>]*>([\s\S]*?)<\/style>/)[1];
  const bundle = await build({ stdin: { contents: "import { mountInstallGuide } from './install-guide.mjs'; mountInstallGuide(document.querySelector('#installTicketMount'));", resolveDir: new URL('.', import.meta.url).pathname }, bundle: true, write: false, format: 'iife' });
  const server = createServer(async (req, res) => {
    if (/^\/static\/install-[a-z-]+\.(png|jpg)$/.test(req.url)) {
      res.setHeader('Content-Type', req.url.endsWith('.png') ? 'image/png' : 'image/jpeg');
      res.end(await readFile(new URL('../internal/web' + req.url, import.meta.url))); return;
    }
    res.setHeader('Content-Type', 'text/html; charset=utf-8');
    res.end(`<!doctype html><html lang="en"><meta name="viewport" content="width=device-width,initial-scale=1"><style>${style}</style><body class="details-visible"><main class="shell"><aside class="panel"><div id="presence">Viewers · 1 online</div><div id="installTicketMount"></div></aside></main><script>${bundle.outputFiles[0].text}</script>${req.url.includes('probe') ? `<script>(${probe})();</script>` : ''}</body></html>`);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  return { url: `http://127.0.0.1:${server.address().port}`, close: () => new Promise(resolve => server.close(resolve)) };
}

if (process.argv.includes('--serve')) {
  const fixture = await startFixture();
  console.log(fixture.url);
} else {
  test('installation guide browser journeys and native prompt lifecycle', { timeout: 120000 }, async () => {
    const browser = await findBraveBrowser();
    assert.ok(browser, 'Brave required');
    const fixture = await startFixture();
    try {
      for (const width of [320, 390, 1440]) {
        const rendered = await renderBraveDOM(browser, fixture.url + '/?probe', { windowSize: `${width},850`, waitExpression: '!!document.querySelector("#fixtureResult")' });
        const match = rendered.stdout.match(/<pre id="fixtureResult" hidden="">([^<]+)<\/pre>/);
        assert.ok(match, 'missing browser report');
        const result = JSON.parse(match[1].replaceAll('&quot;', '"').replaceAll('&amp;', '&'));
        assert.deepEqual(result.errors, [], `${width}: ${result.checks.join(', ')}`);
        console.log(`${width}px: ${result.checks.length} journey checks passed`);
      }
    } finally { await fixture.close(); }
  });
}
