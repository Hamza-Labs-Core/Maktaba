package com.hamzalabs.maktaba.tv.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.tv.MaktabaTVApp
import com.hamzalabs.maktaba.tv.ui.screens.HomePickerScreen
import com.hamzalabs.maktaba.tv.ui.screens.SignInScreen

/** What the TV shows follows the session: choose a home, sign in there, then the library. */
@Composable
fun Root(app: MaktabaTVApp) {
    val session = app.session
    val phase by session.phase.collectAsState()
    val home by session.current.collectAsState()
    when (val p = phase) {
        AppSession.Phase.ChoosingHome -> HomePickerScreen(session)
        AppSession.Phase.SigningIn -> home?.let { SignInScreen(app, it) } ?: HomePickerScreen(session)
        is AppSession.Phase.SignedIn -> SignedInApp(app, p.user, p.client)
    }
}
