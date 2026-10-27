package com.hamzalabs.maktaba.core.session

import android.content.Context
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.os.Build
import androidx.annotation.RequiresApi
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import java.net.Inet4Address
import java.net.Inet6Address
import java.net.InetAddress
import java.util.concurrent.Executors

/** A home that announced itself on this network. */
data class FoundHome(
    /** The machine's name as it announces it (the home's own name comes after signing in). */
    val name: String,
    /** The web port's address, under the web's base path when it is not `/`: what the apps talk to (`/api` and `/stream/`). */
    val url: String,
    /**
     * The home's instance id: `home=` (a full UUID, on every machine of a cluster), else `id=`
     * (an older home, where it is the home's own). Self-reported: never a reason to reuse a sign-in.
     */
    val id: String?,
    val version: String?,
)

/** The DNS-SD service type a Maktaba machine registers (`cmd/maktaba-server/internal/mdns`). */
const val MAKTABA_SERVICE = "_maktaba._tcp"

/**
 * What a `_maktaba._tcp` announcement means to an app: a home (`role=home`, or no role from an
 * older home) at its web port (`web=`, else the service's port), over https when it says
 * `tls=1`, under its web base path (`path=`, when not `/`), named by `name=` (else the
 * instance name without "Maktaba (…)"). A machine that works for a home (`role=node`) is not
 * a home to connect to: null.
 */
fun foundHome(instance: String, host: InetAddress?, port: Int, txt: Map<String, String>): FoundHome? {
    val role = txt["role"]
    if (role != null && role != "home") return null
    if (host == null) return null
    val webPort = txt["web"]?.toIntOrNull()?.takeIf { it in 1..65535 } ?: port
    if (webPort !in 1..65535) return null
    val address = when (host) {
        is Inet6Address -> "[${host.hostAddress?.substringBefore('%')}]"
        else -> host.hostAddress ?: return null
    }
    val name = txt["name"]?.takeIf { it.isNotBlank() }
        ?: Regex("""^Maktaba \((.*)\)$""").find(instance)?.groupValues?.get(1)
        ?: instance
    val scheme = if (txt["tls"]?.trim()?.lowercase() in setOf("1", "true", "yes")) "https" else "http"
    val id = (txt["home"] ?: txt["id"])?.takeIf { it.isNotBlank() }
    return FoundHome(name, "$scheme://$address:$webPort${basePath(txt["path"])}", id, txt["version"])
}

/** A web base path as a URL's tail: "" for none or `/`, else like `/maktaba` (no trailing slash). */
fun basePath(path: String?): String {
    val p = path?.trim()?.trim('/') ?: return ""
    if (p.isEmpty() || p.any { it.isWhitespace() || it == '?' || it == '#' || it == '\\' }) return ""
    return "/$p"
}

/**
 * One row per home: the same home announced twice (two interfaces, IPv4 and IPv6, a rename)
 * is one, by its id, else by its host and port. The first announcement stays.
 */
fun dedupeHomes(homes: List<FoundHome>): List<FoundHome> = homes.distinctBy { h ->
    h.id?.let { "id:" + homeIdKey(it) } ?: h.url.toHttpUrlOrNull()?.let { "at:${it.host}:${it.port}" } ?: h.url
}

/**
 * The homes announcing themselves on this network, as they come and go (Android's NSD: mDNS /
 * DNS-SD). Needs the local network permission on Android 17 and later (`LocalNetwork`).
 */
class HomeDiscovery(context: Context) {
    private val nsd = context.getSystemService(Context.NSD_SERVICE) as NsdManager

    fun homes(): Flow<List<FoundHome>> = callbackFlow {
        val found = linkedMapOf<String, FoundHome>()
        /** Services announced and not lost: a resolve that ends after its service left is dropped. */
        val present = mutableSetOf<String>()
        val lock = Any()
        fun publish() = trySend(synchronized(lock) { dedupeHomes(found.values.toList()) })
        val resolver = Resolver(nsd) { instance, home ->
            synchronized(lock) { if (home != null && instance in present) found[instance] = home else found.remove(instance) }
            publish()
        }
        val listener = object : NsdManager.DiscoveryListener {
            override fun onDiscoveryStarted(serviceType: String) { publish() }
            override fun onDiscoveryStopped(serviceType: String) {}
            override fun onStartDiscoveryFailed(serviceType: String, errorCode: Int) { close() }
            override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) {}
            override fun onServiceFound(info: NsdServiceInfo) {
                synchronized(lock) { present += info.serviceName }
                resolver.resolve(info)
            }
            override fun onServiceLost(info: NsdServiceInfo) {
                resolver.lost(info)
                synchronized(lock) {
                    present -= info.serviceName
                    found.remove(info.serviceName)
                }
                publish()
            }
        }
        nsd.discoverServices(MAKTABA_SERVICE, NsdManager.PROTOCOL_DNS_SD, listener)
        awaitClose {
            runCatching { nsd.stopServiceDiscovery(listener) }
            resolver.close()
        }
    }

    /**
     * Resolves found services to addresses and TXT records: with a service-info callback that
     * follows each one (Android 14 and later), else one `resolveService` at a time (older
     * Androids refuse a second while one runs).
     */
    private class Resolver(private val nsd: NsdManager, private val done: (String, FoundHome?) -> Unit) {
        private val callbacks = mutableMapOf<String, Any>()
        private val queue = ArrayDeque<NsdServiceInfo>()
        private var resolving = false

        fun resolve(info: NsdServiceInfo) {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) follow(info) else enqueue(info)
        }

        fun lost(info: NsdServiceInfo) {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) unfollow(info.serviceName)
        }

        fun close() {
            // the executor stays: NsdManager still posts each unregistration to it
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) synchronized(callbacks) { callbacks.keys.toList() }.forEach(::unfollow)
        }

        @RequiresApi(Build.VERSION_CODES.UPSIDE_DOWN_CAKE)
        private fun follow(info: NsdServiceInfo) {
            val cb = object : NsdManager.ServiceInfoCallback {
                override fun onServiceInfoCallbackRegistrationFailed(errorCode: Int) {
                    synchronized(callbacks) { callbacks.remove(info.serviceName) }
                }
                override fun onServiceUpdated(s: NsdServiceInfo) {
                    val host = s.hostAddresses.firstOrNull { it is Inet4Address } ?: s.hostAddresses.firstOrNull()
                    done(info.serviceName, foundHome(s.serviceName, host, s.port, txt(s)))
                }
                override fun onServiceLost() = done(info.serviceName, null)
                override fun onServiceInfoCallbackUnregistered() {}
            }
            synchronized(callbacks) {
                if (info.serviceName in callbacks) return
                callbacks[info.serviceName] = cb
            }
            runCatching { nsd.registerServiceInfoCallback(info, executor, cb) }
                .onFailure { synchronized(callbacks) { callbacks.remove(info.serviceName) } }
        }

        @RequiresApi(Build.VERSION_CODES.UPSIDE_DOWN_CAKE)
        private fun unfollow(name: String) {
            val cb = synchronized(callbacks) { callbacks.remove(name) } as? NsdManager.ServiceInfoCallback ?: return
            runCatching { nsd.unregisterServiceInfoCallback(cb) }
        }

        private fun enqueue(info: NsdServiceInfo) {
            synchronized(queue) {
                queue.addLast(info)
                if (resolving) return
                resolving = true
            }
            next()
        }

        @Suppress("DEPRECATION")
        private fun next() {
            val info = synchronized(queue) {
                queue.removeFirstOrNull() ?: run { resolving = false; return }
            }
            nsd.resolveService(info, object : NsdManager.ResolveListener {
                override fun onResolveFailed(s: NsdServiceInfo, errorCode: Int) = next()
                override fun onServiceResolved(s: NsdServiceInfo) {
                    done(info.serviceName, foundHome(s.serviceName, s.host, s.port, txt(s)))
                    next()
                }
            })
        }

        companion object {
            /**
             * Where NsdManager calls the service-info callbacks: one thread for the app, never
             * shut down (it delivers `onServiceInfoCallbackUnregistered` after a search ends;
             * a shut-down executor would refuse it and crash the app).
             */
            private val executor = Executors.newSingleThreadExecutor { r -> Thread(r, "maktaba-nsd").apply { isDaemon = true } }
        }

        private fun txt(s: NsdServiceInfo): Map<String, String> =
            s.attributes.mapValues { (_, v) -> v?.let { String(it) } ?: "" }
    }
}
