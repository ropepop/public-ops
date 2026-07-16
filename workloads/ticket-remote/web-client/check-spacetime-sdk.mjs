import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

function read(relativePath, json = false) {
  const value = readFileSync(path.join(__dirname, relativePath), "utf8");
  return json ? JSON.parse(value) : value;
}

function releaseLine(version) {
  const [, major, minor] = /^(\d+)\.(\d+)\.(\d+)$/.exec(String(version || "")) || [];
  if (!major) {
    throw new Error(`expected an exact semantic version, got ${JSON.stringify(version)}`);
  }
  return `${major}.${minor}`;
}

function versionFrom(label, relativePath, pattern) {
  const [, version] = read(relativePath).match(pattern) || [];
  if (!version) {
    throw new Error(`could not determine ${label}`);
  }
  return version;
}

export function verifySpacetimeVersions(versions) {
  const { browserSDK, bundledSDK, serverSDK, bindingsCLI } = versions;
  const expectedLine = releaseLine(browserSDK);
  for (const [label, key] of [
    ["package-lock root SpacetimeDB dependency", "rootLockSDK"],
    ["package-lock resolved SpacetimeDB version", "resolvedSDK"],
    ["installed SpacetimeDB version", "installedSDK"],
    ["browser bundle SpacetimeDB version", "bundledSDK"],
  ]) {
    const version = versions[key];
    if (version !== browserSDK) {
      throw new Error(`${label} is ${JSON.stringify(version)}; expected ${JSON.stringify(browserSDK)}`);
    }
  }
  for (const [label, version] of [
    ["server SpacetimeDB crate", serverSDK],
    ["generated bindings CLI", bindingsCLI],
  ]) {
    if (releaseLine(version) !== expectedLine) {
      throw new Error(`${label} ${version} is incompatible with the Ticket server release line ${expectedLine}.x`);
    }
  }

  return { browserSDK, bundledSDK, serverSDK, bindingsCLI };
}

export function verifySpacetimeSDKCompatibility() {
  const packageJSON = read("package.json", true);
  const packageLock = read("package-lock.json", true);
  return verifySpacetimeVersions({
    browserSDK: packageJSON.dependencies?.spacetimedb,
    rootLockSDK: packageLock.packages?.[""]?.dependencies?.spacetimedb,
    resolvedSDK: packageLock.packages?.["node_modules/spacetimedb"]?.version,
    installedSDK: read("node_modules/spacetimedb/package.json", true).version,
    serverSDK: versionFrom("server SpacetimeDB crate version", "../spacetimedb/Cargo.lock",
      /\[\[package\]\]\nname = "spacetimedb"\nversion = "([^"]+)"/),
    bindingsCLI: versionFrom("generated bindings CLI version", "src/generated/index.ts",
      /generated using spacetimedb cli version (\d+\.\d+\.\d+)/i),
    bundledSDK: versionFrom("browser bundle SpacetimeDB SDK version", "../internal/web/static/spacetime-client.js",
      /Built with SpacetimeDB browser SDK (\d+\.\d+\.\d+)/),
  });
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const versions = verifySpacetimeSDKCompatibility();
  console.log(`SpacetimeDB compatibility verified: browser SDK ${versions.browserSDK}, bundle ${versions.bundledSDK}, server crate ${versions.serverSDK}, generated bindings CLI ${versions.bindingsCLI}.`);
}
