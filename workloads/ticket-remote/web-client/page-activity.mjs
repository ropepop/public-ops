// Foreground samples and delivery have their own owner, independent of the viewer.
export const ACTIVITY_INTERVAL_MS = 5000;
export const ACTIVITY_BATCH_SIZE = 720;
const rigaDate = new Intl.DateTimeFormat('en-CA', {
  timeZone: 'Europe/Riga', year: 'numeric', month: '2-digit', day: '2-digit'
});
const rigaClock = new Intl.DateTimeFormat('en-GB', {
  timeZone: 'Europe/Riga', hour: '2-digit', minute: '2-digit', hourCycle: 'h23'
});

export function firstRetainedActivitySlot(serverMillis) {
  const parts = Object.fromEntries(rigaDate.formatToParts(serverMillis).map(part => [part.type, part.value]));
  const date = new Date(Date.UTC(Number(parts.year), Number(parts.month) - 1, Number(parts.day) - 29));
  // Riga changes its clocks after midnight, so that date's UTC midnight has the
  // same offset as its local midnight, including the two transition days.
  const clock = Object.fromEntries(rigaClock.formatToParts(date).map(part => [part.type, part.value]));
  return (date.getTime() - (Number(clock.hour) * 60 + Number(clock.minute)) * 60000) / ACTIVITY_INTERVAL_MS;
}

export class ActivityOutbox {
  constructor({ accountScopeId, ticketId, indexedDB = globalThis.indexedDB,
    keyRange = globalThis.IDBKeyRange, diagnostic = () => {}, name = 'ticket-page-activity' }) {
    this.scope = [accountScopeId, ticketId];
    this.memory = new Set();
    this.keyRange = keyRange;
    this.diagnostic = diagnostic;
    this.failed = false;
    this.db = new Promise((resolve, reject) => {
      if (!indexedDB) { reject(Error('storage_unavailable')); return; }
      const request = indexedDB.open(name, 1);
      const timer = setTimeout(() => reject(Error('storage_timeout')), 2000);
      request.onupgradeneeded = () => request.result.createObjectStore('slots').createIndex('bySlot', 'slot');
      request.onsuccess = () => {
        clearTimeout(timer);
        if (this.failed) { request.result.close(); return; }
        request.result.onversionchange = () => { request.result.close(); this.storageFailed(); };
        resolve(request.result);
      };
      request.onerror = request.onblocked = () => { clearTimeout(timer); reject(Error('storage_unavailable')); };
    }).catch(() => { this.storageFailed(); return null; });
  }

  storageFailed() {
    if (!this.failed) this.diagnostic('activity_storage_unavailable');
    this.failed = true;
  }

  async transact(write, run) {
    if (this.failed) return null;
    const db = await this.db;
    if (!db || this.failed) return null;
    try {
      return await new Promise((resolve, reject) => {
        const tx = db.transaction('slots', write ? 'readwrite' : 'readonly');
        const timer = setTimeout(() => { try { tx.abort(); } catch {} reject(Error('storage_timeout')); }, 2000);
        tx.onabort = tx.onerror = () => { clearTimeout(timer); reject(Error('storage_unavailable')); };
        let request;
        try { request = run(tx.objectStore('slots')); }
        catch (error) { clearTimeout(timer); tx.abort(); reject(error); return; }
        tx.oncomplete = () => { clearTimeout(timer); resolve(request?.result); };
      });
    } catch { this.storageFailed(); return null; }
  }

  async put(slot) {
    this.memory.add(slot);
    await this.transact(true, store => store.put({ slot }, [...this.scope, slot]));
  }

  prune(firstSlot) {
    if (firstSlot === this.prunedBefore) return this.pruning;
    this.prunedBefore = firstSlot;
    for (const slot of this.memory) if (slot < firstSlot) this.memory.delete(slot);
    this.pruning = this.transact(true, store => {
      const request = store.index('bySlot').openKeyCursor(this.keyRange.upperBound(firstSlot, true));
      request.onsuccess = () => {
        const cursor = request.result;
        if (cursor) { store.delete(cursor.primaryKey); cursor.continue(); }
      };
      return request;
    });
    return this.pruning;
  }

  async pending(firstSlot) {
    await this.prune(firstSlot);
    const keys = await this.transact(false, store => store.getAllKeys(this.keyRange.bound(
      [...this.scope, firstSlot], [...this.scope, Infinity]), ACTIVITY_BATCH_SIZE));
    return [...new Set([...(keys || []).map(key => key[2]), ...this.memory])]
      .sort((a, b) => a - b).slice(0, ACTIVITY_BATCH_SIZE);
  }

  async remove(slots) {
    await this.transact(true, store => { for (const slot of slots) store.delete([...this.scope, slot]); });
    for (const slot of slots) this.memory.delete(slot);
  }

  async close() { (await this.db)?.close(); }
}

export class PageActivity {
  constructor({ config, outbox, fetch = globalThis.fetch.bind(globalThis),
    now = () => performance.now(), wallNow = () => Date.now(),
    visible = () => !document.hidden, online = () => navigator.onLine !== false,
    diagnostic = () => {}, reload = () => location.reload(),
    setTimer = (callback, delay) => setTimeout(callback, delay), clearTimer = timer => clearTimeout(timer) }) {
    Object.assign(this, { config, outbox, fetch, now, wallNow, visible, online, diagnostic, reload, setTimer, clearTimer });
    this.anchor = { server: Date.parse(config.serverTime), monotonic: now(), wall: wallNow() };
    this.lastSampleAt = -Infinity;
    this.lastRequestAt = -Infinity;
    this.persisting = Promise.resolve();
    this.pendingVersion = '';
    this.authBlocked = false;
    this.stopped = false;
  }

  serverNow() {
    const elapsed = this.now() - this.anchor.monotonic;
    // A wall-clock jump or a platform clock paused during sleep makes an offline
    // timestamp ambiguous. Resume collection after the next server clock reply.
    if (elapsed < 0 || Math.abs(this.wallNow() - this.anchor.wall - elapsed) > 2000) return NaN;
    return this.anchor.server + elapsed;
  }

  observe() {
    const now = this.now(), serverTime = this.serverNow();
    if (this.stopped || !this.visible() || !Number.isFinite(serverTime) ||
      now - this.lastSampleAt < ACTIVITY_INTERVAL_MS) return this.persisting;
    this.lastSampleAt = now;
    const slot = Math.floor(serverTime / ACTIVITY_INTERVAL_MS);
    this.persisting = this.persisting.then(async () => {
      await this.outbox.put(slot);
      await this.outbox.prune(firstRetainedActivitySlot(serverTime));
    });
    return this.persisting;
  }

  async tick() {
    await this.observe();
    if (this.pendingVersion) await this.version(this.pendingVersion);
    return this.deliver(this.authBlocked && this.now() - this.lastRequestAt >= 30000);
  }

  async wake() {
    await this.observe();
    if (this.pendingVersion) await this.version(this.pendingVersion);
    return this.deliver(this.authBlocked);
  }

  async version(version) {
    if (typeof version !== 'string' || !version || version === this.config.pageVersion) return;
    this.pendingVersion = version;
    await this.observe();
    await this.persisting;
    if (!this.stopped && this.visible() && this.online()) {
      this.stopped = true;
      this.requestAbort?.abort();
      this.reload();
    }
  }

  deliver(probe = false) {
    if (this.flight) return this.flight;
    if (this.stopped || !this.visible() || !this.online() || (this.authBlocked && !probe)) return Promise.resolve();
    this.flight = this.send(probe).catch(() => { if (!this.stopped) this.diagnostic('activity_delivery_failed'); })
      .finally(() => { this.flight = null; });
    return this.flight;
  }

  async send(probe) {
    // Each batch is bounded. Continue only after explicit acknowledgement makes
    // progress; a partial or empty acknowledgement waits for the next tick.
    do {
      await this.persisting;
      const serverTime = this.serverNow();
      const slots = probe || !Number.isFinite(serverTime) ? [] :
        await this.outbox.pending(firstRetainedActivitySlot(serverTime));
      if (!slots.length && !probe && Number.isFinite(serverTime)) {
        if (this.pendingVersion) await this.version(this.pendingVersion);
        return;
      }
      const abort = new AbortController();
      this.lastRequestAt = this.now();
      this.requestAbort = abort;
      let timer;
      const timeout = new Promise((_, reject) => {
        timer = this.setTimer(() => { abort.abort(); reject(Error('activity_timeout')); }, 10000);
      });
      let payload;
      try {
        payload = await Promise.race([timeout, (async () => {
          const response = await this.fetch('/api/v1/activity', {
            method: 'POST', credentials: 'same-origin', cache: 'no-store', signal: abort.signal,
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ accountScopeId: this.config.accountScopeId, slots })
          });
          if (abort.signal.aborted) throw Error('activity_cancelled');
          if (response.status === 401 || response.status === 403) {
            this.authBlocked = true;
            throw Error('activity_authorization_failed');
          }
          if (!response.ok) throw Error('activity_delivery_failed');
          return response.json();
        })()]);
      } finally { this.clearTimer(timer); this.requestAbort = null; }
      if (this.stopped) return;
      if (payload.accountScopeId !== this.config.accountScopeId) {
        this.authBlocked = true;
        this.diagnostic('activity_account_changed');
        return;
      }
      const confirmedTime = Date.parse(payload.serverTime);
      if (!Number.isFinite(confirmedTime)) throw Error('activity_invalid_response');
      this.authBlocked = false;
      this.anchor = { server: confirmedTime, monotonic: this.now(), wall: this.wallNow() };
      const sent = new Set(slots);
      const confirmed = [...new Set([...(payload.acknowledgedSlots || []), ...(payload.discardedSlots || [])])]
        .filter(slot => Number.isSafeInteger(slot) && sent.has(slot));
      await this.outbox.remove(confirmed);
      await this.version(payload.serverVersion);
      if (this.stopped || !this.visible() || !this.online()) return;
      if (probe || !slots.length) { probe = false; await this.observe(); continue; }
      if (confirmed.length !== slots.length || slots.length < ACTIVITY_BATCH_SIZE) return;
    } while (!this.stopped);
  }

  stop() { this.stopped = true; this.requestAbort?.abort(); }
}
