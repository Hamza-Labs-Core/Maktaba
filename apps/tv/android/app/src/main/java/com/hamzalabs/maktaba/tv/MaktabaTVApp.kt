package com.hamzalabs.maktaba.tv

import android.app.Application

/**
 * Application subclass. Owns the [AppContainer] for the process so any
 * composable can reach the repository through the application context.
 */
class MaktabaTVApp : Application() {
    lateinit var container: AppContainer
        private set

    override fun onCreate() {
        super.onCreate()
        container = AppContainer(this)
    }
}
