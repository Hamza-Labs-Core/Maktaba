package com.hamzalabs.maktaba.player.shell

import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.staticCompositionLocalOf
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.ui.ThemeStore
import kotlinx.coroutines.flow.StateFlow

/**
 * What every signed-in screen of the Player reads: the session (sign out, choose another home),
 * who is signed in, the home's client, the theme, and how a screen says a video plays full
 * screen (the shell then hides its navigation and the theme goes dark).
 *
 * [capabilities] are what the home has on (story 51.11, the session's): a feature that is off
 * is hidden from a viewer and explained to an admin.
 */
class PlayerHome(
    val session: AppSession,
    val user: User,
    val client: ApiClient,
    val themes: ThemeStore,
    val onPlaying: (Boolean) -> Unit = {},
    val capabilities: StateFlow<Capabilities> = session.capabilities,
) {
    /** The home's web address (under its base path): the admin's Components link goes there. */
    val webUrl: String get() = client.home.toString()

    private val kept = mutableMapOf<String, Any>()

    /**
     * A screen's state kept for as long as this home is signed in, under [key]: a section's
     * screen leaves composition when another section is shown, and finds its state again here.
     */
    @Suppress("UNCHECKED_CAST")
    fun <T : Any> keep(key: String, create: () -> T): T = kept.getOrPut(key, create) as T
}

/** The signed-in home, provided by [MainActivity][com.hamzalabs.maktaba.player.MainActivity] (or a test's fake home). */
val LocalPlayerHome = staticCompositionLocalOf<PlayerHome> { error("no PlayerHome: provide one around the shell") }

/** What the signed-in home has on now; screens recompose when it changes. */
@Composable
fun PlayerHome.features(): Capabilities = capabilities.collectAsState().value
