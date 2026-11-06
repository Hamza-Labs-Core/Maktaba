package com.hamzalabs.maktaba.desktop.shell

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.SnapshotStateList
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.Feature

/**
 * Where the desktop Player can be (story 55.01), as the Android Player's `shell/Route.kt`: each
 * section's root, and the pages pushed on a section. Playing a video is not a route: the window
 * shows the player until it is closed, then the page under it.
 */
sealed interface Route {
    data object Home : Route
    data object Libraries : Route
    data object LiveTv : Route
    data object Search : Route
    data object Profile : Route

    /** A video's page; [passage] is the transcript line it was opened from (a search hit), if any. */
    data class Video(val id: String, val passage: Passage? = null) : Route

    data class Series(val id: String) : Route

    /** A library's page; null is every library ("All videos"). */
    data class Library(val id: String?) : Route

    data class Channel(val id: String) : Route

    /** A playlist's page (Profile's Playlists). */
    data class Playlist(val id: String) : Route

    /** A transcript line a video page shows, with Play from its start. */
    data class Passage(val startSec: Double, val text: String)
}

/** The five sections, in the Android Player's order, with their words and their first page. */
enum class Section(val key: String, val root: Route) {
    Home("section.home", Route.Home),
    Libraries("section.libraries", Route.Libraries),
    LiveTv("section.liveTv", Route.LiveTv),
    Search("section.search", Route.Search),
    Profile("section.profile", Route.Profile),
    ;

    /** Shown on the rail: Live TV is left off for a viewer when it is off on the home (story 55.02). */
    fun shown(features: Capabilities): Boolean = this != LiveTv || !features.hides(Feature.LIVE_TV)

    companion object {
        /** The sections on the rail for [features], in their order. */
        fun shown(features: Capabilities): List<Section> = entries.filter { it.shown(features) }
    }
}

/**
 * One stack of pages per section (Compose state). [select] shows a section, or returns it to its
 * root when it is already shown; [push] adds a page (never the same page twice on top); [back]
 * returns. A section keeps its stack while another is shown.
 */
class Navigator {
    private val stacks: Map<Section, SnapshotStateList<Route>> = Section.entries.associateWith { mutableStateListOf(it.root) }

    var section by mutableStateOf(Section.Home)
        private set

    /** Grows each time Ctrl+F or `/` asks for the search field: Search focuses it. */
    var searchAsked by mutableStateOf(0)
        private set

    /** The page shown. */
    val current: Route get() = stacks.getValue(section).last()

    /** There is a page to go back to in the section shown. */
    val canGoBack: Boolean get() = stacks.getValue(section).size > 1

    /** The pages of [s], from its root. */
    fun stack(s: Section): List<Route> = stacks.getValue(s).toList()

    fun select(s: Section) {
        if (s == section) {
            val st = stacks.getValue(s)
            while (st.size > 1) st.removeAt(st.lastIndex)
        } else {
            section = s
        }
    }

    fun push(route: Route) {
        val st = stacks.getValue(section)
        if (st.last() != route) st.add(route)
    }

    /** Back one page: false at a root (nothing happens). */
    fun back(): Boolean {
        val st = stacks.getValue(section)
        if (st.size <= 1) return false
        st.removeAt(st.lastIndex)
        return true
    }

    /** Shows [s] as its stack was (a shortcut opens a section without leaving its page). */
    fun open(s: Section) {
        section = s
    }

    /** Ctrl+F or `/`: Search, with its field focused. */
    fun askSearch() {
        section = Section.Search
        searchAsked++
    }

    /**
     * The home's features changed: a section that is no longer shown (Live TV went off for a
     * viewer) gives way to Home, and its stack starts again. A page of that feature on any other
     * section (a channel opened from Home's What's On Now) closes too, with the pages above it.
     */
    fun follow(features: Capabilities) {
        for (s in Section.entries) {
            if (s.shown(features)) continue
            val st = stacks.getValue(s)
            while (st.size > 1) st.removeAt(st.lastIndex)
            if (section == s) section = Section.Home
        }
        if (features.hides(Feature.LIVE_TV)) {
            for (st in stacks.values) {
                val at = st.indexOfFirst { it is Route.Channel }
                if (at > 0) while (st.size > at) st.removeAt(st.lastIndex)
            }
        }
    }
}
