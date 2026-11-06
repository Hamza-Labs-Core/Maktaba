package com.hamzalabs.maktaba.tv.ui.screens.library

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.activity.compose.BackHandler
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.queue.PlayQueueModel
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.data.TvApi
import com.hamzalabs.maktaba.tv.data.TvPlaylist
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton
import com.hamzalabs.maktaba.tv.ui.components.SectionTitle
import com.hamzalabs.maktaba.tv.ui.components.TvTextField
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import kotlinx.coroutines.launch

/**
 * A card's menu (long Select or the Menu key, README §6.5): Play next, Add to queue, Add to
 * playlist (a manual list, or a new one named with the TV's keyboard) and Go to series.
 */
@Composable
fun CardMenu(api: TvApi, video: String, series: String?, onClose: () -> Unit, onDone: (String) -> Unit, queue: PlayQueueModel) {
    val nav = LocalTvNav.current
    val scope = rememberCoroutineScope()
    var lists by remember { mutableStateOf<List<TvPlaylist>?>(null) }
    var naming by remember { mutableStateOf(false) }
    var name by remember { mutableStateOf("") }
    var seriesId by remember { mutableStateOf(series) }
    val first = remember { FocusRequester() }
    val added = stringResource(R.string.queue_added)
    val failed = stringResource(R.string.playlists_add_error)
    val addedTo = stringResource(R.string.playlists_added, "%s")
    LaunchedEffect(video) {
        runCatching { first.requestFocus() }
        if (seriesId == null) seriesId = api.seriesOf(video)?.id
    }
    BackHandler { onClose() }
    Box(Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.6f)), contentAlignment = Alignment.Center) {
        Column(
            Modifier.width(520.dp).background(MaktabaPalette.Raised, RoundedCornerShape(16.dp)).padding(28.dp).testTag("card-menu"),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            fun queueIt(where: String) = scope.launch {
                runCatching { queue.add(listOf(video), where) }.onSuccess { onDone(added) }.onFailure { onDone(failed) }
                onClose()
            }
            val shown = lists
            when {
                naming -> {
                    TvTextField(name, { name = it }, stringResource(R.string.playlists_new_name), Modifier.focusRequester(first))
                    MaktabaButton(onClick = {
                        scope.launch {
                            runCatching { api.createPlaylist(name.trim(), listOf(video)) }
                                .onSuccess { onDone(addedTo.replace("%s", it.name)) }.onFailure { onDone(failed) }
                            onClose()
                        }
                    }, enabled = name.isNotBlank()) { Text(stringResource(R.string.playlists_new)) }
                }
                shown != null -> {
                    SectionTitle(stringResource(R.string.playlists_add))
                    MaktabaButton(onClick = { naming = true }, modifier = Modifier.focusRequester(first)) { Text("+ " + stringResource(R.string.playlists_new)) }
                    shown.filter { !it.smart }.take(12).forEach { p ->
                        MaktabaButton(onClick = {
                            scope.launch {
                                runCatching { api.addToPlaylist(p.id, listOf(video)) }.onSuccess { onDone(addedTo.replace("%s", p.name)) }.onFailure { onDone(failed) }
                                onClose()
                            }
                        }) { Text(p.name) }
                    }
                }
                else -> {
                    MaktabaButton(onClick = { queueIt("next") }, modifier = Modifier.focusRequester(first).testTag("menu-play-next")) { Text(stringResource(R.string.queue_play_next)) }
                    MaktabaButton(onClick = { queueIt("end") }, modifier = Modifier.testTag("menu-add-queue")) { Text(stringResource(R.string.queue_add)) }
                    MaktabaButton(onClick = { scope.launch { lists = runCatching { api.playlists().items }.getOrDefault(emptyList()) } }) {
                        Text(stringResource(R.string.playlists_add))
                    }
                    seriesId?.let { s -> MaktabaButton(onClick = { onClose(); nav.openSeries(s) }) { Text(stringResource(R.string.series_go_to)) } }
                }
            }
        }
    }
}
