package com.hamzalabs.maktaba.player.search

import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.FakeHome.Companion.json
import mockwebserver3.RecordedRequest

/**
 * The home's search answers for Search's tests, in the shapes `/api/search*` sends (the
 * client's recorded answers in `client/src/test/resources/browse/search*.json`), English or
 * Arabic. No real home is involved.
 */
object SearchFixtures {
    /** A query and what it finds, in one language. */
    data class Lang(
        val query: String,
        val suggestion: String,
        val titles: List<Pair<String, String>>,
        val library: String,
        val transcriptTitle: String,
        val passages: List<Pair<Double, String>>,
        val recent: List<String>,
        /** a named speaker, a second library and a misspelling of [query] (story 49.11) */
        val speaker: String,
        val otherLibrary: String,
        val misspelt: String,
    )

    val EN = Lang(
        query = "patience",
        suggestion = "patience in hardship",
        titles = listOf("v1" to "On <mark>Patience</mark>", "v2" to "The Book of <mark>Patience</mark>, part 2"),
        library = "Lessons",
        transcriptTitle = "The Night Journey",
        passages = listOf(
            754.0 to "and so <mark>patience</mark> is half of faith, as the scholars said",
            1322.5 to "the reward of <mark>patience</mark> comes without measure",
        ),
        recent = listOf("patience", "the night journey"),
        speaker = "Shaykh Ahmad",
        otherLibrary = "Talks",
        misspelt = "patiance",
    )

    val AR = Lang(
        query = "الصَّبر",
        suggestion = "الصبر عند البلاء",
        titles = listOf("v1" to "في <mark>الصبر</mark>", "v2" to "كتاب <mark>الصبر</mark>، الجزء ٢"),
        library = "دروس",
        transcriptTitle = "الإسراء والمعراج",
        passages = listOf(
            754.0 to "و<mark>الصبر</mark> نصف الإيمان كما قال العلماء",
            1322.5 to "أجر <mark>الصبر</mark> بغير حساب",
        ),
        recent = listOf("الصبر", "الإسراء والمعراج"),
        speaker = "الشيخ أحمد",
        otherLibrary = "محاضرات",
        misspelt = "الصبب",
    )

    const val SEARCH_ID = "ffffffff-0000-4000-8000-000000000001"

    private fun esc(s: String) = s.replace("\\", "\\\\").replace("\"", "\\\"")

    fun title(id: String, snippet: String, library: String) =
        """{"video_id":"$id","title":"${esc(snippet.replace("<mark>", "").replace("</mark>", ""))}","library_id":"l1","library_name":"${esc(library)}","poster_url":"/api/videos/$id/poster","score":0.6,"in_transcripts":false,"title_snippet":"${esc(snippet)}","duration_sec":2400}"""

    fun transcript(id: String, title: String, library: String, passages: List<Pair<Double, String>>) =
        """{"video_id":"$id","title":"${esc(title)}","duration_sec":5400,"library_id":"l1","library_name":"${esc(library)}","poster_url":"/api/videos/$id/poster","best_score":0.9,"matches":${passages.size + 5},"passages":[${
            passages.mapIndexed { i, (t, s) -> """{"segment_id":${i + 1},"start_sec":$t,"end_sec":${t + 6},"snippet":"${esc(s)}","score":0.8}""" }.joinToString(",")
        }],"in_title":false}"""

    /** The libraries' ids in the facets. */
    const val LIB = "l1"
    const val OTHER_LIB = "l2"

    /** Story 49.8's facets for [l]: two libraries, two languages, a speaker, two years, two bands. */
    fun facets(l: Lang) = """{
        "library":[{"value":"$LIB","label":"${esc(l.library)}","count":3},{"value":"$OTHER_LIB","label":"${esc(l.otherLibrary)}","count":1}],
        "language":[{"value":"en","count":3},{"value":"ar","count":1}],
        "tag":[],
        "speaker":[{"value":"${esc(l.speaker)}","count":2}],
        "year":[{"value":"2024","count":3,"filter":{"date":{"gte":"2024-01-01T00:00:00.000Z","lt":"2025-01-01T00:00:00.000Z"}}},
                {"value":"2023","count":1,"filter":{"date":{"gte":"2023-01-01T00:00:00.000Z","lt":"2024-01-01T00:00:00.000Z"}}}],
        "duration":[{"value":"20m_60m","count":2,"filter":{"duration_sec":{"gte":1200,"lt":3600}}},
                    {"value":"over_60m","count":2,"filter":{"duration_sec":{"gte":3600}}}],
        "videos":4
    }"""

    /**
     * `POST /api/search`'s answer for [l]: its titles (of [titlesTotal]) and one transcript video,
     * with the order [sort], the facets and [didYouMean] (story 49.8) unless [help] is off (a home
     * before 49.8).
     */
    fun results(
        l: Lang,
        titlesTotal: Int = l.titles.size,
        degraded: Boolean = false,
        help: Boolean = true,
        sort: String = "relevance",
        didYouMean: String? = null,
    ) = """{
        "search_id":"$SEARCH_ID","expires_at":"2026-11-01T10:00:00Z","q":"${esc(l.query)}","mode":"hybrid","filters":{},
        "degraded":$degraded,"page_size":24,"passages_page_size":5,
        "videos":[${l.titles.joinToString(",") { (id, s) -> title(id, s, l.library) }}],"videos_total":$titlesTotal,
        "transcript_videos":[${transcript("v4", l.transcriptTitle, l.library, l.passages)}],"transcript_total":1${
        if (help) ""","sort":"$sort","facets":${facets(l)}${didYouMean?.let { ""","did_you_mean":"${esc(it)}"""" } ?: ""}""" else ""
    }
    }"""

    /** The request's body, as JSON. */
    fun body(r: RecordedRequest) = kotlinx.serialization.json.Json.parseToJsonElement(r.body?.utf8() ?: "{}") as kotlinx.serialization.json.JsonObject

    private fun text(r: RecordedRequest, key: String) =
        (body(r)[key] as? kotlinx.serialization.json.JsonPrimitive)?.content

    /** Answers as a 49.8 home does: the order asked, and did-you-mean for [Lang.misspelt]. */
    fun answering(l: Lang): (RecordedRequest) -> String = { r ->
        val q = text(r, "q")
        results(l.copy(query = q ?: l.query), sort = text(r, "sort") ?: "relevance", didYouMean = if (q == l.misspelt) l.query else null)
    }

    const val EMPTY = """{"search_id":"","q":"nothing","mode":"hybrid","videos":[],"videos_total":0,"transcript_videos":[],"transcript_total":0}"""

    fun recent(l: Lang) = """{"items":[${
        l.recent.mapIndexed { i, q -> """{"search_id":"r$i","q":"${esc(q)}","mode":"hybrid","last_used_at":"2026-10-31T09:00:00Z","title_count":${i + 2},"transcript_count":${i + 7},"live":true}""" }.joinToString(",")
    }]}"""

    /** `GET /api/search/suggest`: the texts, and each with its kind (story 49.8) unless [kinds] is off. */
    fun suggest(l: Lang, kinds: Boolean = true): String {
        val items = listOf(
            "history" to l.recent[0],
            "term" to l.suggestion,
            "title" to plainTitle(l.titles[0].second),
            "speaker" to l.speaker,
        )
        val texts = if (kinds) items.map { it.second } else listOf(l.suggestion)
        val its = if (kinds) {
            ""","items":[${items.joinToString(",") { (k, t) -> """{"text":"${esc(t)}","kind":"$k"${if (k == "title") ""","video_id":"${l.titles[0].first}"""" else ""}}""" }}]"""
        } else {
            ""
        }
        return """{"suggestions":[${texts.joinToString(",") { "\"${esc(it)}\"" }}]$its}"""
    }

    fun plainTitle(snippet: String) = snippet.replace("<mark>", "").replace("</mark>", "")

    /** Answers every search route on [home] for [l]. */
    fun install(home: FakeHome, l: Lang = EN, kinds: Boolean = true, results: (RecordedRequest) -> String = answering(l)) {
        home.answer("/api/search") { json(results(it)) }
        home.answer("/api/search/suggest") { json(suggest(l, kinds)) }
        home.answer("/api/search/recent") { json(if (it.method == "DELETE") "" else recent(l), if (it.method == "DELETE") 204 else 200) }
        l.recent.indices.forEach { i -> home.answer("/api/search/recent/r$i") { json("", 204) } }
        home.answer("/api/search/results/$SEARCH_ID") {
            json("""{"search_id":"$SEARCH_ID","q":"${esc(l.query)}","list":"titles","offset":2,"limit":24,"total":3,"items":[${title("v3", "Third on <mark>patience</mark>", l.library)}]}""")
        }
    }
}
