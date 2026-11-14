import Foundation
import Testing
@testable import MaktabaKit

/// Story 58.03 (T06): "Approve a TV or device" with a TV's code from Maktaba Cloud, and the
/// apps signed in by a code on the Profile's sessions page, against a stub home.
extension BrowseClient {
    @Suite struct CloudTVCodes {
        init() { BrowseStub.reset() }

        static let notHeld = #"{"type":"https://maktaba.tv/problems/not-found","status":404,"title":"code not held"}"#
        static let notLinked = #"{"type":"https://maktaba.tv/problems/cloud-not-linked","status":409,"title":"not registered with Maktaba Cloud","code":"cloud_not_linked"}"#
        static let tv = #"{"code":"ABCD-2345","platform":"tizen","app_version":"1.0.0","country":"JO","expires_at":"2026-11-14T10:10:00Z"}"#
        static let homeDevice = #"{"code":"ABCD-1234","client_app":"tv","platform":"tvOS","device":"tv","device_label":"Living room"}"#

        @MainActor static func model(_ lang: PermissionsStrings.Language = .en) -> ApproveCode {
            ApproveCode(client: BrowseHome.client().0, words: ConnectStrings(lang))
        }

        static func asked(_ method: String, _ path: String) -> Bool {
            BrowseStub.all.contains { $0.method == method && $0.path == path }
        }

        @Test func theCloudsShape() {
            #expect(CloudCode.isCloudShaped("ABCD-2345"))
            #expect(CloudCode.isCloudShaped("abcd 2345"))
            // 0, 1, I, L, O and U are never in a cloud code: only the home's
            #expect(!CloudCode.isCloudShaped("ABCD-1234"))
            #expect(!CloudCode.isCloudShaped("ABCD-23450"))
            #expect(!CloudCode.isCloudShaped("ABCU-2345"))
        }

        /// A cloud-shaped code the home does not hold: the cloud lookup, the TV's platform and
        /// country to confirm, and cloud-approve only once the person approves.
        @MainActor @Test func aCloudCodeIsLookedUpConfirmedThenApproved() async throws {
            BrowseStub.on("GET", "/api/pairing/device/ABCD2345", (404, Self.notHeld))
            BrowseStub.on("GET", "/api/pairing/cloud-code/ABCD2345", (200, Self.tv))
            BrowseStub.on("POST", "/api/pairing/cloud-approve", (200, #"{"status":"approved","user_id":"u1","platform":"tizen","country":"JO"}"#))
            let m = Self.model()
            let shown = await m.lookUp("abcd2345")
            #expect(shown == "ABCD-2345")
            guard case let .cloud(code, tv) = m.step else {
                Issue.record("not a cloud step: \(m.step)")
                return
            }
            #expect(code == "ABCD-2345" && tv.platform == "tizen" && tv.country == "JO" && tv.appVersion == "1.0.0")
            #expect(m.confirmation(tv) == "Is this your TV? Samsung, in Jordan.")
            #expect(Self.model(.ar).confirmation(tv) == "هل هذا تلفازك؟ Samsung، في الأردن.")
            // nothing approved until the person says so
            #expect(!Self.asked("POST", "/api/pairing/cloud-approve"))
            #expect(!Self.asked("POST", "/api/pairing/approve"))
            await m.approve()
            #expect(BrowseStub.last?.path == "/api/pairing/cloud-approve")
            #expect(BrowseStub.last?.json?["code"] as? String == "ABCD2345")
            #expect(m.step == .approved("Approved. The TV is signing in."))
            #expect(m.problem == nil)
        }

        @MainActor @Test func aCloudCodeWithNoCountryNamesThePlatformAlone() {
            let tv = CloudTVCode(code: "ABCD-2345", platform: "webos", country: "")
            #expect(Self.model().confirmation(tv) == "Is this your TV? LG.")
            #expect(Self.model(.ar).confirmation(tv) == "هل هذا تلفازك؟ LG.")
        }

        /// A code the home holds goes the old way, even when it has the cloud's shape.
        @MainActor @Test func aHomeCodeStillUsesTheHomesApproval() async throws {
            BrowseStub.on("GET", "/api/pairing/device/ABCD2345", (200, Self.homeDevice))
            BrowseStub.on("POST", "/api/pairing/approve", (204, ""))
            let m = Self.model()
            await m.lookUp("ABCD-2345")
            guard case .home(_, .found) = m.step else {
                Issue.record("not the home's step: \(m.step)")
                return
            }
            await m.approve()
            #expect(BrowseStub.last?.path == "/api/pairing/approve")
            #expect(BrowseStub.last?.json?["code"] as? String == "ABCD2345")
            #expect(m.step == .approved("Living room is signed in."))
            #expect(!Self.asked("GET", "/api/pairing/cloud-code/ABCD2345"))
            #expect(!Self.asked("POST", "/api/pairing/cloud-approve"))
        }

        /// A home-shaped code the home does not hold is not found: never asked of the cloud.
        @MainActor @Test func aHomeShapedCodeIsNeverAskedOfTheCloud() async throws {
            BrowseStub.on("GET", "/api/pairing/device/ABCD1234", (404, Self.notHeld))
            let m = Self.model()
            await m.lookUp("ABCD-1234")
            #expect(m.step == .home(code: "ABCD-1234", .notFound))
            #expect(!BrowseStub.all.contains { $0.path.hasPrefix("/api/pairing/cloud-code") })
        }

        @MainActor @Test func aCloudCodeNobodyHoldsIsNotFound() async throws {
            BrowseStub.on("GET", "/api/pairing/device/ABCD2345", (404, Self.notHeld))
            BrowseStub.on("GET", "/api/pairing/cloud-code/ABCD2345", (404, Self.notHeld))
            let m = Self.model()
            await m.lookUp("ABCD-2345")
            #expect(m.step == .home(code: "ABCD-2345", .notFound))
        }

        /// A home with no remote access cannot take a cloud code: said so, in both languages.
        @MainActor @Test func cloudNotLinkedAsksForRemoteAccess() async throws {
            BrowseStub.on("GET", "/api/pairing/device/ABCD2345", (404, Self.notHeld))
            BrowseStub.on("GET", "/api/pairing/cloud-code/ABCD2345", (409, Self.notLinked))
            let m = Self.model()
            await m.lookUp("ABCD-2345")
            #expect(m.step == .idle)
            #expect(m.problem == "A code from Maktaba Cloud needs remote access turned on for this home. Use the code from the home instead.")
            let ar = Self.model(.ar)
            await ar.lookUp("ABCD-2345")
            #expect(ar.problem == "يحتاج الرمز القادم من Maktaba Cloud إلى تفعيل الوصول عن بُعد لهذا المنزل. استخدم رمز المنزل بدلًا منه.")
        }

        /// The approval's own answers: the link gone since the lookup, approved already, expired.
        @MainActor @Test func cloudApprovalRefusals() async throws {
            BrowseStub.on("GET", "/api/pairing/device/ABCD2345", (404, Self.notHeld))
            BrowseStub.on("GET", "/api/pairing/cloud-code/ABCD2345", (200, Self.tv))
            BrowseStub.on("POST", "/api/pairing/cloud-approve",
                          (409, Self.notLinked),
                          (409, #"{"type":"https://maktaba.tv/problems/conflict","status":409,"title":"code already approved"}"#),
                          (404, Self.notHeld))
            let m = Self.model()
            let words = ConnectStrings(.en)
            await m.lookUp("ABCD-2345")
            await m.approve()
            #expect(m.problem == words.text(.approveCloudNeedsRemote))
            await m.approve()
            #expect(m.problem == words.text(.alreadyApproved))
            await m.approve()
            #expect(m.problem == words.text(.expired))
            if case .cloud = m.step {} else { Issue.record("the TV is still on screen: \(m.step)") }
        }

        @MainActor @Test func notACode() async {
            let m = Self.model()
            #expect(await m.lookUp("hello") == nil)
            #expect(m.problem == ConnectStrings(.en).text(.notACode))
            #expect(BrowseStub.all.isEmpty)
        }

        /// The Profile's sessions page lists the apps signed in by a code, the cloud's labelled
        /// so, and signs one out.
        @MainActor @Test func appSignInsShowTheCloudLabelAndSignOneOut() async throws {
            let list = #"""
            {"items":[
             {"id":"a1","kind":"tv","label":"tizen","via":"cloud_code","created_at":"2026-11-14T10:00:00Z","last_seen_at":"2026-11-14T11:00:00Z","expires_at":"2026-12-14T10:00:00Z","current":false},
             {"id":"a2","kind":"tv","label":"Living room","via":"","created_at":"2026-11-13T10:00:00Z","last_seen_at":"2026-11-13T11:00:00Z","expires_at":"2026-12-13T10:00:00Z","current":false}
            ]}
            """#
            BrowseStub.on("GET", "/api/me/app-sign-ins", (200, list))
            BrowseStub.on("DELETE", "/api/me/app-sign-ins/a1", (204, ""))
            let model = ProfileModel(client: BrowseHome.client().0, signedOut: {})
            await model.loadAppSignIns()
            let rows = try #require(model.appSignIns.value)
            #expect(rows.map(\.id) == ["a1", "a2"])
            #expect(rows[0].viaCloudCode && !rows[1].viaCloudCode)
            #expect(rows[0].title(ConnectStrings(.en)) == "Samsung TV, via Maktaba Cloud code")
            #expect(rows[0].title(ConnectStrings(.ar)) == "تلفاز Samsung، عبر رمز Maktaba Cloud")
            #expect(rows[1].title(ConnectStrings(.en)) == "Living room")
            #expect(rows[0].lastSeenAt != nil)
            await model.signOut(rows[0])
            #expect(BrowseStub.last?.method == "DELETE" && BrowseStub.last?.path == "/api/me/app-sign-ins/a1")
            #expect(model.appSignIns.value?.map(\.id) == ["a2"])
            #expect(model.problem == nil)
        }

        /// A home from before the list has none to show; a refused sign-out keeps the row.
        @MainActor @Test func appSignInsOnAnOlderHomeAndARefusal() async throws {
            let model = ProfileModel(client: BrowseHome.client().0, signedOut: {})
            await model.loadAppSignIns()
            #expect(model.appSignIns.value?.isEmpty == true)
            BrowseStub.on("GET", "/api/me/app-sign-ins", (200, #"{"items":[{"id":"a1","kind":"tv","label":"tizen","via":"cloud_code"}]}"#))
            BrowseStub.on("DELETE", "/api/me/app-sign-ins/a1", (500, #"{"status":500,"title":"boom"}"#))
            await model.loadAppSignIns()
            let row = try #require(model.appSignIns.value?.first)
            await model.signOut(row)
            #expect(model.appSignIns.value?.map(\.id) == ["a1"])
            #expect(model.problem != nil)
        }
    }
}
