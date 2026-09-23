import { ActivityOutbox, PageActivity, ACTIVITY_INTERVAL_MS } from './page-activity.mjs';

const config = window.TICKET_REMOTE_CONFIG || {};
if (config.authenticated && config.accountScopeId && config.ticketId) {
  const reported = new Set();
  window.TICKET_ACTIVITY_DIAGNOSTICS = [];
  const diagnostic = reason => {
    if (reported.has(reason)) return;
    reported.add(reason);
    window.TICKET_ACTIVITY_DIAGNOSTICS.push(reason);
    document.dispatchEvent(new CustomEvent('ticket:activity-diagnostic', { detail: { reason } }));
    console.warn(`Ticket statistics: ${reason}`);
  };
  const outbox = new ActivityOutbox({ ...config, diagnostic });
  const activity = new PageActivity({ config, outbox, diagnostic });
  const run = () => { void activity.tick().catch(() => diagnostic('activity_tracking_failed')); };
  const wake = () => { void activity.wake().catch(() => diagnostic('activity_tracking_failed')); };
  const version = event => { void activity.version(event.detail?.version).catch(() => diagnostic('activity_tracking_failed')); };
  const timer = setInterval(run, ACTIVITY_INTERVAL_MS);
  document.addEventListener('visibilitychange', wake);
  document.addEventListener('ticket:server-version', version);
  window.addEventListener('online', wake);
  window.addEventListener('pageshow', wake);
  window.addEventListener('pagehide', event => {
    if (event.persisted) return;
    clearInterval(timer);
    activity.stop();
    document.removeEventListener('visibilitychange', wake);
    document.removeEventListener('ticket:server-version', version);
    window.removeEventListener('online', wake);
    window.removeEventListener('pageshow', wake);
  });
  run();
}
