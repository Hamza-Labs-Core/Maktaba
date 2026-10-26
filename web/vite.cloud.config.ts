// Maktaba Cloud web UI (web/cloud/): its own entry and bundle, built from the
// same package as the home app so it shares node_modules, the design system,
// eslint, prettier and vitest. `pnpm dev:cloud` serves it on :5174 and proxies
// the cloud API (the api role) at CLOUD_API_URL, default http://localhost:8090
// — not :8080, which is the home server's API on a dev machine running Maktaba.
// `pnpm build:cloud` writes dist-cloud/.
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const cloudAPI = process.env.CLOUD_API_URL || "http://localhost:8090";

export default defineConfig({
  root: path.resolve(__dirname, "cloud"),
  // the home app's public/ (service worker, manifest) is not the cloud's
  publicDir: false,
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "src"),
      "@ds": path.resolve(__dirname, "design-system"),
      "@maktaba/tvplayer": path.resolve(__dirname, "packages/tvplayer/src"),
    },
  },
  server: {
    port: 5174,
    host: true,
    // the design system and the shared token CSS live outside the cloud root
    fs: { allow: [__dirname] },
    proxy: {
      "/v1": { target: cloudAPI, changeOrigin: false },
      "/.well-known": { target: cloudAPI, changeOrigin: false },
    },
  },
  build: {
    target: "es2022",
    outDir: path.resolve(__dirname, "dist-cloud"),
    emptyOutDir: true,
    sourcemap: true,
  },
});
