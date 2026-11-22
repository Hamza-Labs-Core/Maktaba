import Foundation
import Testing
@testable import MaktabaKit

/// Story 49.11: the search help of story 49.8 in the Apple apps (the Player and the TV share
/// `SearchModel` and these models). The answers are story 49.8's (`Fixtures/Browse/search-help.json`
/// and `search-suggest-items.json`, the shape `api/internal/handlers/search` writes); `search.json`
/// and `search-suggest.json` are a home's from before it, without the new fields.
extension BrowseClient {
    @Suite struct SearchHelp {
        init() { BrowseStub.reset() }

        static let lib2 = "22222222-2222-4222-8222-222222222222"
        let en = Locale(identifier: "en"), ar = Locale(identifier: "ar")
        /// Arabic as the app runs it in Saudi Arabia: Arabic-Indic digits.
        let arSA = Locale(identifier: "ar_SA")

        /// A sentence without the bidi isolates (U+2068, U+2069) around its interpolated words.
        private func plain(_ s: String) -> String {
            s.replacingOccurrences(of: "\u{2068}", with: "").replacingOccurrences(of: "\u{2069}", with: "")
        }

        // MARK: suggestions

        @Test func suggestionsCarryTheirKindAndATitleItsVideo() async throws {
            try BrowseHome.answer("/api/search/suggest", "search-suggest-items")
            let s = try await BrowseHome.client().0.searchSuggestions("المدر")
            #expect(s.map(\.text) == ["المدرسة", "المدرسة النبوية", "مدرسة المدينة", "الشيخ أحمد"])
            #expect(s.map(\.kind) == [.history, .term, .title, .speaker])
            #expect(s[2].videoID == BrowseHome.v4 && s[0].videoID == nil)
        }

        /// A home before 49.8 sends the texts alone: they are suggested without a kind.
        @Test func anOlderHomesPlainTextsHaveNoKind() async throws {
            BrowseStub.on("GET", "/api/search/suggest", (200, #"{"suggestions":["seerah","السيرة"]}"#))
            let s = try await BrowseHome.client().0.searchSuggestions("se")
            #expect(s.map(\.text) == ["seerah", "السيرة"])
            #expect(s.allSatisfy { $0.kind == nil && $0.videoID == nil })
        }

        /// A kind this app does not know reads as none; a title without its video, a blank
        /// text and a repeat are left out; a broken item does not hide the others.
        @Test func oddItemsAreReadTolerantly() async throws {
            BrowseStub.on("GET", "/api/search/suggest", (200, #"""
            {"suggestions":["a"],"items":[
             {"text":"a","kind":"term"},{"text":"a","kind":"term"},{"text":"  ","kind":"term"},
             {"text":"lost","kind":"title"},{"text":"new","kind":"channel"},{"kind":"term"},7,
             {"text":"a","kind":"history"}]}
            """#))
            let s = try await BrowseHome.client().0.searchSuggestions("a")
            #expect(s == [SearchSuggestion(text: "a", kind: .term), SearchSuggestion(text: "new"),
                          SearchSuggestion(text: "a", kind: .history)])
        }

        // MARK: the answer's new fields

        @Test func theAnswerCarriesItsSortDidYouMeanAndFacets() async throws {
            try BrowseHome.answer("POST", "/api/search", "search-help")
            let a = try await BrowseHome.client().0.search("المدرصة")
            #expect(a.sort == .newest)
            #expect(a.didYouMean == "المدرسة")
            let f = try #require(a.facets)
            #expect(f.videos == 2)
            #expect(f.library.map(\.label) == ["Tafsir Lessons", "دروس السيرة"])
            #expect(f.language.map(\.value) == ["ar", "en"] && f.language.map(\.count) == [2, 1])
            #expect(f.speaker.first?.value == "الشيخ أحمد")
            #expect(f.year.map(\.value) == ["2024", "2023"])
            #expect(f.year.first?.filter?.date == ["gte": "2024-01-01T00:00:00.000Z", "lt": "2025-01-01T00:00:00.000Z"])
            #expect(f.duration.map(\.value) == ["20m_60m", "over_60m"])
            #expect(f.duration.last?.filter?.durationSec == ["gte": 3600])

            let r = SearchModel.Results(a, sent: "المدرصة")
            #expect(r.sent == "المدرصة")
            #expect(r.sort == .newest && r.didYouMean == "المدرسة" && r.facets == f)
        }

        /// A home before 49.8: no sort, no did-you-mean, no facets; the results as before.
        @Test func anOlderHomesAnswerHasNoneOfThem() async throws {
            try BrowseHome.answer("POST", "/api/search", "search")
            let a = try await BrowseHome.client().0.search("prayer")
            #expect(a.sort == nil && a.didYouMean == nil && a.facets == nil)
            #expect(a.titles.count == 1 && a.transcripts.count == 1)
            let r = SearchModel.Results(a, sent: "prayer", sort: .matches)
            #expect(r.sort == .matches, "the order asked, when the home says none")
            #expect(r.didYouMean == nil && r.facets == nil)
        }

        /// Facets of odd shapes: null lists, a bucket with no value, a year as a number, a
        /// filter of the wrong shape, a blank did-you-mean.
        @Test func oddFacetsAreReadTolerantly() throws {
            let json = #"""
            {"q":"x","sort":"sideways","did_you_mean":"  ","facets":{"library":null,"language":[{"count":3},{"value":"ar"}],
             "speaker":"none","year":[{"value":2024,"count":1,"filter":{"date":"2024"}}],
             "duration":[{"value":"over_60m","count":-2,"filter":{"duration_sec":{"gte":"long","lt":3600}}}],"videos":"many"}}
            """#
            let a = try JSONDecoder().decode(SearchAnswer.self, from: Data(json.utf8))
            #expect(a.sort == nil && a.didYouMean == nil)
            let f = try #require(a.facets)
            #expect(f.library.isEmpty && f.speaker.isEmpty && f.videos == nil)
            #expect(f.language == [SearchBucket(value: "ar", count: 0)])
            #expect(f.year == [SearchBucket(value: "2024", count: 1)])
            #expect(f.duration == [SearchBucket(value: "over_60m", count: 0, filter: SearchBucketFilter(durationSec: ["lt": 3600]))])
            // nothing to show: no facets at all
            let empty = try JSONDecoder().decode(SearchAnswer.self, from: Data(#"{"facets":{"library":[]}}"#.utf8))
            #expect(SearchModel.Results(empty, sent: "x").facets == nil)
        }

        /// The did-you-mean that only repeats the query is left out.
        @Test func aDidYouMeanThatIsTheQueryIsLeftOut() throws {
            let a = try JSONDecoder().decode(SearchAnswer.self, from: Data(#"{"did_you_mean":"المدرسة"}"#.utf8))
            #expect(SearchModel.Results(a, sent: "المدرسة").didYouMean == nil)
            #expect(SearchModel.Results(a, sent: "المدرصة").didYouMean == "المدرسة")
        }

        // MARK: the request bodies of facet choices

        @Test func listsJoinAndLeaveARangeReplacesTheLast() {
            var c = SearchFilterChoice()
            c = c.toggled(.library, BrowseHome.lib).toggled(.library, Self.lib2).toggled(.language, "ar")
            #expect(c.library == [BrowseHome.lib, Self.lib2] && c.language == ["ar"] && c.count == 3)
            c = c.toggled(.library, BrowseHome.lib)
            #expect(c.library == [Self.lib2])
            c = c.toggled(.year, "2024").toggled(.year, "2023").toggled(.duration, "over_60m")
            #expect(c.year == "2023" && c.duration == "over_60m" && c.count == 4)
            #expect(c.toggled(.year, "2023").year == nil)
            #expect(c.removing(.speaker, "nobody") == c)
            #expect(c.inUse.map { "\($0.facet.rawValue)=\($0.value)" } == ["library=\(Self.lib2)", "language=ar", "year=2023", "duration=over_60m"])
        }

        @Test func noFilterSendsNoFilters() {
            #expect(SearchFilterChoice().requestBody(facets: nil) == nil)
        }

        /// A year and a band go as the bucket's own filter when the answer lists it.
        @Test func aYearAndABandSendTheirBucketsFilter() throws {
            let a = try JSONDecoder().decode(SearchAnswer.self, from: Data(try browseFixture("search-help").utf8))
            let c = SearchFilterChoice(library: [BrowseHome.lib], language: ["ar", "en"], speaker: ["الشيخ أحمد"],
                                       year: "2024", duration: "over_60m")
            let body = try #require(c.requestBody(facets: a.facets))
            #expect(body["library_id"] as? [String] == [BrowseHome.lib])
            #expect(body["language"] as? [String] == ["ar", "en"])
            #expect(body["speaker"] as? [String] == ["الشيخ أحمد"])
            #expect(body["date"] as? [String: String] == ["gte": "2024-01-01T00:00:00.000Z", "lt": "2025-01-01T00:00:00.000Z"])
            #expect(body["duration_sec"] as? [String: Int] == ["gte": 3600])
        }

        /// Without the bucket on screen (a link, an answer that no longer lists it), the range
        /// the home gives that bucket.
        @Test func withoutTheBucketTheRangeTheHomeGivesIt() throws {
            let body = try #require(SearchFilterChoice(year: "1999", duration: "5m_20m").requestBody(facets: nil))
            #expect(body["date"] as? [String: String] == ["gte": "1999-01-01T00:00:00.000Z", "lt": "2000-01-01T00:00:00.000Z"])
            #expect(body["duration_sec"] as? [String: Int] == ["gte": 300, "lt": 1200])
            #expect(body["library_id"] == nil && body["language"] == nil)
            // a band this app does not know, not listed: no range is sent, the choice stays
            let unknown = SearchFilterChoice(duration: "over_3h")
            #expect(unknown.requestBody(facets: nil)?["duration_sec"] == nil && unknown.count == 1)
            for band in SearchFilterChoice.bandOrder {
                #expect(SearchFilterChoice(duration: band).requestBody(facets: nil)?["duration_sec"] != nil, "\(band)")
            }
        }

        /// The client sends the order (not relevance, the default) and the filters.
        @Test func theClientSendsTheSortAndTheFilters() async throws {
            try BrowseHome.answer("POST", "/api/search", "search-help")
            let c = BrowseHome.client().0
            _ = try await c.search("x")
            #expect(BrowseStub.last?.json?["sort"] == nil && BrowseStub.last?.json?["filters"] == nil)
            _ = try await c.search("x", mode: .fts, sort: .matches, filters: SearchFilterChoice(speaker: ["الشيخ أحمد"]))
            let json = try #require(BrowseStub.last?.json)
            #expect(json["sort"] as? String == "matches" && json["mode"] as? String == "fts" && json["q"] as? String == "x")
            #expect((json["filters"] as? [String: Any])?["speaker"] as? [String] == ["الشيخ أحمد"])
        }

        // MARK: the view model

        private func searches() -> [[String: Any]] {
            BrowseStub.all.filter { $0.method == "POST" && $0.path == "/api/search" }.compactMap(\.json)
        }

        @MainActor @Test func aFacetNarrowsTheSearchOnScreenAndSeveralCombine() async throws {
            try BrowseHome.answer("POST", "/api/search", "search-help")
            try BrowseHome.answer("/api/search/recent", "search-recent")
            let model = SearchModel(client: BrowseHome.client().0, signedOut: {})
            await model.submit("المدرصة")
            #expect(model.results?.didYouMean == "المدرسة")
            #expect(model.shownFacets.map(\.facet) == [.library, .language, .speaker, .year, .duration])

            await model.run(try #require(model.ask(toggling: .library, Self.lib2)))
            await model.run(try #require(model.ask(toggling: .year, "2023")))
            await model.run(try #require(model.ask(sort: .matches)))
            let last = try #require(searches().last)
            #expect(last["q"] as? String == "المدرصة" && last["sort"] as? String == "matches")
            let f = try #require(last["filters"] as? [String: Any])
            #expect(f["library_id"] as? [String] == [Self.lib2])
            #expect(f["date"] as? [String: String] == ["gte": "2023-01-01T00:00:00.000Z", "lt": "2024-01-01T00:00:00.000Z"])
            #expect(model.filters.count == 2 && model.sort == .matches)
            let lib = try #require(model.shownFacets.first { $0.facet == .library })
            #expect(lib.buckets.filter(\.chosen).map(\.value) == [Self.lib2])

            // a filter taken off, then every one
            await model.run(try #require(model.ask(removing: .library, Self.lib2)))
            #expect((searches().last?["filters"] as? [String: Any])?["library_id"] == nil)
            await model.run(try #require(model.askClearingFilters()))
            #expect(searches().last?["filters"] == nil && model.filters.isEmpty)
            #expect(searches().last?["sort"] as? String == "matches", "the order stays")
        }

        /// The did-you-mean runs with the order and the filters kept; new words keep the order
        /// and drop the filters; the same words keep both.
        @MainActor @Test func whatEachKindOfSearchKeeps() async throws {
            try BrowseHome.answer("POST", "/api/search", "search-help")
            try BrowseHome.answer("/api/search/recent", "search-recent")
            let model = SearchModel(client: BrowseHome.client().0, signedOut: {})
            #expect(model.ask(sort: .newest) == nil && model.ask(toggling: .language, "ar") == nil, "nothing to refine yet")
            await model.submit("المدرصة")
            await model.run(try #require(model.ask(toggling: .language, "ar")))
            await model.run(try #require(model.ask(sort: .newest)))

            let dym = model.ask(didYouMean: "المدرسة")
            #expect(dym == SearchModel.Ask(query: "المدرسة", sort: .newest, filters: SearchFilterChoice(language: ["ar"])))
            await model.run(dym)
            #expect((searches().last?["filters"] as? [String: Any])?["language"] as? [String] == ["ar"])
            #expect(model.query == "المدرسة")

            #expect(model.ask(words: " المدرسة ").filters.language == ["ar"], "the same words keep the filters")
            await model.submit("الصبر")
            #expect(searches().last?["filters"] == nil, "new words drop the filters")
            #expect(searches().last?["sort"] as? String == "newest", "and keep the order")
            #expect(model.filters.isEmpty)
        }

        /// A chosen bucket that the narrowed answer no longer lists stays, chosen, with no
        /// count, a library still by its name.
        @MainActor @Test func aChosenBucketTheAnswerNoLongerListsStays() async throws {
            try BrowseHome.answer("POST", "/api/search", "search-help")
            try BrowseHome.answer("/api/search/recent", "search-recent")
            let model = SearchModel(client: BrowseHome.client().0, signedOut: {})
            await model.submit("المدرصة")
            // the narrowed answer found nothing: no facets
            BrowseStub.on("POST", "/api/search", (200, #"{"search_id":"s2","q":"المدرصة","videos":[],"transcript_videos":[]}"#))
            await model.run(try #require(model.ask(toggling: .library, Self.lib2)))
            #expect(model.results?.isEmpty == true && model.results?.facets == nil)
            let shown = model.shownFacets
            #expect(shown.map(\.facet) == [.library])
            let b = try #require(shown.first?.buckets.first)
            #expect(b.value == Self.lib2 && b.chosen && b.count == nil && b.label == "دروس السيرة")
        }

        /// Try again asks the failed search again, its order and filters too.
        @MainActor @Test func tryAgainKeepsTheOrderAndFilters() async throws {
            try BrowseHome.answer("POST", "/api/search", "search-help")
            try BrowseHome.answer("/api/search/recent", "search-recent")
            let model = SearchModel(client: BrowseHome.client().0, signedOut: {})
            await model.submit("المدرصة")
            BrowseStub.on("POST", "/api/search", (500, #"{"title":"boom","status":500}"#), (200, try browseFixture("search-help")))
            await model.run(try #require(model.ask(toggling: .speaker, "الشيخ أحمد")))
            #expect(model.problem != nil)
            await model.retry()
            #expect(model.problem == nil)
            #expect((searches().last?["filters"] as? [String: Any])?["speaker"] as? [String] == ["الشيخ أحمد"])
        }

        @MainActor @Test func theModelsSuggestionsHaveTheirKinds() async throws {
            try BrowseHome.answer("/api/search/suggest", "search-suggest-items")
            let model = SearchModel(client: BrowseHome.client().0, signedOut: {})
            model.query = "المدر"
            await model.suggest("المدر")
            #expect(model.suggestions.map(\.kind) == [.history, .term, .title, .speaker])
        }

        // MARK: words

        @Test func theBucketsAreNamedInEachLanguage() {
            #expect(plain(SearchWords.bucket(.duration, "under_5m", label: nil, en)) == "Under 5 min")
            #expect(plain(SearchWords.bucket(.duration, "5m_20m", label: nil, en)) == "5 to 20 min")
            #expect(plain(SearchWords.bucket(.duration, "20m_60m", label: nil, en)) == "20 to 60 min")
            #expect(SearchWords.bucket(.duration, "over_60m", label: nil, en) == "Over an hour")
            #expect(SearchWords.bucket(.duration, "over_60m", label: nil, ar) == "أكثر من ساعة")
            #expect(plain(SearchWords.bucket(.duration, "under_5m", label: nil, arSA)) == "أقل من ٥ دقائق")
            #expect(plain(SearchWords.bucket(.duration, "5m_20m", label: nil, arSA)) == "من ٥ إلى ٢٠ دقيقة")
            #expect(SearchWords.bucket(.year, "2024", label: nil, en) == "2024")
            #expect(SearchWords.bucket(.year, "2024", label: nil, arSA) == "٢٠٢٤")
            #expect(SearchWords.bucket(.language, "ar", label: nil, en) == "Arabic")
            #expect(SearchWords.bucket(.language, "ar", label: nil, ar) == "العربية")
            #expect(SearchWords.bucket(.language, "zz-unknown", label: nil, en) == "zz-unknown")
            #expect(SearchWords.bucket(.library, BrowseHome.lib, label: "Tafsir Lessons", en) == "Tafsir Lessons")
            #expect(SearchWords.bucket(.library, BrowseHome.lib, label: nil, en) == BrowseHome.lib)
            #expect(plain(SearchWords.inUse(.speaker, "الشيخ أحمد", label: nil, ar)) == "المتحدث: الشيخ أحمد")
            #expect(SearchWords.inUse(.library, BrowseHome.lib, label: "Lectures", ar) == "المكتبة: \u{2068}Lectures\u{2069}",
                    "a Latin name isolated in an Arabic line")
            #expect(plain(SearchWords.videos(1, en)) == "1 video" && plain(SearchWords.videos(3, en)) == "3 videos")
            #expect(plain(SearchWords.videos(3, arSA)) == "عدد المقاطع: ٣")
        }

        @Test func theKindsSortsAndDidYouMeanInEachLanguage() {
            #expect(SuggestionKind.allCases.map { SearchWords.kind($0, en) } == ["Recent", "Word", "Video", "Speaker"])
            #expect(SuggestionKind.allCases.map { SearchWords.kind($0, ar) } == ["بحثت عنه", "كلمة", "مقطع", "متحدث"])
            #expect(SearchSort.allCases.map { SearchWords.sort($0, en) } == ["Relevance", "Newest", "Most matches"])
            #expect(SearchSort.allCases.map { SearchWords.sort($0, ar) } == ["الأقرب صلة", "الأحدث", "الأكثر مطابقات"])
            #expect(SearchFacet.allCases.map { SearchWords.facet($0, ar) } == ["المكتبة", "اللغة", "المتحدث", "السنة", "المدة"])
            #expect(plain(SearchWords.didYouMean("school", en)) == "Did you mean school?")
            #expect(plain(SearchWords.didYouMean("المدرسة", ar)) == "هل تقصد المدرسة؟")
            let parts = SearchWords.didYouMeanParts(ar)
            #expect(parts.before == "هل تقصد " && parts.after == "؟")
            #expect(plain(SearchWords.sortedBy(.newest, ar)) == "الترتيب حسب: الأحدث")
        }
    }
}
