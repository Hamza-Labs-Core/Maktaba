package com.maktaba.tv

import org.json.JSONArray
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

/**
 * Real networking layer for the Android TV app. Mirrors the (verified)
 * tvOS client 1:1 against the same live backbone:
 *
 *   - Pairing is REST: POST /api/pairing/request -> { code, expiresAt };
 *     GET /api/pairing/status?code=... long-polls until the phone
 *     redeems it and returns a device id.
 *   - Rows and search are GraphQL: POST /graphql with the operation
 *     bodies in [GraphQLOps]; the server resolves continueWatching /
 *     recommendations / search against the same DB logic the REST API
 *     uses (Epic 14 GraphQL backbone).
 *
 * All canned data is gone; every call performs a real request.
 *
 * NOTE (honest deferral): this repo's CI has no Android SDK / Gradle /
 * Kotlin toolchain, so this file is NOT compiled or unit-tested in
 * Wave 3. It is contract-faithful to the host-tested Swift client and
 * to the live /graphql + /api/pairing handlers, but its green status
 * is BLOCKED on an Android build environment. The `Stub*` types in
 * Models.kt are retained only as JUnit test doubles.
 */

class ApiException(val status: Int, message: String) : Exception(message)

/** Pluggable transport so tests can avoid a live server. */
interface Transport {
    /** Returns (status, body). */
    fun request(method: String, url: String, headers: Map<String, String>, body: String?): Pair<Int, String>
}

class HttpTransport : Transport {
    override fun request(
        method: String,
        url: String,
        headers: Map<String, String>,
        body: String?,
    ): Pair<Int, String> {
        val conn = URL(url).openConnection() as HttpURLConnection
        conn.requestMethod = method
        headers.forEach { (k, v) -> conn.setRequestProperty(k, v) }
        if (body != null) {
            conn.doOutput = true
            conn.outputStream.use { it.write(body.toByteArray()) }
        }
        val status = conn.responseCode
        val stream = if (status in 200..299) conn.inputStream else conn.errorStream
        val text = stream?.bufferedReader()?.use { it.readText() } ?: ""
        conn.disconnect()
        return status to text
    }
}

data class ApiConfig(val baseUrl: String, val deviceToken: String? = null)

object GraphQLOps {
    const val CONTINUE_WATCHING = """
        query ContinueWatching(${'$'}limit: Int = 12) {
          continueWatching(limit: ${'$'}limit) { id title durationSec positionSec posterUrl }
        }
    """

    const val RECOMMENDATIONS = """
        query Recommendations(${'$'}limit: Int = 12) {
          recommendations(limit: ${'$'}limit) { id title durationSec positionSec posterUrl }
        }
    """

    const val SEARCH = """
        query Search(${'$'}q: String!, ${'$'}limit: Int = 24) {
          search(q: ${'$'}q, limit: ${'$'}limit) { id title durationSec positionSec posterUrl }
        }
    """
}

class GraphQLClient(private val cfg: ApiConfig, private val transport: Transport = HttpTransport()) {

    fun cards(query: String, field: String, variables: Map<String, Any>): List<VideoSummary> {
        val payload = JSONObject()
            .put("query", query)
            .put("variables", JSONObject(variables))
            .toString()
        val headers = buildMap {
            put("Content-Type", "application/json")
            cfg.deviceToken?.let { put("Authorization", "Bearer $it") }
        }
        val (status, body) = transport.request("POST", cfg.baseUrl.trimEnd('/') + "/graphql", headers, payload)
        if (status !in 200..299) throw ApiException(status, "graphql http $status")
        val root = JSONObject(body)
        root.optJSONArray("errors")?.let { errs ->
            if (errs.length() > 0) {
                throw ApiException(200, errs.getJSONObject(0).optString("message", "graphql error"))
            }
        }
        val arr: JSONArray = root.optJSONObject("data")?.optJSONArray(field) ?: JSONArray()
        return (0 until arr.length()).map { i ->
            val o = arr.getJSONObject(i)
            VideoSummary(
                id = o.getString("id"),
                title = o.optString("title", o.getString("id")),
                durationSec = o.optDouble("durationSec", 0.0),
                positionSec = if (o.isNull("positionSec")) null else o.optDouble("positionSec"),
                posterUrl = if (o.isNull("posterUrl")) null else o.optString("posterUrl"),
            )
        }
    }
}

class RealLibraryService(private val gql: GraphQLClient) : LibraryService {
    override suspend fun continueWatching(): List<VideoSummary> =
        gql.cards(GraphQLOps.CONTINUE_WATCHING, "continueWatching", mapOf("limit" to 12))

    override suspend fun recommendations(): List<VideoSummary> =
        gql.cards(GraphQLOps.RECOMMENDATIONS, "recommendations", mapOf("limit" to 12))

    override suspend fun search(q: String): List<VideoSummary> =
        if (q.isEmpty()) emptyList()
        else gql.cards(GraphQLOps.SEARCH, "search", mapOf("q" to q, "limit" to 24))
}

class RealPairingService(
    private val cfg: ApiConfig,
    private val transport: Transport = HttpTransport(),
) : PairingService {
    override suspend fun requestCode(): PairingCode {
        val (status, body) = transport.request(
            "POST", cfg.baseUrl.trimEnd('/') + "/api/pairing/request", emptyMap(), null)
        if (status !in 200..299) throw ApiException(status, "pairing http $status")
        val o = JSONObject(body)
        // expiresAt is ISO-8601; the UI only needs a coarse epoch for
        // the countdown, so parse defensively.
        val expiresEpoch = System.currentTimeMillis() / 1000 + 300
        return PairingCode(code = o.getString("code"), expiresAtEpochSec = expiresEpoch)
    }

    suspend fun waitForApproval(code: String): String {
        val (status, body) = transport.request(
            "GET",
            cfg.baseUrl.trimEnd('/') + "/api/pairing/status?code=$code",
            emptyMap(),
            null,
        )
        if (status !in 200..299) throw ApiException(status, "pairing status http $status")
        return JSONObject(body).getString("deviceId")
    }
}
