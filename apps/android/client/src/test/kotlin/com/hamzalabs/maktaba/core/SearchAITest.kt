package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.AI_HELP_PATH
import com.hamzalabs.maktaba.core.api.AiConversation
import com.hamzalabs.maktaba.core.api.AiEvent
import com.hamzalabs.maktaba.core.api.AiPhase
import com.hamzalabs.maktaba.core.api.AiRefusal
import com.hamzalabs.maktaba.core.api.AiRefused
import com.hamzalabs.maktaba.core.api.AiTurn
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.SseEvent
import com.hamzalabs.maktaba.core.api.SseReader
import com.hamzalabs.maktaba.core.api.aiRequest
import com.hamzalabs.maktaba.core.api.searchAI
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.consumeAsFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.take
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonArray
import okhttp3.HttpUrl.Companion.toHttpUrl
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import java.io.BufferedInputStream
import java.io.OutputStream
import java.net.ServerSocket
import java.net.Socket
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread

/**
 * AI mode's stream (story 49.14) as the three Kotlin apps read it: the Server-Sent Events cut
 * anywhere, each event, the home's refusals, the conversation's follow-ups, and cancelling.
 * The stream is the home's own (`api/internal/handlers/search/ai.go`'s `send`): `event: …`,
 * `data: …` and a blank line, with `: ping` comments.
 */
class SearchAITest {
    private val video = "11111111-0000-4000-8000-000000000002"

    /** A stream as the home writes it: a ping, the place in line, sources, two sentences, done. */
    private val stream = buildString {
        append(": ping\n\n")
        append("event: queue\ndata: {\"position\":1}\n\n")
        append("event: sources\ndata: {\"q\":\"ما هو الصبر؟\",\"passages\":[")
        append("{\"n\":1,\"video_id\":\"$video\",\"title\":\"الصبر\",\"library_id\":\"l1\",\"library_name\":\"دروس\",\"poster_url\":\"/api/videos/$video/poster\",\"segment_id\":7,\"start_sec\":61.5,\"end_sec\":66,\"text\":\"الصبر حبس النفس عن الجزع\"},")
        append("{\"n\":2,\"video_id\":\"$video\",\"title\":\"الصبر\",\"library_id\":\"l1\",\"library_name\":\"دروس\",\"poster_url\":\"\",\"segment_id\":9,\"start_sec\":120,\"end_sec\":124,\"text\":\"وهو ثلاثة أنواع\"}]}\n\n")
        append("event: text\ndata: {\"text\":\"الصبر حبس النفس عن الجزع.\",\"citations\":[{\"n\":1,\"video_id\":\"$video\",\"start_sec\":61.5,\"end_sec\":66,\"quote\":\"الصبر حبس النفس عن الجزع\"}]}\n\n")
        append("event: text\ndata: {\"text\":\"وهو ثلاثة أنواع.\",\"citations\":[{\"n\":2,\"video_id\":\"$video\",\"start_sec\":120,\"end_sec\":124,\"quote\":\"وهو ثلاثة أنواع\"}]}\n\n")
        append("event: done\ndata: {\"answered\":true,\"sentences\":2,\"dropped\":1,\"first_token_ms\":350,\"took_ms\":900,\"model\":\"qwen2.5:7b\"}\n\n")
    }

    private fun events(s: String): List<SseEvent> = SseReader().feed(s.toByteArray())

    // ---------- the reader ----------

    @Test
    fun readsEachEventAndSkipsComments() {
        val e = events(stream)
        assertEquals(listOf("queue", "sources", "text", "text", "done"), e.map { it.event })
        assertEquals("{\"position\":1}", e[0].data)
    }

    @Test
    fun theSameEventsWhereverTheStreamIsCut() {
        val bytes = stream.toByteArray()
        val whole = events(stream)
        // every cut in two, and one byte at a time: inside a line, between \r and \n, inside an Arabic letter
        for (cut in 1 until bytes.size) {
            val r = SseReader()
            val got = r.feed(bytes, 0, cut) + r.feed(bytes, cut, bytes.size - cut)
            assertEquals("cut at $cut", whole, got)
        }
        val r = SseReader()
        val oneByOne = bytes.indices.flatMap { r.feed(bytes, it, 1) }
        assertEquals(whole, oneByOne)
    }

    @Test
    fun crlfAndCrEndLinesAndDataLinesJoin() {
        val crlf = stream.replace("\n", "\r\n").toByteArray()
        val cr = stream.replace("\n", "\r").toByteArray()
        assertEquals(events(stream), SseReader().feed(crlf))
        assertEquals(events(stream), SseReader().feed(cr))
        // a \r\n cut between its two bytes ends one line, not two
        val r = SseReader()
        val split = r.feed("event: x\r\ndata: 1\r".toByteArray()) + r.feed("\n\r\n".toByteArray())
        assertEquals(listOf(SseEvent("x", "1")), split)
        assertEquals(listOf(SseEvent("message", "a\nb")), SseReader().feed("data: a\ndata:b\nid: 3\nretry: 9\n\n".toByteArray()))
        // an event the stream ends inside is dropped
        assertEquals(emptyList<SseEvent>(), SseReader().feed("event: text\ndata: {}\n".toByteArray()))
    }

    // ---------- the events ----------

    @Test
    fun eachEventIsRead() {
        val e = events(stream).mapNotNull(AiEvent::of)
        assertEquals(AiEvent.Queue(1), e[0])
        val sources = e[1] as AiEvent.Sources
        assertEquals("ما هو الصبر؟", sources.q)
        assertEquals(listOf(1, 2), sources.passages.map { it.n })
        assertEquals(61.5, sources.passages[0].startSec, 0.0)
        assertEquals("دروس", sources.passages[0].libraryName)
        val first = (e[2] as AiEvent.Text).sentence
        assertEquals("الصبر حبس النفس عن الجزع.", first.text)
        assertEquals(61.5, first.citations.single().startSec, 0.0)
        val done = (e[4] as AiEvent.Done).done
        assertTrue(done.answered)
        assertEquals(1, done.dropped)
        assertEquals(AiEvent.Error("model-failed"), AiEvent.of(SseEvent("error", "{\"code\":\"model-failed\"}")))
        val no = AiEvent.of(SseEvent("done", "{\"answered\":false,\"reason\":\"not-in-passages\",\"sentences\":0,\"dropped\":0,\"took_ms\":3}")) as AiEvent.Done
        assertEquals("not-in-passages", no.done.reason)
    }

    @Test
    fun anOddEventIsLeftOut() {
        assertNull(AiEvent.of(SseEvent("thinking", "{}")))
        assertNull(AiEvent.of(SseEvent("text", "not json")))
        assertNull(AiEvent.of(SseEvent("text", "{\"text\":\"  \"}")))
        // a citation it cannot read, or without a video, is left out; the sentence stays
        val t = AiEvent.of(SseEvent("text", "{\"text\":\"A.\",\"citations\":[{\"n\":1,\"video_id\":\"v\",\"start_sec\":3,\"extra\":true},\"odd\",{\"n\":2}]}")) as AiEvent.Text
        assertEquals(listOf("v"), t.sentence.citations.map { it.videoId })
        // passages that are not a list read as none
        assertEquals(emptyList<Any>(), (AiEvent.of(SseEvent("sources", "{\"q\":\"x\",\"passages\":7}")) as AiEvent.Sources).passages)
    }

    @Test
    fun theRequestCarriesTheLastThreeTurnsAndTheFilters() {
        val turns = (1..5).map { AiTurn("q$it", "a$it") }
        val filters = buildJsonObject { putJsonArray("language") { add(kotlinx.serialization.json.JsonPrimitive("ar")) } }
        val body = aiRequest("  and its kinds?  ", turns, filters)
        assertEquals("and its kinds?", body["q"]!!.jsonPrimitive.content)
        assertEquals(listOf("q3", "q4", "q5"), body["history"]!!.jsonArray.map { it.jsonObject["q"]!!.jsonPrimitive.content })
        assertEquals("a5", body["history"]!!.jsonArray.last().jsonObject["answer"]!!.jsonPrimitive.content)
        assertEquals(filters, body["filters"])
        val first = aiRequest("q")
        assertNull(first["history"])
        assertEquals(JsonObject(emptyMap()), first["filters"])
    }

    // ---------- over HTTP ----------

    private var home: RawHome? = null

    @After
    fun stopHome() {
        home?.close()
    }

    private fun client(h: RawHome) = ApiClient(h.url.toHttpUrl(), MemoryTokens(jwt(4_000_000_000L), "r"))

    @Test
    fun theStreamArrivesInPiecesAndIsRead() = runBlocking {
        val bytes = stream.toByteArray()
        val h = RawHome { req, out ->
            assertEquals("POST /api/search/ai", req.line.substringBeforeLast(" "))
            assertTrue(req.headers["accept"]!!.contains("text/event-stream"))
            assertTrue(req.headers["authorization"]!!.startsWith("Bearer "))
            out.write("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nConnection: close\r\n\r\n".toByteArray())
            // in odd pieces, each flushed: the events arrive cut anywhere
            var i = 0
            var size = 1
            while (i < bytes.size) {
                val n = minOf(size, bytes.size - i)
                out.write(bytes, i, n)
                out.flush()
                i += n
                size = size % 37 + 3
            }
        }.also { home = it }
        val got = withTimeout(10_000) { client(h).searchAI("ما هو الصبر؟").toList() }
        assertEquals(listOf("Queue", "Sources", "Text", "Text", "Done"), got.map { it::class.simpleName })
        val sent = ApiClient.json.parseToJsonElement(h.requests.single().body).jsonObject
        assertEquals("ما هو الصبر؟", sent["q"]!!.jsonPrimitive.content)
    }

    private fun refusal(status: Int, body: String, headers: String = ""): AiRefusal {
        val h = RawHome { _, out ->
            val b = body.toByteArray()
            out.write("HTTP/1.1 $status X\r\nContent-Type: application/problem+json\r\nContent-Length: ${b.size}\r\n$headers\r\n".toByteArray())
            out.write(b)
        }.also { home = it }
        return try {
            runBlocking { withTimeout(10_000) { client(h).searchAI("q").toList() } }
            fail("no refusal")
            error("unreachable")
        } catch (e: AiRefused) {
            e.why
        } finally {
            h.close()
        }
    }

    @Test
    fun theCodesAreTheirRefusals() {
        val unavailable = refusal(409, """{"type":"https://x/problems/ai-unavailable","title":"no language model","status":409,"code":"ai-unavailable","help":{"settings":"/settings/rules?section=tagcheck&q=url","help":"/help/search#ai-mode"}}""")
        assertEquals(AiRefusal.Unavailable("/settings/rules?section=tagcheck&q=url", AI_HELP_PATH), unavailable)
        // a home that names no help: the web app's own paths
        assertEquals(AiRefusal.Unavailable(), refusal(409, """{"code":"ai-unavailable","help":{"settings":"https://elsewhere"}}"""))
        assertEquals(AiRefusal.Off, refusal(409, """{"type":"https://x/problems/ai-off","status":409,"code":"ai-off"}"""))
        assertEquals(AiRefusal.Busy(9, 30), refusal(429, """{"code":"ai-busy","position":9}""", "Retry-After: 30\r\n"))
        // a home before story 49.12 has no such route
        assertEquals(AiRefusal.NotOnThisHome, refusal(404, """{"type":"https://x/problems/not-found","status":404}"""))
    }

    @Test
    fun anotherFailureIsNotARefusal() {
        val h = RawHome { _, out ->
            out.write("HTTP/1.1 500 X\r\nContent-Length: 2\r\n\r\n{}".toByteArray())
        }.also { home = it }
        try {
            runBlocking { client(h).searchAI("q").toList() }
            fail("no failure")
        } catch (e: ApiException.Status) {
            assertEquals(500, e.code)
        }
    }

    @Test
    fun cancellingClosesTheConnection() = runBlocking {
        val closed = CountDownLatch(1)
        val h = RawHome { _, out ->
            out.write("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n".toByteArray())
            out.write("event: queue\ndata: {\"position\":2}\n\n".toByteArray())
            out.flush()
            // the home writes its ping until the client leaves
            try {
                repeat(500) {
                    Thread.sleep(20)
                    out.write(": ping\n\n".toByteArray())
                    out.flush()
                }
            } catch (_: Exception) {
                closed.countDown()
            }
        }.also { home = it }
        val first = withTimeout(10_000) { client(h).searchAI("q").first() }
        assertEquals(AiEvent.Queue(2), first)
        assertTrue("the home saw the client leave", closed.await(5, TimeUnit.SECONDS))
    }

    // ---------- the conversation ----------

    /** A home whose answers the test hands over event by event; it records what was asked. */
    private class Asker {
        val asked = mutableListOf<Pair<String, List<AiTurn>>>()
        val channels = mutableListOf<Channel<AiEvent>>()
        var refuse: AiRefusal? = null
        val cancelled = mutableListOf<Int>()

        fun ask(q: String, history: List<AiTurn>, @Suppress("UNUSED_PARAMETER") f: JsonObject?): Flow<AiEvent> {
            asked += q to history
            refuse?.let { r -> return flow { throw AiRefused(r) } }
            val c = Channel<AiEvent>(Channel.UNLIMITED)
            channels += c
            val i = channels.size - 1
            return flow {
                try {
                    c.consumeAsFlow().collect { emit(it) }
                } catch (e: kotlinx.coroutines.CancellationException) {
                    cancelled += i
                    throw e
                }
            }
        }
    }

    private fun sentence(t: String, n: Int = 1) = AiEvent.Text(
        com.hamzalabs.maktaba.core.api.AiSentence(t, listOf(com.hamzalabs.maktaba.core.api.AiCitation(n, video, 10.0 * n, 0.0, "q"))),
    )

    private fun sources() = events(stream).mapNotNull(AiEvent::of).filterIsInstance<AiEvent.Sources>().single()

    private fun done(answered: Boolean, reason: String = "") =
        AiEvent.Done(com.hamzalabs.maktaba.core.api.AiDone(answered = answered, reason = reason))

    private fun <T> unconfined(block: suspend CoroutineScope.(CoroutineScope) -> T): T = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Unconfined)
        try {
            block(scope)
        } finally {
            scope.cancel()
        }
    }

    @Test
    fun sourcesFirstThenEachSentence() = unconfined { scope ->
        val a = Asker()
        val c = AiConversation(a::ask)
        c.open(scope, "ما هو الصبر؟")
        assertEquals(AiPhase.Asking, c.state.value.exchanges.single().phase)
        a.channels[0].send(AiEvent.Queue(2))
        assertEquals(2, c.state.value.exchanges.single().queue)
        a.channels[0].send(sources())
        val shown = c.state.value.exchanges.single()
        assertEquals(AiPhase.Writing, shown.phase)
        assertNull(shown.queue)
        assertEquals(2, shown.passages.size)
        assertTrue(shown.sentences.isEmpty())
        a.channels[0].send(sentence("الصبر حبس النفس."))
        a.channels[0].send(sentence("وهو ثلاثة أنواع.", 2))
        a.channels[0].send(done(true))
        val e = c.state.value.exchanges.single()
        assertEquals(AiPhase.Answered, e.phase)
        assertEquals("الصبر حبس النفس. وهو ثلاثة أنواع.", e.answer)
        assertEquals(120.0, e.passage(e.sentences[1].citations.single())!!.startSec, 0.0)
        // opening the tab again on the same search asks nothing
        c.open(scope, "ما هو الصبر؟")
        assertEquals(1, a.asked.size)
    }

    @Test
    fun aFollowUpCarriesTheEarlierAnswers() = unconfined { scope ->
        val a = Asker()
        val c = AiConversation(a::ask)
        c.open(scope, "What is patience?")
        a.channels[0].send(sources())
        a.channels[0].send(sentence("Holding back."))
        a.channels[0].send(done(true))
        c.ask(scope, "And its kinds?")
        assertEquals("And its kinds?" to listOf(AiTurn("What is patience?", "Holding back.")), a.asked[1])
        assertEquals(2, c.state.value.exchanges.size)
        // an unanswered question is not carried
        a.channels[1].send(sources())
        a.channels[1].send(done(false, "not-in-passages"))
        assertEquals(AiPhase.NoAnswer, c.state.value.exchanges[1].phase)
        assertEquals(2, c.state.value.exchanges[1].passages.size)
        c.ask(scope, "Third?")
        assertEquals(listOf(AiTurn("What is patience?", "Holding back.")), a.asked[2].second)
    }

    @Test
    fun askingAgainOrLeavingStopsTheAnswer() = unconfined { scope ->
        val a = Asker()
        val c = AiConversation(a::ask)
        c.open(scope, "one")
        a.channels[0].send(sources())
        a.channels[0].send(sentence("First."))
        c.ask(scope, "two")
        assertEquals(listOf(0), a.cancelled)
        val first = c.state.value.exchanges[0]
        assertEquals(AiPhase.Stopped, first.phase)
        assertEquals("First.", first.answer)
        // the person leaves the tab
        c.stop()
        assertEquals(listOf(0, 1), a.cancelled)
        assertEquals(AiPhase.Stopped, c.state.value.exchanges[1].phase)
        // a late event of a stopped answer changes nothing
        a.channels[1].trySend(sentence("Late."))
        assertTrue(c.state.value.exchanges[1].sentences.isEmpty())
        // a new search begins again
        c.open(scope, "three")
        assertEquals(listOf("three"), c.state.value.exchanges.map { it.q })
        assertEquals(emptyList<AiTurn>(), a.asked.last().second)
    }

    @Test
    fun anErrorKeepsWhatWasShown() = unconfined { scope ->
        val a = Asker()
        val c = AiConversation(a::ask)
        c.open(scope, "q")
        a.channels[0].send(sources())
        a.channels[0].send(sentence("One."))
        a.channels[0].send(AiEvent.Error("model-failed"))
        val e = c.state.value.exchanges.single()
        assertEquals(AiPhase.Stopped, e.phase)
        assertEquals("model-failed", e.reason)
        assertEquals("One.", e.answer)
        // the stream ending without its done stops it too
        c.ask(scope, "again")
        a.channels[1].send(sources())
        a.channels[1].close()
        assertEquals(AiPhase.Stopped, c.state.value.exchanges[1].phase)
    }

    @Test
    fun aRefusalIsTheTabsMessageNotAnAnswer() = unconfined { scope ->
        val a = Asker().apply { refuse = AiRefusal.Unavailable() }
        val c = AiConversation(a::ask)
        c.open(scope, "q")
        assertEquals(AiRefusal.Unavailable(), c.state.value.refusal)
        assertTrue(c.state.value.exchanges.isEmpty())
        // Try again asks the same question
        a.refuse = null
        c.retry(scope)
        assertNull(c.state.value.refusal)
        assertEquals(listOf("q", "q"), a.asked.map { it.first })
        // a follow-up refused (the line is full) is the one asked again, with the answer before it
        a.channels[0].send(sources())
        a.channels[0].send(sentence("One."))
        a.channels[0].send(done(true))
        a.refuse = AiRefusal.Busy(9, 30)
        c.ask(scope, "follow-up")
        assertEquals(AiRefusal.Busy(9, 30), c.state.value.refusal)
        assertEquals(1, c.state.value.exchanges.size)
        a.refuse = null
        c.retry(scope)
        assertEquals("follow-up" to listOf(AiTurn("q", "One.")), a.asked.last())
        assertEquals(listOf("q", "follow-up"), c.state.value.exchanges.map { it.q })
    }

    @Test
    fun signedOutIsTold() = unconfined { scope ->
        var out = false
        val c = AiConversation({ _, _, _ -> flow { throw ApiException.SignedOut } }, signedOut = { out = true })
        c.open(scope, "q")
        assertTrue(out)
    }

    @Test
    fun aConversationAgainstAStreamOverHttp() = runBlocking {
        val h = RawHome { _, out ->
            out.write("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nConnection: close\r\n\r\n".toByteArray())
            out.write(stream.toByteArray())
        }.also { home = it }
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val c = AiConversation(client(h))
        val done = CompletableDeferred<Unit>()
        scope.launch {
            c.state.take(100).collect { if (it.exchanges.firstOrNull()?.live == false) done.complete(Unit) }
        }
        c.open(scope, "ما هو الصبر؟")
        withTimeout(10_000) { done.await() }
        val e = c.state.value.exchanges.single()
        assertEquals(AiPhase.Answered, e.phase)
        assertEquals(2, e.sentences.size)
        scope.cancel()
    }
}

/**
 * A home on a socket of its own, so a test controls each byte of the answer and sees the client
 * close the connection (MockWebServer queues a whole answer). [answer] writes the answer to one
 * request.
 */
internal class RawHome(private val answer: (Req, OutputStream) -> Unit) : AutoCloseable {
    class Req(val line: String, val headers: Map<String, String>, val body: String)

    private val socket = ServerSocket(0)
    val url = "http://127.0.0.1:${socket.localPort}/"
    val requests: MutableList<Req> = java.util.Collections.synchronizedList(mutableListOf())
    private val open: MutableList<Socket> = java.util.Collections.synchronizedList(mutableListOf())

    init {
        thread(isDaemon = true) {
            while (!socket.isClosed) {
                val s = runCatching { socket.accept() }.getOrNull() ?: break
                open += s
                thread(isDaemon = true) { serve(s) }
            }
        }
    }

    private fun serve(s: Socket) {
        s.use {
            val input = BufferedInputStream(it.getInputStream())
            fun line(): String {
                val b = StringBuilder()
                while (true) {
                    val c = input.read()
                    if (c < 0 || c == '\n'.code) break
                    if (c != '\r'.code) b.append(c.toChar())
                }
                return b.toString()
            }
            val first = line()
            val headers = mutableMapOf<String, String>()
            while (true) {
                val l = line()
                if (l.isEmpty()) break
                headers[l.substringBefore(':').trim().lowercase()] = l.substringAfter(':').trim()
            }
            val n = headers["content-length"]?.toIntOrNull() ?: 0
            val body = ByteArray(n)
            var read = 0
            while (read < n) {
                val r = input.read(body, read, n - read)
                if (r < 0) break
                read += r
            }
            val req = Req(first, headers, String(body, Charsets.UTF_8))
            requests += req
            val out = it.getOutputStream()
            runCatching { answer(req, out) }
            runCatching { out.flush() }
        }
    }

    override fun close() {
        runCatching { socket.close() }
        open.forEach { runCatching { it.close() } }
    }
}
