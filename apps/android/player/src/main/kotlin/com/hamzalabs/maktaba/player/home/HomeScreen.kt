package com.hamzalabs.maktaba.player.home

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.asPaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBars
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.VideoLibrary
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.Featured
import com.hamzalabs.maktaba.core.api.LiveApi
import com.hamzalabs.maktaba.core.api.NowEntry
import com.hamzalabs.maktaba.core.ui.ErrorState
import com.hamzalabs.maktaba.core.ui.MaktabaTokens
import com.hamzalabs.maktaba.core.ui.formatDuration
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.OnReselect
import com.hamzalabs.maktaba.player.shell.PlayerNavigator
import com.hamzalabs.maktaba.player.shell.Route
import com.hamzalabs.maktaba.player.ui.components.Hero
import com.hamzalabs.maktaba.player.ui.components.HeroPlaceholder
import com.hamzalabs.maktaba.player.ui.components.Loading
import com.hamzalabs.maktaba.player.ui.components.PosterCard
import com.hamzalabs.maktaba.player.ui.components.PosterCardWidth
import com.hamzalabs.maktaba.player.ui.components.PosterSize
import com.hamzalabs.maktaba.player.ui.components.Rail
import com.hamzalabs.maktaba.player.ui.components.RailPlaceholder
import com.hamzalabs.maktaba.player.ui.components.posterUrl
import kotlinx.coroutines.launch
import com.hamzalabs.maktaba.core.R as CoreR

/**
 * Home (FR-10 to FR-13): the hero, then the rows in FR-10's order (an empty one is absent).
 * A card opens its video's page (a series group its series); a long press opens its menu:
 * Resume on a Continue Watching card (the player at the person's position), Not interested on
 * a recommendation. Pull to refresh reads Home again; what was read last shows at once, on a
 * new launch too (FR-12). A home with nothing the person may watch says so, with Switch home
 * and Sign out (FR-13).
 */
@Composable
fun HomeScreen(nav: PlayerNavigator) {
    val home = LocalPlayerHome.current
    val context = LocalContext.current
    val state = home.keep("home") {
        val live = LiveApi(home.client)
        HomeState(
            home.client,
            HomeCache.of(context.cacheDir, home.client.home, home.user.id),
            liveNow = { live.now() },
            signedOut = home.session::signedOut,
        )
    }
    LaunchedEffect(state) { state.open() }
    HomeContent(
        state,
        home.client,
        nav,
        admin = home.user.isAdmin,
        onSwitchHome = home.session::switchHome,
        onSignOut = home.session::signOut,
    )
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun HomeContent(
    state: HomeState,
    client: ApiClient,
    nav: PlayerNavigator,
    admin: Boolean,
    onSwitchHome: () -> Unit,
    onSignOut: () -> Unit,
) {
    val scope = rememberCoroutineScope()
    val list = rememberLazyListState()
    val snackbar = remember { SnackbarHostState() }
    val failedText = stringResource(R.string.home_not_interested_failed)
    OnReselect(nav) { scope.launch { list.animateScrollToItem(0) } }

    // pull to refresh shows its spinner; the quiet read each time Home opens does not (FR-12)
    var pulling by remember { mutableStateOf(false) }
    val rails = state.rails
    val problem = state.problem
    Box(Modifier.fillMaxSize().testTag("home")) {
        PullToRefreshBox(
            isRefreshing = pulling,
            onRefresh = {
                pulling = true
                scope.launch {
                    try {
                        state.load()
                    } finally {
                        pulling = false
                    }
                }
            },
            modifier = Modifier.fillMaxSize(),
        ) {
            when {
                rails == null && problem != null -> ErrorState(
                    problem,
                    stringResource(R.string.home_error_title),
                    onRetry = { scope.launch { state.load() } },
                    onChooseHome = onSwitchHome,
                )
                rails == null -> Loading(true, Modifier.fillMaxSize()) {
                    Column(Modifier.padding(WindowInsets.statusBars.asPaddingValues())) {
                        HeroPlaceholder()
                        RailPlaceholder()
                        RailPlaceholder()
                    }
                }
                state.empty -> EmptyHome(admin, onSwitchHome, onSignOut)
                else -> BoxWithConstraints(Modifier.fillMaxSize()) {
                    val cardWidth = cardWidthFor(maxWidth)
                    LazyColumn(
                        Modifier.fillMaxSize().testTag("home-rows"),
                        state = list,
                        contentPadding = PaddingValues(
                            top = WindowInsets.statusBars.asPaddingValues().calculateTopPadding(),
                            bottom = MaktabaTokens.Spacing.s6,
                        ),
                        verticalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s4),
                    ) {
                        if (state.stale) {
                            item(key = "stale") {
                                Text(
                                    stringResource(R.string.home_stale),
                                    Modifier.fillMaxWidth().padding(horizontal = MaktabaTokens.Spacing.s4).testTag("home-stale"),
                                    style = MaterialTheme.typography.bodyMedium,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                        }
                        rails.featured?.let { f -> item(key = "hero") { HomeHero(f, client, nav) } }
                        items(state.rows, key = { it.key }) { row ->
                            when (row) {
                                is HomeRow.Cards -> CardsRow(row, client, cardWidth, nav, state) { id ->
                                    scope.launch { if (!state.dismiss(id)) snackbar.showSnackbar(failedText) }
                                }
                                is HomeRow.Live -> LiveRow(row.channels, client, cardWidth, nav)
                            }
                        }
                    }
                }
            }
        }
        SnackbarHost(snackbar, Modifier.align(Alignment.BottomCenter))
    }
}

/** A card's width for the window: wider on a foldable open flat and a tablet, so a row shows more of each poster. */
internal fun cardWidthFor(width: Dp): Dp = when {
    width >= 840.dp -> 224.dp
    width >= 600.dp -> 196.dp
    else -> PosterCardWidth
}

/** Home's hero: the featured series (from its first lesson) or the lesson an admin picked. */
@Composable
private fun HomeHero(featured: Featured, client: ApiClient, nav: PlayerNavigator) {
    val video = featured.video
    val series = featured.series
    val isSeries = featured.kind == "series" && series != null
    val title = if (isSeries) series!!.name.ifBlank { stringResource(R.string.home_series_unnamed) } else video.displayTitle
    val meta = when {
        isSeries && series!!.episodes > 0 -> pluralStringResource(R.plurals.home_hero_lessons, series.episodes.toInt(), series.episodes.toInt())
        else -> listOfNotNull(
            series?.name?.takeIf { it.isNotBlank() },
            video.durationSec?.takeIf { it > 0 }?.let(::formatDuration),
        ).joinToString(" · ").ifEmpty { null }
    }
    val fraction = video.watchedFraction?.toFloat()
    Hero(
        title = title,
        backdropUrl = posterUrl(client, video.id, PosterSize.Full),
        onPlay = { nav.open(Route.Watch(video.id)) },
        onDetails = { nav.open(if (isSeries) Route.Series(series!!.id) else Route.Video(video.id)) },
        eyebrow = stringResource(if (featured.picked) R.string.home_hero_picked else R.string.home_hero_new_series),
        meta = meta,
        resume = fraction != null,
        progress = fraction,
    )
}

@Composable
private fun RowTitle.text(): String = when (this) {
    is RowTitle.Named -> name
    RowTitle.Own.ContinueWatching -> stringResource(R.string.home_row_continue_watching)
    RowTitle.Own.OnDeck -> stringResource(R.string.home_row_on_deck)
    RowTitle.Own.LiveNow -> stringResource(R.string.home_row_live_now)
    RowTitle.Own.Favorites -> stringResource(R.string.home_row_favorites)
    RowTitle.Own.RecentlyAdded -> stringResource(R.string.home_row_recently_added)
    RowTitle.Own.RecentlyWatched -> stringResource(R.string.home_row_recently_watched)
    RowTitle.Own.ForYou -> stringResource(R.string.home_row_for_you)
    RowTitle.Own.FromLibrary -> stringResource(R.string.home_row_from_library)
}

@Composable
private fun CardsRow(row: HomeRow.Cards, client: ApiClient, cardWidth: Dp, nav: PlayerNavigator, state: HomeState, onNotInterested: (String) -> Unit) {
    Rail(
        row.title.text(),
        Modifier.testTag("row-${row.key}"),
        onSeeAll = row.seeAll?.let { route -> { nav.open(route) } },
    ) {
        items(row.cards, key = { it.key }) { card ->
            HomeCardView(card, client, cardWidth, nav, state, onNotInterested)
        }
    }
}

/** What a card's menu offers. */
private enum class CardAction { Resume, NotInterested }

@Composable
private fun HomeCardView(card: HomeCard, client: ApiClient, width: Dp, nav: PlayerNavigator, state: HomeState, onNotInterested: (String) -> Unit) {
    if (card.title == null && card.newLessons == null) LaunchedEffect(card.videoId) { state.resolve(card.videoId) }
    val actions = buildList {
        if (card.resumeAtSec != null) add(CardAction.Resume)
        if (card.recommended) add(CardAction.NotInterested)
    }
    var menu by remember { mutableStateOf(false) }
    val title = card.title
        ?: if (card.newLessons != null) stringResource(R.string.home_series_unnamed) else stringResource(R.string.components_loading)
    val subtitle = card.newLessons?.let { pluralStringResource(R.plurals.home_new_lessons, it, it) } ?: card.subtitle
    Box {
        PosterCard(
            title = title,
            posterUrl = posterUrl(client, card.posterVideo, PosterSize.Small),
            onClick = { nav.open(card.open) },
            modifier = Modifier.testTag("card-${card.key}"),
            subtitle = subtitle,
            progress = card.progress,
            watched = card.watched,
            width = width,
            onLongClick = if (actions.isEmpty()) null else ({ menu = true }),
            longClickLabel = if (actions.isEmpty()) null else stringResource(R.string.home_more_options),
        )
        DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
            actions.forEach { action ->
                when (action) {
                    CardAction.Resume -> DropdownMenuItem(
                        text = { Text(stringResource(R.string.components_resume)) },
                        onClick = {
                            menu = false
                            nav.open(Route.Watch(card.videoId, card.resumeAtSec))
                        },
                        modifier = Modifier.testTag("menu-resume"),
                    )
                    CardAction.NotInterested -> DropdownMenuItem(
                        text = { Text(stringResource(R.string.home_not_interested)) },
                        onClick = {
                            menu = false
                            onNotInterested(card.videoId)
                        },
                        modifier = Modifier.testTag("menu-not-interested"),
                    )
                }
            }
        }
    }
}

/** Live TV on now: each channel's programme, how far through it is, and the channel; a tap tunes in. */
@Composable
private fun LiveRow(channels: List<NowEntry>, client: ApiClient, cardWidth: Dp, nav: PlayerNavigator) {
    val noContent = stringResource(R.string.home_no_content)
    Rail(stringResource(R.string.home_row_live_now), Modifier.testTag("row-$KEY_LIVE")) {
        items(channels, key = { it.channel.id }) { e ->
            val current = e.current?.takeUnless { it.isFiller }
            PosterCard(
                title = current?.title?.takeIf { it.isNotBlank() } ?: noContent,
                posterUrl = (current?.poster ?: e.channel.logoUrl ?: e.channel.logo)?.let { homeUrl(client, it) },
                onClick = { nav.open(Route.Channel(e.channel.id)) },
                modifier = Modifier.testTag("channel-${e.channel.id}"),
                subtitle = e.channel.name,
                progress = current?.progress?.toFloat()?.takeIf { it > 0f },
                width = cardWidth,
            )
        }
    }
}

/** A picture's address: a path on the home (`/api/…`) under its base path, or a URL as given. */
internal fun homeUrl(client: ApiClient, path: String): String? =
    if (path.startsWith("http://") || path.startsWith("https://")) path
    else client.home.resolve(client.basePath + "/" + path.trimStart('/'))?.toString()

/** FR-13: nothing the person may watch on this home: says so, with Switch home and Sign out. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun EmptyHome(admin: Boolean, onSwitchHome: () -> Unit, onSignOut: () -> Unit) {
    Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp).testTag("home-empty"),
        verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Icon(Icons.Outlined.VideoLibrary, contentDescription = null, modifier = Modifier.size(48.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(
            stringResource(R.string.home_empty_title),
            Modifier.semantics { heading() },
            style = MaterialTheme.typography.titleLarge,
            textAlign = TextAlign.Center,
        )
        Text(
            stringResource(if (admin) R.string.home_empty_desc_admin else R.string.home_empty_desc),
            Modifier.widthIn(max = 480.dp),
            style = MaterialTheme.typography.bodyLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
        )
        Spacer(Modifier.heightIn(min = 4.dp))
        FlowRow(horizontalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterHorizontally), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(onClick = onSwitchHome, modifier = Modifier.heightIn(min = 48.dp).testTag("home-switch")) {
                Text(stringResource(CoreR.string.switch_home))
            }
            OutlinedButton(onClick = onSignOut, modifier = Modifier.heightIn(min = 48.dp).testTag("home-sign-out")) {
                Text(stringResource(CoreR.string.sign_out))
            }
        }
    }
}
