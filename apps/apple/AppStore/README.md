# App Store Connect drafts

The App Store text of each Apple app, in English (`en-US`) and Arabic (`ar-SA`), laid out as
fastlane `deliver` reads it (`<app>/metadata/<locale>/name.txt`, …). These are **drafts**:
nothing here is uploaded by any workflow, and each text is read and approved before it is
pasted (or uploaded) into App Store Connect. The audit they come from is
[`docs/apple-app-review.md`](../../../docs/apple-app-review.md).

| Folder | Scheme | App Store Connect record |
|---|---|---|
| `player-iphone/` | Maktaba Player | Maktaba Player |
| `player-ipad/` | Maktaba Player iPad | Maktaba Player for iPad |
| `player-mac/` | Maktaba Player Mac | Maktaba Player for Mac |
| `remote-iphone/` | Maktaba Remote | Maktaba Remote |
| `remote-ipad/` | Maktaba Remote iPad | Maktaba Remote for iPad (its record says "Maktaba Remote iPad": Apple's marketing guidelines want "for iPad") |
| `tv/` | Maktaba TV | Maktaba TV (Apple TV) |

- **No secret is written here.** The review notes name the release secrets in double braces
  (`{{REVIEW_HOME_VIEWER_PASSWORD}}`); whoever submits fills each in from the secrets at
  submission time. There is no `demo_password.txt` on purpose.
  `tools/ci/review_credentials.py` scans this folder on every pull request.
- **The Remote's notes need an admin account** on the review home
  (`{{REVIEW_HOME_ADMIN_USERNAME}}`, `{{REVIEW_HOME_ADMIN_PASSWORD}}`), which does not exist
  yet: see the audit's owner actions.
- `tools/ci/tests/test_apple_app_review.py` checks every field against App Store Connect's
  limits (30 characters for a name, 100 bytes of keywords: an Arabic letter is 2 bytes).
- The contact fields of App Review Information (name, email, phone) are left out: the owner
  fills them in App Store Connect.
- Screenshots are not here: `.github/workflows/store-gallery.yml` (#1566) makes them as an
  artifact in deliver's layout (`apple/`), to sit beside these folders' `metadata/` at upload.
