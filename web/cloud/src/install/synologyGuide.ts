// The Synology guide's DSM screenshots (story 59.09). DSM's own screens can only be taken on a
// DiskStation: docs/nas/checklists/synology.md (steps 18 to 22) lists each one. A screenshot
// is shown once it is taken, saved as web/cloud/public/install/synology/<id>.<en|ar>.png and
// marked ready here; until then the step shows its text alone (synologyGuide.test.ts checks
// that every ready one has both files, and that the checklist names every one).

export interface Screen {
  id: string;
  /** the guide's step (1 to 5) it belongs to, or "after" for After setup */
  step: 1 | 2 | 3 | 4 | 5 | "after";
  ready: boolean;
}

export const SCREENS: Screen[] = [
  { id: "guide-user", step: 1, ready: false },
  { id: "guide-uid", step: 1, ready: false },
  { id: "guide-folder", step: 2, ready: false },
  { id: "guide-share", step: 3, ready: false },
  { id: "guide-project", step: 4, ready: false },
  { id: "guide-firewall", step: "after", ready: false },
];

export const screenSrc = (id: string, locale: string) => `/install/synology/${id}.${locale}.png`;
