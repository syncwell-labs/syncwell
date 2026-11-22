// Build the publishable artifacts: ESM bundles, CommonJS bundles, and a
// minified IIFE for <script> tags. esbuild is a devDependency only; the shipped
// package has no runtime dependencies.
import { build } from "esbuild";
import { mkdir, rm } from "node:fs/promises";

const entries = ["syncwell", "crdt", "sequence", "text", "array", "awareness", "storage", "react", "monaco", "tiptap", "devtools"];
const common = {
  bundle: true,
  sourcemap: true,
  target: ["es2020"],
  logLevel: "info",
  external: ["react"],
};

await rm("dist", { recursive: true, force: true });
await Promise.all([mkdir("dist/esm", { recursive: true }), mkdir("dist/cjs", { recursive: true })]);

await Promise.all([
  ...entries.map((name) =>
    build({
      ...common,
      entryPoints: [`${name}.js`],
      format: "esm",
      outfile: `dist/esm/${name}.js`,
    }),
  ),
  ...entries.map((name) =>
    build({
      ...common,
      entryPoints: [`${name}.js`],
      format: "cjs",
      platform: "neutral",
      outfile: `dist/cjs/${name}.cjs`,
    }),
  ),
  build({
    ...common,
    entryPoints: ["syncwell.js"],
    format: "iife",
    globalName: "SyncwellSDK",
    minify: true,
    sourcemap: true,
    outfile: "dist/syncwell.min.js",
  }),
]);
