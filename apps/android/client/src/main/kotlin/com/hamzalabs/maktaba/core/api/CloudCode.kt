package com.hamzalabs.maktaba.core.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import java.time.OffsetDateTime

// Epic 58 story 58.03: a TV that cannot find the home on its network asks Maktaba Cloud for a
// code; a person signed in on the home approves it from "Approve a TV or device". The home's
// own codes (story 42.6) use the full Crockford alphabet and the cloud's a smaller one, so a
// code can look like either: the home's code is tried first, and a code the home does not hold
// that has the cloud's shape is then looked up through the home (`ApproveCode` in the core).

/** The cloud's TV codes: eight characters of [ALPHABET], shown `ABCD-2345`. */
object CloudCode {
    /** No 0, 1, I, L, O or U: nothing a person reads as something else. */
    const val ALPHABET = "23456789ABCDEFGHJKMNPQRSTVWXYZ"

    /** [raw] as the home's routes take it: upper case, without dashes or spaces (`ABCD2345`). */
    fun compact(raw: String): String = raw.uppercase().filterNot { it == '-' || it.isWhitespace() }

    /** Whether [raw] (any case, with or without the dash) has the cloud's shape. */
    fun isCloudShaped(raw: String): Boolean = compact(raw).let { c -> c.length == 8 && c.all { it in ALPHABET } }
}

/** What asked with a cloud code (`GET /api/pairing/cloud-code/{code}`), for "Is this your TV?". */
@Serializable
data class CloudTvCode(
    val code: String = "",
    /** `tizen`, `webos`, `roku`, … */
    val platform: String = "",
    @SerialName("app_version") val appVersion: String? = null,
    /** The country of the TV's address (ISO 3166 alpha-2); empty when the cloud could not tell. */
    val country: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
) {
    /** The country code, or null when the cloud did not say. */
    val countryCode: String? get() = country?.trim()?.uppercase()?.takeIf { it.length == 2 }

    val expiresMillis: Long? get() = expiresAt?.let { runCatching { OffsetDateTime.parse(it).toInstant().toEpochMilli() }.getOrNull() }
}

/** What the home said of a cloud code. */
sealed interface CloudCodeLookup {
    data class Found(val tv: CloudTvCode) : CloudCodeLookup

    /** No TV is waiting with it (wrong, expired, or a home from before cloud codes). */
    data object NotFound : CloudCodeLookup

    /** The home has no remote access: it cannot reach Maktaba Cloud for the code. */
    data object NotLinked : CloudCodeLookup
}

/** The home's problem code when it has no remote access to reach Maktaba Cloud with. */
const val CLOUD_NOT_LINKED = "cloud_not_linked"

/** Whether [this] is the home saying it has no remote access (409, `cloud_not_linked`). */
val ApiException.Status.cloudNotLinked: Boolean get() = code == 409 && problem?.code == CLOUD_NOT_LINKED

/** [s] as one encoded path segment. */
private fun segment(s: String): String = java.net.URLEncoder.encode(s, Charsets.UTF_8.name()).replace("+", "%20")

/**
 * What asked with the cloud code [code] (`GET /api/pairing/cloud-code/{code}`, signed in on the
 * home). A home from before cloud codes has no such route (its 404 or 405): [CloudCodeLookup.NotFound].
 */
suspend fun ApiClient.cloudCode(code: String): CloudCodeLookup = try {
    CloudCodeLookup.Found(decode(send(request("GET", "/api/pairing/cloud-code/${segment(CloudCode.compact(code))}"))))
} catch (e: ApiException.Status) {
    when {
        e.cloudNotLinked -> CloudCodeLookup.NotLinked
        e.code == 404 || e.code == 405 -> CloudCodeLookup.NotFound
        else -> throw e
    }
}

/**
 * Signs the TV showing the cloud code [code] in as the person signed in here
 * (`POST /api/pairing/cloud-approve`). A refusal is the home's [ApiException.Status].
 */
suspend fun ApiClient.approveCloudCode(code: String) {
    send(request("POST", "/api/pairing/cloud-approve", body = buildJsonObject { put("code", CloudCode.compact(code)) }))
}
