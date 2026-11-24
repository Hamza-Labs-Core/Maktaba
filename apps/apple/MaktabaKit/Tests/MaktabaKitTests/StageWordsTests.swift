import Foundation
import Testing
@testable import MaktabaKit

// The queue says what the home sends as codes in the app's own words, in English and in
// Arabic: a stage's id (never its English label in the Arabic app), a running job's step and
// its state, a failure group's stage and cause. A code the app has no words for keeps what the
// home sent. The guards read the repository: every stage the web names, every cause the home
// labels and every step the pipeline reports has its words in both languages here.

@Suite struct StageWordsTests {
    let en = Locale(identifier: "en"), ar = Locale(identifier: "ar")

    /// The repository's root (this file is apps/apple/MaktabaKit/Tests/MaktabaKitTests/).
    static let root: URL = {
        var u = URL(fileURLWithPath: #filePath)
        for _ in 0 ..< 6 { u.deleteLastPathComponent() }
        return u
    }()

    static func read(_ path: String) throws -> String {
        try String(contentsOf: root.appendingPathComponent(path), encoding: .utf8)
    }

    static func json(_ path: String) throws -> [String: String] {
        let o = try JSONSerialization.jsonObject(with: Data(read(path).utf8)) as? [String: Any] ?? [:]
        return o.compactMapValues { $0 as? String }
    }

    static func matches(_ pattern: String, in s: String, options: NSRegularExpression.Options = []) -> [String] {
        let re = try! NSRegularExpression(pattern: pattern, options: options)
        return re.matches(in: s, range: NSRange(s.startIndex..., in: s)).compactMap {
            Range($0.range(at: 1), in: s).map { String(s[$0]) }
        }
    }

    static func hasArabic(_ s: String) -> Bool {
        s.unicodeScalars.contains { (0x0600...0x06FF).contains($0.value) }
    }

    /// The Latin words an Arabic title may keep: names, not English.
    static let names: Set<String> = ["Plex", "MP4", "Tartube", "NAS", "YouTube", "Postgres", "ffmpeg"]

    static func latinWords(_ s: String) -> [String] {
        matches(#"([A-Za-z][A-Za-z0-9]*)"#, in: s).filter { !names.contains($0) }
    }

    // MARK: the words

    @Test func aStageIsSaidInTheAppsLanguage() throws {
        let probe = try JSONDecoder.home.decode(StageInfo.self, from: Data(#"""
        {"stage":"probe","label":"Length","lane":"a","lane_order":1,"hidden":false,"enabled":true,"paused":false}
        """#.utf8))
        #expect(probe.title(locale: en) == "Length")
        #expect(probe.title(locale: ar) == "المدة")
        #expect(StageWords.title("transcribe", label: "Transcribe", locale: ar) == "التفريغ")
        #expect(StageWords.title("index", locale: ar) == "فهرس البحث")
    }

    @Test func aStageWithNoWordsKeepsTheHomesLabel() {
        // a newer home's stage, or a plugin's
        #expect(StageWords.title("from_the_future", label: "Future step", locale: ar) == "Future step")
        // no label either: its id, with spaces
        #expect(StageWords.title("from_the_future", label: "  ", locale: ar) == "from the future")
        #expect(StageWords.title("from_the_future", locale: en) == "from the future")
    }

    @Test func aFailureGroupsStageIsSaidInWords() throws {
        let o: [String: Any] = ["stage": "transcribe", "cause": "disk_full", "label": "", "count": 3, "action": "open_settings"]
        let g = try JSONDecoder.home.decode(FailureGroup.self, from: JSONSerialization.data(withJSONObject: o))
        #expect(g.stageTitle(locale: ar) == "التفريغ")
        #expect(g.stageTitle(locale: en) == "Transcribe")
        #expect(g.title(locale: ar) == "لا مساحة متبقية على القرص")
    }

    @Test func aRunningJobsStepAndStateAreSaidInWords() throws {
        let jobs = try JSONDecoder.home.decode(RunningJobs.self, from: Data(#"""
        {"jobs":[
          {"id":1,"stage":"index","state":"running","subject_label":"a","progress":{"step":"embed","done_units":1,"total_units":4}},
          {"id":2,"stage":"transcribe","state":"running","subject_label":"b","progress":{"step":"transcribe","done_units":1,"total_units":4}},
          {"id":3,"stage":"dedup_scan","state":"running","subject_label":"c","progress":{"step":"full_hash","done_units":1,"total_units":4}},
          {"id":4,"stage":"x_plugin","state":"resuming","subject_label":"d","progress":{"step":"warm_up","done_units":1,"total_units":4}},
          {"id":5,"stage":"probe","state":"claimed","subject_label":"e"}]}
        """#.utf8)).jobs
        #expect(jobs[0].stepWords(locale: ar) == "الفهرسة للبحث")
        #expect(jobs[0].stepWords(locale: en) == "Index for search")
        // the stage's own id says nothing the row does not
        #expect(jobs[1].stepWords(locale: ar) == nil)
        #expect(jobs[2].stepWords(locale: ar) == "مقارنة الملفات")
        // a step this app has no words for: as sent, its underscores as spaces
        #expect(jobs[3].stepWords(locale: ar) == "warm up")
        #expect(jobs[4].stepWords(locale: ar) == nil)
        #expect(jobs[4].stateWords(locale: ar) == "يبدأ")
        #expect(jobs[4].stateWords(locale: en) == "starting")
        #expect(jobs[3].stateWords(locale: ar) == "يُستأنف")
        // a step that is another stage's id is that stage's title
        #expect(StageWords.step("second_listen", stage: "transcribe", locale: ar) == "الاستماع الثاني")
    }

    // MARK: the guards

    /// Every word of the three tables has its English (the table's) and its Arabic in the
    /// catalog, and the Arabic has no English word in it (a name may stay: Plex, MP4).
    @Test func everyWordHasBothLanguages() {
        let tables: [(String, [String: String])] = [
            ("queue.stage.%@.title", StageWords.stages),
            ("queue.running.step.%@", StageWords.steps),
            ("queue.running.state.%@", StageWords.states),
        ]
        for (pattern, table) in tables {
            for (code, english) in table {
                let key = pattern.replacingOccurrences(of: "%@", with: code)
                #expect(kitWord(key, en) == english, "\(key) in English")
                let arabic = kitWord(key, ar) ?? ""
                #expect(Self.hasArabic(arabic), "\(key) in Arabic: \(arabic)")
                #expect(Self.latinWords(arabic).isEmpty, "\(key): English in the Arabic: \(arabic)")
            }
        }
    }

    /// Every stage the web names (en.json `queue.stage.<id>.title`, which the web's own test
    /// holds to the pipeline graph) is here, in the web's English and Arabic.
    @Test func everyStageTheWebNamesIsHere() throws {
        let webEN = try Self.json("web/src/i18n/en.json"), webAR = try Self.json("web/src/i18n/ar.json")
        let keys = webEN.keys.filter { $0.range(of: #"^queue\.stage\.[a-z0-9_]+\.title$"#, options: .regularExpression) != nil }
        #expect(keys.count > 50, "the web's stage titles read")
        for key in keys {
            let stage = String(key.dropFirst("queue.stage.".count).dropLast(".title".count))
            #expect(StageWords.stages[stage] == webEN[key], "\(stage): not the web's English")
            // Plex Live TV: the web keeps the name whole; here its Arabic is the web's other words
            if stage != "plex_livetv" {
                #expect(StageWords.title(stage, locale: ar) == webAR[key], "\(stage): not the web's Arabic")
            }
        }
        #expect(Set(StageWords.stages.keys) == Set(keys.map { String($0.dropFirst("queue.stage.".count).dropLast(".title".count)) }),
                "a stage here the web does not name")
    }

    /// Every cause the home labels (api queues/causes.go: its codes and the kinds it names)
    /// has the app's words, in English and Arabic.
    @Test func everyCauseTheHomeLabelsIsHere() throws {
        let go = try Self.read("api/internal/handlers/queues/causes.go")
        let causes = Self.matches(#"^\s*"([a-z0-9_]+)":\s*\{"#, in: go, options: .anchorsMatchLines)
        let kinds = Self.matches(#"^\s*"([a-z0-9_]+)":\s*""#, in: go, options: .anchorsMatchLines)
        #expect(causes.count > 20 && kinds.count >= 4, "causes.go read: \(causes.count) causes, \(kinds.count) kinds")
        let webAR = try Self.json("web/src/i18n/ar.json")
        for code in causes + kinds + ["awaiting_review", "stale_graph"] {
            let english = AttentionWords.title(cause: code, label: "", locale: en)
            let arabic = AttentionWords.title(cause: code, label: "", locale: ar)
            #expect(AttentionWords.words(code, en) != nil, "\(code) has no words")
            #expect(Self.hasArabic(arabic) && arabic != english, "\(code) in Arabic: \(arabic)")
            // the web's Arabic, where the web has the code
            if let web = webAR["queue.issues.cause.\(code)"] { #expect(arabic == web, "\(code): not the web's Arabic") }
        }
    }

    /// Every step the pipeline and Maktaba's own plugins report (`ctx.progress("<step>", ...)`,
    /// the steps they list) is a stage's id or has its words.
    @Test func everyStepThePipelineReportsIsHere() throws {
        var steps = Set<String>()
        for dir in ["pipeline/src", "plugins"] {
            let base = Self.root.appendingPathComponent(dir)
            let files = FileManager.default.enumerator(at: base, includingPropertiesForKeys: nil)
            while let url = files?.nextObject() as? URL {
                guard url.pathExtension == "py", !url.path.contains("/tests/"),
                      !url.lastPathComponent.hasPrefix("test_") else { continue }
                let src = try String(contentsOf: url, encoding: .utf8)
                steps.formUnion(Self.matches(#"progress\(\s*"([^"]+)""#, in: src))
                for list in Self.matches(#"(?:steps|STEPS)\s*=\s*\[([^\]]*)\]"#, in: src) {
                    steps.formUnion(Self.matches(#""([^"]+)""#, in: list))
                }
            }
        }
        #expect(steps.count > 20, "the steps read: \(steps.sorted())")
        for step in steps {
            #expect(StageWords.stages[step] != nil || StageWords.steps[step] != nil, "the step \(step) has no words")
        }
    }
}
