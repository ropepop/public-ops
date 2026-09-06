// Control authority comes only from the current subscribed phone observation.
export function phoneControlNow(clock, monotonic, wall) {
  if (!clock) return NaN;
  const elapsed = monotonic - clock.receivedMonotonic;
  if (!Number.isFinite(elapsed) || elapsed < 0 || elapsed >= 30000 ||
    Math.abs(wall - clock.receivedWall - elapsed) > 250) return NaN;
  return clock.serverUpperAtReceipt + elapsed;
}

export function phoneControlReady(row, now = Date.now()) {
  if (!row || row.ready !== true || row.busy !== false ||
    !['unactivated_detail', 'activated_detail'].includes(row.view) ||
    !String(row.sessionId || '').startsWith('pc-') ||
    !String(row.contextRevision || '').startsWith(`${row.sessionId}:`)) return false;
  const observed = Date.parse(row.observedAt), expires = Date.parse(row.expiresAt);
  return Number.isFinite(now) && Number.isFinite(observed) && Number.isFinite(expires) &&
    observed <= now && now < expires && expires - observed <= 3000 && expires > observed;
}

export function phoneRegistrationRegion(row, now = Date.now()) {
  if (!phoneControlReady(row, now) || row.view !== 'unactivated_detail') return null;
  const bounds = ['leftBasisPoints', 'topBasisPoints', 'rightBasisPoints', 'bottomBasisPoints'];
  if (!bounds.every((key) => Number.isInteger(row[key]) && row[key] >= 0 && row[key] <= 10000) ||
    row.rightBasisPoints <= row.leftBasisPoints || row.bottomBasisPoints <= row.topBasisPoints) return null;
  return { ...row, proofActionId: row.contextRevision };
}

export function phoneRegistrationSnapshot(row, layoutRevision, now = Date.now()) {
  const region = phoneRegistrationRegion(row, now);
  return region ? { ...region, layoutRevision } : null;
}

export function phoneRegistrationMatches(snapshot, row, layoutRevision, now = Date.now()) {
  const current = phoneRegistrationSnapshot(row, layoutRevision, now);
  return Boolean(snapshot && current && [
    'sessionId', 'sessionGeneration', 'contextRevision', 'view', 'layoutRevision',
    'leftBasisPoints', 'topBasisPoints', 'rightBasisPoints', 'bottomBasisPoints',
  ].every((key) => snapshot[key] === current[key]));
}
