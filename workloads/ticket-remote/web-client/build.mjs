import { execFileSync } from "node:child_process";
import { mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const generatedDir = path.join(__dirname, "src", "generated");
const outFile = path.join(__dirname, "..", "internal", "web", "static", "spacetime-client.js");
const generatedBanner = "/* Generated from web-client/src/index.ts. Edit sources under web-client/, not internal/web/static/spacetime-client.js. */";

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

const allowedGeneratedFiles = new Set([
  "index.ts",
  "ticketremote_ack_stream_command_reducer.ts",
  "ticketremote_append_safe_operational_log_reducer.ts",
  "ticketremote_append_stream_command_reducer.ts",
  "ticketremote_cleanup_expired_reducer.ts",
  "ticketremote_control_code_request_table.ts",
  "ticketremote_control_code_fast_state_table.ts",
  "ticketremote_member_append_safe_operational_log_reducer.ts",
  "ticketremote_member_close_control_code_reducer.ts",
  "ticketremote_member_confirm_control_code_browser_capture_reducer.ts",
  "ticketremote_member_prepare_control_code_reducer.ts",
  "ticketremote_member_recover_stream_reducer.ts",
  "ticketremote_member_remove_member_reducer.ts",
  "ticketremote_member_request_control_code_reducer.ts",
  "ticketremote_member_request_keyframe_reducer.ts",
  "ticketremote_member_set_stream_focus_reducer.ts",
  "ticketremote_member_upsert_member_reducer.ts",
  "ticketremote_phone_current_report_table.ts",
  "ticketremote_register_service_identity_reducer.ts",
  "ticketremote_relay_current_report_table.ts",
  "ticketremote_remove_member_reducer.ts",
  "ticketremote_service_bootstrap_reducer.ts",
  "ticketremote_service_phone_backend_table.ts",
  "ticketremote_service_stream_command_table.ts",
  "ticketremote_service_ticket_member_table.ts",
  "ticketremote_service_ticket_table.ts",
  "ticketremote_set_stream_desired_state_reducer.ts",
  "ticketremote_stream_command_signal_table.ts",
  "ticketremote_stream_desired_state_table.ts",
  "ticketremote_stream_viewer_focus_table.ts",
  "ticketremote_update_control_code_request_reducer.ts",
  "ticketremote_update_phone_current_report_reducer.ts",
  "ticketremote_update_phone_reducer.ts",
  "ticketremote_update_relay_current_report_reducer.ts",
  "ticketremote_upsert_member_reducer.ts",
  "types.ts",
  path.join("types", "procedures.ts"),
  path.join("types", "reducers.ts"),
]);

const allowedTypeNames = new Set([
  "TicketremoteAuthConfig",
  "TicketremoteCleanupSchedule",
  "TicketremoteControlCodeOwner",
  "TicketremoteControlCodeFastState",
  "TicketremoteControlCodeRequest",
  "TicketremotePhoneBackend",
  "TicketremotePhoneCurrentReport",
  "TicketremoteRelayCurrentReport",
  "TicketremoteSafeOperationalLog",
  "TicketremoteServiceIdentity",
  "TicketremoteServiceMember",
  "TicketremoteServicePhone",
  "TicketremoteServiceStreamCommand",
  "TicketremoteServiceTicket",
  "TicketremoteStreamCommand",
  "TicketremoteStreamCommandSignal",
  "TicketremoteStreamDesiredState",
  "TicketremoteStreamViewerFocus",
  "TicketremoteTicket",
  "TicketremoteTicketMember",
]);

function escapeRegExp(value) {
  return String(value).replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function walkFiles(root, base = root) {
  const output = [];
  for (const entry of readdirSync(root)) {
    const fullPath = path.join(root, entry);
    const stat = statSync(fullPath);
    if (stat.isDirectory()) {
      output.push(...walkFiles(fullPath, base));
    } else {
      output.push(path.relative(base, fullPath));
    }
  }
  return output;
}

function pruneGeneratedIndex() {
  const indexFile = path.join(generatedDir, "index.ts");
  let source = readFileSync(indexFile, "utf8");
  for (const match of [...source.matchAll(/^import\s+(\w+)\s+from\s+"\.\/(ticketremote_[^"]+)";$/gm)]) {
    const [line, importName, generatedName] = match;
    const relativeFile = `${generatedName}.ts`;
    if (allowedGeneratedFiles.has(relativeFile)) continue;
    source = source.replace(`${line}\n`, "");
    if (generatedName.endsWith("_table")) {
      const tableName = generatedName.slice(0, -"_table".length);
      source = source.replace(new RegExp(`\\n  ${escapeRegExp(tableName)}: __table\\(\\{[\\s\\S]*?\\n  \\}, ${escapeRegExp(importName)}\\),`, "g"), "");
    } else if (generatedName.endsWith("_reducer")) {
      const reducerName = generatedName.slice(0, -"_reducer".length);
      source = source.replace(new RegExp(`\\n  __reducerSchema\\("${escapeRegExp(reducerName)}", ${escapeRegExp(importName)}\\),`, "g"), "");
    }
  }
  writeFileSync(indexFile, source, "utf8");
}

function pruneGeneratedReducerTypes() {
  const reducerTypesFile = path.join(generatedDir, "types", "reducers.ts");
  let source = readFileSync(reducerTypesFile, "utf8");
  for (const match of [...source.matchAll(/^import\s+(\w+)\s+from\s+"\.\.\/(ticketremote_[^"]+)";$/gm)]) {
    const [line, importName, generatedName] = match;
    const relativeFile = `${generatedName}.ts`;
    if (allowedGeneratedFiles.has(relativeFile)) continue;
    source = source.replace(`${line}\n`, "");
    source = source.replace(new RegExp(`^export type \\w+ = __Infer<typeof ${escapeRegExp(importName)}>;\\n`, "gm"), "");
  }
  writeFileSync(reducerTypesFile, source, "utf8");
}

function pruneGeneratedTypes() {
  const typesFile = path.join(generatedDir, "types.ts");
  let source = readFileSync(typesFile, "utf8");
  source = source.replace(/\nexport const (Ticketremote\w+) = __t\.object\("\1", \{[\s\S]*?\n\}\);\nexport type \1 = __Infer<typeof \1>;\n/g, (block, name) => {
    return allowedTypeNames.has(name) ? block : "";
  });
  writeFileSync(typesFile, source, "utf8");
}

function preserveSafeLogReducerIDArgs() {
  for (const fileName of [
    "ticketremote_append_safe_operational_log_reducer.ts",
    "ticketremote_member_append_safe_operational_log_reducer.ts",
  ]) {
    const filePath = path.join(generatedDir, fileName);
    let source = readFileSync(filePath, "utf8");
    if (source.includes("id: __t.string()")) continue;
    source = source.replace("export default {\n", "export default {\n  id: __t.string(),\n");
    writeFileSync(filePath, source, "utf8");
  }
}

pruneGeneratedIndex();
pruneGeneratedReducerTypes();
pruneGeneratedTypes();
preserveSafeLogReducerIDArgs();
for (const relativeFile of walkFiles(generatedDir)) {
  if (!allowedGeneratedFiles.has(relativeFile)) {
    rmSync(path.join(generatedDir, relativeFile), { force: true });
  }
}

await build({
  entryPoints: [path.join(__dirname, "src", "index.ts")],
  bundle: true,
  banner: {
    js: generatedBanner,
  },
  format: "iife",
  target: "es2020",
  outfile: outFile,
  sourcemap: false,
  logLevel: "info",
});
