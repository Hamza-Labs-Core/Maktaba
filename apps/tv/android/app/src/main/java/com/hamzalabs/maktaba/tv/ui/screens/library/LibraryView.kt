package com.hamzalabs.maktaba.tv.ui.screens.library

import android.content.Context
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Card
import androidx.tv.material3.CardDefaults
import androidx.tv.material3.ExperimentalTvMaterial3Api
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Tab
import androidx.tv.material3.TabDefaults
import androidx.tv.material3.TabRow
import androidx.tv.material3.TabRowDefaults
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.queue.QueueSource
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.data.CollectionRow
import com.hamzalabs.maktaba.tv.data.GroupedCard
import com.hamzalabs.maktaba.tv.data.TvApi
import com.hamzalabs.maktaba.tv.data.TvCard
import com.hamzalabs.maktaba.tv.data.TvCategory
import com.hamzalabs.maktaba.tv.data.TvCollection
import com.hamzalabs.maktaba.tv.data.TvPlaylist
import com.hamzalabs.maktaba.tv.data.TvVideo
import com.hamzalabs.maktaba.tv.ui.components.CardCorner
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton
import com.hamzalabs.maktaba.tv.ui.components.MediaCard
import com.hamzalabs.maktaba.tv.ui.components.SectionTitle
import com.hamzalabs.maktaba.tv.ui.components.goldFocusBorder
import com.hamzalabs.maktaba.tv.ui.components.maktabaCardColors
import com.hamzalabs.maktaba.tv.ui.screens.Centered
import com.hamzalabs.maktaba.tv.ui.screens.Gutter
import com.hamzalabs.maktaba.tv.ui.screens.LoadState
import com.hamzalabs.maktaba.tv.ui.screens.clock
import com.hamzalabs.maktaba.tv.ui.screens.rememberLoad
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import kotlinx.coroutines.CancellationException

/** The five tabs of a library, in order (story 56.03). */
enum class LibraryTab(val label: Int) {
    Recommended(R.string.library_tab_recommended),
    Library(R.string.library_tab_library),
    Collections(R.string.library_tab_collections),
    Playlists(R.string.library_tab_playlists),
    Categories(R.string.library_tab_categories),
}

/** The tab a library reopens on: the last one chosen, kept on the device per library. */
object LibraryTabMemory {
    private fun prefs(c: Context) = c.getSharedPreferences("library_tabs", Context.MODE_PRIVATE)
    fun get(c: Context, library: String): LibraryTab =
        runCatching { LibraryTab.valueOf(prefs(c).getString(library, null) ?: "") }.getOrDefault(LibraryTab.Recommended)
    fun set(c: Context, library: String, tab: LibraryTab) = prefs(c).edit().putString(library, tab.name).apply()
}

/** The Library tab's view: its show (videos or series), sort and filters. Play and Shuffle play it. */
class LibraryView {
    var bySeries by mutableStateOf(false)
    var sort by mutableIntStateOf(0)
    val filters = androidx.compose.runtime.mutableStateMapOf<String, String>()

    /** The grid's query: the filters, the sort and its order. */
    fun query(): Map<String, String> = filters.toMap() + SORTS[sort].let { mapOf("sort" to it.key, "order" to it.order) }

    companion object {
        data class Sort(val label: Int, val key: String, val order: String)
        val SORTS = listOf(
            Sort(R.string.library_sort_newest, "created_at", "desc"),
            Sort(R.string.library_sort_oldest, "created_at", "asc"),
            Sort(R.string.library_sort_published, "published_at", "desc"),
            Sort(R.string.library_sort_az, "title", "asc"),
            Sort(R.string.library_sort_za, "title", "desc"),
            Sort(R.string.library_sort_longest, "duration_sec", "desc"),
            Sort(R.string.library_sort_shortest, "duration_sec", "asc"),
            Sort(R.string.library_sort_plays, "plays", "desc"),
        )
    }
}

/**
 * A library on the TV (Epic 56 stories 56.02-56.05, 56.08, 56.10): its name with Play and
 * Shuffle (which play the Library tab's view), and the five tabs Plex's TV apps have. The tab
 * row selects on focus; Down enters a tab; the last tab chosen reopens.
 */
@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
fun LibraryTabsScreen(api: TvApi, library: String, name: String) {
    val nav = LocalTvNav.current
    val context = LocalContext.current
    var tab by remember(library) { mutableStateOf(LibraryTabMemory.get(context, library)) }
    val view = remember(library) { LibraryView() }
    val tabFocus = remember { FocusRequester() }
    LaunchedEffect(library) { runCatching { tabFocus.requestFocus() } }
    Column(Modifier.fillMaxSize().padding(top = 24.dp)) {
        Row(
            Modifier.fillMaxWidth().padding(horizontal = Gutter),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            SectionTitle(name, Modifier.weight(1f))
            if (tab == LibraryTab.Recommended || tab == LibraryTab.Library) {
                MaktabaButton(onClick = { nav.play(QueueSource("library", library, view.query()), false, null) }, modifier = Modifier.testTag("library-play")) {
                    Text("▶ " + stringResource(R.string.library_play))
                }
                MaktabaButton(onClick = { nav.play(QueueSource("library", library, view.query()), true, null) }, modifier = Modifier.testTag("library-shuffle")) {
                    Text("⤮ " + stringResource(R.string.library_shuffle))
                }
            }
        }
        TabRow(
            selectedTabIndex = tab.ordinal,
            modifier = Modifier.padding(horizontal = Gutter, vertical = 12.dp),
            indicator = { positions, focused ->
                positions.getOrNull(tab.ordinal)?.let {
                    TabRowDefaults.PillIndicator(currentTabPosition = it, doesTabRowHaveFocus = focused, activeColor = MaktabaPalette.Gold, inactiveColor = MaktabaPalette.Raised)
                }
            },
        ) {
            LibraryTab.entries.forEach { t ->
                Tab(
                    selected = t == tab,
                    onFocus = { tab = t; LibraryTabMemory.set(context, library, t) },
                    // the library opens with focus on its tab (in Arabic too: not the row's first child)
                    modifier = Modifier.testTag("tab-${t.name}").then(if (t == tab) Modifier.focusRequester(tabFocus) else Modifier),
                    colors = TabDefaults.pillIndicatorTabColors(
                        contentColor = MaktabaPalette.Muted,
                        inactiveContentColor = MaktabaPalette.Muted,
                        selectedContentColor = MaktabaPalette.Gold,
                        focusedContentColor = MaktabaPalette.OnGold,
                    ),
                ) {
                    Text(stringResource(t.label), modifier = Modifier.padding(horizontal = 16.dp, vertical = 6.dp))
                }
            }
        }
        Box(Modifier.weight(1f)) {
            when (tab) {
                LibraryTab.Recommended -> RecommendedTab(api, library)
                LibraryTab.Library -> LibraryGridTab(api, library, view)
                LibraryTab.Collections -> CollectionsTab(api, library)
                LibraryTab.Playlists -> PlaylistsTab(api, library)
                LibraryTab.Categories -> CategoriesTab(api, library) { key, value ->
                    view.filters.clear()
                    view.filters[key] = value
                    tab = LibraryTab.Library
                }
            }
        }
    }
}

/** Rows of cards (Recommended, Home): each its name and its cards. */
@Composable
fun CardRows(api: TvApi, rows: List<Pair<String, List<TvCard>>>, empty: String) {
    val nav = LocalTvNav.current
    val shown = rows.filter { it.second.isNotEmpty() }
    if (shown.isEmpty()) {
        Centered { Text(empty, style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text) }
        return
    }
    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(start = Gutter, end = Gutter, bottom = 48.dp), verticalArrangement = Arrangement.spacedBy(24.dp)) {
        items(shown, key = { it.first }) { (title, cards) ->
            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                SectionTitle(title)
                LazyRow(horizontalArrangement = Arrangement.spacedBy(24.dp), contentPadding = PaddingValues(vertical = 14.dp, horizontal = 8.dp)) {
                    items(cards, key = { it.id }) { c ->
                        MediaCard(
                            client = api.client, video = c.id, title = c.name,
                            subtitle = c.series?.name?.takeIf { it.isNotBlank() } ?: c.durationSec?.let { clock(it) },
                            progress = c.progress,
                            onClick = { nav.openVideo(c.id) },
                            onLongClick = { nav.cardMenu(c.id, c.series?.id) },
                        )
                    }
                }
            }
        }
    }
}

fun collectionRows(rows: List<CollectionRow>): List<Pair<String, List<TvCard>>> = rows.map { it.collection.name to it.items }

@Composable
fun RecommendedTab(api: TvApi, library: String) {
    val nav = LocalTvNav.current
    val labels = listOf(
        stringResource(R.string.library_row_on_deck), stringResource(R.string.library_row_continue),
        stringResource(R.string.library_row_recently_added), stringResource(R.string.library_row_recently_watched),
    )
    val (state, retry) = rememberLoad(library, nav.signedOut, where = "tv hub") { api.hub(library) }
    LoadState(state, retry) { hub ->
        CardRows(
            api,
            listOf(
                labels[0] to hub.rows.onDeck, labels[1] to hub.rows.continueWatching,
                labels[2] to hub.rows.recentlyAdded, labels[3] to hub.rows.recentlyWatched,
            ) + collectionRows(hub.collectionRows),
            stringResource(R.string.library_empty_recommended),
        )
    }
}

/** The Library tab: Show (videos or series), Sort, Filter and the grid (stories 56.04, 56.05). */
@Composable
fun LibraryGridTab(api: TvApi, library: String, view: LibraryView) {
    val nav = LocalTvNav.current
    var filterOpen by remember { mutableStateOf(false) }
    Column(Modifier.fillMaxSize()) {
        Row(Modifier.padding(horizontal = Gutter), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
            MaktabaButton(onClick = { view.bySeries = !view.bySeries }, modifier = Modifier.testTag("library-show")) {
                Text(stringResource(R.string.library_show) + ": " + stringResource(if (view.bySeries) R.string.library_show_series else R.string.library_show_videos))
            }
            MaktabaButton(onClick = { view.sort = (view.sort + 1) % LibraryView.SORTS.size }, modifier = Modifier.testTag("library-sort")) {
                Text(stringResource(R.string.library_sort) + ": " + stringResource(LibraryView.SORTS[view.sort].label))
            }
            MaktabaButton(onClick = { filterOpen = !filterOpen }, modifier = Modifier.testTag("library-filter")) {
                Text(if (view.filters.isEmpty()) stringResource(R.string.library_filter) else stringResource(R.string.library_filter_count, view.filters.size))
            }
            if (view.filters.isNotEmpty()) {
                MaktabaButton(onClick = { view.filters.clear() }) { Text(stringResource(R.string.library_filter_clear)) }
            }
        }
        if (filterOpen) FilterPanel(api, library, view)
        val key = listOf(library, view.bySeries, view.query())
        if (view.bySeries) {
            PagedGrid(key, nav.signedOut, empty = stringResource(R.string.library_empty_filtered), page = { c -> api.libraryBySeries(library, view.query(), c).let { it.items to it.next } }) { card: GroupedCard ->
                MediaCard(
                    client = api.client, video = card.picture, title = card.label,
                    subtitle = if (card.kind == "series") stringResource(R.string.library_show_series) else card.durationSec?.let { clock(it) },
                    onClick = { if (card.kind == "series") nav.openSeries(card.id) else nav.openVideo(card.id) },
                )
            }
        } else {
            PagedGrid(key, nav.signedOut, empty = stringResource(R.string.library_empty_filtered), page = { c -> api.libraryVideos(library, view.query(), c).let { it.items to it.next } }) { v: TvVideo ->
                MediaCard(
                    client = api.client, video = v.id, title = v.displayTitle,
                    subtitle = v.durationSec?.let { clock(it) },
                    onClick = { nav.openVideo(v.id) },
                    onLongClick = { nav.cardMenu(v.id, null) },
                )
            }
        }
    }
}

/** Unwatched, in progress or watched, a language and a speaker, from the library's facets. */
@Composable
fun FilterPanel(api: TvApi, library: String, view: LibraryView) {
    val nav = LocalTvNav.current
    val (state, retry) = rememberLoad(library, nav.signedOut, where = "tv facets") { api.facets(library) }
    LoadState(state, retry) { f ->
        LazyRow(
            Modifier.padding(vertical = 8.dp),
            contentPadding = PaddingValues(horizontal = Gutter),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            val watched = listOf("unwatched" to R.string.library_filter_unwatched, "in_progress" to R.string.library_filter_in_progress, "watched" to R.string.library_filter_watched)
            items(watched, key = { "w:" + it.first }) { (value, label) ->
                Chip(stringResource(label), view.filters["watched"] == value) { view.toggle("watched", value) }
            }
            items(f.languages, key = { "l:" + it.code }) { l -> Chip(stringResource(R.string.library_filter_language) + ": " + l.code, view.filters["language"] == l.code) { view.toggle("language", l.code) } }
            items(f.speakers.take(30), key = { "s:" + it.name }) { s -> Chip(s.name, view.filters["speaker"] == s.name) { view.toggle("speaker", s.name) } }
        }
    }
}

private fun LibraryView.toggle(key: String, value: String) {
    if (filters[key] == value) filters.remove(key) else filters[key] = value
}

@Composable
fun Chip(label: String, on: Boolean, onClick: () -> Unit) {
    MaktabaButton(onClick = onClick) { Text((if (on) "✓ " else "") + label, maxLines = 1) }
}

/** A grid read a page at a time ([page] takes the cursor before; null first). */
@Composable
fun <T> PagedGrid(
    key: Any,
    signedOut: () -> Unit,
    empty: String,
    page: suspend (String?) -> Pair<List<T>, String?>,
    card: @Composable (T) -> Unit,
) {
    val items = remember(key) { mutableStateListOf<T>() }
    var next by remember(key) { mutableStateOf<String?>(null) }
    var done by remember(key) { mutableStateOf(false) }
    var loading by remember(key) { mutableStateOf(true) }
    var want by remember(key) { mutableIntStateOf(0) }
    LaunchedEffect(key, want) {
        if (done) return@LaunchedEffect
        loading = true
        try {
            val (got, n) = page(next)
            items.addAll(got)
            next = n
            done = n == null
        } catch (e: CancellationException) {
            throw e
        } catch (e: com.hamzalabs.maktaba.core.api.ApiException.SignedOut) {
            signedOut()
        } catch (e: Exception) {
            done = true
        } finally {
            loading = false
        }
    }
    if (items.isEmpty()) {
        Centered { Text(if (loading) stringResource(R.string.loading) else empty, style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text) }
        return
    }
    LazyVerticalGrid(
        columns = GridCells.Adaptive(240.dp),
        modifier = Modifier.fillMaxSize().testTag("library-grid"),
        contentPadding = PaddingValues(horizontal = Gutter, vertical = 20.dp),
        horizontalArrangement = Arrangement.spacedBy(28.dp),
        verticalArrangement = Arrangement.spacedBy(28.dp),
    ) {
        items(items.size) { i ->
            if (i >= items.size - 8 && !done && !loading) LaunchedEffect(items.size) { want++ }
            card(items[i])
        }
    }
}

/** The Collections tab: cards with their kind's badge, and Kind to keep one kind (story 56.08). */
@Composable
fun CollectionsTab(api: TvApi, library: String) {
    val nav = LocalTvNav.current
    val kinds = listOf(
        null to R.string.collections_kind_all, "youtube" to R.string.collections_kind_youtube, "plex" to R.string.collections_kind_plex,
        "curated" to R.string.collections_kind_curated, "topic" to R.string.collections_kind_topics, "filler" to R.string.collections_kind_clips,
    )
    var kind by remember(library) { mutableIntStateOf(0) }
    Column(Modifier.fillMaxSize()) {
        MaktabaButton(onClick = { kind = (kind + 1) % kinds.size }, modifier = Modifier.padding(horizontal = Gutter).testTag("collections-kind")) {
            Text(stringResource(R.string.collections_kind) + ": " + stringResource(kinds[kind].second))
        }
        val (state, retry) = rememberLoad(library to kind, nav.signedOut, where = "tv collections") { api.collections(library, kinds[kind].first).items }
        LoadState(state, retry) { cols ->
            if (cols.isEmpty()) {
                Centered { Text(stringResource(R.string.collections_empty), style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text) }
                return@LoadState
            }
            LazyVerticalGrid(
                columns = GridCells.Adaptive(240.dp),
                modifier = Modifier.fillMaxSize(),
                contentPadding = PaddingValues(horizontal = Gutter, vertical = 20.dp),
                horizontalArrangement = Arrangement.spacedBy(28.dp),
                verticalArrangement = Arrangement.spacedBy(28.dp),
            ) {
                items(cols, key = { it.kind + it.id }) { c ->
                    MediaCard(
                        client = api.client, video = c.posters.firstOrNull() ?: "", title = c.name,
                        subtitle = collectionBadge(c),
                        onClick = { nav.openCollection(library, c) },
                    )
                }
            }
        }
    }
}

/** A collection's badge: YouTube playlist or channel, Plex, Smart, Topics, Clips. */
@Composable
fun collectionBadge(c: TvCollection): String? = when {
    c.sourceKind == "youtube_playlist" -> stringResource(R.string.collections_badge_youtube_playlist)
    c.sourceKind == "youtube_channel" -> stringResource(R.string.collections_badge_youtube_channel)
    c.sourceKind == "plex" -> stringResource(R.string.collections_badge_plex)
    c.kind == "smart" -> stringResource(R.string.collections_badge_smart)
    c.kind == "topic" -> stringResource(R.string.collections_kind_topics)
    c.kind == "clip" -> stringResource(R.string.collections_kind_clips)
    else -> null
}

/** The Playlists tab: the user's lists with videos of this library (story 56.10). */
@Composable
fun PlaylistsTab(api: TvApi, library: String?) {
    val nav = LocalTvNav.current
    val (state, retry) = rememberLoad(library ?: "all", nav.signedOut, where = "tv playlists") { api.playlists(library).items }
    LoadState(state, retry) { lists ->
        if (lists.isEmpty()) {
            Centered {
                Text(
                    stringResource(if (library != null) R.string.playlists_empty_library else R.string.playlists_empty_title),
                    style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text,
                )
            }
            return@LoadState
        }
        LazyVerticalGrid(
            columns = GridCells.Adaptive(240.dp),
            modifier = Modifier.fillMaxSize(),
            contentPadding = PaddingValues(horizontal = Gutter, vertical = 20.dp),
            horizontalArrangement = Arrangement.spacedBy(28.dp),
            verticalArrangement = Arrangement.spacedBy(28.dp),
        ) {
            items(lists, key = { it.id }) { p: TvPlaylist ->
                MediaCard(
                    client = api.client, video = p.posters.firstOrNull() ?: "", title = p.name,
                    subtitle = if (p.smart) stringResource(R.string.playlists_smart_badge) else null,
                    onClick = { nav.openPlaylist(p.id) },
                )
            }
        }
    }
}

/** The Categories tab: Speakers, Topics, Languages, Years; a tile opens the Library tab filtered. */
@Composable
fun CategoriesTab(api: TvApi, library: String, onPick: (key: String, value: String) -> Unit) {
    val nav = LocalTvNav.current
    val (state, retry) = rememberLoad(library, nav.signedOut, where = "tv categories") { api.categories(library) }
    val names = listOf(
        stringResource(R.string.library_categories_speakers), stringResource(R.string.library_categories_topics),
        stringResource(R.string.library_categories_languages), stringResource(R.string.library_categories_years),
    )
    LoadState(state, retry) { c ->
        val rows: List<Triple<String, String, List<TvCategory>>> = listOf(
            Triple(names[0], "speaker", c.speakers), Triple(names[1], "tag", c.topics),
            Triple(names[2], "language", c.languages), Triple(names[3], "year", c.years),
        ).filter { it.third.isNotEmpty() }
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(start = Gutter, end = Gutter, bottom = 48.dp), verticalArrangement = Arrangement.spacedBy(20.dp)) {
            items(rows, key = { it.second }) { (title, key, tiles) ->
                Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    SectionTitle(title)
                    LazyRow(horizontalArrangement = Arrangement.spacedBy(20.dp), contentPadding = PaddingValues(vertical = 12.dp, horizontal = 8.dp)) {
                        items(tiles, key = { it.value }) { t ->
                            Card(
                                onClick = { onPick(key, t.value) },
                                modifier = Modifier.width(220.dp).height(90.dp).testTag("category-$key-${t.value}"),
                                shape = CardDefaults.shape(CardCorner),
                                colors = maktabaCardColors(),
                                scale = CardDefaults.scale(focusedScale = 1.08f),
                                border = goldFocusBorder(),
                            ) {
                                Column(Modifier.fillMaxSize().padding(14.dp), verticalArrangement = Arrangement.Center) {
                                    Text(t.label, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                    Spacer(Modifier.height(4.dp))
                                    Text(t.count.toString(), style = MaterialTheme.typography.bodyMedium, color = MaktabaPalette.Muted)
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}
