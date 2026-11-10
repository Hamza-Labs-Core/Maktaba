# NAS install guides (maktaba.tv/install/…)

The step-by-step guides a NAS owner follows, in English and Arabic. They are written here, beside
the install files they describe, and published on maktaba.tv under `/install/<platform>` and
`/ar/install/<platform>` (Epic 59 story 59.14 settles the site they are built into).

| Guide | Arabic | Page |
|---|---|---|
| [truenas.md](truenas.md) | [truenas.ar.md](truenas.ar.md) | maktaba.tv/install/truenas (story 59.10) |

## Screenshot placeholders

A screenshot of a NAS's own screens can only be taken on the device. Until the device checklist
has taken it, a guide shows a placeholder, written as a quote that starts with
**📷 Screenshot to come** and names the file. The device checklist's step that takes them
([truenas.md step 13](../checklists/truenas.md)) replaces each placeholder with the image.

| File (`screenshots/truenas/`) | What it shows |
|---|---|
| `dataset-apps-preset.{en,ar}.png` | Datasets → Add Dataset with the Apps preset chosen |
| `acl-entry.{en,ar}.png` | the ACL editor with the `apps` user's entry (Read) |
| `install-storage.{en,ar}.png` | the app's form: Storage (data host path, a media folder read-only) |
| `install-resources.{en,ar}.png` | the app's form: Resources (memory, GPU) |
| `app-shell.{en,ar}.png` | the app's Shell with the setup command |
| `install-via-yaml.{en,ar}.png` | Apps → Discover → Install via YAML with the Custom App pasted |

TrueNAS's UI has no Arabic: the `.ar.png` files show TrueNAS in English with Maktaba's Arabic
around it, as the Arabic guide describes each screen.
