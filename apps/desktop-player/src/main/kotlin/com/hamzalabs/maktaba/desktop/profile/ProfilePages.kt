package com.hamzalabs.maktaba.desktop.profile

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
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
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Close
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.HistoryEntry
import com.hamzalabs.maktaba.core.api.PlaylistDoc
import com.hamzalabs.maktaba.core.api.SignInSession
import com.hamzalabs.maktaba.desktop.BuildInfo
import com.hamzalabs.maktaba.desktop.EmptyBox
import com.hamzalabs.maktaba.desktop.ErrorBox
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.Loading
import com.hamzalabs.maktaba.desktop.LocalTexts
import com.hamzalabs.maktaba.desktop.PlayRequest
import com.hamzalabs.maktaba.desktop.PosterCard
import com.hamzalabs.maktaba.desktop.PosterImage
import com.hamzalabs.maktaba.desktop.ProgressStrip
import com.hamzalabs.maktaba.desktop.choosable
import com.hamzalabs.maktaba.desktop.formatDuration
import com.hamzalabs.maktaba.desktop.shell.DesktopHome
import com.hamzalabs.maktaba.desktop.shell.LocalDesktopHome
import com.hamzalabs.maktaba.desktop.shell.Route
import kotlinx.coroutines.launch

@Composable
internal fun AccountPage(state: ProfileState, home: DesktopHome) {
    val t = LocalTexts.current
    var confirm by remember { mutableStateOf(false) }
    LaunchedEffect(state) { state.loadAbout() }
    Text(t["profile.signedIn", home.user.username, home.homeLabel], style = MaterialTheme.typography.bodyLarge, modifier = Modifier.testTag("signed-in"))
    state.cloud?.takeIf { it.available || it.linked }?.let { c ->
        Text(
            if (c.linked) t["profile.cloudLinked", c.linkedLabel ?: home.user.username] else t["profile.cloudNotLinked"],
            color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.testTag("cloud-link"),
        )
    }
    Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        OutlinedButton(onClick = home.actions.switchHome, modifier = Modifier.testTag("switch-home")) { Text(t["profile.switchHome"]) }
        Button(onClick = { confirm = true }, modifier = Modifier.testTag("sign-out")) { Text(t["profile.signOut"]) }
    }
    if (confirm) {
        AlertDialog(
            onDismissRequest = { confirm = false },
            title = { Text(t["profile.signOutTitle", home.homeLabel]) },
            text = { Text(t["profile.signOutBody"]) },
            confirmButton = { TextButton(onClick = { confirm = false; home.actions.signOut() }, modifier = Modifier.testTag("sign-out-confirm")) { Text(t["profile.signOut"]) } },
            dismissButton = { TextButton(onClick = { confirm = false }) { Text(t["common.cancel"]) } },
        )
    }
}

@Composable
private fun Failed(on: Boolean) {
    if (on) Text(LocalTexts.current["profile.failed"], color = MaterialTheme.colorScheme.error, modifier = Modifier.testTag("profile-failed"))
}

@Composable
internal fun HistoryPage(state: ProfileState, home: DesktopHome) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    val list = state.history
    LaunchedEffect(state) { state.loadHistory() }
    val problem = list.problem
    Failed(list.failed)
    when {
        !list.loaded && problem != null -> ErrorBox(t["profile.history"], problem, onRetry = { scope.launch { state.loadHistory() } })
        !list.loaded -> Loading()
        list.items.isEmpty() -> EmptyBox(t["profile.historyEmpty"])
        else -> LazyColumn(Modifier.fillMaxSize().testTag("history")) {
            items(list.items, key = { it.videoId }) { e -> HistoryRow(e, home) { scope.launch { state.forget(e) } } }
            if (list.more) item { TextButton(onClick = { scope.launch { state.moreHistory() } }, modifier = Modifier.testTag("history-more")) { Text(t["profile.loadMore"]) } }
        }
    }
}

@Composable
private fun HistoryRow(e: HistoryEntry, home: DesktopHome, onRemove: () -> Unit) {
    val t = LocalTexts.current
    // a removed video has no title: it shows without a poster and does not open
    val gone = e.title.isBlank()
    val open = { if (!gone) home.nav.push(Route.Video(e.videoId)) }
    Row(
        Modifier.fillMaxWidth().then(if (gone) Modifier else Modifier.choosable(open)).padding(8.dp).testTag("history-${e.videoId}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Box(Modifier.width(160.dp).aspectRatio(16f / 9f).clip(RoundedCornerShape(8.dp))) {
            PosterImage(home.posters, e.videoId.takeIf { !gone }, Modifier.fillMaxSize())
            e.watchedFraction?.takeIf { it < 1.0 }?.let { ProgressStrip(it.toFloat(), Modifier.align(Alignment.BottomCenter)) }
        }
        Column(Modifier.weight(1f)) {
            Text(e.title.ifBlank { e.videoId }, style = MaterialTheme.typography.titleSmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
            e.lastWatchedAt?.let { Text(it.take(10), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        }
        if (e.completed) Icon(Icons.Filled.CheckCircle, t["profile.historyWatched"], tint = MaterialTheme.colorScheme.primary)
        IconButton(onClick = onRemove, modifier = Modifier.testTag("history-remove-${e.videoId}")) { Icon(Icons.Filled.Close, t["profile.historyRemove", e.title]) }
    }
    HorizontalDivider()
}

@Composable
internal fun FavoritesPage(state: ProfileState, home: DesktopHome) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    val list = state.favorites
    LaunchedEffect(state) { state.loadFavorites() }
    val problem = list.problem
    Failed(list.failed)
    when {
        !list.loaded && problem != null -> ErrorBox(t["profile.favorites"], problem, onRetry = { scope.launch { state.loadFavorites() } })
        !list.loaded -> Loading()
        list.items.isEmpty() -> EmptyBox(t["profile.favoritesEmpty"])
        else -> LazyVerticalGrid(GridCells.Adaptive(200.dp), Modifier.fillMaxSize().testTag("favorites"), horizontalArrangement = Arrangement.spacedBy(12.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            items(list.items, key = { it.id }) { v ->
                Box {
                    PosterCard(v.displayTitle, home.posters, v.id, onClick = { home.nav.push(Route.Video(v.id)) }, width = null, modifier = Modifier.testTag("favorite-${v.id}"))
                    IconButton(onClick = { scope.launch { state.unfavorite(v) } }, modifier = Modifier.align(Alignment.TopEnd).testTag("favorite-remove-${v.id}")) {
                        Icon(Icons.Filled.Close, t["profile.favoriteRemove", v.displayTitle])
                    }
                }
            }
            if (list.more) item(span = { GridItemSpan(maxLineSpan) }) { TextButton(onClick = { scope.launch { state.moreFavorites() } }) { Text(t["profile.loadMore"]) } }
        }
    }
}

@Composable
internal fun PlaylistsPage(state: ProfileState, home: DesktopHome) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    val list = state.playlists
    LaunchedEffect(state) { state.loadPlaylists() }
    val problem = list.problem
    when {
        !list.loaded && problem != null -> ErrorBox(t["profile.playlists"], problem, onRetry = { scope.launch { state.loadPlaylists() } })
        !list.loaded -> Loading()
        list.items.isEmpty() || list.missing -> EmptyBox(t["profile.playlistsEmpty"])
        else -> LazyColumn(Modifier.fillMaxSize().testTag("playlists")) {
            items(list.items, key = { it.id }) { p ->
                ListItem(
                    headlineContent = { Text(p.name) },
                    supportingContent = { Text(t["lib.summary", p.videos, formatDuration(p.durationSec)]) },
                    leadingContent = { PosterImage(home.posters, p.posterVideoIds.firstOrNull(), Modifier.width(96.dp).aspectRatio(16f / 9f).clip(RoundedCornerShape(6.dp))) },
                    modifier = Modifier.choosable({ home.nav.push(Route.Playlist(p.id)) }).testTag("playlist-${p.id}"),
                )
            }
        }
    }
}

/** A browser's name from its user agent ("Firefox", "Edge", "Chrome", "Safari"), else the agent itself. */
fun browserName(agent: String?): String {
    val a = agent.orEmpty()
    return when {
        "Edg/" in a -> "Edge"
        "Firefox/" in a -> "Firefox"
        "OPR/" in a -> "Opera"
        "Chrome/" in a -> "Chrome"
        "Safari/" in a -> "Safari"
        else -> a.take(40)
    }
}

@Composable
internal fun SessionsPage(state: ProfileState) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    val list = state.sessions
    LaunchedEffect(state) { state.loadSessions() }
    val problem = list.problem
    Text(t["profile.sessionsAbout"], color = MaterialTheme.colorScheme.onSurfaceVariant)
    Failed(list.failed)
    when {
        !list.loaded && problem != null -> ErrorBox(t["profile.sessions"], problem, onRetry = { scope.launch { state.loadSessions() } })
        !list.loaded -> Loading()
        list.items.isEmpty() -> EmptyBox(t["profile.sessionsEmpty"])
        else -> LazyColumn(Modifier.fillMaxSize().testTag("sessions")) {
            items(list.items, key = { it.id }) { s: SignInSession ->
                ListItem(
                    headlineContent = { Text(browserName(s.userAgent).ifBlank { "—" }) },
                    supportingContent = { Text(listOfNotNull(s.ip, s.lastSeenAt?.take(16)?.replace('T', ' ')).joinToString(" · ")) },
                    trailingContent = {
                        TextButton(onClick = { scope.launch { state.endSession(s) } }, modifier = Modifier.testTag("session-end-${s.id}")) { Text(t["profile.sessionSignOut"]) }
                    },
                    modifier = Modifier.testTag("session-${s.id}"),
                )
            }
        }
    }
}

@Composable
internal fun LanguagePage(home: DesktopHome) {
    for (l in Lang.entries) {
        Row(
            Modifier.fillMaxWidth().selectable(home.prefs.lang == l, role = Role.RadioButton, onClick = { home.prefs.chooseLang(l) }).padding(8.dp).testTag("lang-${l.code}"),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            RadioButton(selected = home.prefs.lang == l, onClick = null)
            Text(if (l == Lang.Ar) "العربية" else "English", modifier = Modifier.padding(start = 8.dp))
        }
    }
}

@Composable
internal fun HelpPage(state: ProfileState, home: DesktopHome) {
    val t = LocalTexts.current
    LaunchedEffect(state) { state.loadAbout() }
    Text(t["profile.version", BuildInfo.VERSION], modifier = Modifier.testTag("app-version"))
    Text(t["profile.homeVersion", state.homeVersion ?: t["profile.unknown"]], modifier = Modifier.testTag("home-version"))
    Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        OutlinedButton(onClick = { home.actions.openUrl("${home.webUrl}/help/desktop-player") }, modifier = Modifier.testTag("help")) { Text(t["menu.help"]) }
        OutlinedButton(onClick = home.actions.updates, modifier = Modifier.testTag("updates")) { Text(t["menu.updates"]) }
    }
}

/** A playlist's page (story 55.09): its videos, and Play, which plays them in order with Up next between them. */
@Composable
fun PlaylistPage(id: String) {
    val t = LocalTexts.current
    val home = LocalDesktopHome.current
    val state = home.profileState()
    val doc by produceState<Result<PlaylistDoc>?>(null, id) { value = runCatching { state.playlist(id) } }
    val d = doc
    when {
        d == null -> Loading()
        d.isFailure -> ErrorBox(t["profile.playlists"], d.exceptionOrNull(), onRetry = null)
        else -> {
            val p = d.getOrThrow()
            Column(Modifier.fillMaxSize().padding(16.dp).testTag("playlist-page"), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text(p.playlist.name, style = MaterialTheme.typography.headlineSmall)
                if (p.items.isNotEmpty()) {
                    Button(onClick = { home.play(playlistRequest(p)) }, modifier = Modifier.testTag("playlist-play")) { Text(t["profile.playlistPlay"]) }
                }
                LazyVerticalGrid(GridCells.Adaptive(220.dp), contentPadding = PaddingValues(vertical = 8.dp), horizontalArrangement = Arrangement.spacedBy(12.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    items(p.items, key = { it.id }) { v ->
                        PosterCard(v.displayTitle, home.posters, v.id, onClick = { home.nav.push(Route.Video(v.id)) }, width = null, progress = v.watchedFraction?.toFloat(), watched = v.completed == true)
                    }
                }
            }
        }
    }
}

/** A playlist's videos as one request: the first, then the rest in order (Up next offers each). */
fun playlistRequest(p: PlaylistDoc): PlayRequest {
    val all = p.items.map { PlayRequest(it.id, it.displayTitle, it.durationSec, startSec = 0) }
    return all.first().copy(then = all.drop(1))
}
