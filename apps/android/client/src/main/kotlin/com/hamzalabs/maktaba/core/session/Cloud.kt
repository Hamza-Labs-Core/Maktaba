package com.hamzalabs.maktaba.core.session

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ClientHeader
import com.hamzalabs.maktaba.core.api.exchange
import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.net.URLEncoder
import java.util.concurrent.TimeUnit

/** One way to reach a home (story 50.13): `local`, `direct`, `relay_hint` or `relay`. */
@Serializable
data class CloudConnection(val url: String, val kind: String) {
    companion object {
        /** The order apps try (story 50.13 §2): local, direct, relay hint, relay. */
        val ORDER = listOf("local", "direct", "relay_hint", "relay")

        /** [cs] in that order; a kind this build doesn't know is left out. */
        fun ordered(cs: List<CloudConnection>): List<CloudConnection> =
            cs.filter { it.kind in ORDER }.sortedBy { ORDER.indexOf(it.kind) }
    }
}

/** A home this cloud account reaches (`GET /v1/homes`). */
@Serializable
data class CloudHome(
    @SerialName("server_id") val serverId: String,
    val name: String = "",
    val slug: String = "",
    /** The home through the cloud's relay: it answers from anywhere. */
    @SerialName("relay_url") val relayUrl: String = "",
    /** The home's own address, when it reported one. */
    @SerialName("direct_url") val directUrl: String? = null,
    val online: Boolean = false,
    /** This cloud account is linked to a user of that home: it can sign in there. */
    val linked: Boolean = false,
    /** This cloud account owns the home's registration (no account in the home by that). */
    val owned: Boolean = false,
    /** Every way to reach it (story 50.13); empty from a cloud older than the list. */
    val connections: List<CloudConnection> = emptyList(),
) {
    /** Its connections in the order apps try, or from the older fields. */
    val ordered: List<CloudConnection>
        get() = if (connections.isNotEmpty()) {
            CloudConnection.ordered(connections)
        } else {
            listOfNotNull(
                directUrl?.takeIf { it.isNotBlank() }?.let { CloudConnection(it, "direct") },
                relayUrl.takeIf { it.isNotBlank() }?.let { CloudConnection(it, "relay") },
            )
        }

    /** Its addresses in the order apps try: local, its own, the relay hint, the relay. */
    val addresses: List<String>
        get() = ordered.mapNotNull { Home.normalizeWithPath(it.url) }.distinct()
}

/**
 * The cloud's home list as last answered, kept per account (story 50.13): names and addresses,
 * never a token. Shown while the cloud can't be reached; 30 days, then marked old; removed at
 * sign-out.
 */
@Serializable
data class SavedCloudHomes(val user: CloudUser, @SerialName("saved_at") val savedAtMs: Long, val homes: List<CloudHome>) {
    fun isOld(nowMs: Long = System.currentTimeMillis()): Boolean = nowMs - savedAtMs > OLD_AFTER_MS

    companion object {
        const val OLD_AFTER_MS = 30L * 24 * 3600 * 1000
        private const val PREFIX = "cloud_homes:"
        private const val INDEX = "cloud_homes_index"

        private fun key(email: String) = PREFIX + email.trim().lowercase()

        fun save(store: KeyValueStore, user: CloudUser, homes: List<CloudHome>, nowMs: Long = System.currentTimeMillis()) {
            store.put(key(user.email), ApiClient.json.encodeToString(serializer(), SavedCloudHomes(user, nowMs, homes)))
            val index = store.get(INDEX)?.split('\n')?.filter { it.isNotBlank() }.orEmpty()
            if (key(user.email) !in index) store.put(INDEX, (index + key(user.email)).joinToString("\n"))
        }

        fun load(store: KeyValueStore, email: String): SavedCloudHomes? =
            store.get(key(email))?.let { runCatching { ApiClient.json.decodeFromString(serializer(), it) }.getOrNull() }

        /** Every account's list (sign-out). */
        fun forgetAll(store: KeyValueStore) {
            store.get(INDEX)?.split('\n')?.filter { it.isNotBlank() }?.forEach { store.put(it, null) }
            store.put(INDEX, null)
        }
    }
}

/**
 * The cloud couldn't be reached (story 50.13): no answer, a server error, or an answer that
 * isn't the cloud's JSON (a captive portal). Not a refusal, which is the cloud's word.
 */
fun isCloudOutage(e: Throwable): Boolean = when (e) {
    is com.hamzalabs.maktaba.core.api.ApiException.Transport -> true
    is CloudException -> e.status == 0 && e.code in setOf("unexpected", "cloud_down") || e.status >= 500
    else -> false
}

@Serializable
data class CloudUser(val id: String = "", val email: String = "")

@Serializable
private data class CloudSignIn(@SerialName("access_token") val accessToken: String, val user: CloudUser = CloudUser())

@Serializable
private data class CloudHomes(val homes: List<CloudHome> = emptyList())

@Serializable
private data class CloudAssertion(val assertion: String)

@Serializable
private data class CloudProblem(val error: String = "", val message: String = "")

/** Maktaba Cloud said no: its [status] and [code] (`invalid_credentials`, `not_linked`, …). */
class CloudException(val status: Int, val code: String, message: String) : Exception(message)

/**
 * A Maktaba Cloud account, signed in (Epic 42 story 42.7): the homes it reaches, and a
 * one-use sign-in assertion for one of them, which the home turns into its own sign-in
 * (`ApiClient.signInWithAssertion`). The cloud's token lives here, in memory only: never
 * stored, asked for again when it is gone. The cloud says who the person is, never what they
 * may do in a home: the home decides.
 */
class CloudSession(
    val base: HttpUrl,
    private val http: OkHttpClient = defaultHttp,
) {
    @Volatile private var token: String? = null

    @Volatile var user: CloudUser? = null
        private set

    val signedIn: Boolean get() = token != null

    suspend fun signIn(email: String, password: String): CloudUser {
        val body = buildJsonObject { put("email", email); put("password", password) }
        val r = decode(CloudSignIn.serializer(), send("POST", "/v1/auth/login", body.toString(), auth = false))
        token = r.accessToken
        user = r.user
        return r.user
    }

    fun signOut() {
        token = null
        user = null
    }

    /** The homes this account is linked to or owns. */
    suspend fun homes(): List<CloudHome> = decode(CloudHomes.serializer(), send("GET", "/v1/homes")).homes

    /** A one-use sign-in assertion for the home [serverId] (good for a minute). */
    suspend fun assertion(serverId: String): String {
        val body = buildJsonObject { put("purpose", "signin") }
        val path = "/v1/homes/${URLEncoder.encode(serverId, Charsets.UTF_8).replace("+", "%20")}/assertion"
        return decode(CloudAssertion.serializer(), send("POST", path, body.toString())).assertion
    }

    private suspend fun send(method: String, path: String, body: String? = null, auth: Boolean = true): String {
        val t = token
        if (auth && t == null) throw CloudException(401, "signed_out", "Sign in to Maktaba Cloud.")
        val url = base.newBuilder().encodedPath(base.encodedPath.trimEnd('/') + path).build()
        val r = Request.Builder().url(url)
            .header("Accept", "application/json")
            .header(ClientHeader.NAME, ClientHeader.value)
            .apply { if (auth) header("Authorization", "Bearer $t") }
            .method(method, body?.toRequestBody(ApiClient.JSON))
            .build()
        val (code, bytes) = http.exchange(r)
        val text = String(bytes)
        if (code in 200..299) return text
        // the cloud no longer takes the token: signed out of the cloud (never of the home)
        if (code == 401 && auth) signOut()
        val p = runCatching { ApiClient.json.decodeFromString(CloudProblem.serializer(), text) }.getOrNull()
        throw CloudException(code, p?.error.orEmpty(), p?.message?.takeIf { it.isNotBlank() } ?: "Maktaba Cloud answered $code")
    }

    private fun <T> decode(s: KSerializer<T>, text: String): T =
        runCatching { ApiClient.json.decodeFromString(s, text) }
            .getOrElse { throw CloudException(0, "unexpected", "Unexpected answer from Maktaba Cloud") }

    companion object {
        /** The hosted Maktaba Cloud's API. */
        const val DEFAULT_API = "https://api.maktaba.tv"

        /** Where the app keeps the cloud's address a person chose (a test or their own cloud). */
        const val KEY_API = "cloud_api"

        /** The cloud's API address from what a person typed (https unless it is a local one). */
        fun normalize(text: String): HttpUrl? = Home.normalizeWithPath(text)?.toHttpUrlOrNull()

        /** No redirect is followed with the cloud's token: it would carry it to another address. */
        val defaultHttp: OkHttpClient by lazy {
            ApiClient.defaultHttp.newBuilder()
                .followRedirects(false)
                .followSslRedirects(false)
                .connectTimeout(10, TimeUnit.SECONDS)
                .readTimeout(20, TimeUnit.SECONDS)
                .build()
        }
    }
}
