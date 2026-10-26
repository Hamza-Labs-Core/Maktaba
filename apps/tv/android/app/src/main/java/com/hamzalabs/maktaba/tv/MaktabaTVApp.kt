package com.hamzalabs.maktaba.tv

import android.app.Application
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.FoundHome
import com.hamzalabs.maktaba.core.session.HomeDiscovery
import com.hamzalabs.maktaba.core.session.PreferencesStore
import com.hamzalabs.maktaba.core.session.TokenStore
import com.hamzalabs.maktaba.core.session.keystoreTokens
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.catch
import kotlinx.coroutines.flow.stateIn

/**
 * The app's session, for as long as the process lives: the homes it knows, the one in use and
 * who is signed in there (the shared core's [AppSession]), with each home's tokens sealed by a
 * Keystore key ([keystoreTokens]).
 */
class MaktabaTVApp : Application() {
    val session: AppSession by lazy { AppSession(PreferencesStore(this, "session"), MainScope(), ::tokens) }

    /** Work that outlives a screen (a stopped video's last position, its session's close). */
    val background = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    /**
     * The homes announcing themselves on this network (mDNS), from the first time the picker
     * shows until the app ends: "Switch home" lists them at once, and the browse is never torn
     * down mid-resolve (the core's discovery crashed closing on Android 14+).
     */
    val homesFound: StateFlow<List<FoundHome>> by lazy {
        HomeDiscovery(this).homes().catch { }.stateIn(MainScope(), SharingStarted.Lazily, emptyList())
    }

    /** A home's token store: the session's own (the same file and key). */
    fun tokens(home: String): TokenStore = keystoreTokens(this, packageName, home)
}
