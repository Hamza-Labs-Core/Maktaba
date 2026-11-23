package com.hamzalabs.maktaba.core

/**
 * AI mode's answers (story 49.14) for the apps' tests, as the home writes them
 * (`api/internal/handlers/search/ai.go`'s `send`: `event: …`, `data: …`, a blank line, and
 * `: ping` comments). A stream is not JSON, so it is built here rather than kept as a file
 * under `browse/`.
 */
object AiStreams {
    /** A passage the model read: its video, title, moment and words. */
    data class Passage(val n: Int, val videoId: String, val title: String, val startSec: Double, val text: String)

    /** A sentence of the answer and the passages it cites, by number. */
    data class Sentence(val text: String, val cites: List<Int>)

    private fun esc(s: String) = s.replace("\\", "\\\\").replace("\"", "\\\"")

    private fun event(name: String, data: String) = "event: $name\ndata: $data\n\n"

    /** The `sources` event. */
    fun sources(q: String, passages: List<Passage>): String = event(
        "sources",
        """{"q":"${esc(q)}","passages":[${passages.joinToString(",") { p ->
            """{"n":${p.n},"video_id":"${p.videoId}","title":"${esc(p.title)}","library_id":"l1","library_name":"Lessons","poster_url":"/api/videos/${p.videoId}/poster","segment_id":${p.n},"start_sec":${p.startSec},"end_sec":${p.startSec + 6},"text":"${esc(p.text)}"}"""
        }}]}""",
    )

    /** One `text` event, its citations taken from [passages]. */
    fun text(s: Sentence, passages: List<Passage>): String = event(
        "text",
        """{"text":"${esc(s.text)}","citations":[${s.cites.mapNotNull { n -> passages.firstOrNull { it.n == n } }.joinToString(",") { p ->
            """{"n":${p.n},"video_id":"${p.videoId}","start_sec":${p.startSec},"end_sec":${p.startSec + 6},"quote":"${esc(p.text)}"}"""
        }}]}""",
    )

    /** The `done` event. */
    fun done(answered: Boolean, sentences: Int, reason: String = ""): String = event(
        "done",
        """{"answered":$answered${if (reason.isNotEmpty()) ""","reason":"$reason"""" else ""},"sentences":$sentences,"dropped":0,"first_token_ms":120,"took_ms":480,"model":"qwen2.5:7b"}""",
    )

    fun queue(position: Int): String = event("queue", """{"position":$position}""")

    fun error(code: String): String = event("error", """{"code":"$code"}""")

    const val PING = ": ping\n\n"

    /** A whole answer: a ping, the sources, each sentence, done. */
    fun answer(q: String, passages: List<Passage>, sentences: List<Sentence>): String =
        PING + sources(q, passages) + sentences.joinToString("") { text(it, passages) } + done(sentences.isNotEmpty(), sentences.size)

    /** The passages do not answer: the sources, then done unanswered. */
    fun noAnswer(q: String, passages: List<Passage>): String =
        sources(q, passages) + done(false, 0, if (passages.isEmpty()) "no-passages" else "not-in-passages")

    /** The 409 a home without a language model answers. */
    const val UNAVAILABLE =
        """{"type":"https://maktaba.local/problems/ai-unavailable","title":"no language model","status":409,"detail":"this home has no language model to answer with","code":"ai-unavailable","help":{"settings":"/settings/rules?section=tagcheck&q=url","help":"/help/search#ai-mode"}}"""

    /** The 409 when an admin turned AI mode off. */
    const val OFF = """{"type":"https://maktaba.local/problems/ai-off","title":"AI mode is off","status":409,"code":"ai-off"}"""

    /** The 429 when the line is full. */
    const val BUSY = """{"type":"https://maktaba.local/problems/ai-busy","title":"AI mode is busy","status":429,"code":"ai-busy","position":9}"""

    /** The English answer the apps' tests show: two passages of one video, two sentences. */
    val EN_PASSAGES = listOf(
        Passage(1, "v4", "The Night Journey", 754.0, "and so patience is half of faith, as the scholars said"),
        Passage(2, "v4", "The Night Journey", 1322.5, "the reward of patience comes without measure"),
    )
    val EN_SENTENCES = listOf(
        Sentence("Patience is half of faith.", listOf(1)),
        Sentence("Its reward comes without measure.", listOf(2)),
    )

    /** The Arabic one. */
    val AR_PASSAGES = listOf(
        Passage(1, "v4", "الإسراء والمعراج", 754.0, "والصبر نصف الإيمان كما قال العلماء"),
        Passage(2, "v4", "الإسراء والمعراج", 1322.5, "أجر الصبر بغير حساب"),
    )
    val AR_SENTENCES = listOf(
        Sentence("الصبر نصف الإيمان.", listOf(1)),
        Sentence("وأجره بغير حساب.", listOf(2)),
    )
}
