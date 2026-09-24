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
  const button = text => [...document.querySelectorAll('button')].find(b => !b.closest('[hidden]') && b.textContent.includes(text));
  const go = async text => { check(Boolean(button(text)), text + ' exists'); button(text).click(); await pause(); };
  const language = async value => {
    const selector = document.querySelector('.install-language');
    selector.value = value;
    selector.dispatchEvent(new Event('change', { bubbles: true }));
    await pause();
    check(document.querySelector('dialog').lang === value && selector.value === value, value + ' language selected');
  };
  const fits = label => {
    const dialog = document.querySelector('dialog'), header = dialog.querySelector('.install-header');
    check(dialog.scrollWidth <= dialog.clientWidth && header.scrollWidth <= header.clientWidth, label + ' fits without horizontal overflow');
    check(dialog.querySelector('.install-language').getBoundingClientRect().height >= 44, 'language selector touch target');
  };
  const russian = label => {
    const dialog = document.querySelector('dialog');
    check([...dialog.querySelectorAll('li p, li strong, .install-note p, .install-intro')].every(node => /[А-Яа-яЁё]/.test(node.textContent)), label + ' instructions are Russian');
    check(/[А-Яа-яЁё]/.test(dialog.querySelector('.install-header button').getAttribute('aria-label')), 'Russian close label');
    check(dialog.querySelector('.install-language').getAttribute('aria-label') === 'Язык инструкции', 'Russian language selector label');
    fits(label + ' Russian');
  };
  const back = () => go('← Back');
  const page = name => check(document.querySelector('dialog').dataset.installPage === name, 'page is ' + name);
  const browserPath = async label => { await go('Android'); await go('Browser installation'); await go(label); };
  async function run() {
    const opener = document.querySelector('#installTicket'), dialog = document.querySelector('dialog');
    check(!opener.hidden, 'button visible before install');
    check(getComputedStyle(opener).visibility === 'visible' && opener.getBoundingClientRect().height >= 36, 'button visibly rendered');
    check(document.querySelector('#presence').nextElementSibling.contains(opener), 'button below viewers');
    opener.click(); await pause();
    check(dialog.open && !dialog.querySelector('ol'), 'always asks first');
    check(dialog.lang === 'lv' && opener.textContent.includes('instalēšanas'), 'Latvian default');
    check(dialog.querySelectorAll('.install-choices:not([hidden]) button').length === 2, 'only OS choices at root');
    await language('en');
    check(dialog.lang === 'en', 'English translation');
    check(dialog.querySelector('h2').textContent === 'Which phone are you using?' && dialog.querySelectorAll('.install-intro').length === 1, 'one clear phone choice and concise introduction');
    check([...dialog.querySelectorAll('.install-choices:not([hidden]) button')].every(node => node.getBoundingClientRect().height >= 80 && getComputedStyle(node).borderStyle === 'solid'), 'phone choices are distinct touch cards');
    check([...dialog.querySelector('.install-language').options].map(option => option.value + ':' + option.lang).join(',') === 'lv:lv,en:en,ru:ru', 'three self-labelled language choices');
    await language('ru');
    check(/[А-Яа-яЁё]/.test(opener.textContent) && /[А-Яа-яЁё]/.test(dialog.querySelector('h2').textContent), 'Russian opener and root title');
    russian('OS menu');
    await go('Android'); page('android'); russian('Android menu');
    check([...dialog.querySelectorAll('.install-choices:not([hidden]) button')].every(node => /[А-Яа-яЁё]/.test(node.textContent)), 'both Android options translated');
    check(dialog.querySelectorAll('.install-intro').length === 0 && dialog.textContent.includes('Значок на главном экране') && dialog.textContent.includes('Отдельное приложение'), 'Android methods explain their difference without repeating the introduction');
    dialog.querySelector('.install-choices:not([hidden]) button').click(); await pause(); page('browsers'); russian('browser menu');
    check([...dialog.querySelectorAll('.install-choices:not([hidden]) button span')].every(node => /[А-Яа-яЁё]/.test(node.textContent)), 'both browser descriptions translated');
    await language('en'); await back(); await back(); page('os');
    for (const label of (window.fixtureInstalled ? [] : ['iPhone / iPad', 'Chrome', 'Firefox'])) {
      if (label === 'iPhone / iPad') await go(label);
      else await browserPath(label);
      check(dialog.querySelectorAll('li').length === 4, label + ' four steps');
      check(dialog.querySelectorAll('.install-picture').length === (label.includes('iPhone') ? 3 : label.includes('Chrome') ? 2 : 1), label + ' real screenshots');
      for (const image of dialog.querySelectorAll('image')) {
        const bitmap = new Image(); bitmap.src = image.getAttribute('href'); await bitmap.decode();
        check(bitmap.naturalWidth > 300, label + ' screenshot loads');
      }
      const previousPage = dialog.dataset.installPage;
      await language('lv');
      check(dialog.lang === 'lv' && dialog.querySelectorAll('li').length === 4 && dialog.textContent.includes('Atver'), label + ' translates in place');
      check(dialog.dataset.installPage === previousPage, 'translation preserves location');
      check([...dialog.querySelectorAll('.install-picture svg')].every(image => image.getAttribute('aria-label') === image.closest('li').querySelector('strong').textContent), 'image descriptions translate with steps');
      await language('ru');
      russian(label);
      check(dialog.dataset.installPage === previousPage && dialog.querySelectorAll('li').length === 4, label + ' Russian preserves location and steps');
      check([...dialog.querySelectorAll('.install-picture svg')].every(image => image.getAttribute('aria-label') === image.closest('li').querySelector('strong').textContent), 'Russian image descriptions match step headings');
      check([...dialog.querySelectorAll('figcaption')].every(node => /[А-Яа-яЁё]/.test(node.textContent)), 'Russian screenshot attribution');
      await language('en');
      check(dialog.scrollWidth <= dialog.clientWidth, label + ' no horizontal overflow');
      check(document.activeElement.classList.contains('install-heading'), label + ' focus moves to heading');
      await back();
      if (label !== 'iPhone / iPad') { page('browsers'); await back(); page('android'); await back(); }
      page('os');
    }
    await browserPath('Chrome');
    if (!window.fixtureInstalled) {
    check(dialog.querySelector('.install-chrome button').hidden, 'no unsupported install button');
    let calls = 0;
    for (const outcome of ['dismissed', 'failure', 'accepted']) {
      const event = new Event('beforeinstallprompt', { cancelable: true });
      event.prompt = async () => { calls++; if (outcome === 'failure') throw Error('fixture'); return { outcome }; };
      window.dispatchEvent(event); await pause();
      check(event.defaultPrevented && !dialog.querySelector('.install-chrome button').hidden, outcome + ' delayed prompt available');
      await back(); await go('Chrome');
      button('Install Ticket').click(); button('Install Ticket').click(); await pause();
      check(dialog.querySelector('.install-chrome button').hidden, outcome + ' prompt consumed');
      check(dialog.querySelector('[role=status]').textContent.includes(outcome === 'dismissed' ? 'cancelled' : outcome === 'failure' ? 'could not open' : 'requested'), outcome + ' truthful status');
      await language('ru');
      check(/[А-Яа-яЁё]/.test(dialog.querySelector('[role=status]').textContent), outcome + ' Russian status');
      await language('en');
    }
    check(calls === 3, 'one call per prompt');
    await language('lv');
    check(dialog.querySelector('[role=status]').textContent.includes('pieprasīta'), 'prompt status translates');
    await language('en');
    window.dispatchEvent(new Event('appinstalled')); await pause();
    } else {
      const event = new Event('beforeinstallprompt', { cancelable: true });
      event.prompt = () => { throw Error('must not prompt from installed app'); };
      window.dispatchEvent(event); await pause();
    }
    check(!opener.hidden && !button('Install Ticket') && !dialog.querySelector('ol'), 'installed app keeps options without another install');
    check(dialog.textContent.includes('already open as an app'), 'installed state explained');
    await language('ru');
    check(/[А-Яа-яЁё]/.test(dialog.querySelector('[role=status]').textContent) && !dialog.querySelector('ol'), 'Russian installed message without reinstallation');
    russian('installed guide');
    await language('en');
    await back(); await back(); page('android');
    check(dialog.querySelectorAll('.install-choices:not([hidden]) button').length === 2 && dialog.querySelectorAll('.install-choices:not([hidden]) .install-choice-icon[aria-hidden=true]').length === 2, 'Android has two labelled icon choices');
    await go('Full-screen viewer'); page('native');
    check(dialog.querySelectorAll('li').length === 3, 'shared viewer setup appears once');
    const download = dialog.querySelector('.install-download');
    check(download.href === 'https://github.com/cylonid/NativeAlphaForAndroid/releases/tag/v1.5.2' && download.rel.includes('noopener'), 'official versioned download');
    const options = [...dialog.querySelectorAll('.install-option')];
    check(options.length === 2 && options[0].querySelector('.install-recommended'), 'GitHub recommended before Store alternative');
    const stores = [...dialog.querySelectorAll('.install-store-links a')];
    check(stores.map(link => link.href).join(',') === 'https://play.google.com/store/apps/details?id=com.cylonid.nativealpha,https://play.google.com/store/apps/details?id=com.cylonid.nativealpha.pro', 'free and paid official Store destinations');
    check(stores.every(link => link.target === '_blank' && link.rel.includes('noopener') && link.rel.includes('noreferrer')), 'Store links isolate external tabs');
    check(stores[1].textContent.includes('paid') && options[1].textContent.includes('free version hides the browser address bar'), 'Store cost and display limits explicit');
    check(dialog.querySelectorAll('li')[2].textContent.includes('Skip this step in the free Play Store version'), 'free Store skips immersive settings');
    const githubHelp = options[0].querySelector('details');
    check(!githubHelp.open, 'long GitHub instructions initially tucked away');
    githubHelp.querySelector('summary').click(); await pause();
    check(githubHelp.open && githubHelp.textContent.includes('Assets') && githubHelp.querySelector('code').textContent === 'NativeAlpha-extendedGithub-universal-release-v1.5.2.apk', 'GitHub instructions name exact APK');
    check(githubHelp.textContent.includes('No GitHub account or app is needed') && githubHelp.textContent.includes('open the release in your browser'), 'browser fallback without GitHub account');
    fits('expanded GitHub instructions');
    const address = dialog.querySelector('#installTicketURL');
    check(address.value === location.origin + '/' && address.readOnly, 'copyable root URL excludes queries and fragments');
    let copied;
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async value => { copied = value; } } });
    await go('Copy Ticket link');
    check(copied === location.origin + '/' && dialog.querySelector('[role=status]').textContent === 'Link copied.', 'copy success');
    await language('lv');
    check(dialog.lang === 'lv' && dialog.dataset.installPage === 'native' && dialog.querySelector('[role=status]').textContent === 'Saite nokopēta.', 'viewer and copy status translate in place');
    check(options[0].textContent.includes('Ieteicams') && stores[1].textContent.includes('Iegādāties'), 'Latvian recommendation and paid choice');
    fits('Latvian alternatives');
    await language('ru');
    russian('Native Alpha');
    check(dialog.dataset.installPage === 'native' && dialog.querySelectorAll('li').length === 3, 'Russian viewer preserves location and steps');
    check([...dialog.querySelectorAll('.install-option h3, .install-option p, .install-option a, summary')].every(node => /[А-Яа-яЁё]/.test(node.textContent)), 'Russian alternatives and disclosures translated');
    check(stores[1].textContent.includes('Купить'), 'Russian paid choice explicit');
    check(/[А-Яа-яЁё]/.test(dialog.querySelector('[role=status]').textContent), 'Russian copy success');
    check(dialog.textContent.includes('Show expert settings') && dialog.textContent.includes('Start URL') && dialog.textContent.includes('Full Screen (Immersive Mode)'), 'Russian keeps actual app setting names');
    check([download, dialog.querySelector('.install-copy'), dialog.querySelector('label')].every(node => /[А-Яа-яЁё]/.test(node.textContent)), 'Russian download and link controls');
    await language('en');
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async () => { throw Error('denied'); } } });
    await go('Copy Ticket link');
    check(dialog.querySelector('[role=status]').textContent.includes('Automatic copying failed') && document.activeElement === address && address.selectionEnd === address.value.length, 'copy denial selects manual fallback');
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined });
    await go('Copy Ticket link');
    check(dialog.querySelector('[role=status]').textContent.includes('Automatic copying failed'), 'missing clipboard handled');
    await language('ru');
    check(/[А-Яа-яЁё]/.test(dialog.querySelector('[role=status]').textContent), 'Russian manual copy fallback');
    await language('en');
    check(dialog.scrollWidth <= dialog.clientWidth, 'viewer has no horizontal overflow');
    const linkSettings = dialog.querySelector('.install-link-settings');
    check(!linkSettings.open, 'link settings available on demand');
    linkSettings.querySelector('summary').click(); await pause();
    check(linkSettings.open && linkSettings.textContent.includes('Start URL') && linkSettings.textContent.includes('Show expert settings'), 'existing viewer settings explained');
    await back(); page('android');
    await go('Full-screen viewer');
    check(dialog.querySelectorAll('li').length === 3 && dialog.querySelector('#installTicketURL').value === location.origin + '/', 'viewer reopens with complete setup');
    check(dialog.querySelector('[role=status]').textContent === '', 'new viewer visit clears old copy status');
    await back();
    await go('Browser installation'); await go('Firefox');
    check(!dialog.querySelector('ol') && dialog.textContent.includes('already open as an app'), 'installed state survives menu navigation');
    await language('ru');
    dialog.close(); await pause();
    check(document.activeElement === opener, 'focus returns on close');
    opener.click(); await pause();
    check(!dialog.querySelector('ol'), 'reopen asks again');
    page('os');
    check(dialog.lang === 'ru' && dialog.querySelector('.install-language').value === 'ru', 'reopening retains chosen Russian language');
    check(!opener.hidden, 'installed event preserves installation options');
    dialog.close();
    check(errors.length === 0, 'no browser errors');
    return { checks, errors };
  }
  run().then(result => { const node = document.createElement('pre'); node.id = 'fixtureResult'; node.hidden = true; node.textContent = JSON.stringify(result); document.body.append(node); })
    .catch(error => { const node = document.createElement('pre'); node.id = 'fixtureResult'; node.hidden = true; node.textContent = JSON.stringify({ checks, errors: [...errors, error.message] }); document.body.append(node); });
}

async function startFixture() {
  const template = await readFile(new URL('../internal/web/static/index.html.tmpl', import.meta.url), 'utf8');
  const style = await readFile(new URL('../internal/web/static/app.css', import.meta.url), 'utf8') + await readFile(new URL('../internal/web/pwa/install-guide.css', import.meta.url), 'utf8') + template.match(/<style[^>]*>([\s\S]*?)<\/style>/)[1];
  const bundle = await build({ stdin: { contents: "import { mountInstallGuide } from './install-guide.mjs'; mountInstallGuide(document.querySelector('#installTicketMount'));", resolveDir: new URL('.', import.meta.url).pathname }, bundle: true, write: false, format: 'iife' });
  const server = createServer(async (req, res) => {
    if (/^\/pwa\/install-[a-z-]+\.(png|jpg)$/.test(req.url)) {
      res.setHeader('Content-Type', req.url.endsWith('.png') ? 'image/png' : 'image/jpeg');
      res.end(await readFile(new URL('../internal/web/static/' + req.url.split('/').pop(), import.meta.url))); return;
    }
    res.setHeader('Content-Type', 'text/html; charset=utf-8');
    const mode = new URL(req.url, 'http://localhost').searchParams.get('app');
    const installed = ['standalone', 'fullscreen'].includes(mode) ? `window.fixtureInstalled = true; const originalMatchMedia = window.matchMedia.bind(window); window.matchMedia = query => query.includes('display-mode: ${mode}') ? { matches: true, addEventListener() {} } : originalMatchMedia(query);` : '';
    res.end(`<!doctype html><html lang="en"><meta name="viewport" content="width=device-width,initial-scale=1"><style>${style}</style><body class="details-visible"><main class="shell"><aside class="panel"><div id="presence">Viewers · 1 online</div><div id="installTicketMount"></div></aside></main><script>${installed}${bundle.outputFiles[0].text}</script>${req.url.includes('probe') ? `<script>(${probe})();</script>` : ''}</body></html>`);
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
      for (const [width, mode] of [[320, ''], [390, ''], [1440, ''], [390, 'standalone'], [390, 'fullscreen']]) {
        const rendered = await renderBraveDOM(browser, fixture.url + '/?probe&app=' + mode + '&returnTo=private#ignored', { windowSize: `${width},850`, waitExpression: '!!document.querySelector("#fixtureResult")' });
        const match = rendered.stdout.match(/<pre id="fixtureResult" hidden="">([^<]+)<\/pre>/);
        assert.ok(match, 'missing browser report');
        const result = JSON.parse(match[1].replaceAll('&quot;', '"').replaceAll('&amp;', '&'));
        assert.deepEqual(result.errors, [], `${width} ${mode}: ${result.checks.join(', ')}`);
        console.log(`${width}px ${mode || 'browser'}: ${result.checks.length} journey checks passed`);
      }
    } finally { await fixture.close(); }
  });
}
