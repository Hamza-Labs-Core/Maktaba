import { defineConfig, loadEnv, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { serviceWorkerBuild, stampServiceWorker } from "./src/lib/swStamp";
import { serverPageModules } from "./src/lib/editionBundle";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// Build stamp (Epic 28, Story 28.1). CI exports VERSION/COMMIT/
// SOURCE_DATE_EPOCH from the git tag for every build path that runs
// `vite build` (Makefile build-web, release.yml, _build-artifacts.yml,
// desktop/mobile release). Defaults keep a bare local build green.
const BUILD_VERSION = process.env.VERSION || "dev";
const BUILD_COMMIT = process.env.COMMIT || "unknown";
const BUILD_DATE = process.env.SOURCE_DATE_EPOCH || "unknown";

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

export default defineConfig(({ mode }) => ({
  define: {
    "import.meta.env.VITE_APP_VERSION": JSON.stringify(BUILD_VERSION),
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
}));
