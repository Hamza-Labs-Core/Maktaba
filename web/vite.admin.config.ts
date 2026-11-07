// Maktaba's operator console (web/admin/, Epic 50 story 50.32): its own entry and bundle, for
// admin.maktaba.tv only, so no admin code ships to a user's browser. It shares the cloud user
// app's pieces (web/cloud/src/shared, the design system) from the same package. `pnpm dev:admin`
// serves it on :5175 and proxies /v1 to the api role's admin listener at CLOUD_ADMIN_API_URL,
// default http://localhost:8091, as Host admin.localhost (server.admin_host in
// cloud/configs/cloud.example.toml). `pnpm build:admin` writes dist-admin/.
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const adminAPI = process.env.CLOUD_ADMIN_API_URL || "http://localhost:8091";

export default defineConfig({
  root: path.resolve(__dirname, "admin"),
  publicDir: false,
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "src"),
      "@ds": path.resolve(__dirname, "design-system"),
      "@cloud-shared": path.resolve(__dirname, "cloud/src/shared"),
    },
  },
  server: {
    port: 5175,
    host: true,
    fs: { allow: [__dirname] },
    proxy: {
      "/v1": { target: adminAPI, changeOrigin: false, headers: { host: "admin.localhost" } },
    },
  },
  build: {
    target: "es2022",
    outDir: path.resolve(__dirname, "dist-admin"),
    emptyOutDir: true,
    sourcemap: true,
  },
});
