# Settings → Subscription

- **Route:** `/settings/subscription` (`/billing` redirects here; the side-menu link **Subscription** points here)
- **Component:** `src/pages/Settings/SubscriptionSettings.tsx` (admin view and viewer view)
- **API calls:** admin: `GET /api/admin/license` → the license view (`available`, `tier`, `features`, `seats_allowed` (-1 unlimited), `seats_used`, `over_seats`, `status` none/active/grace/expired/revoked, `license` {id, tier, seats, holder, issued_at, expires_at, source cloud/offline}, `message`, `last_refresh_at`, `last_refresh_error`, `cloud` {claimed, server_id, slug}, `manage_url`); `POST /api/admin/license` (the pasted license JSON) → the view, 400 `license-invalid` with a plain reason, 503 when the build has no license key; `DELETE /api/admin/license` → 204; `POST /api/admin/license/refresh` → the view. Viewer: `GET /api/entitlements`.
- **Access:** any signed-in user sees the plan; only an admin sees seats, the license and Maktaba Cloud. The API refuses every admin route to a viewer (`api/internal/handlers/subscriptions/license_test.go`).
- **Tests:** `src/pages/Settings/SubscriptionSettings.test.tsx`.

| ID     | Case                                                                                                        | Covered                                            |
| ------ | ----------------------------------------------------------------------------------------------------------- | -------------------------------------------------- |
| SUB-01 | Free with two users: plan, "2 of 1", over-seat warning, nothing included, Get a plan, not linked, no Remove | Yes                                                |
| SUB-02 | Cloud plan license: Pro, Active, holder, "Renews by", "2 of unlimited", linked as the slug, Manage plan     | Yes                                                |
| SUB-03 | Apply: the trimmed paste is parsed and posted, the box empties, the new view shows                          | Yes                                                |
| SUB-04 | A refused license shows the server's plain reason                                                           | Yes                                                |
| SUB-05 | Text that is not JSON never reaches the server                                                              | Yes                                                |
| SUB-06 | Remove asks first; cancelling sends nothing                                                                 | Yes                                                |
| SUB-07 | Check now asks the license server; its error shows                                                          | Yes                                                |
| SUB-08 | A build without the license key: no paste box, no Check now                                                 | Yes                                                |
| SUB-09 | A viewer sees the plan read-only and never calls an admin route                                             | Yes                                                |
| SUB-10 | A revoked license: the server's message as an alert, Revoked badge                                          | Yes                                                |
| SUB-11 | A load error offers Try again                                                                               | Yes                                                |
| SUB-12 | Arabic / RTL strings                                                                                        | Strings in `src/i18n/ar.json`; not a separate test |
