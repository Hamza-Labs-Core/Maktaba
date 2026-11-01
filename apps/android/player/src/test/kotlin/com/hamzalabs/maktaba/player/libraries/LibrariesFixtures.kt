package com.hamzalabs.maktaba.player.libraries

import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.ComposeContentTestRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.swipeUp
import androidx.compose.ui.unit.LayoutDirection
import com.hamzalabs.maktaba.core.layout.Panes
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.FakeHome.Companion.json
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.info
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerSection
import com.hamzalabs.maktaba.player.shell.PlayerShell
import com.hamzalabs.maktaba.player.ui.components.FakePictures
import com.hamzalabs.maktaba.player.ui.components.LocalHomeImages
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import android.content.Context
import coil3.ImageLoader
import coil3.asImage
import coil3.decode.DataSource
import coil3.intercept.Interceptor
import coil3.request.ImageResult
import coil3.request.SuccessResult
import kotlinx.coroutines.Dispatchers
import mockwebserver3.RecordedRequest
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/**
 * The Libraries section's routes on the fake home (NFR-19): two libraries, a hub, facets, five
 * videos paged two at a time by cursor, one series of two seasons; Pin and Mark watched answer
 * as the home does. Every request is kept by [FakeHome.requests].
 */
fun FakeHome.answerLibraries() {
    answer("/api/libraries/summary") { json(SUMMARY) }
    answer("/api/me/pinned-libraries") { r ->
        val ids = Json.parseToJsonElement(r.bodyText()).jsonObject["library_ids"]!!.jsonArray.map { it.jsonPrimitive.content }
        json("""{"library_ids":[${ids.joinToString(",") { "\"$it\"" }}],"items":[]}""")
    }
    answer("/api/libraries/l1/hub") { json(HUB) }
    answer("/api/libraries/l2/hub") { json("""{"library":{"id":"l2","name":"Lessons"},"rows":{}}""") }
    answer("/api/videos/facets") { json(FACETS) }
    answer("/api/videos") { r ->
        when {
            r.url.queryParameter("library") == "l9" -> json("""{"error":"not found"}""", code = 404)
            else -> json(page(r.url.queryParameter("cursor")))
        }
    }
    answer("/api/libraries/l1/series") { json("""{"items":[{"id":"s1","name":"Omar","poster_video_id":"v2","episodes":4,"watched":1}],"total":1,"next":null}""") }
    answer("/api/libraries/l2/series") { json("""{"items":[],"total":0,"next":null}""") }
    answer("/api/series") { json("""{"items":[$SERIES]}""") }
    answer("/api/series/s1") { json(SERIES) }
    answer("/api/series/s1/episodes") { json(EPISODES) }
    answer("/api/me/watched") { r ->
        val n = Json.parseToJsonElement(r.bodyText()).jsonObject["video_ids"]!!.jsonArray.size
        json("""{"updated":$n}""")
    }
}

/** The ids a write sent (`library_ids` or `video_ids`), and the request's flag. */
fun RecordedRequest.ids(key: String): List<String> =
    Json.parseToJsonElement(bodyText()).jsonObject[key]!!.jsonArray.map { it.jsonPrimitive.content }

fun RecordedRequest.bodyText(): String = body?.utf8() ?: ""

const val SUMMARY = """{"items":[
 {"id":"l1","name":"Films","videos":5,"poster_video_ids":["v1"],"pinned":false},
 {"id":"l2","name":"Lessons","videos":3,"poster_video_ids":["v4"],"pinned":false}]}"""

const val HUB = """{"library":{"id":"l1","name":"Films","videos":5},"rows":{
 "on_deck":[],
 "continue_watching":[{"id":"v2","title":"Omar, Episode 1","duration_sec":3000,"position_sec":1200,"series":{"id":"s1","name":"Omar"}}],
 "recently_added":[{"id":"v1","title":"The Message","duration_sec":5400},{"id":"v3","title":"Lion of the Desert","duration_sec":5400}],
 "recently_watched":[]}}"""

const val FACETS = """{"languages":[{"code":"ar","videos":3},{"code":"en","videos":2}],"speakers":[{"name":"Mustafa Akkad","videos":2}]}"""

/** The library's videos, two a page: no cursor → v1, v2; c2 → v3, v4; c3 → v5 (the end). */
fun page(cursor: String?): String {
    val all = FakeHome.VIDEO_LIST
    val (from, next) = when (cursor) {
        null -> 0 to "c2"
        "c2" -> 2 to "c3"
        else -> 4 to null
    }
    val items = all.drop(from).take(2).joinToString(",") { (id, t) -> FakeHome.video(id, t) }
    return """{"items":[$items],"next":${next?.let { "\"$it\"" } ?: "null"}}"""
}

const val SERIES = """{"id":"s1","name":"Omar","poster_video_id":"v2","library_id":"l1","year":2012,"numbering":"season","season_count":2,"episode_count":4,"watched_count":1,"in_progress":1}"""

const val EPISODES = """{"seasons":[
 {"season":1,"episodes":[
   {"video_id":"v2","season":1,"episode":1,"title":"Omar, Episode 1","duration_sec":3000,"progress_pct":100,"watched":true},
   {"video_id":"v3","season":1,"episode":2,"title":"Lion of the Desert","duration_sec":3000,"progress_pct":40,"watched":false}]},
 {"season":2,"episodes":[
   {"video_id":"v4","season":2,"episode":1,"title":"The Night Journey","duration_sec":3000,"progress_pct":0,"watched":false},
   {"video_id":"v5","season":2,"episode":2,"title":"Andalusia, a History","duration_sec":3000,"progress_pct":0,"watched":false}]}],
 "numbering":"season","next_episode":{"video_id":"v3","season":1,"episode":2,"title":"Lion of the Desert","progress_pct":40,"watched":false}}"""

/**
 * The shipped shell (Libraries only) in the dark theme, signed in to [home], laid out for
 * [window]; [panes] stands in for a fold the JVM cannot report, and posters are drawn by
 * [FakePictures] by path ([portlessPictures]) so screenshots are the same on every run and machine.
 */
fun ComposeContentTestRule.setLibraries(
    home: FakeHome,
    window: Window = Window.Phone,
    panes: Panes? = null,
    rtl: Boolean? = null,
) {
    setContent {
        val context = LocalContext.current
        val images = remember { portlessPictures(context) }
        val direction = when (rtl) {
            true -> LayoutDirection.Rtl
            false -> LayoutDirection.Ltr
            null -> LocalLayoutDirection.current
        }
        CompositionLocalProvider(
            LocalPlayerHome provides home.home,
            LocalHomeImages provides images,
            LocalLibrariesPanes provides panes,
            LocalLayoutDirection provides direction,
        ) {
            MaktabaTheme(ThemeMode.Dark) {
                Surface { PlayerShell(sections = PlayerSection.enabled, adaptiveInfo = window.info()) }
            }
        }
    }
}

/**
 * [FakePictures]' pictures keyed by the URL's path and query only: FakeHome listens on a new port
 * each run, and a picture keyed by the whole URL would change colour with it.
 */
fun portlessPictures(context: Context): ImageLoader = ImageLoader.Builder(context)
    .memoryCache(null)
    .diskCache(null)
    .coroutineContext(Dispatchers.Unconfined)
    .interceptorCoroutineContext(Dispatchers.Unconfined)
    .components {
        add(
            Interceptor { chain ->
                val url = chain.request.data.toString()
                val key = url.toHttpUrlOrNull()?.let { it.encodedPath + "?" + it.encodedQuery.orEmpty() } ?: url
                val result: ImageResult = SuccessResult(FakePictures.picture(key).asImage(), chain.request, DataSource.MEMORY)
                result
            },
        )
    }
    .build()

/** Swipes [tag]'s list up until a node tagged [target] is composed (a lazy grid composes what shows). */
fun ComposeContentTestRule.scrollUntil(tag: String, target: String, swipes: Int = 15) {
    repeat(swipes) {
        waitForIdle()
        if (onAllNodes(hasTestTag(target)).fetchSemanticsNodes().isNotEmpty()) return
        onNodeWithTag(tag).performTouchInput { swipeUp() }
    }
    waitForIdle()
}
