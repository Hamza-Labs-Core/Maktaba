#if DEBUG && os(iOS)
import os
import UIKit

/// Tells a UI test when the simulator lost one of its taps (#639).
///
/// XCTest makes a touch device for each synthesized tap. On a loaded CI runner the
/// simulator's `backboardd` sometimes registers that device twice ("already have a main
/// display digitizer" / "unknown digitizer"), the press and the lift land on different
/// copies, and the system cancels the touch. The app never sees the tap end, and the test
/// then fails on whatever the tap should have done. That assertion reads as an app bug.
///
/// Launched with `-maktaba-touch-hook`, the app watches every window's touches with a
/// recognizer that never recognizes and never blocks another (`TouchWatcher`), and counts
/// the touches the system cancelled since launch. A test asks over a Darwin notification
/// (`ask`), and the app answers with the count in the name (`answer(_:)`, one name per
/// count, "more" above `most`). `PlayerUITestCase.record(_:)` asks when a test fails and
/// puts the simulator's lost tap at the front of the failure. Each cancelled touch is also
/// in the app's log (the CI job keeps it). Like `TextSizeHook`: a Debug build's only, and
/// only with the flag. The Player's UI-test targets compile this file too.
enum TouchHook {
    static let flag = "-maktaba-touch-hook"
    static let ask = "com.hamzalabs.maktaba.uitest.touches.ask"
    static let answerPrefix = "com.hamzalabs.maktaba.uitest.touches.cancelled."
    /// The highest count an answer names; above it the answer is "more".
    static let most = 20

    /// The answer that says `cancelled` touches were cancelled since launch.
    static func answer(_ cancelled: Int) -> String {
        answerPrefix + (cancelled > most ? "more" : String(max(cancelled, 0)))
    }

    /// Every answer the app can give (a Darwin observer needs each exact name).
    static var answers: [String] { (0 ... most).map(answer) + [answerPrefix + "more"] }

    /// The count an answer's name says, `most + 1` for "more", nil for another name.
    static func count(in name: String) -> Int? {
        guard name.hasPrefix(answerPrefix) else { return nil }
        let rest = name.dropFirst(answerPrefix.count)
        return rest == "more" ? most + 1 : Int(rest)
    }

    /// The touches the system cancelled since launch.
    @MainActor private(set) static var cancelled = 0

    /// The app's log, as MaktabaKit's `Log.ui` (the UI-test targets compile this file without
    /// MaktabaKit).
    private static let log = Logger(subsystem: Bundle.main.bundleIdentifier ?? "com.hamzalabs.maktaba", category: "ui")

    @MainActor static func noteCancelled(_ touches: Int) {
        cancelled += max(touches, 1)
        log.notice("touch cancelled by the system (\(cancelled, privacy: .public) since launch): a UI test's tap may be lost (#639)")
    }

    /// Watches the touches and answers the tests when launched with `flag`; without it, does
    /// nothing. Whether it watches.
    @discardableResult
    static func start(arguments: [String] = CommandLine.arguments) -> Bool {
        guard arguments.contains(flag) else { return false }
        CFNotificationCenterAddObserver(CFNotificationCenterGetDarwinNotifyCenter(), nil, { _, _, _, _, _ in
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    CFNotificationCenterPostNotification(CFNotificationCenterGetDarwinNotifyCenter(),
                                                         CFNotificationName(TouchHook.answer(TouchHook.cancelled) as CFString),
                                                         nil, nil, true)
                }
            }
        }, ask as CFString, nil, .deliverImmediately)
        NotificationCenter.default.addObserver(forName: UIWindow.didBecomeVisibleNotification, object: nil, queue: .main) { note in
            guard let window = note.object as? UIWindow else { return }
            MainActor.assumeIsolated { watch(window) }
        }
        return true
    }

    /// Adds a `TouchWatcher` to `window` unless it has one. Whether it added one.
    @MainActor @discardableResult
    static func watch(_ window: UIWindow) -> Bool {
        if window.gestureRecognizers?.contains(where: { $0 is TouchWatcher }) == true { return false }
        window.addGestureRecognizer(TouchWatcher())
        return true
    }
}

/// A recognizer that only watches: it never recognizes, never delays or cancels a view's
/// touches, runs beside every other recognizer and cannot be stopped by one, so it sees
/// each touch to its end. A touch the system cancels is counted (`TouchHook`).
final class TouchWatcher: UIGestureRecognizer, UIGestureRecognizerDelegate {
    init() {
        super.init(target: nil, action: nil)
        cancelsTouchesInView = false
        delaysTouchesBegan = false
        delaysTouchesEnded = false
        delegate = self
    }

    override func touchesEnded(_ touches: Set<UITouch>, with event: UIEvent) {
        if Self.allEnded(event, besides: touches) { state = .failed }
    }

    override func touchesCancelled(_ touches: Set<UITouch>, with event: UIEvent) {
        TouchHook.noteCancelled(touches.count)
        state = .failed
    }

    /// Whether no touch of `event` goes on after `ending` (all ended or cancelled).
    static func allEnded(_ event: UIEvent, besides ending: Set<UITouch>) -> Bool {
        (event.allTouches ?? []).allSatisfy { ending.contains($0) || $0.phase == .ended || $0.phase == .cancelled }
    }

    override func canPrevent(_ preventedGestureRecognizer: UIGestureRecognizer) -> Bool { false }
    override func canBePrevented(by preventingGestureRecognizer: UIGestureRecognizer) -> Bool { false }

    func gestureRecognizer(_ gestureRecognizer: UIGestureRecognizer,
                           shouldRecognizeSimultaneouslyWith other: UIGestureRecognizer) -> Bool { true }
}
#endif
