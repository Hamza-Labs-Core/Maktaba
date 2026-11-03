package com.hamzalabs.maktaba.player.live

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.Feature
import com.hamzalabs.maktaba.core.ui.FeatureOffNote
import com.hamzalabs.maktaba.core.ui.Message
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerHome
import com.hamzalabs.maktaba.player.shell.features
import com.hamzalabs.maktaba.player.shell.PlayerSection
import com.hamzalabs.maktaba.core.R as CoreR

/**
 * FR-5: [enabled] with Live TV only when the user can see a channel ([hasChannels] true). Until
 * the home has said (null), and when it answered none, the section is not offered: a section
 * that only ever says "no channels" is no choice.
 *
 * Story 51.11: with Live TV off on the home ([features]), a viewer never sees the section; an
 * admin keeps it, and its screen says Live TV is off with the way to turn it on.
 */
fun liveSections(enabled: List<PlayerSection>, hasChannels: Boolean?, features: Capabilities = Capabilities.AllOn): List<PlayerSection> {
    val shown = when {
        features.explains(Feature.LIVE_TV) -> true
        features.hides(Feature.LIVE_TV) -> false
        else -> hasChannels == true
    }
    return if (shown) enabled else enabled.filter { it != PlayerSection.LiveTv }.ifEmpty { enabled }
}

/**
 * The sections the shell offers for [home]: [enabled], with Live TV hidden while the user can
 * see no channel, or while Live TV is off on the home for a viewer. Asks `GET /api/channels/now`
 * each time the app comes to the front (one request, never a poll in the background, NFR-16);
 * Live TV's own refreshes update it too. Nothing is asked while Live TV is off.
 */
@Composable
fun rememberShownSections(home: PlayerHome, enabled: List<PlayerSection> = PlayerSection.enabled): List<PlayerSection> {
    val live = home.liveTv()
    val features = home.features()
    val liveOn = features.isOn(Feature.LIVE_TV)
    if (PlayerSection.LiveTv in enabled && liveOn) {
        val env = LocalLiveEnv.current
        val lifecycle = LocalLifecycleOwner.current.lifecycle
        LaunchedEffect(live, lifecycle) {
            lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) { live.loadNow(env.nowMs()) }
        }
    }
    return liveSections(enabled, live.hasChannels, features)
}

/**
 * A channel's screen, opened while Live TV is off on the home (a link, a stack kept from before):
 * a viewer reads that Live TV isn't available on this home, an admin why and how to turn it on.
 * Decided when the screen opens: one already open when Live TV goes off is not torn down.
 */
@Composable
fun LiveGate(content: @Composable () -> Unit) {
    val home = LocalPlayerHome.current
    val atOpen = remember { home.capabilities.value }
    when {
        atOpen.explains(Feature.LIVE_TV) -> Box(Modifier.fillMaxSize().safeDrawingPadding(), contentAlignment = Alignment.Center) {
            FeatureOffNote(Feature.LIVE_TV, home.webUrl, centered = true)
        }
        atOpen.hides(Feature.LIVE_TV) -> Message(
            stringResource(CoreR.string.feature_unavailable, stringResource(CoreR.string.feature_live_tv)),
            Modifier.testTag("live-unavailable"),
        )
        else -> content()
    }
}
