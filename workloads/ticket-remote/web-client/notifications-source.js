import { html, reactive } from '@arrow-js/core';

export function notificationSupport(view = window) {
  const ios = /iPad|iPhone|iPod/.test(view.navigator.userAgent) ||
    (view.navigator.platform === 'MacIntel' && view.navigator.maxTouchPoints > 1);
  if (ios && !view.navigator.standalone && !view.matchMedia('(display-mode: standalone)').matches) {
    return 'On iPhone or iPad, add Ticket to your Home Screen, open it there, then enable notifications.';
  }
  if (!view.isSecureContext || !view.navigator.serviceWorker || !view.PushManager || !view.Notification) {
    return 'This browser cannot receive push notifications. Open Ticket in a browser that supports them.';
  }
  if (view.Notification.permission === 'denied') {
    return 'Notifications are blocked. Allow them in your browser or device settings, then refresh this status.';
  }
  return '';
}

export function mountNotifications(mount) {
  if (!mount) return;
  const view = mount.ownerDocument.defaultView;
  const doc = mount.ownerDocument;
  const model = reactive({ loaded: false, busy: false, allowed: true, canManage: false,
    enabled: false, status: '', checked: '', subscribed: false, support: notificationSupport(view),
    available: false, message: 'Loading notification settings…' });
  let subscription = null;
  let publicKey = '';
  let disposed = false;

  async function request(path, body) {
    const response = await view.fetch('/api/v1/admin/' + path, {
      method: body === undefined ? 'GET' : 'POST', credentials: 'same-origin', cache: 'no-store',
      headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(10000)
    });
    if (response.status === 401 || response.status === 403) model.allowed = false;
    if (!response.ok) throw new Error('request_failed');
    return response.json();
  }

  function applyHealth(health) {
    model.enabled = health.enabled === true;
    model.canManage = health.canManageMonitoring === true;
    model.available = health.pushAvailable === true && Boolean(health.vapidPublicKey);
    publicKey = health.vapidPublicKey || '';
    model.status = !model.enabled ? 'Monitoring is off.' : ({
      ready: 'Ready to use.', not_ready: 'Ticket needs attention.',
      unable_to_verify: 'Unable to verify the ticket.', checking: 'Waiting for a fresh check.'
    }[health.status] || 'Waiting for a fresh check.');
    const checked = Number(health.lastCheckedAtMs);
    model.checked = checked > 0 && Number.isFinite(checked)
      ? `Last check: ${new Date(checked).toLocaleString()}.` : 'No check recorded yet.';
  }

  async function refresh() {
    if (model.busy || disposed) return;
    model.busy = true;
    model.message = '';
    try {
      applyHealth(await request('monitoring'));
      model.support = notificationSupport(view);
      subscription = null;
      if (view.navigator.serviceWorker && view.PushManager) {
        const registration = await view.navigator.serviceWorker.getRegistration('/');
        subscription = await registration?.pushManager.getSubscription();
      }
      model.subscribed = Boolean(subscription && (await request('notifications', {
        action: 'status', endpoint: subscription.endpoint
      })).subscribed);
      model.loaded = true;
    } catch {
      model.loaded = false;
      model.message = model.allowed ? 'Settings could not be checked. Refresh to try again.'
        : 'Owner or administrator access is required. Sign in again to check access.';
    } finally { model.busy = false; }
  }

  async function setMonitoring(event) {
    if (!model.canManage || model.busy || !model.loaded || !model.allowed) return;
    const enabled = event.target.checked;
    // Keep the control on the confirmed value until the server accepts the change.
    event.target.checked = model.enabled;
    model.busy = true;
    try {
      applyHealth(await request('monitoring', { enabled }));
      model.message = model.enabled ? 'Monitoring enabled. Starting a fresh check.' : 'Monitoring disabled. Pending alerts cancelled.';
    } catch {
      model.message = 'The change could not be confirmed. Refresh to check the saved setting.';
      model.loaded = false;
    } finally { model.busy = false; }
  }

  async function toggleNotifications() {
    if (model.busy || !model.loaded || !model.allowed) return;
    model.busy = true;
    model.message = '';
    try {
      if (model.subscribed && subscription) {
        await request('notifications', { action: 'unsubscribe', endpoint: subscription.endpoint });
        model.subscribed = false;
        await subscription.unsubscribe();
        subscription = null;
        model.message = 'Notifications disabled on this device.';
        return;
      }
      model.support = notificationSupport(view);
      if (model.support || !model.available) return;
      // iOS requires the permission request directly inside this click gesture.
      const permission = view.Notification.permission === 'granted' ? 'granted' : await view.Notification.requestPermission();
      if (permission !== 'granted') {
        model.support = notificationSupport(view);
        model.message = permission === 'denied' ? '' : 'Notification permission was not granted. You can try again when ready.';
        return;
      }
      await view.navigator.serviceWorker.register('/ticket-notifications-sw.js', { scope: '/', updateViaCache: 'none' });
      let timer;
      const registration = await Promise.race([
        view.navigator.serviceWorker.ready,
        new Promise((_, reject) => { timer = view.setTimeout(() => reject(new Error('worker_timeout')), 10000); })
      ]).finally(() => view.clearTimeout(timer));
      subscription = await registration.pushManager.getSubscription();
      if (!subscription) {
        const key = publicKey.replace(/-/g, '+').replace(/_/g, '/');
        subscription = await registration.pushManager.subscribe({ userVisibleOnly: true,
          applicationServerKey: Uint8Array.from(view.atob(key), character => character.charCodeAt(0)) });
      }
      const result = await request('notifications', { action: 'subscribe', subscription: subscription.toJSON() });
      model.subscribed = result.subscribed === true;
      model.message = model.subscribed ? 'Notifications enabled on this device.' : 'Notifications could not be enabled. Refresh to try again.';
    } catch {
      model.message = 'The notification change could not be confirmed. Refresh to check this device.';
      model.loaded = false;
    } finally { model.busy = false; }
  }

  html`<h2 id="ticketNotificationsTitle">Ticket notifications</h2>
    <p>Get one alert when the ticket needs attention or cannot be checked, then one when it is ready again. Problems must last five minutes. Idle checks run every five minutes, even while the stream sleeps, so an alert can take five to ten minutes.</p>
    <label class="ticket-monitoring-toggle" hidden="${() => !model.canManage}">
      <input id="ticketMonitoring" type="checkbox" checked="${() => model.enabled}"
        disabled="${() => model.busy || !model.loaded || !model.allowed}" @change="${setMonitoring}">Monitoring
    </label>
    <p id="ticketMonitoringStatus">${() => model.status} ${() => model.checked}</p>
    <p id="ticketDeviceNotificationStatus">${() => !model.loaded ? '' : model.subscribed ? 'This device is subscribed.' : 'This device is not subscribed.'}</p>
    <p id="ticketNotificationSupport">${() => model.support || (model.loaded && !model.available ? 'Push notifications are not configured on the server yet.' : '')}</p>
    <div class="ticket-notification-actions">
      <button id="ticketNotificationToggle" type="button" disabled="${() => model.busy || !model.loaded || !model.allowed || (!model.subscribed && (Boolean(model.support) || !model.available))}"
        @click="${toggleNotifications}">${() => model.subscribed ? 'Disable notifications on this device' : 'Enable notifications on this device'}</button>
      <button id="ticketNotificationRefresh" type="button" disabled="${() => model.busy}" @click="${refresh}">Refresh status</button>
    </div>
    <p id="ticketNotificationMessage" role="status" aria-live="polite">${() => model.message}</p>`(mount);
  doc.documentElement.dataset.ticketNotificationsUi = 'arrow';
  const visible = () => { if (!doc.hidden) void refresh(); };
  doc.addEventListener('visibilitychange', visible);
  view.addEventListener('pageshow', visible);
  void refresh();
  return () => {
    disposed = true;
    doc.removeEventListener('visibilitychange', visible);
    view.removeEventListener('pageshow', visible);
  };
}

if (typeof document !== 'undefined') mountNotifications(document.getElementById('ticketNotifications'));
