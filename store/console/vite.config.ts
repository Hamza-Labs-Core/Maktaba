import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// The developer console, /link and the account pages, served by store-web as static assets.
export default defineConfig({ root: __dirname, plugins: [react()], build: { outDir: "dist", emptyOutDir: true } });
