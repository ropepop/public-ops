import { execFileSync } from "node:child_process";
import { copyFileSync, mkdirSync, mkdtempSync, rmSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const generatedDir = path.join(__dirname, "src", "generated");
const generatedTempDir = mkdtempSync(path.join(os.tmpdir(), "satiksme-public-bindings-"));
const outFile = path.join(__dirname, "..", "internal", "web", "static", "live-client.js");
const publicGeneratedFiles = [
  "satiksmebot_public_area_report_table.ts",
  "satiksmebot_public_incident_comment_table.ts",
  "satiksmebot_public_incident_event_table.ts",
  "satiksmebot_public_live_snapshot_state_table.ts",
  "satiksmebot_public_stop_sighting_table.ts",
  "satiksmebot_public_vehicle_sighting_table.ts",
];

rmSync(generatedDir, { recursive: true, force: true });
mkdirSync(generatedDir, { recursive: true });

try {
  execFileSync(
    "spacetime",
    [
      "generate",
      "--module-path",
      path.join(__dirname, "..", "spacetimedb"),
      "--lang",
      "typescript",
      "--out-dir",
      generatedTempDir,
      "--yes",
    ],
    {
      cwd: path.join(__dirname, ".."),
      stdio: "inherit",
    }
  );

  for (const fileName of publicGeneratedFiles) {
    copyFileSync(path.join(generatedTempDir, fileName), path.join(generatedDir, fileName));
  }
} finally {
  rmSync(generatedTempDir, { recursive: true, force: true });
}

await build({
  entryPoints: [path.join(__dirname, "src", "index.ts")],
  bundle: true,
  format: "iife",
  target: "es2020",
  outfile: outFile,
  sourcemap: false,
  logLevel: "info",
});
