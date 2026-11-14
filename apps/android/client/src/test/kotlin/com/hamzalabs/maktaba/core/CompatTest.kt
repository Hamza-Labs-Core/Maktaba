package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.compat.ApiLevels
import com.hamzalabs.maktaba.core.compat.Compat
import com.hamzalabs.maktaba.core.compat.CompatApp
import com.hamzalabs.maktaba.core.compat.CompatHome
import com.hamzalabs.maktaba.core.compat.CompatVerdict
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.ConnectLink
import com.hamzalabs.maktaba.core.session.Home
import com.hamzalabs.maktaba.core.session.HomeProbe
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.io.File
import java.util.Collections

/** Epic 58 story 58.01: the compatibility contract in the Android core. */
class CompatTest {
    // ---------- T04: the shared vectors ----------

    private fun vectors(): File {
        var dir: File? = File("").absoluteFile
        while (dir != null) {
            val f = File(dir, "shared/compat/vectors.json")
            if (f.isFile) return f
            dir = dir.parentFile
        }
        error("shared/compat/vectors.json not found above ${File("").absolutePath}")
    }

    @Test
    fun everyVectorReachesItsVerdict() {
        val cases = Json.parseToJsonElement(vectors().readText()).jsonObject.getValue("cases").jsonArray
        assertTrue("the vectors lost their cases", cases.size >= 10)
        val seen = mutableSetOf<CompatVerdict>()
        for (c in cases) {
            val o = c.jsonObject
            val h = o.getValue("home").jsonObject
            val a = o.getValue("app").jsonObject
            val home = CompatHome(
                apiLevel = h["api_level"]?.jsonPrimitive?.int ?: 0,
                apiLevelOldest = h["api_level_oldest"]?.jsonPrimitive?.int ?: 0,
                clientFloor = (h["client_floor"] as? JsonObject)?.mapValues { it.value.jsonPrimitive.content } ?: emptyMap(),
            )
            val app = CompatApp(
                a.getValue("name").jsonPrimitive.content, a.getValue("platform").jsonPrimitive.content,
                a.getValue("version").jsonPrimitive.content,
                a.getValue("min_api_level").jsonPrimitive.int, a.getValue("built_for_api_level").jsonPrimitive.int,
            )
            val got = Compat.decide(home, app)
            assertEquals(o.getValue("name").jsonPrimitive.content, o.getValue("verdict").jsonPrimitive.content, got.wire)
            seen += got
        }
        assertEquals(CompatVerdict.entries.toSet(), seen)
    }

    @Test
    fun theAppIsReadFromItsClientHeader() {
        val tv = CompatApp.fromClientHeader("tv/1.2.0 (androidtv; tv)")
        assertEquals(CompatApp("tv", "androidtv", "1.2.0", ApiLevels.MIN, ApiLevels.BUILT_FOR), tv)
        assertEquals("tv/androidtv", tv.key)
        assertEquals(CompatApp("player", "android", "dev"), CompatApp.fromClientHeader("player/dev (android; phone)"))
        assertEquals("", CompatApp.fromClientHeader("native").platform)
    }

    @Test
    fun versionsCompareByNumber() {
        assertEquals(0, Compat.compareVersions("1.4", "1.4.0"))
        assertTrue(Compat.compareVersions("1.10.0", "1.9.0")!! > 0)
        assertTrue(Compat.compareVersions("1.3.9-beta", "1.4.0")!! < 0)
        assertNull(Compat.compareVersions("dev", "1.0"))
    }

    // ---------- the session ----------

    private lateinit var home: MockWebServer
    private val asked: MutableList<String> = Collections.synchronizedList(mutableListOf())
    private var discovery = level(1, 1)
    private var login = 200 to """{"access_token":"a","refresh_token":"r"}"""

    private fun level(level: Int, oldest: Int, floor: String = "{}") =
        """{"id":"28e21b45-1111","name":"Kitchen","version":"0.3.140","urls":[],"api_level":$level,"api_level_oldest":$oldest,"client_floor":$floor}"""

    @Before
    fun start() {
        home = MockWebServer().apply {
            dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse {
                    val path = request.url.encodedPath
                    asked += path
                    val (code, body) = when (path) {
                        "/api/discovery/home" -> 200 to discovery
                        "/api/auth/login" -> login
                        "/api/auth/me" -> 200 to """{"id":"u","username":"amal","is_admin":false}"""
                        else -> 404 to "{}"
                    }
                    return MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()
                }
            }
            start()
        }
    }

    @After
    fun stop() = home.close()

    private val url get() = home.url("/").toString().trimEnd('/')

    private fun session(app: CompatApp?, tokens: MutableMap<String, MemoryTokens> = mutableMapOf()) =
        AppSession(MapStore(), CoroutineScope(Dispatchers.Unconfined), { tokens.getOrPut(it) { MemoryTokens() } }, compatApp = app)

    private val player = CompatApp("player", "android", "1.0.0")

    @Test
    fun aHomeThatNoLongerServesThisBuildShowsUpdateThisAppAndAsksNothingElse() = runBlocking {
        discovery = level(99, 99)
        val s = session(player)
        s.connect(ConnectLink(null, null, listOf(url)), HomeProbe())
        val block = s.compatBlock.value
        assertNotNull(block)
        assertEquals(CompatVerdict.UpdateApp, block!!.verdict)
        assertEquals("Kitchen", block.homeName)
        assertEquals("android", block.platform)
        assertEquals("only the home's answer was asked", listOf("/api/discovery/home"), asked.toList())
    }

    @Test
    fun aHomeFromBeforeTheContractIsLevelZeroAndTheseAppsCarryOn() = runBlocking {
        discovery = """{"id":"28e21b45-1111","name":"Kitchen","version":"0.3.100","urls":[]}"""
        val s = session(player)
        s.connect(ConnectLink(null, null, listOf(url)), HomeProbe())
        assertNull(s.compatBlock.value)
        assertEquals(AppSession.Phase.SigningIn, s.phase.value)
    }

    @Test
    fun anAppThatNeedsANewerHomeShowsUpdateTheHome() = runBlocking {
        discovery = """{"id":"28e21b45-1111","name":"Kitchen","version":"0.3.100","urls":[]}"""
        val s = session(player.copy(minApiLevel = 1))
        s.connect(ConnectLink(null, null, listOf(url)), HomeProbe())
        assertEquals(CompatVerdict.UpdateHome, s.compatBlock.value?.verdict)
        assertEquals("0.3.100", s.compatBlock.value?.homeVersion)
    }

    @Test
    fun atStartTheHomeIsAskedBeforeItsKeptSignInIsUsed() {
        discovery = level(99, 99)
        val tokens = mutableMapOf(url to MemoryTokens().apply { save(AuthTokens(jwt(System.currentTimeMillis() / 1000 + 900), "r")) })
        val s = session(player, tokens)
        s.probe = HomeProbe()
        s.use(Home(url, "Kitchen"))
        waitFor { s.compatBlock.value != null }
        assertEquals(CompatVerdict.UpdateApp, s.compatBlock.value?.verdict)
        assertTrue("no sign-in was tried: $asked", "/api/auth/me" !in asked)

        // the home updated its floor away (or rolled forward): Try again carries on and signs in
        discovery = level(1, 1)
        s.recheckCompat()
        waitFor { s.phase.value is AppSession.Phase.SignedIn }
        assertNull(s.compatBlock.value)
    }

    @Test
    fun belowTheFloorTheSignInIsRefusedWithUpdateThisApp() = runBlocking {
        login = 426 to """{"type":"https://maktaba.tv/problems/client-too-old","status":426,"code":"client_too_old","floor":"2.0.0"}"""
        val s = session(player)
        s.connect(ConnectLink(null, null, listOf(url)), HomeProbe())
        try {
            s.signIn("amal", "pw")
            fail("a 426 is not a sign-in")
        } catch (e: Exception) {
            // the screen shows the page, not a failure
        }
        assertEquals(CompatVerdict.UpdateApp, s.compatBlock.value?.verdict)
        assertNull(s.progress.value)
    }

    @Test
    fun withoutAnAppTheSessionDecidesNothing() = runBlocking {
        discovery = level(99, 99)
        val s = session(null)
        s.connect(ConnectLink(null, null, listOf(url)), HomeProbe())
        assertNull(s.compatBlock.value)
    }

    private fun waitFor(cond: () -> Boolean) {
        val until = System.currentTimeMillis() + 5_000
        while (!cond()) {
            if (System.currentTimeMillis() > until) fail("timed out")
            Thread.sleep(20)
        }
    }
}
