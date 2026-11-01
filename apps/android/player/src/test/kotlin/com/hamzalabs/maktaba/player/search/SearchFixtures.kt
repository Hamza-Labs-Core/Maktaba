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
    )

    const val SEARCH_ID = "ffffffff-0000-4000-8000-000000000001"

    private fun esc(s: String) = s.replace("\\", "\\\\").replace("\"", "\\\"")

    fun title(id: String, snippet: String, library: String) =
        """{"video_id":"$id","title":"${esc(snippet.replace("<mark>", "").replace("</mark>", ""))}","library_id":"l1","library_name":"${esc(library)}","poster_url":"/api/videos/$id/poster","score":0.6,"in_transcripts":false,"title_snippet":"${esc(snippet)}","duration_sec":2400}"""

    fun transcript(id: String, title: String, library: String, passages: List<Pair<Double, String>>) =
        """{"video_id":"$id","title":"${esc(title)}","duration_sec":5400,"library_id":"l1","library_name":"${esc(library)}","poster_url":"/api/videos/$id/poster","best_score":0.9,"matches":${passages.size + 5},"passages":[${
            passages.mapIndexed { i, (t, s) -> """{"segment_id":${i + 1},"start_sec":$t,"end_sec":${t + 6},"snippet":"${esc(s)}","score":0.8}""" }.joinToString(",")
        }],"in_title":false}"""

    /** `POST /api/search`'s answer for [l]: its titles (of [titlesTotal]) and one transcript video. */
    fun results(l: Lang, titlesTotal: Int = l.titles.size, degraded: Boolean = false) = """{
        "search_id":"$SEARCH_ID","expires_at":"2026-11-01T10:00:00Z","q":"${esc(l.query)}","mode":"hybrid","filters":{},
        "degraded":$degraded,"page_size":24,"passages_page_size":5,
        "videos":[${l.titles.joinToString(",") { (id, s) -> title(id, s, l.library) }}],"videos_total":$titlesTotal,
        "transcript_videos":[${transcript("v4", l.transcriptTitle, l.library, l.passages)}],"transcript_total":1
    }"""

    const val EMPTY = """{"search_id":"","q":"nothing","mode":"hybrid","videos":[],"videos_total":0,"transcript_videos":[],"transcript_total":0}"""

    fun recent(l: Lang) = """{"items":[${
        l.recent.mapIndexed { i, q -> """{"search_id":"r$i","q":"${esc(q)}","mode":"hybrid","last_used_at":"2026-10-31T09:00:00Z","title_count":${i + 2},"transcript_count":${i + 7},"live":true}""" }.joinToString(",")
    }]}"""

    fun suggest(l: Lang) = """{"suggestions":["${esc(l.suggestion)}"]}"""

    /** Answers every search route on [home] for [l]. */
    fun install(home: FakeHome, l: Lang = EN, results: (RecordedRequest) -> String = { results(l) }) {
        home.answer("/api/search") { json(results(it)) }
        home.answer("/api/search/suggest") { json(suggest(l)) }
        home.answer("/api/search/recent") { json(if (it.method == "DELETE") "" else recent(l), if (it.method == "DELETE") 204 else 200) }
        l.recent.indices.forEach { i -> home.answer("/api/search/recent/r$i") { json("", 204) } }
        home.answer("/api/search/results/$SEARCH_ID") {
            json("""{"search_id":"$SEARCH_ID","q":"${esc(l.query)}","list":"titles","offset":2,"limit":24,"total":3,"items":[${title("v3", "Third on <mark>patience</mark>", l.library)}]}""")
        }
    }
}
