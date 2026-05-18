#if os(tvOS)
import SwiftUI
#if canImport(CoreImage)
import CoreImage
import CoreImage.CIFilterBuiltins
#endif

public struct PairingView: View {
    @EnvironmentObject var session: AppSession
    @State private var code: PairingCode?
    @State private var error: String?

    public init() {}

    public var body: some View {
        VStack(spacing: 32) {
            Text("Pair this Apple TV")
                .font(.title)
            if let code = code {
                if let qr = QRCode.image(for: code.code) {
                    Image(decorative: qr, scale: 1)
                        .interpolation(.none)
                        .resizable()
                        .frame(width: 360, height: 360)
                }
                Text(code.code)
                    .font(.system(size: 96, weight: .bold, design: .monospaced))
                Text("Open Maktaba on your phone and scan the QR code")
            } else if let error = error {
                Text(error).foregroundColor(.red)
            } else {
                ProgressView()
            }
        }
        .task { await loadCode() }
    }

    private func loadCode() async {
        guard let cfg = session.apiConfig else {
            error = "No server configured."
            return
        }
        let svc = PairingService(config: cfg)
        do {
            let c = try await svc.requestCode()
            code = c
            // Long-poll for phone approval, then flip to the main UI.
            let deviceID = try await svc.waitForApproval(code: c.code)
            session.pairedDeviceID = deviceID
            session.isPaired = true
        } catch {
            self.error = "Could not contact server."
        }
    }
}

/// QRCode renders a string into a CoreImage QR bitmap. Returns nil on
/// platforms without CoreImage (host unit-test target).
enum QRCode {
    static func image(for string: String) -> CGImage? {
        #if canImport(CoreImage)
        let ctx = CIContext()
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(string.utf8)
        guard let out = filter.outputImage else { return nil }
        let scaled = out.transformed(by: CGAffineTransform(scaleX: 10, y: 10))
        return ctx.createCGImage(scaled, from: scaled.extent)
        #else
        return nil
        #endif
    }
}
#endif
