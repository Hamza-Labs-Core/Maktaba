package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.NotAvailable
import com.hamzalabs.maktaba.core.api.endSignInSession
import com.hamzalabs.maktaba.core.api.favorites
import com.hamzalabs.maktaba.core.api.forgetHistory
import com.hamzalabs.maktaba.core.api.homeVersion
import com.hamzalabs.maktaba.core.api.playlist
import com.hamzalabs.maktaba.core.api.playlists
import com.hamzalabs.maktaba.core.api.signInSessions
import com.hamzalabs.maktaba.core.api.watchHistory
import kotlinx.coroutines.runBlocking
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
 * The Profile's calls (story 48.31), on a MockWebServer answering with the home's shapes
 * (`src/test/resources/profile/`; `api/internal/handlers/browse/native_profile_fixtures_test.go`
 * and its sessions twin in `handlers/auth` decode the same files into the handlers' own types,
 * refusing unknown fields, so they cannot drift from the API).
 */
class ProfileApiTest {
    private lateinit var server: MockWebServer
    private val seen: MutableList<RecordedRequest> = Collections.synchronizedList(mutableListOf())
    private val token = jwt(4_000_000_000L)

    private val v1 = "11111111-0000-4000-8000-000000000001"
    private val v2 = "11111111-0000-4000-8000-000000000002"
    private val pl = "cccccccc-0000-4000-8000-000000000001"

    private fun fixture(name: String): String =
        requireNotNull(javaClass.getResource("/profile/$name")) { "no fixture $name" }.readText()

    private val routes = mapOf(
        "GET /api/me/history" to "history.json",
        "GET /api/me/sessions" to "sessions.json",
        "GET /api/me/playlists" to "playlists.json",
        "GET /api/me/playlists/$pl" to "playlist.json",
        "GET /api/me/videos" to "favorites.json",
        "GET /api/system/version" to "version.json",
    )

    private var answer: ((RecordedRequest) -> MockResponse?)? = null

    private fun json(code: Int, body: String) =
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                seen += request
                answer?.invoke(request)?.let { return it }
                if (request.method == "DELETE") return MockResponse.Builder().code(204).build()
                val file = routes["${request.method} ${request.url.encodedPath}"]
                    ?: return json(404, """{"type":"https://maktaba.tv/problems/not-found","title":"not found","status":404}""")
                return json(200, fixture(file))
            }
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    private fun client(tokens: MemoryTokens = MemoryTokens(token, "r")) = ApiClient(server.url("/"), tokens)
    private fun last(): RecordedRequest = seen.last()

    @Test
    fun historyListsNewestFirstWithProgress() = runBlocking {
        val items = client().watchHistory()
        assertEquals("/api/me/history", last().url.encodedPath)
        assertEquals("0", last().url.queryParameter("offset"))
        assertEquals("50", last().url.queryParameter("limit"))
        assertEquals("Bearer $token", last().headers["Authorization"])
        assertEquals(listOf(v2, v1, "11111111-0000-4000-8000-000000000003"), items.map { it.videoId })
        assertEquals("سيرة ابن هشام ٢", items[0].title)
        assertEquals(0.25, items[0].watchedFraction!!, 1e-9)
        assertEquals(2, items[1].timesWatched)
        assertEquals(1.0, items[1].watchedFraction!!, 0.0)
        // not started, no length: nothing to show
        assertNull(items[2].watchedFraction)
    }

    @Test
    fun historyPagesByOffset() = runBlocking {
        client().watchHistory(offset = 50, limit = 25)
        assertEquals("50", last().url.queryParameter("offset"))
        assertEquals("25", last().url.queryParameter("limit"))
    }

    @Test
    fun forgettingAVideoDeletesItsHistory() = runBlocking {
        client().forgetHistory(v2)
        assertEquals("DELETE", last().method)
        assertEquals("/api/me/history/$v2", last().url.encodedPath)
    }

    @Test
    fun sessionsListEveryFieldAndToleratesNulls() = runBlocking {
        val s = client().signInSessions()
        assertEquals("/api/me/sessions", last().url.encodedPath)
        assertEquals(2, s.size)
        assertEquals("192.168.1.20", s[0].ip)
        assertTrue(s[0].userAgent!!.contains("Safari"))
        assertEquals("2026-11-01T08:59:00Z", s[0].lastSeenAt)
        assertFalse(s[0].current)
        assertNull(s[1].ip)
        assertNull(s[1].userAgent)
    }

    @Test
    fun signingOutASessionDeletesIt() = runBlocking {
        client().endSignInSession("c2Vzc2lvbi1vbmU")
        assertEquals("DELETE", last().method)
        assertEquals("/api/me/sessions/c2Vzc2lvbi1vbmU", last().url.encodedPath)
    }

    @Test
    fun playlistsListTheirCountsAndPictures() = runBlocking {
        val p = client().playlists()
        assertEquals("/api/me/playlists", last().url.encodedPath)
        assertEquals(listOf("رمضان", "Weekend documentaries"), p.map { it.name })
        assertEquals(2L, p[0].videos)
        assertEquals(5650.0, p[0].durationSec, 0.0)
        assertEquals(listOf(v1, v2), p[0].posterVideoIds)
        assertTrue(p[0].handOrdered)
        assertTrue(p[1].posterVideoIds.isEmpty())
    }

    @Test
    fun onePlaylistHasItsVideosInOrder() = runBlocking {
        val doc = client().playlist(pl)
        assertEquals("/api/me/playlists/$pl", last().url.encodedPath)
        assertEquals("رمضان", doc.playlist.name)
        assertEquals(listOf(v2, v1), doc.items.map { it.id })
        assertEquals(0.25, doc.items[0].watchedFraction!!, 1e-9)
        assertTrue(doc.items[1].completed == true)
    }

    @Test
    fun aMissingPlaylistIsNotAvailable() = runBlocking {
        try {
            client().playlist("cccccccc-0000-4000-8000-00000000dead")
            fail("expected NotAvailable")
        } catch (e: NotAvailable) {
            assertEquals(404, e.status.code)
        }
    }

    @Test
    fun favoritesAreTheCallersOwnList() = runBlocking {
        val page = client().favorites()
        assertEquals("/api/me/videos", last().url.encodedPath)
        assertEquals("true", last().url.queryParameter("favorite"))
        assertEquals("Tafsir al-Kahf, part 1", page.items.single().title)
        assertNull(page.next)
        client().favorites(cursor = "abc")
        assertEquals("abc", last().url.queryParameter("cursor"))
    }

    @Test
    fun theHomesVersion() = runBlocking {
        assertEquals("0.3.52", client().homeVersion())
        answer = { if (it.url.encodedPath == "/api/system/version") json(200, """{"version":"  "}""") else null }
        assertNull(client().homeVersion())
    }

    @Test
    fun unknownFieldsAndOddRowsAreTolerated() = runBlocking {
        answer = {
            if (it.url.encodedPath == "/api/me/history") {
                json(200, """{"items":[{"video_id":"a","title":"A","new_field":1},{"title":"no id"},{"video_id":"b"}],"extra":true}""")
            } else {
                null
            }
        }
        assertEquals(listOf("a", "b"), client().watchHistory().map { it.videoId })
    }

    @Test
    fun anUnrefreshable401IsTheSignedOutPath() = runBlocking {
        answer = { r ->
            when (r.url.encodedPath) {
                "/api/auth/refresh" -> json(401, """{"title":"expired","status":401}""")
                else -> json(401, """{"title":"unauthorized","status":401}""")
            }
        }
        try {
            client().signInSessions()
            fail("expected SignedOut")
        } catch (e: ApiException.SignedOut) {
            // the app goes back to signing in
        }
    }
}
