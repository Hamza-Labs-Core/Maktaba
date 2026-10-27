package com.hamzalabs.maktaba.core.api

import kotlinx.coroutines.delay
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import java.time.OffsetDateTime

/**
 * Sign-in by code (Epic 42 story 42.6; `api/internal/handlers/discovery/device.go`): a device
 * that is not signed in asks the home for a short code and shows it (and a QR of the home's
 * approve page); a person signed in elsewhere (the web, the Remote, the Player's account menu)
 * approves it; the device's poll then collects its own tokens for that person.
 */
object PairingCode {
    /** The code's alphabet (Crockford's base 32: no I, L, O or U). */
    private const val ALPHABET = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

    /**
     * The eight characters of a code as a person types or reads it: any case, with or without
     * the dash and spaces, and the letters a person mistakes for digits (O for 0, I and L for 1)
     * read as the digits. Null when it is not a code.
     */
    fun normalize(text: String): String? {
        val c = text.uppercase().filterNot { it == '-' || it.isWhitespace() }
            .map { when (it) { 'O' -> '0'; 'I', 'L' -> '1'; else -> it } }
            .joinToString("")
        return c.takeIf { it.length == 8 && it.all { ch -> ch in ALPHABET } }
    }

    /** As the device shows it: ABCD-1234. */
    fun shown(code: String): String = normalize(code)?.let { it.take(4) + "-" + it.drop(4) } ?: code

    /**
     * The code in what a scanned QR holds: the approve page's address (`…/pair?code=ABCD-1234`)
     * or the code itself.
     */
    fun fromScan(text: String): String? {
        val t = text.trim()
        t.toHttpUrlOrNull()?.let { url -> return url.queryParameter("code")?.let(::normalize) }
        return normalize(t)
    }
}

/** `GET /api/pairing/device/{code}`: what is asking to sign in, for the approver to see. */
@Serializable
data class PairingRequest(
    val code: String = "",
    /** Which app: player, remote, tv, … (from its `X-Maktaba-Client`). */
    @SerialName("client_app") val clientApp: String? = null,
    /** android, ios, androidtv, … */
    val platform: String? = null,
    /** phone, tablet, tv, … */
    val device: String? = null,
    /** The device's own name, as it gave it. */
    @SerialName("device_label") val deviceLabel: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
) {
    val expiresMillis: Long? get() = expiresAt?.let(::epochMillis)
}

/** What the home says about a code an approver typed or scanned. */
sealed interface PairingLookup {
    data class Waiting(val request: PairingRequest) : PairingLookup

    /** No device is waiting with this code here (wrong code, ran out, or another home's). */
    data object Unknown : PairingLookup

    /** The home cannot say what is asking (older than the lookup). */
    data object Unsupported : PairingLookup
}

/** `POST /api/pairing/device`: the code to show, and the device's own secret to poll with. */
@Serializable
data class DeviceCode(
    /** What a person reads and types: ABCD-1234. */
    val code: String,
    /** The device's secret: only the device holding it collects the approval. Never shown. */
    @SerialName("device_code") val deviceCode: String,
    @SerialName("expires_at") val expiresAt: String? = null,
    /** The approve page on the home, with the code: `/pair?code=ABCD-1234`. */
    @SerialName("verify_path") val verifyPath: String = "/pair",
    /** The approve page's full address, when the home knows the address to give. */
    @SerialName("verify_url") val verifyUrl: String? = null,
    val interval: Int = 2,
) {
    val expiresMillis: Long? get() = expiresAt?.let(::epochMillis)

    /**
     * What the QR holds: the home's `verify_url`, else the approve page on the address this
     * device reached the home at (under its base path).
     */
    fun approveUrl(home: HttpUrl): String {
        verifyUrl?.takeIf { it.toHttpUrlOrNull() != null }?.let { return it }
        val base = home.encodedPath.trimEnd('/')
        val path = if (verifyPath.startsWith("/")) base + verifyPath else "$base/$verifyPath"
        return (home.resolve(path) ?: home).toString()
    }
}

private fun epochMillis(s: String): Long? = runCatching { OffsetDateTime.parse(s).toInstant().toEpochMilli() }.getOrNull()

/**
 * The device side of sign-in by code: asks for a code ([run]'s `onCode`), polls until a person
 * approves it, and returns this device's tokens. A code that runs out (ten minutes, or a key
 * rotation voids it) is replaced by a new one, shown again. A home without sign-in by code is
 * [Unsupported]: the app offers the password instead.
 *
 * [post] sends a request without a token (`ApiClient.publicPost`): its status and body.
 */
class DeviceSignIn(
    private val post: suspend (path: String, body: JsonObject) -> Pair<Int, String>,
    private val label: String,
    private val now: () -> Long = System::currentTimeMillis,
    private val sleep: suspend (Long) -> Unit = { delay(it) },
) {
    /** This home cannot sign a device in with a code. */
    class Unsupported(val status: Int) : Exception("The home has no sign-in by code ($status).")

    /** Runs until a person approves; returns the tokens. Cancel to stop. */
    suspend fun run(onCode: (DeviceCode) -> Unit): AuthTokens {
        while (true) {
            val code = start()
            onCode(code)
            poll(code)?.let { return it }
        }
    }

    internal suspend fun start(): DeviceCode {
        var unreachable = 0
        val body = buildJsonObject { if (label.isNotBlank()) put("device_label", label) }
        while (true) {
            val (status, text) = try {
                post("/api/pairing/device", body)
            } catch (e: ApiException.Transport) {
                // the home is restarting or the network blinked: ask again, then say so
                if (++unreachable >= 3) throw e
                sleep(RETRY_MS)
                continue
            }
            when (status) {
                200, 201 -> return try {
                    ApiClient.json.decodeFromString(DeviceCode.serializer(), text)
                } catch (e: Exception) {
                    throw ApiException.Decoding(e.message ?: e.toString())
                }
                // no such route, or an older home's gate in front of it
                401, 403, 404, 405, 501 -> throw Unsupported(status)
                // busy or restarting: a moment later
                429, 503 -> sleep(RETRY_MS * 2)
                else -> throw ApiException.Status(status, text.take(BODY_KEPT))
            }
        }
    }

    /** Polls one code until approved (the tokens) or gone (null: ask for a new code). */
    internal suspend fun poll(code: DeviceCode): AuthTokens? {
        // the home's expiry, unless this device's clock disagrees with it (the home's 410 says too)
        val until = code.expiresMillis?.takeIf { it - now() in 1..DEFAULT_TTL_MS } ?: (now() + DEFAULT_TTL_MS)
        val every = code.interval.coerceIn(1, 30) * 1000L
        val body = buildJsonObject {
            put("device_code", code.deviceCode)
            if (label.isNotBlank()) put("device_label", label)
        }
        while (now() < until) {
            sleep(every)
            val (status, text) = try {
                post("/api/pairing/device/token", body)
            } catch (e: ApiException.Transport) {
                continue
            }
            when (status) {
                200 -> return try {
                    ApiClient.json.decodeFromString(AuthTokens.serializer(), text)
                } catch (e: Exception) {
                    throw ApiException.Decoding(e.message ?: e.toString())
                }
                202 -> continue
                429 -> sleep(every) // polled too fast: one more turn
                // expired, made under another key, or already collected: a new code
                404, 409, 410 -> return null
                else -> if (status >= 500) continue else return null
            }
        }
        return null
    }

    private companion object {
        const val RETRY_MS = 5_000L
        const val DEFAULT_TTL_MS = 10 * 60_000L
    }
}
