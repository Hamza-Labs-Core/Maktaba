package com.hamzalabs.maktaba.tv.data.api

import com.hamzalabs.maktaba.tv.data.models.AuthTokens
import com.hamzalabs.maktaba.tv.data.models.RefreshRequest
import kotlinx.serialization.json.Json
import okhttp3.Interceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response

/**
 * Adds `Authorization: Bearer <access-token>` to every outgoing
 * request. Pairs with [TokenAuthenticator], which handles the 401 →
 * refresh → retry path. Keeping the two concerns separate lets OkHttp's
 * authenticator machinery cap retries for us (no infinite refresh loop).
 */
class AuthInterceptor(private val tokens: TokenStore) : Interceptor {
    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request().newBuilder().apply {
            tokens.accessToken?.let { header("Authorization", "Bearer $it") }
        }.build()
        return chain.proceed(request)
    }
}

/**
 * Invoked by OkHttp only when a request comes back 401. Exchanges the
 * refresh token for a new access token and retries the original request
 * once. Returning `null` (no retry) when there is no refresh token or
 * the refresh fails bubbles the 401 up so the UI can bounce to sign-in.
 *
 * The refresh call is fired against a *bare* OkHttp client (no auth
 * interceptor) to avoid recursing through this same authenticator.
 */
class TokenAuthenticator(
    private val tokens: TokenStore,
    private val baseUrl: String,
    private val json: Json,
) : okhttp3.Authenticator {

    override fun authenticate(route: okhttp3.Route?, response: Response): Request? {
        // Already retried once (responses chain via priorResponse) → give up.
        if (response.priorResponse != null) return null
        val refresh = tokens.refreshToken ?: return null

        val newTokens = synchronized(this) {
            // Another thread may have refreshed while we waited on the lock.
            val current = tokens.accessToken
            if (current != null && response.request.header("Authorization") != "Bearer $current") {
                return@synchronized AuthTokens(accessToken = current, refreshToken = refresh)
            }
            runCatching { fetchRefresh(refresh) }.getOrNull()
        } ?: return null

        tokens.save(newTokens.accessToken, newTokens.refreshToken)
        return response.request.newBuilder()
            .header("Authorization", "Bearer ${newTokens.accessToken}")
            .build()
    }

    private fun fetchRefresh(refreshToken: String): AuthTokens {
        val client = OkHttpClient()
        val payload = json.encodeToString(RefreshRequest.serializer(), RefreshRequest(refreshToken))
        val body = payload.toRequestBody("application/json".toMediaType())
        val request = Request.Builder()
            .url("${baseUrl.trimEnd('/')}/api/auth/refresh")
            .post(body)
            .build()
        client.newCall(request).execute().use { resp ->
            val text = resp.body?.string().orEmpty()
            require(resp.isSuccessful) { "refresh failed: ${resp.code}" }
            return json.decodeFromString(AuthTokens.serializer(), text)
        }
    }
}
