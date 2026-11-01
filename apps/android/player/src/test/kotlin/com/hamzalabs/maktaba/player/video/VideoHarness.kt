package com.hamzalabs.maktaba.player.video

import android.content.Context
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.junit4.ComposeContentTestRule
import coil3.ImageLoader
import coil3.asImage
import coil3.decode.DataSource
import coil3.intercept.Interceptor
import coil3.request.ErrorResult
import coil3.request.ImageResult
import coil3.request.SuccessResult
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.FakeHome.Companion.json
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.info
import com.hamzalabs.maktaba.player.shell.AppScreens
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerScreens
import com.hamzalabs.maktaba.player.shell.PlayerSection
import com.hamzalabs.maktaba.player.shell.PlayerShell
import com.hamzalabs.maktaba.player.shell.Route
import com.hamzalabs.maktaba.player.ui.components.FakePictures
import com.hamzalabs.maktaba.player.ui.components.LocalHomeImages
import kotlinx.coroutines.Dispatchers
import java.io.IOException
import java.util.Collections

/**
 * The video page's fixtures on the fake home (NFR-19), as `GET /api/videos/{id}` and its
 * neighbours answer them, with Favorite and Mark watched kept by the fake home so a reload reads
 * what was saved:
 * - `v1` "The Night Journey", episode 2 of the series "Omar", resumed at 12:34 of 45:00, three
 *   chapters, a transcript; the series' episodes 3 and 4 follow it.
 * - `v2` "Andalusia, a History": no series, never started (Play).
 * - `vportrait`: a portrait video (its poster is 9:16); `vnoposter`: no poster (404).
 * - anything else: 404, as a video the person may not see.
 */
class VideoHome(private val home: FakeHome) {
    @Volatile var favorite = false
    @Volatile var watched = false

    init {
        home.answer("/api/videos/v1") { json(detail("v1", "The Night Journey", "A night of travel, told slowly.", 2700, if (watched) 0.0 else 754.0, watched)) }
        home.answer("/api/videos/v2") { json(detail("v2", "Andalusia, a History", "Seven centuries in an hour.", 3600, null, false)) }
        home.answer("/api/videos/vportrait") { json(detail("vportrait", "A Short in Portrait", "Filmed on a phone.", 95, null, false)) }
        home.answer("/api/videos/vnoposter") { json(detail("vnoposter", "Lesson Without a Poster", "Still being prepared.", 600, null, false)) }
        home.answer("/api/me/videos/v1") {
            json("""{"video_id":"v1","position_sec":${if (watched) 0 else 754},"completed":$watched,"favorite":$favorite,"playlists":[],"series":{"id":"s1","name":"Omar"}}""")
        }
        home.answer("/api/videos/v1/chapters") {
            json("""{"items":[{"id":1,"seq":1,"start_sec":0,"title":"Opening"},{"id":3,"seq":3,"start_sec":1500,"title":"The return"},{"id":2,"seq":2,"start_sec":600,"title":"The road north"}]}""")
        }
        home.answer("/api/videos/v1/segments") {
            json("""{"items":[{"id":1,"seq":1,"start_sec":2,"end_sec":6,"text":"It was a night like no other."},{"id":2,"seq":2,"start_sec":6,"end_sec":11,"text":"The caravan left before dawn."}],"partial":true}""")
        }
        home.answer("/api/series/s1/episodes") {
            json(
                """{"numbering":"season","sort":"episode","seasons":[{"season":1,"episodes":[""" +
                    """{"video_id":"e1","season":1,"episode":1,"title":"The Call","watched":true},""" +
                    """{"video_id":"v1","season":1,"episode":2,"title":"The Night Journey","progress_pct":28},""" +
                    """{"video_id":"e3","season":1,"episode":3,"title":"The Migration"},""" +
                    """{"video_id":"e4","season":1,"episode":4,"title":"The Battle of Badr"}]}]}""",
            )
        }
        home.answer("/api/me/favorites") { r ->
            favorite = r.body?.utf8()?.contains("\"favorite\":true") == true
            json("""{"updated":1}""")
        }
        home.answer("/api/me/watched") { r ->
            watched = r.body?.utf8()?.contains("\"watched\":true") == true
            json("""{"updated":1}""")
        }
    }

    companion object {
        fun detail(id: String, title: String, description: String, duration: Int, position: Double?, completed: Boolean): String {
            val state = position?.let { ""","playback_state":{"position_sec":$it,"completed":$completed}""" } ?: ""
            return """{"id":"$id","library_id":"l1","filename":"$id.mkv","title":"$title","description":"$description",""" +
                """"duration_sec":$duration,"published_at":"2024-03-11T09:00:00Z","state":"ready"$state}"""
        }
    }
}

/**
 * Posters without a network: [FakePictures]' picture for each URL, portrait (9:16) for
 * `vportrait`, a 404 for `vnoposter`. [urls] keeps every URL asked for.
 */
class VideoPictures(context: Context) {
    val urls: MutableList<String> = Collections.synchronizedList(mutableListOf())

    val loader: ImageLoader = ImageLoader.Builder(context)
        .memoryCache(null)
        .diskCache(null)
        .coroutineContext(Dispatchers.Unconfined)
        .interceptorCoroutineContext(Dispatchers.Unconfined)
        .components {
            add(
                Interceptor { chain ->
                    val url = chain.request.data.toString()
                    urls += url
                    // the picture by its path: the fake home's port changes every run, the colours must not
                    val key = url.substringAfter("/api/")
                    val result: ImageResult = when {
                        "vnoposter" in url -> ErrorResult(null, chain.request, IOException("404"))
                        "vportrait" in url -> SuccessResult(FakePictures.picture(key, 360, 640).asImage(), chain.request, DataSource.MEMORY)
                        else -> SuccessResult(FakePictures.picture(key).asImage(), chain.request, DataSource.MEMORY)
                    }
                    result
                },
            )
        }
        .build()
}

/** The player's screen in tests: says what it was asked to play, `watch <id> <start>`. */
val WatchStub: PlayerScreens = { route, nav ->
    when (route) {
        is Route.Watch -> Text("watch ${route.videoId} ${route.startSec}", Modifier.testTag("watch"))
        Route.Home -> Text("home", Modifier.testTag("home-root"))
        else -> AppScreens(route, nav)
    }
}

/** Opens a link from a test: what MainActivity does with a `maktaba://video/{id}` intent. */
class LinkDriver {
    var link by mutableStateOf<Route?>(null)
}

/**
 * The shell in the app's dark theme with [home] signed in and [pictures] as its image loader,
 * opened on Home (a blank root here) and then on [link]'s screen, as a deep link opens it.
 */
fun ComposeContentTestRule.setVideoShell(
    home: FakeHome,
    pictures: VideoPictures,
    link: String? = null,
    window: Window = Window.Phone,
    driver: LinkDriver = LinkDriver(),
) {
    driver.link = VideoLink.route(link)
    setContent {
        val d = remember { driver }
        MaktabaTheme(ThemeMode.Dark) {
            Surface(Modifier.fillMaxSize()) {
                CompositionLocalProvider(LocalPlayerHome provides home.home, LocalHomeImages provides pictures.loader) {
                    PlayerShell(
                        sections = listOf(PlayerSection.Home),
                        adaptiveInfo = window.info(),
                        screens = WatchStub,
                        link = d.link,
                        onLinkOpened = { d.link = null },
                    )
                }
            }
        }
    }
}
