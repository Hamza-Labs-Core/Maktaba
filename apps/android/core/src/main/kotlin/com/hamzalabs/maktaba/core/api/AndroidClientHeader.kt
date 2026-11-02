package com.hamzalabs.maktaba.core.api

import android.content.Context
import android.content.pm.PackageManager
import android.os.Build

/**
 * Works out this app's [ClientHeader] (once, at start): the app from its package, the device
 * from the screen ([ClientHeader.deviceFor]: a hinge sensor, else the smallest width).
 */
fun ClientHeader.init(context: Context) {
    val version = runCatching {
        context.packageManager.getPackageInfo(context.packageName, 0).versionName
    }.getOrNull()
    val hinge = Build.VERSION.SDK_INT >= Build.VERSION_CODES.R &&
        context.packageManager.hasSystemFeature(PackageManager.FEATURE_SENSOR_HINGE_ANGLE)
    val device = deviceFor(context.resources.configuration.smallestScreenWidthDp, hinge)
    use(make(context.packageName, version, device))
}
