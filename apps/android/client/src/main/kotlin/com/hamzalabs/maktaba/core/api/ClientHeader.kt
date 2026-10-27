package com.hamzalabs.maktaba.core.api

/**
 * `X-Maktaba-Client` (Epic 44 story 44.2): every request says which app sent it,
 * `<app>/<version> (<platform>; <device>)`, e.g. `player/1.3.0 (android; phone)`. The home
 * reads it as a label (analytics, its use per app), never for rights; any app but the web's
 * still signs in for tokens, as `native` did. Until the app sets it ([use]; on Android
 * `ClientHeader.init(context)`, from its package) it is `native`.
 */
object ClientHeader {
    const val NAME = "X-Maktaba-Client"

    @Volatile
    var value: String = "native"
        private set

    /** This app's value (once, at start). */
    fun use(value: String) {
        this.value = value
    }

    /**
     * The value for a package: the app from its id (`…tv` the Android TV app, `…remote…` the
     * Remote, else the Player), the device from its flavour's suffix (`.tablet`, `.foldable`,
     * else a phone).
     */
    fun make(packageName: String, version: String?): String {
        val parts = packageName.lowercase().split('.')
        val v = version?.takeIf { it.isNotBlank() } ?: "dev"
        if (parts.lastOrNull() == "tv") return "tv/$v (androidtv; tv)"
        val app = if ("remote" in parts) "remote" else "player"
        val device = when (parts.lastOrNull()) {
            "tablet" -> "tablet"
            "foldable" -> "foldable"
            else -> "phone"
        }
        return "$app/$v (android; $device)"
    }
}
