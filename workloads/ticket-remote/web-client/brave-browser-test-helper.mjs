import { spawn } from 'node:child_process';
import { access, mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';

const BRAVE_PATHS = [
  process.env.TICKET_BRAVE_BIN,
  '/Applications/Brave Browser.app/Contents/MacOS/Brave Browser',
  '/usr/bin/brave-browser',
  '/opt/brave.com/brave/brave-browser'
];

export async function findBraveBrowser() {
  for (const candidate of BRAVE_PATHS) {
    if (!candidate) continue;
    try {
      await access(candidate);
      return candidate;
    } catch (_) {
      // Try the next Brave installation path.
    }
  }
  return '';
}

function deadlinePromise(milliseconds, message) {
  let timer;
  const promise = new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error(message)), milliseconds);
  });
  return { promise, cancel: () => clearTimeout(timer) };
}

function childIsRunning(child) {
  return child.exitCode === null && child.signalCode === null;
}

function parseWindowSize(value) {
  if (!value) return null;
  const match = /^(\d+),(\d+)$/.exec(value);
  if (!match) throw new Error('Brave fixture windowSize must be WIDTH,HEIGHT');
  const width = Number(match[1]);
  const height = Number(match[2]);
  if (width < 1 || height < 1) throw new Error('Brave fixture dimensions must be positive');
  return { width, height };
}

async function waitForChildClose(child, childClosed, milliseconds) {
  if (!childIsRunning(child)) return true;
  let timer;
  try {
    return await Promise.race([
      childClosed.then(() => true),
      new Promise((resolve) => { timer = setTimeout(() => resolve(false), milliseconds); })
    ]);
  } finally {
    clearTimeout(timer);
  }
}

async function connectCDP(url) {
  const socket = new WebSocket(url);
  const opened = deadlinePromise(5_000, 'Brave DevTools connection timed out');
  try {
    await Promise.race([
      new Promise((resolve, reject) => {
        socket.addEventListener('open', resolve, { once: true });
        socket.addEventListener('error', () => reject(new Error('Brave DevTools connection failed')), { once: true });
      }),
      opened.promise
    ]);
  } catch (error) {
    socket.close();
    throw error;
  } finally {
    opened.cancel();
  }

  let nextId = 0;
  const pending = new Map();
  socket.addEventListener('message', (event) => {
    let message;
    try {
      message = JSON.parse(String(event.data));
    } catch (_) {
      return;
    }
    if (!message.id || !pending.has(message.id)) return;
    const waiter = pending.get(message.id);
    pending.delete(message.id);
    clearTimeout(waiter.timer);
    if (message.error) waiter.reject(new Error(message.error.message || 'Brave DevTools command failed'));
    else waiter.resolve(message.result || {});
  });
  socket.addEventListener('close', () => {
    for (const waiter of pending.values()) {
      clearTimeout(waiter.timer);
      waiter.reject(new Error('Brave DevTools connection closed'));
    }
    pending.clear();
  });

  return {
    send(method, params = {}, sessionId = '') {
      const id = ++nextId;
      return new Promise((resolve, reject) => {
        if (socket.readyState !== WebSocket.OPEN) {
          reject(new Error(`Brave DevTools command could not start: ${method}`));
          return;
        }
        const timer = setTimeout(() => {
          pending.delete(id);
          reject(new Error(`Brave DevTools command timed out: ${method}`));
        }, 5_000);
        pending.set(id, { resolve, reject, timer });
        try {
          socket.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
        } catch (error) {
          clearTimeout(timer);
          pending.delete(id);
          reject(error);
        }
      });
    },
    close() {
      socket.close();
    }
  };
}

export async function renderBraveDOM(browser, url, options = {}) {
  const target = new URL(url);
  if (target.protocol !== 'http:' || target.hostname !== '127.0.0.1' ||
    target.username || target.password
  ) {
    throw new Error('Brave fixture tests accept only unauthenticated loopback HTTP URLs');
  }
  const viewport = parseWindowSize(options.windowSize);
  const profile = await mkdtemp(path.join(os.tmpdir(), 'ticket-brave-fixture-'));
  const args = [
    '--headless',
    '--disable-background-networking',
    '--disable-default-apps',
    '--disable-extensions',
    '--disable-gpu',
    '--no-default-browser-check',
    '--no-first-run',
    '--remote-debugging-port=0',
    `--user-data-dir=${profile}`
  ];
  if (options.windowSize) args.push(`--window-size=${options.windowSize}`);
  args.push('about:blank');

  const child = spawn(browser, args, { stdio: ['ignore', 'ignore', 'pipe'] });
  const childClosed = new Promise((resolve) => child.once('close', resolve));
  let stderr = '';
  let devToolsResolve;
  let devToolsReject;
  const devToolsURL = new Promise((resolve, reject) => {
    devToolsResolve = resolve;
    devToolsReject = reject;
  });
  child.stderr.on('data', (chunk) => {
    stderr = (stderr + chunk).slice(-1024 * 1024);
    const match = stderr.match(/DevTools listening on (ws:\/\/\S+)/);
    if (match) devToolsResolve(match[1]);
  });
  child.once('error', devToolsReject);
  child.once('close', (code) => {
    if (code && !/DevTools listening on/.test(stderr)) {
      devToolsReject(new Error(`Brave exited before DevTools was ready: ${stderr}`));
    }
  });

  const launchDeadline = deadlinePromise(10_000, `Brave did not expose DevTools: ${stderr}`);
  let client;
  try {
    const webSocketURL = await Promise.race([devToolsURL, launchDeadline.promise]);
    client = await connectCDP(webSocketURL);
    const { targetId } = await client.send('Target.createTarget', { url: 'about:blank' });
    const { sessionId } = await client.send('Target.attachToTarget', { targetId, flatten: true });
    await client.send('Page.enable', {}, sessionId);
    await client.send('Runtime.enable', {}, sessionId);
    if (viewport) {
      await client.send('Emulation.setDeviceMetricsOverride', {
        width: viewport.width,
        height: viewport.height,
        deviceScaleFactor: 1,
        mobile: false
      }, sessionId);
    }
    await client.send('Page.navigate', { url }, sessionId);

    const timeoutMillis = options.timeoutMillis || 15_000;
    const waitExpression = options.waitExpression ||
      'document.readyState === "complete" && document.documentElement.dataset.probeComplete === "true"';
    const deadline = Date.now() + timeoutMillis;
    let complete = false;
    while (Date.now() < deadline) {
      try {
        const result = await client.send('Runtime.evaluate', {
          expression: waitExpression,
          returnByValue: true
        }, sessionId);
        complete = result.result?.value === true;
      } catch (_) {
        complete = false;
      }
      if (complete) break;
      await new Promise((resolve) => setTimeout(resolve, 25));
    }
    if (!complete) {
      const diagnostic = await client.send('Runtime.evaluate', {
        expression: 'JSON.stringify({readyState: document.readyState, dataset: {...document.documentElement.dataset}, scripts: document.scripts.length, hasProbeScript: Array.from(document.scripts).some((script) => script.textContent.includes("probeComplete")), browserErrorCount: document.getElementById("ticketAdminStatisticsBrowserErrors")?.dataset.errorCount})',
        returnByValue: true
      }, sessionId).catch(() => null);
      throw new Error(
        `Brave page probe did not complete within ${timeoutMillis} ms: ${String(diagnostic?.result?.value || '')}\n${stderr}`
      );
    }
    const rendered = await client.send('Runtime.evaluate', {
      expression: 'document.documentElement.outerHTML',
      returnByValue: true
    }, sessionId);
    return { stdout: String(rendered.result?.value || ''), stderr };
  } finally {
    launchDeadline.cancel();
    if (client) {
      await client.send('Browser.close').catch(() => {});
      client.close();
    }
    if (childIsRunning(child)) await waitForChildClose(child, childClosed, 2_000);
    if (childIsRunning(child)) {
      child.kill('SIGTERM');
      await waitForChildClose(child, childClosed, 2_000);
    }
    if (childIsRunning(child)) {
      child.kill('SIGKILL');
      await waitForChildClose(child, childClosed, 2_000);
    }
    await rm(profile, { recursive: true, force: true });
  }
}
