# Checklist results

One file per run of a device checklist: `YYYY-MM-DD-<platform>-<device>.md`, for example
`2026-12-01-synology-ds920plus.md`. Copy the template below. Attach the container's log
(`docker logs maktaba > …-docker.log`) and the setup report (Settings → This machine → Copy
report, or `GET /api/system/machine`) beside it, named after the result file. Never attach a
password, a token or a pairing code: read the log before committing it.

```markdown
---
checklist: synology            # synology, truenas or unraid
date: 2026-12-01
device: DS920+ (J4125, 8 GB)
os: DSM 7.2.2-72806 Update 3
maktaba: v0.3.130
result: pass                   # pass, or fail with the failed steps listed below
---

| Step | Result | Notes |
|---|---|---|
| 1 | pass | |
| 2 | fail | the ACL fix text named the wrong share: #NNNN |

Attached: 2026-12-01-synology-ds920plus-docker.log, 2026-12-01-synology-ds920plus-machine.json
```
