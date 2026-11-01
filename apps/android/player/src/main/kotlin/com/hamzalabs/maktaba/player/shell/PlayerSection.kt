package com.hamzalabs.maktaba.player.shell

import androidx.annotation.StringRes
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.AccountCircle
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.LiveTv
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.VideoLibrary
import androidx.compose.material.icons.outlined.AccountCircle
import androidx.compose.material.icons.outlined.Home
import androidx.compose.material.icons.outlined.LiveTv
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material.icons.outlined.VideoLibrary
import androidx.compose.ui.graphics.vector.ImageVector
import com.hamzalabs.maktaba.player.R

/**
 * The Player's five sections (FR-5), in the order the navigation shows them, as on the Apple
 * Player and the web: Home, Libraries, Live TV, Search, Profile.
 */
enum class PlayerSection(
    @param:StringRes val label: Int,
    val icon: ImageVector,
    val selectedIcon: ImageVector,
    /** The section's first screen: what its stack starts with, and what re-selecting it returns to. */
    val root: Route,
) {
    Home(R.string.section_home, Icons.Outlined.Home, Icons.Filled.Home, Route.Home),
    Libraries(R.string.libraries, Icons.Outlined.VideoLibrary, Icons.Filled.VideoLibrary, Route.Libraries),
    LiveTv(R.string.section_live_tv, Icons.Outlined.LiveTv, Icons.Filled.LiveTv, Route.LiveTv),
    Search(R.string.section_search, Icons.Outlined.Search, Icons.Filled.Search, Route.Search),
    Profile(R.string.section_profile, Icons.Outlined.AccountCircle, Icons.Filled.AccountCircle, Route.Profile),
    ;

    companion object {
        /**
         * The sections a person can reach: only those whose screen has merged, so a TestFlight or
         * internal build never shows a half-built one (the epic's release rule). Each screen
         * story adds its section here, one line, in [entries]' order.
         */
        val enabled: List<PlayerSection> = listOf(
            Home,
            Libraries,
            LiveTv,
            Search,
            Profile,
        )

        /** The section whose root is [route], if it is one. */
        fun of(route: Route): PlayerSection? = entries.firstOrNull { it.root == route }
    }
}
