package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Card
import androidx.tv.material3.CardDefaults
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.data.TvApi
import com.hamzalabs.maktaba.tv.data.TvVideo
import com.hamzalabs.maktaba.tv.ui.components.CardCorner
import com.hamzalabs.maktaba.tv.ui.components.MediaCard
import com.hamzalabs.maktaba.tv.ui.components.SectionTitle
import com.hamzalabs.maktaba.tv.ui.components.goldFocusBorder
import com.hamzalabs.maktaba.tv.ui.components.maktabaCardColors
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import kotlinx.coroutines.CancellationException

/** The libraries this person can read (the home lists only those). */
@Composable
fun LibrariesScreen(api: TvApi, onOpen: (String, String) -> Unit, signedOut: () -> Unit) {
    val (state, retry) = rememberLoad(api, signedOut) { api.client.libraries() }
    LoadState(state, retry) { libs ->
        if (libs.isEmpty()) {
            Centered { Text(stringResource(R.string.nothing_yet), style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text) }
            return@LoadState
        }
        LazyVerticalGrid(
            columns = GridCells.Adaptive(320.dp),
            modifier = Modifier.fillMaxSize(),
            contentPadding = PaddingValues(horizontal = Gutter, vertical = 32.dp),
            horizontalArrangement = Arrangement.spacedBy(28.dp),
            verticalArrangement = Arrangement.spacedBy(28.dp),
        ) {
            items(libs, key = { it.id }) { lib ->
                Card(
                    onClick = { onOpen(lib.id, lib.name) },
                    modifier = Modifier.fillMaxWidth().height(150.dp),
                    shape = CardDefaults.shape(CardCorner),
                    colors = maktabaCardColors(),
                    scale = CardDefaults.scale(focusedScale = 1.05f),
                    border = goldFocusBorder(),
                ) {
                    Box(Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.BottomStart) {
                        Column {
                            Text("۞", style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Gold)
                            Text(lib.name, style = MaterialTheme.typography.headlineMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        }
                    }
                }
            }
        }
    }
}

/**
 * A library's videos, the newest first, a page at a time: the next page loads as focus nears
 * the end of the grid.
 */
@Composable
fun LibraryScreen(api: TvApi, library: String, name: String, onOpen: (String) -> Unit, signedOut: () -> Unit) {
    Column(Modifier.fillMaxSize().padding(top = 32.dp)) {
        SectionTitle(name, Modifier.padding(horizontal = Gutter))
        VideoGrid(api, key = library, signedOut = signedOut, onOpen = onOpen) { cursor -> api.videos(library = library, cursor = cursor) }
    }
}

/** A paged grid of videos ([page] takes the cursor of the page before, null for the first). */
@Composable
fun VideoGrid(
    api: TvApi,
    key: Any,
    signedOut: () -> Unit,
    onOpen: (String) -> Unit,
    empty: String? = null,
    page: suspend (String?) -> com.hamzalabs.maktaba.core.api.Page<TvVideo>,
) {
    val videos = remember(key) { mutableStateListOf<TvVideo>() }
    var next by remember(key) { mutableStateOf<String?>(null) }
    var done by remember(key) { mutableStateOf(false) }
    var loading by remember(key) { mutableStateOf(true) }
    var problem by remember(key) { mutableStateOf<String?>(null) }
    var want by remember(key) { mutableIntStateOf(0) }

    LaunchedEffect(key, want) {
        if (done) return@LaunchedEffect
        loading = true
        try {
            val p = page(next)
            videos.addAll(p.items.filter { v -> videos.none { it.id == v.id } })
            next = p.next
            done = p.next == null
            problem = null
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException.SignedOut) {
            signedOut()
        } catch (e: Exception) {
            problem = e.message ?: e.toString()
        } finally {
            loading = false
        }
    }

    when {
        videos.isEmpty() && loading -> Centered { Text(stringResource(R.string.loading), style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text) }
        videos.isEmpty() && problem != null -> LoadState(Load.Failed(problem!!), retry = { want++ }) {}
        videos.isEmpty() -> Centered { Text(empty ?: stringResource(R.string.nothing_yet), style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text) }
        else -> LazyVerticalGrid(
            columns = GridCells.Adaptive(300.dp),
            modifier = Modifier.fillMaxSize(),
            contentPadding = PaddingValues(horizontal = Gutter, vertical = 28.dp),
            horizontalArrangement = Arrangement.spacedBy(28.dp),
            verticalArrangement = Arrangement.spacedBy(32.dp),
        ) {
            items(videos.size, key = { videos[it].id }) { i ->
                val v = videos[i]
                // near the end: the next page
                if (i >= videos.size - 8 && !done && !loading && problem == null) LaunchedEffect(videos.size) { want++ }
                MediaCard(
                    client = api.client,
                    video = v.id,
                    title = v.displayTitle,
                    subtitle = v.durationSec?.let { clock(it) },
                    width = 300.dp,
                    onClick = { onOpen(v.id) },
                )
            }
        }
    }
}
