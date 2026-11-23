package com.hamzalabs.maktaba.core.api

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.channelFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.doubleOrNull
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonArray
import kotlinx.serialization.json.putJsonObject
import java.io.ByteArrayOutputStream
import java.io.IOException

// Story 49.14: AI mode's tab in the Kotlin apps (the Android Player, the Android TV app and the
// Player for Windows and Linux). The home answers `POST /api/search/ai` (story 49.12) as
// Server-Sent Events: the passages it read first (`sources`), then each sentence of the answer
// with the passages it cites (`text`), then `done`. This file reads that stream, once for the
// three apps, and keeps the conversation ([AiConversation]); each app draws it in its own words.
// The guide for client authors: docs/developers/contributing/ai-mode.md.

// ---------- Server-Sent Events ----------

/** One event of a stream: its name (`message` when it named none) and its data lines, joined by `\n`. */
data class SseEvent(val event: String, val data: String)

/**
 * Reads Server-Sent Events from bytes as they come, cut anywhere: inside a line, between a
 * `\r` and its `\n`, or inside a character's UTF-8 bytes. Lines end with `\n`, `\r\n` or `\r`;
 * a blank line ends an event; a line starting with `:` is a comment (the home's `: ping`).
 * Fields other than `event` and `data` are ignored. An event the stream ends inside is dropped,
 * as the format says.
 */
class SseReader {
    private val line = ByteArrayOutputStream()
    private var afterCr = false
    private var started = false
    private var name = ""
    private val data = StringBuilder()
    private var hasData = false

    /** The events completed by [bytes] (from [offset], [length] of them). */
    fun feed(bytes: ByteArray, offset: Int = 0, length: Int = bytes.size - offset): List<SseEvent> {
        val out = ArrayList<SseEvent>(1)
        for (i in offset until offset + length) {
            val b = bytes[i]
            when {
                b == '\n'.code.toByte() && afterCr -> afterCr = false // the \n of a \r\n
                b == '\n'.code.toByte() || b == '\r'.code.toByte() -> {
                    afterCr = b == '\r'.code.toByte()
                    endLine(out)
                }
                else -> {
                    afterCr = false
                    line.write(b.toInt())
                }
            }
        }
        return out
    }

    private fun endLine(out: MutableList<SseEvent>) {
        var text = line.toString(Charsets.UTF_8)
        line.reset()
        if (!started) {
            started = true
            text = text.removePrefix("﻿")
        }
        if (text.isEmpty()) {
            if (hasData) out += SseEvent(name.ifEmpty { "message" }, data.toString())
            name = ""
            data.setLength(0)
            hasData = false
            return
        }
        if (text.startsWith(":")) return
        val colon = text.indexOf(':')
        val field = if (colon < 0) text else text.substring(0, colon)
        val value = if (colon < 0) "" else text.substring(colon + 1).removePrefix(" ")
        when (field) {
            "event" -> name = value
            "data" -> {
                if (hasData) data.append('\n')
                data.append(value)
                hasData = true
            }
        }
    }
}

// ---------- what the home sends ----------

/** A passage the home's model read (`sources`): the video, its moment and its own words. */
@Serializable
data class AiPassage(
    val n: Int = 0,
    @SerialName("video_id") val videoId: String = "",
    val title: String = "",
    @SerialName("library_name") val libraryName: String = "",
    @SerialName("poster_url") val posterUrl: String = "",
    @SerialName("start_sec") val startSec: Double = 0.0,
    @SerialName("end_sec") val endSec: Double = 0.0,
    val text: String = "",
)

/** Where a sentence comes from: a passage by its [n], the moment the player opens at and the words quoted. */
@Serializable
data class AiCitation(
    val n: Int = 0,
    @SerialName("video_id") val videoId: String = "",
    @SerialName("start_sec") val startSec: Double = 0.0,
    @SerialName("end_sec") val endSec: Double = 0.0,
    val quote: String = "",
)

/** One sentence of the answer (`text`) and the passages it cites. */
@Serializable
data class AiSentence(
    val text: String = "",
    @Serializable(with = LenientList::class) val citations: List<AiCitation> = emptyList(),
)

/** The end of an answer (`done`). */
@Serializable
data class AiDone(
    /** false: the passages do not answer the question; they stay shown */
    val answered: Boolean = false,
    /** `no-passages`, `not-in-passages` or `timeout`; empty when answered in full */
    val reason: String = "",
    val sentences: Int = 0,
    val dropped: Int = 0,
    @SerialName("first_token_ms") val firstTokenMs: Long = 0,
    @SerialName("took_ms") val tookMs: Long = 0,
)

@Serializable
private data class AiSourcesDoc(val q: String = "", @Serializable(with = LenientList::class) val passages: List<AiPassage> = emptyList())

@Serializable
private data class AiQueueDoc(val position: Int = 0)

@Serializable
private data class AiErrorDoc(val code: String = "")

/** An event of AI mode's stream. */
sealed interface AiEvent {
    /** The question waits in line: [position] 1 is next. */
    data class Queue(val position: Int) : AiEvent

    /** The passages the model reads, numbered: shown at once. */
    data class Sources(val q: String, val passages: List<AiPassage>) : AiEvent

    /** A sentence of the answer. */
    data class Text(val sentence: AiSentence) : AiEvent

    /** The end. */
    data class Done(val done: AiDone) : AiEvent

    /** The answer stopped (`retrieval-failed`, `model-failed`); what was sent stands. */
    data class Error(val code: String) : AiEvent

    companion object {
        /**
         * [e] read leniently: an event this app does not know, or data it cannot read, is
         * null (left out), so a newer home's additions never stop the answer. A sentence
         * with no words is left out; a citation without a video too.
         */
        fun of(e: SseEvent): AiEvent? = runCatching {
            val json = ApiClient.json
            when (e.event) {
                "queue" -> Queue(json.decodeFromString<AiQueueDoc>(e.data).position)
                "sources" -> json.decodeFromString<AiSourcesDoc>(e.data).let { d ->
                    Sources(d.q, d.passages.filter { it.videoId.isNotBlank() })
                }
                "text" -> json.decodeFromString<AiSentence>(e.data).let { s ->
                    if (s.text.isBlank()) null else Text(s.copy(citations = s.citations.filter { it.videoId.isNotBlank() }))
                }
                "done" -> Done(json.decodeFromString<AiDone>(e.data))
                "error" -> Error(json.decodeFromString<AiErrorDoc>(e.data).code)
                else -> null
            }
        }.getOrNull()
    }
}

// ---------- when there is no answer to write ----------

/** Why the home writes no answer: each has its own sentence, none is an error. */
sealed interface AiRefusal {
    /** 409 `ai-unavailable`: no language model, or it is switched off. [settings] and [help] are paths in the web app. */
    data class Unavailable(val settings: String = AI_SETTINGS_PATH, val help: String = AI_HELP_PATH) : AiRefusal

    /** 409 `ai-off`: an admin turned AI mode off. */
    data object Off : AiRefusal

    /** 429 `ai-busy`: the line is full; [position] is the place this question would have had. */
    data class Busy(val position: Int? = null, val retryAfterSec: Long? = null) : AiRefusal

    /** 404: a home older than story 49.12 has no AI mode. */
    data object NotOnThisHome : AiRefusal

    companion object {
        /** The refusal [e] is, or null when it is another failure. */
        fun of(e: ApiException.Status): AiRefusal? {
            val o = runCatching { ApiClient.json.parseToJsonElement(e.body.trim()) as? JsonObject }.getOrNull()
            val code = (o?.get("code") as? JsonPrimitive)?.contentOrNull.orEmpty()
            return when {
                e.code == 404 || e.code == 405 -> NotOnThisHome
                e.code == 409 && code == "ai-off" -> Off
                e.code == 409 -> {
                    val help = o?.get("help") as? JsonObject
                    fun path(k: String, or: String) = (help?.get(k) as? JsonPrimitive)?.contentOrNull?.takeIf { it.startsWith("/") } ?: or
                    Unavailable(path("settings", AI_SETTINGS_PATH), path("help", AI_HELP_PATH))
                }
                e.code == 429 -> Busy((o?.get("position") as? JsonPrimitive)?.doubleOrNull?.toInt(), e.retryAfterSec)
                else -> null
            }
        }
    }
}

/** Where an admin sets the model server, in the web app (the home's own answer names it too). */
const val AI_SETTINGS_PATH = "/settings/rules?section=tagcheck&q=url"

/** AI mode's help page, in the web app. */
const val AI_HELP_PATH = "/help/search#ai-mode"

/** The home's refusal to answer: [why] says which. */
class AiRefused(val why: AiRefusal) : Exception("AI mode refused: $why")

/** An earlier question and its answer: what a follow-up carries. */
data class AiTurn(val q: String, val answer: String)

/** How many earlier turns a follow-up sends: the home reads the last three. */
const val AI_TURNS_SENT = 3

/** `POST /api/search/ai`'s body. */
fun aiRequest(q: String, history: List<AiTurn> = emptyList(), filters: JsonObject? = null): JsonObject = buildJsonObject {
    put("q", q.trim())
    if (history.isNotEmpty()) {
        putJsonArray("history") {
            history.takeLast(AI_TURNS_SENT).forEach { t ->
                add(buildJsonObject {
                    put("q", t.q)
                    put("answer", t.answer)
                })
            }
        }
    }
    if (filters != null && filters.isNotEmpty()) put("filters", filters) else putJsonObject("filters") {}
}

/**
 * `POST /api/search/ai`: the stream's events as they come. The home's refusals throw
 * [AiRefused]; any other failure is an [ApiException] ([ApiException.Transport] when the stream
 * breaks). Cancelling the collector closes the connection, which the home reads as the question
 * withdrawn (it stops the model and gives the turn to the next).
 */
fun ApiClient.searchAI(q: String, history: List<AiTurn> = emptyList(), filters: JsonObject? = null): Flow<AiEvent> = channelFlow {
    val r = request("POST", "/api/search/ai", body = aiRequest(q, history, filters)).newBuilder()
        .header("Accept", "text/event-stream")
        .build()
    val answer = try {
        opened(r)
    } catch (e: ApiException.Status) {
        throw AiRefusal.of(e)?.let(::AiRefused) ?: e
    }
    val reader = launch(Dispatchers.IO) {
        answer.use { a ->
            val source = a.response.body.source()
            val sse = SseReader()
            val buf = ByteArray(8192)
            while (true) {
                val n = try {
                    source.read(buf)
                } catch (e: IOException) {
                    // the call cancelled because the collector left: not a failure
                    ensureActive()
                    throw ApiException.Transport(e.message ?: e.toString())
                }
                if (n < 0) break
                for (ev in sse.feed(buf, 0, n)) AiEvent.of(ev)?.let { send(it) }
            }
        }
    }
    try {
        reader.join()
    } finally {
        // leaving (the collector cancelled): a read blocked on the socket ends now
        answer.cancel()
    }
}

// ---------- the conversation ----------

/** Where one question's answer is. */
enum class AiPhase {
    /** asked: waiting for the passages (in line, maybe) */
    Asking,
    /** the passages are in; the sentences follow */
    Writing,
    /** done, answered */
    Answered,
    /** done: the passages do not answer the question (they stay shown) */
    NoAnswer,
    /** the answer stopped: the home's `error`, a broken stream, or the person left; what came stands */
    Stopped,
}

/** One question of the conversation and what has come of its answer. */
data class AiExchange(
    val id: Int,
    val q: String,
    /** its place in line while it waits; null when it does not */
    val queue: Int? = null,
    val passages: List<AiPassage> = emptyList(),
    val sentences: List<AiSentence> = emptyList(),
    val phase: AiPhase = AiPhase.Asking,
    /** `done`'s reason (`no-passages`, `not-in-passages`, `timeout`) */
    val reason: String = "",
) {
    /** The answer's words, as a follow-up carries them. */
    val answer: String get() = sentences.joinToString(" ") { it.text.trim() }

    /** Still coming. */
    val live: Boolean get() = phase == AiPhase.Asking || phase == AiPhase.Writing

    /** The passage a citation names. */
    fun passage(c: AiCitation): AiPassage? = passages.firstOrNull { it.n == c.n }
}

/** AI mode's tab: the questions asked, oldest first, and why the home would not answer, if it would not. */
data class AiState(
    val exchanges: List<AiExchange> = emptyList(),
    /** the home's refusal of the last question; null when it answered (or nothing was asked) */
    val refusal: AiRefusal? = null,
) {
    val live: Boolean get() = exchanges.lastOrNull()?.live == true
    val isEmpty: Boolean get() = exchanges.isEmpty() && refusal == null
}

/**
 * AI mode's conversation, the same in the three Kotlin apps. A question asks the home and
 * streams its answer into [state]; asking again first stops the answer still coming, and
 * [stop] ends it (the person left the tab or Search). A follow-up carries the earlier answered
 * questions ([AiTurn]); [start] begins again with a new search's question.
 */
class AiConversation(
    private val asker: (q: String, history: List<AiTurn>, filters: JsonObject?) -> Flow<AiEvent>,
    private val signedOut: () -> Unit = {},
    /** a failure that is not the home's refusal: for the app's log */
    private val failed: (Throwable) -> Unit = {},
) {
    constructor(client: ApiClient, signedOut: () -> Unit = {}, failed: (Throwable) -> Unit = {}) :
        this({ q, h, f -> client.searchAI(q, h, f) }, signedOut, failed)

    private val mutable = MutableStateFlow(AiState())
    val state: StateFlow<AiState> = mutable.asStateFlow()

    /** The search question the conversation began with; [open] asks it once. */
    var startedFor: String? = null
        private set

    private var job: Job? = null
    private var ids = 0

    /**
     * The tab opened on the search [q]: a conversation that did not begin with it begins now
     * (the tab asks only when it is opened or asked from). Blank asks nothing.
     */
    fun open(scope: CoroutineScope, q: String, filters: JsonObject? = null) {
        val t = q.trim()
        if (t.isEmpty() || t == startedFor) return
        start(scope, t, filters)
    }

    /** A new conversation for [q] (a search submitted from the tab): the earlier one is dropped. */
    fun start(scope: CoroutineScope, q: String, filters: JsonObject? = null) {
        val t = q.trim()
        if (t.isEmpty()) return
        stop()
        startedFor = t
        mutable.value = AiState()
        ask(scope, t, filters)
    }

    /** A follow-up: [q] asked with the earlier questions and their answers. */
    fun ask(scope: CoroutineScope, q: String, filters: JsonObject? = null): Job? {
        val t = q.trim()
        if (t.isEmpty()) return null
        stop()
        if (startedFor == null) startedFor = t
        val history = mutable.value.exchanges.filter { it.answer.isNotBlank() }.map { AiTurn(it.q, it.answer) }
        val id = ++ids
        mutable.update { it.copy(exchanges = it.exchanges + AiExchange(id, t), refusal = null) }
        lastFilters = filters
        val j = scope.launch {
            try {
                // an event that arrives after the answer was stopped is left out
                asker(t, history, filters).collect { ev -> edit(id) { if (it.live) it.on(ev) else it } }
                // the stream ended without its `done`
                edit(id) { if (it.live) it.copy(phase = AiPhase.Stopped, queue = null) else it }
            } catch (e: AiRefused) {
                mutable.update { s -> s.copy(exchanges = s.exchanges.filter { it.id != id }, refusal = e.why) }
            } catch (e: ApiException.SignedOut) {
                edit(id) { it.copy(phase = AiPhase.Stopped, queue = null) }
                signedOut()
            } catch (e: CancellationException) {
                edit(id) { if (it.live) it.copy(phase = AiPhase.Stopped, queue = null) else it }
                throw e
            } catch (e: Exception) {
                failed(e)
                edit(id) { it.copy(phase = AiPhase.Stopped, queue = null) }
            }
        }
        job = j
        return j
    }

    private var lastFilters: JsonObject? = null

    /** Asks the last question again (Try again), with what came before it. */
    fun retry(scope: CoroutineScope) {
        val s = mutable.value
        val last = s.exchanges.lastOrNull()
        if (s.refusal != null || last == null) {
            startedFor?.let { q ->
                if (s.exchanges.isEmpty()) start(scope, q, lastFilters) else ask(scope, q, lastFilters)
            }
            return
        }
        stop()
        mutable.update { it.copy(exchanges = it.exchanges.dropLast(1)) }
        ask(scope, last.q, lastFilters)
    }

    /** Stops the answer still coming: what came stands, marked stopped. */
    fun stop() {
        job?.cancel()
        job = null
        mutable.update { s -> s.copy(exchanges = s.exchanges.map { if (it.live) it.copy(phase = AiPhase.Stopped, queue = null) else it }) }
    }

    /** Forgets the conversation (a new search's words, signing out). */
    fun clear() {
        stop()
        startedFor = null
        mutable.value = AiState()
    }

    private fun edit(id: Int, f: (AiExchange) -> AiExchange) =
        mutable.update { s -> s.copy(exchanges = s.exchanges.map { if (it.id == id) f(it) else it }) }
}

/** [this] after [ev]. */
internal fun AiExchange.on(ev: AiEvent): AiExchange = when (ev) {
    is AiEvent.Queue -> if (phase == AiPhase.Asking) copy(queue = ev.position.coerceAtLeast(1)) else this
    is AiEvent.Sources -> copy(passages = ev.passages, phase = AiPhase.Writing, queue = null)
    is AiEvent.Text -> copy(sentences = sentences + ev.sentence, phase = AiPhase.Writing, queue = null)
    is AiEvent.Done -> copy(
        phase = if (ev.done.answered || sentences.isNotEmpty()) AiPhase.Answered else AiPhase.NoAnswer,
        reason = ev.done.reason,
        queue = null,
    )
    is AiEvent.Error -> copy(phase = AiPhase.Stopped, reason = ev.code, queue = null)
}
