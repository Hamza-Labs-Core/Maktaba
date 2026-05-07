# Epic 19 — Scalability

**Goal.** Maktaba serves the 30 TB / single-household target on one box
without falling over, and the same code paths scale horizontally to
multi-host deployments without architectural rewrites. Each service has
an explicit scale axis; bottlenecks are detected by load test, not by
production incident.

This epic does not cover *speed* of any single request (that's
[Epic 18](../18-performance/README.md)). It covers *capacity*: how many
videos, segments, sessions, and concurrent users a deployment can hold
and serve before the next tier kicks in.

Source-of-truth for capacity numbers is
[`specs/architecture.md` §10](../../architecture.md). Where a number
appears in both, the two must agree.

## Stories

- [Story 19.1 — Single-host capacity floor](story-19-01-single-host-capacity.md)
- [Story 19.2 — Horizontal scale-out for the API service](story-19-02-api-scale-out.md)
- [Story 19.3 — Horizontal scale-out for the streaming service](story-19-03-streaming-scale-out.md)
- [Story 19.4 — Horizontal scale-out for the pipeline service](story-19-04-pipeline-scale-out.md)
- [Story 19.5 — Database scaling and failover](story-19-05-database-scaling.md)
- [Story 19.6 — Storage scaling and large library handling](story-19-06-storage-scaling.md)
- [Story 19.7 — Concurrency caps and quotas](story-19-07-concurrency-caps.md)
- [Story 19.8 — Multi-tenant readiness (deferred capacity)](story-19-08-multi-tenant-readiness.md)
