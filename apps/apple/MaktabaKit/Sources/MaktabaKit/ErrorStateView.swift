import SwiftUI

extension EnvironmentValues {
    /// The app's session (set by `SessionGate`, and the TV's root): an error screen's
    /// "Sign in again" and "Choose another home" act on it.
    @Entry public var appSession: AppSession?
}

/// A screen that could not load: what went wrong in the person's language (`AppError`),
/// and what they can do about it. Retry and Go back are the screen's to give; Sign in again
/// and Choose another home act on the session in the environment. Every app's screens use
/// this one (the TV styles its buttons from outside, `.buttonStyle`).
public struct ErrorStateView: View {
    let error: AppError
    let retry: (() -> Void)?
    let back: (() -> Void)?
    private let words: ErrorStrings
    @Environment(\.appSession) private var session

    public init(_ error: AppError, words: ErrorStrings = .forPreferredLanguages(),
                retry: (() -> Void)? = nil, back: (() -> Void)? = nil) {
        self.error = error
        self.words = words
        self.retry = retry
        self.back = back
    }

    public var body: some View {
        ContentUnavailableView {
            Label(error.title(words), systemImage: error.systemImage)
        } description: {
            Text(error.message(words))
        } actions: {
            ForEach(Array(offered.enumerated()), id: \.element) { i, action in
                button(action, main: i == 0)
            }
        }
        .accessibilityIdentifier("error.\(error.kind.rawValue)")
        .environment(\.layoutDirection, words.isRightToLeft ? .rightToLeft : .leftToRight)
    }

    /// The error's actions this screen can take (a retry it was given, a session to act on).
    var offered: [AppError.Action] {
        Self.offered(error.actions, retry: retry != nil, back: back != nil, session: session != nil)
    }

    nonisolated static func offered(_ actions: [AppError.Action], retry: Bool, back: Bool, session: Bool) -> [AppError.Action] {
        var out = actions.filter {
            switch $0 {
            case .retry: retry
            case .back: back
            case .signIn, .chooseHome: session
            }
        }
        // a failure with nothing of its own to offer can still be tried again
        if out.isEmpty, retry, !actions.contains(.signIn) { out = [.retry] }
        return out
    }

    @ViewBuilder private func button(_ action: AppError.Action, main: Bool) -> some View {
        let b = Button(words.action(action)) { perform(action) }
            .accessibilityIdentifier("error.action.\(action.rawValue)")
        #if os(tvOS)
        // the TV's own button style, set from outside
        b
        #else
        if main {
            b.buttonStyle(.borderedProminent)
        } else {
            b.buttonStyle(.bordered)
        }
        #endif
    }

    private func perform(_ action: AppError.Action) {
        switch action {
        case .retry: retry?()
        case .back: back?()
        case .signIn: session?.signedOut()
        case .chooseHome: session?.switchHome()
        }
    }
}

/// One line saying what went wrong, for a form or a list (a sign-in footer, an action's
/// failure above a list), with a Try again when given.
public struct ErrorNote: View {
    let error: AppError
    let retry: (() -> Void)?
    private let words: ErrorStrings

    public init(_ error: AppError, words: ErrorStrings = .forPreferredLanguages(), retry: (() -> Void)? = nil) {
        self.error = error
        self.words = words
        self.retry = retry
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label(error.message(words), systemImage: error.systemImage)
                .foregroundStyle(.red)
            if let retry, error.actions.contains(.retry) {
                Button(words.action(.retry), action: retry)
                    .accessibilityIdentifier("error.action.retry")
            }
        }
        .accessibilityIdentifier("error.\(error.kind.rawValue)")
    }
}
