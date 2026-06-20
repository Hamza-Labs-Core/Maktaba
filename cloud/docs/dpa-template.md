# Data Processing Agreement (TEMPLATE)

> **TEMPLATE — not an executed agreement.** Epic 30, Story 30.2. Fill in
> the bracketed fields before use and have it reviewed by counsel. This
> documents the relay's processing for operators who must enter a DPA
> with their own sub-processors or customers.

## 1. Parties

- **Controller:** [Operator legal entity], [address].
- **Processor:** Hamza Labs ("Maktaba Cloud"), [address].

## 2. Subject-matter and duration

The Processor operates the Maktaba cloud relay, brokering connections
between Controller-operated home servers and their end users. This DPA
covers processing for the duration of the service agreement and survives
until all data is deleted or returned per §8.

## 3. Nature and purpose of processing

Operating, securing, and capacity-planning the relay. Processing is
limited to **aggregate, anonymous analytics** (connection counts,
bandwidth, request volume, country of request, push-delivery outcomes)
plus the operational push-notification log.

## 4. Categories of data

| Category | Identifying? | Where |
|---|---|---|
| Aggregate connection/bandwidth/request counts | No | `relay_metrics_*` |
| Country of request (edge-derived) | No (country only; IP discarded) | `relay_metrics_raw/hourly` |
| Push delivery records (user id, platform, status) | Yes | `push_dispatch_log` |

**Not processed:** IP addresses (analytics), server identities
(analytics), media titles/filenames/content, request URLs or payloads.

## 5. Categories of data subjects

End users of Controller-operated home servers; Controller's operators.

## 6. Sub-processors

| Sub-processor | Purpose | Location |
|---|---|---|
| [Cloud/IaaS provider] | Hosting / compute / database | [region] |
| Apple Push Notification service (APNs) | iOS push delivery | USA |
| Firebase Cloud Messaging (FCM) | Android/web push delivery | USA |
| [CDN/edge provider] | TLS termination, edge country derivation | [region] |

## 7. Security measures (Art. 32)

- Aggregate-only analytics schema: no user/server id, no IP.
- Country derived at the edge; IP never persisted or logged.
- TLS in transit; encryption at rest.
- Access to operator endpoints restricted by email-domain allow-list.
- Retention limits with automatic purge (raw 24h, hourly 90 days).

## 8. Data-subject rights, deletion, and return

- **Erasure / account deletion:** `DataSubjectService.Delete` removes the
  account's push-log rows; aggregate analytics reference no user and
  require no change.
- **Retention:** automatic purge per §7. On termination, remaining
  personal data is deleted within [30] days.

## 9. Audit and assistance

The Processor will make available information necessary to demonstrate
compliance and assist the Controller with data-subject requests and DPIAs
as required by Art. 28(3)(h).

---

_Signatures, effective date, and governing law: [to be completed]._
