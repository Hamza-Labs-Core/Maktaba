package com.hamzalabs.maktaba.tv.data.api

import android.content.Context

/**
 * `X-Maktaba-Client` (Epic 44 story 44.2): every request says which app sent it,
 * here `tv/<version> (androidtv; tv)`. The home reads it as a label (analytics, its use
 * per app), never for rights; it also asks sign-in for the tokens in the body.
 */
object ClientHeader {
    const val NAME = "X-Maktaba-Client"

    @Volatile
    var value: String = make(null)
        private set

    /** Takes the app's version from its package (once, at start). */
    fun init(context: Context) {
        val version = runCatching {
            context.packageManager.getPackageInfo(context.packageName, 0).versionName
        }.getOrNull()
        value = make(version)
    }

    fun make(version: String?): String =
        "tv/${version?.takeIf { it.isNotBlank() } ?: "dev"} (androidtv; tv)"
}
