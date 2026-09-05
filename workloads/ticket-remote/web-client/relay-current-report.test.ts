import assert from "node:assert/strict";
import test from "node:test";
import { relayLastFrameAgeMillis } from "./src/relay-current-report.ts";

const now = Date.parse("2026-09-04T12:00:10Z");

test("lastFrameAt overrides the legacy constant-zero age", () => {
  assert.equal(relayLastFrameAgeMillis({
    lastFrameAt: "2026-09-04T12:00:00Z",
    lastFrameAgoMillis: 0,
  }, now), 10_000);
});

test("snake-case rows also derive age only from lastFrameAt", () => {
  assert.equal(relayLastFrameAgeMillis({
    last_frame_at: "2026-09-04T12:00:07.500Z",
    last_frame_ago_millis: 0,
  }, now), 2_500);
});

test("missing, malformed, and future frame timestamps fail closed", () => {
  for (const lastFrameAt of [undefined, "not-a-time", "2026-09-04T12:00:11Z"]) {
    assert.equal(
      relayLastFrameAgeMillis({ lastFrameAt, lastFrameAgoMillis: 0 }, now),
      Number.MAX_SAFE_INTEGER,
    );
  }
});
