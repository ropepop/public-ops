import { html, reactive } from '@arrow-js/core';
import { locale, t } from './viewer-language.mjs';

const directions = [['towards_riga', 'Uz Rīgu'], ['away_from_riga', 'No Rīgas']];
const label = direction => t(directions.find(([value]) => value === direction)?.[1] || '');
const age = (at, now) => new Intl.RelativeTimeFormat(locale.language, { numeric: 'auto' })
  .format(-Math.max(0, Math.floor((now - Number(at)) / 60000)), 'minute');
const exactTime = at => new Intl.DateTimeFormat(locale.language, { hour: '2-digit', minute: '2-digit' }).format(Number(at));

export function mountTrainCheckin(mount, getClient) {
  // Synthetic legacy page fixtures may omit the new island.
  if (!mount) return { update() {} };
  const pageId = `page-${crypto.randomUUID()}`;
  const view = reactive({ ready: false, busy: false, error: '', mode: 'summary', direction: '', carriage: 0,
    own: null, groups: [], now: Date.now(), revision: '', acknowledged: '' });
  let dialog, opener, noticeAttempted = false, noticeShown = false, lastGroups, lastOwn;
  let submission = null;
  const active = () => view.own?.status === 'active' && Number(view.own.activeUntilMs) > view.now;
  // Server expiry owns individual membership; hide wholly stale groups even while reconnecting.
  const groups = () => view.groups.filter(row => row.status === 'active' && row.count > 0 && Number(row.latestAtMs) + 40 * 60000 > view.now);
  const activeCount = () => groups().reduce((n, row) => n + row.count, 0);
  const ownSummary = () => active() ? `${label(view.own.direction)} · ${t('{n}. vagons', { n: view.own.carriage })} · ${t('Atlikušas {n} min.', { n: Math.max(0, Math.ceil((Number(view.own.activeUntilMs) - view.now) / 60000)) })}` : '';
  const edit = event => open('form', event);
  const close = () => dialog.close();
  function open(mode, event) {
    if (!dialog.open) opener = event?.currentTarget || document.activeElement;
    view.mode = mode;
    view.error = '';
    view.acknowledged = '';
    if (mode === 'form') {
      view.direction = active() ? view.own.direction : '';
      view.carriage = active() ? view.own.carriage : 0;
      view.revision = view.own?.revision || '';
      submission = null;
    }
    if (!dialog.open) dialog.showModal();
    queueMicrotask(() => (mode === 'form' ? dialog.querySelector('input[name=checkinDirection]') : dialog.querySelector('h2')).focus({ preventScroll: true }));
  }
  async function submit(event) {
    event.preventDefault();
    if (!view.ready || view.busy || !view.direction || !view.carriage) return;
    const payload = `${view.direction}:${view.carriage}:${view.revision}`;
    if (!submission || submission.payload !== payload) submission = { id: `checkin-${crypto.randomUUID()}`, payload };
    view.busy = true;
    view.error = '';
    try {
      await getClient().checkIn(submission.id, view.revision, view.direction, view.carriage);
      view.acknowledged = 'Reģistrēšanās saglabāta uz 40 minūtēm.';
      view.mode = 'success';
      queueMicrotask(() => dialog.querySelector('h2').focus({ preventScroll: true }));
    } catch (error) {
      view.error = String(error).includes('checkin_changed')
        ? 'Reģistrēšanās mainīta citā ierīcē. Atver izvēli vēlreiz.'
        : 'Neizdevās apstiprināt saglabāšanu. Pārbaudi savienojumu un mēģini vēlreiz.';
    } finally { view.busy = false; }
  }
  async function checkOut() {
    if (!view.ready || view.busy || !active()) return;
    view.busy = true;
    view.error = '';
    try {
      await getClient().checkOut(view.own.revision);
      view.acknowledged = 'Izrakstīšanās saglabāta.';
      view.mode = 'summary';
      queueMicrotask(() => dialog.querySelector('h2').focus({ preventScroll: true }));
    } catch { view.error = 'Neizdevās apstiprināt izrakstīšanos. Pārbaudi savienojumu un mēģini vēlreiz.'; }
    finally { view.busy = false; }
  }
  html`<button type="button" class="checkin-toggle primary" @click="${event => open('summary', event)}"><span>${() => t('Reģistrēšanās vilcienā')}</span>
    <small>${() => ownSummary() || t('Aktīvas reģistrēšanās: {n}', { n: activeCount() })}</small><span class="checkin-chevron" aria-hidden="true">›</span></button>
  <dialog class="checkin-dialog" aria-labelledby="checkinTitle" lang="${() => locale.language}">
    <div class="checkin-heading"><div><h2 id="checkinTitle" tabindex="-1">${() => t('Reģistrēšanās vilcienā')}</h2><p class="checkin-muted">${() => t('Pēdējās 40 minūtes')}</p></div>
      <button type="button" aria-label="${() => t('Aizvērt')}" @click="${close}">×</button></div>
    <p class="checkin-muted checkin-disclaimer">${() => t('Brīvprātīgas reģistrēšanās uz 40 minūtēm. Tas nav pasažieru skaits un nereģistrē ViVi biļeti.')}</p>
    <p class="checkin-muted" role="status" hidden="${() => view.ready}">${() => t('Savienojums atjaunojas. Dati var būt novecojuši.')}</p>
    <section hidden="${() => view.mode === 'form'}">
      <div class="checkin-success" role="status" hidden="${() => view.mode !== 'success'}"><span class="checkin-tick" aria-hidden="true">✓</span><div><strong>${() => t(view.acknowledged)}</strong><p>${() => `${label(view.direction)} · ${t('{n}. vagons', { n: view.carriage })}`}</p></div></div>
      <p class="checkin-muted" hidden="${() => activeCount() > 0}">${() => t('Vēl nav aktīvu reģistrēšanos.')}</p>
      <div class="checkin-groups">${() => directions.filter(([direction]) => groups().some(row => row.direction === direction)).map(([direction]) => html`
        <article class="checkin-group"><h3><span aria-hidden="true">↑</span> ${() => label(direction)}</h3><div class="checkin-counts">
          ${() => groups().filter(row => row.direction === direction).sort((a, b) => a.carriage - b.carriage).map(row => html`
            <div class="checkin-count"><div><strong>${() => t('{n}. vagons', { n: row.carriage })}</strong><small>${() => t('Pēdējā reģistrēšanās')}: <time datetime="${() => new Date(Number(row.latestAtMs)).toISOString()}" title="${() => exactTime(row.latestAtMs)}">${() => age(row.latestAtMs, view.now)}</time></small></div><span class="checkin-count-number" aria-label="${() => t('Aktīvas reģistrēšanās: {n}', { n: row.count })}">${row.count}</span></div>`)}</div></article>`)}</div>
      <p class="checkin-own" hidden="${() => !active()}">${ownSummary}</p>
      <p role="status" hidden="${() => view.mode === 'success' || !view.acknowledged}">${() => t(view.acknowledged)}</p>
      <div class="checkin-actions"><button type="button" class="primary" disabled="${() => !view.ready || view.busy}" @click="${edit}">${() => t(active() ? 'Mainīt / atjaunot reģistrēšanos' : 'Reģistrēties vilcienā')}</button>
        <button type="button" @click="${close}">${() => t('Skatīt biļeti')}</button></div>
      <button class="checkin-checkout" type="button" hidden="${() => !active()}" disabled="${() => !view.ready || view.busy}" @click="${checkOut}">${() => t('Izrakstīties')}</button>
    </section>
    <form hidden="${() => view.mode !== 'form'}" @submit="${submit}">
      <fieldset disabled="${() => view.busy}"><legend>${() => t('Braukšanas virziens')}</legend>
        <div class="checkin-directions">${directions.map(([value, text]) => html`<label><input type="radio" name="checkinDirection" value="${value}" checked="${() => view.direction === value}" @change="${() => { view.direction = value; view.carriage = 0; }}" required>${() => t(text)}</label>`)}</div>
      </fieldset>
      <fieldset disabled="${() => view.busy}" hidden="${() => !view.direction}"><legend>${() => t('Izvēlies vagonu no braukšanas priekšgala')}</legend>
        <div class="checkin-train"><strong class="checkin-destination">${() => t(view.direction === 'towards_riga' ? 'Rīga' : 'Prom no Rīgas')}</strong>
          <div class="checkin-carriages"><span class="checkin-arrow" aria-hidden="true">↑</span>
            ${[1, 2, 3, 4].map(position => html`<label class="${() => `checkin-carriage${view.carriage === position ? ' selected' : ''}`}"><input type="radio" name="checkinCarriage" value="${position}" checked="${() => view.carriage === position}" @change="${() => { view.carriage = position; }}" required><span>${() => t('{n}. vagons', { n: position })}</span><small>${() => t(position === 1 ? 'Priekšgals' : position === 4 ? 'Aizmugure' : '{n}. no priekšgala', { n: position })}</small></label>`)}</div>
          <span class="checkin-destination">${() => t(view.direction === 'towards_riga' ? 'Sākuma punkts' : 'Rīga')}</span></div>
      </fieldset>
      <button type="submit" class="primary" disabled="${() => !view.ready || view.busy || !view.direction || !view.carriage}">${() => t(view.busy ? 'Saglabā…' : 'Apstiprināt reģistrēšanos')}</button>
      <button type="button" class="checkin-back" disabled="${() => view.busy}" @click="${() => open('summary')}">${() => t('Atpakaļ')}</button>
    </form>
    <p role="alert" hidden="${() => !view.error}">${() => t(view.error)}</p>
  </dialog>`(mount);
  dialog = mount.querySelector('dialog');
  dialog.addEventListener('close', () => { if (opener?.isConnected) opener.focus({ preventScroll: true }); });
  document.documentElement.dataset.ticketCheckinUi = 'arrow';
  return {
    update(state, ready, clock) {
      view.ready = Boolean(ready && state?.checkin);
      if (Number.isFinite(clock)) view.now = clock;
      if (state?.checkin !== lastOwn) { lastOwn = state?.checkin; view.own = lastOwn || null; }
      if (state?.checkinGroups !== lastGroups) { lastGroups = state?.checkinGroups; view.groups = lastGroups || []; }
      // One opening attempt for this document. Later arrivals never reopen it.
      if (view.ready && !noticeAttempted && !document.hidden) {
        noticeAttempted = true;
        getClient().claimCheckinNotice(pageId).catch(() => {});
      }
      if (view.ready && !noticeShown && view.own?.noticePageId === pageId && Number(view.own.noticeUntilMs) > view.now) {
        noticeShown = true;
        if (!document.hidden && !document.querySelector('dialog[open], .code-dialog:not([hidden])')) open('notice');
      }
    }
  };
}
