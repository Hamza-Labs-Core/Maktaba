package com.hamzalabs.maktaba.player.shell

import androidx.compose.runtime.staticCompositionLocalOf
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.ui.ThemeStore

/**
 * What every signed-in screen of the Player reads: the session (sign out, choose another home),
 * who is signed in, the home's client, the theme, and how a screen says a video plays full
 * screen (the shell then hides its navigation and the theme goes dark).
 */
class PlayerHome(
    val session: AppSession,
    val user: User,
    val client: ApiClient,
    val themes: ThemeStore,
    val onPlaying: (Boolean) -> Unit = {},
) {
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
