// maktaba.tv's icons and web manifest (story 60.04), from the app icons in web/public/icons/
// (the gold arch over an open book): favicon.ico (the 32 px PNG in an ICO container),
// apple-touch-icon.png at the root, and the 192, 512 and maskable icons the manifest names.

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

/** An ICO file holding one PNG image (Windows Vista's format, which every browser reads). */
export function icoOf(png) {
  if (png.readUInt32BE(0) !== 0x89504e47) throw new Error("icoOf: not a PNG");
  const width = png.readUInt32BE(16);
  const height = png.readUInt32BE(20);
  if (width > 256 || height > 256) throw new Error("icoOf: an ICO image is at most 256 px");
  const head = Buffer.alloc(6 + 16);
  head.writeUInt16LE(0, 0); // reserved
  head.writeUInt16LE(1, 2); // an icon
  head.writeUInt16LE(1, 4); // one image
  head.writeUInt8(width % 256, 6);
  head.writeUInt8(height % 256, 7);
  head.writeUInt8(0, 8); // no palette
  head.writeUInt8(0, 9);
  head.writeUInt16LE(1, 10); // planes
  head.writeUInt16LE(32, 12); // bits per pixel
  head.writeUInt32LE(png.length, 14);
  head.writeUInt32LE(head.length, 18);
  return Buffer.concat([head, png]);
}

/** The shell's dark page colour (design/site-tokens.css, the `color.semantic.bg` token). */
export function pageColour(
  css = fs.readFileSync(
    path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../design/site-tokens.css"),
    "utf8"
  )
) {
  const m = css.match(/--color-semantic-bg:\s*(#[0-9A-Fa-f]{6})/);
  if (!m) throw new Error("design/site-tokens.css: no --color-semantic-bg");
  return m[1];
}

/** The site's web manifest, in the shell's dark page colour. */
export function manifest() {
  return {
    name: "Maktaba",
    short_name: "Maktaba",
    lang: "en",
    dir: "ltr",
    start_url: "/",
    scope: "/",
    display: "browser",
    background_color: pageColour(),
    theme_color: pageColour(),
    icons: [
      { src: "/icons/icon-192.png", sizes: "192x192", type: "image/png", purpose: "any" },
      { src: "/icons/icon-512.png", sizes: "512x512", type: "image/png", purpose: "any" },
      {
        src: "/icons/maskable-512.png",
        sizes: "512x512",
        type: "image/png",
        purpose: "maskable",
      },
    ],
  };
}

/** Writes the icons and the manifest into dist/ through `write(rel, bytes)`. */
export function writeIcons(repo, write) {
  const dir = path.join(repo, "web/public/icons");
  const read = (f) => fs.readFileSync(path.join(dir, f));
  write("favicon.ico", icoOf(read("favicon-32.png")));
  write("apple-touch-icon.png", read("apple-touch-icon.png"));
  for (const f of ["icon-192.png", "icon-512.png", "maskable-512.png"])
    write(`icons/${f}`, read(f));
  write("manifest.webmanifest", JSON.stringify(manifest(), null, 2) + "\n");
}
