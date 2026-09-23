import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { build } from 'esbuild';
import { findBraveBrowser, renderBraveDOM } from './brave-browser-test-helper.mjs';

function setup(mode) {
  window.fixtureMode = mode; window.fixtureErrors = []; window.fixtureCalls = []; window.fixtureChanges = [];
  localStorage.setItem('ticket.language', 'en');
  addEventListener('error', event => window.fixtureErrors.push(event.message));
  addEventListener('unhandledrejection', event => window.fixtureErrors.push(String(event.reason)));
  Object.defineProperty(navigator, 'userAgent', { value: 'Mozilla/5.0 (Linux; Android 15) Chrome/140 Mobile' });
  Object.defineProperty(navigator, 'languages', { value: ['en'] });
  Object.defineProperty(navigator, 'clipboard', { value: { async writeText(value) { window.fixtureClipboard = value; } } });
  if (mode === 'installed') Object.defineProperty(navigator, 'standalone', { value: true });
  if (mode === 'remembered') localStorage.setItem('ticket.invitation.fixture', '1');
  window.fixtureTrial = { id: 'fixture', status: mode === 'expired' ? 'registration_only' : 'not_started', streamSecondsRemaining: 900, activationsRemaining: 5, controlCodesRemaining: 5, authUrl: '/api/v1/auth/start?invite=1&returnTo=%2F', inviteUrl: location.origin + '/?invite=0123456789abcdefghijklmnopqrstuv' };
  let revoked = false;
  window.fetch = async (url, options = {}) => {
    const body = options.body ? JSON.parse(options.body) : undefined; window.fixtureCalls.push({ url, body });
    let data = { ok: true };
    if (url === '/api/v1/invite/open') data.url = '#invitation-opened';
    if (url === '/api/v1/invite/start') {
      if (mode === 'takeover' && !body.takeover) return { ok: false, status: 409, json: async () => ({ needsTakeover: true }) };
      if (mode !== 'trial') data.url = '#trial-started'; window.fixtureTrial.needsTakeover = false;
    }
    if (url === '/api/v1/invite/status') data.trial = { ...window.fixtureTrial };
    if (url === '/api/v1/admin/invitations') data = body ? { ok: true, inviteUrl: window.fixtureTrial.inviteUrl }
      : { ok: true, invitations: [{ ...window.fixtureTrial, label: 'For Mira', createdBy: 'owner@example.test', createdAt: '2026-09-22T12:00:00Z', trialExpiresAt: '2026-09-25T12:00:00Z', status: revoked ? 'revoked' : 'not_started' }], sources: [{ email: 'member@example.test', kind: 'invitation', label: 'For Mira', inviter: 'owner@example.test', registeredAt: '2026-09-22T12:00:00Z' }] };
    if (url.endsWith('/revoke')) revoked = true;
    return { ok: true, status: 200, json: async () => data };
  };
}
function probe() {
  const checks = [], errors = window.fixtureErrors, pause = () => new Promise(resolve => setTimeout(resolve, 40));
  const check = (value, name) => { if (!value) throw Error(name); checks.push(name); };
  const change = async (node, value) => { node.value = value; node.dispatchEvent(new Event('change', { bubbles: true })); await pause(); };
  const text = selector => document.querySelector(selector)?.textContent || '';
  const button = label => [...document.querySelectorAll('button')].find(node => node.textContent.trim() === label);
  async function run() {
    await pause(); const mode = window.fixtureMode;
    if (mode === 'admin') {
      check(document.documentElement.dataset.ticketInvitationsUi === 'arrow', 'administrator island mounted');
      check(text('[data-member-source-email]').includes('Invited through For Mira'), 'member source is attributed');
      button('Invitations').click(); await pause();
      check(document.querySelector('#adminPeople').hidden, 'People view closes when Invitations opens');
      check(document.querySelector('[name="duration"]').value === '4320', 'three-day default');
      check(document.querySelector('[name="streamMinutes"]').value === '15', '15-minute default');
      await change(document.querySelector('[name="duration"]'), 'custom');
      check(!document.querySelector('[name="customDuration"]').disabled, 'custom minutes can be edited');
      document.querySelector('[name="customDuration"]').value = '90'; document.querySelector('[name="label"]').value = 'For Mira';
      document.querySelector('.invitation-create').requestSubmit(); await pause();
      const created = window.fixtureCalls.find(call => call.body?.label);
      check(created.body.durationMinutes === 90 && created.body.streamMinutes === 15, 'create sends exact selected allowances');
      check(document.querySelector('#createdInvitationLink').value === window.fixtureTrial.inviteUrl, 'complete link shown once');
      button('Copy link').click(); await pause();
      check(window.fixtureClipboard === window.fixtureTrial.inviteUrl && button('Copied'), 'copy returns complete invitation');
      button('Close').click(); await pause(); check(document.querySelector('#createdInvitationLink').value === '', 'closing removes raw link');
      button('Revoke invitation').click(); await pause(); check(text('.invitation-list').includes('Revoked'), 'revocation refreshes status');
      button('People').click(); await pause(); check(!document.querySelector('#adminPeople').hidden, 'People view remains available');
    } else if (mode === 'complete') {
      check(text('[role="status"]').includes('You’re in.'), 'registration has a friendly confirmation');
      check(!new URL(location.href).searchParams.has('registered'), 'registration query is removed');
      button('Close').click(); await pause(); check(!document.querySelector('[role="status"]'), 'confirmation can be dismissed');
    } else if (mode === 'trial') {
      check(text('.ticket-trial').includes('15:00'), 'compact trial time visible');
      window.fixtureTrial.status = 'registration_only'; window.fixtureTrial.resultDeliveryAllowed = true;
      await window.fixtureTrialUI.refresh(); await pause(); check(!document.querySelector('#trialEndDialog').open, 'accepted result delivery stays uncovered');
      window.fixtureTrial.resultDeliveryAllowed = false;
      await window.fixtureTrialUI.refresh(); await pause(); check(document.querySelector('#trialEndDialog').open, 'ending trial presents registration');
      check(window.fixtureChanges.at(-1).status === 'registration_only', 'viewer receives server entitlement');
      window.fixtureTrial.status = 'trial_active'; window.fixtureTrial.needsTakeover = true;
      await window.fixtureTrialUI.refresh(); await pause(); check(text('#trialEndDialog').includes('Continue here'), 'other-device trial offers explicit transfer');
      document.querySelector('#trialContinueHere').click(); await pause();
      check(window.fixtureCalls.some(call => call.body?.takeover === true), 'transfer explicitly requested');
      check(!document.querySelector('#trialEndDialog').open, 'successful transfer dismisses prompt');
    } else if (mode === 'entry') {
      const form = document.querySelector('.invitation-entry'), input = document.querySelector('#invitationLink');
      input.value = 'https://other.example/?invite=0123456789abcdefghijklmnopqrstuv'; form.requestSubmit(); await pause();
      check(window.fixtureCalls.length === 0 && text('[role="status"]').includes('32-character'), 'other-origin links rejected');
      input.value = window.fixtureTrial.inviteUrl; form.requestSubmit(); await pause();
      check(window.fixtureCalls.at(-1).body.link === window.fixtureTrial.inviteUrl, 'entry exchanges link through same-origin API');
    } else if (mode === 'installed' || mode === 'remembered') {
      check(window.fixtureCalls.filter(call => call.url.endsWith('/start')).length === 1, 'existing app or choice resumes trial once');
      check(location.hash === '#trial-started', 'trial resume navigates to viewer');
      check(!document.querySelector('#installTicketDialog')?.open, 'resume skips installation guidance');
    } else if (mode === 'expired') {
      check(text('h1').includes('complete') && !document.querySelector('#inviteTry'), 'expired invitation is registration-only');
      check(document.querySelector('#inviteRegister').href.includes('invite=1'), 'expiry retains registration context');
      check(window.fixtureCalls.length === 0, 'expired welcome starts no trial');
    } else {
      check(document.documentElement.dataset.ticketWelcomeUi === 'arrow', 'invitation welcome mounted');
      const icon = document.querySelector('.welcome-icon'); await icon.decode(); check(icon.naturalWidth === 192, 'public welcome icon loads');
      check(window.fixtureCalls.length === 0, 'first link view starts no guest work');
      check(!document.querySelector('#welcomeInstructions'), 'ordinary welcome is not repeated');
      for (const language of ['ru', 'lv', 'en']) { await change(document.querySelector('#viewerLanguage'), language); check(document.documentElement.lang === language && text('h1').length > 5, language + ' invitation translated'); }
      document.querySelector('#inviteSetup').click(); await pause();
      const dialog = document.querySelector('#installTicketDialog');
      check(dialog.open && dialog.dataset.installPage === 'native', 'Android opens Native Alpha options directly');
      for (const [language, trialText, optionalText] of [['en', 'without an account', 'when you’re ready'], ['lv', 'bez konta', 'kad esi gatavs'], ['ru', 'без аккаунта', 'когда будете готовы']]) {
        await change(dialog.querySelector('.install-language'), language);
        check(dialog.querySelector('.install-steps li:nth-child(2)').textContent.includes(trialText), language + ' Native Alpha step starts trial without an account');
        check(text('#installContinueAuth').includes(optionalText), language + ' registration is optional while trying');
      }
      await change(dialog.querySelector('.install-language'), 'en');
      check(document.querySelector('#installTicketURL').value === window.fixtureTrial.inviteUrl, 'setup keeps invitation link');
      document.querySelector('.install-copy').click(); await pause(); check(window.fixtureClipboard === window.fixtureTrial.inviteUrl, 'copy preserves token');
      dialog.querySelector('.install-back').click(); await pause(); dialog.querySelector('.install-back').click(); await pause();
      [...dialog.querySelectorAll('.install-choices button')].find(node => node.textContent.includes('iPhone')).click(); await pause();
      check(dialog.dataset.installPage === 'ios' && document.querySelector('#installTicketURL').value === window.fixtureTrial.inviteUrl, 'Safari handoff preserves full invitation link');
      check(dialog.querySelector('.install-steps li').textContent.includes('do not need an account'), 'Safari invitation does not require previous approval');
      for (const image of dialog.querySelectorAll('image')) { const bitmap = new Image(); bitmap.src = image.getAttribute('href'); await bitmap.decode(); check(bitmap.naturalWidth > 300, 'installation screenshot loads'); }
      check(window.fixtureCalls.length === 0, 'installation spends no allowance'); dialog.close(); await pause();
      document.querySelector('#inviteTry').click(); await pause();
      if (mode === 'takeover') { check(text('#inviteTry').includes('Continue here'), 'conflict asks before takeover'); document.querySelector('#inviteTry').click(); await pause(); check(window.fixtureCalls.at(-1).body.takeover === true, 'takeover is explicit'); }
      check(location.hash === '#trial-started', 'try navigates to viewer');
      check(localStorage.getItem('ticket.invitation.fixture') === '1', 'acknowledgement scoped to invitation');
      check(!localStorage.getItem('ticket.welcomeAcknowledged'), 'ordinary welcome preference untouched');
    }
    check(document.documentElement.scrollWidth <= innerWidth, 'page fits viewport');
  }
  run().catch(error => errors.push(error.message)).finally(() => { const node = document.createElement('pre'); node.id = 'fixtureResult'; node.hidden = true; node.textContent = JSON.stringify({ checks, errors }); document.body.append(node); });
}

async function startFixture() {
  const browser = await findBraveBrowser(); assert.ok(browser, 'Brave required');
  const template = await readFile(new URL('../internal/web/welcome/index.html.tmpl', import.meta.url), 'utf8');
  const bundles = await Promise.all(['welcome-source.js', 'admin-invitations-source.js'].map(entry => build({ entryPoints: [new URL('./' + entry, import.meta.url).pathname], bundle: true, write: false, format: 'iife' })));
  const trialBundle = await build({ stdin: { contents: `import {mountTrial,showRegistrationComplete} from './invitation-ui.mjs'; if(window.fixtureMode==='complete')showRegistrationComplete(document.querySelector('#trial'));else{window.fixtureTrial.status='trial_active';window.fixtureTrialUI=mountTrial(document.querySelector('#trial'),window.fixtureTrial,next=>window.fixtureChanges.push(next));}`, resolveDir: new URL('.', import.meta.url).pathname }, bundle: true, write: false, format: 'iife' });
  const server = createServer(async (req, res) => {
    const url = new URL(req.url, 'http://localhost');
    if (url.pathname.endsWith('.js')) { res.setHeader('Content-Type', 'text/javascript'); res.end(url.pathname.includes('admin') ? bundles[1].outputFiles[0].text : url.pathname.includes('trial') ? trialBundle.outputFiles[0].text : bundles[0].outputFiles[0].text); return; }
    if (url.pathname.endsWith('.css')) { res.setHeader('Content-Type', 'text/css'); res.end(await readFile(new URL('../internal/web' + url.pathname, import.meta.url))); return; }
    const publicImages = new Set(['icon-192.png', 'icon-512.png', 'icon-maskable.png', 'apple-touch-icon.png', 'install-apple.png', 'install-safari-steps.jpg', 'install-safari-add.jpg', 'install-firefox.png', 'install-chrome-choice.png', 'install-chrome-confirm.png']);
    const publicFile = url.pathname.slice('/pwa/'.length);
    if (url.pathname.startsWith('/pwa/') && publicImages.has(publicFile)) {
      res.setHeader('Content-Type', publicFile.endsWith('.png') ? 'image/png' : 'image/jpeg');
      res.end(await readFile(new URL('../internal/web/' + (publicFile.startsWith('install-') ? 'static/' : 'pwa/') + publicFile, import.meta.url))); return;
    }
    if (url.pathname === '/manifest.webmanifest') { res.setHeader('Content-Type', 'application/manifest+json'); res.end(await readFile(new URL('../internal/web/pwa/manifest.webmanifest', import.meta.url))); return; }
    if (url.pathname === '/favicon.ico') { res.writeHead(204); res.end(); return; }
    const mode = url.searchParams.get('mode') || 'welcome';
    const before = `<script>(${setup})(${JSON.stringify(mode)});</script>`, after = url.searchParams.has('preview') ? '' : `<script>addEventListener('DOMContentLoaded',()=>(${probe})());</script>`;
    let page;
    if (mode === 'admin') page = `<html><head><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/static/app.css"><link rel="stylesheet" href="/static/admin-invitations.css"></head><body class="admin-page"><main class="admin-shell"><section class="admin-section"><div id="adminInvitations"></div><div id="adminPeople"><span data-member-source-email="member@example.test"></span></div></section></main>${before}<script defer src="/admin.js"></script>${after}</body></html>`;
    else if (mode === 'trial' || mode === 'complete') page = `<html><head><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/pwa/install-guide.css"></head><body><div id="trial"></div>${before}<script defer src="/trial.js"></script>${after}</body></html>`;
    else {
      const trial = { id: 'fixture', status: mode === 'expired' ? 'registration_only' : 'not_started', streamSecondsRemaining: 900, activationsRemaining: 5, controlCodesRemaining: 5, authUrl: '/api/v1/auth/start?invite=1&returnTo=%2F', inviteUrl: `http://127.0.0.1:${server.address().port}/?invite=0123456789abcdefghijklmnopqrstuv` };
      page = template.replaceAll('{{.InvitationJSON}}', mode === 'entry' ? 'null' : JSON.stringify(trial)).replaceAll('{{.InvitationEntry}}', String(mode === 'entry')).replaceAll('{{.AuthURL}}', '/api/v1/auth/start').replaceAll('{{.ManifestURL}}', '/manifest.webmanifest').replace(/\{\{[^}]+\}\}/g, '').replace('</head>', before + '</head>').replace('</body>', after + '</body>');
    }
    res.setHeader('Content-Type', 'text/html; charset=utf-8'); res.end(page);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  return { server, browser, url: `http://127.0.0.1:${server.address().port}` };
}

if (process.argv.includes('--serve')) {
  const fixture = await startFixture(); console.log(fixture.url + '/?mode=welcome&preview');
} else test('invitation onboarding, transfer, registration boundary and administrator journey', { timeout: 120000 }, async () => {
  const {server, browser} = await startFixture();
  try {
    for (const mode of ['welcome', 'takeover', 'expired', 'installed', 'remembered', 'entry', 'trial', 'complete', 'admin']) {
      const result = await renderBraveDOM(browser, `http://127.0.0.1:${server.address().port}/?mode=${mode}${mode === 'complete' ? '&registered=1' : ''}`, { windowSize: '390,850', waitExpression: '!!document.querySelector("#fixtureResult")' });
      const match = result.stdout.match(/<pre id="fixtureResult" hidden="">([^<]+)<\/pre>/); assert.ok(match, mode + ': missing report');
      const report = JSON.parse(match[1].replaceAll('&quot;', '"').replaceAll('&amp;', '&'));
      assert.deepEqual(report.errors, [], mode + ': ' + report.checks.join(', ')); console.log(mode + ': ' + report.checks.length + ' checks passed');
    }
  } finally { await new Promise(resolve => server.close(resolve)); }
});
