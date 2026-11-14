---
schema: 1
kind: pr
pr: 1243
title: "One Plugins page for every plugin; Maktaba's own are store plugins, core is not a plugin"
date: 2026-11-14
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
agent: claude-code
---

## What happened
- #1243 merged as 279388e03. Settings → Plugins had two classes: third-party plugins with a
  card, a page and switches, and Maktaba's own stages as a "Built into Maktaba" list with one
  sentence sending the admin to the Components page. That followed Epic 54 §15.7, where each
  first-party plugin kept its Epic 51 component's switch so desired state, bundles and
  licence gates carried over unchanged.
- The requirement changed on 2026-11-14: a plugin made by Maktaba is a store plugin whose
  publisher is Maktaba; what must always run is core and is not a plugin; every server
  release comes in two variants, and the small one is the existing `core` bundle.
- Built: one list with a source filter; a page per plugin at `/settings/plugins/<id>`; the
  switch on the page for every plugin (it writes the component's desired row, the one place
  on/off is kept); a turn-off dialog naming everything that stops; links to the plugin's
  failures and logs; jobs, job times and memory per process, with a shared process shown
  whole and its other plugins named. `core.pipeline` has no card and no page. Routes that
  make no sense for a plugin that comes with the release answer `409 plugin_with_release`.
- Specified, not built: stories 54.45 to 54.52 (Maktaba's plugins as signed store packages,
  install and remove, updates through the store, what is preinstalled, homes with no
  internet, the Full and Core release variants, CPU per process). Release, packaging and
  update code was not touched.
- An independent read-only review after CI was green found two defects that blocked the
  merge. `GET /api/plugins` had started returning Maktaba's plugins in `plugins`, and the
  parental rule pickers (web and the Apple apps) list every entry as an installed plugin, so
  shipped apps would have offered Thumbnails or Transcription as rule subjects that do
  nothing; the default answer is installed plugins only again, and the page asks with
  `?release=true`. And `trim(leading 'plugin.' from id)` strips a set of characters, not a
  prefix (`plugin.net.foo` became `et.foo`). It also found a locked switch that could not be
  turned off, a turn-off with no dialog when the effect could not be read, a problems count
  that used a different rule from the list it linked to, and an unbounded failed-jobs read.
- CI on the way: a Help topic added to the site made the Help index taller, which failed the
  topic-count tests and eight reference images (re-rendered in the CI image, listed in the
  pull request's body); an integration-tagged test file lacked an import the untagged build
  never compiles; main moved three times under the branch.
- Two unrelated tests failed once and passed on one rerun: `TestReplaceSelf` on Windows
  (rename refused, file in use) and `testTimeUpAtTheStartInArabic` on iPhone
  (`timeUp.askMore` not found in 10 s).

## What we learned
- Adding entries to a list an API already returns is a breaking change for clients that
  treat every entry alike. Before widening an answer, grep every client of the route,
  shipped apps included, and widen behind a parameter.
- Postgres `trim(leading X from s)` takes X as a set of characters. A prefix is cut with
  `substr` behind a `left(...) =` check.
- A file behind a build tag is not built by the untagged `go vet` and `go test`; run the
  tagged build for every tagged file touched.
- A Help topic changes the site: its topic counts, its manifest and the Help index reference
  images, which are rendered in the CI image (`npm run e2e:update`), never on the Mac.
- A review by a reader who did not write the code, given the code and not the conclusions,
  found what 139 green checks did not.

## Keep doing
- One independent review before merging an agent's large change, then the findings back to
  the agent that has the context.
- One rerun for a failure in code the change does not touch; a second failure for the same
  reason is a defect.

## Stop doing
- Merging main into a long-running branch after every change on main; merge once, last.

## Open
- Three plugins are in today's `core` bundle (Thumbnails, Convert to MP4, Duplicates). Each
  either stays in core and stops being a plugin, or leaves core; story 54.50 §2 lists them
  for the owner and decides none.
- Install, remove and separate updates of Maktaba's plugins, and the two release variants,
  are specified only.
- CPU per process is not measured, and disk used by a Maktaba plugin's files is not shown.
- Not seen in a browser: a third-party plugin on the new page, real supervisor memory
  figures, a licence-locked switch, the Mac app.
- `AddRule` still accepts any plugin id string as a parental rule subject.
- The transitive walk of the turn-off effect has no fixture with a second level; today's
  catalog has none.
- `TestReplaceSelf` (Windows) and `testTimeUpAtTheStartInArabic` (iPhone) each failed once.
