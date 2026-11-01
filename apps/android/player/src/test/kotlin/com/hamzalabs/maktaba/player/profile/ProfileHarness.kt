package com.hamzalabs.maktaba.player.profile

import android.content.Context
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.test.junit4.ComposeContentTestRule
import androidx.compose.ui.unit.LayoutDirection
import coil3.ImageLoader
import coil3.asImage
import coil3.decode.DataSource
import coil3.intercept.Interceptor
import coil3.request.SuccessResult
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.info
import com.hamzalabs.maktaba.player.shell.AppScreens
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerScreens
import com.hamzalabs.maktaba.player.shell.PlayerSection
import com.hamzalabs.maktaba.player.shell.PlayerShell
import com.hamzalabs.maktaba.player.ui.components.FakePictures
import com.hamzalabs.maktaba.player.ui.components.LocalHomeImages
import kotlinx.coroutines.Dispatchers
import java.net.URI

/** What the Profile's tests watch: the theme's background and the layout's direction as drawn. */
class Drawn {
    @Volatile var background: Color = Color.Unspecified
    @Volatile var direction: LayoutDirection = LayoutDirection.Ltr
}

/**
 * Pictures keyed by the URL's path only: FakeHome's port changes every run, and a picture
 * chosen by the whole URL would make each screenshot differ.
 */
fun pathPictures(context: Context): ImageLoader = ImageLoader.Builder(context)
    .memoryCache(null)
    .diskCache(null)
    .coroutineContext(Dispatchers.Unconfined)
    .interceptorCoroutineContext(Dispatchers.Unconfined)
    .components {
        add(
            Interceptor { chain ->
                val path = runCatching { URI(chain.request.data.toString()).path }.getOrNull() ?: chain.request.data.toString()
                SuccessResult(FakePictures.picture(path).asImage(), chain.request, DataSource.MEMORY)
            },
        )
    }
    .build()

/**
 * The shell as MainActivity draws it, in the home's own theme (so a change of theme shows at
 * once) and the app's language scope, with [languages] and [playback] on test stores.
 */
fun ComposeContentTestRule.setProfileShell(
    home: FakeHome,
    languages: AppLanguages,
    playback: PlaybackSettings,
    drawn: Drawn = Drawn(),
    window: Window = Window.Phone,
    sections: List<PlayerSection> = PlayerSection.entries,
    screens: PlayerScreens = AppScreens,
    images: ImageLoader? = null,
) {
    setContent {
        val mode by home.home.themes.mode.collectAsState()
        MaktabaTheme(mode) {
            CompositionLocalProvider(LocalAppLanguages provides languages, LocalPlaybackSettings provides playback) {
                AppLanguageScope {
                    Probe(drawn)
                    Surface {
                        CompositionLocalProvider(LocalPlayerHome provides home.home, LocalHomeImages provides images) {
                            PlayerShell(sections = sections, adaptiveInfo = window.info(), screens = screens)
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun Probe(drawn: Drawn) {
    val background = MaterialTheme.colorScheme.background
    val direction = LocalLayoutDirection.current
    SideEffect {
        drawn.background = background
        drawn.direction = direction
    }
}
