package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.EpisodeSort
import com.hamzalabs.maktaba.core.api.ErrorKind
import com.hamzalabs.maktaba.core.api.HomeRails
import com.hamzalabs.maktaba.core.api.LibraryHub
import com.hamzalabs.maktaba.core.api.NotAvailable
import com.hamzalabs.maktaba.core.api.SeriesSort
import com.hamzalabs.maktaba.core.api.VideoDetail
import com.hamzalabs.maktaba.core.api.VideoQuery
import com.hamzalabs.maktaba.core.api.VideoSort
import com.hamzalabs.maktaba.core.api.chapters
import com.hamzalabs.maktaba.core.api.clearRecentSearches
import com.hamzalabs.maktaba.core.api.dismissRail
import com.hamzalabs.maktaba.core.api.dismissRecommendation
import com.hamzalabs.maktaba.core.api.errorDetails
import com.hamzalabs.maktaba.core.api.errorKind
import com.hamzalabs.maktaba.core.api.forgetRecentSearch
import com.hamzalabs.maktaba.core.api.homeRails
import com.hamzalabs.maktaba.core.api.libraryHub
import com.hamzalabs.maktaba.core.api.librarySeries
import com.hamzalabs.maktaba.core.api.librarySummaries
import com.hamzalabs.maktaba.core.api.markWatched
import com.hamzalabs.maktaba.core.api.myVideo
import com.hamzalabs.maktaba.core.api.pinnedLibraries
import com.hamzalabs.maktaba.core.api.recentSearches
import com.hamzalabs.maktaba.core.api.recommendations
import com.hamzalabs.maktaba.core.api.search
import com.hamzalabs.maktaba.core.api.searchSuggestions
import com.hamzalabs.maktaba.core.api.searchTitles
import com.hamzalabs.maktaba.core.api.searchTranscripts
import com.hamzalabs.maktaba.core.api.segments
import com.hamzalabs.maktaba.core.api.series
import com.hamzalabs.maktaba.core.api.seriesEpisodes
import com.hamzalabs.maktaba.core.api.seriesList
import com.hamzalabs.maktaba.core.api.seriesPage
import com.hamzalabs.maktaba.core.api.setFavorite
import com.hamzalabs.maktaba.core.api.setPinnedLibraries
import com.hamzalabs.maktaba.core.api.videoDetail
import com.hamzalabs.maktaba.core.api.videoFacets
import com.hamzalabs.maktaba.core.api.videoPage
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
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
 * The Player's browse calls (story 48.8), on a MockWebServer answering with the home's shapes
 * (`src/test/resources/browse/`; `api/internal/handlers/browse/native_fixtures_test.go` decodes
 * the same files into the handlers' own types, so they cannot drift from the API).
 */
class BrowseTest {
    private lateinit var server: MockWebServer
    private val seen: MutableList<RecordedRequest> = Collections.synchronizedList(mutableListOf())
    private val token = jwt(4_000_000_000L)

    private val lib = "aaaaaaaa-0000-4000-8000-000000000001"
    private val series = "bbbbbbbb-0000-4000-8000-000000000001"
    private val video = "11111111-0000-4000-8000-000000000002"
    private val sid = "ffffffff-0000-4000-8000-000000000001"

    private fun fixture(name: String): String =
        requireNotNull(javaClass.getResource("/browse/$name")) { "no fixture $name" }.readText()

    /** "METHOD path" to the fixture it answers with. */
    private val routes = mapOf(
        "GET /api/home" to "home.json",
        "GET /api/recommendations" to "recommendations.json",
        "GET /api/libraries/summary" to "libraries-summary.json",
        "GET /api/me/pinned-libraries" to "pinned-libraries.json",
        "PUT /api/me/pinned-libraries" to "pinned-libraries.json",
        "GET /api/libraries/$lib/hub" to "hub.json",
        "GET /api/libraries/$lib/series" to "library-series.json",
        "GET /api/videos/facets" to "facets.json",
        "GET /api/videos" to "videos.json",
        "GET /api/me/videos" to "videos.json",
        "GET /api/videos/$video" to "video.json",
        "GET /api/me/videos/$video" to "my-video.json",
        "GET /api/videos/$video/chapters" to "chapters.json",
        "GET /api/videos/$video/segments" to "segments.json",
        "POST /api/me/watched" to "updated.json",
        "POST /api/me/favorites" to "updated.json",
        "GET /api/series" to "series-list.json",
        "GET /api/series/$series" to "series.json",
        "GET /api/series/$series/episodes" to "episodes.json",
        "POST /api/search" to "search.json",
        "GET /api/search/suggest" to "search-suggest.json",
        "GET /api/search/recent" to "search-recent.json",
    )

    /** Overrides the fixtures for one test. */
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
                val path = request.url.encodedPath
                if (path == "/api/search/results/$sid") {
                    return json(200, fixture(if (request.url.queryParameter("list") == "titles") "search-titles.json" else "search-transcripts.json"))
                }
                if (request.method == "DELETE") return MockResponse.Builder().code(204).build()
                val file = routes["${request.method} $path"]
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
    private fun RecordedRequest.bodyJson(): JsonObject =
        ApiClient.json.parseToJsonElement(requireNotNull(body).utf8()).jsonObject

    // ---------- Home ----------

    @Test
    fun homeDecodesTheHeroAndEveryRow() = runBlocking {
        val home = client().homeRails()
        val hero = requireNotNull(home.featured)
        assertEquals("series", hero.kind)
        assertEquals("سيرة ابن هشام", hero.series?.name)
        assertEquals(24L, hero.series?.episodes)
        assertFalse(hero.picked)
        // a card without a title reads as its file's name; progress is position over length
        val cw = home.continueWatching.single()
        assertEquals("fiqh-07.mkv", cw.displayTitle)
        assertEquals(0.25, cw.watchedFraction!!, 1e-9)
        assertEquals("سيرة ابن هشام", home.onDeck.single().series?.name)
        assertTrue(home.favorites.isEmpty())
        assertEquals(listOf("series", "video"), home.recentlyAddedGrouped.map { it.kind })
        assertEquals(3, home.recentlyAddedGrouped[0].count)
        assertEquals(2L, home.recentlyWatched.single().plays)
        assertEquals("Talks", home.pinned.single().library.name)
        assertEquals("A talk", home.pinned.single().recentlyAdded.single().title)
        assertEquals("GET", last().method)
        assertEquals("/api/home", last().url.encodedPath)
        assertEquals("Bearer $token", last().headers["Authorization"])
    }

    @Test
    fun recommendationsAskForTheMobileHomeSurface() = runBlocking {
        val recs = client().recommendations()
        assertEquals("mobile-home", last().url.queryParameter("surface"))
        assertEquals(listOf("for_you", "because_watched"), recs.rails.map { it.reasonKind })
        assertEquals("سيرة ابن هشام ١", recs.rails[1].reasonArgs?.get("title")?.jsonPrimitive?.content)
        assertEquals(1800.0, recs.rails[1].items.single().remainingSec!!, 0.0)
        assertTrue(recs.cacheHit)
    }

    @Test
    fun notInterestedAndHideARailAreDeletes() = runBlocking {
        val c = client()
        c.dismissRecommendation(video)
        assertEquals("DELETE", last().method)
        assertEquals("/api/recommendations/items/$video", last().url.encodedPath)
        c.dismissRail("because_watched")
        assertEquals("DELETE", last().method)
        assertEquals("/api/recommendations/rows/because_watched", last().url.encodedPath)
    }

    // ---------- libraries ----------

    @Test
    fun librariesDecodeTheirCardsAndPins() = runBlocking {
        val libs = client().librarySummaries()
        assertEquals(listOf("دروس", "Talks"), libs.map { it.name })
        assertEquals(120L, libs[0].videos)
        assertNull(libs[0].pinPosition)
        assertEquals(2, libs[0].posterVideoIds.size)
        assertTrue(libs[1].pinned)
        assertEquals(0, libs[1].pinPosition)
        assertNull(libs[1].latestAddedAt)
        val pins = client().pinnedLibraries()
        assertEquals(listOf("aaaaaaaa-0000-4000-8000-000000000002"), pins.libraryIds)
        assertEquals("Talks", pins.items.single().name)
    }

    @Test
    fun pinningSendsTheWholeOrder() = runBlocking {
        val ids = listOf("aaaaaaaa-0000-4000-8000-000000000002", lib)
        client().setPinnedLibraries(ids)
        assertEquals("PUT", last().method)
        assertEquals("/api/me/pinned-libraries", last().url.encodedPath)
        assertEquals(JsonArray(ids.map(::JsonPrimitive)), last().bodyJson()["library_ids"])
        client().setPinnedLibraries(emptyList())
        assertEquals(JsonArray(emptyList()), last().bodyJson()["library_ids"])
    }

    @Test
    fun aLibraryHubDecodesItsRows() = runBlocking {
        val hub = client().libraryHub(lib)
        assertEquals("دروس", hub.library.name)
        assertEquals(120L, hub.library.videos)
        assertEquals(1, hub.rows.continueWatching.size)
        assertEquals(14L, hub.rows.mostWatched.single().plays)
        assertEquals("11111111-0000-4000-8000-000000000001", hub.rows.series.single().posterVideoId)
        assertEquals(listOf(2026, 2025), hub.rows.years.map { it.year })
        assertTrue(hub.rows.onDeck.isEmpty())
        assertEquals("/api/libraries/$lib/hub", last().url.encodedPath)
    }

    @Test
    fun aLibrarysSeriesPagesByOffset() = runBlocking {
        val page = client().librarySeries(lib, SeriesSort.Newest, offset = 60, limit = 30)
        assertEquals("newest", last().url.queryParameter("sort"))
        assertEquals("60", last().url.queryParameter("offset"))
        assertEquals("30", last().url.queryParameter("limit"))
        assertEquals(61, page.total)
        assertEquals(60, page.next)
        val s = page.items.single()
        assertEquals(2L, s.watched)
        assertEquals(1L, s.inProgress)
    }

    @Test
    fun facetsAndTheGridSendTheirQuery() = runBlocking {
        val facets = client().videoFacets(lib)
        assertEquals(lib, last().url.queryParameter("library"))
        assertEquals(listOf("ar", "en"), facets.languages.map { it.code })
        assertEquals("الشيخ أحمد", facets.speakers.single().name)
        assertEquals("1080", facets.resolutions.single().value)

        val page = client().videoPage(VideoQuery(library = lib, sort = VideoSort.Title, language = "ar", speaker = "الشيخ أحمد", cursor = "c1"))
        val url = last().url
        assertEquals("/api/videos", url.encodedPath)
        assertEquals(lib, url.queryParameter("library"))
        assertEquals("title", url.queryParameter("sort"))
        assertEquals("ar", url.queryParameter("language"))
        assertEquals("الشيخ أحمد", url.queryParameter("speaker"))
        assertEquals("c1", url.queryParameter("cursor"))
        assertEquals("60", url.queryParameter("limit"))
        assertEquals("eyJrIjoiMjAyNi0xMC0yMCJ9", page.next)
        assertEquals("سيرة ابن هشام ١", page.items.single().displayTitle)

        // newest first by default; the caller's favorites are read from their own list
        client().videoPage(VideoQuery(library = lib, favorite = true))
        assertEquals("/api/me/videos", last().url.encodedPath)
        assertEquals("created_at", last().url.queryParameter("sort"))
        assertEquals("true", last().url.queryParameter("favorite"))
    }

    // ---------- series ----------

    @Test
    fun seriesDecodeWithProgressAndContinue() = runBlocking {
        val all = client().seriesList()
        assertEquals("/api/series", last().url.encodedPath)
        assertEquals(0.25, all.single().progress, 1e-9)
        assertEquals("0", last().url.queryParameter("page"))
        client().seriesList(lib)
        assertEquals(lib, last().url.queryParameter("library_id"))
        // a short page is the last one: the grid stops asking
        val page = client().seriesPage(2)
        assertEquals("2", last().url.queryParameter("page"))
        assertFalse(page.more)

        val one = client().series(series)
        assertEquals("/art/seerah.jpg", one.posterPath)
        assertEquals(2, one.seasonCount)

        val eps = client().seriesEpisodes(series, EpisodeSort.Episode)
        assertEquals("episode", last().url.queryParameter("sort"))
        assertEquals(listOf(1, 2), eps.seasons.map { it.season })
        assertTrue(eps.seasons[0].episodes[0].watched)
        assertEquals(13, eps.seasons[1].episodes.single().absoluteNumber)
        assertEquals("11111111-0000-4000-8000-000000000003", eps.nextEpisode?.videoId)
        assertEquals(40.0, eps.nextEpisode!!.progressPct, 0.0)
    }

    // ---------- a video ----------

    @Test
    fun aVideosPageDecodesItsStateChaptersAndTranscript() = runBlocking {
        val v = client().videoDetail(video)
        assertEquals("الفقه ٧", v.displayTitle)
        assertEquals("باب الطهارة", v.description)
        assertEquals(600.0, v.resumeAtSec!!, 0.0)
        assertEquals(true, v.favorite)
        assertEquals(listOf("فقه"), v.tags)

        val mine = client().myVideo(video)
        assertTrue(mine.favorite)
        assertEquals("Later", mine.playlists.single().name)
        assertNull(mine.series)

        val ch = client().chapters(video)
        assertEquals(listOf(0.0, 300.0), ch.map { it.startSec })
        assertNull(ch[1].endSec)

        val segs = client().segments(video, fromSec = 0.0, toSec = 120.0)
        assertEquals("120.0", last().url.queryParameter("to"))
        assertNull(last().url.queryParameter("from"))
        assertTrue(segs.partial)
        assertEquals("SPEAKER_00", segs.items[0].speaker)
        assertNull(segs.items[1].speaker)
    }

    @Test
    fun resumeIsOnlyForAStartedUnfinishedVideo() {
        fun detail(state: String) = ApiClient.json.decodeFromString<VideoDetail>("""{"id":"v","filename":"f.mp4"$state}""")
        assertNull(detail("").resumeAtSec)
        assertNull(detail(""","playback_state":{"position_sec":0,"completed":false}""").resumeAtSec)
        assertNull(detail(""","playback_state":{"position_sec":900,"completed":true}""").resumeAtSec)
        assertEquals(12.5, detail(""","playback_state":{"position_sec":12.5,"completed":false}""").resumeAtSec!!, 0.0)
    }

    @Test
    fun marksSendTheVideosAndTheValue() = runBlocking {
        val c = client()
        assertEquals(2, c.markWatched(listOf(video, "v2")))
        assertEquals("POST", last().method)
        assertEquals("/api/me/watched", last().url.encodedPath)
        val body = last().bodyJson()
        assertEquals(listOf(video, "v2"), body["video_ids"]!!.jsonArray.map { it.jsonPrimitive.content })
        assertEquals(JsonPrimitive(true), body["watched"])

        c.markWatched(listOf(video), watched = false)
        assertEquals(JsonPrimitive(false), last().bodyJson()["watched"])

        c.setFavorite(listOf(video), favorite = false)
        assertEquals("/api/me/favorites", last().url.encodedPath)
        assertEquals(JsonPrimitive(false), last().bodyJson()["favorite"])
    }

    // ---------- search ----------

    @Test
    fun searchIsHybridAndSendsArabicUnchanged() = runBlocking {
        val r = client().search("  الطهارة الكبرى ")
        assertEquals("POST", last().method)
        val body = last().bodyJson()
        assertEquals("الطهارة الكبرى", body["q"]!!.jsonPrimitive.content)
        assertEquals("hybrid", body["mode"]!!.jsonPrimitive.content)
        assertNull(body["filters"])
        assertEquals(sid, r.searchId)
        assertTrue(r.degraded)
        assertEquals("<mark>الطهارة</mark>", r.videos.single().titleSnippet)
        assertEquals(1, r.videosTotal)
        val t = r.transcriptVideos.single()
        assertEquals(1200.5, t.passages.single().startSec, 0.0)
        assertEquals(7, t.matches)
        assertEquals(31, r.transcriptTotal)

        client().search("salah", libraryIds = listOf(lib))
        assertEquals(JsonArray(listOf(JsonPrimitive(lib))), last().bodyJson()["filters"]!!.jsonObject["library_id"])
    }

    @Test
    fun searchPagesTitlesAndTranscripts() = runBlocking {
        val titles = client().searchTitles(sid, offset = 24)
        assertEquals("titles", last().url.queryParameter("list"))
        assertEquals("24", last().url.queryParameter("offset"))
        assertEquals("Purity", titles.items.single().title)
        assertNull(titles.next)

        val tr = client().searchTranscripts(sid, offset = 0, limit = 2)
        assertEquals("transcripts", last().url.queryParameter("list"))
        assertEquals("2", last().url.queryParameter("limit"))
        assertEquals(1, tr.next)
        assertEquals("باب <mark>الطهارة</mark>", tr.items.single().passages.single().snippet)
    }

    @Test
    fun suggestionsAndRecentSearches() = runBlocking {
        val c = client()
        assertEquals(listOf("الطهارة", "الطهارة الكبرى"), c.searchSuggestions("الطه"))
        assertEquals("الطه", last().url.queryParameter("q"))
        val before = seen.size
        assertTrue(c.searchSuggestions("  ").isEmpty())
        assertEquals("a blank query asks nothing", before, seen.size)

        val recent = c.recentSearches().single()
        assertTrue(recent.live)
        assertEquals(31, recent.transcriptCount)

        c.forgetRecentSearch(sid)
        assertEquals("DELETE", last().method)
        assertEquals("/api/search/recent/$sid", last().url.encodedPath)
        c.clearRecentSearches()
        assertEquals("DELETE", last().method)
        assertEquals("/api/search/recent", last().url.encodedPath)
    }

    // ---------- lenient ----------

    @Test
    fun unknownFieldsAndMissingRowsDecode() {
        // a newer home's fields, an older home's missing rows, null rows (Go's nil slice)
        val home = ApiClient.json.decodeFromString<HomeRails>(
            """{"continue_watching":null,"recently_added":[{"id":"v1","filename":"a.mp4","hologram":true}],"new_row":[1,2],
               "featured":{"kind":"spotlight","video":{"id":"v1"},"picked":true,"glow":"gold"}}""",
        )
        assertTrue(home.continueWatching.isEmpty())
        assertTrue(home.onDeck.isEmpty())
        assertTrue(home.pinned.isEmpty())
        assertEquals("a.mp4", home.recentlyAdded.single().displayTitle)
        // a kind this app does not know is kept as sent
        assertEquals("spotlight", home.featured?.kind)
        assertNull(home.featured?.series)

        // a card that cannot be read is left out, not the row; a row that is not a list is empty
        val odd = ApiClient.json.decodeFromString<HomeRails>(
            """{"favorites":[{"title":"no id"},{"id":"v2","duration_sec":"long"},{"id":"v3"}],"on_deck":{"id":"v4"}}""",
        )
        assertEquals(listOf("v3"), odd.favorites.map { it.id })
        assertTrue(odd.onDeck.isEmpty())

        val hub = ApiClient.json.decodeFromString<LibraryHub>("""{"library":{"id":"l1"}}""")
        assertTrue(hub.rows.series.isEmpty())
        assertEquals("", hub.library.name)
    }

    @Test
    fun everyFixtureDecodesWithAnUnknownFieldAdded() = runBlocking {
        // the same answers with a field no home sends yet, at the top and inside each row
        answer = { r ->
            val file = routes["${r.method} ${r.url.encodedPath}"]
            file?.let { json(200, withExtra(ApiClient.json.parseToJsonElement(fixture(it))).toString()) }
        }
        val c = client()
        assertEquals(1, c.homeRails().continueWatching.size)
        assertEquals(2, c.librarySummaries().size)
        assertEquals(2, c.libraryHub(lib).rows.years.size)
        assertEquals("الفقه ٧", c.videoDetail(video).displayTitle)
        assertEquals(2, c.seriesEpisodes(series).seasons.size)
        assertEquals(1, c.search("x").transcriptVideos.size)
        assertEquals(2, c.recommendations().rails.size)
    }

    private fun withExtra(e: kotlinx.serialization.json.JsonElement): kotlinx.serialization.json.JsonElement = when (e) {
        is JsonObject -> JsonObject(e.mapValues { withExtra(it.value) } + ("x_future" to JsonPrimitive("?")))
        is JsonArray -> JsonArray(e.map(::withExtra))
        else -> e
    }

    // ---------- errors ----------

    @Test
    fun aNotFoundIsNotAvailable() = runBlocking {
        val c = client()
        for (call in listOf<suspend () -> Unit>(
            { c.videoDetail("99999999-0000-4000-8000-000000000000") },
            { c.libraryHub("99999999-0000-4000-8000-000000000000") },
            { c.series("99999999-0000-4000-8000-000000000000") },
        )) {
            try {
                call()
                fail("a 404 must throw")
            } catch (e: NotAvailable) {
                assertEquals(404, e.status.code)
                assertEquals(ErrorKind.NotFound, e.errorKind())
                assertTrue(e.errorDetails()!!.startsWith("HTTP 404"))
            }
        }
    }

    @Test
    fun anotherFailureStaysAStatus() = runBlocking {
        answer = { json(403, """{"title":"forbidden","status":403}""") }
        try {
            client().homeRails()
            fail("a 403 must throw")
        } catch (e: ApiException.Status) {
            assertEquals(403, e.code)
            assertEquals(ErrorKind.NoAccess, e.errorKind())
        }
    }

    @Test
    fun anUnrefreshable401IsTheSignedOutPath() = runBlocking {
        answer = { r ->
            if (r.url.encodedPath == "/api/auth/refresh") json(401, "{}") else json(401, """{"title":"unauthorized"}""")
        }
        val tokens = MemoryTokens(token, "r")
        try {
            client(tokens).homeRails()
            fail("a 401 must sign out")
        } catch (e: ApiException.SignedOut) {
            assertEquals(ErrorKind.SignedOut, e.errorKind())
        }
        assertNull(tokens.access)
        assertNull(tokens.refresh)
    }
}
