package com.hamzalabs.maktaba.core.api

import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.internal.tls.OkHostnameVerifier
import java.util.concurrent.ConcurrentHashMap
import javax.net.ssl.HostnameVerifier
import javax.net.ssl.SSLSession

/**
 * The home addresses reached by IP whose certificate is checked against one of the home's own
 * names (Epic 50 story 50.15 §3): a router blocking DNS rebinding refuses the home's LAN name,
 * so the cloud's home list also gives `https://<ip>:<port>` with the name (`tls_name`) the
 * home's certificate must hold there. The chain is checked as usual; only the name differs, so
 * the app still proves it reached that home and no other.
 */
object PinnedNames {
    private val names = ConcurrentHashMap<String, String>()

    private fun key(host: String, port: Int) = host.removeSurrounding("[", "]").lowercase() + "|" + port

    /** Pins [url] (https, an IP address) to [tlsName]; anything else is ignored. */
    fun pin(url: String, tlsName: String) {
        val u = url.toHttpUrlOrNull() ?: return
        val name = tlsName.trim().trimEnd('.').lowercase()
        if (!u.isHttps || name.isEmpty() || !isIpLiteral(u.host)) return
        names[key(u.host, u.port)] = name
    }

    /** The name [host]:[port]'s certificate is checked against, when it is pinned. */
    fun nameFor(host: String, port: Int): String? = names[key(host, port)]

    /** Forgets every pin (tests; a sign-out keeps them: they name no account). */
    fun clear() = names.clear()

    private fun isIpLiteral(host: String): Boolean {
        val h = host.removeSurrounding("[", "]")
        return h.contains(':') || h.split('.').let { p -> p.size == 4 && p.all { it.toIntOrNull() in 0..255 } }
    }
}

/**
 * OkHttp's hostname check, with a pinned address ([PinnedNames]) checked against its home's
 * name instead of its IP. Every client the app builds starts from [ApiClient.defaultHttp], so
 * the API, the player's streams and the images all use it.
 */
object PinnedHostnameVerifier : HostnameVerifier {
    override fun verify(hostname: String, session: SSLSession): Boolean {
        val pinned = session.peerPort.takeIf { it > 0 }?.let { PinnedNames.nameFor(hostname, it) }
        return OkHostnameVerifier.verify(pinned ?: hostname, session)
    }
}
