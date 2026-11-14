#if canImport(GoogleCast) && os(iOS)
import Foundation
@preconcurrency import GoogleCast
import MaktabaKit

/// The Google Cast iOS SDK as a `CastContexting` (story 58.06). Set up once with Maktaba's
/// receiver id (the SDK takes it at setup only, so an older home's cast goes to the same
/// receiver as a plain load). Made only on an iPhone or iPad whose build names a receiver
/// (`CastConfig.isEnabled`): an empty id never starts the SDK. Discovery starts after the
/// local network screen (`CastController.interstitialContinued`), never at launch.
@MainActor
final class GoogleCastContext: NSObject, CastContexting {
    var events: ((CastEvent) -> Void)?
    private(set) var devices: [CastDevice] = []
    private var found: [String: GCKDevice] = [:]
    private var channel: GCKGenericChannel?
    private weak var media: GCKRemoteMediaClient?

    private var cast: GCKCastContext { GCKCastContext.sharedInstance() }

    static func make(appID: String) -> GoogleCastContext {
        if !GCKCastContext.isSharedInstanceInitialized() {
            let options = GCKCastOptions(discoveryCriteria: GCKDiscoveryCriteria(applicationID: appID))
            options.physicalVolumeButtonsWillControlDeviceVolume = true
            // the Player's own picker starts discovery, after the local network screen
            options.startDiscoveryAfterFirstTapOnCastButton = true
            GCKCastContext.setSharedInstanceWith(options)
            GCKLogger.sharedInstance().delegate = nil
        }
        let c = GoogleCastContext()
        c.cast.sessionManager.add(c)
        c.cast.discoveryManager.add(c)
        if let s = c.cast.sessionManager.currentCastSession { c.attach(s) }
        return c
    }

    var connectedDevice: CastDevice? {
        cast.sessionManager.currentCastSession.map { Self.device($0.device) }
    }

    func startDiscovery() {
        let d = cast.discoveryManager
        if !d.discoveryActive { d.startDiscovery() }
        listChanged()
    }

    func connect(_ device: CastDevice) {
        guard let d = found[device.id], cast.sessionManager.startSession(with: d) else {
            events?(.failedToConnect)
            return
        }
    }

    func disconnect() {
        cast.sessionManager.endSessionAndStopCasting(true)
    }

    func load(_ load: CastLoad) {
        guard let media else { events?(.loadFailed(nil)); return }
        let b: GCKMediaInformationBuilder
        if let url = load.contentURL {
            b = GCKMediaInformationBuilder(contentURL: url)
        } else {
            b = GCKMediaInformationBuilder()
            b.contentID = load.contentID
        }
        b.streamType = .buffered
        let meta = GCKMediaMetadata(metadataType: .movie)
        meta.setString(load.title, forKey: kGCKMetadataKeyTitle)
        b.metadata = meta
        // the grant and the home's address: in the custom data only, never a header
        b.customData = load.customData
        let r = GCKMediaLoadRequestDataBuilder()
        r.mediaInformation = b.build()
        r.autoplay = true
        r.startTime = load.startSec
        let request = media.loadMedia(with: r.build())
        request.delegate = self
    }

    func play() { media?.play() }
    func pause() { media?.pause() }

    func seek(to seconds: Double) {
        let o = GCKMediaSeekOptions()
        o.interval = max(0, seconds)
        o.relative = false
        media?.seek(with: o)
    }

    func next() { media?.queueNextItem() }

    func send(_ message: CastMessage) {
        var error: GCKError?
        channel?.sendTextMessage(message.text, error: &error)
        if let error { Log.failure("cast: send on the namespace", error) }
    }

    private func attach(_ session: GCKCastSession) {
        let ch = GCKGenericChannel(namespace: CastConfig.namespace)
        ch.delegate = self
        session.add(ch)
        channel = ch
        media = session.remoteMediaClient
        session.remoteMediaClient?.add(self)
    }

    private func listChanged() {
        let d = cast.discoveryManager
        var list: [CastDevice] = []
        var map: [String: GCKDevice] = [:]
        for i in 0 ..< d.deviceCount {
            let g = d.device(at: i)
            map[g.deviceID] = g
            list.append(Self.device(g))
        }
        found = map
        devices = list
        events?(.devicesChanged)
    }

    private static func device(_ g: GCKDevice) -> CastDevice {
        CastDevice(id: g.deviceID, name: g.friendlyName ?? g.modelName ?? g.deviceID)
    }

    private static func state(_ s: GCKMediaPlayerState) -> CastReceiverStatus.PlayerState {
        switch s {
        case .playing: .playing
        case .paused: .paused
        case .buffering: .buffering
        case .loading: .loading
        default: .idle
        }
    }
}

extension GoogleCastContext: @preconcurrency GCKDiscoveryManagerListener {
    func didUpdateDeviceList() { listChanged() }
}

extension GoogleCastContext: @preconcurrency GCKSessionManagerListener {
    func sessionManager(_ sessionManager: GCKSessionManager, didStart session: GCKCastSession) {
        attach(session)
        events?(.connected(Self.device(session.device)))
    }

    func sessionManager(_ sessionManager: GCKSessionManager, didResumeCastSession session: GCKCastSession) {
        attach(session)
        events?(.connected(Self.device(session.device)))
    }

    func sessionManager(_ sessionManager: GCKSessionManager, didFailToStart session: GCKCastSession, withError error: Error) {
        Log.failure("cast: start the session", error)
        events?(.failedToConnect)
    }

    func sessionManager(_ sessionManager: GCKSessionManager, didEnd session: GCKCastSession, withError error: Error?) {
        channel = nil
        media = nil
        events?(.disconnected)
    }
}

extension GoogleCastContext: @preconcurrency GCKRemoteMediaClientListener {
    func remoteMediaClient(_ client: GCKRemoteMediaClient, didUpdate mediaStatus: GCKMediaStatus?) {
        guard let s = mediaStatus else { return }
        events?(.status(CastReceiverStatus(state: Self.state(s.playerState), position: s.streamPosition,
                                           duration: s.mediaInformation?.streamDuration ?? 0,
                                           customData: (s.customData ?? s.mediaInformation?.customData) as? [String: Any])))
    }
}

extension GoogleCastContext: @preconcurrency GCKRequestDelegate {
    func request(_ request: GCKRequest, didFailWithError error: GCKError) {
        Log.failure("cast: load", error)
        let data = error.userInfo[kGCKErrorCustomDataKey] as? [String: Any]
        events?(.loadFailed(data?["error"] as? String))
    }
}

extension GoogleCastContext: @preconcurrency GCKGenericChannelDelegate {
    func cast(_ channel: GCKGenericChannel, didReceiveTextMessage message: String, withNamespace protocolNamespace: String) {
        if let m = CastMessage(text: message) { events?(.message(m)) }
    }
}
#endif
