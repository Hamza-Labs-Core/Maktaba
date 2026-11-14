import { defineConfig, loadEnv, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { serviceWorkerBuild, stampServiceWorker } from "./src/lib/swStamp";
import { serverPageModules } from "./src/lib/editionBundle";
import legacy from "@vitejs/plugin-legacy";
import { lowerForM68 } from "./tv-build/tvcss.mjs";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// Build stamp (Epic 28, Story 28.1). CI exports VERSION/COMMIT/
// SOURCE_DATE_EPOCH from the git tag for every build path that runs
// `vite build` (Makefile build-web, release.yml, _build-artifacts.yml,
// desktop/mobile release). Defaults keep a bare local build green.
const BUILD_VERSION = process.env.VERSION || "dev";
const BUILD_COMMIT = process.env.COMMIT || "unknown";
const BUILD_DATE = process.env.SOURCE_DATE_EPOCH || "unknown";

// Maktaba's Cast receiver application ID (Epic 58 story 58.06): shared/cast/app.json, which
// every sender reads; MAKTABA_CAST_APP_ID overrides it for a build. It is public, never a
// secret. Empty (no receiver registered yet): the player shows no Cast button.
function castAppId(): string {
  if (process.env.MAKTABA_CAST_APP_ID !== undefined) return process.env.MAKTABA_CAST_APP_ID.trim();
  const file = path.resolve(__dirname, "../shared/cast/app.json");
  if (!existsSync(file)) return "";
  const id = (JSON.parse(readFileSync(file, "utf8")) as { receiver_app_id?: unknown })
    .receiver_app_id;
  return typeof id === "string" ? id.trim() : "";
}
const CAST_APP_ID = castAppId();

// writeVersionJson emits dist/version.json after the bundle is written so
// the web client (and the mobile update check) can read the build it was
// shipped as without an API round-trip.
function writeVersionJson(): Plugin {
  return {
    name: "maktaba-write-version-json",
    apply: "build",
    closeBundle() {
      // a failed build (the player check, say) wrote nothing to stamp
      if (!existsSync(path.resolve(__dirname, "dist", "index.html"))) return;
      const out = path.resolve(__dirname, "dist", "version.json");
      writeFileSync(
        out,
        JSON.stringify(
          { version: BUILD_VERSION, commit: BUILD_COMMIT, build_date: BUILD_DATE },
          null,
          2
        ) + "\n"
      );
    },
  };
}

// stampServiceWorker gives dist/sw.js this build's stamp (src/lib/swStamp.ts): a new
// release is a new worker, so the browser installs it and offers the reload.
function stampServiceWorkerPlugin(): Plugin {
  return {
    name: "maktaba-stamp-sw",
    apply: "build",
    closeBundle() {
      const dist = path.resolve(__dirname, "dist");
      // a failed build (the player check, say) wrote nothing to stamp
      if (!existsSync(path.join(dist, "index.html"))) return;
      const shell = readFileSync(path.join(dist, "index.html"));
      const build = serviceWorkerBuild(
        BUILD_VERSION,
        createHash("sha256").update(shell).digest("hex")
      );
      const sw = path.join(dist, "sw.js");
      writeFileSync(sw, stampServiceWorker(readFileSync(sw, "utf8"), build));
    },
  };
}

// The edition (Epic 42.5, src/lib/edition.ts): full (the default the server ships),
// player or remote. An unknown value fails the build rather than shipping as full.
function editionOf(mode: string): string {
  const edition = loadEnv(mode, __dirname, "VITE_").VITE_MAKTABA_EDITION || "full";
  if (!["full", "player", "remote"].includes(edition)) {
    throw new Error(`VITE_MAKTABA_EDITION must be full, player or remote, not "${edition}"`);
  }
  return edition;
}

// A player bundle carries no server page code (processing, transcription, settings,
// the admin pages): the build fails naming any such module that made it in.
function playerBundleCheck(): Plugin {
  return {
    name: "maktaba-player-bundle-check",
    apply: "build",
    generateBundle(_, bundle) {
      const found = serverPageModules(
        Object.values(bundle).flatMap((out) =>
          out.type === "chunk"
            ? Object.entries(out.modules).map(([id, m]) => ({ id, size: m.renderedLength }))
            : []
        )
      );
      if (found.length > 0) {
        this.error(`the player bundle has server page code:\n  ${found.join("\n  ")}`);
      }
    },
  };
}

// The TV build (Epic 58 story 58.08, `pnpm build:tv`): the big screen alone, at
// /tv/ on the home, for Chromium 68 (a 2020 Samsung or LG TV). Its scripts are
// lowered to ES2017 with core-js polyfills for what the code uses (plugin-legacy's
// modern polyfills; no legacy SystemJS bundle); its CSS goes through lowerForM68
// then Lightning CSS for Chrome 68 (tv-build/tvcss.mjs). tv-build/tvcheck.mjs
// fails the build when the output would not run there.
const TV_TARGET = ["es2017", "chrome68"];

function tvCss(): Plugin {
  return {
    name: "maktaba-tv-css",
    enforce: "pre",
    transform(code, id) {
      if (!/\.css(\?|$)/.test(id)) return null;
      return { code: lowerForM68(code), map: null };
    },
  };
}

// index.tv.html is the TV build's page: written as dist/tv/index.html
function tvPage(): Plugin {
  return {
    name: "maktaba-tv-page",
    apply: "build",
    // after Vite's own HTML plugin has written the page
    enforce: "post",
    generateBundle(_, bundle) {
      const page = bundle["index.tv.html"];
      if (page) {
        page.fileName = "index.html";
        delete bundle["index.tv.html"];
        bundle["index.html"] = page;
      }
    },
  };
}

function tvConfig() {
  return defineConfig({
    base: "/tv/",
    publicDir: false,
    define: {
      "import.meta.env.VITE_APP_VERSION": JSON.stringify(BUILD_VERSION),
    },
    plugins: [
      tvCss(),
      react(),
      legacy({
        renderLegacyChunks: false,
        modernPolyfills: true,
        modernTargets: "chrome >= 68",
      }),
      tvPage(),
    ],
    resolve: {
      alias: {
        "@": path.resolve(__dirname, "src"),
        "@ds": path.resolve(__dirname, "design-system"),
        "@maktaba/tvplayer": path.resolve(__dirname, "packages/tvplayer/src"),
        "@maktaba/flowchart": path.resolve(__dirname, "../tools/plugin-sdk/js/src/flowchart.ts"),
      },
    },
    css: {
      transformer: "lightningcss",
      lightningcss: { targets: { chrome: 68 << 16 } },
    },
    esbuild: { target: TV_TARGET, supported: { "top-level-await": false } },
    build: {
      target: TV_TARGET,
      cssTarget: "chrome68",
      cssMinify: "lightningcss",
      outDir: "dist/tv",
      emptyOutDir: true,
      sourcemap: true,
      rollupOptions: { input: path.resolve(__dirname, "index.tv.html") },
    },
  });
}

// The --bundle-ui copy of /tv/ (Epic 58 story 58.09, `pnpm build:launcher:bundled`): the same
// TV build, packaged with the Samsung and LG launcher in dist-launcher/ui/ and opened from
// file://. Its paths are relative, and it is one classic deferred script (an IIFE, the lazy
// screens inlined): a file:// page's origin is "null", and a module script there is a
// cross-origin fetch the browser refuses. The launcher passes ?home=<address>; the page
// reaches the home's API there with bearer tokens (src/tv/TvApp.tsx).
function tvBundledConfig() {
  const base = tvConfig();
  return defineConfig({
    ...base,
    base: "./",
    plugins: [
      tvCss(),
      react(),
      legacy({
        renderLegacyChunks: false,
        modernPolyfills: true,
        modernTargets: "chrome >= 68",
      }),
      tvPage(),
      {
        name: "maktaba-tv-bundled-classic",
        enforce: "post",
        transformIndexHtml(html: string) {
          return html
            .replace(/<script type="module" crossorigin src=/g, "<script defer src=")
            .replace(/<link rel="stylesheet" crossorigin /g, '<link rel="stylesheet" ');
        },
      },
    ],
    build: {
      ...base.build,
      outDir: "dist-launcher/ui",
      modulePreload: false,
      cssCodeSplit: false,
      rollupOptions: {
        input: path.resolve(__dirname, "index.tv.html"),
        output: { format: "iife", inlineDynamicImports: true },
      },
    },
  });
}

export default defineConfig(({ mode }) =>
  mode === "tv"
    ? tvConfig()
    : mode === "tv-bundled"
      ? tvBundledConfig()
      : {
          define: {
            "import.meta.env.VITE_APP_VERSION": JSON.stringify(BUILD_VERSION),
            "import.meta.env.VITE_CAST_APP_ID": JSON.stringify(CAST_APP_ID),
          },
          plugins: [
            react(),
            writeVersionJson(),
            stampServiceWorkerPlugin(),
            ...(editionOf(mode) === "player" ? [playerBundleCheck()] : []),
          ],
          resolve: {
            alias: {
              "@": path.resolve(__dirname, "src"),
              "@ds": path.resolve(__dirname, "design-system"),
              // Epic 34.6: the shared TV player (channel switching), used by the live
              // player here and later the public TV site
              "@maktaba/tvplayer": path.resolve(__dirname, "packages/tvplayer/src"),
              // Epic 54 (story 54.41): the plugin SDK's flow chart, one layout for every place
              // that draws a flow; it imports nothing
              "@maktaba/flowchart": path.resolve(
                __dirname,
                "../tools/plugin-sdk/js/src/flowchart.ts"
              ),
            },
          },
          server: {
            port: 5173,
            host: true,
            // reachable from phones and other machines while developing: Bonjour names on
            // the LAN and Tailscale (MagicDNS) names; Vite refuses unknown hosts otherwise
            allowedHosts: [".local", ".ts.net"],
            proxy: {
              // In dev, point /api/* and /ws/* at the local Go API, and the live TV
              // paths (/stream, /xmltv.xml, /lineup.m3u) at the streaming service.
              "/stream": { target: "http://localhost:8081", changeOrigin: false },
              "/xmltv.xml": { target: "http://localhost:8081", changeOrigin: false },
              "/lineup.m3u": { target: "http://localhost:8081", changeOrigin: false },
              "/api": {
                target: "http://localhost:8080",
                changeOrigin: false,
                // the cluster hub's browser socket (/api/cluster/ws) upgrades here
                ws: true,
              },
              "/ws": {
                target: "ws://localhost:8080",
                ws: true,
              },
            },
          },
          build: {
            target: "es2022",
            outDir: "dist",
            sourcemap: true,
          },
        }
);
