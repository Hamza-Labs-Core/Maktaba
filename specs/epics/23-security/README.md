# Epic 23 — Security

**Goal.** Maktaba is safe to expose on a home LAN by default and safe to
expose to the internet with the documented production hardening. No
secret leaves the host that wasn't authorized to. Users authenticate
once and stay authenticated across the device fleet. The supply chain
is auditable.

This epic addresses authentication, authorization, transport, secrets,
content safety, and supply-chain integrity. It composes with
[Epic 21](../21-observability/README.md) (audit log) and
[Epic 22](../22-devops/README.md) (signed artifacts).

## Stories

- [Story 23.1 — Authentication](story-23-01-authentication.md)
- [Story 23.2 — Authorization and ACLs](story-23-02-authorization-acls.md)
- [Story 23.3 — Transport security](story-23-03-transport-security.md)
- [Story 23.4 — Secrets management](story-23-04-secrets-management.md)
- [Story 23.5 — Input validation and content safety](story-23-05-input-validation.md)
- [Story 23.6 — Rate limiting and abuse protection](story-23-06-rate-limiting.md)
- [Story 23.7 — Supply-chain security](story-23-07-supply-chain-security.md)
- [Story 23.8 — Coordinated disclosure and security response](story-23-08-coordinated-disclosure.md)
