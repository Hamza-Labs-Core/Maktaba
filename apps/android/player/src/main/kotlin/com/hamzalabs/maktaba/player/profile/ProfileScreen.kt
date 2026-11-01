package com.hamzalabs.maktaba.player.profile

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.activity.OnBackPressedCallback
import androidx.activity.compose.LocalOnBackPressedDispatcherOwner
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.HelpOutline
import androidx.compose.material.icons.automirrored.outlined.Logout
import androidx.compose.material.icons.automirrored.outlined.OpenInNew
import androidx.compose.material.icons.automirrored.outlined.PlaylistPlay
import androidx.compose.material.icons.filled.AccountCircle
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.outlined.BugReport
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Devices
import androidx.compose.material.icons.outlined.FavoriteBorder
import androidx.compose.material.icons.outlined.History
import androidx.compose.material.icons.outlined.Home
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.outlined.PhonelinkLock
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.core.net.toUri
import com.hamzalabs.maktaba.core.api.HistoryEntry
import com.hamzalabs.maktaba.core.api.SignInSession
import com.hamzalabs.maktaba.core.diagnostics.CrashLog
import com.hamzalabs.maktaba.core.ui.ApproveSignIn
import com.hamzalabs.maktaba.core.ui.ErrorState
import com.hamzalabs.maktaba.core.ui.Message
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.core.ui.formatDuration
import com.hamzalabs.maktaba.core.ui.sentence
import com.hamzalabs.maktaba.player.BackButton
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.OnReselect
import com.hamzalabs.maktaba.player.shell.PlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerNavigator
import com.hamzalabs.maktaba.player.shell.Route
import com.hamzalabs.maktaba.player.ui.components.PosterCard
import com.hamzalabs.maktaba.player.ui.components.PosterCardWidth
import com.hamzalabs.maktaba.player.ui.components.PosterSize
import com.hamzalabs.maktaba.player.ui.components.ProgressStrip
import com.hamzalabs.maktaba.player.ui.components.posterUrl
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.time.Duration
import java.time.Instant
import com.hamzalabs.maktaba.core.R as CoreR

/** The help page for this app on the home (FR-27). */
fun helpUrl(home: String): String = "${home.trimEnd('/')}/help/android-apps"

/**
 * Profile and Settings (FR-25 to FR-31): who is signed in and on which home; their watch history,
 * favorites and playlists; the account (sessions, approving a sign-in, switching home, signing
 * out); this device's settings (theme, language, playback); help and about. Its pages open in
 * the section's place; Back, or selecting Profile again, returns to the first.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ProfileScreen(nav: PlayerNavigator) {
    val home = LocalPlayerHome.current
    val state = home.keep("profile") { ProfileState(home.client, home.session::signedOut) }
    val page = state.page
    OnReselect(nav) { state.page = ProfilePage.Root }
    PageBack(state)

    val snackbar = remember { SnackbarHostState() }
    val resources = LocalResources.current
    state.problem?.let { failure ->
        LaunchedEffect(failure) {
            snackbar.showSnackbar(resources.getString(failure.kind.sentence()))
            state.problem = null
        }
    }

    Scaffold(
        modifier = Modifier.testTag("profile"),
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = {
            TopAppBar(
                title = { Text(stringResource(page.title()), modifier = Modifier.semantics { heading() }) },
                navigationIcon = { if (page != ProfilePage.Root) BackButton { state.page = page.parent } },
            )
        },
    ) { padding ->
        Box(Modifier.padding(padding).fillMaxSize()) {
            when (page) {
                ProfilePage.Root -> RootPage(home, state)
                ProfilePage.History -> HistoryPage(state, nav)
                ProfilePage.Favorites -> FavoritesPage(home, state, nav)
                ProfilePage.Playlists -> PlaylistsPage(state, nav)
                is ProfilePage.Playlist -> PlaylistPage(home, state, page, nav)
                ProfilePage.Sessions -> SessionsPage(state)
                ProfilePage.Playback -> PlaybackPage()
                ProfilePage.Diagnostics -> DiagnosticsPage()
            }
        }
    }
}

/**
 * Back from one of the Profile's pages to its parent. The shell's own handler (Back at another
 * section's root returns to the first section) is composed after the section's screen and so
 * registers later, taking Back first; this one registers from an effect, after the shell's, and
 * again on each page, so the page's Back wins while a page is shown.
 */
@Composable
private fun PageBack(state: ProfileState) {
    val dispatcher = LocalOnBackPressedDispatcherOwner.current?.onBackPressedDispatcher ?: return
    val page = state.page
    LaunchedEffect(dispatcher, page) {
        if (page == ProfilePage.Root) return@LaunchedEffect
        val callback = object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                state.page = state.page.parent
            }
        }
        dispatcher.addCallback(callback)
        try {
            awaitCancellation()
        } finally {
            callback.remove()
        }
    }
}

private fun ProfilePage.title(): Int = when (this) {
    ProfilePage.Root -> R.string.section_profile
    ProfilePage.History -> R.string.profile_history
    ProfilePage.Favorites -> R.string.profile_favorites
    ProfilePage.Playlists, is ProfilePage.Playlist -> R.string.profile_playlists
    ProfilePage.Sessions -> R.string.profile_sessions
    ProfilePage.Playback -> R.string.profile_playback
    ProfilePage.Diagnostics -> R.string.profile_diagnostics
}

// ---------- the first page ----------

@Composable
private fun RootPage(home: PlayerHome, state: ProfileState) {
    val context = LocalContext.current
    val current by home.session.current.collectAsState()
    val homeName = current?.label ?: home.client.home.host
    val mode by home.themes.mode.collectAsState()
    val languages = appLanguages()
    val language by languages.current.collectAsState()
    var approving by remember { mutableStateOf(false) }
    var confirmSignOut by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) {
        languages.refresh()
        state.loadHomeVersion()
    }

    LazyColumn(Modifier.fillMaxSize().testTag("profile-root"), contentPadding = PaddingValues(bottom = 24.dp)) {
        item {
            ListItem(
                headlineContent = { Text(home.user.username, style = MaterialTheme.typography.titleLarge) },
                supportingContent = { Text(homeName) },
                leadingContent = { Icon(Icons.Filled.AccountCircle, null, Modifier.size(48.dp), tint = MaterialTheme.colorScheme.primary) },
                modifier = Modifier.testTag("profile-header"),
            )
        }

        group(R.string.profile_library)
        item { LinkRow(Icons.Outlined.History, R.string.profile_history, tag = "profile-history") { state.page = ProfilePage.History } }
        item { LinkRow(Icons.Outlined.FavoriteBorder, R.string.profile_favorites, tag = "profile-favorites") { state.page = ProfilePage.Favorites } }
        item { LinkRow(Icons.AutoMirrored.Outlined.PlaylistPlay, R.string.profile_playlists, tag = "profile-playlists") { state.page = ProfilePage.Playlists } }

        group(R.string.profile_account)
        item { LinkRow(Icons.Outlined.Devices, R.string.profile_sessions, tag = "profile-sessions") { state.page = ProfilePage.Sessions } }
        item { LinkRow(Icons.Outlined.PhonelinkLock, CoreR.string.account_approve, tag = "profile-approve") { approving = true } }
        item { LinkRow(Icons.Outlined.Home, CoreR.string.switch_home, tag = "profile-switch-home") { home.session.switchHome() } }
        item { LinkRow(Icons.AutoMirrored.Outlined.Logout, CoreR.string.sign_out, tag = "profile-sign-out") { confirmSignOut = true } }

        group(R.string.profile_settings)
        item {
            Choices(
                title = stringResource(R.string.profile_theme),
                options = listOf(ThemeMode.Dark, ThemeMode.Light, ThemeMode.System),
                selected = mode,
                label = { stringResource(it.label) },
                tag = { "theme-${it.name}" },
                onSelect = home.themes::set,
            )
        }
        item {
            Choices(
                title = stringResource(R.string.profile_language),
                options = listOf(AppLanguage.System, AppLanguage.English, AppLanguage.Arabic),
                selected = language,
                label = { stringResource(it.label) },
                tag = { "language-${it.name}" },
                onSelect = languages::set,
            )
        }
        if (languages.perAppLanguages) {
            item {
                LinkRow(Icons.AutoMirrored.Outlined.OpenInNew, R.string.profile_language_system_settings, tag = "profile-language-settings") {
                    openAppLanguageSettings(context)
                }
            }
        }
        item { LinkRow(Icons.Outlined.Settings, R.string.profile_playback, tag = "profile-playback") { state.page = ProfilePage.Playback } }

        group(R.string.profile_help_about)
        item {
            LinkRow(Icons.AutoMirrored.Outlined.HelpOutline, R.string.profile_help, tag = "profile-help") {
                open(context, Intent(Intent.ACTION_VIEW, helpUrl(home.client.home.toString()).toUri()))
            }
        }
        item {
            val info = remember(context) { appVersion(context) }
            ListItem(
                headlineContent = { Text(stringResource(R.string.profile_about_version, info.first, info.second)) },
                supportingContent = {
                    Text(stringResource(R.string.profile_about_home_version, state.homeVersion ?: stringResource(R.string.profile_unknown)))
                },
                leadingContent = { Icon(Icons.Outlined.Info, null) },
                modifier = Modifier.testTag("profile-about"),
            )
        }
        item { LinkRow(Icons.Outlined.BugReport, R.string.profile_diagnostics, tag = "profile-diagnostics") { state.page = ProfilePage.Diagnostics } }
    }

    if (approving) {
        Dialog(
            onDismissRequest = { approving = false },
            properties = DialogProperties(usePlatformDefaultWidth = false, decorFitsSystemWindows = false),
        ) {
            Surface(Modifier.fillMaxSize()) {
                ApproveSignIn(
                    client = home.client,
                    username = home.user.username,
                    homeName = homeName,
                    onClose = { approving = false },
                    onSignedOut = { approving = false; home.session.signedOut() },
                )
            }
        }
    }
    if (confirmSignOut) {
        AlertDialog(
            onDismissRequest = { confirmSignOut = false },
            title = { Text(stringResource(R.string.profile_sign_out_title, homeName)) },
            text = { Text(stringResource(R.string.profile_sign_out_body)) },
            confirmButton = {
                TextButton(onClick = { confirmSignOut = false; home.session.signOut() }, modifier = Modifier.testTag("sign-out-confirm")) {
                    Text(stringResource(CoreR.string.sign_out))
                }
            },
            dismissButton = { TextButton(onClick = { confirmSignOut = false }) { Text(stringResource(R.string.profile_cancel)) } },
        )
    }
}

private fun LazyListScope.group(title: Int) {
    item {
        Column {
            HorizontalDivider(Modifier.padding(top = 8.dp))
            Text(
                stringResource(title),
                style = MaterialTheme.typography.titleSmall,
                color = MaterialTheme.colorScheme.primary,
                modifier = Modifier.padding(start = 16.dp, end = 16.dp, top = 16.dp, bottom = 4.dp).semantics { heading() },
            )
        }
    }
}

/** A row that opens something: at least 56 dp tall, one node for TalkBack. */
@Composable
private fun LinkRow(icon: androidx.compose.ui.graphics.vector.ImageVector, label: Int, tag: String, supporting: String? = null, onClick: () -> Unit) {
    ListItem(
        headlineContent = { Text(stringResource(label)) },
        supportingContent = supporting?.let { { Text(it) } },
        leadingContent = { Icon(icon, null) },
        modifier = Modifier.heightIn(min = 56.dp).clickable(onClick = onClick).testTag(tag),
    )
}

/** A setting with a few choices, as chips; the chosen one is selected (TalkBack says so). */
@Composable
private fun <T> Choices(
    title: String,
    options: List<T>,
    selected: T,
    label: @Composable (T) -> String,
    tag: (T) -> String,
    onSelect: (T) -> Unit,
) {
    Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp)) {
        Text(title, style = MaterialTheme.typography.bodyLarge)
        FlowRow(Modifier.selectableGroup().padding(top = 4.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            options.forEach { option ->
                val chosen = option == selected
                FilterChip(
                    selected = chosen,
                    onClick = { onSelect(option) },
                    label = { Text(label(option)) },
                    leadingIcon = if (chosen) ({ Icon(Icons.Filled.Check, null, Modifier.size(18.dp)) }) else null,
                    modifier = Modifier.heightIn(min = 48.dp).testTag(tag(option)),
                )
            }
        }
    }
}

/** A switch row: the whole row toggles, and reads as one switch. */
@Composable
private fun Toggle(label: Int, checked: Boolean, tag: String, onChange: (Boolean) -> Unit) {
    ListItem(
        headlineContent = { Text(stringResource(label)) },
        trailingContent = { Switch(checked = checked, onCheckedChange = null) },
        modifier = Modifier
            .heightIn(min = 56.dp)
            .toggleable(value = checked, role = Role.Switch, onValueChange = onChange)
            .testTag(tag),
    )
}

@Composable
private fun <T> Loaded(load: Load<T>, title: Int, onRetry: () -> Unit, content: @Composable (T) -> Unit) {
    when (load) {
        Load.Loading -> Box(Modifier.fillMaxSize().testTag("profile-loading"), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
        is Load.Failed -> ErrorState(load.failure, stringResource(title), onRetry = onRetry)
        is Load.Ready -> content(load.value)
    }
}

// ---------- history ----------

@Composable
private fun HistoryPage(state: ProfileState, nav: PlayerNavigator) {
    val scope = rememberCoroutineScope()
    LaunchedEffect(Unit) { state.loadHistory() }
    Loaded(state.history, R.string.profile_history, onRetry = { scope.launch { state.loadHistory() } }) { items ->
        if (items.isEmpty()) {
            Message(stringResource(R.string.profile_history_empty))
            return@Loaded
        }
        LazyColumn(Modifier.fillMaxSize().testTag("history-list")) {
            items(items, key = { it.videoId }) { entry ->
                HistoryRow(entry, onOpen = { nav.open(Route.Video(entry.videoId)) }, onRemove = { scope.launch { state.forget(entry) } })
            }
            if (state.historyMore) {
                item { MoreButton { scope.launch { state.moreHistory() } } }
            }
        }
    }
}

@Composable
private fun HistoryRow(entry: HistoryEntry, onOpen: () -> Unit, onRemove: () -> Unit) {
    val title = entry.title.ifBlank { stringResource(R.string.profile_untitled) }
    val watched = when {
        entry.completed -> stringResource(R.string.profile_history_watched)
        entry.timesWatched > 1 -> LocalResources.current.getQuantityString(R.plurals.profile_history_times, entry.timesWatched, entry.timesWatched)
        else -> null
    }
    ListItem(
        headlineContent = { Text(title, maxLines = 2, overflow = TextOverflow.Ellipsis) },
        supportingContent = {
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                listOfNotNull(entry.lastWatchedAt?.let { ago(it) }, watched).joinToString(" · ").takeIf { it.isNotEmpty() }?.let { Text(it) }
                entry.watchedFraction?.takeIf { !entry.completed }?.let { ProgressStrip(it.toFloat(), Modifier.widthIn(max = 240.dp)) }
            }
        },
        trailingContent = {
            IconButton(onClick = onRemove, modifier = Modifier.testTag("history-remove-${entry.videoId}")) {
                Icon(Icons.Outlined.Close, stringResource(R.string.profile_history_remove, title))
            }
        },
        modifier = Modifier.heightIn(min = 56.dp).clickable(onClick = onOpen).testTag("history-${entry.videoId}"),
    )
}

// ---------- favorites ----------

@Composable
private fun FavoritesPage(home: PlayerHome, state: ProfileState, nav: PlayerNavigator) {
    val scope = rememberCoroutineScope()
    LaunchedEffect(Unit) { state.loadFavorites() }
    Loaded(state.favorites, R.string.profile_favorites, onRetry = { scope.launch { state.loadFavorites() } }) { items ->
        if (items.isEmpty()) {
            Message(stringResource(R.string.profile_favorites_empty))
            return@Loaded
        }
        LazyVerticalGrid(
            GridCells.Adaptive(PosterCardWidth),
            Modifier.fillMaxSize().testTag("favorites-grid"),
            contentPadding = PaddingValues(16.dp),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            items(items, key = { it.id }) { v ->
                PosterCard(
                    title = v.displayTitle,
                    posterUrl = posterUrl(home.client, v.id, PosterSize.Small),
                    onClick = { nav.open(Route.Video(v.id)) },
                    width = PosterCardWidth,
                    modifier = Modifier.testTag("favorite-${v.id}"),
                )
            }
            if (state.favoritesMore) {
                item(span = { GridItemSpan(maxLineSpan) }) { MoreButton { scope.launch { state.moreFavorites() } } }
            }
        }
    }
}

// ---------- playlists ----------

@Composable
private fun PlaylistsPage(state: ProfileState, nav: PlayerNavigator) {
    val scope = rememberCoroutineScope()
    LaunchedEffect(Unit) { state.loadPlaylists() }
    Loaded(state.playlists, R.string.profile_playlists, onRetry = { scope.launch { state.loadPlaylists() } }) { items ->
        if (items.isEmpty()) {
            Message(stringResource(R.string.profile_playlists_empty))
            return@Loaded
        }
        LazyColumn(Modifier.fillMaxSize().testTag("playlists-list")) {
            items(items, key = { it.id }) { p ->
                val name = p.name.ifBlank { stringResource(R.string.profile_untitled) }
                ListItem(
                    headlineContent = { Text(name, maxLines = 2, overflow = TextOverflow.Ellipsis) },
                    supportingContent = { Text(countAndLength(p.videos, p.durationSec)) },
                    leadingContent = { Icon(Icons.AutoMirrored.Outlined.PlaylistPlay, null) },
                    trailingContent = {
                        if (p.videos > 0) {
                            IconButton(
                                onClick = {
                                    scope.launch {
                                        val first = state.firstOf(p.id) ?: return@launch
                                        withContext(Dispatchers.Main) { nav.open(Route.Watch(first, startSec = 0)) }
                                    }
                                },
                                modifier = Modifier.testTag("playlist-play-${p.id}"),
                            ) { Icon(Icons.Filled.PlayArrow, stringResource(R.string.profile_playlist_play_named, name)) }
                        }
                    },
                    modifier = Modifier.heightIn(min = 56.dp).clickable { state.page = ProfilePage.Playlist(p.id, name) }.testTag("playlist-${p.id}"),
                )
            }
        }
    }
}

@Composable
private fun PlaylistPage(home: PlayerHome, state: ProfileState, page: ProfilePage.Playlist, nav: PlayerNavigator) {
    val scope = rememberCoroutineScope()
    LaunchedEffect(page.id) { state.loadPlaylist(page.id) }
    Loaded(state.playlist, R.string.profile_playlists, onRetry = { scope.launch { state.loadPlaylist(page.id) } }) { doc ->
        LazyVerticalGrid(
            GridCells.Adaptive(PosterCardWidth),
            Modifier.fillMaxSize().testTag("playlist-grid"),
            contentPadding = PaddingValues(16.dp),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            item(span = { GridItemSpan(maxLineSpan) }) {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(page.name, style = MaterialTheme.typography.headlineSmall, modifier = Modifier.semantics { heading() })
                    doc.playlist.description.takeIf { it.isNotBlank() }?.let { Text(it, style = MaterialTheme.typography.bodyMedium) }
                    Text(countAndLength(doc.playlist.videos, doc.playlist.durationSec), style = MaterialTheme.typography.bodySmall)
                    doc.items.firstOrNull()?.let { first ->
                        OutlinedButton(onClick = { nav.open(Route.Watch(first.id, startSec = 0)) }, modifier = Modifier.heightIn(min = 48.dp).testTag("playlist-play")) {
                            Icon(Icons.Filled.PlayArrow, null)
                            Text(stringResource(R.string.profile_playlist_play), Modifier.padding(start = 8.dp))
                        }
                    }
                }
            }
            if (doc.items.isEmpty()) {
                item(span = { GridItemSpan(maxLineSpan) }) { Message(stringResource(R.string.profile_playlist_empty)) }
            }
            items(doc.items, key = { it.id }) { card ->
                PosterCard(card, home.client, onClick = { nav.open(Route.Video(card.id)) }, modifier = Modifier.testTag("playlist-item-${card.id}"))
            }
        }
    }
}

// ---------- sessions ----------

@Composable
private fun SessionsPage(state: ProfileState) {
    val scope = rememberCoroutineScope()
    LaunchedEffect(Unit) { state.loadSessions() }
    Loaded(state.sessions, R.string.profile_sessions, onRetry = { scope.launch { state.loadSessions() } }) { items ->
        if (items.isEmpty()) {
            Message(stringResource(R.string.profile_sessions_empty))
            return@Loaded
        }
        LazyColumn(Modifier.fillMaxSize().testTag("sessions-list")) {
            item {
                Text(
                    stringResource(R.string.profile_sessions_about),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(16.dp),
                )
            }
            items(items, key = { it.id }) { s -> SessionRow(s) { scope.launch { state.endSession(s) } } }
        }
    }
}

@Composable
private fun SessionRow(session: SignInSession, onSignOut: () -> Unit) {
    val name = browserName(session.userAgent) ?: stringResource(R.string.profile_session_unknown)
    val details = listOfNotNull(
        session.ip,
        session.lastSeenAt?.let { stringResource(R.string.profile_session_last_seen, ago(it)) },
        if (session.current) stringResource(R.string.profile_session_current) else null,
    ).joinToString(" · ")
    ListItem(
        headlineContent = { Text(name) },
        supportingContent = { Text(details) },
        leadingContent = { Icon(Icons.Outlined.Devices, null) },
        trailingContent = {
            TextButton(onClick = onSignOut, modifier = Modifier.heightIn(min = 48.dp).testTag("session-sign-out-${session.id}")) {
                Text(stringResource(R.string.profile_session_sign_out))
            }
        },
        modifier = Modifier.heightIn(min = 56.dp).testTag("session-${session.id}"),
    )
}

/** A browser's name from its user agent, as a person knows it. */
internal fun browserName(userAgent: String?): String? {
    val ua = userAgent?.takeIf { it.isNotBlank() } ?: return null
    val browser = when {
        "Edg/" in ua -> "Edge"
        "Firefox/" in ua -> "Firefox"
        "Chrome/" in ua -> "Chrome"
        "Safari/" in ua -> "Safari"
        "Maktaba" in ua -> "Maktaba"
        else -> null
    }
    val os = when {
        "Android" in ua -> "Android"
        "iPhone" in ua -> "iPhone"
        "iPad" in ua -> "iPad"
        "Mac OS X" in ua || "Macintosh" in ua -> "Mac"
        "Windows" in ua -> "Windows"
        "Linux" in ua -> "Linux"
        else -> null
    }
    return listOfNotNull(browser, os).joinToString(" · ").ifEmpty { ua.take(40) }
}

// ---------- playback ----------

@Composable
private fun PlaybackPage() {
    val settings = playbackSettings()
    val p by settings.value.collectAsState()
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).testTag("playback-settings")) {
        Toggle(R.string.profile_autoplay_next, p.autoplayNext, "playback-autoplay") { v -> settings.update { it.copy(autoplayNext = v) } }
        Toggle(R.string.profile_resume_without_asking, p.resumeWithoutAsking, "playback-resume") { v -> settings.update { it.copy(resumeWithoutAsking = v) } }
        Toggle(R.string.profile_subtitles_on, p.subtitlesOn, "playback-subtitles") { v -> settings.update { it.copy(subtitlesOn = v) } }
        Choices(
            stringResource(R.string.profile_subtitle_language), SubtitleLanguage.entries.toList(), p.subtitleLanguage,
            label = { stringResource(it.label) }, tag = { "subtitle-language-${it.name}" },
        ) { v -> settings.update { it.copy(subtitleLanguage = v) } }
        Choices(
            stringResource(R.string.profile_subtitle_size), SubtitleSize.entries.toList(), p.subtitleSize,
            label = { stringResource(it.label) }, tag = { "subtitle-size-${it.name}" },
        ) { v -> settings.update { it.copy(subtitleSize = v) } }
        Choices(
            stringResource(R.string.profile_quality_wifi), StreamQuality.entries.toList(), p.qualityWifi,
            label = { stringResource(it.label) }, tag = { "quality-wifi-${it.name}" },
        ) { v -> settings.update { it.copy(qualityWifi = v) } }
        Choices(
            stringResource(R.string.profile_quality_mobile), StreamQuality.entries.toList(), p.qualityMobile,
            label = { stringResource(it.label) }, tag = { "quality-mobile-${it.name}" },
        ) { v -> settings.update { it.copy(qualityMobile = v) } }
    }
}

// ---------- diagnostics ----------

@Composable
private fun DiagnosticsPage() {
    val context = LocalContext.current
    var report by remember { mutableStateOf(CrashLog.last(context)) }
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp).testTag("diagnostics"), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(stringResource(R.string.profile_diagnostics_about), style = MaterialTheme.typography.bodyMedium)
        val text = report
        if (text == null) {
            Text(stringResource(R.string.profile_diagnostics_none), style = MaterialTheme.typography.titleMedium)
        } else {
            SelectionContainer { Text(text, style = MaterialTheme.typography.bodySmall) }
            OutlinedButton(onClick = { CrashLog.clear(context); report = null }, modifier = Modifier.heightIn(min = 48.dp)) {
                Text(stringResource(R.string.profile_diagnostics_clear))
            }
        }
    }
}

// ---------- helpers ----------

@Composable
private fun MoreButton(onClick: () -> Unit) {
    Box(Modifier.fillMaxWidth().padding(16.dp), contentAlignment = Alignment.Center) {
        OutlinedButton(onClick = onClick, modifier = Modifier.heightIn(min = 48.dp).testTag("load-more")) { Text(stringResource(R.string.profile_load_more)) }
    }
}

@Composable
private fun countAndLength(videos: Long, durationSec: Double): String {
    val count = LocalResources.current.getQuantityString(R.plurals.profile_videos, videos.toInt(), videos.toInt())
    return if (durationSec > 0) "$count · ${formatDuration(durationSec)}" else count
}

/** How long ago [iso] was, in the app's words ("5 minutes ago", "3 days ago"). */
@Composable
private fun ago(iso: String): String {
    val at = runCatching { Instant.parse(iso) }.getOrNull() ?: return iso
    val d = Duration.between(at, Instant.now()).coerceAtLeast(Duration.ZERO)
    val r = LocalResources.current
    return when {
        d.toMinutes() < 1 -> stringResource(R.string.profile_just_now)
        d.toHours() < 1 -> r.getQuantityString(R.plurals.profile_minutes_ago, d.toMinutes().toInt(), d.toMinutes().toInt())
        d.toDays() < 1 -> r.getQuantityString(R.plurals.profile_hours_ago, d.toHours().toInt(), d.toHours().toInt())
        else -> r.getQuantityString(R.plurals.profile_days_ago, d.toDays().toInt(), d.toDays().toInt())
    }
}

private fun appVersion(context: Context): Pair<String, String> {
    val info = runCatching { context.packageManager.getPackageInfo(context.packageName, 0) }.getOrNull()
    @Suppress("DEPRECATION")
    val code = info?.let { if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) it.longVersionCode else it.versionCode.toLong() } ?: 0L
    return (info?.versionName ?: "?") to code.toString()
}

/** The system's language page for this app (Android 13+). */
internal fun appLanguageSettingsIntent(context: Context): Intent =
    Intent(Settings.ACTION_APP_LOCALE_SETTINGS, Uri.fromParts("package", context.packageName, null))

private fun openAppLanguageSettings(context: Context) {
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) open(context, appLanguageSettingsIntent(context))
}

private fun open(context: Context, intent: Intent) {
    try {
        context.startActivity(intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    } catch (_: ActivityNotFoundException) {
        // no app opens it: nothing to do
    }
}
