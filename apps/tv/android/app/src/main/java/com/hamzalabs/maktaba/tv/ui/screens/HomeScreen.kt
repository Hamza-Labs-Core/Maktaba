package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.data.TvApi
import com.hamzalabs.maktaba.tv.data.TvVideo
import com.hamzalabs.maktaba.tv.ui.components.MediaCard
import com.hamzalabs.maktaba.tv.ui.components.SectionTitle
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope

/** One row on Home: its name and its videos (with how much of each is watched). */
data class HomeRow(val title: String, val videos: List<Pair<TvVideo, Float>>)

/**
 * Home: Continue Watching first (the home's own rail: where this person stopped), then the
 * newest videos, then the home's suggestions. The library scene is drawn behind it.
 */
@Composable
fun HomeScreen(api: TvApi, onOpen: (String) -> Unit, signedOut: () -> Unit) {
    val cw = stringResource(R.string.continue_watching)
    val recent = stringResource(R.string.recently_added)
    val forYou = stringResource(R.string.for_you)
    val (state, retry) = rememberLoad(api, signedOut) {
        coroutineScope {
            val recs = async { runCatching { api.recommendations() }.getOrNull() }
            val newest = async { api.videos().items }
            val rails = recs.await()?.rails.orEmpty()
            val cont = rails.firstOrNull { it.id == "continue" }?.let { rail ->
                async { api.railVideos(rail).map { (item, v) -> v to item.progress } }
            }
            val suggested = rails.firstOrNull { it.id == "for-you" }?.let { rail ->
                async { api.railVideos(rail).map { (_, v) -> v to 0f } }
            }
            listOfNotNull(
                cont?.await()?.let { HomeRow(cw, it) },
                HomeRow(recent, newest.await().map { it to 0f }),
                suggested?.await()?.let { HomeRow(forYou, it) },
            ).filter { it.videos.isNotEmpty() }
        }
    }
    LoadState(state, retry) { rows ->
        if (rows.isEmpty()) {
            Centered { Text(stringResource(R.string.nothing_yet), style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text) }
        } else {
            LazyColumn(
                Modifier.fillMaxSize(),
                contentPadding = PaddingValues(start = Gutter, end = Gutter, top = 24.dp, bottom = 48.dp),
                verticalArrangement = Arrangement.spacedBy(28.dp),
            ) {
                items(rows, key = { it.title }) { row -> VideoRow(api, row, onOpen) }
            }
        }
    }
}

@Composable
fun VideoRow(api: TvApi, row: HomeRow, onOpen: (String) -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        SectionTitle(row.title)
        // vertical room for the focused card's scale and ring
        LazyRow(
            horizontalArrangement = Arrangement.spacedBy(28.dp),
            contentPadding = PaddingValues(vertical = 14.dp, horizontal = 8.dp),
        ) {
            items(row.videos, key = { it.first.id }) { (v, progress) ->
                MediaCard(
                    client = api.client,
                    video = v.id,
                    title = v.displayTitle,
                    subtitle = v.durationSec?.let { clock(it) },
                    progress = progress,
                    onClick = { onOpen(v.id) },
                )
            }
        }
    }
}

