---
kind: maintenance
component: relay, api
state: scheduled
starts: 2026-01-01T02:00:00Z
ends: 2026-01-01T02:30:00Z
title_en: Planned maintenance on {date}
title_ar: صيانة مجدولة في {date}
---

<!--
Template (maintenance), Epic 50 story 50.12. Copy to status/data/incidents/<yyyy-mm-dd>-<slug>.md
(docs/runbooks/cloud/status-page.md), set the times (UTC, ISO 8601) and the component, replace
each {…} in both languages, and delete this comment. One file per incident: update its state
and add a "### HH:MM UTC" line under both sections for each update.
{component}: relay = Remote access / الوصول عن بُعد, api = Sign-in and accounts / تسجيل الدخول والحسابات, push = Notifications / الإشعارات.
-->

## English

Planned maintenance on {date}, {start}–{end}. Remote play may pause for a few seconds. Playback at home is not affected.

## العربية

صيانة مجدولة في {date}، من {start} إلى {end}. قد يتوقف التشغيل عن بُعد بضع ثوانٍ. التشغيل داخل المنزل لا يتأثر.
