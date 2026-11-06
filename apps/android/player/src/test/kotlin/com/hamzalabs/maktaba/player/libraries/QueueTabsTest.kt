package com.hamzalabs.maktaba.player.libraries

import androidx.activity.ComponentActivity
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.queue.QueueSource
import com.hamzalabs.maktaba.core.session.TokenStore
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import java.util.Base64
import java.util.Collections

/**
 * Epic 56 story 56.12 on the Android Player (T05, T06, T08): Shuffle makes one shuffled library
 * queue and opens the player at its first item; the Collections tab shows the badges; the words
 * in Arabic. Against a MockWebServer home.
 */
@RunWith(AndroidJUnit4::class)
class QueueTabsTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()
    private lateinit var server: MockWebServer
    private val seen: MutableList<Pair<String, String>> = Collections.synchronizedList(mutableListOf())
    private val opened = mutableListOf<String>()

    private class Tokens(var a: String?, var r: String?) : TokenStore {
        override val access get() = a
        override val refresh get() = r
        override fun save(tokens: AuthTokens) { a = tokens.accessToken; r = tokens.refreshToken }
        override fun clear() { a = null; r = null }
    }

    private val token = Base64.getUrlEncoder().withoutPadding().let { e ->
        e.encodeToString("""{"alg":"RS256"}""".toByteArray()) + "." + e.encodeToString("""{"exp":${System.currentTimeMillis() / 1000 + 86_400}}""".toByteArray()) + ".sig"
    }

    private fun json(code: Int, body: String) = MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                seen += "${request.method} ${request.url.encodedPath}" to (request.body?.utf8() ?: "")
                return when (request.url.encodedPath) {
                    "/api/me/play-queues" -> json(201, """{"id":"Q","source":{"kind":"library","id":"L1"},"shuffle":true,"current_pos":0,"version":1,
                        "items":[{"pos":0,"id":"v7","title":"Seven"}]}""")
                    "/api/libraries/L1/collections" -> json(200, """{"items":[{"kind":"youtube","id":"y1","name":"PL lessons","source_kind":"youtube_playlist"},
                        {"kind":"plex","id":"p1","name":"Plex picks","source_kind":"plex"}]}""")
                    else -> json(404, "{}")
                }
            }
        }
        server.start()
    }

    @After fun stop() = server.close()

    private fun client() = ApiClient(server.url("/"), Tokens(token, "r1"))

    private fun shows(t: String) = compose.onAllNodes(hasText(t, substring = true), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    @Test
    fun shuffleIsOneLibraryQueueAndOpensThePlayer() {
        val c = client()
        compose.setContent {
            CompositionLocalProvider(LocalPlayVideo provides { opened += it }) {
                PlayShuffleRow(c, QueueSource("library", "L1", mapOf("sort" to "created_at", "order" to "desc")))
            }
        }
        compose.onNodeWithTag("library-shuffle").performClick()
        compose.waitUntil(5_000) { opened.isNotEmpty() }
        assertEquals(listOf("v7"), opened)
        val (_, body) = seen.first { it.first == "POST /api/me/play-queues" }
        assertTrue(body, body.contains("\"shuffle\":true") && body.contains("\"kind\":\"library\"") && body.contains("\"id\":\"L1\""))
    }

    @Test
    fun theCollectionsTabShowsTheBadges() {
        val c = client()
        compose.setContent { CollectionsList(c, "L1") }
        compose.waitUntil(5_000) { shows("PL lessons") }
        assertTrue(shows("YouTube playlist") && shows("Plex collection"))
    }

    @Test
    @Config(qualifiers = "ar")
    fun inArabic() {
        val c = client()
        compose.setContent { PlayShuffleRow(c, QueueSource("series", "S1")) }
        assertTrue(shows("تشغيل عشوائي"))
    }
}
