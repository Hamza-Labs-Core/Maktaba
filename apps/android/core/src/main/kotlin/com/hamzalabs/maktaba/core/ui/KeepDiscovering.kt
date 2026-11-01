package com.hamzalabs.maktaba.core.ui

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.compose.LifecycleStartEffect
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.HomeDiscovery

/**
 * The homes on this network, browsed the whole time the app is in front: started when it
 * comes to the front (afresh each time), stopped when it leaves, and browsed afresh when the
 * device moves to another network. What is heard keeps the saved homes' addresses current
 * ([AppSession.discover]).
 */
@Composable
fun KeepDiscovering(session: AppSession) {
    val context = LocalContext.current.applicationContext
    if (session.discovery == null) session.discovery = { HomeDiscovery(context).homes() }
    LifecycleStartEffect(session) {
        session.discover(true)
        onStopOrDispose { session.discover(false) }
    }
    DisposableEffect(session) {
        val connectivity = context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        val callback = object : ConnectivityManager.NetworkCallback() {
            /** The last network the device was on (null: none yet). */
            private var on: Network? = null

            override fun onAvailable(network: Network) {
                val before = on
                on = network
                // the first is the network the browse started on
                if (before != null && before != network) session.rediscover()
            }
        }
        val listening = runCatching { connectivity.registerDefaultNetworkCallback(callback) }.isSuccess
        onDispose { if (listening) runCatching { connectivity.unregisterNetworkCallback(callback) } }
    }
}
