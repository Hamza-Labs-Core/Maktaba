' Epic 49 story 49.11: Search's help on brs-engine (source/search.brs): the request body built
' from the order and the facet choices, the suggestion kinds, the answer's new fields and their
' absence from an older home, and the lines the screen shows, in English and Arabic.
sub Main()
    m.pass = 0
    m.fail = 0
    t_Body()
    t_Toggle()
    t_Suggestions()
    t_Answer()
    t_FacetRows()
    t_Lines()
    ck_Result()
end sub

function t_Facets() as Object
    return ParseJson("{""library"":[{""value"":""L1"",""label"":""Tafsir"",""count"":3}],""language"":[{""value"":""ar"",""count"":2},{""value"":""xx"",""count"":1}],""speaker"":[{""value"":""Ahmad"",""count"":2}],""year"":[{""value"":""2024"",""count"":2,""filter"":{""date"":{""gte"":""2024-01-01T03:00:00.000Z"",""lt"":""2025-01-01T03:00:00.000Z""}}}],""duration"":[{""value"":""5m_20m"",""count"":1}],""videos"":3}")
end function

sub t_Body()
    ' relevance and no filters: the request a home before 49.8 reads
    b = SearchBody("ab", "relevance", SearchNoSel(), invalid)
    ck_Eq("plain body", b, {q: "ab", mode: "hybrid", view: "grid"})
    ck_True("no sort", not b.DoesExist("sort"))
    ck_True("no filters", not b.DoesExist("filters"))

    ' each facet's filter; lists combine; a year sends its bucket's own filter
    sel = SearchNoSel()
    sel = SearchToggle(sel, "library", "L1")
    sel = SearchToggle(sel, "library", "L2")
    sel = SearchToggle(sel, "language", "ar")
    sel = SearchToggle(sel, "speaker", "Ahmad")
    sel = SearchToggle(sel, "year", "2024")
    sel = SearchToggle(sel, "duration", "5m_20m")
    b = SearchBody("ab", "newest", sel, t_Facets())
    ck_Eq("sort sent", b.sort, "newest")
    ck_Eq("libraries", b.filters.library_id, ["L1", "L2"])
    ck_Eq("language", b.filters.language, ["ar"])
    ck_Eq("speaker", b.filters.speaker, ["Ahmad"])
    ck_Eq("year from its bucket", b.filters.date.gte, "2024-01-01T03:00:00.000Z")
    ck_Eq("year end from its bucket", b.filters.date.lt, "2025-01-01T03:00:00.000Z")
    ck_Eq("band from the server's ranges", b.filters.duration_sec, {gte: 300, lt: 1200})

    ' a year and a band the facets on screen do not list: the ranges the server gives them
    sel = SearchToggle(SearchToggle(SearchNoSel(), "year", "2020"), "duration", "over_60m")
    b = SearchBody("ab", "matches", sel, invalid)
    ck_Eq("year range", b.filters.date, {gte: "2020-01-01T00:00:00.000Z", lt: "2021-01-01T00:00:00.000Z"})
    ck_Eq("over an hour", b.filters.duration_sec, {gte: 3600})
    ck_Eq("matches", b.sort, "matches")
    ck_Eq("bands", [SearchDurationRange("under_5m"), SearchDurationRange("20m_60m"), SearchDurationRange("forever")], [{gte: 0, lt: 300}, {gte: 1200, lt: 3600}, invalid])
    ck_Eq("a short year", SearchYearRange("999").gte, "0999-01-01T00:00:00.000Z")
    ck_Eq("FormatJson of the body", ParseJson(FormatJson(b)).filters.duration_sec.gte, 3600)
end sub

sub t_Toggle()
    sel = SearchToggle(SearchNoSel(), "language", "ar")
    ck_True("has ar", SearchHas(sel, "language", "ar"))
    sel = SearchToggle(sel, "language", "ar")
    ck_Eq("ar off again", sel.language, [])
    sel = SearchToggle(SearchNoSel(), "year", "2023")
    sel = SearchToggle(sel, "year", "2024")
    ck_Eq("a second year replaces the first", sel.year, "2024")
    ck_Eq("one filter", SearchSelCount(sel), 1)
    sel = SearchToggle(sel, "year", "2024")
    ck_Eq("the year off", sel.year, "")
    sel = SearchToggle(SearchToggle(SearchNoSel(), "speaker", "A"), "duration", "under_5m")
    ck_Eq("two filters", SearchSelCount(sel), 2)
    before = SearchNoSel()
    after = SearchToggle(before, "library", "L1")
    ck_Eq("toggle copies", before.library, [])
    ck_Eq("toggle copies, the new one", after.library, ["L1"])
end sub

sub t_Suggestions()
    a = ParseJson("{""suggestions"":[""x""],""items"":[{""text"":""patience in islam"",""kind"":""history""},{""text"":""patience"",""kind"":""term""},{""text"":""Patience and prayer"",""kind"":""title"",""video_id"":""v9""},{""text"":""No video"",""kind"":""title""},{""text"":""Patrick"",""kind"":""speaker""},{""text"":""patience"",""kind"":""term""},{""text"":""odd"",""kind"":""other""},{""text"":"" ""}]}")
    s = SearchSuggestions(a, 6)
    kinds = []
    for each it in s
        kinds.Push(it.kind)
    end for
    ck_Eq("kinds, a title with no video and a repeat dropped", kinds, ["history", "term", "title", "speaker", ""])
    ck_Eq("a title's video", s[2].videoId, "v9")
    ck_Eq("an unknown kind is none", s[4].text, "odd")
    ' a home before 49.8: plain texts
    s = SearchSuggestions(ParseJson("{""suggestions"":[""abc"","""",""abd""]}"), 6)
    ck_Eq("plain texts", s, [{text: "abc", kind: "", videoId: ""}, {text: "abd", kind: "", videoId: ""}])
    ck_Eq("nothing", SearchSuggestions(invalid, 6), [])
    ck_Eq("an empty answer", SearchSuggestions({}, 6), [])
    ' at most SearchSuggestMax(): eight plain texts give six
    eight = ParseJson("{""suggestions"":[""a1"",""a2"",""a3"",""a4"",""a5"",""a6"",""a7"",""a8""]}")
    s = SearchSuggestions(eight, SearchSuggestMax())
    ck_Eq("six at most", s.Count(), 6)
    ck_Eq("the first six", s[5].text, "a6")
end sub

sub t_Answer()
    a = ParseJson("{""did_you_mean"":"" patience "",""facets"":{""videos"":1},""videos"":[{""video_id"":""v1"",""title"":""A""}],""transcript_videos"":[{""video_id"":""v1"",""title"":""A""},{""video_id"":""v2"",""title"":""B""},{""title"":""no id""}]}")
    ck_Eq("did you mean", SearchDidYouMean(a, "patiense"), "patience")
    ck_Eq("the same words are no correction", SearchDidYouMean(a, "patience"), "")
    ck_Eq("facets", SearchFacets(a).videos, 1)
    r = SearchResults(a)
    ck_Eq("titles, then transcripts, each once", [r[0].video_id, r[1].video_id], ["v1", "v2"])
    ck_Eq("count", r.Count(), 2)
    ' an older home: none of the new fields
    old = ParseJson("{""videos"":[{""video_id"":""v1"",""title"":""A""}]}")
    ck_Eq("no did you mean", SearchDidYouMean(old, "ab"), "")
    ck_Eq("no facets", SearchFacets(old), invalid)
    ck_Eq("no rows", SearchFacetRows(SearchFacets(old), SearchNoSel(), {}), [])
    ck_Eq("results", SearchResults(old).Count(), 1)
    ck_Eq("a failed answer", SearchResults(invalid), [])
end sub

sub t_FacetRows()
    labels = {}
    SearchKeepLabels(t_Facets(), labels)
    ck_Eq("a library's name kept", labels["library:L1"], "Tafsir")
    sel = SearchToggle(SearchToggle(SearchNoSel(), "library", "L1"), "year", "2019")
    rows = SearchFacetRows(t_Facets(), sel, labels)
    out = []
    for each r in rows
        out.Push(r.facet + ":" + r.value + ":" + Int(r.count).ToStr() + ":" + r.chosen.ToStr())
    end for
    ck_Eq("rows in facet order, a dropped choice first without a count", out, ["library:L1:3:true", "language:ar:2:false", "language:xx:1:false", "speaker:Ahmad:2:false", "year:2019:-1:true", "year:2024:2:false", "duration:5m_20m:1:false"])
    ' the narrowed answer lists no library: the chosen one stays, with its name
    rows = SearchFacetRows({}, sel, labels)
    ck_Eq("a chosen library the answer dropped", rows[0], {facet: "library", value: "L1", label: "Tafsir", count: -1, chosen: true})
end sub

sub t_Lines()
    en = I18nNew("en")
    ck_Eq("a library by its label", SearchBucketName(en, "library", "L1", "Tafsir"), "Tafsir")
    ck_Eq("a library without one", SearchBucketName(en, "library", "L1", ""), "L1")
    ck_Eq("a language by its name", SearchBucketName(en, "language", "ar", ""), "Arabic")
    ck_Eq("an unknown language by its code", SearchBucketName(en, "language", "xx", ""), "xx")
    ck_Eq("a band", SearchBucketName(en, "duration", "5m_20m", ""), "5 to 20 min")
    ck_Eq("a year", SearchBucketName(en, "year", "2024", ""), "2024")
    ck_Eq("an option", SearchFacetOption(en, {facet: "library", value: "L1", label: "Tafsir", count: 3, chosen: false}), "Library: Tafsir · 3")
    ck_Eq("a chosen option", SearchFacetOption(en, {facet: "duration", value: "over_60m", label: "", count: 2, chosen: true}), "✓ Length: Over an hour · 2")
    ck_Eq("a chosen one the answer dropped", SearchFacetOption(en, {facet: "year", value: "2019", label: "", count: -1, chosen: true}), "✓ Year: 2019")
    ck_Eq("a suggestion with its kind", SearchSuggestionText(en, {text: "patience", kind: "term", videoId: ""}), "patience  ·  Word")
    ck_Eq("a title", SearchSuggestionText(en, {text: "Patience and prayer", kind: "title", videoId: "v9"}), "Patience and prayer  ·  Video")
    ck_Eq("an older home's", SearchSuggestionText(en, {text: "abc", kind: "", videoId: ""}), "abc")
    ck_Eq("did you mean", en.Format("search.didYouMean", ["patience"]), "Did you mean patience?")

    ' Arabic: shaped and right to left, the kind's word and the mark at the reading start
    ar = I18nNew("ar")
    ck_Eq("a language in Arabic", SearchBucketName(ar, "language", "ar", ""), "العربية")
    ck_Eq("a band in Arabic", SearchBucketName(ar, "duration", "over_60m", ""), "أكثر من ساعة")
    line = SearchSuggestionText(ar, {text: "الصبر", kind: "history", videoId: ""})
    ck_Eq("an Arabic suggestion is shaped", line, VisualLines("الصبر  ·  بحثت عنه", 34, 1, "rtl").Join(Chr(10)))
    opt = SearchFacetOption(ar, {facet: "year", value: "2024", label: "", count: 2, chosen: true})
    ck_Eq("an Arabic option is shaped", opt, VisualLines("✓ السنة: 2024 · 2", 30, 1, "rtl").Join(Chr(10)))
    ck_True("its mark at the reading start, the right", Right(opt, 1) = "✓")
    ck_True("Did you mean in Arabic", ar.Format("search.didYouMean", ["المدرسة"]) = VisualLines("هل تقصد المدرسة؟", 56, 1, "rtl").Join(Chr(10)))
end sub
