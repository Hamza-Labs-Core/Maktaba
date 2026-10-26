package com.hamzalabs.maktaba.tv

import android.app.Application
import com.hamzalabs.maktaba.tv.data.api.ClientHeader

/**
 * Application subclass. Owns the [AppContainer] for the process so any
 * composable can reach the repository through the application context.
 */
class MaktabaTVApp : Application() {
    lateinit var container: AppContainer
        private set

    override fun onCreate() {
        super.onCreate()
        ClientHeader.init(this) // X-Maktaba-Client names this app (Epic 44)
        container = AppContainer(this)
    }
}
