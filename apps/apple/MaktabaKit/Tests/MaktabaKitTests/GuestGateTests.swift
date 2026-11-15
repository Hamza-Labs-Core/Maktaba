import Foundation
import Testing
@testable import MaktabaKit

// Story 50.27 on Apple: Watch a broadcast fails closed under Epic 57's parental limits. A guest
// has no account the edge could check, so the entry (the home picker's and Settings' button,
// an invite link) opens only once the limit is known and is none.

@Suite struct GuestGateTests {
    private func defaults() -> UserDefaults {
        UserDefaults(suiteName: "guest-gate-\(UUID().uuidString)")!
    }

    @MainActor private func store(_ source: FakeParentalSource, defaults: UserDefaults, user: String = "u1") -> ParentalStore {
        let s = ParentalStore(every: .seconds(3600), defaults: defaults, sleep: { _ in try await Task.sleep(for: .seconds(3600)) },
                              now: { Date(timeIntervalSince1970: 1_000) })
        s.use(source, isAdmin: false, key: ParentalStore.key(home: URL(string: "http://gate.test")!, user: user))
        return s
    }

    private func gate(_ s: ParentalStore, signedIn: Bool = true, restoring: Bool = false) -> GuestGate {
        MainActor.assumeIsolated {
            GuestGate.decide(signedIn: signedIn, restoring: restoring, confirmed: s.confirmed,
                             limited: s.current.limited, deviceWasLimited: s.deviceWasLimited,
                             predatesLimits: s.predatesLimits)
        }
    }

    /// Signed in, before the home has said: not known, so closed; then the home says there
    /// is no limit: open.
    @MainActor @Test func unknownThenAnsweredAndNotLimited() async {
        let source = FakeParentalSource()
        source.set(ParentalFixtures.unrestricted)
        let s = store(source, defaults: defaults())
        #expect(!s.answered && !s.current.limited, "before the answer the limit reads as none")
        #expect(gate(s) == .checking, "and yet the entry is closed")
        await s.refresh()
        #expect(s.confirmed)
        #expect(gate(s) == .allowed)
    }

    /// A failed first read marks the store answered with no limit; the gate still waits for a
    /// real answer, since "none" is then only the default.
    @MainActor @Test func aFailedReadIsNotAnAnswer() async {
        let source = FakeParentalSource()
        source.fail()
        let s = store(source, defaults: defaults())
        await s.refresh()
        #expect(s.answered && !s.current.limited)
        #expect(!s.confirmed)
        #expect(gate(s) == .checking)
        // a home from before Epic 57 (404) is an answer: no limit there
        source.set(missing: ())
        await s.refresh()
        #expect(s.confirmed && gate(s) == .allowed)
    }

    /// Answered and limited, by the account or by the device: closed.
    @MainActor @Test func answeredAndLimited() async {
        for limit in [ParentalFixtures.child, ParentalFixtures.deviceLimited] {
            let source = FakeParentalSource()
            source.set(limit)
            let s = store(source, defaults: defaults())
            await s.refresh()
            #expect(s.confirmed && s.current.limited)
            #expect(gate(s) == .limited, "\(limit)")
        }
    }

    /// No home signed in: the flag this device kept decides. A device no home ever answered
    /// on may watch (the spec: no home and no account needed); one where a sign-in was limited
    /// may not, until that sign-in's home says it no longer is.
    @MainActor @Test func noHomeUsesTheKeptFlag() async {
        let d = defaults()
        let fresh = ParentalStore(defaults: d)
        #expect(!fresh.deviceWasLimited)
        #expect(gate(fresh, signedIn: false) == .allowed, "nothing ever kept: a guest needs no home")

        let source = FakeParentalSource()
        source.set(ParentalFixtures.child)
        let child = store(source, defaults: d, user: "child")
        await child.refresh()
        child.reset() // signed out
        #expect(child.deviceWasLimited && d.bool(forKey: ParentalStore.deviceKey))
        #expect(gate(child, signedIn: false) == .limited)

        // a parent signs in on the same device: open for them, still closed with no home
        let parentSource = FakeParentalSource()
        parentSource.set(ParentalFixtures.unrestricted)
        let parent = store(parentSource, defaults: d, user: "parent")
        await parent.refresh()
        #expect(gate(parent) == .allowed)
        parent.reset()
        #expect(gate(parent, signedIn: false) == .limited, "the child's sign-in is still limited")

        // the child's limit is lifted: no sign-in on the device is limited any more
        source.set(ParentalFixtures.unrestricted)
        let lifted = store(source, defaults: d, user: "child")
        await lifted.refresh()
        lifted.reset()
        #expect(!d.bool(forKey: ParentalStore.deviceKey))
        #expect(gate(lifted, signedIn: false) == .allowed)

        // restoring the last home: not known yet, whatever was kept
        #expect(gate(lifted, signedIn: false, restoring: true) == .checking)
    }

    /// A 404 after this sign-in was answered limited is not "no limit": a home does not lose
    /// Epic 57, so it is a host standing in for the home (or a proxy). Nothing is cleared,
    /// and the gate waits as after a failed read.
    @MainActor @Test func a404AfterALimitIsNotAnAnswer() async {
        let d = defaults()
        let source = FakeParentalSource()
        source.set(ParentalFixtures.child)
        let s = store(source, defaults: d)
        await s.refresh()
        #expect(gate(s) == .limited)
        s.reset()
        source.set(missing: ())
        let again = store(source, defaults: d)
        await again.refresh()
        #expect(again.answered && !again.confirmed && !again.predatesLimits)
        #expect(gate(again) == .checking)
        #expect(d.bool(forKey: ParentalStore.key(home: URL(string: "http://gate.test")!, user: "u1")), "the sign-in's limit is kept")
        #expect(again.deviceWasLimited)
        // asked again, not given up on as a home from before Epic 57
        source.set(ParentalFixtures.unrestricted)
        await again.refresh()
        #expect(again.confirmed && gate(again) == .allowed)
    }

    /// A home from before Epic 57 says nothing of a limit, so signed in to it the device's
    /// kept flag still counts: a host answering 404 does not open a limited device.
    @MainActor @Test func aHomeFromBeforeLimitsKeepsTheDeviceFlag() async {
        let d = defaults()
        let source = FakeParentalSource()
        source.set(missing: ())
        let s = store(source, defaults: d, user: "other")
        await s.refresh()
        #expect(s.confirmed && s.predatesLimits)
        #expect(gate(s) == .allowed, "nothing kept on this device: open")
        d.set(true, forKey: ParentalStore.deviceKey)
        #expect(gate(s) == .limited, "a sign-in on this device was limited")
        s.reset()
        #expect(!s.predatesLimits)
    }

    /// A device whose sign-in keys predate the device flag: any limited sign-in counts.
    @Test func keysFromBeforeTheDeviceFlagCount() {
        let d = defaults()
        #expect(!ParentalStore.deviceWasLimited(defaults: d))
        d.set(false, forKey: ParentalStore.key(home: URL(string: "http://a.test")!, user: "a"))
        #expect(!ParentalStore.deviceWasLimited(defaults: d))
        d.set(true, forKey: ParentalStore.key(home: URL(string: "http://b.test")!, user: "b"))
        #expect(ParentalStore.deviceWasLimited(defaults: d))
        d.set(false, forKey: ParentalStore.deviceKey)
        #expect(!ParentalStore.deviceWasLimited(defaults: d), "the device flag, once kept, decides")
        // as a launch argument gives it (`-parental.limited.device YES`): a string
        d.set("YES", forKey: ParentalStore.deviceKey)
        #expect(ParentalStore.deviceWasLimited(defaults: d))
    }

    /// A limit turning on while a broadcast plays: the gate closes (the apps close the guest
    /// space on it), and a held invite is dropped.
    @MainActor @Test func aLimitTurningOnMidWatchCloses() async {
        let source = FakeParentalSource()
        source.set(ParentalFixtures.unrestricted)
        let s = store(source, defaults: defaults())
        await s.refresh()
        #expect(gate(s) == .allowed)
        source.set(ParentalFixtures.child)
        await s.refresh()
        #expect(gate(s) == .limited)
        // the device's own limit too
        source.set(ParentalFixtures.unrestricted)
        await s.refresh()
        #expect(gate(s) == .allowed)
        source.set(ParentalFixtures.deviceLimited)
        await s.refresh()
        #expect(gate(s) == .limited)
    }

    /// An invite link: opened when allowed, held while not known (then opened, or dropped
    /// under a limit), refused under a limit; each closed case has words in both languages.
    @Test func invitesAreHeldOrRefused() {
        var held = HeldInvite()
        #expect(held.arrive("mkbn1.a.b", gate: .allowed) == "mkbn1.a.b")
        #expect(held.token == nil)

        #expect(held.arrive("mkbn1.c.d", gate: .checking) == nil)
        #expect(held.token == "mkbn1.c.d")
        #expect(held.gateChanged(.checking) == nil && held.token == "mkbn1.c.d")
        #expect(held.gateChanged(.allowed) == "mkbn1.c.d")
        #expect(held.token == nil && held.gateChanged(.allowed) == nil, "opened once")

        #expect(held.arrive("mkbn1.e.f", gate: .checking) == nil)
        #expect(held.gateChanged(.limited) == nil && held.token == nil, "dropped under a limit")
        #expect(held.gateChanged(.allowed) == nil)

        #expect(held.arrive("mkbn1.g.h", gate: .limited) == nil && held.token == nil, "refused, not kept")

        #expect(GuestGate.allowed.inviteMessage == nil)
        #expect(GuestGate.checking.inviteMessage != nil && GuestGate.limited.inviteMessage != nil)
        let en = Locale(identifier: "en"), ar = Locale(identifier: "ar")
        for key in ["guest.gate.checking", "guest.gate.limited", "guest.gate.closed"] {
            let english = kitWord(key, en), arabic = kitWord(key, ar)
            #expect(english != nil && english != key, "\(key) in English")
            #expect(arabic != nil && arabic != english, "\(key) in Arabic")
        }
    }
}
