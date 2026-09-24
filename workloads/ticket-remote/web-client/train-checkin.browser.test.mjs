import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { build } from 'esbuild';
import { findBraveBrowser, renderBraveDOM } from './brave-browser-test-helper.mjs';

function probe() {
  const checks = [], errors = [];
  addEventListener('error', e => errors.push(e.message));
  addEventListener('unhandledrejection', e => errors.push(String(e.reason)));
  const pause = () => new Promise(resolve => setTimeout(resolve, 30));
  const check = (value, name) => { if (!value) throw Error(name); checks.push(name); };
  const button = text => [...document.querySelectorAll('button')].find(b => b.textContent.includes(text) && b.checkVisibility());
  async function run() {
    const dialog = document.querySelector('.checkin-dialog'), toggle = document.querySelector('.checkin-toggle');
    const lang = async value => { const select = document.querySelector('#viewerLanguage'); select.value = value; select.dispatchEvent(new Event('change', { bubbles: true })); await pause(); };
    fixture.update(); await pause();
    check(toggle.querySelector('span').textContent === 'Reģistrēšanās vilcienā' && document.querySelector('#activateTicket').textContent === 'Reģistrēt atvērto biļeti tagad', 'Latvian menu labels');
    check(fixture.claims.length === 1 && !dialog.open, 'one opening claim without display');
    check(!document.querySelector('details.checkin-disclosure'), 'no intermediate disclosure');
    toggle.click(); await pause();
    check(dialog.open && document.activeElement.closest('dialog'), 'one tap opens modal and captures focus');
    check(dialog.textContent.includes('Vēl nav aktīvu'), 'empty summary');
    button('Reģistrēties vilcienā').click(); await pause();
    check(button('Apstiprināt').disabled, 'explicit selection required');
    for (const direction of ['towards_riga', 'away_from_riga']) {
      dialog.querySelector(`input[value="${direction}"]`).click(); await pause();
      check(button('Apstiprināt').disabled, direction + ' requires carriage');
      for (const carriage of [1, 2, 3, 4]) {
        dialog.querySelector(`input[name=checkinCarriage][value="${carriage}"]`).click(); await pause();
        check(!button('Apstiprināt').disabled && dialog.querySelector('.checkin-carriage.selected').textContent.includes(`${carriage}.`), direction + ' carriage ' + carriage);
      }
    }
    check(fixture.submissions.length === 0, 'selection does not submit');
    await lang('ru');
    check(toggle.querySelector('span').textContent === 'Отметка в поезде' && document.querySelector('#activateTicket').textContent === 'Зарегистрировать открытый билет сейчас', 'Russian menu labels');
    check(dialog.querySelector('h2').textContent === 'Отметка в поезде' && dialog.lang === 'ru', 'Russian dialog');
    check(dialog.querySelector('input[name=checkinCarriage]:checked').value === '4', 'language preserves selections');
    check(dialog.scrollWidth <= dialog.clientWidth, 'Russian fits narrow screen');
    check(localStorage.getItem('ticket.language') === 'ru', 'language remembered');
    await lang('en');
    check(toggle.querySelector('span').textContent === 'Train check-in' && document.querySelector('#activateTicket').textContent === 'Register open ticket now', 'English menu labels');
    check(button('Confirm check-in') && document.documentElement.lang === 'en', 'English dialog and document language');
    fixture.fail = true; button('Confirm check-in').click(); await pause();
    check(dialog.open && dialog.querySelector('[role=alert]').textContent.includes('Could not confirm'), 'failure keeps form with translated error');
    const retryId = fixture.submissions[0][0];
    fixture.fail = false; button('Confirm check-in').click(); await pause();
    check(fixture.submissions[1][0] === retryId, 'retry reuses request id');
    check(dialog.open && !dialog.querySelector('.checkin-success').hidden, 'success stays in sheet');
    check(dialog.querySelector('.checkin-success').textContent.includes('40 minutes'), 'success confirmation');
    button('View ticket').click(); await pause();
    check(!dialog.open && document.activeElement === toggle, 'close restores list focus');
    fixture.state.checkin = { ...fixture.state.checkin, revision: retryId, direction: 'away_from_riga', carriage: 4, status: 'active', activeUntilMs: fixture.now + 2400000 };
    fixture.state.checkinGroups = ['towards_riga', 'away_from_riga'].flatMap(direction => [1,2,3,4].map(carriage => ({ id:direction+carriage, direction, carriage, status:'active', count:carriage, latestAtMs:fixture.now - 60000 })));
    fixture.state.checkinGroups.push({ id:'expired', direction:'towards_riga',carriage:1,status:'expired',count:80,latestAtMs:fixture.now });
    fixture.state.checkinGroups.push({ id:'out', direction:'towards_riga',carriage:1,status:'checked_out',count:90,latestAtMs:fixture.now });
    fixture.update(); await pause();
    check(!dialog.open && fixture.claims.length === 1, 'arrivals do not interrupt');
    toggle.click(); await pause();
    check(dialog.querySelectorAll('.checkin-group').length === 2, 'one card per direction');
    check(dialog.querySelectorAll('.checkin-count').length === 8, 'four occupied carriages per direction');
    check([...dialog.querySelectorAll('.checkin-count-number')].map(x => x.textContent).join() === '1,2,3,4,1,2,3,4', 'active counts exclude history');
    check(dialog.querySelector('time').title && dialog.querySelector('time').dateTime, 'exact time available');
    check(!dialog.textContent.includes('120'), 'no history menu');
    const initialTime = dialog.querySelector('time').dateTime;
    fixture.state.checkinGroups = fixture.state.checkinGroups.map((row, i) => i === 0 ? {...row,latestAtMs:fixture.now} : row);
    fixture.update(); await pause();
    check(dialog.querySelector('time').dateTime !== initialTime && dialog.querySelector('.checkin-count-number').textContent === '1', 'renewal changes latest time without count');
    button('Change or renew').click(); await pause();
    check(dialog.querySelector('input[name=checkinCarriage]:checked').value === '4', 'current selection prefilled');
    fixture.conflict = true; button('Confirm check-in').click(); await pause();
    check(dialog.querySelector('[role=alert]').textContent.includes('another device'), 'cross-device conflict explained');
    fixture.conflict = false; button('Back').click(); await pause();
    button('Check out').click(); await pause();
    check(fixture.checkouts[0] === retryId, 'checkout uses current revision');
    fixture.state.checkin = {...fixture.state.checkin,status:'checked_out'};
    fixture.state.checkinGroups = [{ id:'boundary',direction:'towards_riga',carriage:2,status:'active',count:1,latestAtMs:fixture.now-2399999 }];
    fixture.update(); await pause();
    check(dialog.querySelectorAll('.checkin-count').length === 1, 'visible before 40-minute boundary');
    fixture.now += 1; fixture.update(); await pause();
    check(dialog.querySelectorAll('.checkin-count').length === 0, 'hidden at 40-minute boundary even before next server update');
    button('View ticket').click(); await pause();
    fixture.state.checkin = { ...fixture.state.checkin, noticePageId:fixture.claims[0],noticeUntilMs:fixture.now+7200000 };
    fixture.state.checkinGroups = [{ id:'new',direction:'away_from_riga',carriage:3,status:'active',count:2,latestAtMs:fixture.now }];
    fixture.update(); await pause();
    check(dialog.open && dialog.querySelectorAll('.checkin-group').length === 1, 'granted reminder uses grouped sheet');
    button('View ticket').click(); await pause(); fixture.update(); await pause();
    check(!dialog.open, 'reminder does not reopen');
    toggle.click(); await pause(); dialog.querySelector('.checkin-actions .primary').click(); await pause();
    fixture.ready = false; fixture.update(); await pause();
    dialog.querySelector('input[value="towards_riga"]').click(); dialog.querySelector('input[name=checkinCarriage][value="1"]').click(); await pause();
    check(button('Confirm check-in').disabled, 'offline submission disabled');
    await lang('lv');
    check(button('Apstiprināt') && dialog.lang === 'lv', 'Latvian restored');
    check(dialog.scrollWidth <= dialog.clientWidth && toggle.scrollWidth <= toggle.clientWidth, 'no horizontal overflow');
    check(errors.length === 0, 'no browser errors');
    return { checks, errors };
  }
  run().catch(error => ({ checks, errors: [...errors, error.message] })).then(result => {
    const node = document.createElement('pre'); node.id = 'fixtureResult'; node.hidden = true;
    node.textContent = JSON.stringify(result); document.body.append(node);
  });
}

function preview() {
  fixture.state.checkinGroups = [1,4].map(carriage => ({ id:String(carriage),direction:'towards_riga',carriage,status:'active',count:carriage===1?2:1,latestAtMs:fixture.now-120000 }));
  fixture.update(); document.querySelector('.checkin-toggle').click();
}

async function startFixture() {
  const style = await readFile(new URL('../internal/web/static/app.css', import.meta.url), 'utf8');
  const template = await readFile(new URL('../internal/web/static/index.html.tmpl', import.meta.url), 'utf8');
  const pageStyle = template.match(/<style[^>]*>([\s\S]*?)<\/style>/)[1];
  const actions = template.match(/<section class="ticket-reset-row"[\s\S]*?<\/section>/)[0];
  const bundle = await build({ stdin: { contents: `import { mountTrainCheckin } from './train-checkin.mjs'; import { mountLanguage, setLanguage, translatePage } from './viewer-language.mjs';
    mountLanguage(document.querySelector('#viewerLanguageMount')); translatePage(); document.addEventListener('ticket:language', () => translatePage()); window.setLanguage=setLanguage;
    const fixture = window.fixture = { now:Date.now(), ready:true, claims:[], submissions:[], checkouts:[], state:{checkin:{id:'self',revision:''},checkinGroups:[]} };
    const island=mountTrainCheckin(document.querySelector('#trainCheckinMount'),()=>({
      claimCheckinNotice:async id=>{fixture.claims.push(id)}, checkIn:async (...args)=>{fixture.submissions.push(args); if(fixture.conflict) throw Error('checkin_changed'); if(fixture.fail) throw Error('offline');},checkOut:async id=>{fixture.checkouts.push(id)}
    })); fixture.update=()=>island.update(fixture.state,fixture.ready,fixture.now);`, resolveDir: new URL('.', import.meta.url).pathname }, bundle: true, write: false, format: 'iife' });
  const server = createServer((req, res) => {
    res.setHeader('Content-Type', 'text/html; charset=utf-8');
    res.end(`<!doctype html><html lang="lv"><meta name="viewport" content="width=device-width,initial-scale=1"><style>${style}${pageStyle}</style><body><main style="max-width:520px;margin:24px auto;padding:12px;display:grid;gap:10px"><div id="viewerLanguageMount"></div><div id="trainCheckinMount"></div>${actions}</main><script>${bundle.outputFiles[0].text}</script>${req.url.includes('probe') ? `<script>(${probe})();</script>` : `<script>(${preview})();</script>`}</body></html>`);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  return { url: `http://127.0.0.1:${server.address().port}`, close: () => new Promise(resolve => server.close(resolve)) };
}
if (process.argv.includes('--serve')) console.log((await startFixture()).url);
else test('voluntary train check-in browser journey', { timeout: 60000 }, async () => {
  const browser = await findBraveBrowser(), fixture = await startFixture();
  assert.ok(browser, 'Brave required');
  try {
    for (const width of [320, 390, 1440]) {
      const result = await renderBraveDOM(browser, fixture.url + '/?probe', { windowSize: `${width},850`, waitExpression: '!!document.querySelector("#fixtureResult")' });
      const match = result.stdout.match(/<pre id="fixtureResult" hidden="">([^<]+)<\/pre>/);
      assert.ok(match, 'browser report missing');
      const report = JSON.parse(match[1].replaceAll('&quot;', '"').replaceAll('&amp;', '&'));
      assert.deepEqual(report.errors, [], JSON.stringify(report));
      console.log(`${width}px: ${report.checks.length} checks passed`);
    }
  } finally { await fixture.close(); }
});
