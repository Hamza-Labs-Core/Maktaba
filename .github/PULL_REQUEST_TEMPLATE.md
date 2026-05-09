<!-- Maktaba PR template (Story 22.1). -->

## Summary

<!-- 1–3 bullets: what changed and why. Link the story/issue. -->

## Test plan

<!-- Bulleted checklist of how this was verified locally and in CI. -->

- [ ] `make lint`
- [ ] `make test-unit`
- [ ] `make test-integration` (if changes touch integration boundaries)
- [ ] `make test-e2e` (if changes touch user-visible flows)

## Force-merge override (delete if N/A)

<!--
Only fill this in if you've added the `force-merge` label and need to
bypass the normal CI gate. The line below is parsed by
`.github/workflows/_pr-body-check.yml` and recorded as the audit trail.
-->

<!-- force-merge: <one-sentence reason; ≥10 chars> -->
