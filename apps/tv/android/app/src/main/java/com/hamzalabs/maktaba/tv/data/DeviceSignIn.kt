package com.hamzalabs.maktaba.tv.data

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import kotlinx.coroutines.delay
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import okhttp3.HttpUrl
import okhttp3.OkHttpClient

/**
 * Signing the TV in with a code (`api/internal/handlers/discovery/device.go`, the same flow as
 * the Apple TV's): the TV asks the home for a code, shows it with a QR of the home's approve
 * page (`/pair?code=…`), and polls until a person signed in on a phone or the web approves it.
 * Then the poll answers with this TV's own tokens, for the person who approved.
 *
 * A code runs out after ten minutes (or a key rotation voids it): the TV asks for a new one and
 * shows it ([run]'s `onCode` again). A home without sign-in by code answers [Unsupported]: the
 * TV offers the keyboard sign-in instead.
 */
class DeviceSignIn(
    private val home: HttpUrl,
    private val label: String,
    private val http: OkHttpClient = ApiClient.defaultHttp,
    private val now: () -> Long = System::currentTimeMillis,
    private val sleep: suspend (Long) -> Unit = { delay(it) },
) {
    /** This home cannot sign a TV in with a code. */
    class Unsupported(code: Int) : Exception("The home has no sign-in by code ($code).")

    /** The approve page's address for [code], for the QR and to read out. */
    fun approveUrl(code: DeviceCode): HttpUrl = home.resolve(code.verifyPath) ?: home

    /** Runs until a person approves; returns the TV's tokens. Cancel to stop. */
    suspend fun run(onCode: (DeviceCode) -> Unit): DeviceTokens {
        while (true) {
            val code = start()
            onCode(code)
            val tokens = poll(code)
            if (tokens != null) return tokens
        }
    }

    internal suspend fun start(): DeviceCode {
        var unreachable = 0
        while (true) {
            val (status, text) = try {
                TvApi.exchange(http, TvApi.post(url("/api/pairing/device"), "{}"))
            } catch (e: ApiException.Transport) {
                // the home is restarting or the network blinked: ask again, then say so
                if (++unreachable >= 3) throw e
                sleep(RETRY_MS)
                continue
            }
            when (status) {
                201, 200 -> return TvApi.json.decodeFromString(DeviceCode.serializer(), text)
                // an older home's global gate (401), no route
                401, 403, 404, 405, 501 -> throw Unsupported(status)
                // busy or restarting: a moment later
                503, 429 -> sleep(RETRY_MS * 2)
                else -> throw ApiException.Status(status, text.take(300))
            }
        }
    }

    /** Polls one code until approved (its tokens) or gone (null: ask for a new code). */
    internal suspend fun poll(code: DeviceCode): DeviceTokens? {
        // the home's expiry, unless this TV's clock disagrees with it (the home's 410 says too)
        val until = code.expiresMillis?.takeIf { it - now() in 1..DEFAULT_TTL_MS } ?: (now() + DEFAULT_TTL_MS)
        val every = code.interval.coerceIn(1, 30) * 1000L
        val body = buildJsonObject {
            put("device_code", code.deviceCode)
            put("device_label", label)
        }.toString()
        while (now() < until) {
            sleep(every)
            val (status, text) = try {
                TvApi.exchange(http, TvApi.post(url("/api/pairing/device/token"), body))
            } catch (e: ApiException.Transport) {
                continue
            }
            when (status) {
                200 -> return TvApi.json.decodeFromString(DeviceTokens.serializer(), text)
                202 -> continue
                429 -> sleep(every) // polled too fast: wait one more turn
                // expired, made under another key, or already collected: a new code
                410, 409, 404 -> return null
                else -> if (status >= 500) continue else return null
            }
        }
        return null
    }

    private fun url(path: String): HttpUrl = home.newBuilder().encodedPath(path).build()

    private companion object {
        const val RETRY_MS = 5_000L
        const val DEFAULT_TTL_MS = 10 * 60_000L
    }
}
