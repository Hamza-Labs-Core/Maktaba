package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.DeviceCode
import com.hamzalabs.maktaba.core.api.DeviceSignIn
import com.hamzalabs.maktaba.core.api.PairingCode
import com.hamzalabs.maktaba.core.api.PairingLookup
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.Home
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.HttpUrl.Companion.toHttpUrl
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.util.Collections

class PairingTest {
    // ---------- the code ----------

    @Test
    fun aCodeIsReadAsAPersonTypesIt() {
        assertEquals("ABCD1234", PairingCode.normalize("abcd-1234"))
        assertEquals("ABCD1234", PairingCode.normalize(" ab cd 12 34 "))
        assertEquals("letters a person mistakes for digits", "A0C11234", PairingCode.normalize("aOcIL234"))
        assertNull("too short", PairingCode.normalize("ABC-123"))
        assertNull("U is not in the alphabet", PairingCode.normalize("ABCU1234"))
        assertNull(PairingCode.normalize("ABCD-1234-X"))
        assertEquals("ABCD-1234", PairingCode.shown("abcd1234"))
    }

    @Test
    fun aScannedApprovePageGivesItsCode() {
        assertEquals("ABCD1234", PairingCode.fromScan("http://192.168.1.5:8088/pair?code=ABCD-1234"))
        assertEquals("ABCD1234", PairingCode.fromScan("https://example.com/maktaba/pair?code=abcd1234&x=1"))
        assertEquals("ABCD1234", PairingCode.fromScan("ABCD-1234"))
        assertNull("a page without a code", PairingCode.fromScan("http://192.168.1.5:8088/pair"))
        assertNull(PairingCode.fromScan("maktaba://connect?u=http://a"))
    }

    @Test
    fun theQrHoldsTheHomesApproveAddress() {
        val code = DeviceCode("ABCD-1234", "dc", verifyPath = "/pair?code=ABCD-1234")
        assertEquals("http://192.168.1.5:8088/pair?code=ABCD-1234", code.approveUrl("http://192.168.1.5:8088".toHttpUrl()))
        assertEquals("under the base path", "https://example.com/maktaba/pair?code=ABCD-1234", code.approveUrl("https://example.com/maktaba".toHttpUrl()))
        assertEquals(
            "the home's own verify_url first",
            "https://darwish.maktaba.dev/pair?code=ABCD-1234",
            code.copy(verifyUrl = "https://darwish.maktaba.dev/pair?code=ABCD-1234").approveUrl("http://192.168.1.5:8088".toHttpUrl()),
        )
    }

    // ---------- the device side, against a fake home ----------

    /** A home's sign-in by code: answers in turn, records each request. */
    private class FakePairing(vararg answers: Pair<Int, String>) {
        val queue = ArrayDeque(answers.toList())
        val sent = mutableListOf<Pair<String, JsonObject>>()
        var down = 0
        suspend fun post(path: String, body: JsonObject): Pair<Int, String> {
            sent += path to body
            if (down > 0) {
                down--
                throw ApiException.Transport("connection refused")
            }
            return queue.removeFirstOrNull() ?: (500 to "")
        }
    }

    private val clock = longArrayOf(1_800_000_000_000L)
    private fun signIn(home: FakePairing, label: String = "Pixel 9 Pro Fold") =
        DeviceSignIn(home::post, label, now = { clock[0] }, sleep = { clock[0] += it })

    private fun codeJson(code: String, device: String) =
        """{"code":"$code","device_code":"$device","expires_at":"2027-01-15T08:10:00Z","verify_path":"/pair?code=$code","interval":2}"""

    @Test
    fun theDeviceShowsTheCodeAndPollsUntilApproved() = runBlocking {
        val home = FakePairing(
            201 to codeJson("ABCD-1234", "secret-1"),
            202 to """{"status":"pending"}""",
            202 to """{"status":"pending"}""",
            200 to """{"access_token":"a1","refresh_token":"r1","user_id":"u1"}""",
        )
        val shown = mutableListOf<String>()
        val tokens = signIn(home).run { shown += it.code }
        assertEquals("a1", tokens.accessToken)
        assertEquals("r1", tokens.refreshToken)
        assertEquals(listOf("ABCD-1234"), shown)
        assertEquals("/api/pairing/device", home.sent[0].first)
        assertEquals("Pixel 9 Pro Fold", home.sent[0].second["device_label"]?.jsonPrimitive?.content)
        assertEquals("/api/pairing/device/token", home.sent[1].first)
        assertEquals("secret-1", home.sent[1].second["device_code"]?.jsonPrimitive?.content)
        assertEquals(4, home.sent.size)
    }

    @Test
    fun aCodeThatRunsOutIsReplacedAndShownAgain() = runBlocking {
        val home = FakePairing(
            201 to codeJson("ABCD-1234", "secret-1"),
            410 to """{"title":"gone"}""",
            201 to codeJson("WXYZ-5678", "secret-2"),
            200 to """{"access_token":"a2","refresh_token":"r2"}""",
        )
        val shown = mutableListOf<String>()
        assertEquals("a2", signIn(home).run { shown += it.code }.accessToken)
        assertEquals(listOf("ABCD-1234", "WXYZ-5678"), shown)
        assertEquals("secret-2", home.sent.last().second["device_code"]?.jsonPrimitive?.content)
    }

    @Test
    fun aHomeThatRestartedAnswers410AtOnceAndANewCodeIsAskedFor() = runBlocking {
        val home = FakePairing(
            201 to codeJson("ABCD-1234", "secret-1"),
            // the first poll after a restart: the old device code is gone
            410 to """{"title":"gone","detail":"code expired: ask for a new one"}""",
            201 to codeJson("WXYZ-5678", "secret-2"),
            202 to """{"status":"pending"}""",
            200 to """{"access_token":"a2","refresh_token":"r2"}""",
        )
        val shown = mutableListOf<String>()
        val start = clock[0]
        assertEquals("a2", signIn(home).run { shown += it.code }.accessToken)
        assertEquals(listOf("ABCD-1234", "WXYZ-5678"), shown)
        assertEquals(
            listOf("/api/pairing/device", "/api/pairing/device/token", "/api/pairing/device", "/api/pairing/device/token", "/api/pairing/device/token"),
            home.sent.map { it.first },
        )
        assertTrue("no waiting out the old code's ten minutes", clock[0] - start < 30_000L)
    }

    @Test
    fun aBlinkWhilePollingIsRidden() = runBlocking {
        val home = FakePairing(201 to codeJson("ABCD-1234", "s"), 503 to "", 429 to "", 200 to """{"access_token":"a","refresh_token":"r"}""")
        assertEquals("a", signIn(home).run {}.accessToken)
    }

    @Test
    fun aHomeWithoutSignInByCodeSaysSo() = runBlocking {
        for (status in listOf(404, 405, 401)) {
            try {
                signIn(FakePairing(status to "")).run {}
                fail("$status: unsupported")
            } catch (e: DeviceSignIn.Unsupported) {
                assertEquals(status, e.status)
            }
        }
    }

    @Test
    fun anUnreachableHomeIsAskedThreeTimesThenReported() = runBlocking {
        val home = FakePairing().apply { down = 5 }
        try {
            signIn(home).run {}
            fail("unreachable")
        } catch (e: ApiException.Transport) {
            assertEquals(3, home.sent.size)
        }
    }

    @Test
    fun aCodeThatIsNeverApprovedRunsOutAtTheHomesExpiry() = runBlocking {
        val home = FakePairing(*Array(400) { 202 to """{"status":"pending"}""" })
        val code = DeviceCode("ABCD-1234", "s", expiresAt = null, interval = 2)
        val start = clock[0]
        assertNull("null: the device asks for a new code", signIn(home).poll(code))
        assertTrue("ten minutes when the home does not say", clock[0] - start in 600_000L..602_000L)
    }

    // ---------- the approver and the session, against a home on MockWebServer ----------

    private lateinit var server: MockWebServer
    private val seen: MutableList<RecordedRequest> = Collections.synchronizedList(mutableListOf())
    private var route: (RecordedRequest) -> MockResponse = { MockResponse.Builder().code(404).build() }

    private fun json(code: Int, body: String) =
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                seen += request
                return route(request)
            }
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    @Test
    fun theApproverSeesWhatIsAskingThenApproves() = runBlocking {
        route = { r ->
            when (r.url.encodedPath) {
                "/api/pairing/device/ABCD1234" -> json(200, """{"code":"ABCD-1234","client_app":"player","platform":"android","device":"phone","device_label":"Amina's Pixel","expires_at":"2027-01-15T08:10:00Z"}""")
                "/api/pairing/approve" -> json(200, """{"status":"approved"}""")
                else -> json(404, """{"title":"not found"}""")
            }
        }
        val client = ApiClient(server.url("/"), MemoryTokens("t", "r"))
        val found = client.pairingRequest("abcd-1234") as PairingLookup.Waiting
        assertEquals("Amina's Pixel", found.request.deviceLabel)
        assertEquals("player", found.request.clientApp)
        assertEquals("Bearer t", seen[0].headers["Authorization"])
        client.approvePairing("abcd1234")
        assertEquals("/api/pairing/approve", seen[1].url.encodedPath)
        assertTrue(seen[1].body!!.utf8().contains("\"code\":\"ABCD-1234\""))
    }

    @Test
    fun anUnknownCodeAndAnOlderHomeAreToldApart() = runBlocking {
        val client = ApiClient(server.url("/"), MemoryTokens("t", "r"))
        route = { json(404, """{"type":"about:blank","title":"not found","status":404}""") }
        assertEquals(PairingLookup.Unknown, client.pairingRequest("ABCD-1234"))
        route = { MockResponse.Builder().code(404).body("404 page not found").build() }
        assertEquals("no such route: an older home", PairingLookup.Unsupported, client.pairingRequest("ABCD-1234"))
        route = { json(405, "") }
        assertEquals(PairingLookup.Unsupported, client.pairingRequest("ABCD-1234"))
        assertEquals("not a code: not asked", PairingLookup.Unknown, client.pairingRequest("nope"))
    }

    @Test
    fun theSessionSignsInWithACode() = runBlocking {
        route = { r ->
            when (r.url.encodedPath) {
                "/maktaba/api/pairing/device" -> json(201, codeJson("ABCD-1234", "secret"))
                "/maktaba/api/pairing/device/token" -> json(200, """{"access_token":"a1","refresh_token":"r1"}""")
                "/maktaba/api/auth/me" -> json(200, """{"id":"u1","username":"amina","home_name":"Darwish home"}""")
                else -> json(404, "{}")
            }
        }
        val tokens = mutableMapOf<String, MemoryTokens>()
        val s = AppSession(MapStore(), CoroutineScope(Dispatchers.Unconfined), { tokens.getOrPut(it) { MemoryTokens() } })
        val base = server.url("/maktaba").toString().trimEnd('/')
        s.use(Home(base, "Darwish"))
        var shown: DeviceCode? = null
        s.signInWithCode("Pixel", onCode = { shown = it }, sleep = {})
        assertEquals("ABCD-1234", shown?.code)
        val phase = s.phase.value as AppSession.Phase.SignedIn
        assertEquals("amina", phase.user.username)
        assertEquals("a1", tokens.getValue(base).access)
        assertNull("asking for a code carries no token", seen.first { it.url.encodedPath.endsWith("/api/pairing/device") }.headers["Authorization"])
        assertEquals("native", seen.first().headers["X-Maktaba-Client"])
        assertEquals("Bearer a1", seen.last().headers["Authorization"])
    }
}
