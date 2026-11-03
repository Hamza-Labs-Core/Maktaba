package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.CapabilitiesAnswer
import com.hamzalabs.maktaba.core.api.ErrorKind
import com.hamzalabs.maktaba.core.api.Feature
import com.hamzalabs.maktaba.core.api.capabilities
import com.hamzalabs.maktaba.core.api.componentsUrl
import com.hamzalabs.maktaba.core.api.errorKind
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.Home
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
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
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.util.Collections

/** Story 51.11 T10: what the home has on, read, refreshed with its ETag, and kept when a refresh fails. */
class CapabilitiesTest {
    private lateinit var server: MockWebServer
    private val asked: MutableList<RecordedRequest> = Collections.synchronizedList(mutableListOf())
    @Volatile private var answer: (RecordedRequest) -> MockResponse = { json(200, VIEWER) }

    private fun json(code: Int, body: String = "") =
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse = when (request.url.encodedPath) {
                "/api/auth/me" -> json(200, """{"id":"u1","username":"amina","home_name":"Darwish home"}""")
                "/api/capabilities" -> { asked += request; answer(request) }
                else -> json(404, "{}")
            }
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    private fun client() = ApiClient(server.url("/"), MemoryTokens(jwt(System.currentTimeMillis() / 1000 + 900), "r"))

    // ---------- the answer ----------

    @Test
    fun aViewersAnswerSaysWhatIsOff() {
        val c = Capabilities.parse(VIEWER)!!
        assertFalse(c.isOn(Feature.SEMANTIC_SEARCH))
        assertFalse(c.isOn(Feature.REMOTE_ACCESS))
        assertTrue(c.isOn(Feature.LIVE_TV))
        assertFalse("no admin block: a viewer", c.admin)
        assertTrue("hidden from a viewer", c.hides(Feature.SEMANTIC_SEARCH))
        assertFalse(c.explains(Feature.SEMANTIC_SEARCH))
        assertEquals("c-1842-1", c.etag)
    }

    @Test
    fun anUnknownFeatureIsIgnoredAndAMissingOneIsOn() {
        // a newer home's feature this app does not know, and an older home that does not name youtube
        val c = Capabilities.parse("""{"etag":"c-1-0","features":{"live_tv":"off","holograms":"off","transcripts":"on"}}""")!!
        assertEquals(setOf(Feature.LIVE_TV), c.off)
        assertTrue("not named: on", c.isOn(Feature.YOUTUBE))
        assertTrue("unknown: ignored", c.isOn("holograms"))
    }

    @Test
    fun anAdminsAnswerExplainsAndCountsThePartsOff() {
        val c = Capabilities.parse(ADMIN)!!
        assertTrue(c.admin)
        assertTrue(c.explains(Feature.LIVE_TV))
        assertFalse("an admin keeps the place", c.hides(Feature.LIVE_TV))
        assertEquals(listOf("livetv.slots", "livetv.inprocess"), c.adminOff.getValue(Feature.LIVE_TV).components)
        assertEquals("switched_off", c.adminOff.getValue(Feature.LIVE_TV).reason)
        assertEquals("each component once, across the features it turns off", 3, c.componentsOff)
    }

    @Test
    fun beforeAnyAnswerEverythingIsOn() {
        Feature.known.forEach { assertTrue(it, Capabilities.AllOn.isOn(it)) }
        assertNull(Capabilities.parse("<html>"))
        assertNull(Capabilities.parse("""{"etag":"x"}"""))
    }

    @Test
    fun theComponentsPageIsUnderTheHomesBasePath() {
        assertEquals("https://example.com/maktaba/settings/components", componentsUrl("https://example.com/maktaba/"))
    }

    // ---------- the call ----------

    @Test
    fun aRefreshSendsTheETagAndA304KeepsTheAnswer() = runBlocking {
        val first = client().capabilities() as CapabilitiesAnswer.Updated
        assertEquals("c-1842-1", first.etag)
        assertNull("the first ask has nothing to match", asked.last().headers["If-None-Match"])

        answer = { r -> if (r.headers["If-None-Match"] == "\"c-1842-1\"") json(304) else json(200, ADMIN) }
        assertSame(CapabilitiesAnswer.NotModified, client().capabilities(first.etag))
        assertEquals("\"c-1842-1\"", asked.last().headers["If-None-Match"])
    }

    @Test
    fun anOldHomeWithoutTheRouteHasEverythingOn() = runBlocking {
        answer = { MockResponse.Builder().code(404).body("404 page not found").build() }
        val a = client().capabilities() as CapabilitiesAnswer.Updated
        assertEquals(Capabilities.AllOn, a.capabilities)
    }

    @Test
    fun anotherFailureIsThrown() = runBlocking {
        answer = { json(503, """{"type":"about:blank","title":"starting","status":503}""") }
        try {
            client().capabilities()
            fail("a 503 is not an answer")
        } catch (e: ApiException.Status) {
            assertEquals(503, e.code)
        }
    }

    // ---------- the session keeps it ----------

    private suspend fun signedIn(gate: Boolean = true): AppSession {
        val s = AppSession(
            MapStore(), CoroutineScope(Dispatchers.Unconfined),
            { MemoryTokens(jwt(System.currentTimeMillis() / 1000 + 900), "r") },
            capabilitiesGate = gate,
        )
        s.use(Home(server.url("/").toString().trimEnd('/'), "Darwish home"))
        withTimeout(5_000) { s.phase.first { it is AppSession.Phase.SignedIn } }
        return s
    }

    @Test
    fun theSessionStartsAllOnThenFollowsTheHome() = runBlocking {
        answer = { json(200, ADMIN) }
        val s = signedIn()
        s.refreshCapabilities()
        assertTrue(s.capabilities.value.explains(Feature.LIVE_TV))
        assertEquals("c-1843-1", s.capabilities.value.etag)
    }

    @Test
    fun a304KeepsTheEtagAndAFailureKeepsTheLastGoodAnswer() = runBlocking {
        answer = { json(200, ADMIN) }
        val s = signedIn()
        s.refreshCapabilities()
        val good = s.capabilities.value

        answer = { r -> if (r.headers["If-None-Match"] == "\"c-1843-1\"") json(304) else json(200, VIEWER) }
        s.refreshCapabilities()
        assertEquals("\"c-1843-1\"", asked.last().headers["If-None-Match"])
        assertEquals("a 304 keeps it", good, s.capabilities.value)

        answer = { json(500, """{"type":"about:blank","title":"boom","status":500}""") }
        s.refreshCapabilities()
        assertEquals("a failure keeps the last good answer", good, s.capabilities.value)

        answer = { throw IllegalStateException("no answer") }
        s.refreshCapabilities()
        assertEquals(good, s.capabilities.value)
    }

    @Test
    fun aHomeThatNeverAnswersLeavesEverythingOn() = runBlocking {
        answer = { json(500) }
        val s = signedIn()
        s.refreshCapabilities()
        assertEquals(Capabilities.AllOn, s.capabilities.value)
    }

    @Test
    fun signingOutAndAnotherHomeStartAgainFromAllOn() = runBlocking {
        answer = { json(200, ADMIN) }
        val s = signedIn()
        s.refreshCapabilities()
        assertFalse(s.capabilities.value.isOn(Feature.LIVE_TV))
        s.signOut()
        assertEquals(Capabilities.AllOn, s.capabilities.value)

        val t = signedIn()
        t.refreshCapabilities()
        t.switchHome()
        assertEquals(Capabilities.AllOn, t.capabilities.value)
    }

    @Test
    fun theBuildSwitchOffKeepsEverythingOn() = runBlocking {
        answer = { json(200, ADMIN) }
        val s = signedIn(gate = false)
        s.refreshCapabilities()
        assertEquals(Capabilities.AllOn, s.capabilities.value)
        assertTrue("nothing is asked", asked.isEmpty())
    }

    // ---------- T14: an older app's error path ----------

    @Test
    fun aComponentOffAnswerMapsToAKindWithoutAnException() {
        // what 51.09's routes answer when their component is off; the error parser of this
        // release (and any older one: an unknown code is still a 409) reads it without throwing
        val body = """{"type":"https://maktaba.tv/problems/component-off","title":"This feature is off","status":409,""" +
            """"detail":"livetv is turned off on this home","instance":"/api/channels/now","code":"component_off","component":"livetv","feature":"live_tv"}"""
        assertEquals(ErrorKind.Off, ApiException.Status(409, body).errorKind())
        assertEquals("an older code-less 409 is the generic kind", ErrorKind.Server, ApiException.Status(409, """{"title":"Conflict","status":409}""").errorKind())
    }

    companion object {
        const val VIEWER = """{"etag":"c-1842-1","features":{"live_tv":"on","transcripts":"on","subtitles":"on","semantic_search":"off",""" +
            """"chapters":"on","youtube":"on","plex":"on","remote_access":"off","recommendations":"on"}}"""

        const val ADMIN = """{"etag":"c-1843-1","features":{"live_tv":"off","transcripts":"on","subtitles":"on","semantic_search":"off",""" +
            """"chapters":"on","youtube":"on","plex":"on","remote_access":"on","recommendations":"on"},""" +
            """"admin":{"manage_components":true,"off":{"live_tv":{"components":["livetv.slots","livetv.inprocess"],"reason":"switched_off"},""" +
            """"semantic_search":{"components":["search.semantic"],"reason":"switched_off"}}}}"""
    }
}
