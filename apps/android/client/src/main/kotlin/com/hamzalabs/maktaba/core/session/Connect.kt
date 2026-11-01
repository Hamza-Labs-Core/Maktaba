package com.hamzalabs.maktaba.core.session

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.ClientHeader
import com.hamzalabs.maktaba.core.api.exchange
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.OkHttpClient
import okhttp3.Request
import java.net.URI
import java.net.URLDecoder
import java.util.concurrent.TimeUnit

/**
 * Connecting to a home by a link (Epic 42 story 42.2): what a home's QR code or a
 * `maktaba://connect` link carries, checking that an address is a Maktaba home, and trying a
 * home's addresses in turn.
 */

/** A home's id as the apps compare it: lower case, no dashes, its first 8 hex (as mDNS sends it). */
fun homeIdKey(id: String): String = id.trim().lowercase().replace("-", "").take(8)

/** Whether two ids (either may be the short mDNS form) name the same home. */
fun sameHome(a: String?, b: String?): Boolean =
    a != null && b != null && a.isNotBlank() && b.isNotBlank() && homeIdKey(a) == homeIdKey(b)

/**
 * A link to a home: `maktaba://connect?id=<id>&n=<name>&u=<base url>[&u=…]` (its addresses,
 * the local one first), or a home's web address scanned or typed as it is.
 */
data class ConnectLink(val id: String?, val name: String?, val urls: List<String>) {
    companion object {
        const val SCHEME = "maktaba"

        /** The link in [text], or null when it is not one (another scheme, no usable address). */
        fun parse(text: String): ConnectLink? {
            val t = text.trim()
            if (t.isEmpty()) return null
            val uri = runCatching { URI(t) }.getOrNull()
            if (uri?.scheme?.lowercase() == SCHEME) {
                // maktaba://connect?… (host "connect"), also maktaba:connect?… as some scanners give it
                val target = (uri.host ?: uri.schemeSpecificPart?.substringBefore('?')?.trim('/'))?.lowercase()
                if (target != "connect") return null
                val params = query(uri.rawQuery ?: uri.schemeSpecificPart?.substringAfter('?', "") ?: "")
                val urls = params.filter { it.first == "u" }.mapNotNull { Home.normalizeWithPath(it.second) }.distinct()
                if (urls.isEmpty()) return null
                return ConnectLink(
                    id = params.firstOrNull { it.first == "id" }?.second?.trim()?.takeIf { it.isNotEmpty() },
                    name = params.firstOrNull { it.first == "n" }?.second?.trim()?.takeIf { it.isNotEmpty() },
                    urls = urls,
                )
            }
            // a home's own address (http/https), as a QR of the web page might hold
            if (uri?.scheme?.lowercase() in setOf("http", "https")) {
                val urls = addressCandidates(t)
                return if (urls.isEmpty()) null else ConnectLink(null, null, urls)
            }
            return null
        }

        /** `a=1&b=2` as pairs, decoded (repeated keys kept, in order). */
        private fun query(raw: String): List<Pair<String, String>> = raw.split('&').filter { it.isNotEmpty() }.map { kv ->
            val k = kv.substringBefore('=')
            val v = kv.substringAfter('=', "")
            decode(k) to decode(v)
        }

        private fun decode(s: String): String = runCatching { URLDecoder.decode(s, Charsets.UTF_8.name()) }.getOrDefault(s)
    }
}

/**
 * The addresses to try for what a person typed (or a scanned web address): as typed, with its
 * path (a home served under a base path), then the same address at its root (a page's path
 * typed by mistake).
 */
fun addressCandidates(text: String): List<String> =
    listOfNotNull(Home.normalizeWithPath(text), Home.normalize(text)).distinct()

/**
 * `GET /api/discovery/home` (public): what a home says about itself before sign-in. An older
 * home (no such route) is recognised by `GET /api/system/version`, and says less.
 */
@Serializable
data class HomeInfo(
    val id: String? = null,
    val name: String? = null,
    val version: String? = null,
    /** Every address the home knows itself by (the local one first). */
    val urls: List<String> = emptyList(),
    /** How a person can sign in here (null: an older home that does not say). */
    @SerialName("sign_in") val signIn: SignInMethods? = null,
)

@Serializable
data class SignInMethods(val password: Boolean = true, val code: Boolean = false)

/** `GET /api/system/version` as far as the apps read it. */
@Serializable
private data class VersionInfo(
    val version: String? = null,
    val role: String? = null,
    @SerialName("home_instance_id") val homeInstanceId: String? = null,
)

/** A home that answered at [url], and what it said. */
data class Reached(val url: String, val info: HomeInfo)

/**
 * Why no address of a home answered as one: [reason] as a person reads it (story 48.37), [why]
 * for the log.
 */
class NotAHome(val tried: List<String>, val why: String?, val reason: FailReason = FailReason.NotAHome) :
    Exception("No Maktaba home answered at ${tried.joinToString()}" + (why?.let { ": $it" } ?: ""))

/**
 * Checks that an address is a Maktaba home. [get] fetches a URL without a token (status and
 * body); by default over OkHttp with this app's `X-Maktaba-Client`, giving up on an address
 * that does not answer within a few seconds (a home's local address seen from elsewhere).
 */
class HomeProbe(private val get: suspend (HttpUrl) -> Pair<Int, String> = ::defaultGet) {
    /** What the home at [url] says about itself; [NotAHome] when it is not one. */
    suspend fun probe(url: String): HomeInfo {
        val base = url.toHttpUrlOrNull() ?: throw NotAHome(listOf(url), "not an address")
        val prefix = base.encodedPath.trimEnd('/')
        fun at(path: String) = base.newBuilder().encodedPath(prefix + path).query(null).build()
        val (code, body) = get(at("/api/discovery/home"))
        if (code == 200) {
            val info = runCatching { ApiClient.json.decodeFromString(HomeInfo.serializer(), body) }.getOrNull()
            // a page that is not the home's JSON (a router, another app's catch-all) is not a home
            if (info != null && (info.id != null || info.name != null || info.version != null)) return info
            throw NotAHome(listOf(url), "unexpected answer")
        }
        if (code != 404 && code != 405) throw NotAHome(listOf(url), "it answered $code")
        // an older home: its version says it is one
        val (vCode, vBody) = get(at("/api/system/version"))
        if (vCode != 200) throw NotAHome(listOf(url), "it answered $vCode")
        val v = runCatching { ApiClient.json.decodeFromString(VersionInfo.serializer(), vBody) }.getOrNull()
        if (v?.version.isNullOrBlank()) throw NotAHome(listOf(url), "unexpected answer")
        if (v?.role == "node") throw NotAHome(listOf(url), "a machine that works for a home, not the home")
        return HomeInfo(id = v?.homeInstanceId?.takeIf { it.isNotBlank() }, version = v?.version)
    }

    /**
     * Tries [urls] in order and keeps the first that answers as a home (a home's local address
     * first, then its others). With [expectId] (a link names its home), an address where
     * another home answers is passed over: a local address reused by another home on this
     * network. [onTry] hears each address as it is tried (the address, its place from 1, how
     * many). [NotAHome] when none does, its reason the most telling of what answered: another
     * home, else something that is not a home, else nothing.
     */
    suspend fun first(
        urls: List<String>,
        expectId: String? = null,
        onTry: (url: String, index: Int, count: Int) -> Unit = { _, _, _ -> },
    ): Reached {
        var why: String? = null
        var reason = FailReason.NoAnswer
        // NoAnswer < NotAHome < OtherHome: their order in FailReason
        fun worse(r: FailReason) {
            if (r.ordinal > reason.ordinal) reason = r
        }
        urls.forEachIndexed { i, u ->
            onTry(u, i + 1, urls.size)
            try {
                val info = probe(u)
                if (expectId != null && info.id != null && !sameHome(expectId, info.id)) {
                    why = "another home answered at $u"
                    worse(FailReason.OtherHome)
                    return@forEachIndexed
                }
                return Reached(u, info)
            } catch (e: CancellationException) {
                throw e
            } catch (e: NotAHome) {
                why = e.why
                worse(e.reason)
            } catch (e: ApiException.Transport) {
                why = e.message
            } catch (e: ApiException) {
                why = e.message
                worse(FailReason.NotAHome)
            }
        }
        throw NotAHome(urls, why, reason)
    }

    companion object {
        private val http: OkHttpClient by lazy {
            ApiClient.defaultHttp.newBuilder()
                .connectTimeout(4, TimeUnit.SECONDS)
                .readTimeout(8, TimeUnit.SECONDS)
                .callTimeout(10, TimeUnit.SECONDS)
                .build()
        }

        suspend fun defaultGet(url: HttpUrl): Pair<Int, String> {
            val r = Request.Builder().url(url)
                .header("Accept", "application/json")
                .header(ClientHeader.NAME, ClientHeader.value)
                .build()
            return http.exchange(r).let { (code, bytes) -> code to String(bytes) }
        }
    }
}
