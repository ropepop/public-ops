import {
  CLIENT_HDR_TARGET_DISPLAY_BOOST,
  normalizeClientHDRDisplayBoost
} from './client-hdr-core.mjs';

function noop() {}

/**
 * Keeps a just-selected browser HDR boost authoritative until the matching
 * account projection arrives. Spacetime publishes many unrelated updates, so
 * an older projection must never undo a newer selector choice.
 */
export class ClientHDRBoostPreferenceController {
  constructor(options = {}) {
    if (typeof options.applyBoost !== 'function') throw new TypeError('applyBoost must be a function');
    if (typeof options.persistBoost !== 'function') throw new TypeError('persistBoost must be a function');
    this.applyBoost = options.applyBoost;
    this.persistBoost = options.persistBoost;
    this.onStatus = typeof options.onStatus === 'function' ? options.onStatus : noop;
    this.onFailure = typeof options.onFailure === 'function' ? options.onFailure : noop;
    this.currentBoost = CLIENT_HDR_TARGET_DISPLAY_BOOST;
    this.desiredBoost = CLIENT_HDR_TARGET_DISPLAY_BOOST;
    this.projectionKnown = false;
    this.projectedBoost = CLIENT_HDR_TARGET_DISPLAY_BOOST;
    this.localOverride = false;
    this.failed = false;
    this.phase = 'default';
    this.choiceRevision = 0;
    this.inFlight = null;
    this.successfulValue = null;
    this.idleWaiters = [];
    this.applyBoost(this.currentBoost, { reason: 'default' });
    this.emitStatus();
  }

  get boost() {
    return this.currentBoost;
  }

  getState() {
    return Object.freeze({
      phase: this.phase,
      boost: this.currentBoost,
      desiredBoost: this.desiredBoost,
      projectionKnown: this.projectionKnown,
      projectedBoost: this.projectionKnown ? this.projectedBoost : null,
      localOverride: this.localOverride,
      inFlight: this.inFlight !== null,
      inFlightValue: this.inFlight ? this.inFlight.value : null,
      failed: this.failed
    });
  }

  observe(value) {
    this.projectionKnown = true;
    this.projectedBoost = normalizeClientHDRDisplayBoost(value);
    if (this.localOverride) {
      if (!this.failed && !this.inFlight && this.successfulValue === this.desiredBoost &&
          this.projectedBoost === this.desiredBoost) {
        this.localOverride = false;
        this.successfulValue = null;
        this.desiredBoost = this.projectedBoost;
        this.setCurrentBoost(this.projectedBoost, 'projection');
        this.phase = 'synced';
        this.emitStatus();
        return true;
      }
      return false;
    }
    this.desiredBoost = this.projectedBoost;
    this.setCurrentBoost(this.projectedBoost, 'projection');
    this.phase = 'synced';
    this.failed = false;
    this.successfulValue = null;
    this.emitStatus();
    return true;
  }

  choose(value) {
    this.choiceRevision += 1;
    this.desiredBoost = normalizeClientHDRDisplayBoost(value);
    this.localOverride = true;
    this.failed = false;
    this.successfulValue = null;
    this.setCurrentBoost(this.desiredBoost, 'user', true);
    this.pump();
    return this.choiceRevision;
  }

  reset(value = CLIENT_HDR_TARGET_DISPLAY_BOOST) {
    this.choiceRevision += 1;
    this.inFlight = null;
    this.successfulValue = null;
    this.localOverride = false;
    this.failed = false;
    this.projectionKnown = false;
    this.projectedBoost = CLIENT_HDR_TARGET_DISPLAY_BOOST;
    this.desiredBoost = normalizeClientHDRDisplayBoost(value);
    this.phase = 'default';
    this.setCurrentBoost(this.desiredBoost, 'reset', true);
    this.emitStatus();
    this.resolveIdleIfReady();
  }

  whenIdle() {
    if (!this.inFlight) return Promise.resolve(this.getState());
    return new Promise((resolve) => this.idleWaiters.push(resolve));
  }

  setCurrentBoost(value, reason, force = false) {
    if (!force && this.currentBoost === value) return;
    this.currentBoost = value;
    this.applyBoost(value, { reason });
  }

  pump() {
    if (this.inFlight || !this.localOverride || this.failed) return;
    const request = Object.freeze({ value: this.desiredBoost, revision: this.choiceRevision });
    this.inFlight = request;
    this.phase = 'saving';
    this.emitStatus();
    Promise.resolve()
      .then(() => this.persistBoost(request.value))
      .then(
        () => this.completeSuccess(request),
        () => this.completeFailure(request)
      );
  }

  completeSuccess(request) {
    if (this.inFlight !== request) return;
    this.inFlight = null;
    if (this.desiredBoost !== request.value) {
      this.pump();
      this.resolveIdleIfReady();
      return;
    }
    this.failed = false;
    this.successfulValue = request.value;
    if (this.projectionKnown && this.projectedBoost === this.desiredBoost) {
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
        code: 'hdr_boost_write_failed',
        state: this.getState()
      }));
    } catch (_) {}
    if (hasNewerChoice) {
      this.failed = false;
      this.pump();
    }
    this.resolveIdleIfReady();
  }

  emitStatus() {
    try { this.onStatus(this.getState()); } catch (_) {}
  }

  resolveIdleIfReady() {
    if (this.inFlight || this.idleWaiters.length === 0) return;
    const state = this.getState();
    const waiters = this.idleWaiters.splice(0);
    for (const resolve of waiters) resolve(state);
  }
}
