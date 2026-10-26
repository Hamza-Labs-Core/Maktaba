package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.data.TvApi
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton
import com.hamzalabs.maktaba.tv.ui.components.Poster
import com.hamzalabs.maktaba.tv.ui.components.SectionTitle
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette

/**
 * A video's page: its picture, name, length and description, and how to watch it: Resume from
 * where this person stopped (and Start over), or Play.
 */
@Composable
fun VideoScreen(api: TvApi, id: String, onPlay: (title: String, startMs: Long) -> Unit, signedOut: () -> Unit) {
    val (state, retry) = rememberLoad(id, signedOut) { api.video(id) }
    LoadState(state, retry) { v ->
        val focus = remember { FocusRequester() }
        LaunchedEffect(v.id) { runCatching { focus.requestFocus() } }
        Row(
            Modifier.fillMaxSize().padding(horizontal = Gutter, vertical = 40.dp),
            horizontalArrangement = Arrangement.spacedBy(36.dp),
        ) {
            Poster(api.client, v.id, Modifier.width(380.dp).aspectRatio(16f / 9f).clip(RoundedCornerShape(12.dp)))
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(14.dp)) {
                SectionTitle(v.displayTitle)
                val facts = listOfNotNull(
                    v.durationSec?.let { clock(it) },
                    v.playback?.takeIf { it.completed }?.let { stringResource(R.string.watched) },
                )
                if (facts.isNotEmpty()) {
                    Text(facts.joinToString("  ·  "), style = MaterialTheme.typography.titleMedium, color = MaktabaPalette.Muted)
                }
                Row(Modifier.padding(vertical = 12.dp), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                    val resume = v.resumeSec
                    if (resume != null) {
                        MaktabaButton(onClick = { onPlay(v.displayTitle, (resume * 1000).toLong()) }, modifier = Modifier.focusRequester(focus)) {
                            Text(stringResource(R.string.resume_from, clock(resume)))
                        }
                        MaktabaButton(onClick = { onPlay(v.displayTitle, 0) }) { Text(stringResource(R.string.start_over)) }
                    } else {
                        MaktabaButton(onClick = { onPlay(v.displayTitle, 0) }, modifier = Modifier.focusRequester(focus)) {
                            Text(stringResource(R.string.play))
                        }
                    }
                }
                v.description?.takeIf { it.isNotBlank() }?.let {
                    Box(Modifier.fillMaxWidth()) {
                        Text(it, style = MaterialTheme.typography.bodyLarge, color = MaktabaPalette.Muted, maxLines = 8, overflow = TextOverflow.Ellipsis)
                    }
                }
            }
        }
    }
}
