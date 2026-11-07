import Foundation
import Testing
@testable import MaktabaKit

// Story 54.20 T04: hostile values are plain text, cut, with no URL used; plugin_panels is
// off unless the home says on.
struct PluginPanelTests {
    let answer = Data("""
    {"panels":[{"plugin_id":"dev.example.loudness","plugin":"Loudness","title":"Report","blocks":[
      {"type":"kv","label":"Levels","value":[{"label":"Link","value":"https://evil.example/x.png"},{"label":"Peak","value":-1.5}]},
      {"type":"badges","value":[{"text":"\\u202Egnol","tone":"warning"}]},
      {"type":"chips","value":["[click](https://evil.example)"]},
      {"type":"markers","value":[{"t_ms":61500,"label":"pause"}]},
      {"type":"image","image":"https://evil.example/x.png"},
      {"type":"text","value":"**bold**"}]}]}
    """.utf8)

    @Test func decodesAndKeepsTextPlain() throws {
        struct Wire: Decodable { let panels: [PanelView] }
        let p = try JSONDecoder().decode(Wire.self, from: answer).panels[0]
        #expect(p.pluginID == "dev.example.loudness")
        let kv = p.blocks[0].value!.list
        #expect(PanelText.plain(kv[0]["value"]) == "https://evil.example/x.png")
        #expect(PanelText.plain(kv[1]["value"]) == "-1.5")
        #expect(PanelText.plain(p.blocks[1].value!.list[0]["text"]) == "gnol")
        #expect(PanelText.plain(p.blocks[5].value) == "**bold**")
        #expect(PanelText.safeImage(p.blocks[4].image) == nil)
        #expect(PanelText.safeImage("/api/panels/image?subject=video:1&plugin=dev.example.x") != nil)
        #expect(PanelText.safeImage("//evil.example/x") == nil)
        #expect(PanelText.clock(61500) == "1:01")
        #expect(PanelText.clock(3_723_000) == "1:02:03")
    }

    @Test func cutsLongText() {
        let s = PanelText.plain(String(repeating: "ع", count: 500))
        #expect(s.count == 200)
        #expect(s.hasSuffix("…"))
        #expect(PanelText.plain(.object(["a": .string("b")])) == "")
        #expect(PanelText.plain(.number(.nan)) == "")
    }

    @Test func tvShowsKvBadgesAndMarkersOnly() throws {
        struct Wire: Decodable { let panels: [PanelView] }
        let p = try JSONDecoder().decode(Wire.self, from: answer).panels[0]
        #expect(PanelText.shown(p.blocks, tv: true).map(\.type) == ["kv", "badges", "markers"])
        #expect(PanelText.shown(p.blocks, tv: false).count == 6)
    }

    @Test func panelsAreOffUnlessTheHomeSaysOn() throws {
        #expect(try Capabilities.parse(Data(#"{"features":{"live_tv":"on"}}"#.utf8)).0.panels == false)
        #expect(try Capabilities.parse(Data(#"{"features":{"plugin_panels":"off"}}"#.utf8)).0.panels == false)
        #expect(try Capabilities.parse(Data(#"{"features":{"plugin_panels":"on"}}"#.utf8)).0.panels == true)
        #expect(Capabilities.allOn.panels == false)
    }
}
