import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { build } from 'esbuild';
import { findBraveBrowser, renderBraveDOM } from './brave-browser-test-helper.mjs';

async function probe(ActivityOutbox, PageActivity) {
  const checks = [], errors = [];
  const check = (name, condition) => { if (!condition) throw Error(name); checks.push(name); };
  const make = (account = 'a', ticket = 't') => new ActivityOutbox({ accountScopeId: account,
    ticketId: ticket, name: 'activity-storage-fixture', diagnostic: reason => errors.push(reason) });
  const deliveries = async expected => {
    for (let attempt = 0; attempt < 100; attempt++) {
      const result = await (await fetch('/activity-count')).json();
      if (result.count >= expected) return result;
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    throw Error('standalone activity delivery did not arrive');
  };
  try {
    const first = make();
    if (!sessionStorage.getItem('activity-reloaded')) {
      await first.put(101); await first.put(102);
      await first.close();
      await deliveries(1);
      sessionStorage.setItem('activity-reloaded', 'true');
      location.reload();
      return;
    }
    check('IndexedDB survives an actual page reload', JSON.stringify(await first.pending(1)) === '[101,102]');
    const second = make(), otherAccount = make('b'), otherTicket = make('a', 'other');
    await Promise.all([first.put(103), second.put(103), otherAccount.put(103), otherTicket.put(103)]);
    check('concurrent connections use one durable key per account, ticket and slot',
      JSON.stringify(await second.pending(1)) === '[101,102,103]');
    check('another account cannot drain these samples', JSON.stringify(await otherAccount.pending(1)) === '[103]');
    check('ticket scopes stay separate', JSON.stringify(await otherTicket.pending(1)) === '[103]');
    await second.remove([101]);
    // A second tab may resend an acknowledged slot from memory; the server
    // deduplicates it. A fresh page sees the exact durable acknowledgement.
    const restored = make();
    check('acknowledgement removes only its durable key', JSON.stringify(await restored.pending(1)) === '[102,103]');
    check('expiry preserves unexpired records across accounts and tickets', JSON.stringify(await restored.pending(103)) === '[103]' &&
      JSON.stringify(await otherAccount.pending(1)) === '[103]');
    await otherAccount.put(99); await otherTicket.put(99);
    const pruner = make(); await pruner.pending(100); await pruner.close();
    const oldAccount = make('b'), oldTicket = make('a', 'other');
    check('expired records from inactive accounts and tickets are pruned too',
      JSON.stringify(await oldAccount.pending(1)) === '[103]' && JSON.stringify(await oldTicket.pending(1)) === '[103]');
    await restored.remove([103]);
    const drained = make();
    check('acknowledged and expired samples are absent after reopening', (await drained.pending(1)).length === 0);
    await Promise.all([first.close(), second.close(), otherAccount.close(), otherTicket.close(), restored.close(), drained.close(), oldAccount.close(), oldTicket.close()]);
    const offline = make('offline');
    const opened = Date.now(); let elapsed = 0;
    const collector = new PageActivity({ config: { accountScopeId: 'offline', ticketId: 't',
      pageVersion: 'v1', serverTime: new Date(opened).toISOString() }, outbox: offline,
      now: () => elapsed, wallNow: () => opened + elapsed, visible: () => true, online: () => false });
    await collector.tick();
    elapsed = 31 * 24 * 60 * 60 * 1000;
    await collector.tick();
    const offlineRestored = make('offline');
    check('offline collection expires native storage after thirty days without delivery',
      JSON.stringify(await offlineRestored.pending(1)) === JSON.stringify([Math.floor((opened + elapsed) / 5000)]));
    await offline.close(); await offlineRestored.close();
    const delivered = await deliveries(2);
    check('standalone entry tracks both visits despite a broken viewer script', delivered.count >= 2);
    check('failed HTTP samples survive actual reload and retry', delivered.recovered);
    const deliveredOutbox = new ActivityOutbox({ accountScopeId: 'fixture-account', ticketId: 'fixture-ticket' });
    let remaining;
    for (let attempt = 0; attempt < 50; attempt++) {
      remaining = await deliveredOutbox.pending(1);
      if (!remaining.length) break;
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    check('native browser timer cleanup permits acknowledged samples to leave storage', remaining.length === 0);
    await deliveredOutbox.close();
  } catch (error) { errors.push(String(error)); }
  const result = document.createElement('pre'); result.id = 'activityResult'; result.hidden = true;
  result.textContent = JSON.stringify({ checks, errors }); document.body.append(result);
  document.documentElement.dataset.probeComplete = 'true';
}

test('real browser storage survives reloads, isolates accounts and runs independently of the viewer', { timeout: 45000 }, async () => {
  const browser = await findBraveBrowser();
  assert.ok(browser, 'Brave is required for the loopback storage test');
  const resolveDir = new URL('.', import.meta.url).pathname;
  const [entry, fixture] = await Promise.all([
    build({ entryPoints: [new URL('page-activity-source.js', import.meta.url).pathname], bundle: true, format: 'iife', write: false, logLevel: 'silent' }),
    build({ stdin: { contents: `import { ActivityOutbox, PageActivity } from './page-activity.mjs'; (${probe.toString()})(ActivityOutbox, PageActivity);`, resolveDir }, bundle: true, format: 'iife', write: false, logLevel: 'silent' })
  ]);
  let count = 0, firstSlots = [], recovered = false;
  const server = createServer(async (req, res) => {
    if (req.url === '/') {
      const config = { authenticated: true, accountScopeId: 'fixture-account', ticketId: 'fixture-ticket', pageVersion: 'v1', serverTime: new Date().toISOString() };
      res.setHeader('Content-Type', 'text/html');
      res.end(`<!doctype html><script>window.TICKET_REMOTE_CONFIG=${JSON.stringify(config)};</script><script src="/activity.js"></script><body><script>throw Error('intentional broken viewer');</script><script defer src="/probe.js"></script>`);
    } else if (req.url === '/activity.js' || req.url === '/probe.js') {
      res.setHeader('Content-Type', 'text/javascript');
      res.end((req.url === '/activity.js' ? entry : fixture).outputFiles[0].text);
    } else if (req.url === '/api/v1/activity') {
      let body = '';
      for await (const chunk of req) body += chunk;
      const payload = JSON.parse(body);
      count++;
      if (count === 1) {
        firstSlots = payload.slots;
        res.writeHead(503); res.end(); return;
      }
      recovered ||= firstSlots.every(slot => payload.slots.includes(slot));
      res.setHeader('Content-Type', 'application/json');
      res.end(JSON.stringify({ accountScopeId: payload.accountScopeId, serverTime: new Date().toISOString(),
        serverVersion: 'v1', acknowledgedSlots: payload.slots, discardedSlots: [] }));
    } else if (req.url === '/activity-count') {
      res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ count, recovered }));
    } else { res.writeHead(404); res.end(); }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  try {
    const rendered = await renderBraveDOM(browser, `http://127.0.0.1:${server.address().port}/`, {
      timeoutMillis: 30000, waitExpression: '!!document.querySelector("#activityResult")'
    });
    const match = rendered.stdout.match(/<pre id="activityResult" hidden="">([^<]+)<\/pre>/);
    assert.ok(match, 'missing browser activity report');
    const result = JSON.parse(match[1].replaceAll('&quot;', '"').replaceAll('&amp;', '&'));
    assert.deepEqual(result.errors, [], JSON.stringify(result));
    assert.equal(result.checks.length, 12);
    console.log(JSON.stringify(result));
  } finally { await new Promise(resolve => server.close(resolve)); }
});
