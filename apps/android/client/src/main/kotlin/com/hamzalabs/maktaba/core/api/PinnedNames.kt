package com.hamzalabs.maktaba.core.api

import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.internal.tls.OkHostnameVerifier
import java.net.InetAddress
import java.security.MessageDigest
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
    // each home's pins (its server id: address|port to name); a list replaces its home's own
    private val homes = ConcurrentHashMap<String, Map<String, String>>()

    private fun key(host: String, port: Int) = host.removeSurrounding("[", "]").lowercase() + "|" + port

    /**
     * The id a home's names carry (`<encoded ip>.<home id>.<domain>`), from its server id as the
     * cloud derives it (`direct.HomeID`): the first 32 hex digits of SHA-256("direct-v1" + id).
     */
    fun homeId(serverId: String): String =
        MessageDigest.getInstance("SHA-256").digest(("direct-v1$serverId").toByteArray())
            .joinToString("") { "%02x".format(it) }.take(32)

    /**
     * Pins the `local_ip` [entries] (url to tls_name) of the home [serverId], replacing that
     * home's earlier pins. An entry is kept only when its url is https to an IP address and its
     * name is this home's name for that same address; anything else is ignored, so a home list
     * can never make an address answer for an unrelated name or for another home.
     */
    fun pinHome(serverId: String, entries: List<Pair<String, String>>) {
        if (serverId.isBlank()) return
        val hid = homeId(serverId)
        val kept = mutableMapOf<String, String>()
        for ((url, tlsName) in entries) {
            val u = url.toHttpUrlOrNull() ?: continue
            val name = tlsName.trim().trimEnd('.').lowercase()
            if (!u.isHttps || !isIpLiteral(u.host) || !namesAddress(name, u.host, hid)) continue
            kept[key(u.host, u.port)] = name
        }
        if (kept.isEmpty()) homes.remove(serverId) else homes[serverId] = kept
    }

    /**
     * The name [host]:[port]'s certificate is checked against, when one home alone pinned it.
     * Two homes on the same private address (each on its own LAN) leave it unpinned: checked by
     * IP, so refused, and the app goes on to the home's next connection; a certificate of one
     * home is never taken for the other.
     */
    fun nameFor(host: String, port: Int): String? {
        val k = key(host, port)
        return homes.values.mapNotNull { it[k] }.distinct().singleOrNull()
    }

    /** Forgets every pin (tests; a sign-out keeps them: they name no account). */
    fun clear() = homes.clear()

    /**
     * Whether [name] is the home [hid]'s name for [host]: four labels or more, the first [host]
     * as the cloud encodes it ("192-168-1-20", "fd00--7"; `direct.EncodeIP`), the second [hid].
     */
    private fun namesAddress(name: String, host: String, hid: String): Boolean {
        val labels = name.split('.')
        if (labels.size < 4 || labels.any { it.isEmpty() } || labels[1] != hid) return false
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
