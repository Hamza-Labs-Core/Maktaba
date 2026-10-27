package com.hamzalabs.maktaba.core.session

import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import java.net.Inet6Address
import java.net.InetAddress

// What a `_maktaba._tcp` announcement means, whoever heard it: Android's NSD
// (core's HomeDiscovery) or the desktop player's mDNS browser.

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
