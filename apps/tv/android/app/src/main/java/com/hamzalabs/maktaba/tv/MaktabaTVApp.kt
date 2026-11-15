package com.hamzalabs.maktaba.tv

import android.app.Application
import com.hamzalabs.maktaba.core.api.ClientHeader
import com.hamzalabs.maktaba.core.api.capabilitiesGate
import com.hamzalabs.maktaba.core.compat.CompatApp
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
        AppLog.install(this, tv = true) // its log lines too, in the shared shape (Epic 53 story 53.01)
        // what crashed since the last start (and, on Android 11+, its ANRs and native crashes) is kept (53.14)
        com.hamzalabs.maktaba.core.diagnostics.CrashReports.collect(this)
        ClientHeader.init(this, tvPlatform = BuildConfig.TV_PLATFORM) // X-Maktaba-Client names this app (Epic 44) and its store build (58.05)
    }

    val session: AppSession by lazy {
        // story 51.11's build switch
        AppSession(
            PreferencesStore(this, "session"), MainScope(), ::tokens,
            capabilitiesGate = capabilitiesGate(BuildConfig.CAPABILITIES_GATE),
            // story 58.01: the compatibility contract, as X-Maktaba-Client names this app
            compatApp = CompatApp.fromClientHeader(ClientHeader.value),
        )
    }

    /** Work that outlives a screen (a stopped video's last position, its session's close). */
    val background = CoroutineScope(SupervisorJob() + Dispatchers.Default + AppLog.handler("tv background"))

    /** Watch a broadcast (story 50.27): the saved broadcasts, sealed on this TV, and the edge's guest API. */
    val guests: com.hamzalabs.maktaba.core.guest.GuestSpace by lazy {
        com.hamzalabs.maktaba.core.guest.GuestSpace(
            com.hamzalabs.maktaba.core.guest.Guests.store(this),
            com.hamzalabs.maktaba.core.guest.Guests.client(this),
        )
    }

    /** Whether Watch a broadcast may open (story 50.27): shut unless no parental limit is known to be on. */
    val guestGate: com.hamzalabs.maktaba.core.guest.GuestGate by lazy { com.hamzalabs.maktaba.core.guest.Guests.gate(this) }

    /** The guest space (from the home picker or Account), over whatever the TV shows; refused under a limit. */
    val guestDoor = com.hamzalabs.maktaba.core.guest.GuestDoor()

    /** A home's token store: the session's own (the same file and key). */
    fun tokens(home: String): TokenStore = keystoreTokens(this, packageName, home)
}
