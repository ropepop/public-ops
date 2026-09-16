export const RECOVERY_ERROR_MS = 30000;
export const RECOVERY_ATTEMPT_MS = 10000;
export const RECOVERY_RETRY_MS = 1000;

// Page-lifetime receipt history survives transport replacement. This clock
// measures visible silence, not picture freshness or command readiness.
export class FrameSilence {
  constructor(now = performance.now()) {
    this.at = now;
    this.active = false;
    this.elapsed = 0;
    this.picture = null;
    this.receivedAt = -Infinity;
  }

  step(now, active = this.active) {
    if (this.active) this.elapsed += Math.max(0, now - this.at);
    this.at = now;
    this.active = active;
  }

  receive(picture, receivedAt, now = performance.now()) {
    if (!this.active || !Number.isFinite(receivedAt) || receivedAt > now || receivedAt < this.receivedAt ||
      (this.picture?.epoch === picture.epoch && picture.sequence <= this.picture.sequence)) return false;
    this.step(now);
    // A buffered startup frame keeps its arrival time. Previously hidden time
    // cannot exceed the visible silence already accumulated by this owner.
    this.elapsed = Math.min(this.elapsed, Math.max(0, now - receivedAt));
    this.picture = { epoch: picture.epoch, sequence: picture.sequence };
    this.receivedAt = receivedAt;
    return true;
  }

  get showError() {
    return this.active && this.elapsed >= RECOVERY_ERROR_MS;
  }
}

// The page's existing tick drives this owner. Callbacks own resources, never retries.
export class ConnectionRecovery {
  constructor({ start, stop, now = performance.now() }) {
    this.start = start;
    this.stop = stop;
    this.generation = 0;
    this.phase = 'waiting';
    this.visible = false;
    this.paused = false;
    this.usable = false;
    this.downtime = 0;
    this.at = now;
    this.nextAt = now;
    this.startedAt = now;
  }

  advance(now) {
    if (this.visible && !this.paused && !this.usable) this.downtime += Math.max(0, now - this.at);
    this.at = now;
  }

  accepts(generation) {
    return generation === this.generation && this.phase !== 'waiting';
  }

  get showError() {
    return this.visible && !this.paused && !this.usable && this.downtime >= RECOVERY_ERROR_MS;
  }

  suspend(now) {
    this.advance(now);
    this.visible = false;
    this.usable = false;
    this.phase = 'waiting';
    this.nextAt = now;
    this.stop();
  }

  fail(generation, now) {
    if (!this.accepts(generation)) return false;
    this.advance(now);
    this.nextAt = now + (this.phase === 'live' ? 0 : RECOVERY_RETRY_MS);
    this.phase = 'waiting';
    this.usable = false;
    this.stop();
    return true;
  }

  wake(now) {
    // An online event or button must not replace an attempt already in progress.
    if (this.phase === 'waiting') this.nextAt = now;
  }

  step(now, { visible, healthy, paused = false }) {
    this.advance(now);
    this.visible = visible;
    this.paused = paused;
    if (!visible) return;
    if (healthy && this.phase !== 'waiting') {
      this.usable = true;
      this.phase = 'live';
      this.downtime = 0;
      return;
    }
    if (this.phase === 'live') this.fail(this.generation, now);
    if (this.phase === 'connecting' && now - this.startedAt >= RECOVERY_ATTEMPT_MS) this.fail(this.generation, now);
    if (this.phase !== 'waiting' || now < this.nextAt) return;
    this.phase = 'connecting';
    this.startedAt = now;
    const generation = ++this.generation;
    try { this.start(generation); }
    catch { this.fail(generation, now); }
  }
}
