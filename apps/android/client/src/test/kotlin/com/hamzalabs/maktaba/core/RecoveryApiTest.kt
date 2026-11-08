package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.RecoveryAction
import com.hamzalabs.maktaba.core.api.RecoveryException
import com.hamzalabs.maktaba.core.api.recoveryAction
import com.hamzalabs.maktaba.core.api.recoveryLines
import com.hamzalabs.maktaba.core.api.recoveryState
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.int
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

// Epic 53 stories 53.04 and 53.05 on Android: the Remote reads and drives the recovery console
// with its pass (else its access token), confirms as the home asks, and a refusal is never a
// sign-out.
class RecoveryApiTest {
    private lateinit var server: MockWebServer
    private val seen = mutableListOf<RecordedRequest>()
    private val answers = mutableMapOf<String, ArrayDeque<MockResponse>>()

    private fun json(code: Int, body: String) =
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    /** Answers "METHOD /path" with [code] and [body], in turn (the last one stays). */
    private fun on(route: String, code: Int, body: String = "{}") {
        answers.getOrPut(route) { ArrayDeque() }.add(json(code, body))
    }

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                synchronized(seen) { seen += request }
                val q = answers["${request.method} ${request.url.encodedPath}"] ?: return json(599, "no stub")
                return if (q.size > 1) q.removeFirst() else q.first()
            }
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    private fun bearers() = seen.map { it.headers["Authorization"]?.removePrefix("Bearer ") }

    private val state = """
        {"status":{"v":1,"mode":"safe","overall":"down","components":{"logs":"ok","database":"down","api":"restarting","search":"off"}},
         "components":[
          {"id":"core.postgres","group":"database","phase":"failed","reason":"crash_loop","state":"down","restarts":3,"cut_off":true,
           "last_exit":"2026-11-07T09:12:00Z","last_code":1,"last_error":"could not open file pg_wal","can_restart":true},
          {"id":"search.index","group":"search","phase":"stopped","reason":"held","state":"off","restarts":0,"can_restart":false}],
         "disk_free":120000000000,"logs_bytes":52428800,"rollback":"0.3.44","safe_mode":true}
    """.trimIndent()

    @Test
    fun readsTheStateWithThePass() = runBlocking {
        on("GET /recovery/api/state", 200, state)
        val s = ApiClient(server.url("/"), MemoryTokens("a1", recoveryPass = "p1")).recoveryState()
        assertTrue(s.safeMode)
        assertEquals("0.3.44", s.rollbackVersion)
        assertEquals(120_000_000_000L, s.diskFree)
        assertEquals(listOf("database", "api", "search", "logs"), s.groups.map { it.first })
        val pg = s.components[0]
        assertTrue(pg.cutOff && pg.troubled && pg.canRestart)
        assertEquals(1, pg.lastCode)
        assertFalse(s.components[1].canRestart || s.components[1].troubled)
        assertEquals(listOf("p1"), bearers())
    }

    @Test
    fun aRefusedPassIsDroppedAndTheAccessTokenTried() = runBlocking {
        on("GET /recovery/api/state", 401, """{"error":"unauthorized"}""")
        on("GET /recovery/api/state", 200, state)
        val tokens = MemoryTokens("a1", recoveryPass = "stale")
        ApiClient(server.url("/"), tokens).recoveryState()
        assertEquals(listOf("stale", "a1"), bearers())
        assertNull("a refused pass is not sent again (each refusal counts toward the lockout)", tokens.recoveryPass)
        assertEquals("a1", tokens.access)
    }

    @Test
    fun refusedEverywhereIsDeniedNeverASignOut() = runBlocking {
        on("GET /recovery/api/state", 401, """{"error":"unauthorized"}""")
        val tokens = MemoryTokens("viewer")
        try {
            ApiClient(server.url("/"), tokens).recoveryState()
            fail("denied")
        } catch (_: RecoveryException.Denied) {
        }
        assertEquals("still signed in to the home", "viewer", tokens.access)
    }

    @Test
    fun anExpiredAccessTokenIsRefreshedOnce() = runBlocking {
        on("GET /recovery/api/state", 401, """{"error":"unauthorized"}""")
        on("GET /recovery/api/state", 200, state)
        on("POST /api/auth/refresh", 200, """{"access_token":"a2","refresh_token":"r2"}""")
        ApiClient(server.url("/"), MemoryTokens("a1", "r1")).recoveryState()
        assertEquals(listOf("a1", "a2"), seen.filter { it.method == "GET" }.map { it.headers["Authorization"]?.removePrefix("Bearer ") })
    }

    @Test
    fun forbiddenIsDeniedAndAnOlderHomeHasNoConsole() = runBlocking {
        on("GET /recovery/api/state", 403, """{"error":"forbidden"}""")
        val tokens = MemoryTokens("a1", recoveryPass = "p1")
        try {
            ApiClient(server.url("/"), tokens).recoveryState(); fail()
        } catch (_: RecoveryException.Denied) {
        }
        assertEquals("p1", tokens.recoveryPass)
        answers.clear()
        on("GET /recovery/api/state", 404, "404 page not found")
        try {
            ApiClient(server.url("/"), tokens).recoveryState(); fail()
        } catch (_: RecoveryException.NotHere) {
        }
    }

    @Test
    fun linesSayTheirMessageRawTextAndError() = runBlocking {
        on("GET /recovery/api/logs", 200, """
            {"lines":[{"ts":"2026-11-07T09:12:00Z","level":"error","service":"postgres","component":"core.postgres","msg":"raw_line","attrs":{"text":"FATAL: could not open file"}},
                      {"ts":"2026-11-07T09:11:00Z","level":"warn","service":"api","component":"core.api","msg":"db_connect","err":{"msg":"connection refused"}},
                      {"level":"error","service":"api","msg":"start","err":"exit 1"}],"truncated":false}
        """.trimIndent())
        val lines = ApiClient(server.url("/"), MemoryTokens("a1")).recoveryLines(30)
        assertEquals(listOf("FATAL: could not open file", "db_connect: connection refused", "start: exit 1"), lines.map { it.msg })
        assertEquals("core.postgres", lines[0].component)
        assertNull(lines[2].ts)
        assertEquals("30", seen.last().url.queryParameter("minutes"))
    }

    @Test
    fun anActionCarriesItsGuardHeaderAndConfirmations() = runBlocking {
        on("POST /recovery/api/action", 200, """{"result":"ok"}""")
        ApiClient(server.url("/"), MemoryTokens("a1", recoveryPass = "p1")).recoveryAction(RecoveryAction.Restart("core.postgres"), 1)
        val r = seen.last()
        assertEquals("1", r.headers["X-Maktaba-Recovery"])
        val body = Json.parseToJsonElement(r.body!!.utf8()).jsonObject
        assertEquals("restart", body["action"]!!.jsonPrimitive.content)
        assertEquals("core.postgres", body["component"]!!.jsonPrimitive.content)
        assertEquals(1, body["confirm"]!!.jsonPrimitive.int)
    }

    @Test
    fun theHomesRefusalAndItsAskForMoreConfirmations() = runBlocking {
        on("POST /recovery/api/action", 409, """{"error":"no previous version is kept"}""")
        val c = ApiClient(server.url("/"), MemoryTokens("a1"))
        try {
            c.recoveryAction(RecoveryAction.Rollback("0.3.44"), 2); fail()
        } catch (e: RecoveryException.Refused) {
            assertEquals("no previous version is kept", e.why)
        }
        answers.clear()
        on("POST /recovery/api/action", 428, """{"error":"confirm","confirm":2}""")
        try {
            c.recoveryAction(RecoveryAction.RestartAll, 1); fail()
        } catch (e: RecoveryException.Confirm) {
            assertEquals(2, e.need)
        }
    }

    @Test
    fun thePassIsKeptFromTheAPIAndForgottenOnSignOut() = runBlocking {
        on("POST /api/auth/recovery-pass", 200, """{"expires_at":"2026-11-14T09:00:00Z","pass":"p-new"}""")
        val tokens = MemoryTokens("a1")
        assertTrue(ApiClient(server.url("/"), tokens).refreshRecoveryPass())
        assertEquals("p-new", tokens.recoveryPass)
        tokens.clear()
        assertNull(tokens.recoveryPass)
        // a viewer: 403, nothing kept, still signed in
        answers.clear()
        on("POST /api/auth/recovery-pass", 403, """{"title":"forbidden"}""")
        val viewer = MemoryTokens("v1")
        assertFalse(ApiClient(server.url("/"), viewer).refreshRecoveryPass())
        assertNull(viewer.recoveryPass)
        assertEquals("v1", viewer.access)
    }

    @Test
    fun aSealedStoreKeepsThePassPerHome() {
        val box = object : com.hamzalabs.maktaba.core.session.SecretBox {
            override fun seal(plain: ByteArray) = plain.reversedArray()
            override fun open(sealed: ByteArray) = sealed.reversedArray()
        }
        val store = MapStore()
        val a = com.hamzalabs.maktaba.core.session.SealedTokenStore(store, box, "https://a")
        val b = com.hamzalabs.maktaba.core.session.SealedTokenStore(store, box, "https://b")
        a.recoveryPass = "pa"
        assertEquals("pa", a.recoveryPass)
        assertNull(b.recoveryPass)
        a.clear()
        assertNull(a.recoveryPass)
    }
}
