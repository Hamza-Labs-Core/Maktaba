# Story 60.06 — Problem types: one registry the API sends from, one page per type

> Epic 60 · Phase 1 (registry and pages), Phase 3 (onto the full design) · Depends on: 60.03,
> 60.04, 60.02 part A

## Why

Every error the API sends is an RFC 9457 problem whose `type` is a URI under
`https://maktaba.tv/problems/`. People follow them: from the API's own HTML error page, from a
log, from a client that shows the link. Today 257 links to 120 distinct slugs point at nothing.

There is also nothing to generate pages from:
- `api/internal/httperror/types.go` says it is the registry, but holds 18 constants;
- the other types are string literals across the handlers;
- seven places build the slug at runtime from a code.

So no grep can list every type the API sends, and nothing stops a new one shipping without its
page.

## Description

**The registry** (`shared/api/problems.json`), one entry per slug:

```json
{
  "slug": "seat-limit",
  "status": 403,
  "area": "accounts",
  "audience": "admin",
  "codes": ["seat_limit"],
  "title": { "en": "This home has no free seats", "ar": "…" },
  "means": { "en": "…", "ar": "…" },
  "do":    { "en": "…", "ar": "…" },
  "help":  "account#seats",
  "install": null
}
```

- **The fields:**
  - `slug` is the URI's last segment, `[a-z0-9_-]`. The four shipped underscore slugs
    (`smart_playlist`, `automatic_collection`, `stale_queue`, `empty_source`) keep their
    spelling, because clients match it (`PlayQueue.kt:97` reads `empty_source`). New slugs use
    hyphens, and a lint refuses a new underscore slug;
  - `codes` lists the `code` values sent with it (the `With("code", …)` extra), so a client that
    reads codes reaches the same entry;
  - `audience` is one of `anyone`, `person`, `admin`, `app` and `node`;
  - `area` groups the index;
  - `help` names an Epic 41 topic and anchor, and `install` an install troubleshooting anchor,
    when one exists.
- **Text:** in the voice of 60.02's rule for errors: what happened, with no blame; then what you
  can do; then whom to ask.
- **Seeding:** from today's code.
  - The 120 literal slugs.
  - The 18 constants.
  - The codes each of the seven runtime call sites can receive (six functions; `review` has
    two call sites):
    - `cluster.problem`;
    - `components` (line 104);
    - `plugins/admin` (line 145);
    - `review.writeEditErr` (the `transcript.Err*` values and batch errors);
    - `rules.conflict`;
    - `tvremote.writeTuneError` (the `remote.TuneError` codes).
  - The seeding pull request lists how many came from each source. The grep's matches are
    normalised (trailing punctuation dropped), so the 120 literals give 119 or 120 slugs, as
    the character class decides.

**The API sends only registered types** (`api/internal/httperror`):
- `httperror.Type(slug string) string` returns `"https://maktaba.tv/problems/" + slug`.
  `httperror.TypeForCode(code string)` maps a code through `codes`. Both read the registry,
  embedded at build time (`go:embed` from a copy generated into the package, checked equal by a
  test).
- **The check is in `httperror.Write`.** Every problem passes through it, however its `Type` was
  made. `Error.Type` is a public field, and handlers build struct literals
  (`cluster/switchover/keptaside.go:288`), so a check inside `Type()` alone would miss them.
- **An unregistered type:**
  - **in a test binary** (`testing.Testing()`), it is recorded. It is appended to the file named
    by `MAKTABA_UNREGISTERED_PROBLEMS`, which the API's CI job sets and reads after
    `go test ./...`, failing the job when the file is not empty. It is not a panic: the
    recoverer middleware (`middleware/recoverer.go`) turns a panic into a 500 `internal`, and a
    test that checks only for an error status would still pass. With the variable unset (a
    local run), it is logged at error level. The whole suite runs under the rule (#662);
  - **in production,** it logs `problem_type_unregistered` with the slug, **keeps the status**,
    and sends the registered generic type for that status (`TypeConflict`, `TypeForbidden`,
    `TypeNotFound`, … `TypeInternal` for 5xx). A 409 never becomes a 500, and a household never
    sees a dead link.
- **Out of reach:** e2e runs of the real binary are not test binaries. The literal scan below and
  the unit suites cover them.
- **Callers:**
  - the 18 constants become `Type("…")` values;
  - every literal in the handlers becomes a constant or a `Type` call;
  - the seven runtime sites call `TypeForCode(code)`.
- **A test** fails any `https://maktaba.tv/problems/` string literal in a `.go` file outside
  `httperror` and the registry's generated copy.
- **The API's HTML error page** (`writePage`, for a browser that navigated to an error) gains a
  link, "What this means", to the type's page. A page in Arabic links the `/ar/` page.

**Clients and docs:**
- **Android, Apple and the web** each have a test that reads `shared/api/problems.json` and fails
  when a type or code the client compares against is not in it. The tests find them by grepping
  the client's sources for `maktaba.tv/problems/` and its code constants.
- **`shared/api/openapi.yaml`'s problem `type` description** links the registry. A test checks
  every `type` example in the file is registered.
- **The cloud** (`cloud/`) sends no maktaba.tv problem types today. If one appears it joins the
  same registry and the same literal test, which scans `cloud/` too.

**The pages** (`scripts/problems.mjs`):
- **`/problems/<slug>/` and `/ar/problems/<slug>/`** for every entry, in 60.01 part A's problem
  layout:
  - the title;
  - "What this means";
  - "What you can do";
  - who usually sees it (from `audience`: "You'll see this if you run the home" for `admin`);
  - links to the help topic and the install troubleshooting section when given;
  - in small type, the HTTP status, the type URI and the codes, for people writing scripts.
- **`/problems/`:** the index, grouped by `area`, each entry with its title, plus a search box
  that filters in the page (no request).
- **The build fails** when an entry lacks any text in either language, or an Arabic page fails the
  Latin-share check. Slugs and codes are counted as code.
- **The check:** every slug in the registry has both pages in `dist/`, and every
  `/problems/<slug>` link anywhere in the repo names a registered slug (60.08 runs it).

**Phase 3:** the pages and index move onto part B's layout. The data and the generator stay.

## Acceptance criteria

- **Given** the API's test suite, **then** it passes with unregistered types panicking, and no
  `https://maktaba.tv/problems/` literal remains outside the registry.
- **Given** production, **when** code asks for an unregistered slug, **then** the response is
  `internal` and the log has `problem_type_unregistered`.
- **Given** every registry entry, **then** `/problems/<slug>/` and `/ar/problems/<slug>/` exist with
  every section, and pass 60.04's bar (axe on every page, Lighthouse on representatives and on
  deploy).
- **Given** each client's problem types and codes, **then** each is in the registry.
- **Given** a browser that navigates to an API error, **then** the HTML page links the type's page
  in its language.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit | `Write` of an `Error{Type: ".../no-such-thing", Status: 409}` struct literal in a test binary with the variable set | write | the slug appended to the file |
| T02 | Go unit | the same with test mode off (an unexported hook the test sets) | write | status 409, type `conflict`, log line `problem_type_unregistered` |
| T12 | CI | the API job with a fixture handler that sends an unregistered type behind the recoverer | run | the job fails naming the slug (positive control: passes once it is registered) |
| T13 | lint | a new slug with an underscore (fixture) | run | refused; the four shipped ones allowed |
| T03 | Go unit | repo `.go` files | scan for the literal prefix | only in `httperror` and the generated copy |
| T04 | Go unit | the seven runtime sites | each code their source can produce | each maps through `TypeForCode` |
| T05 | Go unit | embedded copy vs `shared/api/problems.json` | compare | equal |
| T06 | Go unit | `writePage` | render an error (en, ar) | link to `https://maktaba.tv/problems/<slug>/` (or `/ar/…`) |
| T07 | Kotlin / Swift / vitest | client sources | grep types and codes | all registered |
| T08 | unit | `openapi.yaml` | read `type` examples | all registered |
| T09 | build | registry | `problems.mjs` | two pages per entry, every section, Latin share under 20% |
| T10 | build | an entry missing its Arabic `do` (fixture) | build | fails naming the entry and field |
| T11 | Playwright | `/problems/` | type in the filter | the list narrows; no network request |

## Strings

The page and index labels (`scripts/problems.mjs`, `T`). The Arabic addresses the reader in the
plural, as 60.02's voice guide does, and names the product «مكتبة». The entries' own texts are the
registry's.

| Key | English | Arabic |
|---|---|---|
| `problem.crumbs` | Messages | الرسائل |
| `problem.means` | What this means | ما معنى هذا |
| `problem.do` | What you can do | ما الذي يمكنكم فعله |
| `problem.whoHelps` | Who can help | من يمكنه المساعدة |
| `problem.who.anyone` | Anyone may see this, signed in or not. | قد تظهر هذه الرسالة لأي شخص، قبل تسجيل الدخول أو بعده. |
| `problem.who.person` | You may see this while using Maktaba. | قد تظهر لكم هذه الرسالة أثناء استخدام مكتبة. |
| `problem.who.admin` | You'll see this if you run the home. | تظهر لكم هذه الرسالة إن كنتم من يدير الجهاز الرئيسي. |
| `problem.who.app` | An app that works with Maktaba gets this, and may show it to you. | تصل هذه الرسالة إلى التطبيقات التي تعمل مع مكتبة، وقد تعرضها عليكم. |
| `problem.who.node` | Your home's machines send this to each other. | تتبادل أجهزة البيت هذه الرسالة فيما بينها. |
| `problem.helps.person` (anyone, person) | If it keeps happening, ask the person who runs your home. If that's you, Troubleshooting goes through the common causes. | وإن تكرّر الأمر، فاسألوا من يدير الجهاز الرئيسي في بيتكم. وإن كنتم أنتم من يديره، فصفحة حل المشكلات تشرح أسبابه الشائعة. |
| `problem.helps.admin` (admin, node) | If it keeps happening, Troubleshooting goes through the common causes. If it doesn't explain it, tell us on GitHub. | وإن تكرّر الأمر، فصفحة حل المشكلات تشرح أسبابه الشائعة. وإن لم تجدوا فيها الجواب، فأخبرونا على GitHub. |
| `problem.helps.app` | If an app shows you this, ask the person who runs your home, or the app's maker. | وإن عرضها عليكم تطبيق، فاسألوا من يدير الجهاز الرئيسي في بيتكم، أو الجهة التي صنعت التطبيق. |
| `problem.related` | Related pages | صفحات ذات صلة |
| `problem.details` | Details for developers | تفاصيل للمطوّرين |
| `problem.details.status` / `.type` / `.code` / `.codes` | Status / Type / Code / Codes | الحالة / النوع / الرمز / الرموز |
| `problem.index.title` | Messages and what they mean | الرسائل ومعانيها |
| `problem.index.lead` | Every message Maktaba can show, grouped by where you meet it. Each page says what happened and what you can do. | كل رسالة قد يعرضها برنامج مكتبة، مجمّعةً بحسب المكان الذي تظهر فيه. وفي صفحة كل رسالة شرح لما حدث، ولما يمكنكم فعله. |
| `problem.index.filter` | Find a message | البحث عن رسالة |
| `problem.index.placeholder` | A word from the message, or its name | كلمة من الرسالة، أو اسمها |
| `problem.index.count` | 1 message / {n} messages | رسالة واحدة / رسالتان / {n} رسائل / {n} رسالة (by Arabic's plural forms) |
| `problem.index.noMatch` | No message matches. Try a shorter word. | لا رسالة تطابق ما كتبتم. جرّبوا كلمة أقصر. |
| `error.page.whatThisMeans` (API page) | What this means | ما معنى هذا |

The index's area headings are `AREAS` in `scripts/problems.mjs`, in both languages.

## Edge cases

- **One code, several slugs** (an old and a new name): the registry allows `codes` on one entry
  only. The old slug stays as an entry whose text points to the new one, so old links still work.
- **A slug retired from the API:** its page stays, saying it is no longer sent and what replaced
  it. Links in old logs and old clients keep working.
- **`review.writeEditErr` with a wrapped error whose text is not a code:** `TypeForCode` does not
  find it. Under test it is recorded and fails the job; in production it is sent as `conflict`
  with its 409.
- **`about:blank`** (sent by some tests and pages): not a maktaba.tv URI, and not in the registry.

## Build plan

| File | Change |
|---|---|
| `shared/api/problems.json` | the registry, seeded |
| `api/internal/httperror/{types.go,registry.go,registry_gen.json,registry_test.go,literal_test.go}` | `Type`, `TypeForCode`, the guards |
| the handlers that send problem types (about 66 files in `api/internal`, and `api/node_bootstrap.go`) | constants or `Type` calls |
| `api/internal/httperror/httperror.go` | "What this means" link on the HTML page |
| `apps/android/**`, `apps/apple/**`, `web/src/**` tests | client checks |
| `shared/api/openapi.yaml` | the description; its test |
| `web/packages/website/scripts/problems.mjs`, its tests | the pages and index |
