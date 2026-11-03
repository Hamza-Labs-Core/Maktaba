import Foundation
import Testing
@testable import MaktabaKit

// Approving a TV from the phone (Epic 42 story 42.6): the list of devices waiting
// (`GET /api/pairing/pending`), the banner it makes, the words on both screens, and the
// device's poll hearing that a phone read its code (`looked_up`).

/// The words: the approve button is named the same everywhere, and every key has both languages.
@Suite struct ApproveWords {
    @Test func theCodeScreenNamesTheApproveButtonByItsOwnWords() {
        for lang in [PermissionsStrings.Language.en, .ar] {
            let w = ConnectStrings(lang)
            let menu = w.text(.approveMenu)
            #expect(w.codeIntro.components(separatedBy: menu).count == 3, "\(lang): both apps' buttons named")
            #expect(!w.codeIntro.contains("%@"))
        }
        #expect(ConnectStrings(.en).codeIntro == "To sign in, approve this device from your phone: in Maktaba Remote tap “Approve a TV or device” at the top, or in Maktaba Player open Profile and tap “Approve a TV or device”. On an older app it is “Approve a sign-in” under the person icon. Type this code, or scan the QR code.")
        #expect(ConnectStrings(.ar).codeIntro.contains("«الموافقة على تلفاز أو جهاز»"))
        #expect(ConnectStrings(.ar).codeIntro.contains("«الموافقة على تسجيل دخول»"))
    }

    @Test func theFinalWording() {
        let en = ConnectStrings(.en), ar = ConnectStrings(.ar)
        #expect(en.text(.approveMenu) == "Approve a TV or device")
        #expect(ar.text(.approveMenu) == "الموافقة على تلفاز أو جهاز")
        #expect(en.text(.approveTitle) == en.text(.approveMenu) && ar.text(.approveTitle) == ar.text(.approveMenu))
        #expect(en.text(.approveIntro) == "Type the 8-character code your TV shows, or scan its QR code.")
        #expect(ar.text(.approveIntro) == "اكتب الرمز المكوّن من 8 خانات الذي يعرضه التلفاز، أو امسح رمز QR الخاص به.")
        #expect(en.text(.confirmSignIn, "Living room TV", "mahmoud") == "Sign in Living room TV as mahmoud?")
        #expect(ar.text(.confirmSignIn, "Living room TV", "mahmoud") == "تسجيل دخول Living room TV باسم mahmoud؟")
        #expect(en.text(.approved, "Living room TV") == "Living room TV is signed in.")
        #expect(ar.text(.approved, "Living room TV") == "تم تسجيل دخول Living room TV.")
        #expect(en.text(.expired) == "That code has expired or is wrong. Ask the TV for a new one.")
        #expect(en.text(.notFound) == en.text(.expired) && ar.text(.notFound) == ar.text(.expired))
        #expect(ar.text(.expired) == "انتهت صلاحية هذا الرمز أو أنه غير صحيح. اطلب رمزًا جديدًا من التلفاز.")
        #expect(en.text(.approveFromHome) == "Approve from home: connect to your home's network first.")
        #expect(ar.text(.approveFromHome) == "وافق من المنزل: اتصل بشبكة منزلك أولًا.")
        #expect(en.text(.lookedUp) == "Your phone found this device — tap Approve on it.")
        #expect(ar.text(.lookedUp) == "عثر هاتفك على هذا الجهاز — اضغط «موافقة» عليه.")
        #expect(en.text(.newCode) == "Get a new code" && ar.text(.newCode) == "احصل على رمز جديد")
        #expect(en.text(.enterCode) == "Enter code" && ar.text(.enterCode) == "أدخل الرمز")
        #expect(en.text(.dismiss) == "Dismiss" && ar.text(.dismiss) == "إخفاء")
    }

    @Test func everyNewKeyHasBothLanguages() {
        let keys: [ConnectStrings.Key] = [.lookedUp, .newCode, .confirmSignIn, .approveFromHome, .bannerTV, .bannerTVUnnamed,
                                          .bannerDevice, .bannerDeviceUnnamed, .enterCode, .dismiss]
        for key in keys {
            #expect(ConnectStrings.en[key] != nil, "en \(key)")
            #expect(ConnectStrings.ar[key] != nil, "ar \(key)")
            #expect(ConnectStrings.en[key] != ConnectStrings.ar[key], "\(key) translated")
        }
    }

    @Test func theBannerSaysATVOrADeviceByItsName() {
        let en = ConnectStrings(.en), ar = ConnectStrings(.ar)
        let tv = PendingSignIn(id: "1", clientApp: "tv", platform: "android", device: "tv", deviceLabel: "Living room TV")
        #expect(en.banner(tv) == "A TV is asking to sign in: Living room TV. Approve it by typing the code it shows.")
        #expect(ar.banner(tv) == "تلفاز يطلب تسجيل الدخول: Living room TV. وافق عليه بكتابة الرمز الذي يعرضه.")
        // only an old TV says nothing of itself: a TV, unnamed
        let old = PendingSignIn(id: "2")
        #expect(old.isTV)
        #expect(en.banner(old) == "A TV is asking to sign in. Approve it by typing the code it shows.")
        #expect(ar.banner(old) == "تلفاز يطلب تسجيل الدخول. وافق عليه بكتابة الرمز الذي يعرضه.")
        // no label: the app's name
        let unlabeled = PendingSignIn(id: "3", clientApp: "tv")
        #expect(en.banner(unlabeled) == "A TV is asking to sign in: Maktaba TV. Approve it by typing the code it shows.")
        // a phone signing in by code is a device
        let phone = PendingSignIn(id: "4", clientApp: "player", platform: "ios", device: "phone", deviceLabel: "Sara's iPhone")
        #expect(!phone.isTV)
        #expect(en.banner(phone) == "A device is asking to sign in: Sara's iPhone. Approve it by typing the code it shows.")
        #expect(ar.banner(phone) == "جهاز يطلب تسجيل الدخول: Sara's iPhone. وافق عليه بكتابة الرمز الذي يعرضه.")
        let nameless = PendingSignIn(id: "5", device: "phone")
        #expect(en.banner(nameless) == "A device is asking to sign in. Approve it by typing the code it shows.")
        #expect(ar.banner(nameless) == "جهاز يطلب تسجيل الدخول. وافق عليه بكتابة الرمز الذي يعرضه.")
        // the Remote on a TV is a TV
        #expect(PendingSignIn(id: "6", clientApp: "remote", device: "tv").isTV)
    }
}

/// The banner: the newest request not dismissed; a dismissal lasts until a new id arrives.
@MainActor @Suite struct PendingBanner {
    let a = PendingSignIn(id: "a", clientApp: "tv", deviceLabel: "Den")
    let b = PendingSignIn(id: "b", clientApp: "tv", deviceLabel: "Kitchen")

    @Test func showsTheNewest() {
        let w = PendingSignInsWatcher()
        #expect(w.banner == nil)
        w.update([b, a])
        #expect(w.banner == b)
    }

    @Test func aDismissalLastsUntilANewIdArrives() {
        let w = PendingSignInsWatcher()
        w.update([a])
        w.dismiss()
        #expect(w.banner == nil)
        w.update([a])
        #expect(w.banner == nil, "the same request, still dismissed")
        w.update([b, a])
        #expect(w.banner == b, "a new one shows")
        w.dismiss()
        w.update([a])
        #expect(w.banner == nil, "the older one stays dismissed")
    }

    @Test func noneWaitingHidesIt() {
        let w = PendingSignInsWatcher()
        w.update([a])
        #expect(w.banner == a)
        w.update([])
        #expect(w.banner == nil && w.requests.isEmpty)
    }
}

extension AgainstAStubHome {
    @Suite struct PendingList {
        let home = URL(string: "http://pending.test:8088")!

        func client() -> APIClient {
            APIClient(home: home, tokens: TokenStore(app: "test.pending", home: home), session: RouteStub.session())
        }

        static let list = #"{"count":2,"requests":[{"id":"r2","client_app":"tv","platform":"android","device":"tv","device_label":"Living room TV","created_at":"2026-11-03T10:00:00Z","expires_at":"2026-11-03T10:10:00Z"},{"id":"r1","client_app":"","platform":"","device":"","device_label":"","created_at":"2026-11-03T09:59:00Z","expires_at":"2026-11-03T10:09:00Z"}]}"#

        @Test func readsTheList() async throws {
            RouteStub.reset()
            RouteStub.on("GET pending.test/api/pairing/pending", .json(200, Self.list))
            let list = try #require(try await client().pendingSignIns())
            #expect(list.map(\.id) == ["r2", "r1"])
            let tv = list[0]
            #expect(tv.clientApp == "tv" && tv.platform == "android" && tv.device == "tv" && tv.deviceLabel == "Living room TV")
            #expect(tv.createdAt == ISO8601DateFormatter().date(from: "2026-11-03T10:00:00Z"))
            #expect(tv.expiresAt == ISO8601DateFormatter().date(from: "2026-11-03T10:10:00Z"))
            // empty fields are none: an old TV
            #expect(list[1].clientApp == nil && list[1].deviceLabel == nil && list[1].isTV)
        }

        @Test func anOlderHomeHasNone() async throws {
            RouteStub.reset()
            RouteStub.on("GET pending.test/api/pairing/pending", .text(404, "404 page not found"))
            #expect(try await client().pendingSignIns() == nil)
            RouteStub.reset()
            RouteStub.on("GET pending.test/api/pairing/pending", .text(405, ""))
            #expect(try await client().pendingSignIns() == nil)
        }

        @MainActor @Test func theWatcherStopsAskingAnOlderHome() async {
            RouteStub.reset()
            RouteStub.on("GET pending.test/api/pairing/pending", .text(404, "404 page not found"))
            let w = PendingSignInsWatcher()
            await w.run(client(), sleep: { _ in })
            #expect(RouteStub.seen.count == 1)
        }

        @MainActor @Test func theWatcherReadsUntilStopped() async {
            RouteStub.reset()
            RouteStub.on("GET pending.test/api/pairing/pending", .json(200, Self.list), .json(200, #"{"count":0,"requests":[]}"#))
            let w = PendingSignInsWatcher()
            await w.refresh(client())
            #expect(w.banner?.id == "r2")
            await w.refresh(client())
            #expect(w.banner == nil)
        }
    }

    /// The device's poll hears that a phone read its code.
    @MainActor @Suite struct LookedUp {
        let home = URL(string: "http://looked.test:8088")!
        static let pairing1 = #"{"code":"AAAA-1111","device_code":"dc1","expires_at":"2099-01-01T00:00:00Z","verify_path":"/pair?code=AAAA-1111","interval":2}"#
        static let pairing2 = #"{"code":"BBBB-2222","device_code":"dc2","expires_at":"2099-01-01T00:00:00Z","verify_path":"/pair?code=BBBB-2222","interval":2}"#
        let pairing = try! JSONDecoder().decode(DevicePairing.self, from: Data(pairing1.utf8))

        @Test func thePollSaysLookedUp() async throws {
            RouteStub.reset()
            RouteStub.on("POST looked.test/api/pairing/device/token",
                         .json(202, #"{"status":"pending","interval":2,"looked_up":false}"#),
                         .json(202, #"{"status":"pending","interval":2,"looked_up":true}"#),
                         .json(202, #"{"status":"pending"}"#))
            let c = APIClient(home: home, tokens: TokenStore(app: "test.looked", home: home), session: RouteStub.session())
            #expect(try await c.collectDevicePairing(pairing, label: "TV") == .waiting)
            #expect(try await c.collectDevicePairing(pairing, label: "TV") == .lookedUp)
            // an older home says nothing of it
            #expect(try await c.collectDevicePairing(pairing, label: "TV") == .waiting)
        }

        @Test func theCodeFlowShowsIt() async throws {
            RouteStub.reset()
            RouteStub.on("POST looked.test/api/pairing/device", .json(201, Self.pairing1))
            RouteStub.on("POST looked.test/api/pairing/device/token",
                         .json(202, #"{"status":"pending"}"#), .json(202, #"{"status":"pending","looked_up":true}"#),
                         .json(200, #"{"access_token":"a.b.c","refresh_token":"r"}"#))
            let tokens = TokenStore(app: "test.looked.flow", home: home)
            tokens.clear()
            defer { tokens.clear() }
            let f = CodeSignIn(client: APIClient(home: home, tokens: tokens, session: RouteStub.session()), label: "TV",
                               finish: {}, sleep: { _ in })
            var seen: [Bool] = []
            f.progressObserver = { _ in seen.append(f.lookedUp) }
            await f.run()
            #expect(f.status == .approved && f.lookedUp)
            #expect(seen.first == false, "not looked up while the code is new")
        }

        @MainActor final class Box {
            var flow: CodeSignIn?
            var asked = false
            var steps: [ConnectProgress.Step] = []
        }

        @Test func aNewCodeOnRequest() async throws {
            RouteStub.reset()
            RouteStub.on("POST looked.test/api/pairing/device", .json(201, Self.pairing1), .json(201, Self.pairing2))
            RouteStub.on("POST looked.test/api/pairing/device/token", .json(200, #"{"access_token":"x.y.z","refresh_token":"r"}"#))
            let tokens = TokenStore(app: "test.looked.new", home: home)
            tokens.clear()
            defer { tokens.clear() }
            let box = Box()
            let clock = ConnectClock(now: { Date() }, sleep: { _ in
                await MainActor.run {
                    // the person asks for a new code while the first one shows
                    if !box.asked { box.asked = true; box.flow?.newCode() }
                }
            })
            let f = CodeSignIn(client: APIClient(home: home, tokens: tokens, session: RouteStub.session()), label: "TV",
                               clock: clock, finish: {})
            box.flow = f
            f.progressObserver = { p in if let p { box.steps.append(p.step) } }
            await f.run()
            #expect(f.status == .approved)
            // the first code was never polled; the second one was approved
            #expect(RouteStub.seen.compactMap { $0.body?["device_code"] } == ["dc2"])
            #expect(!box.steps.contains(.failed(reason: .approvalExpired, tried: [])), "no 'it expired' in between")
        }
    }
}
