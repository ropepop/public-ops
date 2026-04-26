import { execFileSync } from "node:child_process";
import { mkdirSync, rmSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const generatedDir = path.join(__dirname, "src", "generated");
const outFile = path.join(__dirname, "..", "internal", "web", "static", "live-client.js");

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

await build({
  entryPoints: [path.join(__dirname, "src", "index.ts")],
  bundle: true,
  format: "iife",
  target: "es2020",
  outfile: outFile,
  sourcemap: false,
  logLevel: "info",
});
