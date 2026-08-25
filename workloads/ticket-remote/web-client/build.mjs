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
  ...["activation_decision", "activation_eligibility", "control_code_fast_state", "control_code_request", "latency_link_v_1", "member_limit_state", "phone_current_report", "relay_current_report", "stream_desired_state", "stream_viewer_focus", "ticket_interaction", "ticket_action_v_3", "ticket_slider_region_v_3"]
    .map((name) => `ticketremote_${name}_table`),
  ...["close_control_code", "confirm_control_code_browser_capture", "recover_stream", "refresh_limit_state", "request_control_code", "request_keyframe", "set_limit_preference", "set_stream_focus", "request_ticket_action_v_3"]
    .map((name) => `ticketremote_member_${name}_reducer`),
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
  ? new RegExp(`\\n  ${name.slice(0, -6)}: __table\\(\\{[\\s\\S]*?\\n  \\}, ${symbol}\\),`, "g")
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
