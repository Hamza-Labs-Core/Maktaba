package com.hamzalabs.maktaba.tv.data.models

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** A browsable library. Mirrors `libraryView` in the API. */
@Serializable
data class Library(
    val id: String,
    val name: String,
    @SerialName("content_type") val contentType: String? = null,
    @SerialName("total_videos") val totalVideos: Int? = null,
)

/** Envelope for `GET /api/libraries`. */
@Serializable
data class LibraryList(
    val items: List<Library> = emptyList(),
)
