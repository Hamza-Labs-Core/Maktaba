import { resolve } from "node:path";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// The developer console, /link and the account pages (index.html), and the review and
// store-admin pages (admin.html: their own bundle, which store-web sends only after the role
// check), served by store-web as static assets.
export default defineConfig({
  root: __dirname,
  plugins: [react()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
    rollupOptions: { input: { index: resolve(__dirname, "index.html"), admin: resolve(__dirname, "admin.html") } },
  },
});
