#if DEBUG && os(iOS)
import Darwin

/// Launched by the UI tests with `-maktaba-console-off`, the app writes nothing to its
/// standard error: it goes to /dev/null from the app's first line.
///
/// Xcode launches the app under test with `OS_ACTIVITY_DT_MODE` set, and libtrace then
/// copies every log message the process makes to its standard error, a write that waits
/// until the reader takes it. XCTest answers a test's query inside the app, on its main
/// thread (`XCTElementQueryProcessor`), and logs as it reads each element's attributes. On
/// the Xcode 27 runners' loaded simulators the reader fell behind, and each read waited on
/// that write: the spindumps XCTest took for
/// "Failed to get matching snapshots: Timed out while evaluating UI query" (CI runs
/// 45182333067 and 45189795489, iPhone) had the app's main thread in `writev` under
/// `_os_log_impl_stream` for 312 of 325 samples, queries took 15 to 20 s, and the test timed
/// out. The variable cannot be turned off from the test: libtrace reads it at the process's
/// first log message, whatever its value ("NO" copies too), and `OS_ACTIVITY_MODE=disable`
/// also keeps the messages out of the unified log, which the CI job reads and checks
/// (tools/apple/sim-log.py). /dev/null takes each write at once, and the unified log keeps
/// every message. A Debug build's only, and only with the flag. The Player's UI-test targets
/// compile this file too.
enum ConsoleHook {
    static let flag = "-maktaba-console-off"

    /// Points standard error at /dev/null when launched with `flag`. Whether it did.
    @discardableResult
    static func start(arguments: [String] = CommandLine.arguments) -> Bool {
        guard arguments.contains(flag) else { return false }
        let null = open("/dev/null", O_WRONLY)
        guard null >= 0 else { return false }
        defer { close(null) }
        return dup2(null, STDERR_FILENO) == STDERR_FILENO
    }
}
#endif
