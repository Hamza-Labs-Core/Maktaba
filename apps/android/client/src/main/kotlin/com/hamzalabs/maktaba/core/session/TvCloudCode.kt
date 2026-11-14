package com.hamzalabs.maktaba.core.session

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.ClientHeader
import com.hamzalabs.maktaba.core.api.exchange
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody

// Epic 58 story 58.03, the TV side on Android TV and Fire TV (58.05): a TV that has no home
// yet asks Maktaba Cloud for a code (POST /v1/tv/codes, no sign-in), shows it with a QR of the
// approve page, and polls (POST /v1/tv/codes/poll {code, poll_secret}) until a person signed
// in to a home approves it, in the Player (Approve a TV or device) or on maktaba.tv/tv. The
// approval names the home, its connections (story 50.13's order) and a grant: the home's own
// single-use pairing secret, which the TV redeems at the home (AppSession.signInWithTvApproval).
// The poll secret and the grant stay in memory: never stored, never in a URL, never logged
// (their toString leaves them out).

/** POST /v1/tv/codes's answer. */
@Serializable
data class TvCode(
    /** As shown: `ABCD-2345`. */
    val code: String,
    @SerialName("poll_secret") val pollSecret: String,
    @SerialName("expires_in") val expiresIn: Int = 600,
    val interval: Int = 5,
    /** The approve page the QR opens (`https://maktaba.tv/tv?c=ABCD2345`). */
    @SerialName("verify_url") val verifyUrl: String = "",
) {
    override fun toString() = "TvCode($code)"
}

/** An approved code: the home, how to reach it, and the grant it redeems once. */
@Serializable
data class TvApproval(
    /** The home's id in Maktaba Cloud (its server id: not the id the home itself answers with). */
    @SerialName("home_id") val homeId: String,
    @SerialName("home_name") val homeName: String = "",
    val connections: List<CloudConnection> = emptyList(),
    val grant: String,
) {
    override fun toString() = "TvApproval($homeId, ${connections.size} connections)"

    /** Its connections in the order apps try, without a kind this build does not know. */
    val ordered: List<CloudConnection> get() = CloudConnection.ordered(connections)

    /** Its addresses in that order, each once. */
    val addresses: List<String> get() = ordered.mapNotNull { Home.normalizeWithPath(it.url) }.distinct()

    /**
     * The addresses the grant may go to, in the cloud's order: https by name (its relay, its
     * direct name, its LAN names under its certificate) and a raw LAN address pinned to one of
     * its names (`local_ip`, checked by [com.hamzalabs.maktaba.core.api.PinnedNames]), where
     * the certificate check proves the home. The cloud names the home by its own server id, not
     * the id the home answers with, so the home is known by these addresses alone. A plain http
     * address or an unpinned raw IP proves nothing: anyone on that network or on the path can
     * answer there and say anything about itself (the cloud keeps the home's plain public
     * address for older apps, an address can change hands, a private one is another machine on
     * another network), so the grant never goes to one.
     */
    val grantAddresses: List<String>
        get() = ordered.filter { c ->
            val u = c.url.toHttpUrlOrNull() ?: return@filter false
            u.isHttps && (!isIpHost(u.host) || (c.kind == "local_ip" && !c.tlsName.isNullOrBlank()))
        }.mapNotNull { Home.normalizeWithPath(it.url) }.distinct()

    /** A grant must have the home's pairing secret's shape, or it is no approval. */
    val wellFormed: Boolean get() = homeId.isNotBlank() && GRANT.matches(grant)
}

private val GRANT = Regex("^[A-Za-z0-9_-]{16,128}$")

private val IPV4 = Regex("""\d{1,3}(\.\d{1,3}){3}""")

/** Whether [host] (as OkHttp gives it) is a raw IP address rather than a name. */
private fun isIpHost(host: String) = host.contains(':') || IPV4.matches(host)

/** One poll's answer. */
sealed interface TvPoll {
    data object Pending : TvPoll

    /** Polled faster than the cloud's interval: five seconds more. */
    data object SlowDown : TvPoll

    /** The code ran out, was used, or the cloud does not know it: a new code. */
    data object Gone : TvPoll

    data class Approved(val approval: TvApproval) : TvPoll

    /** No answer, or one the TV cannot read: polled again at the interval. */
    data object Failed : TvPoll
}

/** The cloud's TV code routes. [base] is the cloud's API (`https://api.maktaba.tv`). */
class TvCodes(
    private val base: HttpUrl,
    private val http: OkHttpClient = CloudSession.defaultHttp,
) {
    /** A new code for this TV ([platform] `androidtv` or `firetv`); null when the cloud gives none. */
    suspend fun create(platform: String, appVersion: String): TvCode? {
        val body = buildJsonObject { put("platform", platform); put("app_version", appVersion) }
        val (code, text) = post("/v1/tv/codes", body.toString()) ?: return null
        if (code != 200 && code != 201) return null
        return runCatching { ApiClient.json.decodeFromString(TvCode.serializer(), text) }.getOrNull()
            ?.takeIf { it.code.isNotBlank() && it.pollSecret.isNotBlank() }
    }

    /** Asks whether [code] was approved; the poll secret goes in the body only. */
    suspend fun poll(code: TvCode): TvPoll {
        val body = buildJsonObject { put("code", code.code); put("poll_secret", code.pollSecret) }
        val (status, text) = post("/v1/tv/codes/poll", body.toString()) ?: return TvPoll.Failed
        return when (status) {
            200 -> runCatching { ApiClient.json.decodeFromString(TvApproval.serializer(), text) }.getOrNull()
                ?.takeIf { it.wellFormed }?.let { TvPoll.Approved(it) } ?: TvPoll.Failed
            202 -> TvPoll.Pending
            429 -> TvPoll.SlowDown
            404, 410 -> TvPoll.Gone
            else -> TvPoll.Failed
        }
    }

    private suspend fun post(path: String, body: String): Pair<Int, String>? {
        val url = base.newBuilder().encodedPath(base.encodedPath.trimEnd('/') + path).build()
        val r = Request.Builder().url(url)
            .header("Accept", "application/json")
            .header(ClientHeader.NAME, ClientHeader.value)
            .post(body.toRequestBody(ApiClient.JSON))
            .build()
        return try {
            http.exchange(r).let { it.code to String(it.body) }
        } catch (e: CancellationException) {
            throw e
        } catch (_: ApiException.Transport) {
            null
        }
    }
}

/** What the code screen shows while it waits. */
sealed interface TvCodeState {
    data object Asking : TvCodeState

    /** [code] on screen; it runs out at [expiresAtMs] on this TV's clock. */
    data class Showing(val code: TvCode, val expiresAtMs: Long) : TvCodeState

    /** The cloud gave no code: the other ways stay, and it is asked again in a while. */
    data object Down : TvCodeState
}

/**
 * Shows a code from [codes] and waits until it is approved: polls at the cloud's interval
 * (five seconds more on slow_down), asks for a new code when one is gone or runs out, and asks
 * again after [RETRY_DOWN_MS] when the cloud gives none. Returns the approval; cancel to stop.
 */
suspend fun waitForTvApproval(
    codes: TvCodes,
    platform: String,
    appVersion: String,
    onState: (TvCodeState) -> Unit,
    sleep: suspend (Long) -> Unit = { delay(it) },
    now: () -> Long = System::currentTimeMillis,
): TvApproval {
    while (true) {
        onState(TvCodeState.Asking)
        val code = codes.create(platform, appVersion)
        if (code == null) {
            onState(TvCodeState.Down)
            sleep(RETRY_DOWN_MS)
            continue
        }
        val until = now() + code.expiresIn.coerceIn(30, 3600) * 1000L
        onState(TvCodeState.Showing(code, until))
        var every = code.interval.coerceIn(1, 60) * 1000L
        while (now() < until) {
            sleep(every)
            when (val p = codes.poll(code)) {
                is TvPoll.Approved -> return p.approval
                TvPoll.SlowDown -> every += 5_000L
                TvPoll.Gone -> break
                TvPoll.Pending, TvPoll.Failed -> {}
            }
        }
    }
}

/** How long the code screen waits before asking a cloud that gave no code again. */
const val RETRY_DOWN_MS = 30_000L
