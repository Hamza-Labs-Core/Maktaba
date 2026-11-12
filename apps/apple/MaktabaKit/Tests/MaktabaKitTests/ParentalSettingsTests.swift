import Foundation
import Testing
@testable import MaktabaKit

// Epic 57 story 57.12 on the Apple Player: turning parental controls on in an existing home.
// The capabilities' `parental` block, the admin's switch with its notice, and Rate
// libraries' suggestions, each shaped as the Go handlers write it
// (`api/internal/handlers/parental/settings.go`, `api/internal/parental/enable.go`). No home
// is involved.

private let en = Locale(identifier: "en_US")
private let ar = Locale(identifier: "ar_SA")

@Suite struct TheCapabilitiesParentalBlock {
    @Test func theBlockIsReadWithTheFeatures() throws {
        let json = #"{"etag":"c1","features":{"live_tv":"off"},"parental":{"enabled":false,"restricted":false,"tier_limit":null,"can_manage":true}}"#
        let (caps, etag) = try Capabilities.parse(Data(json.utf8))
        #expect(etag == "c1" && !caps.isOn(.liveTV))
        #expect(caps.parental == Capabilities.Parental(enabled: false, restricted: false, tierLimit: nil, canManage: true))

        let child = #"{"features":{},"parental":{"enabled":true,"restricted":true,"tier_limit":9,"can_manage":false}}"#
        let limited = try Capabilities.parse(Data(child.utf8)).0.parental
        #expect(limited?.enabled == true && limited?.restricted == true && limited?.tierLimit == 9 && limited?.canManage == false)
    }

    @Test func anOlderHomeHasNoBlockAndAnOddOneHidesNothing() throws {
        let older = try Capabilities.parse(Data(#"{"etag":"c0","features":{"live_tv":"on"}}"#.utf8)).0
        #expect(older.parental == nil && older.isOn(.liveTV))
        #expect(Capabilities.allOn.parental == nil)
        // a block of another shape reads as none; the rest of the answer is kept
        let odd = try Capabilities.parse(Data(#"{"features":{"live_tv":"off"},"parental":"yes"}"#.utf8)).0
        #expect(odd.parental == nil && !odd.isOn(.liveTV))
        // a switch that is missing counts as on, as on the home
        let bare = try Capabilities.parse(Data(#"{"features":{},"parental":{}}"#.utf8)).0
        #expect(bare.parental == Capabilities.Parental(enabled: true))
    }
}

@Suite struct TurningItOnInAnExistingHome {
    @Test func theSwitchDecodesAsTheHomeWritesIt() throws {
        let json = #"{"enabled":false,"restricted_accounts":0,"can_disable":true,"child_devices":2,"child_device_notice":true}"#
        let s = try JSONDecoder.home.decode(ParentalSettings.self, from: Data(json.utf8))
        #expect(s == ParentalSettings(enabled: false, restrictedAccounts: 0, canDisable: true, childDevices: 2, childDeviceNotice: true))
        let on = try JSONDecoder.home.decode(ParentalSettings.self, from: Data(#"{"enabled":true,"restricted_accounts":1,"can_disable":false,"child_devices":0,"child_device_notice":false}"#.utf8))
        #expect(on.enabled && !on.canDisable && on.restrictedAccounts == 1 && !on.childDeviceNotice)
        // a switch that cannot be read counts as on; with no restricted account it can go off
        let bare = try JSONDecoder.home.decode(ParentalSettings.self, from: Data("{}".utf8))
        #expect(bare.enabled && bare.canDisable && !bare.childDeviceNotice)
    }

    /// 57.12 §3: a library with no tier that is mostly lectures is offered All ages; one
    /// with a tier, or with no suggestion, is offered nothing.
    @Test func aLecturesLibraryIsOfferedAllAgesAndNothingElseIs() throws {
        let json = #"""
        {"libraries":[
          {"library_id":"l1","name":"Lectures","tier":null,"suggested_tier":0,"classified":40,"lectures":38},
          {"library_id":"l2","name":"Films","tier":null,"suggested_tier":null,"classified":20,"lectures":10},
          {"library_id":"l3","name":"Rated already","tier":13,"suggested_tier":0,"classified":10,"lectures":10},
          {"library_id":"l4","name":"Odd","tier":null,"suggested_tier":7},
          {"name":"no id"}
        ]}
        """#
        struct A: Decodable {
            let libraries: [LibrarySuggestion]
            init(from decoder: Decoder) throws {
                libraries = try decoder.container(keyedBy: AnyKey.self).lenientList(AnyKey(stringValue: "libraries"))
            }
        }
        let list = try JSONDecoder.home.decode(A.self, from: Data(json.utf8)).libraries
        #expect(list.map(\.id) == ["l1", "l2", "l3", "l4"])
        #expect(list[0].offered == 0 && list[0].lectures == 38 && list[0].classified == 40)
        #expect(list[1].offered == nil && list[1].tier == nil)
        #expect(list[2].offered == nil && list[2].tier == 13, "a library with a tier keeps it")
        #expect(list[3].offered == nil, "only a tier the home may store is offered")
    }

    @Test func theStorysWordsInEnglishAndArabic() {
        let words: [(String, String, String)] = [
            ("parental.notice.title", "A child may be using Maktaba", "ربما يستخدم طفلٌ Maktaba"),
            ("parental.notice.body", "A device signed in to Maktaba says a child uses it. Set up parental controls.",
             "يشير جهاز مسجَّل الدخول إلى Maktaba إلى أن طفلًا يستخدمه. اضبط الرقابة الأبوية."),
            ("parental.notice.action", "Set up", "إعداد"),
            ("parental.enabled", "Parental controls", "الرقابة الأبوية"),
            ("parental.enabled.offBlocked", "Lift every restriction before turning this off.", "ارفع كل التقييدات قبل إيقاف هذا."),
        ]
        for (key, english, arabic) in words {
            #expect(ParentalWords.text(key, en) == english, "\(key)")
            #expect(ParentalWords.text(key, ar) == arabic, "\(key)")
        }
        for key in ["parental.notice.dismiss", "parental.enabled.on", "parental.enabled.off", "family.rateLibraries.saveSuggested",
                    "family.rateLibraries.suggested.detail", "help.parental.title", "help.parental.screenTime", "help.parental.web"] {
            let e = ParentalWords.text(key, en), a = ParentalWords.text(key, ar)
            #expect(e != key, "\(key) has its English")
            #expect(a != key && a != e, "\(key) has its Arabic")
        }
        #expect(ParentalWords.text("family.rateLibraries.suggested", en, AgeTier.label(0, en)) == "Suggested: All ages")
        #expect(ParentalWords.text("family.rateLibraries.suggested", ar, AgeTier.label(0, ar)).hasPrefix("المقترح: "))
        // Help also says where a guardian with no iPhone goes: Family on the home's web page
        #expect(ParentalWords.text("help.parental.web", en) == "A guardian with no iPhone or iPad uses Family on the home's web page (/family).")
        #expect(ParentalWords.text("help.parental.web", ar).contains("/family"))
        #expect(FamilyNotice.restrictionsExist.line(en) == "Lift every restriction before turning this off.")
    }
}

extension AgainstAParentalStubHome {
    // MARK: turning it on in an existing home (57.12)

    static let settingsOffWithNotice = #"{"enabled":false,"restricted_accounts":0,"can_disable":true,"child_devices":1,"child_device_notice":true}"#
    static let settingsOff = #"{"enabled":false,"restricted_accounts":0,"can_disable":true,"child_devices":1,"child_device_notice":false}"#
    static let settingsOn = #"{"enabled":true,"restricted_accounts":0,"can_disable":true,"child_devices":1,"child_device_notice":false}"#
    static let settingsOnHeld = #"{"enabled":true,"restricted_accounts":1,"can_disable":false,"child_devices":0,"child_device_notice":false}"#
    static let restrictionsExist = #"{"type":"https://maktaba.tv/problems/parental-restrictions-exist","title":"conflict","status":409,"detail":"lift every restriction before turning parental controls off","code":"restrictions_exist"}"#
    static let adminOnly = #"{"type":"https://maktaba.tv/problems/forbidden","title":"forbidden","status":403,"code":"admin_only"}"#

    /// An admin's Family reads the switch; a guardian's does not ask for it (the home would
    /// refuse), and neither sees a notice the home did not send.
    @MainActor @Test func anAdminReadsTheSwitchAndAGuardianDoesNotAsk() async {
        ParentalStub.reset()
        ParentalStub.on("GET /api/parental/children", 200, #"{"children":[]}"#)
        ParentalStub.on("GET /api/parental/requests", 200, #"{"requests":[]}"#)
        ParentalStub.on("GET /api/me/parental-pin", 200, #"{"set":false}"#)
        ParentalStub.on("GET /api/admin/library-acl", 200, #"{"users":[]}"#)
        ParentalStub.on("GET /api/parental/settings", 200, Self.settingsOffWithNotice)
        let client = ParentalStub.client()
        let admin = FamilyModel(client: client, isAdmin: true)
        #expect(!admin.showsChildDeviceNotice, "nothing shows before the home answers")
        await admin.load()
        #expect(admin.settings?.enabled == false && admin.settings?.childDevices == 1 && admin.showsChildDeviceNotice)

        let guardian = FamilyModel(client: client, isAdmin: false)
        await guardian.load()
        #expect(guardian.settings == nil && !guardian.showsChildDeviceNotice)
        #expect(ParentalStub.seen("GET /api/parental/settings").count == 1, "only the admin asked")
        await guardian.setEnabled(true)
        await guardian.dismissChildDeviceNotice()
        #expect(ParentalStub.seen("PUT /api/parental/settings").isEmpty && ParentalStub.seen("POST /api/parental/settings/notice/dismiss").isEmpty)
    }

    /// Dismiss puts the notice away at once and tells the home; a refusal brings it back.
    @MainActor @Test func dismissingTheNoticeTellsTheHomeAndARefusalBringsItBack() async {
        ParentalStub.reset()
        ParentalStub.on("GET /api/parental/children", 200, #"{"children":[]}"#)
        ParentalStub.on("GET /api/parental/settings", 200, Self.settingsOffWithNotice)
        ParentalStub.on("POST /api/parental/settings/notice/dismiss", 403, Self.adminOnly)
        ParentalStub.on("POST /api/parental/settings/notice/dismiss", 200, Self.settingsOff)
        let model = FamilyModel(client: ParentalStub.client(), isAdmin: true)
        await model.load()
        #expect(model.showsChildDeviceNotice)
        await model.dismissChildDeviceNotice()
        #expect(model.showsChildDeviceNotice && model.notice == .readOnly, "refused: the notice is back and the screen says why")
        model.notice = nil
        await model.dismissChildDeviceNotice()
        #expect(!model.showsChildDeviceNotice && model.notice == nil && model.settings?.childDevices == 1)
        #expect(ParentalStub.seen("POST /api/parental/settings/notice/dismiss").count == 2)
        await model.dismissChildDeviceNotice()
        #expect(ParentalStub.seen("POST /api/parental/settings/notice/dismiss").count == 2, "nothing to dismiss: nothing sent")
    }

    /// The switch sends `{enabled}`; off while an account is restricted is 409
    /// `restrictions_exist`: the switch stays on and the screen says to lift every restriction.
    @MainActor @Test func theSwitchTurnsOnAndOffIsRefusedWhileAnAccountIsRestricted() async {
        ParentalStub.reset()
        ParentalStub.on("GET /api/parental/children", 200, #"{"children":[]}"#)
        ParentalStub.on("GET /api/parental/settings", 200, Self.settingsOff)
        ParentalStub.on("GET /api/parental/settings", 200, Self.settingsOnHeld)
        ParentalStub.on("PUT /api/parental/settings", 200, Self.settingsOn)
        ParentalStub.on("PUT /api/parental/settings", 409, Self.restrictionsExist)
        let model = FamilyModel(client: ParentalStub.client(), isAdmin: true)
        await model.load()
        #expect(model.settings?.enabled == false)
        await model.setEnabled(false)
        #expect(ParentalStub.seen("PUT /api/parental/settings").isEmpty, "already off: nothing sent")

        await model.setEnabled(true)
        #expect(model.settings?.enabled == true && model.notice == nil && !model.switching)
        #expect(ParentalStub.seen("PUT /api/parental/settings").first?.json["enabled"] as? Bool == true)

        // someone made an account restricted meanwhile: off is refused
        await model.setEnabled(false)
        #expect(ParentalStub.seen("PUT /api/parental/settings").last?.json["enabled"] as? Bool == false)
        #expect(model.notice == .restrictionsExist)
        #expect(model.notice?.line(en) == "Lift every restriction before turning this off.")
        #expect(model.notice?.line(ar) == "ارفع كل التقييدات قبل إيقاف هذا.")
        #expect(model.settings?.enabled == true && model.settings?.canDisable == false, "the switch stays on, read again from the home")
        #expect(ParentalRefusal(APIError.status(409, Self.restrictionsExist)) == .restrictionsExist)
    }

    /// The routes themselves: a home from before them answers 404, read as "none".
    @MainActor @Test func theSwitchAndTheSuggestionsOnAHomeFromBeforeThem() async throws {
        ParentalStub.reset()
        ParentalStub.on("GET /api/parental/settings", 404, ParentalFixtures.notFound)
        ParentalStub.on("GET /api/parental/library-suggestions", 404, ParentalFixtures.notFound)
        ParentalStub.on("GET /api/parental/library-suggestions", 200,
                        #"{"libraries":[{"library_id":"l1","name":"Lectures","tier":null,"suggested_tier":0,"classified":40,"lectures":38}]}"#)
        ParentalStub.on("GET /api/parental/library-suggestions", 403, Self.adminOnly)
        let client = ParentalStub.client()
        #expect(try await client.parentalSettings() == nil)
        #expect(try await client.librarySuggestions() == nil)
        let list = try #require(try await client.librarySuggestions())
        #expect(list.map(\.id) == ["l1"] && list[0].offered == 0 && list[0].name == "Lectures")
        await #expect(throws: APIError.self) { _ = try await client.librarySuggestions() }
    }
}
