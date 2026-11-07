package com.hamzalabs.maktaba.core.crash

import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.time.Instant

// Epic 53 story 53.14 (T05, T10) and 53.15 (T04, T08) on Android: a kept crash is an envelope
// the home takes, sent once and only when crash reports are on; a report is what its form showed.
class CrashOutboxTest {
    @get:Rule val tmp = TemporaryFolder()

    /** A stand-in for the shared rules: what the tests look for is masked. */
    private val scrub: (String) -> String = { it.replace(Regex("Bearer [A-Za-z0-9._-]+"), "Bearer [REDACTED]").replace("someone@example.com", "[EMAIL]") }
    private val header = Envelope.Header("android.remote", "0.3.90", "android", "15", "Pixel 9", "install-1")

    private val crashLog = """
        app: com.hamzalabs.maktaba.remote 0.3.90
        os: Android 15 (API 35)
        at: 2027-01-15T08:00:00Z
        thread: main
        java.lang.RuntimeException: java.lang.IllegalStateException
        	at com.hamzalabs.maktaba.remote.Screens.load(Screens.kt:12)
        	at android.os.Handler.dispatchMessage(Handler.java:99)
        Caused by: java.lang.IllegalStateException
        	at com.hamzalabs.maktaba.core.api.ApiClient.get(ApiClient.kt:40)
        	at com.hamzalabs.maktaba.remote.Screens.load(Screens.kt:11)
        	at kotlinx.coroutines.DispatchedTask.run(DispatchedTask.kt:108)
    """.trimIndent()

    private fun parse(env: ByteArray): Pair<JsonObject, List<Pair<String, String>>> {
        val text = String(env)
        var rest = text
        fun line(): String { val i = rest.indexOf('\n'); val l = rest.substring(0, i); rest = rest.substring(i + 1); return l }
        val hdr = Json.parseToJsonElement(line()).jsonObject
        val items = mutableListOf<Pair<String, String>>()
        while (rest.isNotEmpty()) {
            val ih = Json.parseToJsonElement(line()).jsonObject
            val n = ih["length"]!!.jsonPrimitive.content.toInt()
            val bytes = rest.toByteArray()
            items += ih["type"]!!.jsonPrimitive.content to String(bytes.copyOfRange(0, n))
            rest = String(bytes.copyOfRange(n + 1, bytes.size))
        }
        return hdr to items
    }

    @Test
    fun aCrashLogIsAnEventOfItsDeepestCauseWithTheAppsFramesInApp() {
        val c = KeptCrash.fromCrashLog(crashLog, "Pixel 9")!!
        assertEquals("java.lang.IllegalStateException", c.type)
        assertEquals(Instant.parse("2027-01-15T08:00:00Z").toEpochMilli(), c.at)
        assertEquals("0.3.90", c.appVersion)
        assertEquals("com.hamzalabs.maktaba.core.api.ApiClient.get(ApiClient.kt:40)", c.frames.first())
        val e = c.event()
        val ex = e["exception"]!!.jsonObject["values"]!!.jsonArray[0].jsonObject
        val frames = ex["stacktrace"]!!.jsonObject["frames"]!!.jsonArray.map { it.jsonObject }
        // oldest first: the crashing frame last
        assertEquals("com.hamzalabs.maktaba.core.api.ApiClient.get", frames.last()["function"]!!.jsonPrimitive.content)
        assertEquals(listOf(false, true, true), frames.map { it["in_app"]!!.jsonPrimitive.content.toBoolean() })
        assertNull(KeptCrash.fromCrashLog("not a crash", null))
    }

    @Test
    fun theEnvelopeCarriesTheConsentAndIsScrubbedAndWithinTheLimits() {
        val c = KeptCrash(at = 1_800_000_000_000, type = "ANR", reason = "Input dispatching timed out Bearer abc.def", mechanism = "anr",
            trace = "\"main\" prio=5 someone@example.com")
        val lines = (0 until 20000).map { """{"n":$it,"msg":"a line of the app's log, long enough to add up"}""" } + """{"msg":"Bearer abc.def"}"""
        val env = Envelope.build(header, c.event(), lines, scrub)
        assertTrue(env.size <= Envelope.MAX_BYTES)
        val (hdr, items) = parse(env)
        assertEquals("crash_reports_to_home", hdr["consent"]!!.jsonPrimitive.content)
        assertEquals("install-1", hdr["install_id"]!!.jsonPrimitive.content)
        assertEquals(listOf("event", "attachment"), items.map { it.first })
        assertFalse(items[0].second.contains("abc.def"))
        assertFalse(items[0].second.contains("someone@example.com"))
        assertTrue(items[1].second.toByteArray().size <= Envelope.MAX_ATTACHMENT)
        assertTrue(items[1].second.endsWith("""{"msg":"Bearer [REDACTED]"}"""))
        assertFalse(items[1].second.contains(""""n":0,"""))
    }

    private fun outbox(now: () -> Long) = CrashOutbox(tmp.newFolder(), now)

    @Test
    fun offSendsNothingAndKeepsTheCrash() = runBlocking {
        val o = outbox { 1_800_000_000_000 }
        o.keep(KeptCrash(at = 1_800_000_000_000, type = "x"))
        val sent = mutableListOf<ByteArray>()
        assertEquals(0, o.flush(false, header, { emptyList() }, scrub) { sent += it; CrashOutbox.SendResult.Sent })
        assertTrue(sent.isEmpty())
        assertEquals(1, o.files().size)
        assertEquals(CrashOutbox.Notice.Kept, o.notice())
        assertNull(o.notice())
    }

    @Test
    fun onSendsEachOnceAndKeepsFive() = runBlocking {
        val o = outbox { 1_800_000_000_000 }
        repeat(7) { o.keep(KeptCrash(at = 1_800_000_000_000 + it, type = "x$it")) }
        assertEquals(5, o.files().size)
        var n = 0
        assertEquals(5, o.flush(true, header, { emptyList() }, scrub) { n++; CrashOutbox.SendResult.Sent })
        assertEquals(0, o.flush(true, header, { emptyList() }, scrub) { n++; CrashOutbox.SendResult.Sent })
        assertEquals(5, n)
        assertEquals(CrashOutbox.Notice.Sent, o.notice())
    }

    @Test
    fun a429WaitsAndRetriesAtMostThreeTimes() = runBlocking {
        val t0 = 1_800_000_000_000
        var now = t0
        val o = outbox { now }
        val f = o.keep(KeptCrash(at = t0, type = "x"))
        val answers = ArrayDeque(listOf<CrashOutbox.SendResult>(CrashOutbox.SendResult.Later(120_000), CrashOutbox.SendResult.Later(),
            CrashOutbox.SendResult.Later(), CrashOutbox.SendResult.Later()))
        var tries = 0
        suspend fun at(ms: Long) { now = t0 + ms; o.flush(true, header, { emptyList() }, scrub) { tries++; answers.removeFirstOrNull() ?: CrashOutbox.SendResult.Sent } }
        at(0); at(60_000)
        assertEquals(1, tries)
        at(121_000); at(2 * 86_400_000L); at(5 * 86_400_000L); at(6 * 86_400_000L)
        assertEquals(4, tries)
        assertFalse(o.wasSent(f.name))
    }

    @Test
    fun aRefusalStops() = runBlocking {
        val o = outbox { 1_800_000_000_000 }
        o.keep(KeptCrash(at = 1, type = "a")); o.keep(KeptCrash(at = 2, type = "b"))
        var n = 0
        o.flush(true, header, { emptyList() }, scrub) { n++; CrashOutbox.SendResult.Refused }
        assertEquals(1, n)
    }

    @Test
    fun theReportIsWhatTheFormShowed() {
        val now = 1_800_000_000_000
        fun line(ago: Long, msg: String) = """{"level":"info","msg":"$msg","ts":"${Instant.ofEpochMilli(now - ago)}"}"""
        val l = ReportPart.lines(listOf(line(3_600_000, "old"), line(600_000, "recent"), line(1000, "mail someone@example.com"), "junk"), now, scrub)
        assertEquals(2, l.size)
        assertFalse(l[1].contains("someone@example.com"))
        val part = ReportPart.part("playback", "x".repeat(2500), now - 3_600_000, l, listOf(JsonObject(mapOf("platform" to JsonPrimitive("java")))),
            header, listOf("aaaa1111-0000-7000-8000-000000000000"), "0192f0c4", false, scrub)
        val j = Json.parseToJsonElement(String(part)).jsonObject
        assertEquals(2000, j["text"]!!.jsonPrimitive.content.length)
        assertEquals(listOf("aaaa1111-0000-7000-8000-000000000000", "0192f0c4"), (j["request_ids"] as JsonArray).map { it.jsonPrimitive.content })
        assertEquals("false", j["screenshot_previewed"]!!.jsonPrimitive.content)
        assertEquals(1, (j["crashes"] as JsonArray).size)
        assertEquals("2027-01-15T07:00:00Z", j["when"]!!.jsonPrimitive.content)
        val big = (0 until 2000).map { line(1, "line $it " + "y".repeat(400)) }
        assertTrue(ReportPart.part("other", "", null, big, emptyList(), header, emptyList(), null, false, scrub).size <= ReportPart.PART_MAX)
        val none = Json.parseToJsonElement(String(ReportPart.part("other", "", null, emptyList(), emptyList(), header, emptyList(), null, false, scrub))).jsonObject
        assertNull(none["crashes"])
    }
}
