package com.hamzalabs.maktaba.player.video

import com.hamzalabs.maktaba.player.shell.Route
import java.net.URI
import java.net.URLDecoder

/**
 * A video's deep link (FR-9): `maktaba://video/{id}` opens that video's page. The manifest's
 * intent filter sends the link to MainActivity, which hands the route to the shell; the page then
 * asks the home, so a video the person may not see says "not available" (the home answers 404).
 */
object VideoLink {
    const val SCHEME = "maktaba"
    const val HOST = "video"

    /** The link for [id]. */
    fun of(id: String): String = "$SCHEME://$HOST/" + java.net.URLEncoder.encode(id, Charsets.UTF_8.name()).replace("+", "%20")

    /** The page [data] (an `Intent.dataString`) opens, or null when it is not a video's link. */
    fun route(data: String?): Route.Video? {
        val text = data?.trim()?.takeIf { it.isNotEmpty() } ?: return null
        val uri = runCatching { URI(text) }.getOrNull() ?: return null
        if (!uri.scheme.equals(SCHEME, ignoreCase = true) || !uri.host.equals(HOST, ignoreCase = true)) return null
        val segments = uri.rawPath.orEmpty().split('/').filter { it.isNotEmpty() }
        val raw = segments.singleOrNull() ?: return null
        val id = runCatching { URLDecoder.decode(raw.replace("+", "%2B"), Charsets.UTF_8.name()) }.getOrNull()?.trim()
        return id?.takeIf { it.isNotEmpty() }?.let { Route.Video(it) }
    }
}
