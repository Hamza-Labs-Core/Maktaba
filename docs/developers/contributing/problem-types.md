# Problem types: the API's errors and their registry

Every error the Maktaba API sends is an RFC 9457 problem (`application/problem+json`). Its
`type` is a URI under `https://maktaba.tv/problems/`, and its last part, the slug, names what
went wrong: `https://maktaba.tv/problems/seat-limit`. Each slug is an entry of one registry,
[`shared/api/problems.json`](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/shared/api/problems.json),
and maktaba.tv publishes a page for each entry, in English and Arabic, saying what happened and
what a person can do. Each entry gets its page at `https://maktaba.tv/problems/<slug>/` and
`https://maktaba.tv/ar/problems/<slug>/`, built from the registry with the site (a new entry needs
no page of its own), and [the index](https://maktaba.tv/problems/) lists them all by `area`.

## An entry

```json
{
  "slug": "seat-limit",
  "status": 403,
  "area": "licensing",
  "audience": "admin",
  "codes": [],
  "title": { "en": "This home has no free seats", "ar": "لا مقاعد شاغرة في هذا الجهاز الرئيسي" },
  "means": { "en": "…", "ar": "…" },
  "do":    { "en": "…", "ar": "…" },
  "help": "users#seat-limit",
  "install": null
}
```

- **`slug`:** lower case, digits and hyphens. Four slugs that shipped with underscores keep them
  (`smart_playlist`, `automatic_collection`, `stale_queue`, `empty_source`) because clients
  match them; a test refuses any other underscore.
- **`status`:** the HTTP status it is sent with.
- **`area`:** the group it is listed under on the index page (`accounts`, `plugins`, `machines` …).
- **`audience`:** who meets it: `anyone`, `person` (a signed-in member of the household),
  `admin`, `app` (only an app or a script), or `node` (one machine of a home talking to another).
- **`codes`:** the `code` values sent with it (the problem's `code` field). A code belongs to one
  entry only.
- **`title`, `means`, `do`:** in English and Arabic. What happened, in one sentence and with no
  blame; then what you can do, then whom to ask (the home's admin, or Maktaba). Write for a
  household, not for a developer. Write the Arabic from the meaning, in Modern Standard Arabic,
  with the app catalog's words («الجهاز الرئيسي» for the home, «مكتبة» for Maktaba). Put what a
  person must type (a file name, a setting, a request) in backquotes.
- **`help`:** a user guide topic and anchor (`users#seat-limit`) when one covers it; a test checks
  the anchor exists in both languages. **`install`:** an install guide's troubleshooting anchor,
  or `null`.

## Sending a problem

In the API, build every type with the `httperror` package
(`api/internal/httperror`):

```go
// a generic type
httperror.Write(w, r, httperror.Conflict("", "the name is taken"))
// a type of its own
httperror.Write(w, r, httperror.Conflict(httperror.Type("collection-name-exists"), "the name is taken"))
// a type from a code
httperror.Write(w, r, (&httperror.Error{Type: httperror.TypeForCode(code), Status: status}).With("code", code))
```

- `httperror.Type(slug)` returns the slug's URI. `httperror.TypeForCode(code)` returns the type
  of the entry whose `codes` list the code.
- Never write the URI as a string: a test fails any `https://maktaba.tv/problems/` literal in a
  Go file outside `httperror`, in `api/` and `cloud/`.
- **`httperror.Write` checks every type**, however it was made, a struct literal included:
  - in a test binary, an unregistered type is appended to the file named by
    `MAKTABA_UNREGISTERED_PROBLEMS` (and logged when it is unset);
  - in production, it is logged as `problem_type_unregistered`, and the answer keeps its
    status with the generic type for it (`conflict` for a 409, `internal` for a 5xx). A
    household never sees a dead link.
  - `about:blank` types are not maktaba.tv types, and pass.
- A browser that opens an API error gets a page with a **What this means** link to the type's
  page, in Arabic for a browser whose first language is Arabic.

## Adding a type

1. Add the entry to `shared/api/problems.json`, in slug order, with every text in both languages.
2. Refresh the API's embedded copy: `cd api && go generate ./internal/httperror`.
3. Send it with `httperror.Type("your-slug")` (or list its code and use `TypeForCode`).
4. Run `go test ./internal/httperror` in `api/`: it checks the copy is current, the entry is
   whole and its Arabic is Arabic, and the help anchor exists.

A slug retired from the API keeps its entry, its text saying it is no longer sent and what
replaced it, so links in old logs and old apps still work.

## The checks

| Check | Where | Fails when |
|---|---|---|
| The registry is whole | `api/internal/httperror/registry_test.go` | a text is missing in either language, a slug or code is listed twice, a new slug has an underscore, a help anchor is missing |
| The copy is current | the same | `registry_gen.json` differs from `shared/api/problems.json` |
| No literal types | `api/internal/httperror/literal_test.go` | a Go file outside `httperror` writes the URI |
| Runtime codes | `registry_test.go` | a code one of the seven call sites can receive maps to no entry |
| Sent types | the unit job (`tools/ci/unregistered-problems.sh`) | any test sent an unregistered type; a positive control proves the check fails and names the slug |
| The OpenAPI description | `registry_test.go` | a problem `type` example in `shared/api/openapi.yaml` is unregistered |
| Android, Apple, web | `ProblemRegistryTest.kt`, `ProblemRegistryTests.swift`, `web/src/lib/problemRegistry.test.ts` | an app compares a slug or a problem `code` the registry lacks |

The app checks read each app's sources for `maktaba.tv/problems/` URIs, comparisons with a
`slug`, and comparisons with `code` in the error classifier. A code an app compares that is no
API problem's (Maktaba Cloud's relay names its reason in its own `error` field) is listed in the
test with why, and the test fails when that list holds a code no longer compared.
