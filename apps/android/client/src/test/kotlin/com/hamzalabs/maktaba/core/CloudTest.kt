package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.CloudException
import com.hamzalabs.maktaba.core.session.CloudHome
import com.hamzalabs.maktaba.core.session.CloudSession
import com.hamzalabs.maktaba.core.session.FoundHome
import com.hamzalabs.maktaba.core.session.Home
import com.hamzalabs.maktaba.core.session.HomeProbe
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
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
import java.util.Collections

/**
 * Signing in through Maktaba Cloud (story 42.7): the cloud says who, the home decides what;
 * the cloud's token is never kept; a home's tokens stay with the address that issued them.
 */
class CloudTest {
    private lateinit var cloud: MockWebServer
    private lateinit var relay: MockWebServer
    private lateinit var elsewhere: MockWebServer
    private val seen: MutableList<Pair<String, RecordedRequest>> = Collections.synchronizedList(mutableListOf())
    private val homeId = "0a1b2c3d-0000-4000-8000-000000000000"
    private val fresh = jwt(System.currentTimeMillis() / 1000 + 900, "home")

    private fun json(code: Int, body: String) =
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    private fun server(name: String, route: (RecordedRequest) -> MockResponse) = MockWebServer().apply {
        dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                seen += name to request
                return route(request)
            }
        }
        start()
    }

    private val relayUrl get() = relay.url("/").toString().trimEnd('/')

    @Before
    fun start() {
        elsewhere = server("elsewhere") { json(200, "{}") }
        cloud = server("cloud") { r ->
            when (r.url.encodedPath) {
                "/v1/auth/login" -> json(200, """{"access_token":"cloud-tok","expires_in":900,"user":{"id":"c1","email":"sara@example.com"}}""")
                "/v1/homes" ->
                    if (r.headers["Authorization"] == "Bearer cloud-tok") {
                        json(200, """{"homes":[
                            {"server_id":"$homeId","name":"Kitchen","slug":"kitchen","relay_url":"$relayUrl","direct_url":"http://192.0.2.1:9","online":true,"linked":true,"owned":true},
                            {"server_id":"s2","name":"Cabin","slug":"cabin","relay_url":"https://cabin.relay.example","online":false,"linked":false,"owned":true}]}""")
                    } else {
                        json(401, """{"error":"unauthorized","message":"sign in"}""")
                    }
                "/v1/homes/$homeId/assertion" -> json(200, """{"assertion":"mkha1.p.s","server_id":"$homeId"}""")
                "/v1/redirect" -> MockResponse.Builder().code(302).setHeader("Location", elsewhere.url("/steal").toString()).build()
                else -> json(404, """{"error":"not_found","message":"no"}""")
            }
        }
        relay = server("relay") { r ->
            when (r.url.encodedPath) {
                "/api/discovery/home" -> json(200, """{"id":"$homeId","name":"Kitchen","urls":["http://192.0.2.1:9","$relayUrl"]}""")
                "/api/auth/cloud" -> json(200, """{"access_token":"$fresh","refresh_token":"r1","user":{"id":"u1","username":"sara"},"cloud_scope":"play"}""")
                "/api/auth/me" ->
                    if (r.headers["Authorization"] == "Bearer $fresh") json(200, """{"id":"u1","username":"sara","home_name":"Kitchen"}""")
                    else json(401, "{}")
                else -> json(404, "{}")
            }
        }
    }

    @After
    fun stop() {
        cloud.close()
        relay.close()
        elsewhere.close()
    }

    private fun cloudSession() = CloudSession(cloud.url("/"))

    @Test
    fun signsInToTheCloudAndListsItsHomes() = runBlocking {
        val c = cloudSession()
        assertEquals("sara@example.com", c.signIn("sara@example.com", "pw").email)
        val login = seen.first { it.first == "cloud" }.second
        assertTrue(login.body!!.utf8().contains("\"email\":\"sara@example.com\""))
        val homes = c.homes()
        assertEquals(listOf("Kitchen", "Cabin"), homes.map { it.name })
        assertTrue(homes[0].linked)
        assertFalse("owning a home is no account in it", homes[1].linked)
        assertEquals("its own address first, then the relay", listOf("http://192.0.2.1:9", relayUrl), homes[0].addresses)
        assertEquals("mkha1.p.s", c.assertion(homeId))
        assertEquals("""{"purpose":"signin"}""", seen.last { it.second.url.encodedPath.endsWith("/assertion") }.second.body!!.utf8())
    }

    @Test
    fun aRefusedCloudTokenSignsOutOfTheCloudOnly() = runBlocking {
        val c = CloudSession(cloud.url("/"))
        try {
            c.homes()
            fail("no token, no homes")
        } catch (e: CloudException) {
            assertEquals(401, e.status)
        }
        assertTrue("nothing was sent without a token", seen.isEmpty())
    }

    @Test
    fun theCloudsTokenNeverFollowsARedirect() = runBlocking {
        val c = cloudSession()
        c.signIn("sara@example.com", "pw")
        // a cloud API path that answers a redirect to another server
        val r = CloudSession(cloud.url("/v1/redirect/.."), CloudSession.defaultHttp)
        runCatching { r.homes() }
        assertTrue("the other server saw nothing", seen.none { it.first == "elsewhere" })
    }

    @Test
    fun aCloudHomeIsUsedAtTheAddressThatAnswersAndSignedInThroughTheCloud() = runBlocking {
        val store = MapStore()
        val tokens = mutableMapOf<String, MemoryTokens>()
        val s = AppSession(store, CoroutineScope(Dispatchers.Unconfined), { tokens.getOrPut(it) { MemoryTokens() } })
        val c = cloudSession()
        c.signIn("sara@example.com", "pw")
        s.cloud = c
        val home = c.homes().first()
        s.useCloudHome(home, HomeProbe())
        // its own address (192.0.2.1, a test address nothing answers at) did not answer: the relay
        assertEquals(relayUrl, s.current.value?.url)
        assertEquals(homeId, s.current.value?.cloudServer)
        assertTrue("both addresses kept", "http://192.0.2.1:9" in s.current.value!!.addresses)
        val signedIn = s.phase.value as AppSession.Phase.SignedIn
        assertEquals("sara", signedIn.user.username)
        val asked = seen.single { it.second.url.encodedPath == "/api/auth/cloud" }.second
        assertEquals("""{"assertion":"mkha1.p.s"}""", asked.body!!.utf8())
        assertNull("a cloud sign-in sends no token of its own", asked.headers["Authorization"])
        assertEquals("the home's tokens, under the relay's address only", fresh, tokens.getValue(relayUrl).access)
        assertNull(tokens["http://192.0.2.1:9"]?.access)
        assertFalse("the cloud's token is never kept", store.map.values.any { "cloud-tok" in it })
    }

    @Test
    fun awayFromHomeTheRelayIsUsedWithItsOwnSignIn() = runBlocking {
        val tokens = mutableMapOf<String, MemoryTokens>()
        val s = AppSession(MapStore(), CoroutineScope(Dispatchers.Unconfined), { tokens.getOrPut(it) { MemoryTokens() } })
        // known at its local address (signed in there), the relay among its addresses
        val local = "http://192.0.2.1:9"
        tokens.getOrPut(local) { MemoryTokens() }.save(AuthTokens("local-token", "r0"))
        val c = cloudSession()
        c.signIn("sara@example.com", "pw")
        s.cloud = c
        s.use(Home(local, "Kitchen", homeId, addresses = listOf(relayUrl), cloudServer = homeId, cloudAddresses = listOf(relayUrl)))
        // the local address does not answer (restore fails): the relay does, as the same home
        s.probe = HomeProbe()
        assertTrue(s.reachElsewhere())
        assertEquals(relayUrl, s.current.value?.url)
        assertEquals("one home, not two", 1, s.homes.value.size)
        assertTrue(local in s.current.value!!.addresses)
        assertTrue(s.phase.value is AppSession.Phase.SignedIn)
        // the local token never went to the relay
        assertTrue(seen.filter { it.first == "relay" }.none { it.second.headers["Authorization"] == "Bearer local-token" })
        assertEquals("local-token", tokens.getValue(local).access)
        assertEquals(fresh, tokens.getValue(relayUrl).access)
    }

    @Test
    fun aCloudAssertionGoesOnlyToAnAddressTheCloudGave() = runBlocking {
        val tokens = mutableMapOf<String, MemoryTokens>()
        val s = AppSession(MapStore(), CoroutineScope(Dispatchers.Unconfined), { tokens.getOrPut(it) { MemoryTokens() } })
        val c = cloudSession()
        c.signIn("sara@example.com", "pw")
        s.cloud = c
        // the relay here is only an address the home announced, not one the cloud gave
        s.use(Home("http://192.0.2.1:9", "Kitchen", homeId, addresses = listOf(relayUrl), cloudServer = homeId))
        s.probe = HomeProbe()
        assertTrue(s.reachElsewhere())
        assertEquals(relayUrl, s.current.value?.url)
        assertEquals("asked to sign in there instead", AppSession.Phase.SigningIn, s.phase.value)
        assertTrue("no assertion was asked for or sent", seen.none { it.second.url.encodedPath.endsWith("/assertion") || it.second.url.encodedPath == "/api/auth/cloud" })
        try {
            s.signInWithCloud()
            fail("refused")
        } catch (e: CloudException) {
            assertEquals("not_a_cloud_address", e.code)
        }
    }

    /**
     * Kept under a name that no longer resolves (its `.local` name changed): announced at a
     * new address with its id, the home is asked there and its entry moves; its tokens stay
     * with the address that issued them, so it signs in afresh. Another home's id moves nothing.
     */
    @Test
    fun aHomeAnnouncedAtANewAddressIsMovedTo() = runBlocking {
        val tokens = mutableMapOf<String, MemoryTokens>()
        val store = MapStore()
        val dead = "http://127.0.0.1:1"
        tokens.getOrPut(dead) { MemoryTokens() }.save(AuthTokens("old-name-token", "r0"))
        val s = AppSession(store, CoroutineScope(Dispatchers.Unconfined), { tokens.getOrPut(it) { MemoryTokens() } })
        s.probe = HomeProbe()
        s.use(Home(dead, "Kitchen", homeId, nickname = "Grandma's"))
        withTimeout(10_000) { while (s.restoreProblem.value == null) delay(20) }
        s.announced(listOf(FoundHome("Studio", relayUrl, "another-home-id", "0.40.0")))
        assertEquals("another home is not asked", dead, s.current.value?.url)
        s.announced(listOf(FoundHome("Studio", relayUrl, homeId, "0.40.0")))
        withTimeout(10_000) { while (s.current.value?.url != relayUrl) delay(20) }
        val moved = s.homes.value.single()
        assertEquals(relayUrl, moved.url)
        assertEquals(listOf(dead), moved.addresses)
        assertEquals("the person's name stays", "Grandma's", moved.nickname)
        assertEquals("the next launch starts there", relayUrl, AppSession(store, CoroutineScope(Dispatchers.Unconfined), { MemoryTokens() }).current.value?.url)
        assertEquals("signed in afresh there", AppSession.Phase.SigningIn, s.phase.value)
        assertNull(s.restoreProblem.value)
        assertTrue("the old token never went to the new address", seen.none { it.second.headers["Authorization"] == "Bearer old-name-token" })
        assertEquals("old-name-token", tokens.getValue(dead).access)
    }

    @Test
    fun anotherHomeAtAnAddressIsNotMovedTo() = runBlocking {
        val s = AppSession(MapStore(), CoroutineScope(Dispatchers.Unconfined), { MemoryTokens() })
        s.use(Home("http://192.0.2.1:9", "Kitchen", "another-home-id", addresses = listOf(relayUrl)))
        s.probe = HomeProbe()
        assertFalse("the relay answers as another home", s.reachElsewhere())
        assertEquals("http://192.0.2.1:9", s.current.value?.url)
        // and a home whose id is unknown is never moved: an id proves nothing by itself
        val t = AppSession(MapStore(), CoroutineScope(Dispatchers.Unconfined), { MemoryTokens() })
        t.use(Home("http://192.0.2.1:9", "Kitchen", null, addresses = listOf(relayUrl)))
        t.probe = HomeProbe()
        assertFalse(t.reachElsewhere())
    }

    @Test
    fun aCloudHomeKeepsItsAddressesAcrossRestarts() {
        val store = MapStore()
        val s = AppSession(store, CoroutineScope(Dispatchers.Unconfined), { MemoryTokens() })
        s.use(Home("https://kitchen.relay.example", "Kitchen", homeId, addresses = listOf("http://192.168.1.5:8088"), cloudServer = homeId))
        val again = AppSession(store, CoroutineScope(Dispatchers.Unconfined), { MemoryTokens() }).homes.value.single()
        assertEquals(listOf("http://192.168.1.5:8088"), again.addresses)
        assertEquals(homeId, again.cloudServer)
        // a home saved before addresses existed still loads
        store.put("homes", """[{"url":"http://a:8088","name":"Studio"}]""")
        assertEquals(emptyList<String>(), AppSession(store, CoroutineScope(Dispatchers.Unconfined), { MemoryTokens() }).homes.value.single().addresses)
    }
}
