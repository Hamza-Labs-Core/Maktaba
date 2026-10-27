import Foundation
import Testing
@testable import MaktabaKit

@Suite struct WalkthroughWords {
    @Test func everyKeyIsInBothLanguages() {
        for key in PermissionsStrings.Key.allCases {
            let en = PermissionsStrings.en[key]
            let ar = PermissionsStrings.ar[key]
            #expect(en?.isEmpty == false, "en \(key)")
            #expect(ar?.isEmpty == false, "ar \(key)")
            #expect(en != ar, "ar \(key) is not translated")
            // the same placeholders, so a name lands in both
            #expect(en?.components(separatedBy: "%@").count == ar?.components(separatedBy: "%@").count, "\(key)")
        }
    }

    @Test func arabicIsRightToLeft() {
        #expect(PermissionsStrings.forPreferredLanguages(["ar-SA", "en"]).isRightToLeft)
        #expect(!PermissionsStrings.forPreferredLanguages(["en-GB", "ar"]).isRightToLeft)
        #expect(!PermissionsStrings.forPreferredLanguages([]).isRightToLeft)
    }

    @Test func theAppsNameFillsIn() {
        let w = PermissionsStrings(.en)
        #expect(w.text(.intro, "Maktaba Player").hasPrefix("Maktaba Player needs"))
        #expect(PermissionsStrings(.ar).text(.localNetworkDeniedHow, "Maktaba", "Maktaba").contains("Maktaba"))
        #expect(!w.text(.localNetworkDeniedHow, "A", "A").contains("%@"))
    }

    @Test func theWebsTerms() {
        let ar = PermissionsStrings(.ar)
        #expect(ar.text(.required) == "مطلوب")
        #expect(ar.text(.checking) == "جارٍ التحقق…")
        #expect(ar.text(.localNetworkWhy).contains("الجهاز الرئيسي") || ar.text(.localNetworkWhy).contains("جهاز مكتبة الرئيسي"))
    }
}
