package com.hamzalabs.maktaba.tv.data

import android.content.Context

/**
 * Non-secret app preferences (server URL, language) in plain
 * SharedPreferences. Tokens live in [com.hamzalabs.maktaba.tv.data.api.TokenStore]
 * instead, which is encrypted.
 */
class SettingsStore(context: Context) {
    private val prefs = context.getSharedPreferences("maktaba_settings", Context.MODE_PRIVATE)

    var serverUrl: String
        get() = prefs.getString(KEY_SERVER, DEFAULT_SERVER) ?: DEFAULT_SERVER
        set(value) = prefs.edit().putString(KEY_SERVER, value).apply()

    /** BCP-47 tag; "ar" mirrors the UI to RTL (Maktaba is Arabic-first). */
    var language: String
        get() = prefs.getString(KEY_LANG, "en") ?: "en"
        set(value) = prefs.edit().putString(KEY_LANG, value).apply()

    private companion object {
        const val KEY_SERVER = "server_url"
        const val KEY_LANG = "language"
        const val DEFAULT_SERVER = "https://demo.maktaba.app"
    }
}
