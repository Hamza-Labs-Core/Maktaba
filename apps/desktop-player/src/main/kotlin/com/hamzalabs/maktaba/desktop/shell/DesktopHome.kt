package com.hamzalabs.maktaba.desktop.shell

import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.core.playback.PlaybackSettings
import com.hamzalabs.maktaba.core.session.KeyValueStore
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.PlayRequest
import com.hamzalabs.maktaba.desktop.Posters
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow

/**
 * The Player's own settings, in its settings file: the language (at once, as the Language menu
 * and Profile both set it), start in big screen, and the playback settings (55.09; 55.08 reads them).
 */
class AppPrefs(val store: KeyValueStore) {
    var lang by mutableStateOf(Lang.of(store.get(KEY_LANG)))
        private set

    val playback = PlaybackSettings(store)

    fun chooseLang(l: Lang) {
        lang = l
        store.put(KEY_LANG, l.code)
    }

    companion object {
        const val KEY_LANG = "lang"
        const val KEY_START_BIG = "start_big"
    }
}

/** What a signed-in window offers beside its pages: the Account and Help menus' actions, which Profile shows too. */
class HomeActions(
    val switchHome: () -> Unit = {},
    val signOut: () -> Unit = {},
    val approve: () -> Unit = {},
    val updates: () -> Unit = {},
    /** Opens a page of the home (or any URL) in the system browser. */
    val openUrl: (String) -> Unit = {},
)

/**
 * What every signed-in screen of the desktop Player reads, as the Android `PlayerHome`: who is
 * signed in, the home's client, the navigation, the posters, the settings, a [background] scope
 * for a change that must reach the home after its page is left, and how a page plays a video.
 *
 * [capabilities] are what the home has on (story 51.11, the session's): a feature that is off
 * is hidden from a viewer and explained to an admin (story 55.02).
 */
class DesktopHome(
    val user: User,
    val client: ApiClient,
    /** The home's name as this computer shows it. */
    val homeLabel: String,
    val prefs: AppPrefs,
    val background: CoroutineScope,
    val capabilities: StateFlow<Capabilities> = MutableStateFlow(Capabilities.AllOn),
    val posters: Posters = Posters(client),
    val nav: Navigator = Navigator(),
    val actions: HomeActions = HomeActions(),
    private val onPlay: (PlayRequest) -> Unit = {},
    private val onSignedOut: () -> Unit = {},
) {
    /** The home's web address (under its base path): help and the admin's Components link go there. */
    val webUrl: String get() = client.home.toString().trimEnd('/')

    private val kept = mutableMapOf<String, Any>()

    /** A page's state kept for as long as this home is signed in, under [key]. */
    @Suppress("UNCHECKED_CAST")
    fun <T : Any> keep(key: String, create: () -> T): T = kept.getOrPut(key, create) as T

    /** Plays a video; the window shows the player until it is closed. */
    fun play(request: PlayRequest) = onPlay(request)

    /** A call answered that the sign-in ended: back to signing in. */
    fun signedOut() = onSignedOut()
}

/** The signed-in home, provided around the shell (or by a test). */
val LocalDesktopHome = staticCompositionLocalOf<DesktopHome> { error("no DesktopHome: provide one around the shell") }

/** What the signed-in home has on now; screens recompose when it changes. */
@Composable
fun DesktopHome.features(): Capabilities = capabilities.collectAsState().value
