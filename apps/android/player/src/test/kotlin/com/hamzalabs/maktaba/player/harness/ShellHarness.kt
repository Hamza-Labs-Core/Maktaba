package com.hamzalabs.maktaba.player.harness

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.adaptive.Posture
import androidx.compose.material3.adaptive.WindowAdaptiveInfo
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.junit4.ComposeContentTestRule
import androidx.compose.ui.unit.dp
import androidx.window.core.layout.WindowSizeClass
import androidx.window.core.layout.computeWindowSizeClass
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.player.shell.AppScreens
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerScreens
import com.hamzalabs.maktaba.player.shell.PlayerSection
import com.hamzalabs.maktaba.player.shell.PlayerShell
import com.hamzalabs.maktaba.player.shell.Route

/** The windows the tests lay the Player out in, in dp, with the Robolectric qualifiers that match. */
enum class Window(val widthDp: Int, val heightDp: Int, val qualifiers: String) {
    /** A phone in portrait (Pixel 9). */
    Phone(411, 891, "w411dp-h891dp-xxhdpi"),
    /** A foldable open flat (Pixel 9 Pro Fold, inner screen). */
    Foldable(673, 841, "w673dp-h841dp-xhdpi"),
    /** A large tablet in landscape. */
    Tablet(1280, 800, "w1280dp-h800dp-xhdpi"),
}

/** Material's adaptive info for a [widthDp] × [heightDp] window, half folded flat on a table or not. */
fun adaptiveInfo(widthDp: Int, heightDp: Int, tabletop: Boolean = false) = WindowAdaptiveInfo(
    WindowSizeClass.BREAKPOINTS_V2.computeWindowSizeClass(widthDp.toFloat(), heightDp.toFloat()),
    Posture(isTabletop = tabletop),
)

fun Window.info(tabletop: Boolean = false) = adaptiveInfo(widthDp, heightDp, tabletop)

/**
 * The shell in the app's dark theme, as MainActivity shows it, with [home] signed in (its
 * screens read it) and the window's adaptive info given rather than measured.
 */
fun ComposeContentTestRule.setShell(
    home: FakeHome? = null,
    sections: List<PlayerSection> = PlayerSection.entries,
    window: Window = Window.Phone,
    tabletop: Boolean = false,
    immersive: Boolean = false,
    screens: PlayerScreens = AppScreens,
) {
    setContent {
        MaktabaTheme(ThemeMode.Dark) {
            Surface {
                if (home != null) {
                    CompositionLocalProvider(LocalPlayerHome provides home.home) {
                        PlayerShell(sections = sections, adaptiveInfo = window.info(tabletop), immersive = immersive, screens = screens)
                    }
                } else {
                    PlayerShell(sections = sections, adaptiveInfo = window.info(tabletop), immersive = immersive, screens = screens)
                }
            }
        }
    }
}

/**
 * Screens for testing the shell alone: every section's root is a list of 60 rows tagged
 * `list` (a row opens a video, `row-<n>`); a video says `video <id>`.
 */
val ListScreens: PlayerScreens = { route, nav ->
    when (route) {
        is Route.Video -> Text("video ${route.id}", Modifier.testTag("video"))
        else -> LazyColumn(Modifier.fillMaxSize().testTag("list")) {
            items((1..60).toList()) { n ->
                Text(
                    "$route row $n",
                    Modifier.testTag("row-$n").clickable { nav.open(Route.Video("v$n")) }.padding(16.dp),
                )
            }
        }
    }
}
