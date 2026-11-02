package com.hamzalabs.maktaba.tv

import android.app.Application
import com.hamzalabs.maktaba.core.api.ClientHeader
import com.hamzalabs.maktaba.core.api.init
import com.hamzalabs.maktaba.core.diagnostics.AppLog
import com.hamzalabs.maktaba.core.diagnostics.CrashLog
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.PreferencesStore
import com.hamzalabs.maktaba.core.session.TokenStore
import com.hamzalabs.maktaba.core.session.keystoreTokens
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.SupervisorJob

/**
 * The app's session, for as long as the process lives: the homes it knows, the one in use and
 * who is signed in there (the shared core's [AppSession]), with each home's tokens sealed by a
 * Keystore key ([keystoreTokens]).
 */
class MaktabaTVApp : Application() {
    override fun onCreate() {
        super.onCreate()
        CrashLog.install(this) // an uncaught exception is kept on the device, without anything personal
        ClientHeader.init(this) // X-Maktaba-Client names this app (Epic 44)
    }

    val session: AppSession by lazy { AppSession(PreferencesStore(this, "session"), MainScope(), ::tokens) }

    /** Work that outlives a screen (a stopped video's last position, its session's close). */
    val background = CoroutineScope(SupervisorJob() + Dispatchers.Default + AppLog.handler("tv background"))

    /** A home's token store: the session's own (the same file and key). */
    fun tokens(home: String): TokenStore = keystoreTokens(this, packageName, home)
}
