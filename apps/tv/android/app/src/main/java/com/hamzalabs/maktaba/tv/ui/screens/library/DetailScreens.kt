package com.hamzalabs.maktaba.tv.ui.screens.library

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.queue.QueueSource
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.data.TvApi
import com.hamzalabs.maktaba.tv.data.TvCard
import com.hamzalabs.maktaba.tv.data.TvCollection
import com.hamzalabs.maktaba.tv.data.TvEpisode
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton
import com.hamzalabs.maktaba.tv.ui.components.MediaCard
import com.hamzalabs.maktaba.tv.ui.components.SectionTitle
import com.hamzalabs.maktaba.tv.ui.screens.Gutter
import com.hamzalabs.maktaba.tv.ui.screens.LoadState
import com.hamzalabs.maktaba.tv.ui.screens.clock
import com.hamzalabs.maktaba.tv.ui.screens.rememberLoad
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import kotlinx.coroutines.launch

/** A season's name: its own, else Season n (Specials are season 0). */
@Composable
fun seasonName(n: Int, name: String?): String = name?.takeIf { it.isNotBlank() } ?: stringResource(R.string.series_season, n.toString())

/**
 * A series (story 56.04): its name, Continue (its next unwatched episode, else Play), Shuffle
 * and Mark watched; a pill per season; the season's episodes in lesson order (an ongoing
 * season newest first). Continue takes the first focus. Select on a pill opens the season.
 */
@Composable
fun SeriesScreen(api: TvApi, id: String, focusSeason: Int? = null) {
    val nav = LocalTvNav.current
    val scope = rememberCoroutineScope()
    var reload by remember { mutableStateOf(0) }
    val (state, retry) = rememberLoad(id to reload, nav.signedOut, where = "tv series") { api.series(id) to api.episodes(id) }
    LoadState(state, retry) { (series, eps) ->
        val first = remember { FocusRequester() }
        LaunchedEffect(id) { runCatching { first.requestFocus() } }
        var season by remember(id) { mutableStateOf(focusSeason ?: eps.nextEpisode?.season ?: eps.seasons.firstOrNull()?.season ?: 0) }
        val shown = eps.seasons.firstOrNull { it.season == season }
        Column(Modifier.fillMaxSize().padding(horizontal = Gutter, vertical = 28.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            SectionTitle(series.name.ifBlank { stringResource(R.string.series_untitled) })
            series.description?.takeIf { it.isNotBlank() }?.let { Text(it, style = MaterialTheme.typography.bodyLarge, color = MaktabaPalette.Muted, maxLines = 3) }
            Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                val started = eps.seasons.flatMap { it.episodes }.any { it.watched || it.progressPct > 0 }
                MaktabaButton(onClick = { nav.play(QueueSource("series", id), false, null) }, modifier = Modifier.focusRequester(first).testTag("series-continue")) {
                    Text("▶ " + stringResource(if (started) R.string.series_continue else R.string.library_play))
                }
                MaktabaButton(onClick = { nav.play(QueueSource("series", id), true, null) }, modifier = Modifier.testTag("series-shuffle")) {
                    Text("⤮ " + stringResource(R.string.library_shuffle))
                }
                MaktabaButton(onClick = {
                    scope.launch { runCatching { api.markWatched(eps.seasons.flatMap { s -> s.episodes.map { it.videoId } }) }; reload++ }
                }) { Text(stringResource(R.string.series_mark_watched)) }
            }
            if (eps.seasons.size > 1) {
                LazyRow(horizontalArrangement = Arrangement.spacedBy(12.dp), contentPadding = PaddingValues(vertical = 6.dp)) {
                    items(eps.seasons, key = { it.season }) { s ->
                        MaktabaButton(onClick = { if (season == s.season) nav.openSeason(id, s.season) else season = s.season }, modifier = Modifier.testTag("season-${s.season}")) {
                            Text((if (s.season == season) "● " else "") + seasonName(s.season, s.name))
                        }
                    }
                }
            }
            val list = shown?.episodes.orEmpty().let { if (shown?.kind == "ongoing") it.reversed() else it }
            EpisodeList(api, list)
        }
    }
}

@Composable
private fun EpisodeList(api: TvApi, list: List<TvEpisode>) {
    val nav = LocalTvNav.current
    LazyRow(horizontalArrangement = Arrangement.spacedBy(24.dp), contentPadding = PaddingValues(vertical = 14.dp, horizontal = 8.dp)) {
        items(list, key = { it.videoId }) { e ->
            MediaCard(
                client = api.client, video = e.videoId,
                title = listOfNotNull(e.episode?.toString(), e.title).joinToString(". "),
                subtitle = listOfNotNull(e.durationSec?.let { clock(it) }, if (e.watched) "✓" else null).joinToString("  ").ifBlank { null },
                progress = (e.progressPct / 100).toFloat(),
                onClick = { nav.openVideo(e.videoId) },
            )
        }
    }
}

/** A season (story 56.04): Play (its next unwatched episode), Shuffle, Mark season watched, its episodes. */
@Composable
fun SeasonScreen(api: TvApi, seriesId: String, number: Int) {
    val nav = LocalTvNav.current
    val scope = rememberCoroutineScope()
    var reload by remember { mutableStateOf(0) }
    val (state, retry) = rememberLoad(seriesId to (number to reload), nav.signedOut, where = "tv season") { api.episodes(seriesId) }
    LoadState(state, retry) { eps ->
        val s = eps.seasons.firstOrNull { it.season == number } ?: return@LoadState
        val first = remember { FocusRequester() }
        LaunchedEffect(seriesId, number) { runCatching { first.requestFocus() } }
        val source = s.id?.let { QueueSource("season", it) } ?: QueueSource("series", seriesId)
        Column(Modifier.fillMaxSize().padding(horizontal = Gutter, vertical = 28.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            SectionTitle(seasonName(s.season, s.name))
            Text(
                listOfNotNull(stringResource(R.string.library_show_videos) + ": " + s.episodes.size, s.episodes.sumOf { it.durationSec ?: 0.0 }.takeIf { it > 0 }?.let { clock(it) }).joinToString("  ·  "),
                style = MaterialTheme.typography.titleMedium, color = MaktabaPalette.Muted,
            )
            Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                MaktabaButton(onClick = { nav.play(source, false, null) }, modifier = Modifier.focusRequester(first).testTag("season-play")) { Text("▶ " + stringResource(R.string.library_play)) }
                MaktabaButton(onClick = { nav.play(source, true, null) }, modifier = Modifier.testTag("season-shuffle")) { Text("⤮ " + stringResource(R.string.library_shuffle)) }
                MaktabaButton(onClick = { scope.launch { runCatching { api.markWatched(s.episodes.map { it.videoId }) }; reload++ } }) {
                    Text(stringResource(R.string.series_mark_season_watched))
                }
            }
            EpisodeList(api, if (s.kind == "ongoing") s.episodes.reversed() else s.episodes)
        }
    }
}

/** A collection's or a playlist's page: its name, count and length, Play and Shuffle, its videos. */
@Composable
fun ListPage(
    api: TvApi,
    title: String,
    badge: String?,
    rules: String?,
    source: QueueSource,
    cards: List<TvCard>,
    onRemove: ((TvCard) -> Unit)? = null,
) {
    val nav = LocalTvNav.current
    val first = remember { FocusRequester() }
    LaunchedEffect(source) { runCatching { first.requestFocus() } }
    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(horizontal = Gutter, vertical = 28.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
        item { SectionTitle(title) }
        item {
            val total = cards.sumOf { it.durationSec ?: 0.0 }
            Text(
                listOfNotNull(badge, stringResource(R.string.library_show_videos) + ": " + cards.size, total.takeIf { it > 0 }?.let { clock(it) }, rules).joinToString("  ·  "),
                style = MaterialTheme.typography.titleMedium, color = MaktabaPalette.Muted,
            )
        }
        item {
            Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                MaktabaButton(onClick = { nav.play(source, false, null) }, enabled = cards.isNotEmpty(), modifier = Modifier.focusRequester(first).testTag("list-play")) {
                    Text("▶ " + stringResource(R.string.library_play))
                }
                if (cards.size > 1) {
                    MaktabaButton(onClick = { nav.play(source, true, null) }, modifier = Modifier.testTag("list-shuffle")) { Text("⤮ " + stringResource(R.string.library_shuffle)) }
                }
            }
        }
        items(cards, key = { it.id }) { c ->
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                MediaCard(client = api.client, video = c.id, title = c.name, subtitle = c.durationSec?.let { clock(it) }, progress = c.progress, width = 320.dp, onClick = { nav.play(source, false, c.id) })
                if (onRemove != null) MaktabaButton(onClick = { onRemove(c) }) { Text(stringResource(R.string.playlists_remove)) }
            }
        }
    }
}

@Composable
fun CollectionScreen(api: TvApi, library: String, collection: TvCollection) {
    val nav = LocalTvNav.current
    val (state, retry) = rememberLoad(collection.queueId, nav.signedOut, where = "tv collection") { api.collectionVideos(library, collection) }
    LoadState(state, retry) { cards ->
        ListPage(api, collection.name, collectionBadge(collection), null, QueueSource("collection", collection.queueId), cards)
    }
}

/** A playlist (story 56.10): a smart one shows its rules and has no Remove. */
@Composable
fun PlaylistScreen(api: TvApi, id: String) {
    val nav = LocalTvNav.current
    val scope = rememberCoroutineScope()
    var reload by remember { mutableStateOf(0) }
    val (state, retry) = rememberLoad(id to reload, nav.signedOut, where = "tv playlist") { api.playlist(id) }
    LoadState(state, retry) { doc ->
        val p = doc.playlist
        val rules = p.smartQuery?.let { q ->
            (q.filters.map { (k, v) -> "$k: $v" } + listOfNotNull(q.sort, "≤ ${q.limit}")).joinToString(", ")
        }
        ListPage(
            api, p.name, if (p.smart) stringResource(R.string.playlists_smart_badge) else null,
            rules?.let { stringResource(R.string.playlists_smart_rules) + ": " + it },
            QueueSource("playlist", id), doc.items,
            onRemove = if (p.smart) null else { c -> scope.launch { runCatching { api.removeFromPlaylist(id, c.id) }; reload++ } },
        )
    }
}
