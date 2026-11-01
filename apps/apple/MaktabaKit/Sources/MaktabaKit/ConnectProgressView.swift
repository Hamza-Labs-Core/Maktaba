import SwiftUI

// Story 48.37: how the start screens say what connecting is doing (never a bare spinner).
// The words are MaktabaKit's catalog's (Localizable.xcstrings, English and Arabic).

extension ConnectProgress {
    /// The line that says what is happening now ("Trying http://192.168.1.20:8088 — 1 of 3").
    public func headline(_ locale: Locale = AppLanguage.locale) -> String {
        switch step {
        case .finding:
            kit("Looking for your home…", locale)
        case let .trying(address, index, count):
            kit("Trying \(Self.isolated(address)) — \(index) of \(count)", locale)
        case let .found(name):
            kit("Found \(Self.isolated(name))", locale)
        case let .signingIn(home):
            kit("Signing in to \(Self.isolated(home))…", locale)
        case .awaitingApproval:
            kit("Waiting for approval on another device", locale)
        case let .reconnecting(name):
            kit("Reconnecting to \(Self.isolated(name))…", locale)
        case .homeBusy:
            kit("Your home is busy or still starting", locale)
        case let .failed(reason, _):
            Self.sentence(reason, locale)
        }
    }

    /// The line under it, at `now` (a countdown moves with it), if any.
    public func detail(now: Date, _ locale: Locale = AppLanguage.locale) -> String? {
        switch step {
        case let .awaitingApproval(code, at):
            kit("Code \(Self.isolated(code)) · \(Self.countdown(to: at, now: now)) left", locale)
        case let .homeBusy(at):
            kit("Next try in \(Self.countdown(to: at, now: now))", locale)
        case let .failed(_, tried) where !tried.isEmpty:
            kit("Tried: \(tried.map(Self.isolated).joined(separator: ", "))", locale)
        default:
            nil
        }
    }

    /// "Attempt 2" (nil on the first).
    public func attemptLabel(_ locale: Locale = AppLanguage.locale) -> String? {
        attempt > 1 ? kit("Attempt \(attempt)", locale) : nil
    }

    /// Why it did not work, in one sentence.
    public static func sentence(_ reason: Failure, _ locale: Locale = AppLanguage.locale) -> String {
        switch reason {
        case .unreachable: kit("Your home didn't answer.", locale)
        case .notAHome: kit("That address is not a Maktaba home.", locale)
        case .otherHome: kit("That address answers for a different home.", locale)
        case .wrongPassword: kit("That username or password is not right.", locale)
        case .approvalExpired: kit("The sign-in code expired before it was approved.", locale)
        case .busy: kit("Your home is busy right now.", locale)
        case .server: kit("Something went wrong on your home.", locale)
        }
    }

    /// The step's name, for the screen's accessibility identifier (`connect.progress.trying`).
    public var stepName: String {
        switch step {
        case .finding: "finding"
        case .trying: "trying"
        case .found: "found"
        case .signingIn: "signingIn"
        case .awaitingApproval: "awaitingApproval"
        case .reconnecting: "reconnecting"
        case .homeBusy: "homeBusy"
        case .failed: "failed"
        }
    }

    /// An address, a name or a code kept in its own direction inside an Arabic sentence.
    static func isolated(_ s: String) -> String { "\u{2068}\(s)\u{2069}" }
}

/// One `ConnectProgress` as a start screen shows it: what is happening now, the attempt, a
/// live countdown when there is one, and on failure why, what was tried and Retry.
public struct ConnectProgressView: View {
    let progress: ConnectProgress
    let locale: Locale
    let retry: (() -> Void)?
    let retryID: String

    /// - `retry`: what Retry does (nil: no Retry button, as when the form itself is the retry).
    public init(_ progress: ConnectProgress, locale: Locale = AppLanguage.locale,
                retryID: String = "connect.retry", retry: (() -> Void)? = nil) {
        self.progress = progress
        self.locale = locale
        self.retry = retry
        self.retryID = retryID
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .firstTextBaseline, spacing: 10) {
                indicator
                Text(verbatim: progress.headline(locale))
                    .font(.body.weight(.semibold))
                    .foregroundStyle(failed ? Color.red : Color.primary)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
                if let attempt = progress.attemptLabel(locale) {
                    Text(verbatim: attempt)
                        .font(.caption.weight(.medium))
                        .padding(.horizontal, 8)
                        .padding(.vertical, 2)
                        .background(Capsule().fill(Color.secondary.opacity(0.2)))
                        .accessibilityIdentifier("connect.progress.attempt")
                }
            }
            detail
            if failed, let retry {
                Button(action: retry) {
                    Label(kit("Retry", locale), systemImage: "arrow.clockwise")
                }
                .accessibilityIdentifier(retryID)
            }
        }
        .padding(.vertical, 4)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("connect.progress.\(progress.stepName)")
    }

    private var failed: Bool { !progress.isWorking }

    @ViewBuilder private var indicator: some View {
        switch progress.step {
        case .failed:
            Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.red)
        case .found:
            Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
        case .homeBusy:
            Image(systemName: "clock").foregroundStyle(Color.mktBrassText)
        default:
            // a spinner beside the words, never alone (the words say what it waits for)
            ProgressView().controlSize(.small).accessibilityHidden(true)
        }
    }

    @ViewBuilder private var detail: some View {
        if progress.deadline != nil {
            TimelineView(.periodic(from: .now, by: 1)) { ctx in
                if let line = progress.detail(now: ctx.date, locale) {
                    Text(verbatim: line).font(.footnote.monospacedDigit()).foregroundStyle(.secondary)
                }
            }
        } else if let line = progress.detail(now: .now, locale) {
            Text(verbatim: line)
                .font(.footnote)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }
}

/// A button's or a row's wait: a small spinner beside the words that say what it waits for.
public struct ConnectBusyLabel: View {
    let text: String

    public init(_ text: String) { self.text = text }

    public var body: some View {
        HStack(spacing: 8) {
            ProgressView().controlSize(.small).accessibilityHidden(true)
            Text(verbatim: text)
        }
        .accessibilityElement(children: .combine)
    }
}

extension ConnectProgress {
    /// The words of the start screens' smaller waits (a button that is working).
    public enum Busy: Sendable {
        case lookingUpCode, approving, cloudSignIn

        public func text(_ locale: Locale = AppLanguage.locale) -> String {
            switch self {
            case .lookingUpCode: kit("Looking up the code…", locale)
            case .approving: kit("Approving…", locale)
            case .cloudSignIn: kit("Signing in to Maktaba Cloud…", locale)
            }
        }
    }
}
