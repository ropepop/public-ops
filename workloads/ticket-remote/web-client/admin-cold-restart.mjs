import { html, reactive } from '@arrow-js/core';

export function mountColdRestart(mount) {
  if (!mount) return { update() {}, unavailable() {} };
  const model = reactive({ ready: false, sending: false, busy: false, phase: '', message: 'Loading stream state…' });
  let sentAt = 0;
  let sentOperationId = '';
  let requestNotice = '';
  const messages = {
    quiescing: 'Stopping — cancelling the warm timer and closing streams…',
    stopping: 'Stopping — waiting for the phone to release capture…',
    confirmed: 'Cold confirmed — capture is fully stopped.',
    reloading: 'Cold confirmed. Reconnecting viewers…',
    asleep: 'Cold confirmed. Asleep until the next page opens.',
    live: 'Live — the stream has returned from cold mode.',
    failed: 'Cold shutdown could not be proved. Streaming remains paused. Check the phone before trying again.'
  };
  const running = () => ['quiescing','stopping','confirmed','reloading'].includes(model.phase);
  const begin = async () => {
    if (!model.ready || model.sending || model.busy || running()) return;
    model.sending = true;
    requestNotice = '';
    model.message = 'Requesting cold mode…';
    sentAt = performance.now();
    sentOperationId = crypto.randomUUID();
    mount.dataset.coldRestartPressedAtMillis = String(performance.timeOrigin + sentAt);
    try {
      const response = await fetch('/api/v1/admin/stream/cold-restart', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ operationId: sentOperationId }),
        signal: AbortSignal.timeout(10000)
      });
      const result = await response.json();
      if (!response.ok || !result.ok) {
        requestNotice = 'The phone is busy or cold mode could not be admitted. No stop was queued.';
        model.message = requestNotice;
      }
    } catch {
      requestNotice = 'The request outcome is uncertain. Live stream status will show whether cold mode started.';
      model.message = requestNotice;
    }
    finally { model.sending = false; }
  };
  html`<div class="admin-section-header"><div><h2>Cold stream reset</h2>
    <p class="admin-muted">Stops phone capture. Open Ticket pages reconnect; without viewers, it stays asleep until someone opens Ticket.</p></div>
    <button class="primary" type="button" disabled="${() => !model.ready || model.sending || model.busy || running()}"
      @click="${begin}">Run cold reset</button></div>
    <p class="admin-muted admin-action-status" role="status" aria-live="polite">${() => model.message}</p>`(mount);
  return {
    update(state) {
      model.ready = true;
      model.busy = Boolean(state?.phoneControlState?.busy || state?.ticketActions?.some(row => ['queued','pending','running'].includes(row.status)));
      const row = state?.streamDesired;
      const phase = row?.coldRestartPhase || '';
      model.phase = phase;
      mount.dataset.coldRestartPhase = phase;
      mount.dataset.coldRestartId = row?.coldRestartId || '';
      if (row?.coldRestartId === sentOperationId) requestNotice = '';
      if (!model.sending) model.message = requestNotice || (model.busy ? 'Wait for the current phone action to finish.' : messages[phase] || 'Ready for a cold reset.');
      if (phase === 'live' && sentAt && row?.coldRestartId === sentOperationId) { mount.dataset.coldRestartElapsedMs = String(Math.round(performance.now() - sentAt)); sentAt = 0; }
    },
    unavailable() { model.ready = false; model.message = 'Stream state is temporarily unavailable.'; }
  };
}
