package com.hamzalabs.maktaba.player.libraries

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.PushPin
import androidx.compose.material.icons.outlined.PushPin
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.PrimaryTabRow
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.VerticalDivider
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.R as CoreR
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.LibrarySummary
import com.hamzalabs.maktaba.core.api.SeriesItem
import com.hamzalabs.maktaba.core.layout.HingeSplit
import com.hamzalabs.maktaba.core.layout.ListPaneWidth
import com.hamzalabs.maktaba.core.layout.Panes
import com.hamzalabs.maktaba.core.layout.currentPanes
import com.hamzalabs.maktaba.core.ui.AccountMenu
import com.hamzalabs.maktaba.core.ui.ErrorState
import com.hamzalabs.maktaba.core.ui.MaktabaTokens
import com.hamzalabs.maktaba.core.ui.Message
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.OnReselect
import com.hamzalabs.maktaba.player.shell.PlayerNavigator
import com.hamzalabs.maktaba.player.shell.Route
import com.hamzalabs.maktaba.player.ui.components.HomePicture
import com.hamzalabs.maktaba.player.ui.components.MissingPosterMark
import com.hamzalabs.maktaba.player.ui.components.POSTER_ASPECT
import com.hamzalabs.maktaba.player.ui.components.PosterCard
import com.hamzalabs.maktaba.player.ui.components.PosterSize
import com.hamzalabs.maktaba.player.ui.components.posterUrl
import kotlinx.coroutines.launch

// The Libraries section (story 48.27, FR-14 to FR-17): the libraries and every series, a library's
// page, a series' page. The layout follows the window (FR-8, core/layout/Panes.kt):
// - one pane (a phone, a foldable folded): the grid; a library or series is pushed on the stack;
// - 600 dp and wider: the list beside the chosen library's or series' page;
// - half folded: split at the hinge, the page on the first half (looked at), the list on the
//   second (touched).

/** The layout the section uses; null reads the window (tests give a hinge the JVM cannot fold). */
internal val LocalLibrariesPanes = staticCompositionLocalOf<Panes?> { null }

/** The Libraries section's root (FR-14, FR-16). Re-selecting it returns the list to its first page. */
@Composable
fun LibrariesScreen(nav: PlayerNavigator) {
    val home = LocalPlayerHome.current
    val state = home.keep("libraries.root") { LibrariesState(home.client, home.session::signedOut) }
    val panes = LocalLibrariesPanes.current ?: currentPanes()
    LaunchedEffect(state) { state.loadLibraries() }
    LaunchedEffect(state, state.tab) { if (state.tab == RootTab.Series) state.loadSeries() }
    OnReselect(nav) { state.chosen = null }

    VideoHost(nav) {
        when (panes) {
            Panes.One -> RootList(state, home.client, grid = true, chosen = null, onOpen = { c ->
                when (c) {
                    is Chosen.Library -> nav.open(Route.Library(c.id))
                    is Chosen.Series -> nav.open(Route.Series(c.id))
                }
            })
            Panes.ListDetail -> Row(Modifier.fillMaxSize()) {
                RootList(state, home.client, grid = false, chosen = state.shown, onOpen = { state.chosen = it }, Modifier.width(ListPaneWidth).fillMaxHeight())
                VerticalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                Box(Modifier.weight(1f).fillMaxHeight().testTag("libraries-detail")) { Detail(state, nav) }
            }
            is Panes.Tabletop, is Panes.Book -> HingeSplit(
                panes,
                first = { Box(Modifier.fillMaxSize().testTag("libraries-detail")) { Detail(state, nav) } },
                second = { RootList(state, home.client, grid = false, chosen = state.shown, onOpen = { state.chosen = it }) },
            )
        }
    }
}

/** A library's page, pushed on the stack (FR-15). */
@Composable
fun LibraryScreen(id: String, nav: PlayerNavigator) = VideoHost(nav) { LibraryPage(id, nav, embedded = false) }

/** A series' page, pushed on the stack (FR-17). */
@Composable
fun SeriesScreen(id: String, nav: PlayerNavigator) = VideoHost(nav) { SeriesPage(id, nav, embedded = false) }

/** What the page beside the list shows: the chosen one, else the shown tab's first. */
private val LibrariesState.shown: Chosen?
    get() = chosen ?: when (tab) {
        RootTab.Libraries -> libraries.firstOrNull()?.let { Chosen.Library(it.id) }
        RootTab.Series -> series.firstOrNull()?.let { Chosen.Series(it.id) }
    }

@Composable
private fun Detail(state: LibrariesState, nav: PlayerNavigator) {
    when (val c = state.shown) {
        is Chosen.Library -> LibraryPage(c.id, nav, embedded = true, initialName = state.libraries.firstOrNull { it.id == c.id }?.name)
        is Chosen.Series -> SeriesPage(c.id, nav, embedded = true)
        null -> Message(stringResource(R.string.choose_library))
    }
}

/** The tabs Libraries and Series, over the libraries' cards or every series' cards. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun RootList(
    state: LibrariesState,
    client: ApiClient,
    grid: Boolean,
    chosen: Chosen?,
    onOpen: (Chosen) -> Unit,
    modifier: Modifier = Modifier,
) {
    val home = LocalPlayerHome.current
    val scope = rememberCoroutineScope()
    var refreshing by remember { mutableStateOf(false) }
    Scaffold(
        modifier = modifier.testTag("libraries-list"),
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.libraries)) },
                actions = { AccountMenu(home.session, home.user, home.themes) },
            )
        },
    ) { padding ->
        Column(Modifier.padding(padding).fillMaxSize()) {
            PrimaryTabRow(selectedTabIndex = state.tab.ordinal) {
                Tab(state.tab == RootTab.Libraries, onClick = { state.show(RootTab.Libraries) }, modifier = Modifier.testTag("tab-libraries"), text = { Text(stringResource(R.string.libraries)) })
                Tab(state.tab == RootTab.Series, onClick = { state.show(RootTab.Series) }, modifier = Modifier.testTag("tab-series"), text = { Text(stringResource(R.string.libraries_tab_series)) })
            }
            PullToRefreshBox(
                isRefreshing = refreshing,
                onRefresh = {
                    scope.launch {
                        refreshing = true
                        if (state.tab == RootTab.Libraries) state.loadLibraries() else state.loadSeries()
                        refreshing = false
                    }
                },
                modifier = Modifier.fillMaxSize(),
            ) {
                when (state.tab) {
                    RootTab.Libraries -> LibraryCards(state, client, grid, chosen, onOpen)
                    RootTab.Series -> SeriesCards(state, client, grid, chosen, onOpen)
                }
            }
        }
    }
}

private val gridCells = GridCells.Adaptive(170.dp)
private val gridPadding = PaddingValues(MaktabaTokens.Spacing.s4)
private val gridGap = Arrangement.spacedBy(MaktabaTokens.Spacing.s3)

@Composable
private fun LibraryCards(state: LibrariesState, client: ApiClient, grid: Boolean, chosen: Chosen?, onOpen: (Chosen) -> Unit) {
    val scope = rememberCoroutineScope()
    val problem = state.problem
    when {
        problem != null && state.libraries.isEmpty() -> ErrorState(
            problem,
            title = stringResource(CoreR.string.error_title),
            onRetry = { scope.launch { state.loadLibraries() } },
            onChooseHome = LocalPlayerHome.current.session::switchHome,
        )
        !state.librariesLoaded -> Spinner()
        state.libraries.isEmpty() -> Message(stringResource(R.string.libraries_empty))
        else -> LazyVerticalGrid(
            gridCells,
            Modifier.fillMaxSize().testTag("libraries-grid"),
            state = rememberLazyGridState(),
            contentPadding = gridPadding,
            verticalArrangement = gridGap,
            horizontalArrangement = gridGap,
        ) {
            items(state.libraries, key = { it.id }) { lib ->
                LibraryCard(
                    lib, client,
                    chosen = chosen == Chosen.Library(lib.id),
                    onClick = { onOpen(Chosen.Library(lib.id)) },
                    onPin = { scope.launch { state.togglePin(lib.id) } },
                )
            }
        }
    }
}

/** A library's card (FR-14): its art (a poster of its videos), name, count, and Pin. */
@Composable
private fun LibraryCard(lib: LibrarySummary, client: ApiClient, chosen: Boolean, onClick: () -> Unit, onPin: () -> Unit) {
    val shape = RoundedCornerShape(MaktabaTokens.Radius.md)
    val count = pluralStringResource(R.plurals.libraries_videos_count, lib.videos.toInt(), lib.videos.toInt())
    Column(
        Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(if (chosen) MaterialTheme.colorScheme.secondaryContainer else MaterialTheme.colorScheme.surfaceContainer)
            // one control for TalkBack ("Films, 5 videos"); Pin stays its own
            .clickable(role = Role.Button, onClick = onClick)
            .semantics { selected = chosen }
            .testTag("library-${lib.id}"),
    ) {
        Box(Modifier.fillMaxWidth().aspectRatio(POSTER_ASPECT)) {
            HomePicture(lib.posterVideoIds.firstOrNull()?.let { posterUrl(client, it, PosterSize.Small) }, Modifier.fillMaxSize()) {
                MissingPosterMark()
            }
        }
        Row(Modifier.fillMaxWidth().padding(start = MaktabaTokens.Spacing.s3), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f).padding(vertical = MaktabaTokens.Spacing.s2)) {
                Text(lib.name, style = MaterialTheme.typography.titleSmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
                val meta = if (lib.pinned) "$count · ${stringResource(R.string.libraries_pinned)}" else count
                Text(meta, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            val label = stringResource(if (lib.pinned) R.string.libraries_unpin_named else R.string.libraries_pin_named, lib.name)
            IconButton(onClick = onPin, modifier = Modifier.testTag("pin-${lib.id}")) {
                Icon(
                    if (lib.pinned) Icons.Filled.PushPin else Icons.Outlined.PushPin,
                    contentDescription = label,
                    tint = if (lib.pinned) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

@Composable
private fun SeriesCards(state: LibrariesState, client: ApiClient, grid: Boolean, chosen: Chosen?, onOpen: (Chosen) -> Unit) {
    val scope = rememberCoroutineScope()
    val problem = state.seriesProblem
    when {
        problem != null && state.series.isEmpty() -> ErrorState(
            problem,
            title = stringResource(CoreR.string.error_title),
            onRetry = { scope.launch { state.loadSeries() } },
            onChooseHome = LocalPlayerHome.current.session::switchHome,
        )
        !state.seriesLoaded -> Spinner()
        state.series.isEmpty() -> Message(stringResource(R.string.libraries_series_empty))
        else -> {
            val gridState = rememberLazyGridState()
            // the next page about a screen before the end, so scrolling doesn't stop at a page's edge
            val nearEnd by remember(gridState) {
                derivedStateOf {
                    val last = gridState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: 0
                    last >= gridState.layoutInfo.totalItemsCount - SERIES_AHEAD
                }
            }
            LaunchedEffect(nearEnd, state.series.size) { if (nearEnd && state.moreSeries) state.loadMoreSeries() }
            LazyVerticalGrid(
            gridCells,
            Modifier.fillMaxSize().testTag("series-grid"),
            state = gridState,
            contentPadding = gridPadding,
            verticalArrangement = gridGap,
            horizontalArrangement = gridGap,
        ) {
            items(state.series, key = { it.id }) { s ->
                Box(Modifier.testTag("series-${s.id}")) {
                    SeriesCard(s, client, chosen = chosen == Chosen.Series(s.id)) { onOpen(Chosen.Series(s.id)) }
                }
            }
            }
        }
    }
}

/** How many cards before the end the Series grid asks for its next page. */
private const val SERIES_AHEAD = 18

/** A series' card (FR-16): its poster, name and the caller's progress through it. */
@Composable
internal fun SeriesCard(series: SeriesItem, client: ApiClient, chosen: Boolean = false, onClick: () -> Unit) {
    SeriesCard(
        name = series.name,
        posterVideoId = series.posterVideoId,
        episodes = series.episodeCount,
        watched = series.watchedCount,
        client = client,
        chosen = chosen,
        onClick = onClick,
    )
}

@Composable
internal fun SeriesCard(name: String, posterVideoId: String?, episodes: Int, watched: Int, client: ApiClient, chosen: Boolean = false, onClick: () -> Unit) {
    val progress = if (episodes > 0) (watched.toFloat() / episodes).coerceIn(0f, 1f) else 0f
    PosterCard(
        title = name.ifBlank { stringResource(R.string.libraries_series_untitled) },
        posterUrl = posterVideoId?.let { posterUrl(client, it, PosterSize.Small) },
        onClick = onClick,
        modifier = Modifier
            .fillMaxWidth()
            .then(if (chosen) Modifier.background(MaterialTheme.colorScheme.secondaryContainer, RoundedCornerShape(MaktabaTokens.Radius.md)) else Modifier),
        subtitle = if (watched > 0) {
            stringResource(R.string.libraries_series_progress, watched, episodes)
        } else {
            pluralStringResource(R.plurals.libraries_episodes, episodes, episodes)
        },
        progress = progress.takeIf { it > 0f && it < 1f },
        watched = episodes > 0 && watched >= episodes,
    )
}

@Composable
internal fun Spinner() = Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }

/** A library or series that is gone, or not shared with this person (a 404, the same answer). */
@Composable
internal fun NotAvailable() = Message(stringResource(R.string.libraries_not_available), detail = stringResource(R.string.libraries_not_available_detail))
