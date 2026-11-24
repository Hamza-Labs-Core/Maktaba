#if DEBUG && os(iOS)
import Darwin

/// Launched by the UI tests with `-maktaba-console-off`, the app's writes to its standard
/// error never wait: a line the reader has no room for is dropped.
///
/// Xcode launches the app under test with `OS_ACTIVITY_DT_MODE` set, and libtrace then
/// copies every log message the process makes to its standard error, a write that waits
/// until the reader takes it. XCTest answers a test's query inside the app, on its main
/// thread (`XCTElementQueryProcessor`), and logs as it reads each element's attributes. On
/// the Xcode 27 runners' loaded simulators the reader fell behind, and each read waited on
/// that write: the spindumps XCTest took for "Failed to get matching snapshots: Timed out
/// while evaluating UI query" (CI runs 45182333067 and 45189795489, iPhone) had the app's
/// main thread in `writev` under `_os_log_impl_stream` for 312 of 325 samples, queries took
/// 15 to 20 s, and the test timed out. The variable cannot be turned off from the test:
/// libtrace reads it at the process's first log message, whatever its value ("NO" copies
/// too), and `OS_ACTIVITY_MODE=disable` also keeps the messages out of the unified log, which
/// the CI job reads and checks (tools/apple/sim-log.py).
///
/// Standard error stays where Xcode put it, only made non-blocking: libtrace drops a copy
/// that would wait (20,000 messages into a full pipe took 0.03 s, nothing spun), and the
/// unified log keeps every message. #1550 pointed it at /dev/null instead, which let go of
/// Xcode's stream as the app started rather than as it ended; from then on XCTest's
/// "Failed to terminate <app>:0" came in 5 of about 8 full runs, against 1 in the 31 runs
/// before (Trail #1561). A Debug build's only, and only with the flag. The Player's UI-test
/// targets compile this file too.
enum ConsoleHook {
    static let flag = "-maktaba-console-off"

    /// Makes standard error non-blocking when launched with `flag`. Whether it did.
    @discardableResult
    static func start(arguments: [String] = CommandLine.arguments) -> Bool {
        guard arguments.contains(flag) else { return false }
        let flags = fcntl(STDERR_FILENO, F_GETFL)
        guard flags >= 0 else { return false }
        return fcntl(STDERR_FILENO, F_SETFL, flags | O_NONBLOCK) == 0
    }
}
#endif
