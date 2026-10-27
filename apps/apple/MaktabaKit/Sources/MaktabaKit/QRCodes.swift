import CoreImage
import CoreImage.CIFilterBuiltins
import SwiftUI
#if os(iOS)
import AVFoundation
import VisionKit
#endif

/// A QR code of `text` (CoreImage's generator), black on white whatever the theme.
public struct QRCodeView: View {
    let text: String

    public init(text: String) {
        self.text = text
    }

    public var body: some View {
        if let image = Self.image(text) {
            Image(decorative: image, scale: 1)
                .interpolation(.none)
                .resizable()
                .scaledToFit()
                .padding(10)
                .background(Color.white, in: RoundedRectangle(cornerRadius: 12))
        }
    }

    static func image(_ text: String) -> CGImage? {
        let f = CIFilter.qrCodeGenerator()
        f.message = Data(text.utf8)
        f.correctionLevel = "M"
        guard let out = f.outputImage?.transformed(by: CGAffineTransform(scaleX: 8, y: 8)) else { return nil }
        return CIContext().createCGImage(out, from: out.extent)
    }
}

#if os(iOS)
/// Scans one QR code with the camera (VisionKit's data scanner) and hands over its text.
/// Asks for the camera the first time; says so when it is refused or the device cannot scan.
struct QRScannerSheet: View {
    let words: ConnectStrings
    let found: (String) -> Void
    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL
    @State private var camera = SystemCamera().state()

    var body: some View {
        NavigationStack {
            Group {
                if !DataScannerViewController.isSupported {
                    ContentUnavailableView(words.text(.scanTitle), systemImage: "qrcode.viewfinder",
                                           description: Text(words.text(.scanUnsupported)))
                } else if camera == .denied {
                    ContentUnavailableView {
                        Label(words.text(.scanTitle), systemImage: "camera")
                    } description: {
                        Text(words.text(.cameraDenied))
                    } actions: {
                        Button(words.text(.openSettings)) {
                            if let url = URL(string: UIApplication.openSettingsURLString) { openURL(url) }
                        }
                    }
                } else if camera == .granted {
                    QRScanner { text in
                        dismiss()
                        found(text)
                    }
                    .ignoresSafeArea(edges: .bottom)
                } else {
                    ProgressView()
                }
            }
            .navigationTitle(words.text(.scanTitle))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button(words.text(.cancel)) { dismiss() }
                }
            }
        }
        .environment(\.layoutDirection, words.isRightToLeft ? .rightToLeft : .leftToRight)
        .task {
            if camera == .notAsked {
                _ = await SystemCamera().request()
            }
            camera = SystemCamera().state()
        }
    }
}

/// VisionKit's scanner, QR codes only; the first one read ends it.
struct QRScanner: UIViewControllerRepresentable {
    let found: (String) -> Void

    func makeCoordinator() -> Coordinator { Coordinator(found: found) }

    func makeUIViewController(context: Context) -> DataScannerViewController {
        let vc = DataScannerViewController(recognizedDataTypes: [.barcode(symbologies: [.qr])],
                                           qualityLevel: .balanced, recognizesMultipleItems: false,
                                           isHighFrameRateTrackingEnabled: false, isHighlightingEnabled: true)
        vc.delegate = context.coordinator
        return vc
    }

    func updateUIViewController(_ vc: DataScannerViewController, context: Context) {
        if !vc.isScanning, !context.coordinator.done {
            DispatchQueue.main.async { try? vc.startScanning() }
        }
    }

    static func dismantleUIViewController(_ vc: DataScannerViewController, coordinator: Coordinator) {
        vc.stopScanning()
    }

    @MainActor final class Coordinator: NSObject, DataScannerViewControllerDelegate {
        let found: (String) -> Void
        var done = false

        init(found: @escaping (String) -> Void) {
            self.found = found
        }

        func dataScanner(_ dataScanner: DataScannerViewController, didAdd addedItems: [RecognizedItem], allItems: [RecognizedItem]) {
            guard !done else { return }
            for item in addedItems {
                if case let .barcode(code) = item, let text = code.payloadStringValue {
                    done = true
                    dataScanner.stopScanning()
                    found(text)
                    return
                }
            }
        }
    }
}
#endif
