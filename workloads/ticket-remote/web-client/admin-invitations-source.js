import { html, reactive } from '@arrow-js/core';

const mount = document.querySelector('#adminInvitations');
const people = document.querySelector('#adminPeople');
const state = reactive({ view: new URL(location.href).searchParams.get('view') === 'invitations' ? 'invitations' : 'people',
  rows: [], loading: false, loaded: false, busy: false, duration: '4320', error: '', inviteUrl: '', copied: false, revoking: '' });
const statuses = { not_started: 'Not started', trial_active: 'Trial active', registration_only: 'Registration only', registered: 'Registered', revoked: 'Revoked' };
const date = value => value ? new Date(value).toLocaleString('en-GB', { dateStyle: 'medium', timeStyle: 'short' }) : '—';
const time = value => `${Math.floor(value / 60)}:${String(value % 60).padStart(2, '0')}`;
async function api(body, path = '/api/v1/admin/invitations') {
  const response = await fetch(path, { credentials: 'same-origin', cache: 'no-store', ...(body === undefined ? {} : { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }) });
  const data = await response.json();
  if (!response.ok || data.ok === false) throw Error('Unable to update invitations. Please try again.');
  return data;
}
function applySources(sources) {
  const byEmail = new Map(sources.map(source => [source.email.toLowerCase(), source]));
  for (const node of document.querySelectorAll('[data-member-source-email]')) {
    const source = byEmail.get(node.dataset.memberSourceEmail.toLowerCase());
    const description = source?.kind === 'invitation' ? `Invited through ${source.label || source.invitationId || 'invitation'}${source.inviter ? ' · ' + source.inviter : ''}${source.registeredAt ? ' · ' + date(source.registeredAt) : ''}`
      : source?.kind === 'manual' ? 'Added manually' : 'Existing member';
    node.replaceChildren(); html`${description}`(node);
  }
}
async function load() {
  if (state.loading) return;
  state.loading = true; state.error = '';
  try { const data = await api(); state.rows = data.invitations || []; applySources(data.sources || []); state.loaded = true; }
  catch (error) { state.error = error.message; }
  finally { state.loading = false; }
}
function select(view) {
  state.view = view; people.hidden = view !== 'people';
  const url = new URL(location.href); if (view === 'people') url.searchParams.delete('view'); else url.searchParams.set('view', view);
  history.replaceState(null, '', url);
}
html`<nav class="invitation-tabs" aria-label="Member views"><button type="button" aria-pressed="${() => state.view === 'people'}" @click="${() => select('people')}">People</button><button type="button" aria-pressed="${() => state.view === 'invitations'}" @click="${() => select('invitations')}">Invitations</button></nav>
  <div hidden="${() => state.view !== 'invitations'}">
    <form class="invitation-create" @submit="${async event => {
      event.preventDefault(); if (state.busy) return;
      const fields = new FormData(event.currentTarget);
      state.busy = true; state.error = ''; state.inviteUrl = ''; state.copied = false;
      try {
        const durationMinutes = state.duration === 'custom' ? Number(fields.get('customDuration')) : Number(state.duration);
        const result = await api({ label: String(fields.get('label') || '').trim(), durationMinutes, streamMinutes: Number(fields.get('streamMinutes')) });
        state.inviteUrl = result.inviteUrl; await load();
        queueMicrotask(() => document.querySelector('#createdInvitationLink')?.focus());
      } catch (error) { state.error = error.message; }
      finally { state.busy = false; }
    }}">
      <h2>Create invitation</h2><p class="admin-muted">One shared trial and one new member. After the trial ends, the link still lets them register until you revoke it.</p>
      <label><span>Private label <span class="admin-muted">(optional)</span></span><input name="label" type="text" maxlength="120" autocomplete="off" placeholder="For a friend"></label>
      <div class="invitation-fields"><label><span>Trial duration</span><select name="duration" value="${() => state.duration}" @change="${event => { state.duration = event.target.value; }}"><option value="1440">1 day</option><option value="4320">3 days</option><option value="7200">5 days</option><option value="custom">Custom</option></select></label>
        <label hidden="${() => state.duration !== 'custom'}"><span>Custom duration, minutes</span><input name="customDuration" type="number" min="1" max="525600" step="1" value="60" required="${() => state.duration === 'custom'}" disabled="${() => state.duration !== 'custom'}"></label>
        <label><span>Viewing allowance</span><select name="streamMinutes"><option value="5">5 minutes</option><option value="15" selected>15 minutes</option><option value="30">30 minutes</option></select></label></div>
      <p class="admin-muted">Five successful activations · Five successful control codes</p><button class="primary" type="submit" disabled="${() => state.busy}">${() => state.busy ? 'Creating…' : 'Create invitation'}</button>
    </form>
    <section class="invitation-created" hidden="${() => !state.inviteUrl}" aria-label="New invitation"><h3>Your invitation is ready</h3><p>Copy this link now. It is shown only here and cannot be retrieved later.</p><input id="createdInvitationLink" type="url" readonly value="${() => state.inviteUrl}" aria-label="Invitation link"><div class="invitation-link-actions"><button class="primary" type="button" @click="${async () => { try { await navigator.clipboard.writeText(state.inviteUrl); state.copied = true; } catch { const input = document.querySelector('#createdInvitationLink'); input.focus(); input.select(); state.error = 'Select and copy the link above.'; } }}">${() => state.copied ? 'Copied' : 'Copy link'}</button><button type="button" @click="${() => { state.inviteUrl = ''; state.copied = false; }}">Close</button></div></section>
    <div class="invitation-list-heading"><h2>Invitations</h2><button type="button" disabled="${() => state.loading}" @click="${load}">${() => state.loading ? 'Refreshing…' : 'Refresh'}</button></div>
    <p class="admin-muted" hidden="${() => !state.loaded || state.rows.length > 0}">No invitations yet. Create one above.</p>
    <div class="invitation-list">${() => state.rows.map(row => html`<article class="invitation-row"><div class="invitation-row-heading"><strong>${row.label || 'Invitation ' + row.id.slice(0, 8)}</strong><span class="admin-pill">${statuses[row.status] || 'Unavailable'}</span></div><p class="admin-muted">Created ${date(row.createdAt)} by ${row.createdBy}</p><p>Trial deadline: ${date(row.trialExpiresAt)}</p><p>${time(Math.max(0, row.streamSecondsRemaining))} viewing · ${row.activationsRemaining} activations · ${row.controlCodesRemaining} codes remaining</p>${row.registeredEmail ? html`<p>Registered: ${row.registeredEmail}</p>` : ''}<button type="button" hidden="${row.status === 'revoked' || row.status === 'registered'}" disabled="${() => state.revoking === row.id}" @click="${async () => { if (state.revoking) return; state.revoking = row.id; state.error = ''; try { await api({ id: row.id }, '/api/v1/admin/invitations/revoke'); await load(); } catch (error) { state.error = error.message; } finally { state.revoking = ''; } }}">${() => state.revoking === row.id ? 'Revoking…' : 'Revoke invitation'}</button></article>`.key(row.id))}</div>
  </div><p class="invitation-error" role="status">${() => state.error}</p>`(mount);
select(state.view);
document.documentElement.dataset.ticketInvitationsUi = 'arrow';
void load();
