package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.ReviewAction
import com.hamzalabs.maktaba.core.api.ReviewClip
import com.hamzalabs.maktaba.core.api.ReviewReason
import com.hamzalabs.maktaba.core.api.ReviewStatus
import com.hamzalabs.maktaba.core.api.isReviewForbidden
import com.hamzalabs.maktaba.core.api.resolveFlag
import com.hamzalabs.maktaba.core.api.reviewConflict
import com.hamzalabs.maktaba.core.api.reviewFlags
import kotlinx.coroutines.runBlocking
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.HttpUrl.Companion.toHttpUrl
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test

// Epic 52 (story 52.13): the Remote's word review calls, read tolerantly.
class ReviewApiTest {
    private lateinit var server: MockWebServer
    private val seen = mutableListOf<RecordedRequest>()
    private var answer: (RecordedRequest) -> MockResponse = { json(200, "{}") }

    private fun json(code: Int, body: String) =
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                seen += request
                return answer(request)
            }
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    private fun client() = ApiClient(server.url("/"), MemoryTokens(jwt(System.currentTimeMillis() / 1000 + 3600), "r"))

    @Test
    fun decodesAPageWithAnUnknownReasonAndAnOddCandidate() = runBlocking {
        answer = {
            json(200, """{"items":[{"id":"a","original":"😀x","context":"قال 😀x خير","char_start":4,"char_len":2,
              "language":"ar","direction":"rtl","reasons":["quran_mismatch","from_the_future"],
              "candidates":[{"text":"x","source":"quran","score":0.9},{"nope":1}],"status":"open","extra":true}],
              "next_cursor":null}""")
        }
        val page = client().reviewFlags(library = "L")
        val f = page.items.single()
        assertEquals(listOf(ReviewReason.QuranMismatch, ReviewReason.Unknown), f.reasonCodes)
        assertEquals(1, f.candidates.size)
        assertEquals(ReviewStatus.Open, f.state)
        assertTrue(f.rightToLeft && f.isQuran)
        // code points: the emoji is one, as the API counts
        assertEquals(Triple("قال ", "😀x", " خير"), f.parts())
        assertEquals("L", seen.single().url.queryParameter("library"))
    }

    @Test
    fun resolvesAndReadsAConflictAndAForbidden() = runBlocking {
        answer = { json(200, """{"status":"corrected","changed":3,"edit_id":"e","batch_id":"b"}""") }
        val res = client().resolveFlag("a", ReviewAction.Correct, "the", applyToSameKey = true)
        assertEquals("b", res.batchId)
        val body = seen.last().body?.utf8() ?: ""
        assertTrue(body, body.contains("\"action\":\"correct\"") && body.contains("\"apply_to_same_key\":true"))
        answer = { json(409, """{"type":"https://maktaba.tv/problems/locked","title":"conflict","status":409,"code":"locked"}""") }
        try {
            client().resolveFlag("a", ReviewAction.Accept)
            fail("409 answered")
        } catch (e: ApiException) {
            assertEquals("locked", e.reviewConflict())
        }
        answer = { json(403, """{"type":"x","title":"forbidden","status":403}""") }
        try {
            client().reviewFlags()
            fail("403 answered")
        } catch (e: ApiException) {
            assertTrue(e.isReviewForbidden())
        }
    }

    @Test
    fun theClipResolvesAgainstTheHome() {
        val clip = ReviewClip(url = "/stream/clip/v?track=1&sig=x")
        assertEquals("https://h.local/maktaba/stream/clip/v?track=1&sig=x", clip.absolute("https://h.local/maktaba/".toHttpUrl()))
    }
}
