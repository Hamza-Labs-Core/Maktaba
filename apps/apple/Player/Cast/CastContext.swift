import Foundation
import MaktabaKit

// The Google Cast SDK behind a protocol (story 58.06): the controller and the screens talk to
// `CastContexting`, so the UI tests drive a fake one (`FakeCastContext`) with no SDK and no
// TV, and the Mac (which has no Cast SDK) builds the same sources. The SDK's own adapter is
// `GoogleCastContext` (iOS and iPadOS only).

/// A Cast device the SDK found on the network.
struct CastDevice: Identifiable, Equatable, Sendable {
    let id: String
    let name: String
}

/// One load on the receiver: Maktaba's custom data (the content id is the video's), or, for
/// a home older than the cast grant, the plain signed URL and no custom data.
struct CastLoad: @unchecked Sendable {
    let contentID: String
    let contentURL: URL?
    let title: String
    let startSec: Double
    /// `CastCustomData.build`'s map (nil: an older home's plain load).
    let customData: [String: Any]?
}

/// What the SDK tells the controller.
enum CastEvent: Sendable {
    case devicesChanged
    case connected(CastDevice)
    /// The session ended (the person stopped it, the TV went away).
    case disconnected
    case failedToConnect
    case status(CastReceiverStatus)
    case message(CastMessage)
    /// The receiver turned the load down (`LOAD_FAILED`), with its `customData.error`.
    case loadFailed(String?)
}

@MainActor
protocol CastContexting: AnyObject {
    var events: ((CastEvent) -> Void)? { get set }
    var devices: [CastDevice] { get }
    /// The device of a session that is on (after a lock, a relaunch), nil when none.
    var connectedDevice: CastDevice? { get }
    func startDiscovery()
    func connect(_ device: CastDevice)
    func disconnect()
    func load(_ load: CastLoad)
    func play()
    func pause()
    func seek(to seconds: Double)
    func next()
    func send(_ message: CastMessage)
}

/// The UI tests' Cast context (a Debug build launched with `-maktaba-cast fake`, or
/// `fake-empty` for a network where no TV is found): one TV, "Living Room TV", that plays
/// whatever it is loaded with from its start and moves on a second a second. What it was
/// loaded with is kept for the test state.
@MainActor
final class FakeCastContext: CastContexting {
    var events: ((CastEvent) -> Void)?
    private(set) var devices: [CastDevice] = []
    private(set) var connectedDevice: CastDevice?
    private(set) var lastLoad: CastLoad?
    private(set) var sent: [CastMessage] = []
    private let empty: Bool
    private var status = CastReceiverStatus(state: .idle)
    private var ticking: Task<Void, Never>?

    static let tv = CastDevice(id: "fake-tv", name: "Living Room TV")

    init(empty: Bool) {
        self.empty = empty
    }

    func startDiscovery() {
        Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(300))
            devices = empty ? [] : [Self.tv]
            events?(.devicesChanged)
        }
    }

    func connect(_ device: CastDevice) {
        Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(200))
            connectedDevice = device
            events?(.connected(device))
        }
    }

    func disconnect() {
        ticking?.cancel()
        connectedDevice = nil
        status = CastReceiverStatus(state: .idle)
        events?(.disconnected)
    }

    func load(_ load: CastLoad) {
        lastLoad = load
        let data = load.customData
        status = CastReceiverStatus(state: .playing, position: load.startSec, duration: 3120,
                                    videoID: data?["video_id"] as? String ?? load.contentID,
                                    queueID: data?["queue_id"] as? String, queuePos: data?["queue_pos"] as? Int)
        events?(.status(status))
        ticking?.cancel()
        ticking = Task { @MainActor [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(1))
                guard let self, !Task.isCancelled else { return }
                if self.status.state == .playing { self.status.position += 1 }
                self.events?(.status(self.status))
            }
        }
    }

    func play() { status.state = .playing; events?(.status(status)) }
    func pause() { status.state = .paused; events?(.status(status)) }
    func seek(to seconds: Double) { status.position = max(0, seconds); events?(.status(status)) }
    func next() {}
    func send(_ message: CastMessage) { sent.append(message) }
}
