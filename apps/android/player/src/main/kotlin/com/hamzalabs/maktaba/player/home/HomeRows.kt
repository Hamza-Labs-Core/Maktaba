package com.hamzalabs.maktaba.player.home

import com.hamzalabs.maktaba.core.api.AddedEntry
import com.hamzalabs.maktaba.core.api.HomeRails
import com.hamzalabs.maktaba.core.api.NowAnswer
import com.hamzalabs.maktaba.core.api.NowEntry
import com.hamzalabs.maktaba.core.api.Recommendations
import com.hamzalabs.maktaba.core.api.VideoCard
import com.hamzalabs.maktaba.player.shell.Route

// What Home shows, as data (FR-10, FR-11): the rows in their order, each row's cards and what a
// card does. Built by a pure function from the three answers, so the order and the hiding of
// empty rows are unit-tested apart from the screen.

/** A row's title: one of Home's own (translated on screen), or a pinned library's name. */
sealed interface RowTitle {
    data class Named(val name: String) : RowTitle
    enum class Own : RowTitle { ContinueWatching, OnDeck, LiveNow, Favorites, RecentlyAdded, RecentlyWatched, ForYou, FromLibrary }
}

/** One row of Home under the hero. [key] is stable (the list's key and the row's test tag, `row-<key>`). */
sealed interface HomeRow {
    val key: String
    val title: RowTitle

    /** A row of video (or series) cards; [seeAll] opens the whole list when there is a screen for it. */
    data class Cards(override val key: String, override val title: RowTitle, val cards: List<HomeCard>, val seeAll: Route? = null) : HomeRow

    /** Live TV on now: one card per channel. */
    data class Live(val channels: List<NowEntry>) : HomeRow {
        override val key: String get() = KEY_LIVE
        override val title: RowTitle get() = RowTitle.Own.LiveNow
    }
}

/**
 * A card of a row. [posterVideo] is whose poster it shows; [title] is null for a recommendation
 * whose video Home has not read yet (the screen asks for it, `GET /api/videos/{id}`).
 *
 * - [resumeAtSec]: a Continue Watching card's context menu has Resume, opening the player there.
 * - [recommended]: a recommendation's context menu has Not interested.
 * - [newLessons]: a series of Recently Added with that many new lessons (it opens the series).
 */
data class HomeCard(
    val videoId: String,
    val title: String?,
    val open: Route,
    val posterVideo: String = videoId,
    val subtitle: String? = null,
    val progress: Float? = null,
    val watched: Boolean = false,
    val resumeAtSec: Int? = null,
    val recommended: Boolean = false,
    val newLessons: Int? = null,
    val seriesName: String? = null,
) {
    /** The list key: a series group and its first lesson's video card never collide. */
    val key: String get() = if (newLessons != null) "series:${(open as? Route.Series)?.id}" else videoId
}

const val KEY_CONTINUE = "continue"
const val KEY_ON_DECK = "ondeck"
const val KEY_LIVE = "live"
const val KEY_FAVORITES = "favorites"
const val KEY_ADDED = "added"
const val KEY_WATCHED = "watched"
const val KEY_PINNED = "pinned-"
const val KEY_FOR_YOU = "rec-for_you"
const val KEY_FROM_LIBRARY = "rec-from_library"

/** The recommendation rails Home shows (FR-10), in their order; others (Continue Watching's twin) are Home's own rows already. */
val HOME_RAIL_KINDS = listOf("for_you" to RowTitle.Own.ForYou, "from_library" to RowTitle.Own.FromLibrary)

/**
 * Home's rows under the hero, top to bottom (FR-10): Continue Watching, On Deck, Live TV on now,
 * Favorites, Recently Added, Recently Watched, one row per pinned library, For You, From your
 * library. A row with no cards is left out. [known] holds the videos read for recommendation
 * cards (by id); Home's own rows name most of them already.
 */
fun homeRows(rails: HomeRails, recs: Recommendations?, now: NowAnswer?, known: Map<String, VideoCard> = emptyMap()): List<HomeRow> = buildList {
    fun cards(key: String, title: RowTitle, cards: List<HomeCard>, seeAll: Route? = null) {
        if (cards.isNotEmpty()) add(HomeRow.Cards(key, title, cards, seeAll))
    }

    cards(KEY_CONTINUE, RowTitle.Own.ContinueWatching, rails.continueWatching.map { it.card(resume = true) })
    cards(KEY_ON_DECK, RowTitle.Own.OnDeck, rails.onDeck.map { it.card() })
    now?.channels?.takeIf { it.isNotEmpty() }?.let { add(HomeRow.Live(it)) }
    cards(KEY_FAVORITES, RowTitle.Own.Favorites, rails.favorites.map { it.card() })
    // grouped by series when the home sends it (an older home: the plain list)
    val added = if (rails.recentlyAddedGrouped.isNotEmpty()) rails.recentlyAddedGrouped.map { it.card() } else rails.recentlyAdded.map { it.card() }
    cards(KEY_ADDED, RowTitle.Own.RecentlyAdded, added)
    cards(KEY_WATCHED, RowTitle.Own.RecentlyWatched, rails.recentlyWatched.map { it.card() })
    rails.pinned.forEach { p ->
        // what the person is in the middle of first, then what is new (the web's order)
        val videos = (p.continueWatching + p.recentlyAdded).distinctBy { it.id }
        cards(KEY_PINNED + p.library.id, RowTitle.Named(p.library.name), videos.map { it.card() }, Route.Library(p.library.id))
    }

    // what Home's rows already say about a video names a recommendation's card too
    val named = buildMap {
        putAll(known)
        (rails.continueWatching + rails.onDeck + rails.favorites + rails.recentlyAdded + rails.recentlyWatched +
            rails.pinned.flatMap { it.continueWatching + it.recentlyAdded }).forEach { put(it.id, it) }
    }
    HOME_RAIL_KINDS.forEach { (kind, title) ->
        val items = recs?.rails.orEmpty().filter { it.reasonKind == kind }.flatMap { it.items }.distinctBy { it.videoId }
        cards(
            "rec-$kind",
            title,
            items.map { item ->
                val v = named[item.videoId]
                HomeCard(
                    videoId = item.videoId,
                    title = v?.displayTitle,
                    open = Route.Video(item.videoId),
                    subtitle = v?.series?.name,
                    progress = com.hamzalabs.maktaba.core.api.watchedFraction(item.positionSec, item.durationSec, false)?.toFloat(),
                    recommended = true,
                )
            },
        )
    }
}

/** A video card; [resume] gives it Resume at the person's position (Continue Watching). */
internal fun VideoCard.card(resume: Boolean = false): HomeCard = HomeCard(
    videoId = id,
    title = displayTitle,
    open = Route.Video(id),
    subtitle = series?.name,
    progress = watchedFraction?.toFloat(),
    watched = completed == true,
    resumeAtSec = if (resume) (positionSec ?: 0.0).toInt() else null,
)

/** A Recently Added entry: a series with its new lessons (opens the series), or a video. */
internal fun AddedEntry.card(): HomeCard {
    val s = series
    return if (kind == "series" && s != null) {
        HomeCard(
            videoId = video.id,
            title = s.name.takeIf { it.isNotBlank() },
            open = Route.Series(s.id),
            newLessons = count,
            seriesName = s.name,
        )
    } else {
        video.card().let { if (it.subtitle == null && s != null) it.copy(subtitle = s.name) else it }
    }
}

/** True when Home has nothing to show at all (FR-13: no libraries the person may see). */
fun isEmptyHome(rails: HomeRails, rows: List<HomeRow>): Boolean = rails.featured == null && rows.none { it is HomeRow.Cards }
