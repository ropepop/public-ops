function normalizeEnabled(value) {
  return value === true;
}

function noop() {}

/**
 * Keeps the user's immediate HDR choice separate from the eventually
 * consistent account projection.
 *
 * A local choice remains authoritative in this browser until its write has
 * succeeded and the matching projection has arrived. This prevents an older
 * subscription row from undoing a fresh click. Failed writes are never
 * retried without another explicit choice.
 */
export class ExperimentalHDRPreferenceController {
  constructor(options = {}) {
    if (typeof options.applyEnabled !== 'function') {
      throw new TypeError('applyEnabled must be a function');
    }
    if (typeof options.persistEnabled !== 'function') {
      throw new TypeError('persistEnabled must be a function');
    }

    this.applyEnabled = options.applyEnabled;
    this.persistEnabled = options.persistEnabled;
    this.onStatus = typeof options.onStatus === 'function' ? options.onStatus : noop;
    this.onFailure = typeof options.onFailure === 'function' ? options.onFailure : noop;

    this.currentEnabled = false;
    this.desiredEnabled = false;
    this.projectionKnown = false;
    this.projectedEnabled = false;
    this.localOverride = false;
    this.failed = false;
    this.phase = 'default';
    this.choiceRevision = 0;
    this.inFlight = null;
    this.successfulValue = null;
    this.idleWaiters = [];

    this.applyEnabled(false, { reason: 'default' });
    this.emitStatus();
  }

  get enabled() {
    return this.currentEnabled;
  }

  getState() {
    return Object.freeze({
      phase: this.phase,
      enabled: this.currentEnabled,
      desiredEnabled: this.desiredEnabled,
      projectionKnown: this.projectionKnown,
      projectedEnabled: this.projectionKnown ? this.projectedEnabled : null,
      localOverride: this.localOverride,
      inFlight: this.inFlight !== null,
      inFlightValue: this.inFlight ? this.inFlight.value : null,
      failed: this.failed
    });
  }

  /**
   * Applies an authoritative account projection when no local choice is being
   * protected. A missing row is the deterministic disabled default.
   *
   * Returns true when the projection became authoritative in this session.
   */
  observe(enabled) {
    this.projectionKnown = true;
    this.projectedEnabled = normalizeEnabled(enabled);

    if (this.localOverride) {
      if (!this.failed && !this.inFlight && this.successfulValue === this.desiredEnabled &&
          this.projectedEnabled === this.desiredEnabled) {
        this.localOverride = false;
        this.successfulValue = null;
        this.desiredEnabled = this.projectedEnabled;
        this.setCurrentEnabled(this.projectedEnabled, 'projection');
        this.phase = 'synced';
        this.emitStatus();
        return true;
      }
      return false;
    }

    this.desiredEnabled = this.projectedEnabled;
    this.setCurrentEnabled(this.projectedEnabled, 'projection');
    this.phase = 'synced';
    this.failed = false;
    this.successfulValue = null;
    this.emitStatus();
    return true;
  }

  /**
   * Records an explicit user choice. It takes effect immediately and is then
   * persisted in the background. Rapid choices retain at most one active write
   * and one coalesced latest desired value.
   */
  choose(enabled) {
    this.choiceRevision += 1;
    this.desiredEnabled = normalizeEnabled(enabled);
    this.localOverride = true;
    this.failed = false;
    this.successfulValue = null;
    this.setCurrentEnabled(this.desiredEnabled, 'user', true);
    this.pump();
    return this.choiceRevision;
  }

  whenIdle() {
    if (!this.inFlight) return Promise.resolve(this.getState());
    return new Promise((resolve) => {
      this.idleWaiters.push(resolve);
    });
  }

  setCurrentEnabled(enabled, reason, force = false) {
    if (!force && this.currentEnabled === enabled) return;
    this.currentEnabled = enabled;
    this.applyEnabled(enabled, { reason });
  }

  pump() {
    if (this.inFlight || !this.localOverride || this.failed) return;

    const request = Object.freeze({
      value: this.desiredEnabled,
      revision: this.choiceRevision
    });
    this.inFlight = request;
    this.phase = 'saving';
    this.emitStatus();

    Promise.resolve()
      .then(() => this.persistEnabled(request.value))
      .then(
        () => this.completeSuccess(request),
        () => this.completeFailure(request)
      );
  }

  completeSuccess(request) {
    if (this.inFlight !== request) return;
    this.inFlight = null;

    if (this.desiredEnabled !== request.value) {
      this.pump();
      this.resolveIdleIfReady();
      return;
    }

    this.failed = false;
    this.successfulValue = request.value;
    if (this.projectionKnown && this.projectedEnabled === this.desiredEnabled) {
      this.localOverride = false;
      this.successfulValue = null;
      this.phase = 'synced';
    } else {
      this.phase = 'saved';
    }
    this.emitStatus();
    this.resolveIdleIfReady();
  }

  completeFailure(request) {
    if (this.inFlight !== request) return;
    this.inFlight = null;
    const hasNewerChoice = this.choiceRevision > request.revision;

    this.failed = true;
    this.successfulValue = null;
    this.phase = 'failed';
    this.emitStatus();
    try {
      this.onFailure(Object.freeze({
        code: 'hdr_preference_write_failed',
        state: this.getState()
      }));
    } catch {
      // A presentation/logging callback must not turn a failed preference write
      // into a live-stream failure or an automatic persistence retry.
    }

    if (hasNewerChoice) {
      this.failed = false;
      this.pump();
    }
    this.resolveIdleIfReady();
  }

  emitStatus() {
    try {
      this.onStatus(this.getState());
    } catch {
      // Status presentation is advisory; controller safety cannot depend on it.
    }
  }

  resolveIdleIfReady() {
    if (this.inFlight || this.idleWaiters.length === 0) return;
    const state = this.getState();
    const waiters = this.idleWaiters.splice(0);
    for (const resolve of waiters) resolve(state);
  }
}
