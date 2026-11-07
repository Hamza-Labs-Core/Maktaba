package com.hamzalabs.maktaba.tv.ui

import com.hamzalabs.maktaba.core.ui.FollowCapabilities
import com.hamzalabs.maktaba.core.ui.KeepDiscovering
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.tv.MaktabaTVApp
import com.hamzalabs.maktaba.tv.ui.screens.ConnectLinkDialog
import com.hamzalabs.maktaba.tv.ui.screens.HomePickerScreen
import com.hamzalabs.maktaba.tv.ui.screens.SignInScreen

/**
 * What the TV shows follows the session: choose a home, sign in there, then the library; a
 * home's link the app was opened with is confirmed on top of it.
 */
@Composable
fun Root(app: MaktabaTVApp) {
    val session = app.session
    KeepDiscovering(session)
    val phase by session.phase.collectAsState()
    val home by session.current.collectAsState()
    val link by session.link.collectAsState()
    when (val p = phase) {
        AppSession.Phase.ChoosingHome -> HomePickerScreen(app)
        AppSession.Phase.SigningIn -> home?.let { SignInScreen(app, it) } ?: HomePickerScreen(app)
        is AppSession.Phase.SignedIn -> {
            // what the home has on, asked again each time the TV app comes to the front (51.11)
            FollowCapabilities(session)
            // crash reports to the home (asked once, off until yes) and Report a problem (Epic 53)
            com.hamzalabs.maktaba.core.ui.DiagnosticsHost(p.client, app.session.current.collectAsState().value, tv = true) {
                SignedInApp(app, p.user, p.client)
            }
        }
    }
    // a maktaba://connect link the app was opened with: asked about first, wherever the TV is
    link?.let { ConnectLinkDialog(session, it) }
}
