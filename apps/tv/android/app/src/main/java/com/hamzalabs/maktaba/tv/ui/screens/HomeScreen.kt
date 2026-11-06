package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.diagnostics.AppLog
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.data.TvApi
import com.hamzalabs.maktaba.tv.data.TvVideo
import com.hamzalabs.maktaba.tv.ui.components.MediaCard
import com.hamzalabs.maktaba.tv.ui.components.SectionTitle
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.platform.testTag
import com.hamzalabs.maktaba.core.queue.PlayQueueModel
import com.hamzalabs.maktaba.core.queue.QueueDoc
import com.hamzalabs.maktaba.core.queue.QueueItem
import com.hamzalabs.maktaba.tv.data.TvCard
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton

/** One row on Home: its name and its videos (with how much of each is watched). */
data class HomeRow(val title: String, val videos: List<Pair<TvVideo, Float>>)

/**
 * Home: Continue Watching first (the home's own rail: where this person stopped), then the
 * newest videos, then the home's suggestions. The library scene is drawn behind it.
 */
/** A card as a row's video, with how much of it is watched. */
fun TvCard.asRowVideo(): Pair<TvVideo, Float> = TvVideo(id = id, libraryId = libraryId, filename = filename, title = title, durationSec = durationSec) to progress

@Composable
fun HomeScreen(
    api: TvApi,
    focusFirst: Boolean,
    onOpen: (String) -> Unit,
    signedOut: () -> Unit,
    /** Epic 56 story 56.11: the queue last used on any device, offered first. */
    queue: PlayQueueModel? = null,
    onContinue: (QueueItem) -> Unit = {},
) {
    val cw = stringResource(R.string.continue_watching)
    val recent = stringResource(R.string.recently_added)
    val forYou = stringResource(R.string.for_you)
    val onDeck = stringResource(R.string.library_row_on_deck)
    val watched = stringResource(R.string.library_row_recently_watched)
    val (state, retry) = rememberLoad(api, signedOut, where = "tv home") {
        coroutineScope {
            // Home's rows (story 56.03): Continue Watching, On Deck, Recently Added, Recently
            // Watched and the collections an admin shows here; the suggestions are extra
            val recs = async { runCatching { api.recommendations() }.onFailure { if (it is CancellationException) throw it else AppLog.failure("tv recommendations", it) }.getOrNull() }
            val home = async { runCatching { api.home() }.onFailure { if (it is CancellationException) throw it else AppLog.failure("tv home rows", it) }.getOrNull() }
            val h = home.await()
            val rails = recs.await()?.rails.orEmpty()
            val suggested = rails.firstOrNull { it.id == "for-you" }?.let { rail ->
                async { api.railVideos(rail).map { (_, v) -> v to 0f } }
            }
            val rows = if (h != null) {
                listOf(
                    HomeRow(cw, h.continueWatching.map { it.asRowVideo() }),
                    HomeRow(onDeck, h.onDeck.map { it.asRowVideo() }),
                    HomeRow(recent, h.recentlyAdded.map { it.asRowVideo() }),
                    HomeRow(watched, h.recentlyWatched.map { it.asRowVideo() }),
                ) + h.collectionRows.map { r -> HomeRow(r.collection.name, r.items.map { it.asRowVideo() }) }
            } else {
                // an older home without /api/home: the newest videos
                listOf(HomeRow(recent, api.videos().items.map { it to 0f }))
            }
            (rows + listOfNotNull(suggested?.await()?.let { HomeRow(forYou, it) })).filter { it.videos.isNotEmpty() }
        }
    }
    val current = remember(api) { mutableStateOf<QueueDoc?>(null) }
    LaunchedEffect(queue) {
        val q = queue ?: return@LaunchedEffect
        val doc = runCatching { q.fetchCurrent() }.getOrNull() ?: return@LaunchedEffect
        val used = runCatching { java.time.Instant.parse(doc.usedAt).toEpochMilli() }.getOrNull() ?: return@LaunchedEffect
        if (System.currentTimeMillis() - used <= 24 * 3_600_000L && doc.current != null) current.value = doc
    }
    current.value?.let { doc ->
        val item = doc.current ?: return@let
        val device = deviceWords(doc.deviceId)
        Column(Modifier.padding(start = Gutter, top = 16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            SectionTitle(stringResource(R.string.home_continue_queue))
            MaktabaButton(onClick = { queue?.resume(doc)?.let(onContinue) }, modifier = Modifier.testTag("continue-queue")) {
                Text(if (device != null) stringResource(R.string.home_continue_queue_from, item.name, device) else item.name)
            }
        }
    }
    LoadState(state, retry) { rows ->
        if (rows.isEmpty()) {
            Centered { Text(stringResource(R.string.nothing_yet), style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text) }
        } else {
            val first = remember { FocusRequester() }
            // at launch and back from a video: the first card (not when the tab bar has focus)
            LaunchedEffect(rows, focusFirst) { if (focusFirst) runCatching { first.requestFocus() } }
            LazyColumn(
                Modifier.fillMaxSize(),
                contentPadding = PaddingValues(start = Gutter, end = Gutter, top = 24.dp, bottom = 48.dp),
                verticalArrangement = Arrangement.spacedBy(28.dp),
            ) {
                items(rows, key = { it.title }) { row ->
                    VideoRow(api, row, onOpen, first = if (focusFirst && row === rows.first()) first else null)
                }
            }
        }
    }
}

@Composable
fun VideoRow(api: TvApi, row: HomeRow, onOpen: (String) -> Unit, first: FocusRequester? = null) {
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        SectionTitle(row.title)
        // vertical room for the focused card's scale and ring
        LazyRow(
            horizontalArrangement = Arrangement.spacedBy(24.dp),
            contentPadding = PaddingValues(vertical = 14.dp, horizontal = 8.dp),
        ) {
            items(row.videos, key = { it.first.id }) { (v, progress) ->
                MediaCard(
                    client = api.client,
                    video = v.id,
                    title = v.displayTitle,
                    subtitle = v.durationSec?.let { clock(it) },
                    progress = progress,
                    cardModifier = if (first != null && v === row.videos.first().first) Modifier.focusRequester(first) else Modifier,
                    onClick = { onOpen(v.id) },
                )
            }
        }
    }
}


/** A queue's device in words: the TV, a phone, a browser (else none). */
@Composable
fun deviceWords(id: String?): String? = when {
    id == null -> null
    id.startsWith("tv") -> stringResource(R.string.device_tv)
    id.startsWith("web") -> stringResource(R.string.device_web)
    id.startsWith("phone") || id.startsWith("android") || id.startsWith("ios") -> stringResource(R.string.device_phone)
    else -> null
}
