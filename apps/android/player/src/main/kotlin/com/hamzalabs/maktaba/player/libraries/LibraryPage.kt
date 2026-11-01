package com.hamzalabs.maktaba.player.libraries

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyGridScope
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.itemsIndexed
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowDropDown
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.PrimaryTabRow
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.R as CoreR
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.api.VideoCard
import com.hamzalabs.maktaba.core.api.VideoDetail
import com.hamzalabs.maktaba.core.api.watchedFraction
import com.hamzalabs.maktaba.core.ui.ErrorState
import com.hamzalabs.maktaba.core.ui.MaktabaTokens
import com.hamzalabs.maktaba.core.ui.Message
import com.hamzalabs.maktaba.core.ui.formatDuration
import com.hamzalabs.maktaba.core.ui.sentence
import com.hamzalabs.maktaba.player.BackButton
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerNavigator
import com.hamzalabs.maktaba.player.shell.Route
import com.hamzalabs.maktaba.player.ui.components.PosterCard
import com.hamzalabs.maktaba.player.ui.components.PosterSize
import com.hamzalabs.maktaba.player.ui.components.Rail
import com.hamzalabs.maktaba.player.ui.components.posterUrl
import kotlinx.coroutines.launch
import java.util.Locale

/**
 * A library's page (FR-15): its hub rows (On Deck, Continue Watching, Recently Added, Recently
 * Watched; an empty row is left out), then the tabs Videos and Series. Videos is every video,
 * newest first or A to Z, narrowed by language and speaker, paged by cursor as the grid scrolls;
 * Series is the library's series, by offset. [embedded] beside the list (no Back of its own).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun LibraryPage(id: String, nav: PlayerNavigator, embedded: Boolean, initialName: String? = null) {
    val home = LocalPlayerHome.current
    val state = home.keep("libraries.library.$id") { LibraryState(home.client, id, home.session::signedOut) }
    if (state.name.isBlank() && !initialName.isNullOrBlank()) state.name = initialName
    val scope = rememberCoroutineScope()
    var refreshing by remember { mutableStateOf(false) }

    LaunchedEffect(state) { state.loadHub() }
    LaunchedEffect(state, state.sort, state.language, state.speaker) { state.reload() }
    LaunchedEffect(state, state.tab) { if (state.tab == LibraryTab.Series && !state.seriesLoaded) state.loadSeries() }

    Scaffold(
        modifier = Modifier.testTag("library-page"),
        topBar = {
            TopAppBar(
                title = { Text(state.name, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                navigationIcon = { if (!embedded && nav.canGoBack) BackButton { nav.back() } },
            )
        },
    ) { padding ->
        Box(Modifier.padding(padding).fillMaxSize()) {
            if (state.notAvailable && state.videos.isEmpty()) {
                NotAvailable()
                return@Box
            }
            PullToRefreshBox(
                isRefreshing = refreshing,
                onRefresh = {
                    scope.launch {
                        refreshing = true
                        state.loadHub()
                        state.reload()
                        refreshing = false
                    }
                },
                modifier = Modifier.fillMaxSize(),
            ) {
                LazyVerticalGrid(
                    gridCells,
                    Modifier.fillMaxSize().testTag("library-grid"),
                    state = rememberLazyGridState(),
                    contentPadding = PaddingValues(bottom = MaktabaTokens.Spacing.s6),
                    verticalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s3),
                    horizontalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s3),
                ) {
                    hubRows(state, home.client)
                    full("tabs") {
                        PrimaryTabRow(selectedTabIndex = state.tab.ordinal) {
                            Tab(state.tab == LibraryTab.Videos, onClick = { state.tab = LibraryTab.Videos }, modifier = Modifier.testTag("library-tab-videos"), text = { Text(stringResource(R.string.library_tab_videos)) })
                            Tab(state.tab == LibraryTab.Series, onClick = { state.tab = LibraryTab.Series }, modifier = Modifier.testTag("library-tab-series"), text = { Text(stringResource(R.string.libraries_tab_series)) })
                        }
                    }
                    when (state.tab) {
                        LibraryTab.Videos -> videos(state, home.client, onRetry = { scope.launch { state.reload() } }, onMore = { scope.launch { state.loadMore() } })
                        LibraryTab.Series -> series(state, home.client, nav, onMore = { scope.launch { state.loadSeries() } })
                    }
                }
            }
        }
    }
}

private val gridCells = GridCells.Adaptive(170.dp)
private val sidePadding = MaktabaTokens.Spacing.s4

/** An item as wide as the grid. */
private fun LazyGridScope.full(key: String, content: @Composable () -> Unit) =
    item(key = key, span = { GridItemSpan(maxLineSpan) }, contentType = key) { content() }

private fun LazyGridScope.hubRows(state: LibraryState, client: ApiClient) {
    val rows = state.rows
    listOf(
        "on-deck" to rows.onDeck to R.string.library_row_on_deck,
        "continue" to rows.continueWatching to R.string.library_row_continue,
        "recently-added" to rows.recentlyAdded to R.string.library_row_recently_added,
        "recently-watched" to rows.recentlyWatched to R.string.library_row_recently_watched,
    ).forEach { (keyed, title) ->
        val (key, cards) = keyed
        if (cards.isNotEmpty()) full("row-$key") { HubRow(stringResource(title), cards, client, Modifier.testTag("row-$key")) }
    }
}

@Composable
private fun HubRow(title: String, cards: List<VideoCard>, client: ApiClient, modifier: Modifier) {
    val open = LocalOpenVideo.current
    Rail(title, modifier) {
        items(cards, key = { it.id }) { card -> PosterCard(card, client, onClick = { open(card.id) }) }
    }
}

private fun LazyGridScope.videos(state: LibraryState, client: ApiClient, onRetry: () -> Unit, onMore: () -> Unit) {
    full("filters") { Filters(state) }
    val problem = state.problem
    when {
        problem != null && state.videos.isEmpty() -> full("problem") {
            ErrorState(problem, title = stringResource(CoreR.string.error_title), onRetry = onRetry)
        }
        !state.loaded -> full("loading") { Box(Modifier.fillMaxWidth().padding(sidePadding), contentAlignment = Alignment.Center) { CircularProgressIndicator() } }
        state.videos.isEmpty() -> full("empty") {
            val filtered = state.language != null || state.speaker != null
            Message(stringResource(if (filtered) R.string.library_no_match else R.string.library_no_videos), Modifier.testTag("library-empty"))
        }
        else -> {
            itemsIndexed(state.videos, key = { _, v -> "v-${v.id}" }) { i, v ->
                Box(Modifier.padding(horizontal = sidePadding)) { VideoTile(v, client) }
                if (i == state.videos.lastIndex && state.next != null) LaunchedEffect(state.next) { onMore() }
            }
            if (problem != null) full("more-failed") { MoreFailed(problem, onRetry = onMore) }
        }
    }
}

/** A video of the grid: its poster, title, length and the caller's progress. */
@Composable
private fun VideoTile(v: VideoDetail, client: ApiClient) {
    val open = LocalOpenVideo.current
    val completed = v.playbackState?.completed == true
    PosterCard(
        title = v.displayTitle,
        posterUrl = posterUrl(client, v.id, PosterSize.Small),
        onClick = { open(v.id) },
        modifier = Modifier.fillMaxWidth().testTag("video-${v.id}"),
        subtitle = v.durationSec?.let { formatDuration(it) },
        progress = watchedFraction(v.playbackState?.positionSec, v.durationSec, completed)?.toFloat(),
        watched = completed,
    )
}

/** Sort, language and speaker (the facets of `GET /api/videos/facets`); a facet with nothing to choose is left out. */
@Composable
private fun Filters(state: LibraryState) {
    val locale = LocalConfiguration.current.locales[0]
    Row(
        Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = sidePadding),
        horizontalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s2),
    ) {
        Choice(
            label = stringResource(R.string.library_sort),
            value = stringResource(state.sort.label),
            selected = false,
            options = LibrarySort.entries.map { stringResource(it.label) to it },
            tag = "filter-sort",
        ) { state.sort = it ?: LibrarySort.Newest }
        if (state.facets.languages.isNotEmpty()) {
            Choice(
                label = stringResource(R.string.library_language),
                value = state.language?.let { languageName(it, locale) },
                selected = state.language != null,
                options = listOf(stringResource(R.string.library_any_language) to null) +
                    state.facets.languages.map { languageName(it.code, locale) to it.code },
                tag = "filter-language",
            ) { state.language = it }
        }
        if (state.facets.speakers.isNotEmpty()) {
            Choice(
                label = stringResource(R.string.library_speaker),
                value = state.speaker,
                selected = state.speaker != null,
                options = listOf(stringResource(R.string.library_any_speaker) to null) + state.facets.speakers.map { it.name to it.name },
                tag = "filter-speaker",
            ) { state.speaker = it }
        }
    }
}

private val LibrarySort.label: Int
    get() = when (this) {
        LibrarySort.Newest -> R.string.library_sort_newest
        LibrarySort.Title -> R.string.library_sort_title
    }

/** A language's name in the app's language ("ar" → "Arabic", «العربية»); the code when unknown. */
internal fun languageName(code: String, locale: Locale): String =
    Locale.forLanguageTag(code).getDisplayLanguage(locale).takeIf { it.isNotBlank() && it != code } ?: code

/** A chip that opens a menu of [options]; its words are "label: value". */
@Composable
private fun <T> Choice(label: String, value: String?, selected: Boolean, options: List<Pair<String, T>>, tag: String, onChoose: (T) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Box {
        FilterChip(
            selected = selected,
            onClick = { open = true },
            label = { Text(if (value != null) "$label: $value" else label) },
            trailingIcon = { Icon(Icons.Filled.ArrowDropDown, null) },
            modifier = Modifier.testTag(tag),
        )
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            options.forEach { (text, v) ->
                DropdownMenuItem(text = { Text(text) }, onClick = { open = false; onChoose(v) })
            }
        }
    }
}

private fun LazyGridScope.series(state: LibraryState, client: ApiClient, nav: PlayerNavigator, onMore: () -> Unit) {
    when {
        !state.seriesLoaded -> full("series-loading") { Box(Modifier.fillMaxWidth().padding(sidePadding), contentAlignment = Alignment.Center) { CircularProgressIndicator() } }
        state.series.isEmpty() -> full("series-empty") { Message(stringResource(R.string.libraries_series_empty)) }
        else -> itemsIndexed(state.series, key = { _, s -> "s-${s.id}" }) { i, s ->
            Box(Modifier.padding(horizontal = sidePadding).testTag("series-${s.id}")) {
                SeriesCard(s.name, s.posterVideoId, s.episodes.toInt(), s.watched.toInt(), client) { nav.open(Route.Series(s.id)) }
            }
            if (i == state.series.lastIndex && state.moreSeries) LaunchedEffect(state.series.size) { onMore() }
        }
    }
}

/** The next page failed: say so where the grid ends, and ask again on a tap. */
@Composable
private fun MoreFailed(problem: Failure, onRetry: () -> Unit) {
    Row(Modifier.fillMaxWidth().padding(horizontal = sidePadding), verticalAlignment = Alignment.CenterVertically) {
        Text(
            stringResource(problem.kind.sentence()),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.error,
            modifier = Modifier.weight(1f),
        )
        TextButton(onClick = onRetry) { Text(stringResource(CoreR.string.try_again)) }
    }
}
