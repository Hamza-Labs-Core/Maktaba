---
schema: 1
kind: pr
pr: 959
title: The native Remotes restore a backup and download diagnostics
date: 2026-11-08
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [918]
---

## What happened
- No server change was needed: the supervisor's console already serves `GET /recovery/api/backups`, the `restore` action (the home insists on 2 confirmations) and `GET /recovery/api/diagnostics` (a `.tar.gz`). Both Remotes use them, in English and Arabic.
- Restore lists the backups with when each was taken and its size, asks twice with the web page's wording (the web has no typed confirmation, so neither do the apps), and pins the outcome below the list like the other actions.
- Diagnostics: on iPhone the bundle goes to a temporary file of its own handed to the share sheet, deleted when the sheet closes, and anything a crash left is cleared when the screen opens. On Android the system's create-document picker takes the bytes straight into the file chosen, so the app keeps no copy.
- Both use the recovery call path: a refused pass is dropped, a refusal never signs the app out, and the 10 s poll also stops if a refusal happened while it waited, so no extra try counts toward the 5-a-minute lockout.
- A Go role test shows the backups list and the bundle are admin-only with a bearer token and refused tries count toward the lockout.

## What we learned
- On this Mac the iOS 27.1 runtime has no iPhone device types: make throwaway simulators on iOS 27.0.
- Android screenshots can be recorded locally with Homebrew's `openjdk@17` (`JAVA_HOME` set) and `-Proborazzi.test.record=true` on just the affected tests.
- Robolectric drives a file picker with `LocalActivityResultRegistryOwner` and a fake `ActivityResultRegistry`; `registerOutputStreamSupplier` captures what the app writes.
- `merge-main.sh` stops on `Localizable.xcstrings` conflicts: merge them as JSON keeping main's key order (case-insensitive); Python's `sorted()` reorders the whole file.
- macOS bash is 3.2 (no `mapfile`): build `-only-testing` arguments as an array, since word splitting breaks names with spaces.
- With every merge to main conflicting on the CHANGELOGs, a loop of merge-main, gate, push, watch saved many manual rounds.
