import CrashReporter
import Foundation
import MaktabaKit

/// The Apple TV's crash reports (Epic 53 story 53.14): tvOS has no MetricKit, so
/// PLCrashReporter (MIT) catches a crash and keeps it until the next launch, which reads it
/// into a `KeptCrash` beside the other apps' (CrashReports.directory, the last 5) and lets
/// the report go. From there it goes to the home only when the person turned crash reports
/// on; else it stays on the TV, where a problem report can attach it.
enum CrashCapture {
    /// Reads the last launch's crash, if any, then listens for this one's. Once, at launch.
    static func start() {
        // under the debugger the handler would catch the debugger's own signals
        guard !isDebuggerAttached else { return }
        let config = PLCrashReporterConfig(signalHandlerType: .BSD, symbolicationStrategy: [])
        guard let reporter = PLCrashReporter(configuration: config) else { return }
        if reporter.hasPendingCrashReport() {
            if let data = try? reporter.loadPendingCrashReportDataAndReturnError(),
               let report = try? PLCrashReport(data: data) {
                CrashReports.store(crash: kept(report))
            }
            reporter.purgePendingCrashReport()
        }
        do {
            try reporter.enableAndReturnError()
        } catch {
            Log.crash.error("crash reporter not started: \(String(describing: error), privacy: .private)")
        }
    }

    /// The report as the kit's file: the signal or exception, the crashed thread's frames as
    /// binary and offset, each binary's UUID.
    static func kept(_ r: PLCrashReport) -> KeptCrash {
        var type = r.signalInfo?.name ?? "crash"
        var reason = [r.signalInfo?.code].compactMap { $0 }.joined()
        var ips: [UInt64] = []
        if r.hasExceptionInfo, let ex = r.exceptionInfo {
            type = ex.exceptionName ?? type
            reason = ex.exceptionReason ?? reason
            ips = (ex.stackFrames as? [PLCrashReportStackFrameInfo] ?? []).map(\.instructionPointer)
        }
        if ips.isEmpty, let crashed = (r.threads as? [PLCrashReportThreadInfo] ?? []).first(where: \.crashed) {
            ips = (crashed.stackFrames as? [PLCrashReportStackFrameInfo] ?? []).map(\.instructionPointer)
        }
        var images: [String: String] = [:]
        let frames: [KeptCrash.Frame] = ips.map { ip in
            guard let image = r.image(forAddress: ip) else { return KeptCrash.Frame(binary: "?", offset: ip, address: ip) }
            let name = (image.imageName as NSString?)?.lastPathComponent ?? "?"
            if let uuid = image.imageUUID { images[name] = uuid }
            return KeptCrash.Frame(binary: name, offset: ip &- image.imageBaseAddress, address: ip)
        }
        return KeptCrash(at: r.systemInfo?.timestamp ?? Date(), appVersion: r.applicationInfo?.applicationMarketingVersion,
                         build: r.applicationInfo?.applicationVersion, osVersion: r.systemInfo?.operatingSystemVersion,
                         device: r.machineInfo?.modelName, type: type, reason: reason, frames: frames, images: images)
    }

    private static var isDebuggerAttached: Bool {
        var info = kinfo_proc()
        var size = MemoryLayout<kinfo_proc>.stride
        var mib: [Int32] = [CTL_KERN, KERN_PROC, KERN_PROC_PID, getpid()]
        return sysctl(&mib, 4, &info, &size, nil, 0) == 0 && (info.kp_proc.p_flag & P_TRACED) != 0
    }
}
