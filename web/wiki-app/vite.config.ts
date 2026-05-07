import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// External asset roots (relative to the repo).
const REPO_ROOT = path.resolve(__dirname, '../..');
const MOCKUPS_SRC = path.resolve(REPO_ROOT, 'web/mockups');
const DIAGRAMS_SRC = path.resolve(REPO_ROOT, 'specs/diagrams');

const MIME: Record<string, string> = {
  '.html': 'text/html; charset=utf-8',
  '.htm': 'text/html; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.js': 'application/javascript; charset=utf-8',
  '.mjs': 'application/javascript; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.gif': 'image/gif',
  '.webp': 'image/webp',
  '.ico': 'image/x-icon',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
  '.ttf': 'font/ttf',
  '.drawio': 'application/xml; charset=utf-8',
  '.xml': 'application/xml; charset=utf-8',
  '.txt': 'text/plain; charset=utf-8',
};

function copyDir(src: string, dest: string) {
  if (!fs.existsSync(src)) return;
  fs.mkdirSync(dest, { recursive: true });
  for (const entry of fs.readdirSync(src, { withFileTypes: true })) {
    const s = path.join(src, entry.name);
    const d = path.join(dest, entry.name);
    if (entry.isDirectory()) copyDir(s, d);
    else if (entry.isFile()) fs.copyFileSync(s, d);
  }
}

function externalAssets(): Plugin {
  // Returns: file path if found, 'missing' if the URL clearly belongs to a
  // mounted prefix but the file doesn't exist on disk, or null if the URL
  // isn't ours.
  const resolveExternal = (
    reqUrl: string,
  ): { target: string } | { missing: true } | null => {
    const PREFIXES: Array<[string, string]> = [
      ['/mockups/', MOCKUPS_SRC],
      ['/diagrams/', DIAGRAMS_SRC],
    ];
    for (const [prefix, root] of PREFIXES) {
      if (!reqUrl.startsWith(prefix)) continue;
      const rel = reqUrl.slice(prefix.length).split('?')[0].split('#')[0];
      const decoded = decodeURIComponent(rel);
      if (!decoded || decoded.includes('..')) return { missing: true };
      const target = path.join(root, decoded);
      if (!target.startsWith(root)) return { missing: true };
      if (fs.existsSync(target) && fs.statSync(target).isFile()) {
        return { target };
      }
      return { missing: true };
    }
    return null;
  };

  const handler = (req: any, res: any, next: any) => {
    if (!req.url) return next();
    const result = resolveExternal(req.url);
    if (!result) return next();
    if ('missing' in result) {
      // Explicit 404 — keeps Vite's SPA fallback from serving index.html
      // for missing mockup/diagram files.
      res.statusCode = 404;
      res.setHeader('Content-Type', 'text/plain; charset=utf-8');
      res.end('Not found');
      return;
    }
    const ext = path.extname(result.target).toLowerCase();
    res.setHeader('Content-Type', MIME[ext] || 'application/octet-stream');
    res.setHeader('Cache-Control', 'no-cache');
    fs.createReadStream(result.target).pipe(res);
  };

  return {
    name: 'maktaba-external-assets',
    configureServer(server) {
      server.middlewares.use(handler);
    },
    configurePreviewServer(server) {
      // Mirror dev server behavior for `vite preview`.
      server.middlewares.use(handler);
    },
    closeBundle() {
      const out = path.resolve(__dirname, 'dist');
      if (fs.existsSync(MOCKUPS_SRC)) {
        copyDir(MOCKUPS_SRC, path.join(out, 'mockups'));
      }
      if (fs.existsSync(DIAGRAMS_SRC)) {
        copyDir(DIAGRAMS_SRC, path.join(out, 'diagrams'));
      }
    },
  };
}

export default defineConfig({
  plugins: [react(), externalAssets()],
  base: './',
  resolve: {
    alias: {
      '@': path.resolve(__dirname, 'src'),
    },
  },
  server: {
    port: 5173,
    host: true,
  },
});
