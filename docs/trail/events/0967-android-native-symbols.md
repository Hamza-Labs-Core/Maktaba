---
schema: 1
kind: pr
pr: 967
title: 53.16: Android native crash frames symbolicated
date: 2026-11-08
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [952, 961]
---

## What happened
- A native symbol table kind (`cloud/internal/symbols/elf.go`) is built in Go from an unstripped `.so`: DWARF function ranges plus the symbol table's functions DWARF misses, and the line table. An inlined helper shows the outer function's name with the helper's own lines. Tables are keyed by the first 16 bytes of the GNU build id and keep the whole id; a frame is read only when the whole id matches.
- A stripped library makes no table: the device's unwinder already names those functions, and a `.dynsym`-only table would mark the crash read with native symbols and hide that the real symbols are missing.
- At read time, frames of the app's own libraries are matched by library and build id; system libraries stay as they came and are never listed missing. A reason-only crash (Android 11, or a tombstone that does not parse) no longer asks for an R8 mapping.
- The Play bundles workflow builds tables from the libraries before AGP strips them and uploads them with the same token, no-secret line and warn-on-failure; the tables are kept 90 days as the `native-symbols` artifact on failure.
- T07 also sends an Android TV native crash and sees it symbolicated.

## What we learned
- The Android apps build no native code of their own (no NDK or CMake; playback is Media3/ExoPlayer, not libmpv). The only `.so` files are prebuilt AndroidX ones that Google strips. Tables will appear only once an app builds native code.
- Tombstone caller pcs are already adjusted by libunwindstack, so they are looked up as they come; Apple return addresses are moved back a byte first.
- The home's crash scrub keeps `code_id`, `instruction_addr` and `addr_mode` unchanged.
- ELF test fixtures build on this Mac with `docker run --platform linux/arm64 alpine:3.20` (gcc).
- In `cloud-rehearsal`, the 4 s aggregate verdict proves nothing alone: read the run's jobs to see that a scenario actually ran.

## Open
- The workflow reads unstripped libraries from `build/intermediates/merged_native_libs/release`, unconfirmed on AGP 9.4 (no JDK here); the first tag run shows it (a "no native libraries" notice would mean the path is wrong).
- The Crash symbols page has no column for the table kind: a native table's build id shows under the UUID header.
