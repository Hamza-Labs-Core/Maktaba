// The title a video shows when it has no metadata title: its file name
// without the final extension. The API applies the same rule (Go package
// videotitle) wherever it picks the title itself; this is for the places
// the web app falls back to `filename` on its own. The raw file name stays
// visible where the view is about the file (paths, duplicates, file details).

// An extension is a final "." and one or more ASCII letters or digits, after
// a non-empty stem: "a.b.mkv" -> "a.b"; "notes", ".mp4" and "x." stay.
const EXT = /^(.+)\.[A-Za-z0-9]+$/s;

/** The file name without its final extension, or unchanged when it has none. */
export function stripExt(filename: string): string {
  const m = EXT.exec(filename);
  return m ? m[1] : filename;
}

/** The video's title when it has one (not blank), else its file name without
 * the extension, else its id. */
export function videoTitle(v: {
  title?: string | null;
  filename?: string | null;
  id?: string | null;
}): string {
  if (v.title && v.title.trim() !== "") return v.title;
  if (v.filename) return stripExt(v.filename);
  return v.id ?? "";
}
