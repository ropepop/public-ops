import path from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const sourceFile = path.join(__dirname, "ticket-app-source.js");
const outFile = path.join(__dirname, "..", "internal", "web", "static", "app.js");
const generatedBanner = "/* Generated from web-client/ticket-app-source.js. Edit that source, then run npm run build:ticket-app from web-client/. */";

await build({
  entryPoints: [sourceFile],
  bundle: true,
  minifyWhitespace: true,
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
