package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.Board
import com.hamzalabs.maktaba.core.api.ErrorFilter
import com.hamzalabs.maktaba.core.api.ErrorGroupAction
import com.hamzalabs.maktaba.core.api.ErrorSort
import com.hamzalabs.maktaba.core.api.LogQuery
import com.hamzalabs.maktaba.core.api.board
import com.hamzalabs.maktaba.core.api.errorGroup
import com.hamzalabs.maktaba.core.api.errorGroups
import com.hamzalabs.maktaba.core.api.gapsOverASecond
import com.hamzalabs.maktaba.core.api.problemReport
import com.hamzalabs.maktaba.core.api.problemReports
import com.hamzalabs.maktaba.core.api.replyToReport
import com.hamzalabs.maktaba.core.api.searchLogs
import com.hamzalabs.maktaba.core.api.sendCrashEnvelope
import com.hamzalabs.maktaba.core.api.sendProblemReport
import com.hamzalabs.maktaba.core.api.setErrorStatus
import com.hamzalabs.maktaba.core.api.setReportStatus
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test

// Epic 53 on Android: the Remote's Errors, Logs, dashboards and inbox (53.11 §4, 53.12 §4,
// 53.15 §5, T06) read the web's routes; reports and crash envelopes go where the home takes them.
class DiagnosticsApiTest {
    private lateinit var server: MockWebServer
    private val seen = mutableListOf<RecordedRequest>()
    private val bodies = mutableListOf<String>()
    private var answer: (RecordedRequest) -> MockResponse = { json(200, "{}") }

    private fun json(code: Int, body: String) =
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                seen += request
                bodies += request.body?.utf8() ?: ""
                return answer(request)
            }
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    private fun client() = ApiClient(server.url("/"), MemoryTokens(jwt(System.currentTimeMillis() / 1000 + 3600), "r"))
    private fun last() = seen.last()
    private fun lastJson() = Json.parseToJsonElement(bodies.last()).jsonObject

    @Test
    fun errorGroupsAskWithTheWebsFiltersAndReadTolerantly() = runBlocking {
        answer = {
            json(200, """{"groups":[{"fp":"a1","service":"api","component":"core.api","title":"database timed out","count":42,
              "count_24h":7,"users":3,"status":"new","days":[0,1,2],"extra":1},{"fp":"b2","service":"apple.remote","title":"x"}]}""")
        }
        val gs = client().errorGroups(ErrorFilter.Open, ErrorSort.Count)
        assertEquals(listOf("a1", "b2"), gs.map { it.fp })
        assertEquals(7L, gs[0].count24h)
        assertEquals("core.api", gs[0].part)
        assertEquals("apple.remote", gs[1].part)
        assertEquals("/api/admin/errors", last().url.encodedPath)
        assertEquals("new,seen", last().url.queryParameter("status"))
        assertEquals("count", last().url.queryParameter("sort"))
        client().errorGroups(ErrorFilter.All)
        assertNull(last().url.queryParameter("status"))
    }

    @Test
    fun aGroupsPageHasItsHoursLinesReleasesAndMachines() = runBlocking {
        answer = {
            json(200, """{"group":{"fp":"a1","title":"t","status":"seen"},
              "hours":{"2026-11-06T09:00:00Z":1,"2026-11-06T08:00:00Z":2},
              "samples":{"lines":[{"ts":"2026-11-06T09:00:00.5Z","level":"error","service":"api","node":"home","msg":"db_timeout",
                "version":"0.3.90","request_id":"0192f0c4-1111-7000-8000-000000000001","err":{"code":"pq"},"took_ms":5000}],"ids":[991]}}""")
        }
        val d = client().errorGroup("a/1")
        assertEquals("/api/admin/errors/a%2F1", last().url.encodedPath)
        assertEquals(listOf(2L, 1L), d.hourly.map { it.second })
        assertEquals(listOf("0.3.90"), d.versions)
        assertEquals(listOf("home"), d.machines)
        val (id, line) = d.samples!!.rows.single()
        assertEquals(991L, id)
        assertTrue(line.isError)
        assertEquals("5000", line.texts["took_ms"])
        assertEquals("""{"code":"pq"}""", line.texts["err"])
    }

    @Test
    fun statusActionsPostTheWebsBodies() = runBlocking {
        answer = { MockResponse.Builder().code(204).build() }
        val c = client()
        c.setErrorStatus("a1", ErrorGroupAction.Seen)
        assertEquals("seen", lastJson()["status"]!!.jsonPrimitive.content)
        c.setErrorStatus("a1", ErrorGroupAction.Resolve("0.3.90"))
        assertEquals("0.3.90", lastJson()["version"]!!.jsonPrimitive.content)
        c.setErrorStatus("a1", ErrorGroupAction.Mute(1_800_000_000_000))
        assertEquals("2027-01-15T08:00:00Z", lastJson()["muted_until"]!!.jsonPrimitive.content)
        c.setErrorStatus("a1", ErrorGroupAction.Mute(null))
        assertNull(lastJson()["muted_until"])
        assertEquals("/api/admin/errors/a1/status", last().url.encodedPath)
    }

    @Test
    fun aSearchTakesWordsLevelWindowAndAReferenceFollowsTheRequest() = runBlocking {
        answer = { json(200, """{"lines":[],"ids":null}""") }
        val r = client().searchLogs(LogQuery(" playback -debug ", "warn", 60), nowMs = 1_800_000_000_000)
        assertTrue(r.lines.isEmpty() && r.rows.isEmpty())
        assertEquals("playback -debug", last().url.queryParameter("q"))
        assertEquals("warn", last().url.queryParameter("level"))
        assertEquals("2027-01-15T07:00:00Z", last().url.queryParameter("since"))
        client().searchLogs(LogQuery("ignored", requestId = "0192f0c4"))
        assertEquals("0192f0c4", last().url.queryParameter("request_id"))
        assertNull(last().url.queryParameter("q"))
        assertEquals("0192f0c4", LogQuery.requestReference("0192F0C4"))
        assertEquals(null, LogQuery.requestReference("playback"))
        assertTrue(LogQuery.requestReference("0192f0c4-1111-7000-8000-000000000001") != null)
    }

    @Test
    fun aRestartingIndexIsA503() = runBlocking {
        answer = { json(503, """{"title":"log search is restarting","status":503}""") }
        try {
            client().searchLogs(LogQuery())
            fail("a 503")
        } catch (e: ApiException.Status) {
            assertEquals(503, e.code)
        }
    }

    @Test
    fun gapsOverASecondAreMarked() {
        val lines = Json.decodeFromString<com.hamzalabs.maktaba.core.api.LogSearch>(
            """{"lines":[{"ts":"2026-11-06T09:00:00Z"},{"ts":"2026-11-06T09:00:00.400Z"},{"ts":"2026-11-06T09:00:02Z"}]}""",
        ).lines
        assertEquals(listOf(false, false, true), gapsOverASecond(lines))
    }

    @Test
    fun boardsAreGroupedIntoPanels() = runBlocking {
        answer = {
            json(200, """{"board":"storage","range":"7d","series":[
              {"name":"maktaba_logs_bytes","labels":"kind=segments","points":[[1800000000,1024],[1800003600,2048]]},
              {"name":"maktaba_logs_bytes","labels":"kind=index","points":[[1800000000,512]]},
              {"name":"maktaba_disk_free_bytes","points":[[1800000000,9.5e10]]}]}""")
        }
        val b = client().board(Board.Storage, "7d")
        assertEquals("/api/admin/dashboards/storage", last().url.encodedPath)
        assertEquals("7d", last().url.queryParameter("range"))
        assertEquals(listOf("maktaba_logs_bytes", "maktaba_disk_free_bytes"), b.panels.map { it.first })
        assertEquals(listOf("segments", "index"), b.panels[0].second.map { it.legend })
    }

    @Test
    fun theInboxReadsAReportsTimelineAndChangesIt() = runBlocking {
        answer = { r ->
            when (r.url.encodedPath) {
                "/api/reports" -> json(200, """{"reports":[{"id":27,"app":"android.player","category":"playback","status":"new"}]}""")
                "/api/reports/27" -> json(200, """{"id":27,"app":"android.player","version":"0.3.90","platform":"android",
                    "device_model":"Pixel 9","category":"playback","status":"new","text":"it stopped",
                    "lines":[{"ts":"2026-11-06T08:59:02Z","msg":"stall"}],"server":[{"ts":"2026-11-06T08:59:01Z","msg":"segment_missing"}],
                    "crashes":[{"platform":"java"}]}""")
                else -> json(200, """{"id":27}""")
            }
        }
        val c = client()
        assertEquals(listOf(27), c.problemReports().map { it.id })
        val d = c.problemReport(27)
        assertEquals(listOf(true, false), d.timeline.map { it.first })
        assertEquals(1, d.crashes.size)
        c.setReportStatus(27, "looking")
        assertEquals("looking", lastJson()["status"]!!.jsonPrimitive.content)
        c.replyToReport(27, "Fixed in the next release")
        assertEquals("Fixed in the next release", lastJson()["reply"]!!.jsonPrimitive.content)
    }

    @Test
    fun aReportIsMultipartAndTheScreenshotOnlyWhenGiven() = runBlocking {
        answer = { json(200, """{"id":27}""") }
        assertEquals(27, client().sendProblemReport("""{"category":"playback"}""".toByteArray(), null))
        assertTrue(last().headers["Content-Type"]!!.startsWith("multipart/form-data"))
        assertTrue(bodies.last().contains("name=\"report\""))
        assertFalse(bodies.last().contains("name=\"screenshot\""))
        client().sendProblemReport("{}".toByteArray(), byteArrayOf(1, 2, 3))
        assertTrue(bodies.last().contains("name=\"screenshot\""))
    }

    @Test
    fun anEnvelopeIsSentAsOne() = runBlocking {
        answer = { json(200, """{"id":"x"}""") }
        client().sendCrashEnvelope("{}\n".toByteArray())
        assertEquals("/api/diagnostics/envelope", last().url.encodedPath)
        assertEquals("application/x-sentry-envelope", last().headers["Content-Type"])
        answer = { MockResponse.Builder().code(429).setHeader("Retry-After", "120").body("{}").build() }
        try {
            client().sendCrashEnvelope("{}\n".toByteArray())
            fail("a 429")
        } catch (e: ApiException.Status) {
            assertEquals(429, e.code)
            assertEquals(120L, e.retryAfterSec)
        }
    }
}
