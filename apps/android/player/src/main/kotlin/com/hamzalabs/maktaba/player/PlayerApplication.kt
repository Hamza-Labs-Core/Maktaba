package com.hamzalabs.maktaba.player

import android.app.Application
import com.hamzalabs.maktaba.core.api.ClientHeader
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.PreferencesStore
import com.hamzalabs.maktaba.core.session.keystoreTokens
import com.hamzalabs.maktaba.core.ui.ThemeStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.SupervisorJob

/** The app's session and theme: one each, for as long as the process lives. */
class PlayerApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        ClientHeader.init(this) // X-Maktaba-Client names this app (Epic 44)
    }

    val session: AppSession by lazy {
        AppSession(PreferencesStore(this, "session"), MainScope(), { home -> keystoreTokens(this, packageName, home) })
    }
    /** Work that outlives a screen (closing a stream session). */
    val background = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    val themes: ThemeStore by lazy { ThemeStore(PreferencesStore(this, "settings")) }
}
