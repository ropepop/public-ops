const blockedPhases = new Set(['quiescing', 'stopping', 'confirmed', 'failed']);
const completePhases = new Set(['reloading', 'asleep', 'live']);

export class ColdRestartPage {
  constructor({ openedAt, hidden, pause, restart, recall, remember }) {
    Object.assign(this, { openedAt, hidden, pause, restart, recall, remember });
    this.blocked = false;
    this.phase = '';
    this.operationId = '';
    this.pending = '';
    this.pausedId = '';
  }

  update(row) {
    const id = row?.coldRestartId || '';
    const phase = row?.coldRestartPhase || '';
    if (!id) return;
    this.operationId = id;
    this.phase = phase;
    this.blocked = blockedPhases.has(phase);
    if (this.blocked && this.pausedId !== id) {
      this.pausedId = id;
      this.pending = id;
      this.pause();
    }
    if (completePhases.has(phase) && this.recall() !== id &&
      (this.pending === id || Date.parse(row.coldRestartStartedAt) > this.openedAt)) {
      this.pending = id;
      this.resume();
    }
  }

  resume() {
    if (!this.pending || this.blocked || this.hidden() || !completePhases.has(this.phase)) return false;
    const id = this.pending;
    this.pending = '';
    this.remember(id);
    this.restart();
    return true;
  }
}
