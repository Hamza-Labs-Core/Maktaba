# Implementation Plan — Story 15.5 QR code pairing for mobile → server

> Companion to [story-15-05-qr-pairing.md](story-15-05-qr-pairing.md).
> The story states *what* and *why*; this plan states *how*.
> The API surface is owned by [Story 15.6](story-15-06-pairing-api.md).
> This story is the **client-side** flow: QR generation on TV/desktop,
> QR scanning on mobile, fallback manual entry, and SPKI pin commitment.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| QR generation | Client-side: tvOS uses `Core Image` `CIQRCodeGenerator`; AndroidTV uses `zxing-core`; desktop uses `qrcode-generator` (npm). |
| QR scanning | Mobile iOS uses `AVCaptureSession` + `AVCaptureMetadataOutput`; Android uses `CameraX` + ML Kit barcode. |
| URL format | `https://{server-host}/pair?code={code}&mid={mdns_id}&spki={spki_hash}&n={nonce}` per [Story 15.6](story-15-06-pairing-api.md). |
| Manual fallback | 6-digit code entry path used when camera permission denied or device has no camera. |
| Pin commitment | The scanning client adds the QR-supplied SPKI hash to its pin set on successful pair. |
| Out of scope | The pairing API itself ([Story 15.6](story-15-06-pairing-api.md)); SPKI-pinning client primitives ([Story 15.2](story-15-02-cloud-relay.md) §6). |

## 1. Architecture diagram

```
   ┌─────────────────────────────┐                 ┌─────────────────────┐
   │ TV/Desktop (issuer)         │                 │ Mobile (claimer)    │
   │  POST /api/auth/pair        │                 │  scan QR            │
   │  ←  {code, qr_url, expires} │                 │  parse url          │
   │  render QR + 6-digit code   │                 │  POST /api/auth/pair│
   │  poll GET /api/auth/pair    │                 │      /claim         │
   │  ← {claimed_at, ...}        │                 │  ←  {access, refresh│
   │  transition signed-in       │                 │     server: {spki}} │
   └─────────────────────────────┘                 │  pin spki           │
                                                   │  refetch + sign-in  │
                                                   └─────────────────────┘
```

## 2. TV/desktop — issuer

### 2.1 tvOS

```swift
// PairingView.swift (Story 14.1 references this)
struct PairingView: View {
    @StateObject var model = PairingViewModel()
    var body: some View {
        VStack(spacing: TVTokens.Spacing.lg) {
            if let qr = model.qrImage {
                Image(uiImage: qr).interpolation(.none).resizable()
                    .frame(width: 480, height: 480)
            } else { ProgressView() }
            Text(model.humanCode ?? "------")
                .font(.system(size: 96, weight: .bold, design: .monospaced))
            Text("Open Maktaba on your phone and scan this code.")
        }
        .task { await model.start() }   // POST /api/auth/pair
        .onChange(of: model.claimed) { c in if c { router.signedIn() } }
    }
}

@MainActor final class PairingViewModel: ObservableObject {
    @Published var qrImage: UIImage?
    @Published var humanCode: String?
    @Published var claimed = false
    func start() async {
        guard let resp = try? await api.createPair(deviceKind: .tv) else { return }
        humanCode = resp.code
        qrImage   = QRRenderer.render(string: resp.qrURL.absoluteString,
                                      moduleSize: 16,
                                      foreground: .black, background: .white)
        startPollLoop(resp.code)
    }
    private func startPollLoop(_ code: String) {
        Task {
            while !Task.isCancelled && !claimed {
                try? await Task.sleep(for: .seconds(2))
                if let st = try? await api.getPair(code), st.claimed { claimed = true }
            }
        }
    }
}
```

`QRRenderer.render` wraps `CIQRCodeGenerator`:

```swift
enum QRRenderer {
    static func render(string: String, moduleSize: Int, foreground: UIColor, background: UIColor) -> UIImage? {
        let data = string.data(using: .utf8)!
        let f = CIFilter.qrCodeGenerator()
        f.setValue(data, forKey: "inputMessage")
        f.setValue("H", forKey: "inputCorrectionLevel")  // High EC for TV viewing
        guard let out = f.outputImage else { return nil }
        let scaled = out.transformed(by: CGAffineTransform(scaleX: CGFloat(moduleSize), y: CGFloat(moduleSize)))
        return UIImage(ciImage: scaled)
    }
}
```

### 2.2 Desktop / web

`web/src/features/pairing/QRDisplay.tsx`:

```tsx
import QRCode from 'qrcode';
export function QRDisplay({ url }: { url: string }) {
    const [src, setSrc] = useState<string>();
    useEffect(() => { QRCode.toDataURL(url, { errorCorrectionLevel: 'H' }).then(setSrc); }, [url]);
    return <img src={src} alt="" width={480} height={480} />;
}
```

### 2.3 AndroidTV

```kotlin
val bitmap = QRCodeWriter().encode(url, BarcodeFormat.QR_CODE, 480, 480).toBitmap()
```

## 3. Mobile — claimer

### 3.1 iOS

```swift
final class QRScannerVC: UIViewController, AVCaptureMetadataOutputObjectsDelegate {
    private let session = AVCaptureSession()
    func metadataOutput(_ output: AVCaptureMetadataOutput,
                        didOutput metadataObjects: [AVMetadataObject],
                        from connection: AVCaptureConnection) {
        guard let m = metadataObjects.first as? AVMetadataMachineReadableCodeObject,
              m.type == .qr,
              let raw = m.stringValue,
              let url = URL(string: raw) else { return }
        session.stopRunning()
        Task { await PairFlow.claim(url: url) }
    }
}
```

`PairFlow.claim` parses the URL, verifies the host matches a server reachable on LAN (mDNS lookup) or falls back to relay, then `POST /api/auth/pair/claim {code, nonce, ...}`. On success, the response includes `server.spki` which is committed to the pin store.

### 3.2 Android

```kotlin
val scanner = BarcodeScanning.getClient()
imageAnalysis.setAnalyzer(executor) { proxy ->
    val image = InputImage.fromMediaImage(proxy.image!!, proxy.imageInfo.rotationDegrees)
    scanner.process(image).addOnSuccessListener { codes ->
        codes.firstOrNull { it.format == Barcode.FORMAT_QR_CODE }?.rawValue?.let { raw ->
            cameraExecutor.shutdown()
            viewModel.claim(raw.toUri())
        }
    }.addOnCompleteListener { proxy.close() }
}
```

### 3.3 Manual entry fallback

Camera permission denied or no camera → a `PairManualView` with a 6-digit input. The manual claim cannot supply the nonce (it's not on the QR) — so the AC-EC about phishing requires that **the manual path is treated as lower-trust**: the claim endpoint must reject manual claims that don't include a nonce. This is enforced by [Story 15.6](story-15-06-pairing-api.md) returning `400 nonce-mismatch`. We surface the error: "This device requires the QR code, not just the 6-digit code. Use a phone with a camera to scan."

This means manual entry only works when the user is on the same LAN and the QR's nonce is published to a side-channel (we do not currently support that — manual entry is therefore a limited fallback on TVs that show both the code and the nonce). For v1, manual entry shows a clear "Use the QR code" message rather than fudging trust.

## 4. SPKI commitment

After successful claim, the response includes `server: { mdns_id, spki }`. The mobile client:

```swift
SecurePinStore.shared.add(mdnsID: resp.server.mdnsID, spki: resp.server.spki)
```

This pin set is the trust anchor that all future requests (LAN or relay) verify against (per [Story 15.2](story-15-02-cloud-relay.md) §6).

If the QR-supplied `spki` doesn't match the response's `server.spki` (e.g., MITM substituted the QR), the client refuses: "Server identity mismatch — do not approve."

```swift
guard qrSpki == resp.server.spki else {
    throw PairError.spkiMismatch
}
```

## 5. URL parsing & confirmation

URL form: `https://{server}/pair?code=ABC123&mid={mdns_id}&spki={hash}&n={nonce}`.

Before POSTing the claim, the client checks:
- The `mdns_id` is one we've seen via mDNS, OR we surface a confirmation dialog "Pair with `maktaba.local`?" (story EC).
- The `spki` is a valid hex-encoded sha256 (64 chars).
- The `code` length and alphabet match Story 15.6 spec.

If any check fails, the UI shows "QR code is not from a Maktaba server" and aborts.

## 6. TTL handling

The QR encodes an `expires_at`-bound code (Story 15.6 issues with 5-min TTL). The TV view auto-refreshes 30 s before expiry:

```swift
.task {
    while !Task.isCancelled {
        if let resp = await api.createPair(...) { update(resp) }
        try? await Task.sleep(until: resp.expires - 30)
    }
}
```

If the user is mid-scan when the QR refreshes, the in-flight scan still works — the previous code is valid for the full 5 minutes.

## 7. Test plan

### 7.1 TV/desktop

| Test | What it pins |
|---|---|
| `testQRRendersWithCorrectECLevel` | Generated QR's parsed correction level = H. |
| `testPollDetectsClaim` | Stub poll; on `claimed_at != null` flip, view transitions. |
| `testQRRefreshBefore30sExpiry` | Stub clock; new QR fetched 30 s before expiry. |

### 7.2 Mobile

| Test | What it pins |
|---|---|
| `testCameraDeniedFallsBackToManualWithGuidance` | Permission denied → `PairManualView`; surface "use the QR" explanation. |
| `testMITMQRRefused` | Stub claim response with different SPKI than the QR → throws `spkiMismatch`. |
| `testUnknownMDNSIDPromptsConfirmation` | mDNS table doesn't include the `mid` → confirmation dialog appears. |
| `testRelayFallbackWhenNoLAN` | LAN unreachable; QR URL host resolves to relay → claim still works. |

### 7.3 End-to-end

| Test | What it pins |
|---|---|
| `e2ePairWithin3s` | Headless TV simulator + iOS XCTest UI test; full flow under 3 s. |
| `e2eExpiredQRError` | Wait 5 min after issue; claim returns 400 code-expired; UI shows "Pairing code expired". |

## 8. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| QR for a server the phone has never seen | "Pair with `maktaba.local`?" confirmation. | `testUnknownMDNSIDPromptsConfirmation` |
| Camera permission denied | Manual code entry with explanation that nonce is missing. | `testCameraDeniedFallsBackToManualWithGuidance` |
| Phishing fake QR | Server's `spki` from claim response ≠ QR's; refused. | `testMITMQRRefused` |
| QR's SPKI doesn't match real cert (MITM mid-pair) | Same: claim verifies; mismatch refused. | (above) |
| Cellular path (relay) | URL host resolves via relay; SPKI binding still verified end-to-end. | `testRelayFallbackWhenNoLAN` |
| QR refreshes mid-scan | Previous code still valid for 5 min; scan succeeds. | `testQRRefreshDuringScan` |
| User cancels mid-scan | `DELETE /api/auth/pair/{code}` to revoke; idempotent. | `testCancelRevokes` |
| Time skew on phone | Don't trust local time; trust server's `expires_at`. | `testServerTimeOnly` |
| `nonce` missing in URL | Reject parse; "QR is not from Maktaba". | `testNonceRequired` |
| Two scans of same QR | First wins; second gets 400 `code-already-claimed` and a clear UI message. | `testDoubleClaimSurfacesError` |

## 9. Dependencies

| Dep | Version | Why |
|---|---|---|
| `CIQRCodeGenerator` | system | tvOS QR rendering. |
| `qrcode` | latest npm | Web/desktop. |
| `com.google.zxing:core` | 3.5 | AndroidTV QR rendering. |
| `AVCaptureSession` | system | iOS scan. |
| `com.google.mlkit:barcode-scanning` | 17.x | Android scan. |

## 10. Acceptance checklist

**Issuer**
- [ ] tvOS, AndroidTV, desktop render QR + 6-digit code.
- [ ] QR auto-refreshes before expiry.
- [ ] Polls `GET /api/auth/pair` for claim transition.

**Claimer**
- [ ] iOS / Android scans QR; parses url; claims via Story 15.6 endpoint.
- [ ] Manual entry guides user when nonce missing.
- [ ] SPKI pin committed on success; mismatch refuses.

**Tests**
- [ ] All §7 tests pass.

**Docs**
- [ ] `specs/epics/15-discovery/README.md` ticks story 15.5.
