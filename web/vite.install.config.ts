// maktaba.tv's install pages (Epic 59 stories 59.09 to 59.11 and 59.14, web/packages/install):
// the NAS landing page /install/nas and its model table, /install/docker, /install/synology
// (59.09), the TrueNAS guide /install/truenas (59.10), the Unraid, QNAP and UGREEN/ASUSTOR guides,
// After setup and Troubleshooting (59.14), and their Arabic pages under /ar/. `pnpm build:install`
// writes dist-install/. The published compose file (cloud/installers/docker/docker-compose.yml)
// is built into the page (`?raw`): the pages fetch nothing and send nothing. The build fails
// when an Arabic page's prose is mostly Latin (maktaba.dev's check, src/latin.ts).
import { defineConfig, type Plugin } from "vite";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { latinFindings } from "./packages/install/src/latin";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const site = path.resolve(__dirname, "packages/install/site");

/** Every guide, in English and Arabic: <name> is /install/<name>/, ar-<name> /ar/install/<name>/. */
export const PAGES = [
  "nas",
  "docker",
  "synology",
  "truenas",
  "unraid",
  "qnap",
  "ugreen",
  "after-setup",
  "troubleshooting",
];

const input: Record<string, string> = {};
for (const p of PAGES) {
  input[p] = path.resolve(site, `install/${p}/index.html`);
  input[`ar-${p}`] = path.resolve(site, `ar/install/${p}/index.html`);
}

const outDir = path.resolve(__dirname, "dist-install");

/** The Latin-share check over the built Arabic pages (read once written: Vite's own HTML step
 * emits them after a plugin's generateBundle). */
function arabicPages(): Plugin {
  return {
    name: "maktaba-install-arabic",
    apply: "build",
    closeBundle() {
      const pages: Record<string, string> = {};
      for (const p of PAGES) {
        const rel = `ar/install/${p}/index.html`;
        pages[rel] = fs.readFileSync(path.join(outDir, rel), "utf8");
      }
      const findings = latinFindings(pages);
      if (findings.length) throw new Error(`Arabic pages left in English:\n${findings.join("\n")}`);
    },
  };
}

export default defineConfig({
  root: site,
  // the Synology guide's DSM screenshots (packages/install/public/install/synology, 59.09)
  publicDir: path.resolve(__dirname, "packages/install/public"),
  resolve: { alias: { "/src": path.resolve(__dirname, "packages/install/src") } },
  server: { port: 5176, fs: { allow: [path.resolve(__dirname, "..")] } },
  plugins: [arabicPages()],
  build: {
    target: "es2022",
    outDir,
    emptyOutDir: true,
    rollupOptions: { input },
  },
});
