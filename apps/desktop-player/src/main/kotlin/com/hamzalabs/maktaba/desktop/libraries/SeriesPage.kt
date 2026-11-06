package com.hamzalabs.maktaba.desktop.libraries

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.ScrollableTabRow
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.Episode
import com.hamzalabs.maktaba.desktop.EmptyBox
import com.hamzalabs.maktaba.desktop.ErrorBox
import com.hamzalabs.maktaba.desktop.Loading
import com.hamzalabs.maktaba.desktop.LocalTexts
import com.hamzalabs.maktaba.desktop.PlayRequest
import com.hamzalabs.maktaba.desktop.PosterImage
import com.hamzalabs.maktaba.desktop.ProgressStrip
import com.hamzalabs.maktaba.desktop.choosable
import com.hamzalabs.maktaba.desktop.formatDuration
import com.hamzalabs.maktaba.desktop.shell.DesktopHome
import com.hamzalabs.maktaba.desktop.shell.LocalDesktopHome
import com.hamzalabs.maktaba.desktop.shell.Route
import kotlinx.coroutines.launch

/** A series' page (story 55.06). */
@Composable
fun SeriesPage(id: String) {
    val home = LocalDesktopHome.current
    val state = home.keep("series:$id") { SeriesState(home.client, id, home::signedOut) }
    LaunchedEffect(state) { state.load() }
    SeriesContent(state, home)
}

@Composable
fun SeriesContent(state: SeriesState, home: DesktopHome) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    val s = state.series
    val eps = state.episodes
    val problem = state.problem
    when {
        state.notAvailable -> EmptyBox(t["lib.notAvailable"], t["lib.notAvailableDetail"], Modifier.testTag("not-available"))
        (s == null || eps == null) && problem != null -> ErrorBox(t["lib.tab.series"], problem, onRetry = { scope.launch { state.load() } })
        s == null || eps == null -> Loading()
        else -> LazyColumn(Modifier.fillMaxSize().testTag("series-page"), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            item {
                Row(Modifier.fillMaxWidth().padding(16.dp), horizontalArrangement = Arrangement.spacedBy(20.dp)) {
                    PosterImage(home.posters, s.posterVideoId, Modifier.width(320.dp).aspectRatio(16f / 9f).clip(RoundedCornerShape(12.dp)))
                    Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        Text(s.name.ifBlank { t["series.untitled"] }, style = MaterialTheme.typography.headlineMedium, modifier = Modifier.semantics { heading() })
                        Text(t["lib.seriesProgress", s.watchedCount, s.episodeCount], color = MaterialTheme.colorScheme.onSurfaceVariant)
                        state.continueEpisode()?.let { e ->
                            Button(
                                onClick = { home.play(PlayRequest(e.videoId, e.title, e.durationSec, startSec = null)) },
                                modifier = Modifier.testTag("series-continue"),
                            ) { Text(t["series.continue"]) }
                        }
                        if (state.saveFailed) Text(t["video.saveFailed"], color = MaterialTheme.colorScheme.error, modifier = Modifier.testTag("save-failed"))
                    }
                }
            }
            val seasons = eps.seasons
            if (seasons.size > 1) item {
                ScrollableTabRow(selectedTabIndex = seasons.indexOfFirst { it.season == state.season }.coerceAtLeast(0)) {
                    for (se in seasons) Tab(se.season == state.season, { state.season = se.season }, text = { Text(t["series.season", se.season]) }, modifier = Modifier.testTag("season-${se.season}"))
                }
            }
            val shown = seasons.firstOrNull { it.season == state.season } ?: seasons.firstOrNull()
            if (shown != null) {
                val allWatched = shown.episodes.isNotEmpty() && shown.episodes.all { it.watched }
                item {
                    TextButton(
                        onClick = { home.change { state.mark(state.seasonIds(shown.season), !allWatched) } },
                        modifier = Modifier.padding(horizontal = 16.dp).testTag("mark-season"),
                    ) { Text(if (allWatched) t["series.unmarkSeason"] else t["series.markSeason"]) }
                }
                items(shown.episodes, key = { it.videoId }) { e -> EpisodeRow(e, home, onMark = { home.change { state.mark(listOf(e.videoId), !e.watched) } }) }
            }
        }
    }
}

@Composable
private fun EpisodeRow(e: Episode, home: DesktopHome, onMark: () -> Unit) {
    val t = LocalTexts.current
    Row(
        Modifier.fillMaxWidth().padding(horizontal = 16.dp).choosable({ home.nav.push(Route.Video(e.videoId)) }).padding(8.dp).testTag("episode-${e.videoId}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Box(Modifier.width(192.dp).aspectRatio(16f / 9f).clip(RoundedCornerShape(8.dp))) {
            PosterImage(home.posters, e.videoId, Modifier.fillMaxSize())
            if (e.progressPct > 0 && !e.watched) ProgressStrip((e.progressPct / 100).toFloat(), Modifier.align(Alignment.BottomCenter))
        }
        Column(Modifier.weight(1f)) {
            (e.episode ?: e.absoluteNumber)?.let { Text(t["series.episode", it], style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary) }
            Text(e.title, style = MaterialTheme.typography.titleSmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
            e.durationSec?.takeIf { it > 0 }?.let { Text(formatDuration(it), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        }
        if (e.watched) Icon(Icons.Filled.CheckCircle, t["series.watched"], tint = MaterialTheme.colorScheme.primary, modifier = Modifier.testTag("watched-${e.videoId}"))
        OutlinedButton(onClick = onMark, modifier = Modifier.testTag("mark-${e.videoId}")) { Text(if (e.watched) t["series.markUnwatched"] else t["series.markWatched"]) }
    }
}
