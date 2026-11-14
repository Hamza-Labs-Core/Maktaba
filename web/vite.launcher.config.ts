// The Samsung and LG launcher (Epic 58 story 58.09, apps/tv/web): `pnpm build:launcher`
// writes dist-launcher/, what 58.10 packages as a .wgt and 58.11 as an .ipk. Like /tv/, it
// runs on Chromium 68 (a 2020 TV): ES2017, its CSS lowered by tv-build/tvcss.mjs, and
// tv-build/tvcheck.mjs fails the build on what M68 cannot run. It is one classic script
// (an IIFE, loaded with defer), not a module: a packaged app's page is a file:// page, where
// a module script is a cross-origin fetch a TV runtime may refuse.
//
// `pnpm build:launcher:bundled` is the --bundle-ui build (README §11 decision 3, owner
// question Q3): the same launcher with a copy of /tv/ in dist-launcher/ui/ (`vite build
// --mode tv-bundled`), which it loads from the package and points at the home's API with
// bearer tokens. The home-served UI is the default.
import { defineConfig, type Plugin } from "vite";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { lowerForM68 } from "./tv-build/tvcss.mjs";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(__dirname, "../apps/tv/web");
const VERSION = process.env.VERSION || "dev";
const TARGET = ["es2017", "chrome68"];

function m68Css(): Plugin {
  return {
    name: "maktaba-launcher-css",
    enforce: "pre",
    transform(code, id) {
      if (!/\.css(\?|$)/.test(id)) return null;
      return { code: lowerForM68(code), map: null };
    },
  };
}

/** The page loads the bundle as a classic deferred script (see the header). */
function classicScript(): Plugin {
  return {
    name: "maktaba-launcher-classic-script",
    enforce: "post",
    transformIndexHtml(html) {
      return html
        .replace(/<script type="module" crossorigin src=/g, "<script defer src=")
        .replace(/<link rel="stylesheet" crossorigin /g, '<link rel="stylesheet" ');
    },
  };
}

export default defineConfig(({ mode }) => ({
  root,
  // the package opens index.html from file://: every asset path is relative
  base: "./",
  publicDir: false,
  define: {
    __LAUNCHER_VERSION__: JSON.stringify(VERSION),
    __BUNDLE_UI__: JSON.stringify(mode === "bundle-ui"),
  },
  plugins: [
    m68Css(), classicScript()],
  resolve: {
    // the launcher's source lives outside web/: its packages come from web/node_modules
    alias: [{ find: /^@fontsource\//, replacement: path.resolve(__dirname, "node_modules/@fontsource") + "/" }],
  },
  css: {
    transformer: "lightningcss",
    lightningcss: { targets: { chrome: 68 << 16 } },
  },
  esbuild: { target: TARGET, supported: { "top-level-await": false } },
  server: { fs: { allow: [root, path.resolve(__dirname, "node_modules")] } },
  build: {
    target: TARGET,
    cssTarget: "chrome68",
    cssMinify: "lightningcss",
    outDir: path.resolve(__dirname, "dist-launcher"),
    emptyOutDir: true,
    sourcemap: true,
    modulePreload: false,
    cssCodeSplit: false,
    rollupOptions: { output: { format: "iife", inlineDynamicImports: true } },
  },
}));
