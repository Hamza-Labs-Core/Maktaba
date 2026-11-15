package com.hamzalabs.maktaba.tv.ui

import com.hamzalabs.maktaba.core.ui.FollowCapabilities
import com.hamzalabs.maktaba.core.ui.KeepDiscovering
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import com.hamzalabs.maktaba.core.guest.GuestDoor
import com.hamzalabs.maktaba.core.session.AppSession
import kotlinx.coroutines.launch
import com.hamzalabs.maktaba.tv.MaktabaTVApp
import com.hamzalabs.maktaba.tv.ui.screens.CompatScreen
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
    // story 50.27: Watch a broadcast, over everything (a guest needs no home and no account);
    // signed out, the limits kept on this TV decide
    androidx.compose.runtime.LaunchedEffect(phase) { if (phase !is AppSession.Phase.SignedIn) app.guestGate.follow(null) }
    if (TvGuestDoor(app)) return
    // story 58.01: this app and the home are too far apart: the page, instead of signing in
    session.compatBlock.collectAsState().value?.let {
        CompatScreen(it, onTryAgain = session::recheckCompat, onOtherHome = session::switchHome)
        return
    }
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

/**
 * The guest space as [MaktabaTVApp.guestDoor] says, only while no parental limit is known to be
 * on ([MaktabaTVApp.guestGate]): it closes the moment one turns on, and a notice says why. True
 * while the guest space is drawn instead of the rest.
 */
@Composable
fun TvGuestDoor(app: MaktabaTVApp): Boolean {
    val guestAccess by app.guestGate.access.collectAsState(initial = androidx.compose.runtime.remember { app.guestGate.now() })
    val door by app.guestDoor.shown.collectAsState()
    // whenever the answer or what is shown changes: nothing stays open that the answer shuts
    androidx.compose.runtime.LaunchedEffect(guestAccess, door) { app.guestDoor.update(guestAccess) }
    if (door is GuestDoor.Shown.Open) {
        // the signed-in screens are not drawn meanwhile: the sign-in's limit is still read when the TV comes back
        val scope = androidx.compose.runtime.rememberCoroutineScope()
        androidx.lifecycle.compose.LifecycleResumeEffect(Unit) {
            val job = app.guestGate.following?.let { p -> scope.launch { p.refresh() } }
            onPauseOrDispose { job?.cancel() }
        }
        com.hamzalabs.maktaba.tv.ui.screens.TvGuestSpace(app.guests, onClose = app.guestDoor::close)
        return true
    }
    if (door == GuestDoor.Shown.Refused) com.hamzalabs.maktaba.tv.ui.screens.TvGuestNotice("guest.limited", app.guestDoor::close)
    return false
}
