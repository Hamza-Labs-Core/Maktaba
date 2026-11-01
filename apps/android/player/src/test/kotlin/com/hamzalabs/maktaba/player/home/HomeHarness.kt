package com.hamzalabs.maktaba.player.home

import android.content.Context
import androidx.activity.ComponentActivity
import coil3.ImageLoader
import coil3.asImage
import coil3.decode.DataSource
import coil3.intercept.Interceptor
import coil3.request.ErrorResult
import coil3.request.ImageResult
import coil3.request.SuccessResult
import kotlinx.coroutines.Dispatchers
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import java.io.IOException
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.MutableIntState
import androidx.compose.runtime.key
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.junit4.AndroidComposeTestRule
import androidx.compose.ui.test.junit4.ComposeContentTestRule
import androidx.compose.ui.test.onRoot
import androidx.test.ext.junit.rules.ActivityScenarioRule
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.info
import com.hamzalabs.maktaba.player.shell.AppScreens
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerScreens
import com.hamzalabs.maktaba.player.shell.PlayerSection
import com.hamzalabs.maktaba.player.shell.PlayerShell
import com.hamzalabs.maktaba.player.shell.Route
import com.hamzalabs.maktaba.player.ui.components.FakePictures
import com.hamzalabs.maktaba.player.ui.components.LocalHomeImages

/**
 * The app's screens, except where Home leads: a pushed screen says which one it is and with what
 * (`opened`: "video v1", "watch v1 at 600", "series s1", "channel c1", "library l1"), so a test
 * reads where a tap went.
 */
val HomeTestScreens: PlayerScreens = { route, nav ->
    val opened = when (route) {
        is Route.Video -> "video ${route.id}"
        is Route.Watch -> "watch ${route.videoId} at ${route.startSec}"
        is Route.Series -> "series ${route.id}"
        is Route.Channel -> "channel ${route.id}"
        is Route.Library -> "library ${route.id}"
        else -> null
    }
    if (opened != null) Text(opened, Modifier.testTag("opened")) else AppScreens(route, nav)
}

/**
 * [FakePictures]' pictures keyed by the URL's path and query only: FakeHome's port changes on
 * every run, and a picture keyed by it would make every screenshot differ.
 */
fun homePictures(context: Context): ImageLoader = ImageLoader.Builder(context)
    .memoryCache(null)
    .diskCache(null)
    .coroutineContext(Dispatchers.Unconfined)
    .interceptorCoroutineContext(Dispatchers.Unconfined)
    .components {
        add(
            Interceptor { chain ->
                val url = chain.request.data.toString()
                val key = url.toHttpUrlOrNull()?.let { it.encodedPath + (it.encodedQuery?.let { q -> "?$q" } ?: "") } ?: url
                val result: ImageResult = if ("missing" in key) {
                    ErrorResult(null, chain.request, IOException("404"))
                } else {
                    SuccessResult(FakePictures.picture(key).asImage(), chain.request, DataSource.MEMORY)
                }
                result
            },
        )
    }
    .build()

/**
 * The shipped shell ([PlayerSection.enabled]: Home first) in the dark theme with [home] signed in
 * and [homePictures] for every picture. Each new value of [launch] is a new launch of the app: a
 * new signed-in home (nothing kept in memory), the same device (Home's disk cache kept).
 */
fun AndroidComposeTestRule<ActivityScenarioRule<ComponentActivity>, ComponentActivity>.setHome(
    home: FakeHome,
    window: Window = Window.Phone,
    launch: MutableIntState? = null,
    screens: PlayerScreens = HomeTestScreens,
) {
    val activity = activity
    setContent {
        key(launch?.intValue ?: 0) {
            val n = launch?.intValue ?: 0
            val signedIn = remember { if (n == 0) home.home else home.home.let { PlayerHome(it.session, it.user, it.client, it.themes) } }
            val images = remember { homePictures(activity) }
            MaktabaTheme(ThemeMode.Dark) {
                Surface(Modifier.fillMaxSize()) {
                    CompositionLocalProvider(LocalPlayerHome provides signedIn, LocalHomeImages provides images) {
                        PlayerShell(sections = PlayerSection.enabled, adaptiveInfo = window.info(), screens = screens)
                    }
                }
            }
        }
    }
}

/**
 * An accessibility audit of what is on screen (NFR-6), on the merged tree TalkBack reads:
 * every control has words (its label or text) and a role, and is at least 48 × 48 dp (NFR-8).
 * The problems, one line each (empty: it passes).
 */
fun ComposeContentTestRule.accessibilityProblems(): List<String> {
    val root = onRoot().fetchSemanticsNode()
    val density = root.layoutInfo.density.density
    val problems = mutableListOf<String>()
    fun visit(node: SemanticsNode) {
        val c = node.config
        val words = (c.getOrNull(SemanticsProperties.ContentDescription).orEmpty() +
            c.getOrNull(SemanticsProperties.Text).orEmpty().map { it.text }).joinToString(" ").trim()
        val tag = c.getOrNull(SemanticsProperties.TestTag) ?: words.take(24)
        if (c.getOrNull(SemanticsActions.OnClick) != null && c.getOrNull(SemanticsProperties.Disabled) == null) {
            if (words.isEmpty()) problems += "$tag: a control without words"
            if (c.getOrNull(SemanticsProperties.Role) == null && c.getOrNull(SemanticsProperties.SelectableGroup) == null) {
                problems += "$tag: a control without a role"
            }
            val w = node.size.width / density
            val h = node.size.height / density
            if (w < 48f - 0.5f || h < 48f - 0.5f) problems += "$tag: ${w.toInt()} × ${h.toInt()} dp, under 48 dp"
        }
        node.children.forEach(::visit)
    }
    visit(root)
    return problems
}
