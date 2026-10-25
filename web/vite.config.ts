import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { readFileSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { serviceWorkerBuild, stampServiceWorker } from "./src/lib/swStamp";

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

export default defineConfig({
  define: {
    "import.meta.env.VITE_APP_VERSION": JSON.stringify(BUILD_VERSION),
  },
  plugins: [react(), writeVersionJson(), stampServiceWorkerPlugin()],
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
});
