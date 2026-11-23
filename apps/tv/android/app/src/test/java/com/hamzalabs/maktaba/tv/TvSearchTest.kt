package com.hamzalabs.maktaba.tv

import androidx.activity.ComponentActivity
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.assertIsFocused
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performImeAction
import androidx.compose.ui.test.performKeyInput
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.pressKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.AiStreams
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
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import java.util.Base64
import java.util.concurrent.CopyOnWriteArrayList

/**
 * Story 49.11 on Android TV: Search against `POST /api/search` with story 49.8's help.
 * Suggestions with their kinds (a title opens its video), Did you mean, the order, the facets'
 * rows and the filters in use, the passage that plays from its moment, the focus that stays on
 * a chip pressed, an older home without the help, and the Arabic. Against a MockWebServer home.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w960dp-h540dp-xhdpi")
@OptIn(ExperimentalTestApi::class)
class TvSearchTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private lateinit var server: MockWebServer
    private val asked: MutableList<RecordedRequest> = CopyOnWriteArrayList()
    private val opened = mutableListOf<String>()
    private val played = mutableListOf<Pair<String, Double>>()

    /** AI mode's answer (story 49.14); a home before it has no such route. */
    private var ai: (RecordedRequest) -> MockResponse = { MockResponse.Builder().code(404).build() }

    private fun stream(body: String) = MockResponse.Builder().code(200).setHeader("Content-Type", "text/event-stream").body(body).build()

    private fun answering(passages: List<AiStreams.Passage>, sentences: List<AiStreams.Sentence>): (RecordedRequest) -> MockResponse =
        { r -> stream(AiStreams.answer(body(r)["q"]!!.jsonPrimitive.content, passages, sentences)) }

    private fun aiAsks() = asked.filter { it.method == "POST" && it.url.encodedPath == "/api/search/ai" }.map(::body)

    /** Whether the home answers with story 49.8's help (off: a home before it). */
    private var help = true

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

    private fun results(q: String, sort: String): String {
        val helpFields = if (!help) "" else ""","sort":"$sort",${if (q == "patiance") "\"did_you_mean\":\"patience\"," else ""}
            "facets":{"library":[{"value":"l1","label":"Lessons","count":3},{"value":"l2","label":"Talks","count":1}],
              "language":[{"value":"en","count":3}],"speaker":[{"value":"Shaykh Ahmad","count":2}],
              "year":[{"value":"2024","count":3,"filter":{"date":{"gte":"2024-01-01T00:00:00.000Z","lt":"2025-01-01T00:00:00.000Z"}}}],
              "duration":[{"value":"over_60m","count":2,"filter":{"duration_sec":{"gte":3600}}}],"videos":4}"""
        // a real home keeps each search under a fresh id
        return """{"search_id":"s${searches().size}","q":"$q","mode":"hybrid","videos":[{"video_id":"v1","title":"On Patience","library_id":"l1","library_name":"Lessons","duration_sec":2400}],
            "videos_total":1,"transcript_videos":[{"video_id":"v4","title":"The Night Journey","library_id":"l1","passages":[{"start_sec":754,"snippet":"and so <mark>patience</mark> is half"}]}],
            "transcript_total":1$helpFields}"""
    }

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                asked += request
                return when (request.url.encodedPath) {
                    "/api/search/suggest" -> json(
                        if (help) {
                            """{"suggestions":["patience","On Patience","Shaykh Ahmad"],"items":[{"text":"patience","kind":"term"},
                                {"text":"On Patience","kind":"title","video_id":"v1"},{"text":"Shaykh Ahmad","kind":"speaker"}]}"""
                        } else {
                            """{"suggestions":["patience"]}"""
                        },
                    )
                    "/api/search" -> {
                        val b = body(request)
                        json(results(b["q"]!!.jsonPrimitive.content, b["sort"]?.jsonPrimitive?.content ?: "relevance"))
                    }
                    // story 49.14: no answer set is a home before AI mode
                    "/api/search/ai" -> ai(request)
                    else -> json("{}")
                }
            }
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    private lateinit var model: SearchModel

    private fun show(focusFirst: Boolean = false) {
        val api = TvApi(ApiClient(server.url("/"), Tokens(token, "r1")))
        model = SearchModel(api.client)
        compose.setContent {
            MaktabaTvTheme {
                SearchScreen(api, model, focusFirst = focusFirst, onOpen = { opened += it }, onPlay = { v, s -> played += v to s })
            }
        }
    }

    private fun shows(text: String) = compose.onAllNodes(hasText(text, substring = true), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
    private fun has(tag: String) = compose.onAllNodes(androidx.compose.ui.test.hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty()
    private fun wait(what: () -> Boolean) = compose.waitUntil(STALL_MS) { compose.waitForIdle(); what() }
    private fun searches() = asked.filter { it.method == "POST" && it.url.encodedPath == "/api/search" }.map(::body)
    private fun press(tag: String) = compose.onNodeWithTag(tag).performSemanticsAction(SemanticsActions.OnClick)

    /** Brings a row of the screen into view (the screen is a lazy list the D-pad scrolls). */
    private fun scrollTo(tag: String) {
        compose.onNodeWithTag("tv-search-list").performScrollToNode(androidx.compose.ui.test.hasTestTag(tag))
        compose.waitForIdle()
    }

    private fun search(q: String) {
        val field = compose.onNode(androidx.compose.ui.test.hasSetTextAction())
        field.performTextInput(q)
        field.performImeAction()
        wait { has("tv-search-titles") }
    }

    @Test
    fun suggestionsFollowTheTypingWithTheirKindsAndATitleOpensItsVideo() {
        show()
        compose.onNode(androidx.compose.ui.test.hasSetTextAction()).performTextInput("pa")
        wait { has("tv-suggestion-2") }
        assertTrue(shows("Word"))
        assertTrue(shows("Speaker"))
        press("tv-suggestion-1")
        assertEquals(listOf("v1"), opened)
        assertTrue("a title is opened, not searched", searches().isEmpty())
    }

    @Test
    fun theSearchKeyRunsTheWordsAndAPassagePlaysFromItsMoment() {
        show()
        search("patience")
        assertEquals("patience", searches().single()["q"]!!.jsonPrimitive.content)
        assertTrue(shows("Titles"))
        assertTrue(shows("In the transcript"))
        assertTrue(shows("Play from 12:34"))
        press("tv-transcript-v4")
        wait { played.isNotEmpty() }
        assertEquals("v4" to 754.0, played.single())
        press("tv-title-v1")
        assertEquals(listOf("v1"), opened)
    }

    @Test
    fun theOrderFacetsAndFiltersInUseRunTheSearchAgainAndKeepTheFocus() {
        show()
        search("patience")
        press("tv-sort-newest")
        wait { searches().size == 2 }
        assertEquals("newest", searches().last()["sort"]!!.jsonPrimitive.content)
        wait { shows("✓ Newest") }

        press("tv-filters")
        wait { has("tv-facet-library") }
        assertTrue("each bucket with its count", shows("Talks · 1"))
        assertTrue(shows("English · 3"))
        scrollTo("tv-facet-duration")
        assertTrue(shows("Over an hour · 2"))
        scrollTo("tv-facet-library")
        compose.onNodeWithTag("tv-facet-library-l2").performSemanticsAction(SemanticsActions.RequestFocus)
        press("tv-facet-library-l2")
        wait { searches().size == 3 }
        assertEquals("[\"l2\"]", searches().last()["filters"]!!.jsonObject["library_id"].toString())
        assertEquals("newest", searches().last()["sort"]!!.jsonPrimitive.content)
        wait { shows("✓ Talks · 1") }
        // the D-pad stays where it was: on the chip just pressed
        compose.onNodeWithTag("tv-facet-library-l2").assertIsFocused()

        scrollTo("tv-facet-duration")
        press("tv-facet-duration-over_60m")
        wait { searches().size == 4 }
        assertEquals("3600", searches().last()["filters"]!!.jsonObject["duration_sec"]!!.jsonObject["gte"]!!.jsonPrimitive.content)
        scrollTo("tv-active-filters")
        wait { has("tv-active-library-l2") }
        assertTrue(shows("✕ Library: Talks"))
        assertTrue(shows("Filters (2)"))

        press("tv-active-library-l2")
        wait { searches().size == 5 }
        assertNull(searches().last()["filters"]!!.jsonObject["library_id"])
        press("tv-filters-clear")
        wait { searches().size == 6 }
        assertNull(searches().last()["filters"])
    }

    @Test
    fun didYouMeanRunsTheCorrection() {
        show()
        search("patiance")
        wait { has("tv-did-you-mean") }
        assertTrue(shows("Did you mean"))
        press("tv-did-you-mean-run")
        wait { searches().size == 2 }
        assertEquals("patience", searches().last()["q"]!!.jsonPrimitive.content)
    }

    @Test
    fun theDpadMovesFromTheOrderToTheResults() {
        show()
        search("patience")
        compose.onNodeWithTag("tv-sort-relevance").performSemanticsAction(SemanticsActions.RequestFocus)
        compose.onRoot().performKeyInput { pressKey(Key.DirectionDown) }
        compose.waitForIdle()
        // down from the order reaches the first result's card, not a dead end
        assertTrue(
            "a result card has the focus",
            compose.onAllNodes(androidx.compose.ui.test.isFocused()).fetchSemanticsNodes().any { n ->
                generateSequence(n) { it.parent }.any { p -> p.config.getOrNull(androidx.compose.ui.semantics.SemanticsProperties.TestTag)?.startsWith("tv-title-") == true }
            },
        )
    }

    @Test
    fun backFromAResultFocusesTheResults() {
        show(focusFirst = true)
        // the model already holds a search (as after Back from a video)
        kotlinx.coroutines.runBlocking { model.submit("patience") }
        wait { has("tv-search-titles") }
        compose.waitUntil(STALL_MS) {
            compose.onAllNodes(androidx.compose.ui.test.isFocused()).fetchSemanticsNodes().any { n ->
                generateSequence(n) { it.parent }.any { p -> p.config.getOrNull(androidx.compose.ui.semantics.SemanticsProperties.TestTag) == "tv-title-v1" }
            }
        }
        // a refined answer (a new search id) leaves the focus on the button pressed
        compose.onNodeWithTag("tv-sort-newest").performSemanticsAction(SemanticsActions.RequestFocus)
        press("tv-sort-newest")
        wait { searches().size == 2 }
        wait { shows("✓ Newest") }
        compose.onNodeWithTag("tv-sort-newest").assertIsFocused()
    }

    @Test
    fun anOlderHomeHasTheOrderButNoFiltersAndPlainSuggestions() {
        help = false
        show()
        compose.onNode(androidx.compose.ui.test.hasSetTextAction()).performTextInput("pa")
        wait { has("tv-suggestion-0") }
        assertTrue(!shows("Word"))
        search("patience")
        assertTrue(has("tv-sort-relevance"))
        assertTrue(!has("tv-filters"))
        assertTrue(!has("tv-did-you-mean"))
    }

    @Test
    @Config(qualifiers = "ar-w960dp-h540dp-xhdpi")
    fun theHelpIsInArabic() {
        show()
        search("patiance")
        assertTrue(shows("الترتيب حسب"))
        assertTrue(shows("الأقرب صلة"))
        assertTrue(shows("هل تقصد"))
        assertTrue(shows("العناوين"))
        press("tv-filters")
        wait { has("tv-facet-library") }
        assertTrue(shows("المكتبة"))
        assertTrue(shows("الإنجليزية"))
        scrollTo("tv-facet-duration")
        assertTrue(shows("أكثر من ساعة"))
    }

    // ---------- story 49.14: AI mode's tab, by the D-pad ----------

    /** Focuses [tag] and presses OK on it, as the remote does. */
    private fun ok(tag: String) {
        scrollTo(tag)
        compose.onNodeWithTag(tag).performSemanticsAction(SemanticsActions.RequestFocus)
        compose.onRoot().performKeyInput { pressKey(Key.DirectionCenter) }
        compose.waitForIdle()
    }

    @Test
    fun aiModeStreamsTheSourcesAndSentencesAndACitationPlaysItsMoment() {
        ai = answering(AiStreams.EN_PASSAGES, AiStreams.EN_SENTENCES)
        show()
        search("patience")
        assertTrue("Results never asks AI mode", aiAsks().isEmpty())
        ok("tv-tab-ai")
        wait { has("tv-ai-sentence-1-1") }
        assertEquals("patience", aiAsks().single()["q"]!!.jsonPrimitive.content)
        assertTrue(shows("Patience is half of faith."))
        scrollTo("tv-ai-sources-1")
        assertTrue(shows("Passages read"))
        assertTrue(has("tv-ai-passage-1-2"))
        ok("tv-ai-cite-1-1-2")
        wait { played.isNotEmpty() }
        assertEquals("v4" to 1322.5, played.single())
        // back to Results: the results are there, AI mode not asked again on its return
        ok("tv-tab-results")
        wait { has("tv-search-titles") }
        ok("tv-tab-ai")
        wait { has("tv-ai-sentence-1-1") }
        assertEquals(1, aiAsks().size)
    }

    @Test
    fun aFollowUpOnTheTvCarriesTheEarlierAnswer() {
        ai = answering(AiStreams.EN_PASSAGES, AiStreams.EN_SENTENCES)
        show()
        search("patience")
        ok("tv-tab-ai")
        wait { has("tv-ai-sentence-1-1") }
        scrollTo("tv-ai-follow-up")
        val field = compose.onNode(androidx.compose.ui.test.hasSetTextAction() and androidx.compose.ui.test.hasAnyAncestor(androidx.compose.ui.test.hasTestTag("tv-ai-follow-up")))
        field.performTextInput("and its reward?")
        field.performImeAction()
        wait { aiAsks().size == 2 }
        val turn = aiAsks().last()["history"]!!.let { it as kotlinx.serialization.json.JsonArray }.single().jsonObject
        assertEquals("patience", turn["q"]!!.jsonPrimitive.content)
        assertEquals("Patience is half of faith. Its reward comes without measure.", turn["answer"]!!.jsonPrimitive.content)
        wait { has("tv-ai-question-2") }
    }

    @Test
    fun noLanguageModelIsTheTabsMessageWithTheHelpAddress() {
        ai = { MockResponse.Builder().code(409).setHeader("Content-Type", "application/problem+json").body(AiStreams.UNAVAILABLE).build() }
        show()
        search("patience")
        ok("tv-tab-ai")
        wait { has("tv-ai-unavailable") }
        assertTrue(shows("AI mode is not set up"))
        assertTrue(shows("/help/search#ai-mode"))
    }

    @Test
    fun aBusyHomeSaysItsPlaceInLineAndWhenToAskAgain() {
        ai = {
            MockResponse.Builder().code(429).setHeader("Content-Type", "application/problem+json")
                .setHeader("Retry-After", "30").body(AiStreams.BUSY).build()
        }
        show()
        search("patience")
        ok("tv-tab-ai")
        wait { has("tv-ai-busy") }
        assertTrue(shows("AI mode is busy answering other questions. Ask again shortly. 9 questions are ahead of this one. Ask again in about 30 seconds."))
    }

    @Test
    fun anOlderHomeSaysItHasNoAiModeYet() {
        show()
        search("patience")
        ok("tv-tab-ai")
        wait { has("tv-ai-old-home") }
        assertTrue(shows("This home does not have AI mode yet."))
    }

    @Test
    @Config(qualifiers = "ar-w960dp-h540dp-xhdpi")
    fun aiModeIsInArabic() {
        ai = answering(AiStreams.AR_PASSAGES, AiStreams.AR_SENTENCES)
        show()
        search("الصبر")
        assertTrue(shows("وضع الذكاء الاصطناعي"))
        ok("tv-tab-ai")
        wait { has("tv-ai-sentence-1-1") }
        assertEquals("الصبر", aiAsks().single()["q"]!!.jsonPrimitive.content)
        assertTrue(shows("الصبر نصف الإيمان."))
        scrollTo("tv-ai-sources-1")
        assertTrue(shows("المقاطع المقروءة"))
    }
}
