import path from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
for (const [source, output] of [
  ["welcome-source.js", "pwa/welcome.js"],
  ["page-activity-source.js", "static/page-activity.js"],
  ["ticket-notifications-sw.js", "pwa/ticket-notifications-sw.js"],
  ["notifications-source.js", "static/notifications.js"],
  ["ticket-app-source.js", "static/app.js"],
  ["hdr-diagnostic-source.js", "diagnostic/hdr-diagnostic.js"],
  ["admin-schedule-source.js", "static/admin-schedule.js"],
  ["admin-vivi-auth-source.js", "static/admin-vivi-auth.js"],
  ["admin-statistics-source.js", "static/admin-statistics.js"],
  ["admin-invitations-source.js", "static/admin-invitations.js"],
]) {
  await build({
    entryPoints: [path.join(__dirname, source)],
    bundle: true,
    minify: true,
    keepNames: true,
    banner: { js: `/* Generated from web-client/${source}. Edit that source, then run npm run build:ticket-app from web-client/. */` },
    format: "iife",
    target: "es2020",
    charset: "utf8",
    outfile: path.join(__dirname, "..", "internal", "web", output),
    sourcemap: false,
    logLevel: "info",
  });
}
