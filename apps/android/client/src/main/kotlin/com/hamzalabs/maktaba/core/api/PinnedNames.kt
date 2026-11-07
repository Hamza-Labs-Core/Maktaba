package com.hamzalabs.maktaba.core.api

import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.internal.tls.OkHostnameVerifier
import java.net.InetAddress
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

    /**
     * Pins [url] (https, an IP address) to [tlsName], a home's name for that same address
     * (`<encoded ip>.<home id>.<domain>`); anything else is ignored, so a home list can never
     * make an address answer for an unrelated name.
     */
    fun pin(url: String, tlsName: String) {
        val u = url.toHttpUrlOrNull() ?: return
        val name = tlsName.trim().trimEnd('.').lowercase()
        if (!u.isHttps || !isIpLiteral(u.host) || !namesAddress(name, u.host)) return
        names[key(u.host, u.port)] = name
    }

    /** The name [host]:[port]'s certificate is checked against, when it is pinned. */
    fun nameFor(host: String, port: Int): String? = names[key(host, port)]

    /** Forgets every pin (tests; a sign-out keeps them: they name no account). */
    fun clear() = names.clear()

    /**
     * Whether [name] is a home's name for [host]: four labels or more, the first [host] as the
     * cloud encodes it ("192-168-1-20", "fd00--7"; `direct.EncodeIP`).
     */
    private fun namesAddress(name: String, host: String): Boolean {
        val labels = name.split('.')
        if (labels.size < 4 || labels.any { it.isEmpty() }) return false
        val label = labels[0]
        if (label.any { it !in '0'..'9' && it !in 'a'..'f' && it != '-' }) return false
        val v4 = label.count { it == '-' } == 3 && "--" !in label && label.none { it in 'a'..'f' }
        val text = label.replace('-', if (v4) '.' else ':')
        // both are literals (checked), so no lookup happens
        if (!isIpLiteral(text)) return false
        return runCatching { InetAddress.getByName(text) == InetAddress.getByName(host.removeSurrounding("[", "]")) }
            .getOrDefault(false)
    }

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
