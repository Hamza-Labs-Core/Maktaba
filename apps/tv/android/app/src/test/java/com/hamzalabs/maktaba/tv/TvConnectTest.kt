package com.hamzalabs.maktaba.tv

import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.api.DeviceCode
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.ConnectLink
import com.hamzalabs.maktaba.core.session.FoundHome
import com.hamzalabs.maktaba.core.session.Home
import com.hamzalabs.maktaba.core.session.HomeProbe
import com.hamzalabs.maktaba.core.session.KeyValueStore
import com.hamzalabs.maktaba.core.session.TokenStore
import com.hamzalabs.maktaba.tv.data.HomeChoices
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.util.Collections

/**
 * The TV's home picker and sign-in by code (Epic 42 story 42.10) on the core's session: what
 * the picker lists, what a typed address tries, and signing in by code against a mock home.
 */
class TvConnectTest {
    private lateinit var server: MockWebServer
    private val seen: MutableList<RecordedRequest> = Collections.synchronizedList(mutableListOf())
    private var route: (RecordedRequest) -> MockResponse = { json(404, "{}") }

    private class MapStore : KeyValueStore {
        val map = mutableMapOf<String, String>()
        override fun get(key: String) = map[key]
        override fun put(key: String, value: String?) {
            if (value == null) map.remove(key) else map[key] = value
        }
    }

    private class MemoryTokens(var accessToken: String? = null, var refreshToken: String? = null) : TokenStore {
        override val access get() = accessToken
        override val refresh get() = refreshToken
        override fun save(tokens: AuthTokens) {
            accessToken = tokens.accessToken
            refreshToken = tokens.refreshToken
        }
        override fun clear() {
            accessToken = null
            refreshToken = null
        }
    }

    private fun json(code: Int, body: String) =
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    private fun code(n: Int) = """{"code":"ABCD-000$n","device_code":"secret-$n","expires_at":"2099-01-01T00:00:00Z",
        "verify_path":"/pair?code=ABCD-000$n","verify_url":"http://192.168.1.5:8088/pair?code=ABCD-000$n","interval":2}"""

    private val tokens = mutableMapOf<String, MemoryTokens>()
    private fun session() = AppSession(MapStore(), CoroutineScope(Dispatchers.Unconfined), { tokens.getOrPut(it) { MemoryTokens() } })

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

    // ---------- the picker ----------

    @Test
    fun aNearbyHomeIsListedOnceByItsExactAddress() {
        val saved = listOf(Home("http://kitchen.local:8088", "Kitchen", "0a1b2c3d"))
        val found = listOf(
            FoundHome("Kitchen", "http://kitchen.local:8088", "0a1b2c3d", "v1"),
            // the same id at another address: a new home (its tokens are not the saved one's)
            FoundHome("Kitchen", "http://192.168.1.9:8088", "0a1b2c3d", "v1"),
            FoundHome("Den", "https://den.local:8443/maktaba", null, null),
        )
        assertEquals(listOf("http://192.168.1.9:8088", "https://den.local:8443/maktaba"), HomeChoices.nearby(found, saved).map { it.url })
        assertEquals(3, HomeChoices.nearby(found, emptyList()).size)
    }

    @Test
    fun whatWasTypedIsALinkOrTheAddressesToTry() {
        assertNull(HomeChoices.typed("   "))
        assertNull(HomeChoices.typed("not an address"))
        assertEquals(listOf("http://192.168.1.20:8088"), HomeChoices.typed("192.168.1.20:8088")!!.urls)
        // a page's path typed by mistake: as typed first, then the root
        assertEquals(
            listOf("https://nas.example.com/maktaba", "https://nas.example.com"),
            HomeChoices.typed("https://nas.example.com/maktaba")!!.urls,
        )
        val link = HomeChoices.typed("maktaba://connect?id=h1&n=Den&u=http%3A%2F%2F10.0.0.2%3A8088&u=https%3A%2F%2Fden.relay.maktaba.app")!!
        assertEquals("Den", link.name)
        assertEquals("h1", link.id)
        assertEquals(listOf("http://10.0.0.2:8088", "https://den.relay.maktaba.app"), link.urls)
    }

    @Test
    fun theApprovePageIsReadOutWithoutItsCode() {
        assertEquals("192.168.1.5:8088/pair", HomeChoices.shownApprovePage("http://192.168.1.5:8088/pair?code=ABCD-1234"))
        assertEquals("nas.example.com/maktaba/pair", HomeChoices.shownApprovePage("https://nas.example.com/maktaba/pair?code=X"))
    }

    // ---------- connecting ----------

    @Test
    fun aTypedAddressIsAskedWhetherItIsAHomeBeforeItIsUsed() = runBlocking {
        route = { r ->
            when (r.url.encodedPath) {
                "/api/discovery/home" -> json(200, """{"id":"0a1b2c3d-0000-4000-8000-000000000000","name":"Kitchen","urls":[],"sign_in":{"password":true,"code":true}}""")
                else -> json(404, "{}")
            }
        }
        val s = session()
        val base = server.url("/").toString().trimEnd('/')
        val home = s.connect(HomeChoices.typed(base)!!, HomeProbe())
        assertEquals("Kitchen", home.name)
        assertEquals("0a1b2c3d-0000-4000-8000-000000000000", home.id)
        assertEquals(AppSession.Phase.SigningIn, s.phase.value)
        assertNull("nothing carries a token before sign-in", seen.single().headers["Authorization"])
    }

    @Test
    fun aKnownIdAtANewAddressDoesNotTakeTheOldTokens() {
        val s = session()
        tokens["http://kitchen.local:8088"] = MemoryTokens("old", "old-r")
        s.use(Home("http://kitchen.local:8088", "Kitchen", "0a1b2c3d"))
        s.switchHome()
        s.use(Home("http://192.168.1.9:8088", "Kitchen", "0a1b2c3d"))
        assertNull(tokens.getValue("http://192.168.1.9:8088").access)
        assertEquals(2, s.homes.value.size)
    }

    @Test
    fun aLinkFromOutsideWaitsForYes() {
        val s = session()
        assertTrue(s.openLink("maktaba://connect?n=Den&u=http%3A%2F%2F10.0.0.2%3A8088"))
        assertEquals(ConnectLink(null, "Den", listOf("http://10.0.0.2:8088")), s.link.value)
        assertEquals("nothing is used before yes", AppSession.Phase.ChoosingHome, s.phase.value)
        s.dismissLink()
        assertNull(s.link.value)
        assertTrue("only maktaba: links are offered", !s.openLink("https://evil.example/connect"))
    }

    // ---------- sign-in by code ----------

    @Test
    fun theTvShowsTheCodeAndANewOneWhenItRunsOut() = runBlocking {
        var starts = 0
        route = { r ->
            when (r.url.encodedPath) {
                "/api/pairing/device" -> json(201, code(++starts))
                // the first code ran out (or the home restarted): 410, ask for a new one at once
                "/api/pairing/device/token" ->
                    if (r.body!!.utf8().contains("secret-1")) json(410, """{"title":"expired"}""")
                    else json(200, """{"access_token":"a2","refresh_token":"r2"}""")
                "/api/auth/me" -> json(200, """{"id":"u1","username":"amina","home_name":"Kitchen"}""")
                else -> json(404, "{}")
            }
        }
        val s = session()
        val base = server.url("/").toString().trimEnd('/')
        s.use(Home(base, "Kitchen"))
        val shown = mutableListOf<DeviceCode>()
        s.signInWithCode("Android TV (Shield)", onCode = { shown += it }, sleep = {})
        assertEquals(listOf("ABCD-0001", "ABCD-0002"), shown.map { it.code })
        // the QR holds the home's verify_url
        assertEquals("http://192.168.1.5:8088/pair?code=ABCD-0002", shown.last().approveUrl(server.url("/")))
        assertEquals("amina", (s.phase.value as AppSession.Phase.SignedIn).user.username)
        assertEquals("a2", tokens.getValue(base).access)
        val asks = seen.filter { it.url.encodedPath == "/api/pairing/device" }
        assertTrue(asks.all { it.headers["Authorization"] == null && it.body!!.utf8().contains("\"device_label\":\"Android TV (Shield)\"") })
    }
}
