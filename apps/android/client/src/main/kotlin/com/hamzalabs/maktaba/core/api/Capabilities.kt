package com.hamzalabs.maktaba.core.api

import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull

// What the home has on (Epic 51 stories 51.09 and 51.11): `GET /api/capabilities` says, for
// every feature, "on" or "off"; an admin's answer also says what turns each one off. The apps
// hide a feature that is off from a viewer and explain it to an admin with a way to turn it on.
// The home still refuses an off feature's routes (409 component_off): hiding is never the rule.

/**
 * The build switch for 51.11 (default on). Off, every app reads every feature as on and hides
 * nothing; the home's 409 still answers for what is off.
 */
const val CAPABILITIES_GATE = true

/** The features the apps know, as `GET /api/capabilities` names them. */
object Feature {
    const val LIVE_TV = "live_tv"
    const val TRANSCRIPTS = "transcripts"
    const val SUBTITLES = "subtitles"
    const val SEMANTIC_SEARCH = "semantic_search"
    const val CHAPTERS = "chapters"
    const val YOUTUBE = "youtube"
    const val PLEX = "plex"
    const val REMOTE_ACCESS = "remote_access"
    const val RECOMMENDATIONS = "recommendations"

    val known: List<String> = listOf(LIVE_TV, TRANSCRIPTS, SUBTITLES, SEMANTIC_SEARCH, CHAPTERS, YOUTUBE, PLEX, REMOTE_ACCESS, RECOMMENDATIONS)
}

/** Why a feature is off and the components that turn it off (an admin's answer only). */
data class FeatureOff(val components: List<String> = emptyList(), val reason: String = "")

/**
 * What the home has on. [off] holds the known features that are off; a feature the answer does
 * not name (an older home) is on, and one this app does not know (a newer home) is ignored.
 * [admin] is true when the answer has an `admin` block: a cloud-linked play sign-in never gets
 * one, so it is a viewer even on an admin's account. [adminOff] is that block's explanation.
 */
data class Capabilities(
    val off: Set<String> = emptySet(),
    val admin: Boolean = false,
    val adminOff: Map<String, FeatureOff> = emptyMap(),
    /** The answer's version, sent back as `If-None-Match`; null before the first answer. */
    val etag: String? = null,
) {
    fun isOn(feature: String): Boolean = feature !in off

    /** Hidden: off, and this is a viewer (an admin sees the feature's place, with why it is off). */
    fun hides(feature: String): Boolean = !isOn(feature) && !admin

    /** Off and an admin: the place shows why, and the way to turn it on. */
    fun explains(feature: String): Boolean = !isOn(feature) && admin

    /** How many components are off across [adminOff] (the Remote's "3 parts are off"). */
    val componentsOff: Int get() = adminOff.values.flatMap { it.components }.toSet().size

    companion object {
        /** Every feature on: before the first answer, on a home too old to say, and with the gate off. */
        val AllOn = Capabilities()

        /** Reads a `GET /api/capabilities` body; null when it is not one. */
        fun parse(body: String): Capabilities? {
            val o = runCatching { ApiClient.json.parseToJsonElement(body) as? JsonObject }.getOrNull() ?: return null
            val features = o["features"] as? JsonObject ?: return null
            val off = Feature.known.filter { (features[it] as? JsonPrimitive)?.contentOrNull == "off" }.toSet()
            val admin = o["admin"] as? JsonObject
            val adminOff = (admin?.get("off") as? JsonObject).orEmpty()
                .filterKeys { it in Feature.known }
                .mapValues { (_, v) ->
                    val e = v as? JsonObject
                    FeatureOff(
                        components = (e?.get("components") as? JsonArray).orEmpty().mapNotNull { (it as? JsonPrimitive)?.contentOrNull },
                        reason = (e?.get("reason") as? JsonPrimitive)?.contentOrNull.orEmpty(),
                    )
                }
            val etag = (o["etag"] as? JsonPrimitive)?.contentOrNull
            return Capabilities(off, admin != null, adminOff, etag)
        }
    }
}

/** What asking for the capabilities gave. */
sealed interface CapabilitiesAnswer {
    data class Updated(val capabilities: Capabilities, val etag: String?) : CapabilitiesAnswer
    /** 304: the answer kept is still the home's. */
    data object NotModified : CapabilitiesAnswer
}

/**
 * `GET /api/capabilities`, with [etag] as `If-None-Match` when there is one. A home too old to
 * have the route (404) answers every feature on. Any other failure is thrown: the caller keeps
 * the last good answer.
 */
suspend fun ApiClient.capabilities(etag: String? = null): CapabilitiesAnswer {
    val r = request("GET", "/api/capabilities").let { if (etag != null) it.newBuilder().header("If-None-Match", "\"$etag\"").build() else it }
    return try {
        val c = Capabilities.parse(String(send(r))) ?: throw ApiException.Decoding("not a capabilities answer")
        CapabilitiesAnswer.Updated(c, c.etag)
    } catch (e: ApiException.Status) {
        when (e.code) {
            304 -> CapabilitiesAnswer.NotModified
            404 -> CapabilitiesAnswer.Updated(Capabilities.AllOn, null)
            else -> throw e
        }
    }
}

/** The home's Components page (51.10), where an admin turns a feature back on. */
fun componentsUrl(home: String): String = "${home.trimEnd('/')}/settings/components"
