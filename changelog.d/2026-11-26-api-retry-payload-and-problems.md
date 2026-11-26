### Fixed

- **Retries from the failures and queue pages keep job options.** Retrying failed jobs from the
  failures page, queue bulk retry, and single-job retry now preserves each job's `payload` and
  `force_cascade` flag while stripping one-shot `run_now` marks.
- **API error types match their HTTP statuses, and plugin refusals no longer return 500 on
  `/api/components`.** Unauthenticated 401 responses now send `unauthorized` instead of `forbidden`,
  expired device pairing codes send 410 `pairing-code-expired` instead of `conflict`, refused
  Maktaba Cloud links send 422 `cloud-link-refused` instead of `invalid-credentials`, batch undo
  returns 404 when a referenced edit is missing, expired access tokens emit 401 `token-expired`,
  and plugin lifecycle refusals through `/api/components` now surface their own status and code
  instead of 500.
