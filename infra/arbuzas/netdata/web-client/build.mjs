import { createHash } from "node:crypto";
import {
  existsSync,
  mkdtempSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  renameSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const webClientDir = path.dirname(fileURLToPath(import.meta.url));
const sourceDir = path.join(webClientDir, "src");
const outputDir = path.join(webClientDir, "..", "web", "kitty-gration");
const outputParent = path.dirname(outputDir);
const previousDir = `${outputDir}.previous-${process.pid}`;
mkdirSync(outputParent, { recursive: true });
const temporaryDir = mkdtempSync(path.join(outputParent, ".kitty-dashboard-build-"));

try {
  const result = await build({
    entryPoints: {
      app: path.join(sourceDir, "app.js"),
      "native-mobile": path.join(sourceDir, "native-mobile.js"),
    },
    bundle: true,
    format: "iife",
    target: "es2020",
    outdir: temporaryDir,
    entryNames: "[name].[hash]",
    assetNames: "asset.[hash]",
    minify: true,
    sourcemap: false,
    metafile: true,
    legalComments: "none",
    logLevel: "info",
  });

  const outputFiles = Object.keys(result.metafile.outputs)
    .map((file) => path.basename(file))
    .sort();
  const script = outputFiles.find((file) => file.startsWith("app.") && file.endsWith(".js"));
  const stylesheet = outputFiles.find((file) => file.startsWith("app.") && file.endsWith(".css"));
  const nativeMobileScript = outputFiles.find((file) => file.startsWith("native-mobile.") && file.endsWith(".js"));
  const nativeMobileStylesheet = outputFiles.find((file) => file.startsWith("native-mobile.") && file.endsWith(".css"));
  if (!script || !stylesheet || !nativeMobileScript || !nativeMobileStylesheet) {
    throw new Error("dashboard build did not produce the dashboard and native mobile JavaScript/CSS pairs");
  }

  const buildHash = createHash("sha256");
  for (const file of [script, stylesheet, nativeMobileScript, nativeMobileStylesheet]) {
    buildHash.update(readFileSync(path.join(temporaryDir, file)));
  }
  const buildId = buildHash.digest("hex").slice(0, 16);

  const indexTemplate = readFileSync(path.join(sourceDir, "index.html"), "utf8");
  const index = indexTemplate
    .replaceAll("__APP_SCRIPT__", script)
    .replaceAll("__APP_STYLESHEET__", stylesheet)
    .replaceAll("__BUILD_ID__", buildId);
  writeFileSync(path.join(temporaryDir, "index.html"), index, "utf8");
  writeFileSync(
    path.join(temporaryDir, "build.json"),
    `${JSON.stringify(
      {
        dashboard: "Kitty-gration Operations",
        ui: "arrow",
        api: "netdata-v3",
        buildId,
        assets: [script, stylesheet, nativeMobileScript, nativeMobileStylesheet],
        nativeMobile: {
          script: nativeMobileScript,
          stylesheet: nativeMobileStylesheet,
          viewport: "width=device-width, initial-scale=1, viewport-fit=cover",
        },
      },
      null,
      2,
    )}\n`,
    "utf8",
  );

  rmSync(previousDir, { recursive: true, force: true });
  if (existsSync(outputDir)) renameSync(outputDir, previousDir);
  try {
    renameSync(temporaryDir, outputDir);
  } catch (error) {
    if (!existsSync(outputDir) && existsSync(previousDir)) renameSync(previousDir, outputDir);
    throw error;
  }
  rmSync(previousDir, { recursive: true, force: true });
} catch (error) {
  rmSync(temporaryDir, { recursive: true, force: true });
  if (!existsSync(outputDir) && existsSync(previousDir)) renameSync(previousDir, outputDir);
  throw error;
}

const builtFiles = readdirSync(outputDir).sort();
if (builtFiles.some((file) => file.endsWith(".map"))) {
  throw new Error("source maps must not be shipped with the operations dashboard");
}
console.log(`Built Kitty-gration dashboard: ${builtFiles.join(", ")}`);
