package com.hamzalabs.maktaba.desktop.video

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Favorite
import androidx.compose.material.icons.filled.FavoriteBorder
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Button
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
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
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.Feature
import com.hamzalabs.maktaba.desktop.EmptyBox
import com.hamzalabs.maktaba.desktop.ErrorBox
import com.hamzalabs.maktaba.desktop.Loading
import com.hamzalabs.maktaba.desktop.LocalTexts
import com.hamzalabs.maktaba.desktop.PlayRequest
import com.hamzalabs.maktaba.desktop.PosterCard
import com.hamzalabs.maktaba.desktop.PosterImage
import com.hamzalabs.maktaba.desktop.Rail
import com.hamzalabs.maktaba.desktop.choosable
import com.hamzalabs.maktaba.desktop.formatDuration
import com.hamzalabs.maktaba.desktop.shell.DesktopHome
import com.hamzalabs.maktaba.desktop.shell.FeatureOffNote
import com.hamzalabs.maktaba.desktop.shell.LocalDesktopHome
import com.hamzalabs.maktaba.desktop.shell.Route
import com.hamzalabs.maktaba.desktop.shell.features
import kotlinx.coroutines.launch
import java.util.Locale

/** A window this wide puts the poster beside the details. */
private val WIDE = 900.dp

/** A video's page (story 55.06); [passage] is the transcript line it was opened from. */
@Composable
fun VideoPage(id: String, passage: Route.Passage?) {
    val home = LocalDesktopHome.current
    val state = home.keep("video:$id") { VideoPageState(home.client, id, home::signedOut, chaptersOn = { home.capabilities.value.isOn(Feature.CHAPTERS) }) }
    LaunchedEffect(state) { state.load() }
    VideoContent(state, home, home.features(), passage)
}

@Composable
fun VideoContent(state: VideoPageState, home: DesktopHome, features: Capabilities, passage: Route.Passage? = null) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    val d = state.detail
    val problem = state.problem
    when {
        state.notAvailable -> {
            EmptyBox(t["video.notAvailable"], t["video.notAvailableDetail"], Modifier.testTag("not-available"))
            return
        }
        d == null && problem != null -> {
            ErrorBox(t["video.notAvailable"], problem, onRetry = { scope.launch { state.load() } })
            return
        }
        d == null -> {
            Loading()
            return
        }
    }
    val v = d!!
    val request = { start: Int? -> PlayRequest(v.id, v.displayTitle, v.durationSec, startSec = start) }
    BoxWithConstraints(Modifier.fillMaxSize().testTag("video-page")) {
        val wide = maxWidth >= WIDE
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            val poster = @Composable { m: Modifier -> PosterImage(home.posters, v.id, m.aspectRatio(16f / 9f).clip(RoundedCornerShape(12.dp))) }
            val details = @Composable {
                Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    Text(v.displayTitle, style = MaterialTheme.typography.headlineMedium, modifier = Modifier.semantics { heading() })
                    val ep = state.episode
                    state.mine?.series?.let { s ->
                        TextButton(onClick = { home.nav.push(Route.Series(s.id)) }, modifier = Modifier.testTag("video-series")) {
                            Text(if (ep?.season != null && ep.episode != null) "${s.name} · ${t["video.seasonEpisode", ep.season, ep.episode]}" else s.name)
                        }
                    }
                    val facts = listOfNotNull(
                        (v.publishedAt ?: v.createdAt)?.take(4),
                        v.durationSec?.takeIf { it > 0 }?.let(::formatDuration),
                        v.detectedLanguage?.let { Locale.forLanguageTag(it).getDisplayLanguage(Locale.forLanguageTag(t.lang.code)).ifBlank { it } },
                    )
                    if (facts.isNotEmpty()) Text(facts.joinToString(" · "), color = MaterialTheme.colorScheme.onSurfaceVariant)
                    if (v.tags.isNotEmpty()) FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) { v.tags.forEach { AssistChip(onClick = {}, label = { Text(it) }) } }
                    PlayButtons(state.resumeSec, onPlay = { home.play(request(it)) })
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        FilterChip(
                            state.favorite, { home.change { state.toggleFavorite() } },
                            label = { Text(t["video.favorite"]) },
                            leadingIcon = { Icon(if (state.favorite) Icons.Filled.Favorite else Icons.Filled.FavoriteBorder, null) },
                            modifier = Modifier.testTag("favorite"),
                        )
                        FilterChip(
                            state.watched, { home.change { state.toggleWatched() } },
                            label = { Text(t["video.markWatched"]) },
                            leadingIcon = { if (state.watched) Icon(Icons.Filled.Check, null) },
                            modifier = Modifier.testTag("watched"),
                        )
                    }
                    if (state.saveFailed) Text(t["video.saveFailed"], color = MaterialTheme.colorScheme.error, modifier = Modifier.testTag("save-failed"))
                    v.description?.takeIf { it.isNotBlank() }?.let { Text(it, style = MaterialTheme.typography.bodyMedium) }
                }
            }
            if (wide) {
                Row(horizontalArrangement = Arrangement.spacedBy(24.dp)) {
                    poster(Modifier.width(420.dp))
                    Column(Modifier.weight(1f)) { details() }
                }
            } else {
                poster(Modifier.fillMaxWidth())
                details()
            }
            passage?.let { p ->
                Column(Modifier.testTag("video-passage"), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text(t["video.transcript"], style = MaterialTheme.typography.titleMedium, modifier = Modifier.semantics { heading() })
                    Text(p.text, style = MaterialTheme.typography.bodyLarge)
                    OutlinedButton(onClick = { home.play(request(p.startSec.toInt())) }) { Text(t["video.playFrom", formatDuration(p.startSec)]) }
                }
            }
            when {
                features.explains(Feature.CHAPTERS) -> FeatureOffNote(Feature.CHAPTERS, home.webUrl, open = home.actions.openUrl)
                !features.isOn(Feature.CHAPTERS) -> {}
                state.chapters.isNotEmpty() -> Column(Modifier.testTag("chapters"), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Text(t["video.chapters"], style = MaterialTheme.typography.titleMedium, modifier = Modifier.semantics { heading() })
                    for (c in state.chapters) {
                        Row(
                            Modifier.fillMaxWidth().choosable({ home.play(request(c.startSec.toInt())) }).padding(8.dp).testTag("chapter-${c.seq}"),
                            horizontalArrangement = Arrangement.spacedBy(12.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Text(formatDuration(c.startSec), color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.labelLarge)
                            Text(c.title, style = MaterialTheme.typography.bodyMedium)
                        }
                    }
                }
            }
            if (state.nextEpisodes.isNotEmpty()) {
                Rail(t["video.nextEpisodes"], Modifier.testTag("next-episodes")) {
                    items(state.nextEpisodes, key = { it.videoId }) { e ->
                        PosterCard(
                            e.title, home.posters, e.videoId, onClick = { home.nav.push(Route.Video(e.videoId)) },
                            subtitle = e.episode?.let { t["series.episode", it] }, watched = e.watched,
                            progress = (e.progressPct / 100).toFloat().takeIf { it > 0f && !e.watched },
                        )
                    }
                }
            }
            val lists = state.mine?.playlists.orEmpty()
            if (lists.isNotEmpty()) {
                Column(verticalArrangement = Arrangement.spacedBy(6.dp), modifier = Modifier.testTag("video-playlists")) {
                    Text(t["video.playlists"], style = MaterialTheme.typography.titleMedium, modifier = Modifier.semantics { heading() })
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        lists.forEach { p -> AssistChip(onClick = { home.nav.push(Route.Playlist(p.id)) }, label = { Text(p.name) }) }
                    }
                }
            }
        }
    }
}

/** Play (from the start when never played), or Resume from and Play from start when there is a saved position. */
@Composable
fun PlayButtons(resumeSec: Int?, onPlay: (Int?) -> Unit) {
    val t = LocalTexts.current
    Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        if (resumeSec != null) {
            Button(onClick = { onPlay(resumeSec) }, modifier = Modifier.testTag("resume")) { Text(t["video.resumeAt", formatDuration(resumeSec.toDouble())]) }
            OutlinedButton(onClick = { onPlay(0) }, modifier = Modifier.testTag("from-start")) { Text(t["video.fromStart"]) }
        } else {
            Button(onClick = { onPlay(0) }, modifier = Modifier.testTag("play")) { Text(t["video.play"]) }
        }
    }
}
