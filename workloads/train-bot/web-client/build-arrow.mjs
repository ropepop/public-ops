import path from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

await build({
  entryPoints: [path.join(__dirname, "src", "arrow-core.js")],
  bundle: true,
  format: "iife",
  globalName: "ArrowJS",
  footer: {
    js: "globalThis.ArrowJS = ArrowJS;"
  },
  target: "es2020",
  outfile: path.join(__dirname, "..", "internal", "web", "static", "arrow-core.js"),
  minify: true,
  sourcemap: false,
  logLevel: "info"
});
