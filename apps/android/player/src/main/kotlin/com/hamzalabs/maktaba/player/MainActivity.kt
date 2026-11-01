package com.hamzalabs.maktaba.player

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.permissions.PermissionsGate
import com.hamzalabs.maktaba.core.permissions.permissionsHelpUrl
import com.hamzalabs.maktaba.core.ui.MaktabaApp
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.SessionGate
import com.hamzalabs.maktaba.player.live.rememberShownSections
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerShell
import com.hamzalabs.maktaba.player.shell.Route
import com.hamzalabs.maktaba.player.ui.components.ImageCache
import com.hamzalabs.maktaba.player.ui.components.LocalHomeImages
import com.hamzalabs.maktaba.player.video.VideoLink

/** Maktaba Player: a home's sections (the shell) and what plays, on a phone, a foldable or a tablet. */
class MainActivity : ComponentActivity() {
    /** A screen a deep link asked for (`maktaba://video/{id}`), until the shell opens it (after sign-in, if need be). */
    private val link = mutableStateOf<Route?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        val app = application as PlayerApplication
        // opened by a home's link (maktaba://connect…): the app asks before it connects
        if (savedInstanceState == null) {
            app.session.openLink(intent?.dataString)
            link.value = VideoLink.route(intent?.dataString)
        }
        setContent {
            val mode by app.themes.mode.collectAsState()
            var playing by remember { mutableStateOf(false) }
            // dark while a video plays, as on the web and the Apple apps
            MaktabaTheme(mode, forceDark = playing) {
                Surface {
                    // first run, and whenever a required permission is missing (checked on resume)
                    PermissionsGate(MaktabaApp.Player, helpUrl = { app.session.current.value?.url?.let(::permissionsHelpUrl) }) {
                        SessionGate(app.session, MaktabaApp.Player) { user, client ->
                            val home = remember(user, client) { PlayerHome(app.session, user, client, app.themes, onPlaying = { playing = it }) }
                            // this home's pictures, with its token, in the app's shared caches (NFR-4)
                            val images = remember(client) { ImageCache.loader(applicationContext, client) }
                            // the sections (a bar, a rail or a drawer, by the window), Live TV only with a channel (FR-5); hidden while a video plays full screen
                            CompositionLocalProvider(LocalPlayerHome provides home, LocalHomeImages provides images) {
                                PlayerShell(sections = rememberShownSections(home), immersive = playing, link = link.value, onLinkOpened = { link.value = null })
                            }
                        }
                    }
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        (application as PlayerApplication).session.openLink(intent.dataString)
        VideoLink.route(intent.dataString)?.let { link.value = it }
    }
}
