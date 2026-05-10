import SwiftUI

public struct PairingView: View {
    @State private var code: PairingCode?
    @State private var error: String?

    public init() {}

    public var body: some View {
        VStack(spacing: 32) {
            Text("Pair this Apple TV")
                .font(.title)
            if let code = code {
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
        do {
            code = try await PairingService().requestCode()
        } catch {
            self.error = "Could not contact server."
        }
    }
}
