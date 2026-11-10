// maktaba.tv's install pages (Epic 59 story 59.11, web/packages/install): /install/docker and
// /ar/install/docker. `pnpm build:install` writes dist-install/. The published compose file
// (cloud/installers/docker/docker-compose.yml) is built into the page (`?raw`): the page
// fetches nothing and sends nothing.
import { defineConfig } from "vite";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const site = path.resolve(__dirname, "packages/install/site");

export default defineConfig({
  root: site,
  publicDir: false,
  resolve: { alias: { "/src": path.resolve(__dirname, "packages/install/src") } },
  server: { port: 5176, fs: { allow: [path.resolve(__dirname, "..")] } },
  build: {
    target: "es2022",
    outDir: path.resolve(__dirname, "dist-install"),
    emptyOutDir: true,
    rollupOptions: {
      input: {
        en: path.resolve(site, "install/docker/index.html"),
        ar: path.resolve(site, "ar/install/docker/index.html"),
      },
    },
  },
});
