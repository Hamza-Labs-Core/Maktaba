# Story 34.8 — Branding and domain settings

## Description

*As the owner, I want the site to carry my channel brand — name, logo, colours, about page, legal
texts — and to run on my own domain once I have picked one, without editing code.*

Design: [design.md](design.md) §3.1, §4, §10.3; README R8.

## Acceptance criteria

- **AC1** Branding settings (ar/en where text): site name, short name, logo (SVG/PNG ≤ 512 KB), favicon,
  colours (primary, accent, background, text; AA contrast checked on save with a suggestion when it
  fails), about page (Markdown, sanitised), footer, legal pages (terms, privacy, copyright/takedown with
  a contact address), slate image and texts, off-air texts, default language, `arabic_digits`.
- **AC2** Branding changes are published as `site.json` within seconds (34.2 delta) and apply to open
  pages without a reload (CSS variables); the PWA manifest and icons follow.
- **AC3** `tv_public.domain` and `staging_domain` accept a hostname in a zone of the owner's Cloudflare
  account (checked through the API: zone active, no conflicting CNAME); empty means workers.dev.
- **AC4** Setting or changing the domain, in order: attach the Workers Custom Domain (Cloudflare creates
  the DNS record and the certificate) → wait for the certificate → add the hostname to `allowedOrigins`
  on every public input, the Turnstile widget and the Access app → switch canonical URLs and the sitemap
  → only then remove the old hostname (kept as a 301 redirect for 30 days when it was a custom domain).
- **AC5** The legal pages ship as clearly marked placeholders; enabling the site asks the owner to
  confirm they reviewed them (README R11.5).
- **AC6** The admin page shows a live preview of the home page with the current branding in both
  languages.

## Test cases

- **TC1** `TestBrandingValidation` (sizes, contrast, Markdown sanitising).
- **TC2** `TestDomainChangeOrder` (fake CF API: custom domain, cert status, inputs, widget, Access).
- **TC3** web `branding.test.tsx` (CSS variables applied live, RTL).
- **TC4** Worker `redirect-old-host.test.ts`.

## Edge cases

- **EC1** The zone is not on the owner's account: clear message with the steps to add it to Cloudflare.
- **EC2** Certificate issuance takes long: the old hostname keeps serving; the admin page shows "pending".
- **EC3** Apex vs `www`: only the exact hostname is attached; the runbook explains the redirect rule.

## Dependencies

34.2, 34.3 (inputs), 34.7 (widget/app), 34.10 (deploy job).
