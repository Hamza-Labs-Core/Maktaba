package com.hamzalabs.maktaba.tv

import androidx.activity.ComponentActivity
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.assertIsFocused
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performSemanticsAction
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.queue.QueueSource
import com.hamzalabs.maktaba.core.session.TokenStore
import com.hamzalabs.maktaba.tv.data.TvApi
import com.hamzalabs.maktaba.tv.ui.screens.library.LibraryTabsScreen
import com.hamzalabs.maktaba.tv.ui.screens.library.LocalTvNav
import com.hamzalabs.maktaba.tv.ui.screens.library.PlaylistScreen
import com.hamzalabs.maktaba.tv.ui.screens.library.SeriesScreen
import com.hamzalabs.maktaba.tv.ui.screens.library.TvNav
import com.hamzalabs.maktaba.tv.ui.screens.library.UP_NEXT_SEC
import com.hamzalabs.maktaba.tv.ui.screens.library.upNextState
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaTvTheme
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import java.util.Base64
import java.util.Collections

/**
 * Epic 56 on Android TV (stories 56.02-56.05, 56.08, 56.10, 56.11): a library's five tabs, its
 * Play and Shuffle (the Library tab's view as a library queue), the Collections tab's Kind, a
 * series' Continue taking the first focus, a smart playlist with no Remove, the Up next rule and
 * the Arabic tab names. Against a MockWebServer home.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w960dp-h540dp-xhdpi")
class LibraryViewTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private lateinit var server: MockWebServer
    private val urls: MutableList<String> = Collections.synchronizedList(mutableListOf())
    private val played = mutableListOf<Pair<QueueSource, Boolean>>()

    private class Tokens(var a: String?, var r: String?) : TokenStore {
        override val access get() = a
        override val refresh get() = r
        override fun save(tokens: AuthTokens) { a = tokens.accessToken; r = tokens.refreshToken }
        override fun clear() { a = null; r = null }
    }

    private val token: String = Base64.getUrlEncoder().withoutPadding().let { enc ->
        enc.encodeToString("""{"alg":"RS256"}""".toByteArray()) + "." +
            enc.encodeToString("""{"exp":${System.currentTimeMillis() / 1000 + 86_400}}""".toByteArray()) + ".sig"
    }

    private fun json(body: String) = MockResponse.Builder().code(200).setHeader("Content-Type", "application/json").body(body).build()

    @Before
    fun start() {
        // each test opens the library afresh (the last tab chosen is kept on the device)
        androidx.test.core.app.ApplicationProvider.getApplicationContext<android.content.Context>()
            .getSharedPreferences("library_tabs", android.content.Context.MODE_PRIVATE).edit().clear().commit()
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                urls += request.url.toString()
                val p = request.url.encodedPath
                return when {
                    p == "/api/libraries/L1/hub" -> json("""{"library":{"id":"L1","name":"Fiqh"},"rows":{"recently_added":[{"id":"v1","title":"Lesson one"}]},
                        "collection_rows":[{"collection":{"id":"c1","name":"Picked"},"items":[{"id":"v2","title":"Lesson two"}]}]}""")
                    p == "/api/libraries/L1/collections" && request.url.queryParameter("kind") == "youtube" ->
                        json("""{"items":[{"kind":"youtube","id":"y1","name":"PL Lessons","source_kind":"youtube_playlist","automatic":true}]}""")
                    p == "/api/libraries/L1/collections" -> json("""{"items":[{"kind":"youtube","id":"y1","name":"PL Lessons","source_kind":"youtube_playlist"},
                        {"kind":"plex","id":"p1","name":"Plex picks","source_kind":"plex"},{"kind":"curated","id":"c1","name":"Picked"}]}""")
                    p == "/api/series/S1" -> json("""{"id":"S1","name":"Tafsir"}""")
                    p == "/api/series/S1/episodes" -> json("""{"seasons":[{"season":1,"id":"ss1","episodes":[{"video_id":"e1","title":"One","watched":true},{"video_id":"e2","title":"Two"}]}],
                        "next_episode":{"video_id":"e2","title":"Two","season":1}}""")
                    p == "/api/me/playlists/pl1" -> json("""{"playlist":{"id":"pl1","name":"Unwatched X","kind":"smart","smart_query":{"filters":{"speaker":"X"},"limit":50}},
                        "items":[{"id":"v1","title":"Lesson one"}]}""")
                    else -> json("{}")
                }
            }
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    private fun show(content: @androidx.compose.runtime.Composable (TvApi) -> Unit) {
        val api = TvApi(ApiClient(server.url("/"), Tokens(token, "r1")))
        compose.setContent {
            MaktabaTvTheme {
                CompositionLocalProvider(LocalTvNav provides TvNav(play = { s, sh, _ -> played += s to sh })) { content(api) }
            }
        }
    }

    private fun shows(text: String) = compose.onAllNodes(hasText(text, substring = true), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    @Test
    fun aLibraryHasItsFiveTabsAndOpensOnRecommended() {
        show { LibraryTabsScreen(it, "L1", "Fiqh") }
        compose.waitUntil(5_000) { compose.waitForIdle(); shows("Lesson one") }
        for (t in listOf("Recommended", "Library", "Collections", "Playlists", "Categories")) assertTrue(t, shows(t))
        // the promoted collection is a row of Recommended (story 56.08 T07)
        assertTrue(shows("Picked"))
    }

    @Test
    fun shuffleIsALibraryQueueOfTheView() {
        show { LibraryTabsScreen(it, "L1", "Fiqh") }
        compose.waitUntil(5_000) { compose.waitForIdle(); shows("Lesson one") }
        compose.onNodeWithTag("library-shuffle").performSemanticsAction(SemanticsActions.OnClick)
        compose.waitForIdle()
        val (source, shuffle) = played.single()
        assertEquals("library", source.kind)
        assertEquals("L1", source.id)
        assertEquals(mapOf("sort" to "created_at", "order" to "desc"), source.filters)
        assertTrue(shuffle)
    }

    @Test
    fun theCollectionsTabKeepsOneKind() {
        show { LibraryTabsScreen(it, "L1", "Fiqh") }
        compose.waitUntil(5_000) { compose.waitForIdle(); shows("Collections") }
        compose.onNodeWithTag("tab-Collections").performSemanticsAction(SemanticsActions.RequestFocus)
        compose.waitUntil(5_000) { compose.waitForIdle(); shows("Plex picks") }
        assertTrue("youtube badge", shows("YouTube playlist"))
        assertTrue("plex badge", shows("Plex collection"))
        compose.onNodeWithTag("collections-kind").performSemanticsAction(SemanticsActions.OnClick)
        compose.waitUntil(5_000) { compose.waitForIdle(); !shows("Plex picks") }
        assertTrue(urls.any { it.contains("kind=youtube") })
    }

    @Test
    fun aSeriesOpensWithContinueFocusedAndPlaysTheSeries() {
        show { SeriesScreen(it, "S1") }
        compose.waitUntil(5_000) { compose.waitForIdle(); shows("Tafsir") }
        compose.onNodeWithTag("series-continue").assertIsFocused()
        compose.onNodeWithTag("series-continue").performSemanticsAction(SemanticsActions.OnClick)
        compose.waitForIdle()
        assertEquals(QueueSource("series", "S1") to false, played.single())
    }

    @Test
    fun aSmartPlaylistShowsItsRulesAndHasNoRemove() {
        show { PlaylistScreen(it, "pl1") }
        compose.waitUntil(5_000) { compose.waitForIdle(); shows("Unwatched X") }
        assertTrue(shows("speaker: X"))
        assertTrue(!shows("Remove from playlist"))
        compose.onNodeWithTag("list-play").assertIsFocused()
    }

    @Test
    @Config(qualifiers = "ar-w960dp-h540dp-xhdpi")
    fun theTabsInArabic() {
        show { LibraryTabsScreen(it, "L1", "Fiqh") }
        compose.waitUntil(5_000) { compose.waitForIdle(); shows("المقترحات") }
        for (t in listOf("المكتبة", "المجموعات", "قوائم التشغيل", "الفئات")) assertTrue(t, shows(t))
        compose.onNodeWithTag("library-shuffle", useUnmergedTree = false).assert(hasText("تشغيل عشوائي", substring = true))
    }

    @Test
    fun upNextCountsDownOnlyWithAutoplay() {
        assertNull(upNextState(remainingSec = 120.0, endedForSec = null, autoplay = true))
        assertEquals(-1, upNextState(20.0, null, true))
        assertEquals(UP_NEXT_SEC, upNextState(0.0, 0.0, true))
        assertEquals(4, upNextState(0.0, 6.0, true))
        assertEquals(0, upNextState(0.0, 12.0, true))
        assertEquals(-1, upNextState(0.0, 12.0, autoplay = false))
    }
}
