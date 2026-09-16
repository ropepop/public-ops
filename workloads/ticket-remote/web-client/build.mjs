import { execFileSync } from "node:child_process";
import { mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";
import { verifySpacetimeSDKCompatibility } from "./check-spacetime-sdk.mjs";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const generatedDir = path.join(__dirname, "src", "generated");
const generatedBanner = `/* Generated from web-client/src/index.ts. Built with SpacetimeDB browser SDK ${JSON.parse(readFileSync(path.join(__dirname, "package.json"), "utf8")).dependencies.spacetimedb}. Edit sources under web-client/, not internal/web/static/spacetime-client.js. */`;

rmSync(generatedDir, { recursive: true, force: true });
mkdirSync(generatedDir, { recursive: true });

execFileSync(
  "spacetime",
  [
    "generate",
    "--module-path",
    path.join(__dirname, "..", "spacetimedb"),
    "--lang",
    "typescript",
    "--out-dir",
    generatedDir,
    "--yes",
  ],
  {
    cwd: path.join(__dirname, ".."),
    stdio: "inherit",
  }
);

const keptBindings = new Set([
  ...["control_code_request", "member_ticket_switch", "member_hdr_state", "member_hdr_boost_state", "member_limit_state", "phone_current_report", "phone_control_state", "relay_current_report", "stream_desired_state", "stream_viewer_focus", "ticket_action_v_3", "vivi_credential_state", "vivi_reauth_attempt", "owner_vivi_credentials"]
    .map((name) => `ticketremote_${name}_table`),
  ...["command", "close_control_code", "confirm_control_code_browser_capture", "record_activity_tick", "refresh_hdr_state", "refresh_hdr_boost_state", "refresh_limit_state", "set_hdr_preference", "set_limit_preference", "set_stream_focus"]
    .map((name) => `ticketremote_member_${name}_reducer`),
  "ticketremote_owner_set_hdr_display_boost_reducer",
  "ticketremote_owner_prepare_vivi_credentials_reducer",
  "ticketremote_owner_save_vivi_credentials_reducer",
  "ticketremote_owner_clear_vivi_credentials_reducer",
  "ticketremote_admin_schedule_ticket_action_v_3_reducer",
]);
const allowedGeneratedFiles = new Set([
  "index.ts", "types.ts", path.join("types", "procedures.ts"), path.join("types", "reducers.ts"),
  ...[...keptBindings].map((name) => `${name}.ts`),
]);
const keepsType = (name) => keptBindings.has(`ticketremote_${name.slice(12).replace(/[A-Z]/g, (letter, offset) => `${offset ? "_" : ""}${letter.toLowerCase()}`)}_table`);

function rewrite(relativeFile, update) {
  const file = path.join(generatedDir, relativeFile);
  writeFileSync(file, update(readFileSync(file, "utf8")), "utf8");
}

function pruneBindings(relativeFile, importPattern, referencePattern) {
  rewrite(relativeFile, (source) => {
    for (const [line, symbol, name] of source.matchAll(importPattern)) {
      if (keptBindings.has(name)) continue;
      source = source.replace(`${line}\n`, "").replace(referencePattern(symbol, name), "");
    }
    return source;
  });
}

pruneBindings("index.ts", /^import\s+(\w+)\s+from\s+"\.\/(ticketremote_[^"]+)";$/gm, (symbol, name) => name.endsWith("_table")
  ? new RegExp(`\\n  \\w+: __table\\(\\{(?:(?!\\n  \\}, \\w+\\),)[\\s\\S])*\\n  \\}, ${symbol}\\),`, "g")
  : new RegExp(`\\n  __reducerSchema\\("[^"]+", ${symbol}\\),`, "g"));
pruneBindings(path.join("types", "reducers.ts"), /^import\s+(\w+)\s+from\s+"\.\.\/(ticketremote_[^"]+)";$/gm,
  (symbol) => new RegExp(`^export type \\w+ = __Infer<typeof ${symbol}>;\\n`, "gm"));
rewrite("types.ts", (source) => source.replace(/\nexport const (Ticketremote\w+) = __t\.object\("\1", \{[\s\S]*?\n\}\);\nexport type \1 = __Infer<typeof \1>;\n/g,
  (block, name) => keepsType(name) ? block : ""));

const generatedIndex = readFileSync(path.join(generatedDir, "index.ts"), "utf8");
const importedSymbols = new Set([...generatedIndex.matchAll(/^import\s+(\w+)\s+from\s+/gm)].map((match) => match[1]));
const missingReducerImports = [...generatedIndex.matchAll(/__reducerSchema\("[^"]+",\s*(\w+)\)/g)]
  .map((match) => match[1])
  .filter((symbol) => !importedSymbols.has(symbol));
const missingTableImports = [...generatedIndex.matchAll(/^  \}, (\w+)\),/gm)]
  .map((match) => match[1]).filter((symbol) => !importedSymbols.has(symbol));
if (missingTableImports.length > 0) {
  throw new Error(`Pruned bindings left table schemas without imports: ${missingTableImports.join(", ")}`);
}
if (missingReducerImports.length > 0) {
  throw new Error(`Pruned bindings left reducer schemas without imports: ${[...new Set(missingReducerImports)].join(", ")}`);
}
for (const relativeFile of readdirSync(generatedDir, { recursive: true })) {
  const file = path.join(generatedDir, relativeFile);
  if (statSync(file).isFile() && !allowedGeneratedFiles.has(relativeFile)) rmSync(file, { force: true });
}

await build({
  entryPoints: [path.join(__dirname, "src", "index.ts")],
  bundle: true,
  minify: true,
  keepNames: true,
  banner: {
    js: generatedBanner,
  },
  format: "iife",
  target: "es2020",
  outfile: path.join(__dirname, "..", "internal", "web", "static", "spacetime-client.js"),
  sourcemap: false,
  logLevel: "info",
});

verifySpacetimeSDKCompatibility();
// Bundling does not resolve undeclared schema symbols. Load the emitted IIFE
// without connecting so broken generated references fail before deployment.
execFileSync(process.execPath, ["--input-type=module", "-e", `
  globalThis.window = {};
  await import(${JSON.stringify(path.join(__dirname, "..", "internal", "web", "static", "spacetime-client.js"))});
  if (typeof window.TicketSpacetime?.create !== "function") throw new Error("Ticket client did not initialize");
`], { stdio: "inherit" });
