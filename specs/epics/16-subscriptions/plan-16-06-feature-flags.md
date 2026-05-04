# Implementation Plan — Story 16.6 Feature flags per tier (client surface)

> Companion to [story-16-06-feature-flags.md](story-16-06-feature-flags.md).
> The story states *what* and *why*; this plan states *how*.
> The server resolution is owned by [Story 16.8](story-16-08-feature-flags-api.md);
> this story owns the **client cache, refresh, signature verification,
> and React/SwiftUI/Compose binding.**

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Web client | `web/src/lib/flags/{client.ts,verifier.ts,context.tsx,hooks.ts}` plus a Zustand-backed cache. |
| Native (tvOS) | `apps/tvos/Sources/Flags/{Flags.swift,FlagSignatureVerifier.swift}`. |
| Native (AndroidTV / mobile Android) | `apps/androidtv/.../flags/{Flags.kt,FlagSignatureVerifier.kt}`. |
| Cache TTL | 60 s; refreshed on app foreground; persisted to disk so flags survive cold-launch and offline. |
| Signature verification | Ed25519. Server's long-term key set is owned by **Epic 10 Story 10.18** ("Ed25519 long-term server identity keys") — NOT Story 10.6, which covers RS256 / JWKS for short-lived API JWTs only. Public keys are bundled at client build-time. Client refuses unsigned or bad-signature responses. Until Story 10.18 lands this plan blocks: there is no Ed25519 source to verify against. |
| Beta opt-in | Settings → Advanced → Experiments toggles call `POST /api/me/cohorts {cohort}` ([Story 16.8](story-16-08-feature-flags-api.md)). |
| Out of scope | Server resolver + admin endpoints ([Story 16.8](story-16-08-feature-flags-api.md)). |

## 1. Architecture diagram

```
   App boot                           Foreground
        │                                  │
        ▼                                  ▼
   ┌──────────────────────────────────────────────┐
   │ FlagsClient                                  │
   │  - load from disk cache (signed bundle)      │
   │  - verify signature; if invalid, ignore      │
   │  - schedule refresh (60s + jitter)           │
   │  - expose `useFlag(key)` hook                │
   └──────────────────┬───────────────────────────┘
                      │
                      ▼
                GET /api/me/flags  (Story 16.8)
                      │
                      ▼
              { flags, signature, kid, issued_at, expires_at }
                      │
                      ▼
            verify(Ed25519, kid → bundled pubkey)
            on success: persist + emit reactive update
            on failure: keep prior cached flags; log; never trust unsigned
```

## 2. Type definitions

```ts
// web/src/lib/flags/types.ts
export type FlagValue = boolean | number | string | null;
export interface FlagsBundle {
    flags: Record<string, FlagValue>;
    signature: string;     // base64url
    kid: string;           // key id
    issued_at: string;     // ISO 8601
    expires_at: string;    // ISO 8601
    tier?: 'free' | 'home' | 'pro';
}
```

## 3. Web client

```ts
// web/src/lib/flags/client.ts
import { verifyEd25519 } from './verifier';
import { z } from 'zod';

const Bundle = z.object({
    flags: z.record(z.union([z.boolean(), z.number(), z.string(), z.null()])),
    signature: z.string(),
    kid: z.string(),
    issued_at: z.string(),
    expires_at: z.string(),
});

export class FlagsClient {
    private bundle: FlagsBundle | null = null;
    private timer?: number;
    private listeners = new Set<() => void>();

    constructor(private fetch = window.fetch.bind(window)) {}

    async start() {
        this.bundle = await this.loadFromDisk();
        if (this.bundle && !await this.verify(this.bundle)) this.bundle = null;
        await this.refresh();
        this.scheduleRefresh();
        addEventListener('visibilitychange', () => {
            if (document.visibilityState === 'visible') void this.refresh();
        });
    }

    isOn(key: string): boolean {
        const v = this.bundle?.flags[key];
        return typeof v === 'boolean' ? v : false;
    }

    private async refresh() {
        try {
            const r = await this.fetch('/api/me/flags');
            if (!r.ok) return;
            const raw = await r.json();
            const parsed = Bundle.safeParse(raw);
            if (!parsed.success) return;        // refuse malformed
            const ok = await this.verify(parsed.data);
            if (!ok) return;                    // refuse unsigned
            this.bundle = parsed.data;
            void this.persist(parsed.data);
            this.listeners.forEach(l => l());
        } catch { /* keep prior */ }
    }

    private scheduleRefresh() {
        const jitter = 1 + Math.random() * 0.5;
        this.timer = setTimeout(() => { this.scheduleRefresh(); void this.refresh(); }, 60_000 * jitter);
    }

    private async verify(b: FlagsBundle): Promise<boolean> {
        const pub = SERVER_LONGTERM_PUBKEYS[b.kid];   // bundled at build
        if (!pub) return false;
        if (new Date(b.expires_at) < new Date()) return false;
        const canonical = canonicalize({ flags: b.flags, kid: b.kid, issued_at: b.issued_at, expires_at: b.expires_at });
        return verifyEd25519(pub, canonical, b.signature);
    }

    subscribe(fn: () => void) { this.listeners.add(fn); return () => this.listeners.delete(fn); }
}
```

## 4. React binding

```tsx
// web/src/lib/flags/hooks.ts
import { useSyncExternalStore } from 'react';

export function useFlag(key: string): boolean {
    return useSyncExternalStore(
        (cb) => flagsClient.subscribe(cb),
        () => flagsClient.isOn(key),
    );
}

// usage
function RemoteAccessSection() {
    const enabled = useFlag('relay');
    if (!enabled) return null;     // hidden, not greyed (Story 16.1)
    return <RemoteAccessForm />;
}
```

## 5. Native parity

### 5.1 tvOS

```swift
// apps/tvos/Sources/Flags/Flags.swift
@MainActor final class Flags: ObservableObject {
    @Published private(set) var bundle: Bundle?
    private let verifier = SignatureVerifier()

    func bootstrap() async {
        if let cached = persistence.load() { if await verifier.verify(cached) { bundle = cached } }
        await refresh()
        Task { for await _ in NotificationCenter.default.notifications(named: UIApplication.willEnterForegroundNotification) {
            await refresh()
        } }
        Task { await refreshLoop() }
    }

    func isOn(_ key: String) -> Bool {
        guard case let .bool(v)? = bundle?.flags[key] else { return false }
        return v
    }
}
```

### 5.2 AndroidTV

```kotlin
class FlagsClient(...) {
    private val _bundle = MutableStateFlow<FlagsBundle?>(null)
    val bundle: StateFlow<FlagsBundle?> = _bundle.asStateFlow()

    suspend fun bootstrap() {
        persistence.load()?.takeIf { verifier.verify(it) }?.also { _bundle.value = it }
        refresh()
        // Refresh on app foreground
        ProcessLifecycleOwner.get().lifecycle.addObserver(object : DefaultLifecycleObserver {
            override fun onStart(owner: LifecycleOwner) { scope.launch { refresh() } }
        })
        // Periodic
        scope.launch {
            while (isActive) {
                delay(60_000L + Random.nextLong(30_000L))
                refresh()
            }
        }
    }
}
```

### 5.3 Compose / SwiftUI

```kotlin
@Composable
fun rememberFlag(key: String): Boolean {
    val client = LocalFlagsClient.current
    val bundle by client.bundle.collectAsStateWithLifecycle()
    return remember(bundle, key) { (bundle?.flags?.get(key) as? Boolean) == true }
}
```

## 6. Tampering defense

The story EC: "Tampering with the local cache: flags are signed by the server and re-checked on every privileged action."

Implementation: every privileged action (e.g., creating a federation
pair, starting a backup) calls `flags.assertOn('federation')` which
checks an in-memory "this bundle has been verified" flag before
proceeding. If the cached bundle has been mutated on disk (storage
hijacked), the next refresh tries to verify the loaded bytes; on
failure the verified flag flips to `false` and we refuse, regardless
of what `bundle.flags` claims.

```ts
// The Ed25519 verify is run ONCE per bundle (on load and on each
// successful refresh), and the result is cached on the in-memory
// FlagsBundle object. Subsequent assertOn() calls hit the cached
// boolean — not the verifier — so a privileged-action storm does not
// pay an Ed25519 verify per call (~100 µs each on web).
function assertOn(key: string) {
    if (!flagsClient.isOn(key)) throw new FlagDeniedError(key);
    if (!flagsClient.currentBundle.signatureVerified) throw new TamperedCacheError();
}
```

The cached `signatureVerified` boolean is invalidated on the same
events that drop the bundle: refresh failure, kid rotation, sign-out,
and any explicit cache-clear path.

## 7. Beta cohorts

Settings → Advanced → Experiments lists cohorts the user is allowed to opt in/out of. Toggling calls:

```ts
await fetch(`/api/me/cohorts`, { method: 'POST', body: JSON.stringify({ cohort: 'preview-2026' }) });
await flagsClient.refresh();
```

The flags server re-resolves on the next refresh; the user immediately sees the experimental feature show up.

## 8. Test plan

### 8.1 Unit

| Test | What it pins |
|---|---|
| `testRefuseMalformedBundle` | Schema mismatch → bundle stays null. |
| `testRefuseInvalidSignature` | Tampered `flags.relay = true` (not signed) → ignored; `isOn('relay')` returns prior. |
| `testRefuseUnknownKID` | KID not in bundled pubkeys → ignored. |
| `testRefuseExpiredBundle` | `expires_at < now()` → ignored. |
| `testCacheTTLAndJitter` | 100 instances → refresh times spread across [60, 90] s. |
| `testRefreshOnVisibilityChange` | Tab hidden → visible → refresh fires. |
| `testIsOnFalsyByDefault` | Unknown key → false. |
| `testPersistRoundTrip` | Save bundle to disk; reload; identical and verifies. |

### 8.2 React binding

| Test | What it pins |
|---|---|
| `testUseFlagRerendersOnRefresh` | Render component using `useFlag('x')`; flip flag in store; component re-renders. |
| `testGatedComponentHiddenWhenFalse` | `relay = false` → no DOM. |

### 8.3 Native

| Test | What it pins |
|---|---|
| `testTVOSFlagsBootstrap` | Cold launch → loads cached → verifies → emits. |
| `testAndroidTVRefreshesOnForeground` | App moves to foreground → refresh job posts. |

### 8.4 End-to-end

| Test | What it pins |
|---|---|
| `e2e_TierFlipsHidesRow` | Premium → free; within ≤ 60 s next refresh, premium UI rows hidden. |
| `e2e_BetaOptInRevealsFlag` | Toggle cohort opt-in; refresh; experimental panel visible. |

## 9. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Refresh fails (5xx) | Keep prior cache; never enable a flag that failed validation. | `testRefreshFail` |
| Conflicting flags (relay=true, quota=0) | Client renders feature but server rejects use; client surfaces a clear error. | `testConflictingFlagsServerEnforces` |
| Local cache tampered | Signature mismatch → bundle ignored; default-false. | `testRefuseInvalidSignature` |
| Beta flag rolled back | Server returns flag absent; client falls back to default; UI hides within 60 s. | `e2e_BetaRollback` |
| User signs out | Client clears cache; defaults until next sign-in. | `testSignOutClearsCache` |
| Multiple tabs, one signs out | Storage event listener clears bundle in all tabs. | `testStorageEventClears` |
| Clock skew | `expires_at` fudged by ± 5 min server-side; client uses server's `issued_at` to detect skew and warn. | `testClockSkewWarning` |
| KID rotation | Old kid in cache; new bundled key. Cache refused; refresh fetches new bundle with new kid; we accept. | `testKIDRotationGraceful` |
| Server returns flag value of wrong type (number for boolean key) | Client coerces (truthy/falsy) but logs a warning. | `testTypeCoercion` |
| Privileged action attempted while offline (cache stale) | Allowed if signature still verifies and `expires_at` is in the future; otherwise denied. | `testPrivilegedOfflineAllowedIfFresh` |

## 10. Dependencies

| Dep | Version | Why |
|---|---|---|
| `@noble/ed25519` | latest | Web Ed25519 verify. |
| `CryptoKit` | system | tvOS Ed25519 verify. |
| `org.bouncycastle:bcprov-jdk18on` | latest | AndroidTV Ed25519 verify (or `java.security` with Ed25519 from JDK 15+). |

## 11. Acceptance checklist

**Client**
- [ ] FlagsClient on web, tvOS, AndroidTV, mobile.
- [ ] 60 s cache TTL with jitter.
- [ ] Refresh on app foreground.

**Verification**
- [ ] Ed25519 signature checked before bundle accepted.
- [ ] Tampered/expired bundles refused.

**UI**
- [ ] `useFlag` hook + native equivalents.
- [ ] Cohort opt-in works.

**Tests**
- [ ] All §8 tests pass.

**Docs**
- [ ] `specs/epics/16-subscriptions/README.md` ticks story 16.6.
