// Baut die Editor-Abhängigkeiten zu einem Bundle und legt die statischen
// Dateien daneben. Eingebettet wird nur dist/ — node_modules bleibt draussen.
import * as esbuild from "esbuild";
import { cp, mkdir, rm } from "node:fs/promises";

await rm("dist", { recursive: true, force: true });
await mkdir("dist", { recursive: true });

await esbuild.build({
  entryPoints: ["src/editor.js"],
  bundle: true,
  minify: true,
  format: "iife",
  globalName: "SoapEditor",
  target: ["safari16"],
  outfile: "dist/editor.js",
  logLevel: "info",
});

for (const f of ["index.html", "style.css", "app.js"]) {
  await cp(`src/${f}`, `dist/${f}`);
}
