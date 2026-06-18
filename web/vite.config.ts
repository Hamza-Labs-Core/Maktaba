import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { writeFileSync } from "node:fs";

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

export default defineConfig({
  define: {
    "import.meta.env.VITE_APP_VERSION": JSON.stringify(BUILD_VERSION),
  },
  plugins: [react(), writeVersionJson()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "src"),
      "@ds": path.resolve(__dirname, "design-system"),
    },
  },
  server: {
    port: 5173,
    host: true,
    proxy: {
      // In dev, point /api/* and /ws/* at the local Go API.
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: false,
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
