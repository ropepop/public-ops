import assert from "node:assert/strict";
import test from "node:test";
import { verifySpacetimeVersions } from "./check-spacetime-sdk.mjs";

const alignedVersions = {
  browserSDK: "2.6.1",
  rootLockSDK: "2.6.1",
  resolvedSDK: "2.6.1",
  installedSDK: "2.6.1",
  bundledSDK: "2.6.1",
  serverSDK: "2.6.0",
  bindingsCLI: "2.6.0",
};

test("accepts exact patch versions on the same SpacetimeDB release line", () => {
  assert.deepEqual(verifySpacetimeVersions(alignedVersions), {
    browserSDK: "2.6.1",
    bundledSDK: "2.6.1",
    serverSDK: "2.6.0",
    bindingsCLI: "2.6.0",
  });
});

test("rejects a floating browser SDK dependency", () => {
  assert.throws(
    () => verifySpacetimeVersions({ ...alignedVersions, browserSDK: "^2.6.1" }),
    /expected an exact semantic version/
  );
});

test("rejects a stale browser bundle", () => {
  assert.throws(
    () => verifySpacetimeVersions({ ...alignedVersions, bundledSDK: "2.6.0" }),
    /browser bundle SpacetimeDB version.*expected "2\.6\.1"/
  );
});

test("rejects a different server release line", () => {
  assert.throws(
    () => verifySpacetimeVersions({ ...alignedVersions, serverSDK: "2.7.0" }),
    /server SpacetimeDB crate 2\.7\.0 is incompatible.*2\.6\.x/
  );
});
