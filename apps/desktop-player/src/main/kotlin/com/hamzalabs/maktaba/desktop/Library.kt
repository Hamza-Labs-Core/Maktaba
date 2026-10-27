package com.hamzalabs.maktaba.desktop

import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.focusable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationDrawerItem
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.toComposeImageBitmap
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.Library
import com.hamzalabs.maktaba.core.api.Video
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import org.jetbrains.skia.Image as SkiaImage

/** Posters already fetched, a few hundred at most (a library is browsed page by page). */
class Posters(private val client: ApiClient, private val max: Int = 300) {
    private val cache = object : LinkedHashMap<String, ImageBitmap?>(64, 0.75f, true) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, ImageBitmap?>) = size > max
    }

    suspend fun get(video: String): ImageBitmap? {
        synchronized(cache) { if (cache.containsKey(video)) return cache[video] }
        val img = runCatching { SkiaImage.makeFromEncoded(client.poster(video)).toComposeImageBitmap() }.getOrNull()
        synchronized(cache) { cache[video] = img }
        return img
    }
}

/**
 * Browsing the home: its libraries (All videos first) beside the videos of the one chosen, a
 * search over titles, and more on demand. Choosing a video plays it ([onPlay]).
 */
@Composable
fun LibraryBrowser(client: ApiClient, posters: Posters, onPlay: (Video) -> Unit, onSignedOut: () -> Unit) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    var libraries by remember { mutableStateOf<List<Library>>(emptyList()) }
    var library by remember { mutableStateOf<String?>(null) }
    var query by remember { mutableStateOf("") }
    var searched by remember { mutableStateOf<String?>(null) }
    val videos = remember { mutableStateListOf<Video>() }
    var next by remember { mutableStateOf<String?>(null) }
    var loading by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }

    suspend fun guard(block: suspend () -> Unit) {
        try {
            problem = null
            block()
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException.SignedOut) {
            onSignedOut()
        } catch (e: Exception) {
            problem = e.message ?: t["common.error"]
        }
    }

    LaunchedEffect(client) { guard { libraries = client.libraries() } }
    LaunchedEffect(client, library, searched) {
        loading = true
        guard {
            val page = client.videos(library, searched)
            videos.clear()
            videos.addAll(page.items)
            next = page.next
        }
        loading = false
    }

    Row(Modifier.fillMaxSize()) {
        LazyColumn(Modifier.width(240.dp).fillMaxHeight().background(MaterialTheme.colorScheme.surfaceContainerLow).padding(8.dp)) {
            item {
                Text(
                    t["nav.libraries"], style = MaterialTheme.typography.titleMedium,
                    modifier = Modifier.padding(12.dp).semantics { heading() },
                )
            }
            item {
                NavigationDrawerItem(label = { Text(t["nav.all"]) }, selected = library == null, onClick = { library = null })
            }
            items(libraries, key = { it.id }) { lib ->
                NavigationDrawerItem(label = { Text(lib.name, maxLines = 1, overflow = TextOverflow.Ellipsis) }, selected = library == lib.id, onClick = { library = lib.id })
            }
        }
        Column(Modifier.weight(1f).fillMaxHeight().padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            OutlinedTextField(
                query, { query = it }, singleLine = true,
                label = { Text(t["nav.search"]) },
                placeholder = { Text(t["lib.searchHint"]) },
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                keyboardActions = KeyboardActions(onSearch = { searched = query.trim().ifEmpty { null } }),
                modifier = Modifier.fillMaxWidth().onKeyEvent {
                    if (it.type == KeyEventType.KeyUp && it.key == Key.Enter) {
                        searched = query.trim().ifEmpty { null }
                        true
                    } else false
                },
            )
            problem?.let { Problem(it) }
            if (!loading && videos.isEmpty() && problem == null) {
                Text(t["lib.empty"], color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            LazyVerticalGrid(GridCells.Adaptive(220.dp), horizontalArrangement = Arrangement.spacedBy(12.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
                items(videos, key = { it.id }) { v -> VideoCard(v, posters, onClick = { onPlay(v) }) }
                if (loading) item(span = { GridItemSpan(maxLineSpan) }) {
                    Box(Modifier.fillMaxWidth().padding(16.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
                } else if (next != null) item(span = { GridItemSpan(maxLineSpan) }) {
                    Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
                        Button(onClick = {
                            scope.launch {
                                loading = true
                                guard {
                                    val page = client.videos(library, searched, next)
                                    videos.addAll(page.items.filter { n -> videos.none { it.id == n.id } })
                                    next = page.next
                                }
                                loading = false
                            }
                        }) { Text(t["lib.more"]) }
                    }
                }
            }
        }
    }
}

/** A video's poster and title; Enter or a click plays it, and the focus is drawn in brass (big screen's D-pad). */
@Composable
fun VideoCard(v: Video, posters: Posters, onClick: () -> Unit) {
    val poster by produceState<ImageBitmap?>(null, v.id) { value = posters.get(v.id) }
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val shape = RoundedCornerShape(10.dp)
    Column(
        Modifier
            .clip(shape)
            .border(if (focused) 3.dp else 0.dp, if (focused) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.surface, shape)
            .onKeyEvent {
                if (it.type == KeyEventType.KeyUp && (it.key == Key.Enter || it.key == Key.NumPadEnter)) {
                    onClick()
                    true
                } else false
            }
            .clickable(interactionSource = interaction, indication = null, onClick = onClick)
            .focusable(interactionSource = interaction)
            .semantics { role = Role.Button }
            .padding(4.dp),
    ) {
        Box(Modifier.fillMaxWidth().aspectRatio(16f / 9f).clip(RoundedCornerShape(8.dp)).background(MaterialTheme.colorScheme.surfaceContainerHigh)) {
            poster?.let { Image(it, contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize()) }
        }
        Text(
            v.displayTitle, maxLines = 2, overflow = TextOverflow.Ellipsis,
            style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(top = 6.dp),
        )
        v.durationSec?.takeIf { it > 0 }?.let {
            Text(formatDuration(it), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

/** 1:02:03 or 4:05, read left to right in either language. */
fun formatDuration(seconds: Double): String {
    val s = seconds.toLong().coerceAtLeast(0)
    val h = s / 3600
    val m = (s % 3600) / 60
    val sec = s % 60
    val text = if (h > 0) "%d:%02d:%02d".format(h, m, sec) else "%d:%02d".format(m, sec)
    return "⁦$text⁩"
}
