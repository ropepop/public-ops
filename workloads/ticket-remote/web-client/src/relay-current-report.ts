const UNKNOWN_FRAME_AGE_MILLIS = Number.MAX_SAFE_INTEGER;

export function relayLastFrameAgeMillis(report: any, nowMillis = Date.now()): number {
  const value = String(report && (report.lastFrameAt || report.last_frame_at) || "").trim();
  const lastFrameAtMillis = Date.parse(value);
  if (!Number.isFinite(nowMillis) || !Number.isFinite(lastFrameAtMillis) || lastFrameAtMillis > nowMillis) {
    return UNKNOWN_FRAME_AGE_MILLIS;
  }
  return Math.min(UNKNOWN_FRAME_AGE_MILLIS, nowMillis - lastFrameAtMillis);
}
