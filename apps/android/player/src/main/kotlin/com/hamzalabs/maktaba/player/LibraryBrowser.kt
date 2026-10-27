package com.hamzalabs.maktaba.player

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.ArrowDropDown
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.outlined.GridView
import androidx.compose.material.icons.outlined.VideoLibrary
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.VerticalDivider
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.diagnostics.AppLog
import com.hamzalabs.maktaba.core.api.Library
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.core.api.Video
import com.hamzalabs.maktaba.core.layout.HingeSplit
import com.hamzalabs.maktaba.core.layout.ListPaneWidth
import com.hamzalabs.maktaba.core.layout.Panes
import com.hamzalabs.maktaba.core.layout.currentPanes
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.R as CoreR
import com.hamzalabs.maktaba.core.ui.AccountMenu
import com.hamzalabs.maktaba.core.ui.ErrorState
import com.hamzalabs.maktaba.core.ui.sentence
import com.hamzalabs.maktaba.core.ui.Message
import com.hamzalabs.maktaba.core.ui.Poster
import com.hamzalabs.maktaba.core.ui.ThemeStore
import com.hamzalabs.maktaba.core.ui.formatDuration
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** The libraries, their videos and the chosen one: what every layout of the browser shows. */
class BrowserState(
    private val client: ApiClient,
    private val signedOut: () -> Unit,
    /** "Choose another home", offered when this one does not answer. */
    val chooseHome: (() -> Unit)? = null,
) {
    var libraries by mutableStateOf<List<Library>>(emptyList())
    /** null: all videos. */
    var library by mutableStateOf<String?>(null)
    val videos = mutableStateListOf<Video>()
    var next by mutableStateOf<String?>(null)
    var video by mutableStateOf<Video?>(null)
    var query by mutableStateOf("")
    /** Why the last load failed (in a person's words through [Failure.kind]; never the home's raw answer). */
    var problem by mutableStateOf<Failure?>(null)
    /** The page that failed to load after the first: Try again asks for it once more. */
    private var failedCursor: String? = null
    var loading by mutableStateOf(false)
    var loaded by mutableStateOf(false)

    val title: String get() = libraries.firstOrNull { it.id == library }?.name ?: "All videos"

    suspend fun loadLibraries() = guard { libraries = client.libraries() }

    suspend fun reload(pickFirst: Boolean) = guard {
        loading = true
        try {
            val page = client.videos(library, query)
            videos.clear()
            videos.addAll(page.items)
            next = page.next
            problem = null
            loaded = true
            // a wide window shows a page, not an empty pane
            if (pickFirst && (video == null || videos.none { it.id == video?.id })) video = videos.firstOrNull()
        } finally {
            loading = false
        }
    }

    suspend fun loadMore() {
        val cursor = next ?: return
        next = null
        failedCursor = null
        guard {
            try {
                val page = client.videos(library, query, cursor)
                videos.addAll(page.items.filter { v -> videos.none { it.id == v.id } })
                next = page.next
                problem = null
            } catch (e: kotlinx.coroutines.CancellationException) {
                next = cursor // left mid-page: the list's end asks again when it shows
                throw e
            } catch (e: Exception) {
                failedCursor = cursor
                throw e
            }
        }
    }

    /**
     * Asks again for the page after the list's end, after it failed (its row's Try again);
     * false when no page failed (a refresh did: reload instead).
     */
    fun retryMore(): Boolean {
        val cursor = failedCursor ?: return false
        failedCursor = null
        problem = null
        next = cursor
        return true
    }

    private suspend fun guard(block: suspend () -> Unit) {
        try {
            block()
        } catch (e: ApiException.SignedOut) {
            signedOut()
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (e: Exception) {
            AppLog.failure("player browser", e)
            problem = Failure.of(e)
        }
    }
}

/**
 * Libraries, their videos, a video, and what is playing. The layout follows the window:
 * - one pane (a phone, a foldable folded): the list, then a video's page, the player full screen;
 * - wide (a tablet, a foldable open flat): the list beside the page, the player full screen;
 * - half folded (tabletop or book): split at the hinge, what is looked at on the first half
 *   and what is touched on the second. Nothing chosen: the libraries | their videos; a video
 *   chosen: its page | the videos; playing: the player | the videos, where a tap opens that
 *   video's page (play there switches what plays), so browsing goes on beside the video.
 */
@Composable
fun LibraryBrowser(
    session: AppSession,
    user: User,
    client: ApiClient,
    themes: ThemeStore,
    onPlaying: (Boolean) -> Unit,
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val app = context.applicationContext as PlayerApplication
    val playback = remember(client) { Playback(app, client, scope, app.background, session::signedOut) }
    val state = remember(client) { BrowserState(client, session::signedOut, session::switchHome) }
    val panes = currentPanes()

    DisposableEffect(playback) { onDispose { playback.stop() } }
    LifecycleEventEffect(Lifecycle.Event.ON_STOP) { playback.player?.pause() }
    LaunchedEffect(playback.video != null) { onPlaying(playback.video != null) }
    LaunchedEffect(client) { state.loadLibraries() }
    LaunchedEffect(client, state.library, state.query) {
        if (state.query.isNotEmpty()) delay(300)
        state.reload(pickFirst = panes == Panes.ListDetail)
    }

    val account: @Composable () -> Unit = { AccountMenu(session, user, themes) }
    when (panes) {
        is Panes.Tabletop, is Panes.Book -> SplitBrowser(panes, state, playback, client, account, session)
        else -> Box(Modifier.fillMaxSize()) {
            if (panes == Panes.ListDetail) {
                Row(Modifier.fillMaxSize()) {
                    VideoList(state, client, account, onOpen = { state.video = it }, Modifier.width(ListPaneWidth).fillMaxHeight())
                    VerticalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                    Box(Modifier.weight(1f).fillMaxHeight()) {
                        val v = state.video
                        if (v != null) VideoPage(client, v, onPlay = { playback.play(v) }) else Message("Choose a video")
                    }
                }
            } else {
                val v = state.video
                if (v == null) {
                    VideoList(state, client, account, onOpen = { state.video = it }, Modifier.fillMaxSize())
                } else {
                    BackHandler { state.video = null }
                    VideoPage(client, v, onPlay = { playback.play(v) }, onBack = { state.video = null })
                }
            }
            if (playback.video != null) {
                BackHandler { playback.stop() }
                WatchScreen(playback, fullScreen = true, onSignIn = session::signedOut, onChooseHome = session::switchHome)
            }
        }
    }
}

@Composable
private fun SplitBrowser(
    panes: Panes,
    state: BrowserState,
    playback: Playback,
    client: ApiClient,
    account: @Composable () -> Unit,
    session: AppSession,
) {
    /** A page opened from the list while the player holds the other half. */
    var browsing by remember { mutableStateOf<Video?>(null) }
    // a video that starts playing is the page to come back to when it stops
    LaunchedEffect(playback.video?.id) {
        browsing = null
        playback.video?.let { state.video = it }
    }
    HingeSplit(
        panes,
        first = {
            val v = state.video
            when {
                playback.video != null -> {
                    BackHandler { playback.stop() }
                    WatchScreen(playback, fullScreen = false, onSignIn = session::signedOut, onChooseHome = session::switchHome)
                }
                v != null -> {
                    BackHandler { state.video = null }
                    VideoPage(client, v, onPlay = { playback.play(v) }, onBack = { state.video = null })
                }
                else -> LibraryChoices(state)
            }
        },
        second = {
            val b = browsing
            if (b != null) {
                BackHandler { browsing = null }
                VideoPage(client, b, onPlay = { playback.play(b) }, onBack = { browsing = null })
            } else {
                VideoList(
                    state, client, account,
                    onOpen = { if (playback.video != null) browsing = it else state.video = it },
                    Modifier.fillMaxSize(),
                    showLibraryMenu = playback.video != null || state.video != null,
                    chosen = (playback.video ?: state.video)?.id,
                )
            }
        },
    )
}

/** Half folded with nothing chosen: "All videos" and each library, to pick from. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun LibraryChoices(state: BrowserState) {
    Scaffold(topBar = { TopAppBar(title = { Text("Libraries") }) }) { padding ->
        LazyColumn(Modifier.padding(padding)) {
            item { LibraryRow("All videos", Icons.Outlined.GridView, state.library == null) { state.library = null } }
            items(state.libraries, key = { it.id }) { lib ->
                LibraryRow(lib.name, Icons.Outlined.VideoLibrary, state.library == lib.id) { state.library = lib.id }
            }
        }
    }
}

@Composable
private fun LibraryRow(name: String, icon: androidx.compose.ui.graphics.vector.ImageVector, chosen: Boolean, onClick: () -> Unit) {
    ListItem(
        headlineContent = { Text(name) },
        leadingContent = { Icon(icon, null, tint = MaterialTheme.colorScheme.primary) },
        colors = ListItemDefaults.colors(
            containerColor = if (chosen) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surface,
        ),
        modifier = Modifier.clickable(onClick = onClick),
    )
}

/** The videos of the chosen library: a title bar with the library menu, search, the list. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun VideoList(
    state: BrowserState,
    client: ApiClient,
    account: @Composable () -> Unit,
    onOpen: (Video) -> Unit,
    modifier: Modifier = Modifier,
    showLibraryMenu: Boolean = true,
    chosen: String? = state.video?.id,
) {
    val scope = rememberCoroutineScope()
    var menu by remember { mutableStateOf(false) }
    Scaffold(
        modifier = modifier,
        topBar = {
            TopAppBar(
                title = {
                    if (showLibraryMenu) {
                        Box {
                            Row(Modifier.clickable { menu = true }, verticalAlignment = Alignment.CenterVertically) {
                                Text(state.title, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                Icon(Icons.Filled.ArrowDropDown, "Choose a library")
                            }
                            DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                                DropdownMenuItem(text = { Text("All videos") }, onClick = { state.library = null; menu = false })
                                state.libraries.forEach { lib ->
                                    DropdownMenuItem(text = { Text(lib.name) }, onClick = { state.library = lib.id; menu = false })
                                }
                            }
                        }
                    } else {
                        Text(state.title, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                },
                actions = { account() },
            )
        },
    ) { padding ->
        Column(Modifier.padding(padding).fillMaxSize()) {
            OutlinedTextField(
                value = state.query,
                onValueChange = { state.query = it },
                placeholder = { Text("Search titles") },
                leadingIcon = { Icon(Icons.Filled.Search, null) },
                singleLine = true,
                modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 4.dp),
            )
            PullToRefreshBox(
                isRefreshing = state.loading && state.loaded,
                onRefresh = { scope.launch { state.reload(pickFirst = false) } },
                modifier = Modifier.fillMaxSize(),
            ) {
                when {
                    state.problem != null && state.videos.isEmpty() -> state.problem?.let { f ->
                        ErrorState(
                            f,
                            title = stringResource(CoreR.string.error_title),
                            onRetry = { scope.launch { state.loadLibraries(); state.reload(pickFirst = false) } },
                            onChooseHome = state.chooseHome,
                        )
                    }
                    state.loaded && state.videos.isEmpty() -> Message("No videos")
                    !state.loaded && state.videos.isEmpty() -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                        CircularProgressIndicator()
                    }
                    else -> LazyColumn(Modifier.fillMaxSize()) {
                        itemsIndexed(state.videos, key = { _, v -> v.id }) { i, v ->
                            VideoRow(client, v, chosen = v.id == chosen) { onOpen(v) }
                            if (i == state.videos.lastIndex && state.next != null) {
                                LaunchedEffect(state.next) { state.loadMore() }
                            }
                        }
                        // the next page failed: say so where the list ends, and ask again on a tap
                        state.problem?.let { f ->
                            item(key = "more-failed") {
                                Row(
                                    Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp),
                                    verticalAlignment = Alignment.CenterVertically,
                                ) {
                                    Text(
                                        stringResource(f.kind.sentence()),
                                        style = MaterialTheme.typography.bodyMedium,
                                        color = MaterialTheme.colorScheme.error,
                                        modifier = Modifier.weight(1f),
                                    )
                                    TextButton(onClick = { if (!state.retryMore()) scope.launch { state.reload(pickFirst = false) } }) { Text(stringResource(CoreR.string.try_again)) }
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun VideoRow(client: ApiClient, video: Video, chosen: Boolean, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth()
            .background(if (chosen) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surface)
            .clickable(onClick = onClick)
            .padding(horizontal = 12.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Poster(client, video.id, Modifier.width(120.dp).clip(RoundedCornerShape(6.dp)))
        Column(Modifier.padding(start = 12.dp).weight(1f)) {
            Text(video.displayTitle, style = MaterialTheme.typography.titleSmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
            video.durationSec?.let {
                Text(formatDuration(it), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
    }
    HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
}

/** A back arrow for a pane's title bar. */
@Composable
internal fun BackButton(onBack: () -> Unit) {
    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") }
}
