package com.hamzalabs.maktaba.player.shell

import kotlinx.serialization.Serializable

/**
 * Where the Player can be, as Navigation Compose reads it (typed routes): each section's root,
 * and the screens pushed on a section's stack, the same five as the Apple Player's route enum
 * (`video`, `series`, `library`, `channel`, `watch`). Every section can push every screen
 * (Home opens a video, Search a series, Live TV a channel), so each section's graph holds them all.
 */
@Serializable
sealed interface Route {
    // ---- the sections' roots ----
    @Serializable data object Home : Route
    @Serializable data object Libraries : Route
    @Serializable data object LiveTv : Route
    @Serializable data object Search : Route
    @Serializable data object Profile : Route

    // ---- pushed on a section's stack ----
    /** A video's page (48.28). */
    @Serializable data class Video(val id: String) : Route
    /** A series' page (48.27). */
    @Serializable data class Series(val id: String) : Route
    /** A library's videos or series (48.27). */
    @Serializable data class Library(val id: String) : Route
    /** A channel's live player (48.30), by the channel's id (`GET /api/channels/{id}`). */
    @Serializable data class Channel(val id: String) : Route
    /** The player (48.32), from [startSec] (null: where the person left it). */
    @Serializable data class Watch(val videoId: String, val startSec: Int? = null) : Route
}
