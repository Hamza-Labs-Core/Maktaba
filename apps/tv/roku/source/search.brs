' Search's help (Epic 49 story 49.11, on the API of story 49.8, as the web's Search has it in
' 49.10): what SearchScreen asks the home and reads from its answers, kept apart from the screen
' so brs-engine tests it (tests/search_test.brs).
'
'   sel = SearchNoSel()                       ' the facet buckets chosen: library, language and
'                                             ' speaker are lists (any of them), a year and a
'                                             ' duration band one each ("" for none)
'   sel = SearchToggle(sel, "library", "L1")  ' a list's bucket joins or leaves; a year or band
'                                             ' replaces the last
'   SearchBody(q, sort, sel, facets)          ' the POST /api/search body; relevance and no
'                                             ' filters leave their fields out
'   SearchSuggestions(answer, max)            ' GET /api/search/suggest: [{text, kind, videoId}]
'   SearchDidYouMean(answer, q)               ' the corrected query, or ""
'   SearchFacets(answer)                      ' the answer's facets, or invalid (an older home)
'   SearchFacetRows(facets, sel)              ' the buckets to list, facet by facet
'   SearchResults(answer)                     ' the videos found: titles, then transcripts

' The facets in the order they are listed, and the order the home may sort by.
function SearchFacetNames() as Object
    return ["library", "language", "speaker", "year", "duration"]
end function

function SearchSorts() as Object
    return ["relevance", "newest", "matches"]
end function

function SearchNoSel() as Object
    return {library: [], language: [], speaker: [], year: "", duration: ""}
end function

function search_IsList(facet as String) as Boolean
    return facet = "library" or facet = "language" or facet = "speaker"
end function

function SearchSelCount(sel as Object) as Integer
    n = sel.library.Count() + sel.language.Count() + sel.speaker.Count()
    if sel.year <> "" then n = n + 1
    if sel.duration <> "" then n = n + 1
    return n
end function

function SearchHas(sel as Object, facet as String, value as String) as Boolean
    if search_IsList(facet) then
        for each v in sel[facet]
            if v = value then return true
        end for
        return false
    end if
    return sel[facet] = value
end function

' A copy of sel with the bucket toggled.
function SearchToggle(sel as Object, facet as String, value as String) as Object
    out = {library: [], language: [], speaker: [], year: sel.year, duration: sel.duration}
    for each f in ["library", "language", "speaker"]
        for each v in sel[f]
            if not (f = facet and v = value) then out[f].Push(v)
        end for
    end for
    if search_IsList(facet) then
        if not SearchHas(sel, facet, value) then out[facet].Push(value)
    else if facet = "year" or facet = "duration" then
        if sel[facet] = value then out[facet] = "" else out[facet] = value
    end if
    return out
end function

' The seconds of a duration band, as the server names and bounds them
' (api/internal/handlers/search/facets.go), or invalid for an unknown one.
function SearchDurationRange(band as String) as Dynamic
    if band = "under_5m" then return {gte: 0, lt: 300}
    if band = "5m_20m" then return {gte: 300, lt: 1200}
    if band = "20m_60m" then return {gte: 1200, lt: 3600}
    if band = "over_60m" then return {gte: 3600}
    return invalid
end function

' The date range of a year (UTC), as the server's year bucket gives it.
function SearchYearRange(year as String) as Object
    y = year.ToInt()
    return {gte: search_Year4(y) + "-01-01T00:00:00.000Z", lt: search_Year4(y + 1) + "-01-01T00:00:00.000Z"}
end function

function search_Year4(y as Integer) as String
    s = y.ToStr()
    while Len(s) < 4
        s = "0" + s
    end while
    return s
end function

' The bucket of a facet with that value in the facets on screen, or invalid.
function search_Bucket(facets as Dynamic, facet as String, value as String) as Dynamic
    if type(facets) <> "roAssociativeArray" then return invalid
    list = facets[facet]
    if type(list) <> "roArray" then return invalid
    for each b in list
        if type(b) = "roAssociativeArray" and b.value = value then return b
    end for
    return invalid
end function

' The POST /api/search body. A year or band sends its bucket's own filter when the facets on
' screen have it, else the range the server gives that bucket.
function SearchBody(q as String, sort as String, sel as Object, facets as Dynamic) as Object
    body = {q: q, mode: "hybrid", view: "grid"}
    if sort <> "" and sort <> "relevance" then body.sort = sort
    if SearchSelCount(sel) = 0 then return body
    f = {}
    if sel.library.Count() > 0 then f.library_id = sel.library
    if sel.language.Count() > 0 then f.language = sel.language
    if sel.speaker.Count() > 0 then f.speaker = sel.speaker
    if sel.year <> "" then
        f.date = SearchYearRange(sel.year)
        b = search_Bucket(facets, "year", sel.year)
        if b <> invalid and type(b.filter) = "roAssociativeArray" and type(b.filter.date) = "roAssociativeArray" then f.date = b.filter.date
    end if
    if sel.duration <> "" then
        r = SearchDurationRange(sel.duration)
        b = search_Bucket(facets, "duration", sel.duration)
        if b <> invalid and type(b.filter) = "roAssociativeArray" and type(b.filter.duration_sec) = "roAssociativeArray" then r = b.filter.duration_sec
        if r <> invalid then f.duration_sec = r
    end if
    body.filters = f
    return body
end function

function search_IsText(v as Dynamic) as Boolean
    return (type(v) = "roString" or type(v) = "String") and v.Trim() <> ""
end function

' The suggestions of a GET /api/search/suggest answer: its items with their kinds (history,
' term, title, speaker), else its plain texts (a home before story 49.8) with kind "". A title
' with no video is dropped (it opens its video); the same text and kind once; max of them.
' The suggestions the list under the keyboard shows at most.
function SearchSuggestMax() as Integer
    return 6
end function

function SearchSuggestions(answer as Dynamic, max as Integer) as Object
    out = []
    if type(answer) <> "roAssociativeArray" then return out
    kinds = {history: true, term: true, title: true, speaker: true}
    if type(answer.items) = "roArray" and answer.items.Count() > 0 then
        for each it in answer.items
            if type(it) = "roAssociativeArray" and search_IsText(it.text) then
                kind = ""
                if search_IsText(it.kind) and kinds.DoesExist(it.kind) then kind = it.kind
                vid = ""
                if search_IsText(it.video_id) then vid = it.video_id
                out.Push({text: it.text, kind: kind, videoId: vid})
            end if
        end for
    else if type(answer.suggestions) = "roArray" then
        for each s in answer.suggestions
            if search_IsText(s) then out.Push({text: s, kind: "", videoId: ""})
        end for
    end if
    seen = {}
    kept = []
    for each it in out
        k = it.kind + Chr(10) + it.text + Chr(10) + it.videoId
        if kept.Count() < max and not (it.kind = "title" and it.videoId = "") and not seen.DoesExist(k) then
            seen[k] = true
            kept.Push(it)
        end if
    end for
    return kept
end function

' An answer's corrected query, or "" when it has none or repeats the query.
function SearchDidYouMean(answer as Dynamic, q as String) as String
    if type(answer) <> "roAssociativeArray" or not search_IsText(answer.did_you_mean) then return ""
    d = answer.did_you_mean.Trim()
    if d = q.Trim() then return ""
    return d
end function

function SearchFacets(answer as Dynamic) as Dynamic
    if type(answer) <> "roAssociativeArray" or type(answer.facets) <> "roAssociativeArray" then return invalid
    return answer.facets
end function

' The buckets to list, facet by facet: {facet, value, label, count, chosen}. A chosen bucket
' that the narrowed answer no longer lists comes first in its facet with count -1, so it can be
' taken off where it was put on. labels holds names seen before ("library:<id>" -> name).
function SearchFacetRows(facets as Dynamic, sel as Object, labels as Object) as Object
    rows = []
    for each facet in SearchFacetNames()
        listed = []
        if type(facets) = "roAssociativeArray" and type(facets[facet]) = "roArray" then
            for each b in facets[facet]
                if type(b) = "roAssociativeArray" and search_IsText(b.value) then listed.Push(b)
            end for
        end if
        chosen = []
        if search_IsList(facet) then
            chosen = sel[facet]
        else if sel[facet] <> "" then
            chosen = [sel[facet]]
        end if
        for each v in chosen
            if search_Bucket({list: listed}, "list", v) = invalid then
                rows.Push({facet: facet, value: v, label: search_Label(labels, facet, v, invalid), count: -1, chosen: true})
            end if
        end for
        for each b in listed
            count = -1
            if b.count <> invalid then count = b.count
            rows.Push({facet: facet, value: b.value, label: search_Label(labels, facet, b.value, b.label), count: count, chosen: SearchHas(sel, facet, b.value)})
        end for
    end for
    return rows
end function

function search_Label(labels as Object, facet as String, value as String, label as Dynamic) as String
    if search_IsText(label) then return label
    k = facet + ":" + value
    if labels <> invalid and labels.DoesExist(k) then return labels[k]
    return ""
end function

' The library names of an answer's facets, added to labels.
sub SearchKeepLabels(facets as Dynamic, labels as Object)
    if type(facets) <> "roAssociativeArray" or type(facets.library) <> "roArray" then return
    for each b in facets.library
        if type(b) = "roAssociativeArray" and search_IsText(b.value) and search_IsText(b.label) then labels["library:" + b.value] = b.label
    end for
end sub

' The videos found: those found by title, then those found in transcripts, each once.
function SearchResults(answer as Dynamic) as Object
    out = []
    if type(answer) <> "roAssociativeArray" then return out
    seen = {}
    for each key in ["videos", "transcript_videos"]
        list = answer[key]
        if type(list) = "roArray" then
            for each v in list
                if type(v) = "roAssociativeArray" and search_IsText(v.video_id) and not seen.DoesExist(v.video_id) then
                    seen[v.video_id] = true
                    out.Push(v)
                end if
            end for
        end if
    end for
    return out
end function

' The name a bucket is shown by, in logical order (a value for a template): a library's label,
' a language's or a band's name in the channel's language, a year as it is.
function SearchBucketName(i18n as Object, facet as String, value as String, label as String) as String
    if facet = "library" then
        if label <> "" then return label
        return value
    end if
    if facet = "language" or facet = "duration" then
        key = "search." + facet + "." + value
        if i18n.templates[key] <> invalid then return i18n.Fill(key, [])
    end if
    return value
end function

' A facet row as the filter sheet lists it (visual): "Library: Tafsir · 12", a chosen one
' marked ✓ at the reading start, one the narrowed answer dropped without a count.
function SearchFacetOption(i18n as Object, row as Object) as String
    name = i18n.Fill("search.facet." + row.facet, [])
    bucket = SearchBucketName(i18n, row.facet, row.value, row.label)
    if row.count < 0 then
        if row.chosen then return i18n.Format("search.facet.chosenGone", [name, bucket])
        return i18n.Format("search.facet.noCount", [name, bucket])
    end if
    count = Int(row.count).ToStr()
    if row.chosen then return i18n.Format("search.facet.chosen", [name, bucket, count])
    return i18n.Format("search.facet.option", [name, bucket, count])
end function

' A suggestion as the list under the keyboard shows it (visual): its text and its kind's word;
' a home before story 49.8 sends no kind, and its text shows alone.
function SearchSuggestionText(i18n as Object, it as Object) as String
    if it.kind = "" then return i18n.Format("search.suggest.plain", [it.text])
    return i18n.Format("search.suggest.item", [it.text, i18n.Fill("search.kind." + it.kind, [])])
end function
