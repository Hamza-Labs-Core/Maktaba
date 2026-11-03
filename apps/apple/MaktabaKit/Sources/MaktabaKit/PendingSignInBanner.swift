import SwiftUI

/// The in-app banner for a device waiting for an approval (`PendingSignInsWatcher.banner`):
/// what is asking, Enter code (opens Approve a TV or device) and Dismiss.
public struct PendingSignInBanner: View {
    let request: PendingSignIn
    let enterCode: () -> Void
    let dismiss: () -> Void
    private let words = ConnectStrings.forPreferredLanguages()

    public init(request: PendingSignIn, enterCode: @escaping () -> Void, dismiss: @escaping () -> Void) {
        self.request = request
        self.enterCode = enterCode
        self.dismiss = dismiss
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Label(words.banner(request), systemImage: request.isTV ? "tv" : "laptopcomputer.and.iphone")
                .font(.callout)
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: .infinity, alignment: .leading)
                .accessibilityIdentifier("pending.text")
            HStack(spacing: 12) {
                Button(words.text(.enterCode), action: enterCode)
                    .buttonStyle(.borderedProminent)
                    .accessibilityIdentifier("pending.enterCode")
                Button(words.text(.dismiss), action: dismiss)
                    .buttonStyle(.bordered)
                    .accessibilityIdentifier("pending.dismiss")
            }
        }
        .padding(14)
        .background(.regularMaterial, in: .rect(cornerRadius: 14))
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .environment(\.layoutDirection, words.isRightToLeft ? .rightToLeft : .leftToRight)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("pending.banner")
    }
}
