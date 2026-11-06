package com.hamzalabs.maktaba.desktop.home

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.Feature
import com.hamzalabs.maktaba.core.api.LiveApi
import com.hamzalabs.maktaba.core.api.NowEntry
import com.hamzalabs.maktaba.core.browse.CardOpen
import com.hamzalabs.maktaba.core.browse.HomeCard
import com.hamzalabs.maktaba.core.browse.HomeRow
import com.hamzalabs.maktaba.core.browse.KEY_LIVE
import com.hamzalabs.maktaba.core.browse.RowTitle
import com.hamzalabs.maktaba.desktop.CardAction
import com.hamzalabs.maktaba.desktop.EmptyBox
import com.hamzalabs.maktaba.desktop.ErrorBox
import com.hamzalabs.maktaba.desktop.Loading
import com.hamzalabs.maktaba.desktop.LocalTexts
import com.hamzalabs.maktaba.desktop.PlayRequest
import com.hamzalabs.maktaba.desktop.PosterCard
import com.hamzalabs.maktaba.desktop.Rail
import com.hamzalabs.maktaba.desktop.Texts
import com.hamzalabs.maktaba.desktop.choosable
import com.hamzalabs.maktaba.desktop.live.ChannelLogo
import com.hamzalabs.maktaba.desktop.shell.DesktopHome
import com.hamzalabs.maktaba.desktop.shell.FeatureOffNote
import com.hamzalabs.maktaba.desktop.shell.LocalDesktopHome
import com.hamzalabs.maktaba.desktop.shell.Route
import com.hamzalabs.maktaba.desktop.shell.features
import kotlinx.coroutines.launch

/** This home's Home state, kept while it is signed in. */
fun DesktopHome.homeState(): HomeState = keep("home") {
    val live = LiveApi(client)
    HomeState(
        client,
        recommendationsOn = { capabilities.value.isOn(Feature.RECOMMENDATIONS) },
        // no channel row while Live TV is off on the home: nothing is asked
        liveNow = { if (capabilities.value.isOn(Feature.LIVE_TV)) live.now() else null },
        signedOut = ::signedOut,
    )
}

/** Home (story 55.03). */
@Composable
fun HomeScreen() {
    val home = LocalDesktopHome.current
    val state = home.homeState()
    LaunchedEffect(state) { state.open() }
    HomeContent(state, home, home.features())
}

/** A row's title in this language. */
fun Texts.rowTitle(title: RowTitle): String = when (title) {
    is RowTitle.Named -> title.name
    RowTitle.Own.ContinueWatching -> this["home.row.continue"]
    RowTitle.Own.OnDeck -> this["home.row.onDeck"]
    RowTitle.Own.LiveNow -> this["home.row.liveNow"]
    RowTitle.Own.Favorites -> this["home.row.favorites"]
    RowTitle.Own.RecentlyAdded -> this["home.row.recentlyAdded"]
    RowTitle.Own.RecentlyWatched -> this["home.row.recentlyWatched"]
    RowTitle.Own.ForYou -> this["home.row.forYou"]
    RowTitle.Own.FromLibrary -> this["home.row.fromLibrary"]
}

/** What choosing [card] does: Continue Watching plays from where the person left; others open their page. */
fun DesktopHome.choose(card: HomeCard) {
    when {
        card.resumeAtSec != null -> play(PlayRequest(card.videoId, card.title.orEmpty(), startSec = null))
        else -> when (val o = card.open) {
            is CardOpen.Video -> nav.push(Route.Video(o.id))
            is CardOpen.Series -> nav.push(Route.Series(o.id))
        }
    }
}

@Composable
fun HomeContent(state: HomeState, home: DesktopHome, features: Capabilities) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    val rails = state.rails
    val problem = state.problem
    val admin = home.user.isAdmin
    Box(Modifier.fillMaxSize().testTag("home")) {
        when {
            rails == null && problem != null -> ErrorBox(t["home.error"], problem, onRetry = { scope.launch { state.load() } })
            rails == null -> Column {
                Loading()
                Row(Modifier.padding(16.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    repeat(4) { Surface(Modifier.width(220.dp).height(124.dp), shape = RoundedCornerShape(8.dp), color = MaterialTheme.colorScheme.surfaceContainerHigh) {} }
                }
            }
            state.empty -> EmptyBox(t["home.empty"], if (admin) t["home.emptyAdmin"] else t["home.emptyViewer"])
            else -> {
                val liveOn = features.isOn(Feature.LIVE_TV)
                val recsOn = features.isOn(Feature.RECOMMENDATIONS)
                val rows = if (liveOn) state.rows else state.rowsWithoutLive()
                LazyColumn(Modifier.fillMaxSize().testTag("home-rows"), contentPadding = PaddingValues(vertical = 16.dp), verticalArrangement = Arrangement.spacedBy(20.dp)) {
                    if (state.stale) item { Text(t["home.stale"], color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(horizontal = 16.dp).testTag("stale")) }
                    if (state.dismissFailed) item { Text(t["home.notInterestedFailed"], color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(horizontal = 16.dp).testTag("dismiss-failed")) }
                    rails.featured?.let { f ->
                        item(key = "hero") {
                            Hero(
                                f, home.posters,
                                onPlay = { home.play(PlayRequest(f.video.id, f.video.displayTitle, f.video.durationSec, startSec = null)) },
                                onMore = {
                                    val s = f.series
                                    if (f.kind == "series" && s != null) home.nav.push(Route.Series(s.id)) else home.nav.push(Route.Video(f.video.id))
                                },
                            )
                        }
                    }
                    items(rows, key = { it.key }) { row ->
                        when (row) {
                            is HomeRow.Cards -> CardsRow(row, state, home)
                            is HomeRow.Live -> LiveRow(row.channels, home)
                        }
                    }
                    if (admin && !liveOn) item(key = "off-live") { Box(Modifier.padding(horizontal = 16.dp)) { FeatureOffNote(Feature.LIVE_TV, home.webUrl, open = home.actions.openUrl) } }
                    if (admin && !recsOn) item(key = "off-recs") { Box(Modifier.padding(horizontal = 16.dp)) { FeatureOffNote(Feature.RECOMMENDATIONS, home.webUrl, open = home.actions.openUrl) } }
                }
            }
        }
    }
}

@Composable
private fun CardsRow(row: HomeRow.Cards, state: HomeState, home: DesktopHome) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    Rail(t.rowTitle(row.title), Modifier.testTag("row-${row.key}"), onSeeAll = row.seeAll?.let { lib -> { home.nav.push(Route.Library(lib)) } }) {
        items(row.cards, key = { it.key }) { card ->
            if (card.title == null && card.newLessons == null) LaunchedEffect(card.videoId) { state.resolve(card.videoId) }
            val title = card.title ?: if (card.newLessons != null) t["series.untitled"] else t["common.loading"]
            val dismiss = { scope.launch { state.dismiss(card.videoId) }; Unit }
            Box {
                PosterCard(
                    title = title,
                    posters = home.posters,
                    posterVideo = card.posterVideo,
                    onClick = { home.choose(card) },
                    modifier = Modifier.testTag("card-${card.key}"),
                    subtitle = card.newLessons?.let { t["home.newLessons", it] } ?: card.subtitle,
                    progress = card.progress,
                    watched = card.watched,
                    actions = if (card.recommended) listOf(CardAction(t["home.notInterested"], dismiss)) else emptyList(),
                )
                if (card.recommended) {
                    var menu by remember { mutableStateOf(false) }
                    Box(Modifier.align(Alignment.TopEnd)) {
                        IconButton(onClick = { menu = true }, modifier = Modifier.testTag("more-${card.key}")) { Icon(Icons.Filled.MoreVert, t["home.moreOptions"]) }
                        DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                            DropdownMenuItem(text = { Text(t["home.notInterested"]) }, onClick = { menu = false; dismiss() }, modifier = Modifier.testTag("not-interested"))
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun LiveRow(channels: List<NowEntry>, home: DesktopHome) {
    val t = LocalTexts.current
    val api = remember(home.client) { LiveApi(home.client) }
    Rail(t["home.row.liveNow"], Modifier.testTag("row-$KEY_LIVE")) {
        items(channels, key = { it.channel.id }) { e ->
            Column(
                Modifier.width(240.dp).choosable({ home.nav.push(Route.Channel(e.channel.id)) }).padding(8.dp).testTag("channel-${e.channel.id}"),
                verticalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    ChannelLogo(e.channel, api, 40.dp)
                    Text(e.channel.name, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
                Text(e.current?.title ?: t["live.nothingOn"], style = MaterialTheme.typography.bodyMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
        }
    }
}
