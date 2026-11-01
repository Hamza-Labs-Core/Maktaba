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
) {
    /** Its addresses: its own first (on its network it answers there, faster), then the relay. */
    val addresses: List<String>
        get() = listOfNotNull(directUrl, relayUrl).filter { it.isNotBlank() }
            .mapNotNull { Home.normalizeWithPath(it) }.distinct()
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
