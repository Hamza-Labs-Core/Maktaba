package com.hamzalabs.maktaba.player.libraries

import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.outlined.CheckCircle
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.R as CoreR
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.Episode
import com.hamzalabs.maktaba.core.ui.ErrorState
import com.hamzalabs.maktaba.core.ui.MaktabaTokens
import com.hamzalabs.maktaba.core.ui.formatDuration
import com.hamzalabs.maktaba.player.BackButton
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerNavigator
import com.hamzalabs.maktaba.player.ui.components.HomePicture
import com.hamzalabs.maktaba.player.ui.components.MissingPosterMark
import com.hamzalabs.maktaba.player.ui.components.POSTER_ASPECT
import com.hamzalabs.maktaba.player.ui.components.PosterSize
import com.hamzalabs.maktaba.player.ui.components.ProgressStrip
import com.hamzalabs.maktaba.player.ui.components.posterUrl
import kotlinx.coroutines.launch

/**
 * A series' page (FR-17): its art, name and the caller's progress, **Continue** (its next
 * episode), a season picker, and the season's episodes with progress and watched marks; Mark
 * watched on an episode or the whole season (`POST /api/me/watched`). The home sends no
 * description for a series, so none is shown. [embedded] beside the list (no Back of its own).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun SeriesPage(id: String, nav: PlayerNavigator, embedded: Boolean) {
    val home = LocalPlayerHome.current
    val state = home.keep("libraries.series.$id") { SeriesState(home.client, id, home.session::signedOut) }
    val scope = rememberCoroutineScope()
    val open = LocalOpenVideo.current
    LaunchedEffect(state) { state.load() }

    Scaffold(
        modifier = Modifier.testTag("series-page"),
        topBar = {
            TopAppBar(
                title = { Text(state.series?.name.orEmpty(), maxLines = 1, overflow = TextOverflow.Ellipsis) },
                navigationIcon = { if (!embedded && nav.canGoBack) BackButton { nav.back() } },
            )
        },
    ) { padding ->
        Box(Modifier.padding(padding).fillMaxSize()) {
            val problem = state.problem
            val series = state.series
            when {
                state.notAvailable -> NotAvailable()
                problem != null && series == null -> ErrorState(
                    problem,
                    title = stringResource(CoreR.string.error_title),
                    onRetry = { scope.launch { state.load() } },
                )
                series == null -> Spinner()
                else -> LazyColumn(Modifier.fillMaxSize().testTag("episodes"), state = rememberLazyListState()) {
                    item(key = "header") { Header(state, home.client, onContinue = { e -> open(e.videoId) }) }
                    item(key = "seasons") {
                        Seasons(state, onMarkSeason = { scope.launch { state.markSeason() } })
                    }
                    items(state.episodes, key = { it.videoId }) { e ->
                        EpisodeRow(
                            e, home.client,
                            onOpen = { open(e.videoId) },
                            onMark = { scope.launch { state.mark(listOf(e.videoId), !e.watched) } },
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun Header(state: SeriesState, client: ApiClient, onContinue: (Episode) -> Unit) {
    val series = state.series ?: return
    Column(
        Modifier.fillMaxWidth().padding(MaktabaTokens.Spacing.s4),
        verticalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s3),
    ) {
        Box(Modifier.widthIn(max = 640.dp).fillMaxWidth().aspectRatio(POSTER_ASPECT).clip(RoundedCornerShape(MaktabaTokens.Radius.lg))) {
            HomePicture(series.posterVideoId?.let { posterUrl(client, it, PosterSize.Full) }, Modifier.fillMaxSize()) { MissingPosterMark() }
        }
        Text(
            series.name.ifBlank { stringResource(R.string.libraries_series_untitled) },
            style = MaterialTheme.typography.headlineSmall,
            modifier = Modifier.semantics { heading() },
        )
        val meta = listOfNotNull(pluralStringResource(R.plurals.libraries_episodes, series.episodeCount, series.episodeCount), series.year?.toString())
        Text(meta.joinToString(" · "), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        if (series.episodeCount > 0 && series.watchedCount > 0) {
            Text(
                stringResource(R.string.libraries_series_progress, series.watchedCount, series.episodeCount),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            ProgressStrip(series.progress.toFloat(), Modifier.widthIn(max = 320.dp).fillMaxWidth())
        }
        state.next?.let { next ->
            Button(onClick = { onContinue(next) }, modifier = Modifier.testTag("continue").heightIn(min = 48.dp)) {
                Icon(Icons.Filled.PlayArrow, null)
                Column(Modifier.padding(start = MaktabaTokens.Spacing.s2)) {
                    Text(stringResource(R.string.series_continue))
                    if (next.title.isNotBlank()) Text(next.title, style = MaterialTheme.typography.labelSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }
        }
    }
}

/** The season picker (when there is more than one) and Mark the season watched. */
@Composable
private fun Seasons(state: SeriesState, onMarkSeason: () -> Unit) {
    val chosen = state.season ?: state.seasons.firstOrNull()?.season
    Column(Modifier.fillMaxWidth().padding(horizontal = MaktabaTokens.Spacing.s4)) {
        if (state.seasons.size > 1) {
            Row(
                Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()),
                horizontalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s2),
            ) {
                state.seasons.forEach { s ->
                    FilterChip(
                        selected = s.season == chosen,
                        onClick = { state.season = s.season },
                        label = { Text(stringResource(R.string.series_season, s.season)) },
                        modifier = Modifier.testTag("season-${s.season}"),
                    )
                }
            }
        }
        val eps = state.episodes
        if (eps.isNotEmpty()) {
            TextButton(onClick = onMarkSeason, modifier = Modifier.testTag("mark-season")) {
                Text(stringResource(if (eps.all { it.watched }) R.string.series_unmark_season else R.string.series_mark_season))
            }
        }
    }
}

/** An episode: its picture, number and title, length, progress, and its watched mark (a toggle). */
@Composable
private fun EpisodeRow(e: Episode, client: ApiClient, onOpen: () -> Unit, onMark: () -> Unit) {
    val number = e.episode ?: e.absoluteNumber
    Row(
        Modifier.fillMaxWidth().testTag("episode-${e.videoId}")
            .clickable(role = Role.Button, onClick = onOpen)
            .padding(horizontal = MaktabaTokens.Spacing.s4, vertical = MaktabaTokens.Spacing.s2),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.width(128.dp).aspectRatio(POSTER_ASPECT).clip(RoundedCornerShape(MaktabaTokens.Radius.sm))) {
            HomePicture(posterUrl(client, e.videoId, PosterSize.Small), Modifier.fillMaxSize()) { MissingPosterMark() }
            val pct = (e.progressPct / 100).toFloat()
            if (!e.watched && pct > 0f && pct < 1f) {
                ProgressStrip(pct, Modifier.align(Alignment.BottomStart).padding(MaktabaTokens.Spacing.s1), track = MaktabaTokens.Dark.scrim)
            }
        }
        Column(Modifier.weight(1f).padding(horizontal = MaktabaTokens.Spacing.s3)) {
            if (number != null) {
                Text(stringResource(R.string.series_episode, number), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary)
            }
            Text(e.title, style = MaterialTheme.typography.titleSmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
            val meta = listOfNotNull(e.durationSec?.let { formatDuration(it) }, if (e.watched) stringResource(R.string.series_watched) else null)
            if (meta.isNotEmpty()) {
                Text(meta.joinToString(" · "), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
        IconButton(onClick = onMark, modifier = Modifier.testTag("mark-${e.videoId}")) {
            Icon(
                if (e.watched) Icons.Filled.CheckCircle else Icons.Outlined.CheckCircle,
                contentDescription = stringResource(if (e.watched) R.string.series_mark_unwatched else R.string.series_mark_watched) + ", " + e.title,
                tint = if (e.watched) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
    HorizontalDivider(Modifier.padding(horizontal = MaktabaTokens.Spacing.s4), color = MaterialTheme.colorScheme.outlineVariant)
}
