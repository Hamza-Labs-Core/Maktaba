package com.hamzalabs.maktaba.player.media

import androidx.annotation.OptIn
import androidx.compose.foundation.layout.Box
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Cast
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import com.hamzalabs.maktaba.player.R
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
import com.google.android.gms.cast.framework.CastContext

/**
 * The receivers the player can cast to (FR-38, story 58.06). Nothing shows without Cast at all
 * ([on]: no receiver application ID, no Google Play services); the button shows while a receiver
 * is on the network (the routes of the receiver the Cast context launches: Maktaba's, or for an
 * older home the Default Media Receiver), looked for while the player is on screen.
 */
interface CastRoutes {
    /** Cast can run on this phone: a receiver application ID, Google Play services, the framework started. */
    @Composable
    fun on(): Boolean

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

/** Tag of the button that says no TV was found, for tests. */
const val CAST_NONE_TAG = "cast-none"

private object DeviceCastRoutes : CastRoutes {
    /** The routes of the receiver the Cast context launches (it follows setReceiverApplicationId). */
    private val selector: MediaRouteSelector
        get() = runCatching { CastContext.getSharedInstance()?.mergedSelector }.getOrNull()
            ?: MediaRouteSelector.Builder().addControlCategory(CastMediaControlIntent.categoryForCast(CAST_APP_ID)).build()

    @Composable
    override fun on(): Boolean {
        val context = LocalContext.current
        return remember(context) { castStarted(context) }
    }

    @Composable
    override fun available(): Boolean {
        val context = LocalContext.current
        if (!on()) return false
        val selector = remember(context) { selector }
        val router = remember(context) { MediaRouter.getInstance(context.applicationContext) }
        var any by remember { mutableStateOf(router.anyReceiver(selector)) }
        DisposableEffect(router) {
            val callback = object : MediaRouter.Callback() {
                override fun onRouteAdded(router: MediaRouter, route: MediaRouter.RouteInfo) { any = router.anyReceiver(selector) }
                override fun onRouteRemoved(router: MediaRouter, route: MediaRouter.RouteInfo) { any = router.anyReceiver(selector) }
                override fun onRouteChanged(router: MediaRouter, route: MediaRouter.RouteInfo) { any = router.anyReceiver(selector) }
                override fun onRouteSelected(router: MediaRouter, route: MediaRouter.RouteInfo, reason: Int) { any = router.anyReceiver(selector) }
                override fun onRouteUnselected(router: MediaRouter, route: MediaRouter.RouteInfo, reason: Int) { any = router.anyReceiver(selector) }
            }
            // looks for receivers only while the player shows (discovery costs battery)
            router.addCallback(selector, callback, MediaRouter.CALLBACK_FLAG_REQUEST_DISCOVERY)
            onDispose { router.removeCallback(callback) }
        }
        return any
    }

    private fun MediaRouter.anyReceiver(selector: MediaRouteSelector): Boolean =
        isRouteAvailable(selector, MediaRouter.AVAILABILITY_FLAG_IGNORE_DEFAULT_ROUTE) ||
            selectedRoute.matchesSelector(selector) && !selectedRoute.isDefault

    @OptIn(UnstableApi::class)
    @Composable
    override fun Button(modifier: Modifier) {
        Box(modifier.testTag(CAST_BUTTON_TAG)) { MediaRouteButton() }
    }
}

/**
 * Cast is on but no TV is on the network (a guest or hotel network blocks the TVs' discovery):
 * a dimmed Cast button that says the TV must be on the same Wi-Fi network.
 */
@Composable
fun NoTvButton(modifier: Modifier = Modifier) {
    var open by remember { mutableStateOf(false) }
    IconButton(onClick = { open = true }, modifier = modifier.testTag(CAST_NONE_TAG)) {
        Icon(Icons.Filled.Cast, stringResource(R.string.cast_button), tint = Color.White.copy(alpha = 0.45f))
    }
    if (open) {
        AlertDialog(
            onDismissRequest = { open = false },
            confirmButton = { TextButton(onClick = { open = false }) { Text(stringResource(R.string.cast_ok)) } },
            title = { Text(stringResource(R.string.cast_button)) },
            text = { Text(stringResource(R.string.cast_no_devices), Modifier.testTag("cast-no-devices")) },
        )
    }
}
