package com.hamzalabs.maktaba.player.live

import android.content.Context
import android.os.Build
import androidx.compose.runtime.staticCompositionLocalOf
import java.time.ZoneId

/**
 * What Live TV reads from the world: this device's clock and time zone, how a channel's video
 * plays, and the name a remote session gives this device. The app's is [LiveEnv.System]; a test
 * provides a fixed clock, a fixed zone and a fake engine through [LocalLiveEnv], so its
 * screenshots and its "now" are the same on every run.
 */
class LiveEnv(
    /** This device's time, epoch milliseconds (the home's time follows from each answer's `server_time`). */
    val nowMs: () -> Long = java.lang.System::currentTimeMillis,
    /** The zone the guide's times and days are shown in. */
    val zone: () -> ZoneId = ZoneId::systemDefault,
    /** Makes the player of a channel's HLS ([ExoLiveEngine] in the app). */
    val engine: (Context) -> LiveEngine = ::ExoLiveEngine,
    /** How this device is named in `POST /api/tv/remote/tune` (the home lists the sessions by it). */
    val device: String = "Maktaba Player (${Build.MODEL ?: "Android"})",
    /** How often Now and the guide refresh while shown (NFR-16: at most one request a minute). */
    val refreshMs: Long = REFRESH_MS,
    /** How often the shown progress moves between refreshes. */
    val tickMs: Long = TICK_MS,
) {
    companion object {
        const val REFRESH_MS = 60_000L
        const val TICK_MS = 15_000L
        val System = LiveEnv()
    }
}

val LocalLiveEnv = staticCompositionLocalOf { LiveEnv.System }
