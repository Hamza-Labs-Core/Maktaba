# Story 9.12 — Tag CRUD and normalization

`tags` and `video_tags` (§8.2). The endpoints are in Epic 7 Story 7.14;
this story owns the normalization, uniqueness rules, and the schema
update that adds `display_name` and `normalized_name` to architecture
§8.2.

**AC-1 — `tags` schema additions.**
- The architecture's `tags(id, name)` is extended to:
  ```
  ALTER TABLE tags ADD COLUMN display_name    TEXT;
  ALTER TABLE tags ADD COLUMN normalized_name TEXT;
  UPDATE tags SET display_name = name, normalized_name = lower(name);
  ALTER TABLE tags ALTER COLUMN display_name SET NOT NULL;
  ALTER TABLE tags ALTER COLUMN normalized_name SET NOT NULL;
  ALTER TABLE tags DROP COLUMN name;
  CREATE UNIQUE INDEX tags_normalized_name ON tags (normalized_name);
  ```

**AC-2 — Normalization on insert.**
- **Given** a tag name `"  Tafsir  "`,
- **When** inserted,
- **Then** the stored `display_name` is `"Tafsir"` (trim), the
  `normalized_name` (the uniqueness key) is `"tafsir"` (NFC unicode
  normalize + casefold).

**AC-3 — Conflict on normalized collision.**
- **Given** an existing tag `"Tafsir"`,
- **When** `"tafsir"` is inserted,
- **Then** the existing row is reused (same `id`); no new row, no
  error. The display name is *not* overwritten.

**AC-4 — Rename preserves links.**
- **Given** a tag with N video links,
- **When** PATCH renames the display name,
- **Then** `normalized_name` is recomputed; if the new normalized form
  collides with another tag, return 409 `type: tag-name-exists` and
  suggest merge. Otherwise the tag id stays; all video links stay.

**Test cases:**
- Unit: normalization fixtures for Arabic (with diacritics, NFC vs NFD).
- Integration: insert two normalize-equal tags → one row.
- Integration: rename to colliding name → 409.

**Edge cases:**
- Empty / whitespace-only tag → 422.
- Tag containing a slash (`"finance/2024"`) — allowed; surfaced as a
  flat string in v1. Hierarchical tags out of scope.
