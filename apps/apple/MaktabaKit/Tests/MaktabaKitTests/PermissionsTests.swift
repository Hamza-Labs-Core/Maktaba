import Foundation
import Testing
@testable import MaktabaKit

@Suite struct LocalNetworkVerdictRules {
    let denied = LocalNetworkVerdict.policyDenied

    @Test func aListedHomeIsAllowed() {
        var v = LocalNetworkVerdict()
        #expect(v.feed(.results(1), at: .zero) == .granted)
        #expect(v.isFinal)
    }

    @Test func policyDeniedIsNotAllowed() {
        var v = LocalNetworkVerdict()
        #expect(v.feed(.waiting(dnsError: denied), at: .milliseconds(10)) == .denied)
        #expect(LocalNetworkVerdict.policyDenied == -65570)
    }

    @Test func readyWithoutARefusalIsAllowedAfterTheGrace() {
        var v = LocalNetworkVerdict(grace: .seconds(2), timeout: .seconds(6))
        #expect(v.feed(.ready, at: .milliseconds(100)) == nil)
        #expect(v.tick(at: .seconds(1)) == nil)
        #expect(v.tick(at: .milliseconds(2100)) == .granted)
    }

    @Test func aRefusalAfterReadyWins() {
        var v = LocalNetworkVerdict(grace: .seconds(2), timeout: .seconds(6))
        v.feed(.ready, at: .zero)
        #expect(v.feed(.waiting(dnsError: denied), at: .seconds(1)) == .denied)
        #expect(v.tick(at: .seconds(4)) == .denied)
    }

    @Test func anEmptyResultSetIsNotAnAnswer() {
        var v = LocalNetworkVerdict()
        #expect(v.feed(.results(0), at: .zero) == nil)
    }

    @Test func anotherErrorCantTell() {
        var v = LocalNetworkVerdict()
        #expect(v.feed(.waiting(dnsError: -65555), at: .zero) == .unknown) // NoAuth: no NSBonjourServices
        var w = LocalNetworkVerdict()
        #expect(w.feed(.failed(dnsError: nil), at: .zero) == .unknown)
        var f = LocalNetworkVerdict()
        #expect(f.feed(.failed(dnsError: denied), at: .zero) == .denied)
    }

    @Test func silenceTimesOut() {
        var v = LocalNetworkVerdict(grace: .seconds(2), timeout: .seconds(6))
        #expect(v.tick(at: .seconds(5)) == nil)
        #expect(v.tick(at: .seconds(6)) == .unknown)
    }

    @Test func theEndOfABrowseDecides() {
        var ready = LocalNetworkVerdict()
        ready.feed(.ready, at: .zero)
        #expect(ready.feed(.ended, at: .milliseconds(10)) == .granted)
        var nothing = LocalNetworkVerdict()
        #expect(nothing.feed(.ended, at: .zero) == .unknown)
    }

    /// iOS refuses the browse while its question is up, then retries it once allowed.
    @Test func allowingAfterTheQuestionTurnsARefusalAround() {
        var v = LocalNetworkVerdict()
        #expect(v.feed(.waiting(dnsError: denied), at: .zero) == .denied)
        v.feed(.ready, at: .seconds(3))
        #expect(v.feed(.results(2), at: .seconds(3)) == .granted)
    }
}

/// A probe that counts its browses.
final class CountingProbe: LocalNetworkProbe, @unchecked Sendable {
    var script: [BrowseSignal]
    private(set) var browses = 0
    init(_ script: [BrowseSignal]) { self.script = script }
    func signals() -> AsyncStream<BrowseSignal> {
        browses += 1
        return ScriptedProbe(script).signals()
    }
}

@MainActor @Suite struct WalkthroughModel {
    let quick = PermissionsModel.Timing(check: LocalNetworkVerdict(grace: .milliseconds(200), timeout: .seconds(1)), request: .seconds(1))
    let deniedScript: [BrowseSignal] = [.waiting(dnsError: LocalNetworkVerdict.policyDenied)]
    let grantedScript: [BrowseSignal] = [.ready, .results(1)]

    func freshDefaults() -> UserDefaults {
        let name = "permissions-test-\(UUID().uuidString)"
        let d = UserDefaults(suiteName: name)!
        d.removePersistentDomain(forName: name)
        return d
    }

    @Test func theFirstRunShowsItWithoutBrowsing() async {
        let probe = CountingProbe(grantedScript)
        let m = PermissionsModel(defaults: freshDefaults(), probe: probe, timing: quick)
        await m.refresh()
        #expect(m.state(of: .localNetwork) == .notAsked)
        #expect(m.isPresented)
        #expect(probe.browses == 0) // the system's question waits for the walkthrough
    }

    @Test func allowingThenClosingIsNotShownAgain() async {
        let d = freshDefaults()
        let m = PermissionsModel(defaults: d, probe: CountingProbe(grantedScript), timing: quick)
        await m.refresh()
        await m.request(.localNetwork)
        #expect(m.state(of: .localNetwork) == .granted)
        #expect(!m.requiredMissing)
        m.dismiss()
        #expect(!m.isPresented && m.seen)

        let next = PermissionsModel(defaults: d, probe: CountingProbe(grantedScript), timing: quick)
        await next.refresh()
        #expect(next.state(of: .localNetwork) == .granted)
        #expect(!next.isPresented)
    }

    @Test func aRefusalShowsItAgainOnTheNextRun() async {
        let d = freshDefaults()
        let m = PermissionsModel(defaults: d, probe: CountingProbe(deniedScript), timing: quick)
        await m.refresh()
        await m.request(.localNetwork)
        #expect(m.state(of: .localNetwork) == .denied)
        #expect(m.requiredMissing)
        m.dismiss()
        // closed this run: coming back to the foreground does not show it again
        await m.refresh()
        #expect(!m.isPresented)

        let next = PermissionsModel(defaults: d, probe: CountingProbe(deniedScript), timing: quick)
        await next.refresh()
        #expect(next.state(of: .localNetwork) == .denied)
        #expect(next.isPresented)
    }

    @Test func turningItOffInSettingsShowsItAgainAtOnce() async {
        let d = freshDefaults()
        let probe = CountingProbe(grantedScript)
        let m = PermissionsModel(defaults: d, probe: probe, timing: quick)
        await m.request(.localNetwork)
        m.dismiss()
        await m.refresh()
        #expect(!m.isPresented)
        probe.script = deniedScript // the person turned it off and came back
        await m.refresh()
        #expect(m.state(of: .localNetwork) == .denied)
        #expect(m.isPresented)
    }

    @Test func allowingInSettingsIsSeenOnReturn() async {
        let d = freshDefaults()
        let probe = CountingProbe(deniedScript)
        let m = PermissionsModel(defaults: d, probe: probe, timing: quick)
        await m.request(.localNetwork)
        #expect(m.state(of: .localNetwork) == .denied)
        probe.script = grantedScript
        await m.refresh()
        #expect(m.state(of: .localNetwork) == .granted)
    }

    @Test func onlyTheFirstRequestWaitsForTheSystemsQuestion() async {
        let d = freshDefaults()
        let m = PermissionsModel(defaults: d, probe: CountingProbe(deniedScript), timing: quick)
        var start = ContinuousClock.now
        await m.request(.localNetwork) // a refusal may be the system's while it asks: followed
        #expect(ContinuousClock.now - start >= .seconds(1))
        #expect(m.state(of: .localNetwork) == .denied)
        start = ContinuousClock.now
        await m.request(.localNetwork) // asked before: the answer at once
        #expect(ContinuousClock.now - start < .milliseconds(800))
        #expect(m.state(of: .localNetwork) == .denied)
    }

    @Test func aCheckThatCantTellSaysSo() async {
        let d = freshDefaults()
        d.set(true, forKey: PermissionsModel.askedKey)
        let m = PermissionsModel(defaults: d, probe: ScriptedProbe([.failed(dnsError: nil)], staysOpen: false), timing: quick)
        await m.refresh()
        #expect(m.state(of: .localNetwork) == .unknown)
    }

    @Test func aSilentBrowseTimesOut() async {
        let d = freshDefaults()
        d.set(true, forKey: PermissionsModel.askedKey)
        let m = PermissionsModel(defaults: d, probe: ScriptedProbe([]), timing: quick)
        await m.refresh()
        #expect(m.state(of: .localNetwork) == .unknown)
        #expect(m.busy.isEmpty)
    }

    @Test func nothingToAskIsNeverShown() async {
        let m = PermissionsModel(kinds: [], defaults: freshDefaults(), probe: CountingProbe(deniedScript), timing: quick)
        await m.refresh()
        #expect(!m.isPresented)
    }

    @Test func theFakeScriptsTheUITestsUse() {
        #expect(ScriptedProbe(named: "granted") != nil)
        #expect(ScriptedProbe(named: "denied")?.script == deniedScript)
        #expect(ScriptedProbe(named: "unknown") != nil)
        #expect(ScriptedProbe(named: "maybe") == nil)
    }

    @Test func thisPlatformAsksForLocalNetwork() {
        #if os(tvOS)
        #expect(PermissionKind.forThisPlatform.isEmpty)
        #else
        #expect(PermissionKind.forThisPlatform == [.localNetwork])
        #expect(PermissionKind.localNetwork.isRequired)
        #endif
    }

    @Test func settingsOpenTheLocalNetworkPane() {
        #if os(macOS)
        #expect(PermissionKind.localNetwork.settingsURL?.absoluteString == "x-apple.systempreferences:com.apple.preference.security?Privacy_LocalNetwork")
        #elseif os(iOS)
        #expect(PermissionKind.localNetwork.settingsURL?.scheme == "app-settings")
        #endif
    }
}
