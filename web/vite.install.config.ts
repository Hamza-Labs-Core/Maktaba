// maktaba.tv's install pages (Epic 59 stories 59.11, 59.09 and 59.10, web/packages/install):
// /install/docker, /install/synology (59.09), the TrueNAS guide /install/truenas (59.10), and
// their Arabic pages under /ar/. `pnpm build:install` writes dist-install/. The published compose file
// (cloud/installers/docker/docker-compose.yml) is built into the page (`?raw`): the page
// fetches nothing and sends nothing.
import { defineConfig } from "vite";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const site = path.resolve(__dirname, "packages/install/site");

export default defineConfig({
  root: site,
  // the Synology guide's DSM screenshots (packages/install/public/install/synology, 59.09)
  publicDir: path.resolve(__dirname, "packages/install/public"),
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
        synology: path.resolve(site, "install/synology/index.html"),
        "ar-synology": path.resolve(site, "ar/install/synology/index.html"),
        // story 59.10: the TrueNAS guide, static pages
        truenas: path.resolve(site, "install/truenas/index.html"),
        "truenas-ar": path.resolve(site, "ar/install/truenas/index.html"),
      },
    },
  },
});
