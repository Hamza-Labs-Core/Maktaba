package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.PinnedNames
import com.hamzalabs.maktaba.core.api.exchange
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.CloudConnection
import com.hamzalabs.maktaba.core.session.HomeInfo
import com.hamzalabs.maktaba.core.session.HomeProbe
import com.hamzalabs.maktaba.core.session.NotAHome
import com.hamzalabs.maktaba.core.session.RETRY_DOWN_MS
import com.hamzalabs.maktaba.core.session.TvApproval
import com.hamzalabs.maktaba.core.session.TvCode
import com.hamzalabs.maktaba.core.session.TvCodeState
import com.hamzalabs.maktaba.core.session.TvCodes
import com.hamzalabs.maktaba.core.session.TvPoll
import com.hamzalabs.maktaba.core.session.waitForTvApproval
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.tls.HandshakeCertificates
import okhttp3.tls.HeldCertificate
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.util.Collections

/**
 * The cloud code on Android TV and Fire TV (story 58.03's TV side, 58.05): the code asked for
 * and polled, renewed and asked again; the approving home known by its addresses (the cloud's
 * id for it is not the one it answers with); the grant redeemed only there, for this TV's own
 * tokens at that address; the poll secret and the grant in no URL and no toString.
 */
class TvCloudCodeTest {
    private val seen: MutableList<Pair<String, RecordedRequest>> = Collections.synchronizedList(mutableListOf())
    private fun seenNow() = synchronized(seen) { seen.toList() }
    private val servers = mutableListOf<MockWebServer>()

    private fun json(code: Int, body: String) =
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    private fun server(name: String, tls: HandshakeCertificates? = null, route: (RecordedRequest) -> MockResponse) =
        MockWebServer().apply {
            dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse {
                    seen += name to request
                    return route(request)
                }
            }
            tls?.let { useHttps(it.sslSocketFactory()) }
            start()
            servers += this
        }

    private val ca = HeldCertificate.Builder().certificateAuthority(0).build()
    private val trust = HandshakeCertificates.Builder().addTrustedCertificate(ca.certificate).build()
    private val trusting: OkHttpClient =
        ApiClient.defaultHttp.newBuilder().sslSocketFactory(trust.sslSocketFactory(), trust.trustManager).build()

    @Before
    fun setUp() = PinnedNames.clear()

    @After
    fun tearDown() {
        servers.forEach { it.close() }
        PinnedNames.clear()
    }

    // ---------- the cloud's calls ----------

    private val code = """{"code":"ABCD-2345","poll_secret":"$POLL_SECRET","expires_in":600,"interval":5,"verify_url":"https://maktaba.tv/tv?c=ABCD2345"}"""

    @Test
    fun asksForACodeWithThePlatformAndPollsWithTheSecretInTheBodyOnly() = runBlocking {
        var poll = 0
        val cloud = server("cloud") { r ->
            when (r.url.encodedPath) {
                "/v1/tv/codes" -> json(201, code)
                "/v1/tv/codes/poll" -> when (poll++) {
                    0 -> json(202, """{"status":"pending"}""")
                    1 -> json(429, """{"error":"slow_down"}""")
                    2 -> json(410, """{"error":"expired_token"}""")
                    3 -> json(404, """{"error":"not_found"}""")
                    4 -> json(200, approvalJson(grant = "x y"))
                    5 -> json(500, "{}")
                    else -> json(200, approvalJson())
                }
                else -> json(404, "{}")
            }
        }
        val codes = TvCodes(cloud.url("/"))
        val c = codes.create("firetv", "1.2.3")!!
        assertEquals("ABCD-2345", c.code)
        assertEquals("https://maktaba.tv/tv?c=ABCD2345", c.verifyUrl)
        val create = seenNow().first().second
        assertEquals("POST", create.method)
        assertEquals("""{"platform":"firetv","app_version":"1.2.3"}""", create.body!!.utf8())
        assertEquals(TvPoll.Pending, codes.poll(c))
        assertEquals(TvPoll.SlowDown, codes.poll(c))
        assertEquals(TvPoll.Gone, codes.poll(c))
        assertEquals(TvPoll.Gone, codes.poll(c))
        assertEquals("a grant not of the home's shape is no approval", TvPoll.Failed, codes.poll(c))
        assertEquals(TvPoll.Failed, codes.poll(c))
        val approved = codes.poll(c) as TvPoll.Approved
        assertEquals(GRANT, approved.approval.grant)
        assertEquals(CLOUD_ID, approved.approval.homeId)
        val polls = seenNow().filter { it.second.url.encodedPath == "/v1/tv/codes/poll" }.map { it.second }
        assertTrue(polls.all { POLL_SECRET !in it.url.toString() })
        assertEquals("""{"code":"ABCD-2345","poll_secret":"$POLL_SECRET"}""", polls.first().body!!.utf8())
        assertFalse("the poll secret is never in toString", POLL_SECRET in c.toString())
        assertFalse("the grant is never in toString", GRANT in approved.approval.toString() || GRANT in approved.toString())
    }

    @Test
    fun noCodeWhenTheCloudRefusesOrDoesNotAnswer() = runBlocking {
        val cloud = server("cloud") { json(429, """{"error":"rate_limited"}""") }
        assertNull(TvCodes(cloud.url("/")).create("androidtv", "1"))
        val gone = cloud.url("/")
        cloud.close()
        assertNull(TvCodes(gone).create("androidtv", "1"))
        assertEquals(TvPoll.Failed, TvCodes(gone).poll(TvCode("ABCD-2345", POLL_SECRET)))
    }

    // ---------- the code screen's loop ----------

    @Test
    fun pollsAtTheIntervalSlowerOnSlowDownRenewsAndAsksAgainWhenDown() = runBlocking {
        var clock = 0L
        val slept = mutableListOf<Long>()
        val states = mutableListOf<TvCodeState>()
        var creates = 0
        var polls = 0
        val answers = listOf(TvPoll.Pending, TvPoll.SlowDown, TvPoll.Pending, TvPoll.Gone, TvPoll.Pending)
        val cloud = server("cloud") { r ->
            when (r.url.encodedPath) {
                // the first ask gives nothing (the cloud busy), then codes
                "/v1/tv/codes" -> if (creates++ == 0) json(503, "{}") else json(201, code.replace("ABCD-2345", "CODE-000$creates"))
                else -> {
                    val i = polls++
                    if (i < answers.size) {
                        when (answers[i]) {
                            TvPoll.Pending -> json(202, "{}")
                            TvPoll.SlowDown -> json(429, "{}")
                            else -> json(410, "{}")
                        }
                    } else {
                        json(200, approvalJson())
                    }
                }
            }
        }
        val approval = waitForTvApproval(
            TvCodes(cloud.url("/")), "androidtv", "1.0",
            onState = { states += it },
            sleep = { slept += it; clock += it },
            now = { clock },
        )
        assertEquals(GRANT, approval.grant)
        assertEquals("asked again after the cloud gave none", RETRY_DOWN_MS, slept.first())
        // 5 s, then 10 s after slow_down, until the code was gone; the new code at its own 5 s
        assertEquals(listOf(RETRY_DOWN_MS, 5_000L, 5_000L, 10_000L, 10_000L, 5_000L, 5_000L), slept)
        assertEquals(TvCodeState.Down, states[1])
        val shown = states.filterIsInstance<TvCodeState.Showing>().map { it.code.code }
        assertEquals(listOf("CODE-0002", "CODE-0003"), shown)
    }

    @Test
    fun aCodeThatRunsOutIsReplaced() = runBlocking {
        var clock = 0L
        var creates = 0
        val cloud = server("cloud") { r ->
            if (r.url.encodedPath == "/v1/tv/codes") json(201, code.replace("\"expires_in\":600", "\"expires_in\":30").also { creates++ })
            else if (creates >= 2) json(200, approvalJson()) else json(202, "{}")
        }
        waitForTvApproval(TvCodes(cloud.url("/")), "firetv", "1", onState = {}, sleep = { clock += it }, now = { clock })
        assertEquals(2, creates)
    }

    // ---------- which answering home is the approving one ----------

    private fun conns(vararg c: Pair<String, String>, pinned: String? = null) =
        c.map { (url, kind) -> CloudConnection(url, kind, if (kind == "local_ip") pinned else null) }

    @Test
    fun theGrantGoesOnlyWhereACertificateProvesTheHome() {
        val a = TvApproval(
            CLOUD_ID, "Den",
            conns(
                "http://192.168.1.20:8088" to "local",
                "https://10-0-0-7.h1.direct.example:8443" to "local",
                "https://10.0.0.7:8443" to "local_ip",
                "https://h1.direct.example" to "direct",
                "http://203.0.113.5:8088" to "direct",
                "https://203.0.113.5:8443" to "direct",
                "https://den.relay.maktaba.tv" to "relay",
                pinned = "10-0-0-7.h1.direct.example",
            ),
            GRANT,
        )
        // https names and the raw LAN address pinned to the home's name, in the cloud's order;
        // never a plain address or an unpinned raw IP
        assertEquals(
            listOf(
                "https://10-0-0-7.h1.direct.example:8443",
                "https://10.0.0.7:8443",
                "https://h1.direct.example",
                "https://den.relay.maktaba.tv",
            ),
            a.grantAddresses,
        )
        val unpinned = a.copy(connections = conns("https://10.0.0.7:8443" to "local_ip"))
        assertEquals(emptyList<String>(), unpinned.grantAddresses)
    }

    // ---------- signing in with the approval ----------

    private fun home(name: String, tls: HandshakeCertificates?, urls: () -> List<String>, homeId: String = "instance-7") =
        server(name, tls) { r ->
            when (r.url.encodedPath) {
                "/api/discovery/home" -> json(200, ApiClient.json.encodeToString(HomeInfo.serializer(), HomeInfo(id = homeId, name = "Den", urls = urls())))
                "/api/pairing/device/token" -> json(200, """{"access_token":"$FRESH","refresh_token":"r1"}""")
                "/api/auth/me" -> if (r.headers["Authorization"] == "Bearer $FRESH") json(200, """{"id":"u1","username":"sara","home_name":"Den"}""") else json(401, "{}")
                else -> json(404, "{}")
            }
        }

    /** A home over https at `localhost`, its certificate signed by the test's authority. */
    private fun relayTls(): HandshakeCertificates {
        val leaf = HeldCertificate.Builder().addSubjectAlternativeName("localhost").signedBy(ca).build()
        return HandshakeCertificates.Builder().heldCertificate(leaf, ca.certificate).build()
    }

    private fun session(tokens: MutableMap<String, MemoryTokens>) = AppSession(
        MapStore(), CoroutineScope(Dispatchers.Unconfined), { tokens.getOrPut(it) { MemoryTokens() } },
        clientFor = { url, t -> ApiClient(url, t, trusting) },
    )

    /** The home opens signed in with the kept tokens, in the session's scope: waits for it. */
    private suspend fun signedIn(s: AppSession) = withTimeout(10_000) {
        while (s.phase.value !is AppSession.Phase.SignedIn) delay(20)
    }

    private val probe = HomeProbe { url ->
        trusting.exchange(Request.Builder().url(url).build()).let { it.code to String(it.body) }
    }

    @Test
    fun aPlainLanAddressIsNeverTriedAndTheGrantRedeemedAtTheRelay() = runBlocking {
        lateinit var relayUrl: String
        val relay = home("relay", relayTls(), urls = { listOf(relayUrl) })
        relayUrl = relay.url("/").toString().trimEnd('/').replace("127.0.0.1", "localhost")
        // something answers on the plain LAN address, even listing the home's relay
        val other = home("other", null, urls = { listOf("https://other.relay.maktaba.tv", relayUrl) }, homeId = "instance-9")
        val lan = other.url("/").toString().trimEnd('/')
        val tokens = mutableMapOf<String, MemoryTokens>()
        val s = session(tokens)
        val a = TvApproval(CLOUD_ID, "Den", conns(lan to "local", relayUrl to "relay"), GRANT)
        s.signInWithTvApproval(a, "Living room TV", probe)
        signedIn(s)
        assertEquals(relayUrl, s.current.value?.url)
        assertEquals("sara", (s.phase.value as AppSession.Phase.SignedIn).user.username)
        assertEquals("the TV's tokens, under the relay's address only", FRESH, tokens.getValue(relayUrl).access)
        assertNull(tokens[lan]?.access)
        assertTrue("the plain LAN address is never tried", seenNow().none { it.first == "other" })
        val redeem = seenNow().single { it.second.url.encodedPath == "/api/pairing/device/token" }.second
        assertEquals("""{"device_code":"$GRANT","device_label":"Living room TV"}""", redeem.body!!.utf8())
        assertNull("redeemed without a token", redeem.headers["Authorization"])
        assertTrue("the grant is in no URL", seenNow().none { GRANT in it.second.url.toString() })
        assertEquals("the home's own id kept, the cloud's as its cloud server", "instance-7", s.current.value?.id)
        assertEquals(CLOUD_ID, s.current.value?.cloudServer)
    }

    @Test
    fun theHomesLanNameIsUsedFirst() = runBlocking {
        // its LAN name under its certificate (here `localhost`), and a relay that is not reached
        lateinit var lanUrl: String
        val lanHome = home("lan", relayTls(), urls = { listOf(lanUrl) })
        lanUrl = lanHome.url("/").toString().trimEnd('/').replace("127.0.0.1", "localhost")
        val tokens = mutableMapOf<String, MemoryTokens>()
        val s = session(tokens)
        s.signInWithTvApproval(TvApproval(CLOUD_ID, "Den", conns(lanUrl to "local", "https://den.relay.invalid" to "relay"), GRANT), "TV", probe)
        signedIn(s)
        assertEquals(lanUrl, s.current.value?.url)
        assertEquals(FRESH, tokens.getValue(lanUrl).access)
    }

    @Test
    fun noProvenAddressAnsweringIsSaidAndNothingSent() = runBlocking {
        // only a plain address: never tried, so no home, and the grant goes nowhere
        val other = home("other", null, urls = { listOf("https://den.relay.maktaba.tv") }, homeId = "instance-9")
        val s = session(mutableMapOf())
        try {
            s.signInWithTvApproval(TvApproval(CLOUD_ID, "Den", conns(other.url("/").toString() to "local"), GRANT), "TV", probe)
            fail("no address a certificate proves")
        } catch (_: NotAHome) {
        }
        assertTrue(seenNow().none { it.first == "other" })
        assertTrue(s.phase.value !is AppSession.Phase.SignedIn)
    }

    @Test
    fun aRefusedGrantIsSaidAndTheHomeNotOpened() = runBlocking {
        val refusing = server("refusing", relayTls()) { r ->
            when (r.url.encodedPath) {
                "/api/discovery/home" -> json(200, """{"id":"instance-7","name":"Den","urls":[]}""")
                else -> json(410, """{"error":"expired_token"}""")
            }
        }
        val url = refusing.url("/").toString().trimEnd('/').replace("127.0.0.1", "localhost")
        val tokens = mutableMapOf<String, MemoryTokens>()
        val s = session(tokens)
        try {
            s.signInWithTvApproval(TvApproval(CLOUD_ID, "Den", conns(url to "relay"), GRANT), "TV", probe)
            fail("the home refused the grant")
        } catch (e: com.hamzalabs.maktaba.core.api.ApiException.Status) {
            assertEquals(410, e.code)
        }
        assertNull(tokens[url]?.access)
        assertNull("the home is not opened without a sign-in", s.current.value)
    }

    private fun approvalJson(grant: String = GRANT) =
        """{"status":"approved","home_id":"$CLOUD_ID","home_name":"Den","connections":[{"url":"https://den.relay.maktaba.tv","kind":"relay"}],"grant":"$grant"}"""

    private companion object {
        const val POLL_SECRET = "ps-0123456789abcdefghij"
        const val GRANT = "Gr4nt_zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
        /** The cloud's server id: not the id the home answers with. */
        const val CLOUD_ID = "7d0c1e9a-5b2f-4c1e-9a7d-3e2f1b0c4d5a"
        val FRESH = jwt(System.currentTimeMillis() / 1000 + 900, "home")
    }
}
