package com.hamzalabs.maktaba.player.shell

import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.core.parental.ParentalState
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.ui.ThemeStore
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch

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
    /** Where the sign-in's last parental limit is remembered between launches (null: nowhere). */
    limitKept: com.hamzalabs.maktaba.core.parental.KeptLimit? = null,
    /** Drops what the device keeps outside this object (Home's rows on disk, pictures). */
    private val dropKept: suspend () -> Unit = {},
) {
    /** The home's web address (under its base path): the admin's Components link goes there. */
    val webUrl: String get() = client.home.toString()

    /**
     * What this sign-in is under parental controls (Epic 57): its limit, and for a restricted
     * account the time the home says is left. The home enforces both; the screens only say so.
     */
    val parental: ParentalState by lazy { ParentalState(client, limitKept).also { it.onChange = ::limitChanged } }

    private val kept = mutableMapOf<String, Any>()

    /** Moves when the kept states were dropped: a screen that reads [keep] then makes its state anew. */
    private var keptEpoch by androidx.compose.runtime.mutableIntStateOf(0)

    /**
     * The sign-in's parental limit changed (story 57.05): every page's kept state, Home's rows
     * on disk, the queue's window and the pictures were read under another limit and go; the
     * screens then read again. What is playing is the player's own, and the home ends it itself.
     */
    private suspend fun limitChanged() {
        kept.keys.removeAll { !it.startsWith(KEEP_ALWAYS) }
        com.hamzalabs.maktaba.player.libraries.PlayerQueues.of(client).forget()
        dropKept()
        keptEpoch++
    }

    /**
     * A screen's state kept for as long as this home is signed in, under [key]: a section's
     * screen leaves composition when another section is shown, and finds its state again here.
     */
    @Suppress("UNCHECKED_CAST")
    fun <T : Any> keep(key: String, create: () -> T): T {
        keptEpoch // read: the screen composes again when the kept states were dropped
        return kept.getOrPut(key, create) as T
    }

    private companion object {
        /** Kept whatever the limit: the scope a stopped video's last calls go out in. */
        const val KEEP_ALWAYS = "watch."
    }
}

/** The signed-in home, provided by [MainActivity][com.hamzalabs.maktaba.player.MainActivity] (or a test's fake home). */
val LocalPlayerHome = staticCompositionLocalOf<PlayerHome> { error("no PlayerHome: provide one around the shell") }

/** What the signed-in home has on now; screens recompose when it changes. */
@Composable
fun PlayerHome.features(): Capabilities = capabilities.collectAsState().value

/**
 * Reads the sign-in's parental limit now and each time the app comes back to the front (Epic
 * 57): a guardian may have changed it meanwhile. The home enforces it either way.
 */
@Composable
fun FollowParental(home: PlayerHome) {
    androidx.lifecycle.compose.LifecycleResumeEffect(home) {
        val job = kotlinx.coroutines.CoroutineScope(kotlinx.coroutines.Dispatchers.Main.immediate).let { scope ->
            scope.launch { home.parental.refresh() }
        }
        onPauseOrDispose { job.cancel() }
    }
}
