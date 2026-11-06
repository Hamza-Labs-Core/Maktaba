package com.hamzalabs.maktaba.player.libraries

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.queue.EmptySource
import com.hamzalabs.maktaba.core.queue.QueueSource
import com.hamzalabs.maktaba.player.R
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/** Opens the player at a video (a queue's first item): the shell gives it (Route.Watch). */
val LocalPlayVideo = compositionLocalOf<(String) -> Unit> { {} }

/** Play and Shuffle on a source (story 56.12): one server queue, then the player at its first item. */
@Composable
fun PlayShuffleRow(
    client: ApiClient,
    source: QueueSource,
    modifier: Modifier = Modifier,
    shuffle: Boolean = true,
    /** The Shuffle button's words when it stands alone (a series' Shuffle the series). */
    playLabel: Int = R.string.library_shuffle,
    onlyShuffle: Boolean = false,
) {
    val play = LocalPlayVideo.current
    val scope = rememberCoroutineScope()
    var busy by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<Int?>(null) }
    fun start(sh: Boolean) {
        if (busy) return
        busy = true
        scope.launch {
            try {
                PlayerQueues.of(client).create(source, sh)?.let { play(it.id) } ?: run { problem = R.string.queue_empty }
                problem = null
            } catch (e: CancellationException) {
                throw e
            } catch (e: EmptySource) {
                problem = R.string.queue_empty
            } catch (e: Exception) {
                problem = R.string.queue_error_load
            } finally {
                busy = false
            }
        }
    }
    Column(modifier) {
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            if (onlyShuffle) {
                OutlinedButton(onClick = { start(true) }, enabled = !busy, modifier = Modifier.testTag("series-shuffle")) { Text("⤮ " + stringResource(playLabel)) }
                return@Row
            }
            Button(onClick = { start(false) }, enabled = !busy, modifier = Modifier.testTag("library-play")) { Text("▶ " + stringResource(R.string.library_play)) }
            if (shuffle) {
                OutlinedButton(onClick = { start(true) }, enabled = !busy, modifier = Modifier.testTag("library-shuffle")) { Text("⤮ " + stringResource(R.string.library_shuffle)) }
            }
        }
        problem?.let { Text(stringResource(it), color = MaterialTheme.colorScheme.error, modifier = Modifier.testTag("queue-problem")) }
    }
}

/** The library's Collections tab: each with its badge; one opens with Play and Shuffle. */
@Composable
fun CollectionsList(client: ApiClient, library: String) {
    val api = remember(client) { PlayerLibraryApi(client) }
    var items by remember(library) { mutableStateOf<List<PlayerCollection>?>(null) }
    var open by remember(library) { mutableStateOf<PlayerCollection?>(null) }
    LaunchedEffect(library) { items = runCatching { api.collections(library) }.getOrDefault(emptyList()) }
    Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp).testTag("library-collections"), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        val list = items ?: return@Column
        if (list.isEmpty()) Text(stringResource(R.string.collections_empty))
        list.forEach { c ->
            Column(Modifier.fillMaxWidth().clickable { open = if (open == c) null else c }.padding(vertical = 8.dp)) {
                Text(c.name, style = MaterialTheme.typography.titleMedium)
                collectionBadge(c)?.let { Text(stringResource(it), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary) }
                if (open == c) PlayShuffleRow(client, c.queueSource, Modifier.padding(top = 8.dp), shuffle = (c.videos ?: 2) > 1)
            }
        }
    }
}

fun collectionBadge(c: PlayerCollection): Int? = when {
    c.sourceKind == "youtube_playlist" -> R.string.collections_badge_youtube_playlist
    c.sourceKind == "youtube_channel" -> R.string.collections_badge_youtube_channel
    c.sourceKind == "plex" -> R.string.collections_badge_plex
    c.kind == "smart" -> R.string.collections_badge_smart
    else -> null
}

/** The library's Playlists tab: the user's lists with videos here (smart ones too). */
@Composable
fun PlaylistsList(client: ApiClient, library: String) {
    val api = remember(client) { PlayerLibraryApi(client) }
    var items by remember(library) { mutableStateOf<List<PlayerPlaylist>?>(null) }
    var open by remember(library) { mutableStateOf<String?>(null) }
    LaunchedEffect(library) { items = runCatching { api.playlists(library) }.getOrDefault(emptyList()) }
    Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp).testTag("library-playlists"), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        val list = items ?: return@Column
        if (list.isEmpty()) Text(stringResource(R.string.playlists_empty_library))
        list.forEach { p ->
            Column(Modifier.fillMaxWidth().clickable { open = if (open == p.id) null else p.id }.padding(vertical = 8.dp)) {
                Text(p.name, style = MaterialTheme.typography.titleMedium)
                if (p.kind == "smart") Text(stringResource(R.string.playlists_smart_badge), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary)
                if (open == p.id) PlayShuffleRow(client, QueueSource("playlist", p.id), Modifier.padding(top = 8.dp))
            }
        }
    }
}

/** The filter row's Save as smart playlist: a name, then the grid's filters and sort saved. */
@Composable
fun SaveSmartPlaylist(state: LibraryState) {
    val client = com.hamzalabs.maktaba.player.shell.LocalPlayerHome.current.client
    val api = remember(client) { PlayerLibraryApi(client) }
    val scope = rememberCoroutineScope()
    var asking by remember { mutableStateOf(false) }
    var name by remember { mutableStateOf("") }
    var said by remember { mutableStateOf<String?>(null) }
    val failed = stringResource(R.string.playlists_smart_save_error)
    val saved = stringResource(R.string.playlists_smart_saved, "%s")
    androidx.compose.material3.AssistChip(
        onClick = { asking = true },
        label = { Text(stringResource(R.string.playlists_smart_save)) },
        modifier = Modifier.testTag("save-smart"),
    )
    said?.let { Text(it, style = MaterialTheme.typography.labelMedium, modifier = Modifier.testTag("save-smart-said")) }
    if (asking) {
        androidx.compose.material3.AlertDialog(
            onDismissRequest = { asking = false },
            title = { Text(stringResource(R.string.playlists_smart_save)) },
            text = {
                androidx.compose.material3.OutlinedTextField(
                    value = name, onValueChange = { name = it },
                    label = { Text(stringResource(R.string.playlists_smart_name)) },
                    modifier = Modifier.testTag("save-smart-name"),
                )
            },
            confirmButton = {
                androidx.compose.material3.TextButton(
                    onClick = {
                        asking = false
                        scope.launch {
                            said = runCatching { api.saveSmart(name.trim(), state.id, state.queueFilters()) }
                                .fold({ saved.replace("%s", name.trim()) }, { failed })
                        }
                    },
                    enabled = name.isNotBlank(),
                    modifier = Modifier.testTag("save-smart-ok"),
                ) { Text(stringResource(R.string.common_save)) }
            },
            dismissButton = { androidx.compose.material3.TextButton(onClick = { asking = false }) { Text(stringResource(R.string.common_cancel)) } },
        )
    }
}

/** Home's first row (story 56.11 on the phone): the queue last used on any device in a day. */
@Composable
fun rememberContinueQueue(client: ApiClient): com.hamzalabs.maktaba.core.queue.QueueDoc? {
    var doc by remember(client) { mutableStateOf<com.hamzalabs.maktaba.core.queue.QueueDoc?>(null) }
    LaunchedEffect(client) {
        val d = runCatching { PlayerQueues.of(client).fetchCurrent() }.getOrNull() ?: return@LaunchedEffect
        val used = runCatching { java.time.Instant.parse(d.usedAt).toEpochMilli() }.getOrNull() ?: return@LaunchedEffect
        if (System.currentTimeMillis() - used <= 24 * 3_600_000L && d.current != null) doc = d
    }
    return doc
}

@Composable
fun ContinueQueue(client: ApiClient, d: com.hamzalabs.maktaba.core.queue.QueueDoc, open: (String) -> Unit, modifier: Modifier = Modifier) {
    val item = d.current ?: return
    val device = when {
        d.deviceId?.startsWith("tv") == true -> stringResource(R.string.device_tv)
        d.deviceId?.startsWith("web") == true -> stringResource(R.string.device_web)
        d.deviceId?.startsWith("phone") == true -> stringResource(R.string.device_phone)
        else -> null
    }
    Column(modifier.testTag("continue-queue")) {
        Text(stringResource(R.string.home_continue_queue), style = MaterialTheme.typography.titleMedium)
        OutlinedButton(onClick = { PlayerQueues.of(client).resume(d)?.let { open(it.id) } }) {
            Text(if (device != null) stringResource(R.string.home_continue_queue_from, item.name, device) else item.name)
        }
    }
}
