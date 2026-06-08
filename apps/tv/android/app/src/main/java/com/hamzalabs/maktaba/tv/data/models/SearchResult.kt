package com.hamzalabs.maktaba.tv.data.models

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** A single search hit; shaped to overlap with [Media] for the grid. */
@Serializable
data class SearchResult(
    val id: String,
    val title: String,
    val kind: String? = null,
    @SerialName("poster_url") val posterUrl: String? = null,
    @SerialName("library_id") val libraryId: String? = null,
) {
    fun asMedia() = Media(id = id, title = title, posterUrl = posterUrl)
}

/** Envelope for `GET /api/search` and `GET /api/libraries/{id}/items`. */
@Serializable
data class SearchResponse(
    val items: List<SearchResult> = emptyList(),
)
