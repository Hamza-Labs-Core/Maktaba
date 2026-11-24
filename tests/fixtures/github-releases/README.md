# GitHub's release list, as a home reads it

`page-1.json.gz` is the first page of this repository's releases exactly as GitHub answered a
home's update check on 2026-11-24 (`record.sh`): `GET /repos/Hamza-Labs-Core/Maktaba/releases?per_page=10&page=1`,
unauthenticated, `Accept: application/vnd.github+json`, `X-GitHub-Api-Version: 2022-11-28`, Go's
User-Agent. It is GitHub's answer byte for byte (gzipped for the repository): 3,197,782 bytes of
minified JSON, v0.3.161 to v0.3.152, 179 to 190 assets a release, the largest release 325,470
bytes. Everything in it is public; every uploader and author is `github-actions[bot]`.

The tests check every cap on what a home reads from GitHub against it (`docs/updates.md`, "The
release feed's caps"): `api/internal/updates` (the list, the tag read, the signed files),
`api/internal/system` (the About check), `cmd/maktaba-server/internal/selfupdate` and
`ghasset`, and the update rehearsal's scenario 20, which serves releases in its shape.

Re-record it when a release's shape changes (many more assets, longer notes) or when a release
feed check fails, and say in the pull request what grew.
