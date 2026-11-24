package com.hamzalabs.maktaba.core.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * What a home's streaming service says of itself (`GET /api/stream/capabilities`). Only what a
 * player decides with is read.
 */
@Serializable
data class StreamCapabilities(
    /**
     * The client profiles the home knows (story 13.18). A player asks for a newer profile only
     * when it is listed: a home reads a profile it does not know as "generic" (H.264 and AAC,
     * 720p). Empty from a home older than the list.
     */
    @SerialName("client_profiles") val clientProfiles: List<String> = emptyList(),
    /** Every conversion on that home is in software (Epic 59 story 59.05). */
    @SerialName("software_only") val softwareOnly: Boolean = false,
)

suspend fun ApiClient.streamCapabilities(): StreamCapabilities = get("/api/stream/capabilities")
