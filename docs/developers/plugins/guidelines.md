# Review guidelines

> **Status: on `main` (Phase 2, story 54.16).** The automated checks and the review queue are
> built; the store is not open for submissions yet. This is the checklist reviewers use.

Every version is checked automatically. A person reviews every **first** version of a plugin,
any version that adds a capability, a panel or a `plugin-data.read`, any version the checks
flag, any version of a plugin with an open malware or data-misuse report, and a random 5 % of
the rest. Reviewers see your manifest, your reasons for each capability, the check report, the
call trace, the outputs on the sample set, the diff against your last version and your source.

## The checklist

1. **Ask only for what you use.** Each capability you declare should be imported by your
   component (declared but unused is flagged). For every medium- or high-risk capability, say
   why in your release notes: name the capability and what you do with it.
2. **Do what the listing says.** The name, summary and description match what the plugin does,
   on the sample media and on your own fixtures.
3. **No hidden data stores.** A plugin has no network, but it must not fingerprint the library
   or keep data for some later use beyond its stated purpose. Data you keep goes in your own
   namespace and is described in the listing.
4. **Nothing illegal or hateful**, in strings, panels or outputs.
5. **Names and icons are yours.** Nothing imitating Maktaba or another developer. `Maktaba`,
   its Arabic name «مكتبة» and `official` are reserved in names (checked, with or without
   Arabic diacritics). Ids that contain others' trademarks are flagged.
6. **English and Arabic.** Every string key exists in both languages. Machine-translated Arabic
   is allowed when you say so in your notes; the listing then shows "Arabic machine
   translated" / «ترجمة آلية». A listing without both languages is refused.
7. **No links in what users see.** URLs, email addresses and phone numbers in your strings are
   flagged; panels and strings are plain text everywhere.
8. **Your source builds.** Every version needs its source archive (kept private to
   reviewers). Rust and TinyGo sources that build to the same `plugin.wasm` get the
   reproducible badge.

## What blocks a version automatically

| Check | Blocks when |
|---|---|
| C1 package | the format or a limit is wrong |
| C2 signature | your signature is invalid, the key is not active on your account, or the id is not under your verified prefix |
| C3 manifest | it is invalid, or `maktaba` has no upper bound |
| C4 imports | an import outside the allowed set (`plugin_forbidden_import`), or a gated interface with no capability (`plugin_undeclared_import`) |
| C6 strings | a key missing in one language, or a reserved word in the name |
| C7 malware | a scanner or rule hit |
| C8 sandbox | a trap on your own fixtures, peak memory over 80 % of what you declared, or outputs that break the rules |

Warnings (C5, C6's links, C9's diff, C10's build) send the version to a person; they do not
block it.

## Decisions and targets

A reviewer approves (the store countersigns with level `reviewed`), rejects with reasons from a
fixed list (misleading, does not work, asks for more than it needs, harmful outputs, malware,
policy, strings) and notes, or asks you a question; the clock pauses until you answer
(`POST /v1/submissions/{id}/answer`). First response within **5 business days** for a new
plugin and **2** for an update needing review, counted from when the check report is ready.
A queue past its target alerts the store's admins.
