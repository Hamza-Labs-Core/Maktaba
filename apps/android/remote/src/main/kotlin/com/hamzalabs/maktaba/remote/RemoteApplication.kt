package com.hamzalabs.maktaba.remote

import android.app.Application
import com.hamzalabs.maktaba.core.api.ClientHeader
import com.hamzalabs.maktaba.core.diagnostics.CrashLog
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.PreferencesStore
import com.hamzalabs.maktaba.core.session.keystoreTokens
import com.hamzalabs.maktaba.core.ui.ThemeStore
import kotlinx.coroutines.MainScope

/** The app's session and theme: one each, for as long as the process lives. */
class RemoteApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        CrashLog.install(this) // an uncaught exception is kept on the device, without anything personal
        ClientHeader.init(this) // X-Maktaba-Client names this app (Epic 44)
    }

    val session: AppSession by lazy {
        AppSession(PreferencesStore(this, "session"), MainScope(), { home -> keystoreTokens(this, packageName, home) })
    }
    val themes: ThemeStore by lazy { ThemeStore(PreferencesStore(this, "settings")) }
}
