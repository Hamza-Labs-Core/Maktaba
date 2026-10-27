package com.hamzalabs.maktaba.core.api

import android.content.Context

/** Works out this app's [ClientHeader] from its package (once, at start). */
fun ClientHeader.init(context: Context) {
    val version = runCatching {
        context.packageManager.getPackageInfo(context.packageName, 0).versionName
    }.getOrNull()
    use(make(context.packageName, version))
}
