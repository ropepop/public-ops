import path from "node:path";
import { fileURLToPath } from "node:url";
import { readFile, rm, writeFile } from "node:fs/promises";
import { build } from "esbuild";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const sourceFile = path.join(__dirname, "ticket-app-source.js");
const prodSourceFile = path.join(__dirname, ".ticket-app-source.prod.js");
const outFile = path.join(__dirname, "..", "internal", "web", "static", "app.js");
const generatedBanner = "/* Generated from web-client/ticket-app-source.js. Edit that source, then run npm run build:ticket-app from web-client/. */";

// Build mode:
//   dev   = include test harness, full size
//   prod  = strip test harness, slim build (default)
const mode = (process.env.TICKET_APP_MODE || "prod").toLowerCase();
const devMode = mode === "dev";
let entryPoint = sourceFile;

if (!devMode) {
  const source = await readFile(sourceFile, "utf8");
  const marker = "\n  // ============================================================\n  // Test harness:";
  const start = source.indexOf(marker);
  const end = source.lastIndexOf("\n})();");
  if (start === -1 || end === -1 || end <= start) {
    throw new Error("Could not locate ticket app test harness block to strip from production build.");
  }
  await writeFile(prodSourceFile, source.slice(0, start) + source.slice(end), "utf8");
  entryPoint = prodSourceFile;
}

try {
  await build({
    entryPoints: [entryPoint],
    bundle: true,
    minifyWhitespace: true,
    define: {
      "process.env.TICKET_APP_DEV": JSON.stringify(devMode),
    },
    banner: {
      js: generatedBanner,
    },
    format: "iife",
    target: "es2020",
    charset: "utf8",
    outfile: outFile,
    sourcemap: false,
    logLevel: "info",
  });
} finally {
  if (!devMode) {
    await rm(prodSourceFile, { force: true });
  }
}
