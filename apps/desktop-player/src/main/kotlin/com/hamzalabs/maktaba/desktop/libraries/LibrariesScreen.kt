package com.hamzalabs.maktaba.desktop.libraries

import androidx.compose.foundation.ContextMenuArea
import androidx.compose.foundation.ContextMenuItem
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.PushPin
import androidx.compose.material.icons.outlined.PushPin
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
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
import com.hamzalabs.maktaba.core.api.LibrarySummary
import com.hamzalabs.maktaba.desktop.EmptyBox
import com.hamzalabs.maktaba.desktop.ErrorBox
import com.hamzalabs.maktaba.desktop.Loading
import com.hamzalabs.maktaba.desktop.LocalTexts
import com.hamzalabs.maktaba.desktop.PosterImage
import com.hamzalabs.maktaba.desktop.choosable
import com.hamzalabs.maktaba.desktop.formatDuration
import com.hamzalabs.maktaba.desktop.shell.DesktopHome
import com.hamzalabs.maktaba.desktop.shell.LocalDesktopHome
import com.hamzalabs.maktaba.desktop.shell.Route
import kotlinx.coroutines.launch

/** This home's Libraries state, kept while it is signed in. */
fun DesktopHome.librariesState(): LibrariesState = keep("libraries") { LibrariesState(client, ::signedOut) }

/** The Libraries section (story 55.05): All videos, then the pinned, then the rest. */
@Composable
fun LibrariesScreen() {
    val home = LocalDesktopHome.current
    val state = home.librariesState()
    LaunchedEffect(state) { state.load() }
    LibrariesContent(state, home)
}

@Composable
fun LibrariesContent(state: LibrariesState, home: DesktopHome) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    val problem = state.problem
    when {
        !state.loaded && problem != null -> ErrorBox(t["section.libraries"], problem, onRetry = { scope.launch { state.load() } })
        !state.loaded -> Loading()
        state.libraries.isEmpty() -> EmptyBox(t["lib.none"])
        else -> {
            val (pinned, rest) = state.libraries.partition { it.pinned }
            LazyVerticalGrid(
                GridCells.Adaptive(240.dp),
                Modifier.fillMaxSize().testTag("libraries"),
                contentPadding = PaddingValues(16.dp),
                horizontalArrangement = Arrangement.spacedBy(16.dp),
                verticalArrangement = Arrangement.spacedBy(16.dp),
            ) {
                if (state.saveFailed) item(span = { GridItemSpan(maxLineSpan) }) {
                    Text(t["lib.saveFailed"], color = MaterialTheme.colorScheme.error, modifier = Modifier.testTag("save-failed"))
                }
                item(key = "all") {
                    LibraryCard(t["lib.allVideos"], null, null, pinned = null, home, onOpen = { home.nav.push(Route.Library(null)) }, onPin = {})
                }
                if (pinned.isNotEmpty()) {
                    item(span = { GridItemSpan(maxLineSpan) }) {
                        Text(t["lib.pinned"], style = MaterialTheme.typography.titleMedium, modifier = Modifier.semantics { heading() }.testTag("pinned"))
                    }
                    items(pinned, key = { "p-" + it.id }) { lib -> SummaryCard(lib, home) { scope.launch { state.togglePin(lib.id) } } }
                    item(span = { GridItemSpan(maxLineSpan) }) {
                        Text(t["section.libraries"], style = MaterialTheme.typography.titleMedium, modifier = Modifier.semantics { heading() })
                    }
                }
                items(rest, key = { it.id }) { lib -> SummaryCard(lib, home) { scope.launch { state.togglePin(lib.id) } } }
            }
        }
    }
}

@Composable
private fun SummaryCard(lib: LibrarySummary, home: DesktopHome, onPin: () -> Unit) {
    val t = LocalTexts.current
    val line = t["lib.summary", lib.videos, formatDuration(lib.durationSec)]
    LibraryCard(lib.name, line, lib.posterVideoIds.firstOrNull(), lib.pinned, home, onOpen = { home.nav.push(Route.Library(lib.id)) }, onPin = onPin, tag = lib.id)
}

@Composable
private fun LibraryCard(name: String, line: String?, poster: String?, pinned: Boolean?, home: DesktopHome, onOpen: () -> Unit, onPin: () -> Unit, tag: String = "all") {
    val t = LocalTexts.current
    val pinLabel = if (pinned == true) t["lib.unpin", name] else t["lib.pin", name]
    val card = @Composable {
        Box(Modifier.testTag("library-$tag")) {
            Column(Modifier.fillMaxWidth().choosable(onOpen).padding(4.dp)) {
                PosterImage(home.posters, poster, Modifier.fillMaxWidth().aspectRatio(16f / 9f).clip(RoundedCornerShape(8.dp)))
                Text(name, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 6.dp))
                line?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
            }
            if (pinned != null) {
                IconButton(onClick = onPin, modifier = Modifier.align(Alignment.TopEnd).testTag("pin-$tag")) {
                    Icon(if (pinned) Icons.Filled.PushPin else Icons.Outlined.PushPin, pinLabel, tint = MaterialTheme.colorScheme.primary)
                }
            }
        }
    }
    if (pinned == null) card() else ContextMenuArea(items = { listOf(ContextMenuItem(pinLabel, onPin)) }) { card() }
}
