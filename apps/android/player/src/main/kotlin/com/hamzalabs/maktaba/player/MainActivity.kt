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
import com.hamzalabs.maktaba.core.ui.ApproveRequests
import com.hamzalabs.maktaba.core.ui.HomeStatusHost
import com.hamzalabs.maktaba.core.ui.PendingSignInsHost
import com.hamzalabs.maktaba.core.ui.MaktabaApp
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.SessionGate
import com.hamzalabs.maktaba.player.live.rememberShownSections
import com.hamzalabs.maktaba.player.profile.AppLanguageScope
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
            // a tapped sign-in request notification: Approve opens
            ApproveRequests.handle(intent)
        }
        setContent {
            val mode by app.themes.mode.collectAsState()
            var playing by remember { mutableStateOf(false) }
            // dark while a video plays, as on the web and the Apple apps
            MaktabaTheme(mode, forceDark = playing) {
                // before Android 13, the in-app language (48.31); 13+ uses per-app locales
                AppLanguageScope { Surface {
                    // first run, and whenever a required permission is missing (checked on resume)
                    PermissionsGate(MaktabaApp.Player, helpUrl = { app.session.current.value?.url?.let(::permissionsHelpUrl) }) {
                        SessionGate(app.session, MaktabaApp.Player) { user, client ->
                            val home = remember(user, client) {
                                PlayerHome(
                                    app.session, user, client, app.themes, onPlaying = { playing = it },
                                    // Epic 57 story 57.05: the limit as last known, and what goes when it changes
                                    limitKept = com.hamzalabs.maktaba.core.parental.KeptLimit.InFile.of(filesDir, client.home, user.id),
                                    dropKept = { dropKeptFor(applicationContext, client.home, user.id) },
                                )
                            }
                            // this home's pictures, with its token, in the app's shared caches (NFR-4)
                            val images = remember(client) { ImageCache.loader(applicationContext, client) }
                            // the sections (a bar, a rail or the collapsible rail, by the window and its fold), Live TV only with a channel (FR-5); hidden while a video plays full screen
                            CompositionLocalProvider(LocalPlayerHome provides home, LocalHomeImages provides images) {
                                // the sign-in's parental limit, read at sign-in and whenever the app comes back (Epic 57)
                                com.hamzalabs.maktaba.player.shell.FollowParental(home)
                                // a TV waiting to sign in: a banner on top (not over a playing video), and a notification
                                PendingSignInsHost(app.session, user, client, enabled = !playing) {
                                    // what is down on the home, on top (Epic 53 story 53.06; not over a playing video)
                                    HomeStatusHost(client, admin = user.isAdmin, enabled = !playing) {
                                        // crash reports to the home (asked once, off until yes) and Report a problem (Epic 53)
                                        com.hamzalabs.maktaba.core.ui.DiagnosticsHost(client, app.session.current.collectAsState().value) {
                                            PlayerShell(sections = rememberShownSections(home), immersive = playing, link = link.value, onLinkOpened = { link.value = null })
                                        }
                                    }
                                }
                            }
                        }
                    }
                } }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        (application as PlayerApplication).session.openLink(intent.dataString)
        VideoLink.route(intent.dataString)?.let { link.value = it }
        ApproveRequests.handle(intent)
    }
}

/**
 * What the device keeps for [userId] on [home] outside the screens' own states (Epic 57 story
 * 57.05): Home's rows on disk and every picture, in memory and on disk. Dropped when the
 * sign-in's parental limit changes, so nothing read under the limit before is shown.
 */
suspend fun dropKeptFor(context: android.content.Context, home: okhttp3.HttpUrl, userId: String) {
    kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
        com.hamzalabs.maktaba.core.browse.HomeCache.of(context.cacheDir, home, userId).clear()
        ImageCache.clear(context)
    }
    com.hamzalabs.maktaba.core.ui.forgetPosters()
}
