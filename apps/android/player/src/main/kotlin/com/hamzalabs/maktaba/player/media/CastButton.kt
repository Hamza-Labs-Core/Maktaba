package com.hamzalabs.maktaba.player.media

import androidx.annotation.OptIn
import androidx.compose.foundation.layout.Box
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.media3.cast.MediaRouteButton
import androidx.media3.common.util.UnstableApi
import androidx.mediarouter.media.MediaRouteSelector
import androidx.mediarouter.media.MediaRouter
import com.google.android.gms.cast.CastMediaControlIntent

/**
 * The receivers the player can cast to (FR-38). The button shows only while one is on the
 * network: the Default Media Receiver's routes, looked for while the player is on screen.
 */
interface CastRoutes {
    /** A receiver is on the network, or a cast is on: the button shows. */
    @Composable
    fun available(): Boolean

    /** The Cast button: the route picker, or the cast's controls while one is on. */
    @Composable
    fun Button(modifier: Modifier)
}

/** The device's receivers (Google Play services' Cast); a test gives its own. */
val LocalCastRoutes = staticCompositionLocalOf<CastRoutes> { DeviceCastRoutes }

/** Tag of the Cast button, for tests. */
const val CAST_BUTTON_TAG = "cast-button"

private object DeviceCastRoutes : CastRoutes {
    private val selector: MediaRouteSelector = MediaRouteSelector.Builder()
        .addControlCategory(CastMediaControlIntent.categoryForCast(CastMediaControlIntent.DEFAULT_MEDIA_RECEIVER_APPLICATION_ID))
        .build()

    @Composable
    override fun available(): Boolean {
        val context = LocalContext.current
        val started = remember(context) { castStarted(context) }
        if (!started) return false
        val router = remember(context) { MediaRouter.getInstance(context.applicationContext) }
        var any by remember { mutableStateOf(router.anyReceiver()) }
        DisposableEffect(router) {
            val callback = object : MediaRouter.Callback() {
                override fun onRouteAdded(router: MediaRouter, route: MediaRouter.RouteInfo) { any = router.anyReceiver() }
                override fun onRouteRemoved(router: MediaRouter, route: MediaRouter.RouteInfo) { any = router.anyReceiver() }
                override fun onRouteChanged(router: MediaRouter, route: MediaRouter.RouteInfo) { any = router.anyReceiver() }
                override fun onRouteSelected(router: MediaRouter, route: MediaRouter.RouteInfo, reason: Int) { any = router.anyReceiver() }
                override fun onRouteUnselected(router: MediaRouter, route: MediaRouter.RouteInfo, reason: Int) { any = router.anyReceiver() }
            }
            // looks for receivers only while the player shows (discovery costs battery)
            router.addCallback(selector, callback, MediaRouter.CALLBACK_FLAG_REQUEST_DISCOVERY)
            onDispose { router.removeCallback(callback) }
        }
        return any
    }

    private fun MediaRouter.anyReceiver(): Boolean =
        isRouteAvailable(selector, MediaRouter.AVAILABILITY_FLAG_IGNORE_DEFAULT_ROUTE) ||
            selectedRoute.matchesSelector(selector) && !selectedRoute.isDefault

    @OptIn(UnstableApi::class)
    @Composable
    override fun Button(modifier: Modifier) {
        Box(modifier.testTag(CAST_BUTTON_TAG)) { MediaRouteButton() }
    }
}
