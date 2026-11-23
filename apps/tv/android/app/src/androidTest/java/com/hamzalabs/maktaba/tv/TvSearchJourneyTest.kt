package com.hamzalabs.maktaba.tv

import android.view.KeyEvent
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsNodeInteractionsProvider
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isFocused
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performImeAction
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTextInput
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.search.SearchModel
import com.hamzalabs.maktaba.core.session.TokenStore
import com.hamzalabs.maktaba.tv.data.TvApi
import com.hamzalabs.maktaba.tv.ui.screens.SearchScreen
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaTvTheme
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
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
import java.util.Base64
import java.util.concurrent.CopyOnWriteArrayList

/**
 * Story 49.11 on the Android TV emulator (run by tools/ci/firetv-journey.sh): Search with the
 * remote, against a mock home on the device itself. The words are typed and searched with the
 * keyboard's Search key; then only the D-pad and its centre key: right along the order to
 * Newest, which runs the search in that order; right to Filters, which opens the facets' rows;
 * down to the libraries' chips, where the centre key narrows the search to one, and the focus
 * stays on that chip while the narrowed answer comes back. Story 49.14: right from Results to
 * AI mode, centre asks it; down reaches the first sentence's citation, whose centre plays the
 * video from the cited moment.
 */
@RunWith(AndroidJUnit4::class)
class TvSearchJourneyTest {
    @get:Rule val rule = createComposeRule()

    private val server = MockWebServer()
    private val asked: MutableList<RecordedRequest> = CopyOnWriteArrayList()

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

    private fun body(r: RecordedRequest): JsonObject = Json.parseToJsonElement(r.body?.utf8() ?: "{}").jsonObject

    // a real home keeps each search under a fresh id
    private fun results(q: String, sort: String) = """{"search_id":"s${searches().size}","q":"$q","mode":"hybrid","sort":"$sort",
        "videos":[{"video_id":"v1","title":"On Patience","library_id":"l1","library_name":"Lessons","duration_sec":2400}],"videos_total":1,
        "transcript_videos":[{"video_id":"v4","title":"The Night Journey","library_id":"l1","passages":[{"start_sec":754,"snippet":"and so <mark>patience</mark> is half"}]}],
        "transcript_total":1,
        "facets":{"library":[{"value":"l1","label":"Lessons","count":3},{"value":"l2","label":"Talks","count":1}],
          "language":[{"value":"en","count":3}],"speaker":[{"value":"Shaykh Ahmad","count":2}],
          "year":[{"value":"2024","count":3,"filter":{"date":{"gte":"2024-01-01T00:00:00.000Z","lt":"2025-01-01T00:00:00.000Z"}}}],
          "duration":[{"value":"over_60m","count":2,"filter":{"duration_sec":{"gte":3600}}}],"videos":4}}"""

    @Before
    fun start() {
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                asked += request
                return when (request.url.encodedPath) {
                    "/api/search/suggest" -> json("""{"suggestions":["patience"],"items":[{"text":"patience","kind":"term"}]}""")
                    "/api/search" -> {
                        val b = body(request)
                        json(results(b["q"]!!.jsonPrimitive.content, b["sort"]?.jsonPrimitive?.content ?: "relevance"))
                    }
                    // story 49.14: AI mode's answer, as the home streams it
                    "/api/search/ai" -> MockResponse.Builder().code(200).setHeader("Content-Type", "text/event-stream").body(AI_STREAM).build()
                    else -> json("{}")
                }
            }
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    private fun key(code: Int) {
        InstrumentationRegistry.getInstrumentation().sendKeyDownUpSync(code)
        rule.waitForIdle()
    }

    private fun wait(what: () -> Boolean) = rule.waitUntil(30_000) { rule.waitForIdle(); what() }

    private fun has(tag: String) = rule.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty()

    private fun searches() = asked.filter { it.method == "POST" && it.url.encodedPath == "/api/search" }.map(::body)

    /** The test tags of the focused node and its ancestors. */
    private fun SemanticsNodeInteractionsProvider.focusedTags(): List<String> =
        onAllNodes(isFocused()).fetchSemanticsNodes().flatMap { n ->
            generateSequence(n) { it.parent }.mapNotNull { it.config.getOrNull(SemanticsProperties.TestTag) }.toList()
        }

    @Test
    fun searchWithTheRemote() {
        val api = TvApi(ApiClient(server.url("/"), Tokens(token, "r1")))
        val model = SearchModel(api.client)
        rule.setContent { MaktabaTvTheme { SearchScreen(api, model, focusFirst = false, onOpen = {}, onPlay = { _, _ -> }) } }

        val field = rule.onNode(hasSetTextAction())
        field.performTextInput("patience")
        field.performImeAction()
        // the remote from here on: the on-screen keyboard is put away, as Back does on a TV
        androidx.test.espresso.Espresso.closeSoftKeyboard()
        wait { has("tv-title-v1") }
        assertEquals("patience", searches().single()["q"]!!.jsonPrimitive.content)

        // the order, with the D-pad: Relevance, right to Newest, centre
        rule.onNodeWithTag("tv-sort-relevance").performSemanticsAction(SemanticsActions.RequestFocus)
        rule.waitForIdle()
        key(KeyEvent.KEYCODE_DPAD_RIGHT)
        assertTrue("focus on Newest: ${rule.focusedTags()}", "tv-sort-newest" in rule.focusedTags())
        key(KeyEvent.KEYCODE_DPAD_CENTER)
        wait { searches().size == 2 }
        assertEquals("newest", searches().last()["sort"]!!.jsonPrimitive.content)
        wait { rule.onAllNodes(hasText("✓ Newest", substring = true), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() }

        // right past Most matches to Filters, centre: the facets' rows open
        key(KeyEvent.KEYCODE_DPAD_RIGHT)
        key(KeyEvent.KEYCODE_DPAD_RIGHT)
        assertTrue("focus on Filters: ${rule.focusedTags()}", "tv-filters" in rule.focusedTags())
        key(KeyEvent.KEYCODE_DPAD_CENTER)
        wait { has("tv-facet-library") }

        // down to the libraries' chips; centre narrows the search to the one focused
        key(KeyEvent.KEYCODE_DPAD_DOWN)
        val chip = rule.focusedTags().firstOrNull { it.startsWith("tv-facet-library-") }
        assertTrue("focus on a library's chip: ${rule.focusedTags()}", chip != null)
        key(KeyEvent.KEYCODE_DPAD_CENTER)
        wait { searches().size == 3 }
        val lib = chip!!.removePrefix("tv-facet-library-")
        assertEquals("[\"$lib\"]", searches().last()["filters"]!!.jsonObject["library_id"].toString())
        assertEquals("the order is kept", "newest", searches().last()["sort"]!!.jsonPrimitive.content)
        wait { rule.onAllNodes(hasText("✓ ", substring = true), useUnmergedTree = true).fetchSemanticsNodes().size >= 2 }
        assertTrue("the focus stays on the chip pressed: ${rule.focusedTags()}", chip in rule.focusedTags())
    }

    @Test
    fun aiModeWithTheRemote() {
        val api = TvApi(ApiClient(server.url("/"), Tokens(token, "r1")))
        val model = SearchModel(api.client)
        val played = CopyOnWriteArrayList<Pair<String, Double>>()
        rule.setContent { MaktabaTvTheme { SearchScreen(api, model, focusFirst = false, onOpen = {}, onPlay = { v, s -> played += v to s }) } }

        val field = rule.onNode(hasSetTextAction())
        field.performTextInput("patience")
        field.performImeAction()
        androidx.test.espresso.Espresso.closeSoftKeyboard()
        wait { has("tv-title-v1") }

        // Results, right to AI mode, centre: AI mode asks the search's words
        rule.onNodeWithTag("tv-tab-results").performSemanticsAction(SemanticsActions.RequestFocus)
        rule.waitForIdle()
        key(KeyEvent.KEYCODE_DPAD_RIGHT)
        assertTrue("focus on AI mode: ${rule.focusedTags()}", "tv-tab-ai" in rule.focusedTags())
        key(KeyEvent.KEYCODE_DPAD_CENTER)
        wait { has("tv-ai-cite-1-0-1") }
        val ai = asked.filter { it.url.encodedPath == "/api/search/ai" }.map(::body)
        assertEquals("patience", ai.single()["q"]!!.jsonPrimitive.content)

        // down to the first sentence's citation; centre plays from its moment
        key(KeyEvent.KEYCODE_DPAD_DOWN)
        assertTrue("focus on the first citation: ${rule.focusedTags()}", "tv-ai-cite-1-0-1" in rule.focusedTags())
        key(KeyEvent.KEYCODE_DPAD_CENTER)
        wait { played.isNotEmpty() }
        assertEquals("v4" to 754.0, played.single())
    }

    private companion object {
        const val AI_STREAM = ": ping\n\n" +
            "event: sources\ndata: {\"q\":\"patience\",\"passages\":[{\"n\":1,\"video_id\":\"v4\",\"title\":\"The Night Journey\",\"library_name\":\"Lessons\",\"start_sec\":754,\"end_sec\":760,\"text\":\"and so patience is half of faith\"}]}\n\n" +
            "event: text\ndata: {\"text\":\"Patience is half of faith.\",\"citations\":[{\"n\":1,\"video_id\":\"v4\",\"start_sec\":754,\"end_sec\":760,\"quote\":\"and so patience is half of faith\"}]}\n\n" +
            "event: done\ndata: {\"answered\":true,\"sentences\":1,\"dropped\":0,\"took_ms\":40}\n\n"
    }
}
