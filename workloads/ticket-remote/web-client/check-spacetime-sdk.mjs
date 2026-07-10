import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

function readJSON(filePath) {
  return JSON.parse(readFileSync(filePath, "utf8"));
}

function releaseLine(version) {
  const match = /^(\d+)\.(\d+)\.(\d+)$/.exec(String(version || ""));
  if (!match) {
    throw new Error(`expected an exact semantic version, got ${JSON.stringify(version)}`);
  }
  return `${match[1]}.${match[2]}`;
}

function requireVersion(label, source, pattern) {
  const match = source.match(pattern);
  if (!match) {
    throw new Error(`could not determine ${label}`);
  }
  return match[1];
}

function assertEqual(label, actual, expected) {
  if (actual !== expected) {
    throw new Error(`${label} is ${JSON.stringify(actual)}; expected ${JSON.stringify(expected)}`);
  }
}

function assertCompatible(label, version, expectedLine) {
  const actualLine = releaseLine(version);
  if (actualLine !== expectedLine) {
    throw new Error(`${label} ${version} is incompatible with the Ticket server release line ${expectedLine}.x`);
  }
}

export function verifySpacetimeVersions({
  browserSDK,
  rootLockSDK,
  resolvedSDK,
  installedSDK,
  bundledSDK,
  serverSDK,
  bindingsCLI,
}) {
  const expectedLine = releaseLine(browserSDK);

  assertEqual("package-lock root SpacetimeDB dependency", rootLockSDK, browserSDK);
  assertEqual("package-lock resolved SpacetimeDB version", resolvedSDK, browserSDK);
  assertEqual("installed SpacetimeDB version", installedSDK, browserSDK);
  assertEqual("browser bundle SpacetimeDB version", bundledSDK, browserSDK);
  assertCompatible("server SpacetimeDB crate", serverSDK, expectedLine);
  assertCompatible("generated bindings CLI", bindingsCLI, expectedLine);

  return { browserSDK, bundledSDK, serverSDK, bindingsCLI };
}

export function verifySpacetimeSDKCompatibility() {
  const packageJSON = readJSON(path.join(__dirname, "package.json"));
  const packageLock = readJSON(path.join(__dirname, "package-lock.json"));
  const installedPackage = readJSON(path.join(__dirname, "node_modules", "spacetimedb", "package.json"));
  const cargoLock = readFileSync(path.join(__dirname, "..", "spacetimedb", "Cargo.lock"), "utf8");
  const generatedBindings = readFileSync(path.join(__dirname, "src", "generated", "index.ts"), "utf8");
  const browserBundle = readFileSync(path.join(__dirname, "..", "internal", "web", "static", "spacetime-client.js"), "utf8");

  const browserSDK = packageJSON.dependencies?.spacetimedb;
  const rootLockSDK = packageLock.packages?.[""]?.dependencies?.spacetimedb;
  const resolvedSDK = packageLock.packages?.["node_modules/spacetimedb"]?.version;
  const serverSDK = requireVersion(
    "server SpacetimeDB crate version",
    cargoLock,
    /\[\[package\]\]\nname = "spacetimedb"\nversion = "([^"]+)"/
  );
  const bindingsCLI = requireVersion(
    "generated bindings CLI version",
    generatedBindings,
    /generated using spacetimedb cli version (\d+\.\d+\.\d+)/i
  );
  const bundledSDK = requireVersion(
    "browser bundle SpacetimeDB SDK version",
    browserBundle,
    /Built with SpacetimeDB browser SDK (\d+\.\d+\.\d+)/
  );

  return verifySpacetimeVersions({
    browserSDK,
    rootLockSDK,
    resolvedSDK,
    installedSDK: installedPackage.version,
    bundledSDK,
    serverSDK,
    bindingsCLI,
  });
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const versions = verifySpacetimeSDKCompatibility();
  console.log(
    `SpacetimeDB compatibility verified: browser SDK ${versions.browserSDK}, ` +
      `bundle ${versions.bundledSDK}, server crate ${versions.serverSDK}, ` +
      `generated bindings CLI ${versions.bindingsCLI}.`
  );
}
