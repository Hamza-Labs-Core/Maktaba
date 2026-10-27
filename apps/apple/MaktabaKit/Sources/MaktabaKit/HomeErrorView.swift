import SwiftUI

/// A screen's "this did not work": the kind's symbol, a plain title, one sentence and what
/// can be done, the first action focused (for the Siri remote, a keyboard), with what the
/// home answered behind Details. The player's (`video`) says it about the video and offers
/// `HomeFailure.actions`; a list's offers Try again.
///
/// The buttons take the system's styles; a TV wraps the view in its own button style.
public struct HomeErrorView: View {
    let failure: HomeFailure
    let video: Bool
    let actions: [ErrorAction]
    let words: ErrorStrings
    let perform: (ErrorAction) -> Void

    @State private var showDetails = false
    @FocusState private var focused: ErrorAction?

    public init(_ failure: HomeFailure, video: Bool = true, actions: [ErrorAction]? = nil,
                words: ErrorStrings = .forPreferredLanguages(), perform: @escaping (ErrorAction) -> Void) {
        self.failure = failure
        self.video = video
        self.actions = actions ?? (video ? failure.actions : [.tryAgain])
        self.words = words
        self.perform = perform
    }

    public var body: some View {
        VStack(spacing: Self.spacing) {
            Image(systemName: failure.symbol)
                .font(.system(size: Self.iconSize))
                .foregroundStyle(Color.brass)
                .accessibilityHidden(true)
            Text(words.text(video ? .playerTitle : .title))
                .font(Self.titleFont)
                .multilineTextAlignment(.center)
                .accessibilityIdentifier("error.title")
            Text(words.sentence(failure.kind, video: video))
                .font(Self.bodyFont)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityIdentifier("error.message")
            ViewThatFits(in: .horizontal) {
                HStack(spacing: Self.spacing / 2) { buttons }
                VStack(spacing: Self.spacing / 2) { buttons }
            }
            .padding(.top, Self.spacing / 2)
            if let details = failure.details {
                Button(words.text(.details), systemImage: showDetails ? "chevron.up" : "chevron.down") {
                    showDetails.toggle()
                }
                .font(Self.detailsFont)
                #if !os(tvOS)
                .buttonStyle(.borderless)
                #endif
                .accessibilityIdentifier("error.details")
                if showDetails {
                    detailsText(details)
                }
            }
        }
        .frame(maxWidth: Self.width)
        .padding(Self.spacing)
        .environment(\.layoutDirection, words.isRightToLeft ? .rightToLeft : .leftToRight)
        .defaultFocus($focused, actions.first)
        .onAppear { focused = actions.first }
    }

    private var buttons: some View {
        ForEach(actions, id: \.self) { a in
            button(a, primary: a == actions.first)
        }
    }

    @ViewBuilder private func button(_ a: ErrorAction, primary: Bool) -> some View {
        let b = Button(words.action(a)) { perform(a) }
            .focused($focused, equals: a)
            .accessibilityIdentifier("error.action.\(a)")
        #if os(tvOS)
        b
        #else
        if primary {
            b.buttonStyle(.borderedProminent).controlSize(.large).keyboardShortcut(.defaultAction)
        } else {
            b.buttonStyle(.bordered).controlSize(.large)
        }
        #endif
    }

    /// The home's own words: a technical note, read left to right in either language.
    private func detailsText(_ details: String) -> some View {
        Text(verbatim: details)
            .font(Self.detailsFont.monospaced())
            .foregroundStyle(.secondary)
            .multilineTextAlignment(.leading)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(Self.spacing / 2)
            .background(.quaternary.opacity(0.5), in: .rect(cornerRadius: 8))
            .environment(\.layoutDirection, .leftToRight)
            #if !os(tvOS)
            .textSelection(.enabled)
            #endif
            .accessibilityIdentifier("error.details.text")
    }

    #if os(tvOS)
    static let spacing: CGFloat = 32
    static let iconSize: CGFloat = 88
    static let width: CGFloat = 1200
    static let titleFont = Font.system(size: 52, weight: .bold)
    static let bodyFont = Font.system(size: 34)
    static let detailsFont = Font.system(size: 24)
    #else
    static let spacing: CGFloat = 16
    static let iconSize: CGFloat = 48
    static let width: CGFloat = 520
    static let titleFont = Font.title2.bold()
    static let bodyFont = Font.body
    static let detailsFont = Font.footnote
    #endif
}

#Preview("Not ready") {
    HomeErrorView(HomeFailure(APIError.notReady(#"{"type":"https://maktaba.invalid/problems/rendition-not-found","title":"rendition index not on disk","status":404}"#))) { _ in }
}

#Preview("Arabic, unreachable") {
    HomeErrorView(HomeFailure(kind: .unreachable), words: ErrorStrings(.ar)) { _ in }
}
