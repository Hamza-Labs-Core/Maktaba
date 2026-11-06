package com.hamzalabs.maktaba.tv.ui

import com.hamzalabs.maktaba.core.ui.HomeStatusHost
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.Feature
import kotlinx.coroutines.flow.StateFlow
import androidx.compose.runtime.collectAsState
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.tv.MaktabaTVApp
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.data.TvApi
import com.hamzalabs.maktaba.tv.ui.components.LibraryBackdrop
import com.hamzalabs.maktaba.tv.ui.components.TopBar
import com.hamzalabs.maktaba.tv.ui.screens.AccountScreen
import com.hamzalabs.maktaba.tv.ui.screens.HomeScreen
import com.hamzalabs.maktaba.tv.ui.screens.LibrariesScreen
import com.hamzalabs.maktaba.tv.ui.screens.LivePlayerScreen
import com.hamzalabs.maktaba.tv.ui.screens.LiveOffNote
import com.hamzalabs.maktaba.tv.ui.screens.LiveScreen
import com.hamzalabs.maktaba.tv.ui.screens.LiveState
import com.hamzalabs.maktaba.tv.ui.screens.LocalChooseHome
import com.hamzalabs.maktaba.tv.ui.screens.PlayerScreen
import com.hamzalabs.maktaba.tv.ui.screens.SearchScreen
import com.hamzalabs.maktaba.tv.ui.screens.SearchState
import com.hamzalabs.maktaba.tv.ui.screens.VideoScreen
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.diagnostics.AppLog
import com.hamzalabs.maktaba.core.queue.ApiQueueTransport
import com.hamzalabs.maktaba.core.queue.EmptySource
import com.hamzalabs.maktaba.core.queue.PlayQueueModel
import com.hamzalabs.maktaba.core.queue.QueueItem
import com.hamzalabs.maktaba.core.queue.QueueSource
import com.hamzalabs.maktaba.tv.data.TvCollection
import com.hamzalabs.maktaba.tv.data.resumeAt
import com.hamzalabs.maktaba.tv.ui.components.SectionTitle
import com.hamzalabs.maktaba.tv.ui.screens.Gutter
import com.hamzalabs.maktaba.tv.ui.screens.library.CardMenu
import com.hamzalabs.maktaba.tv.ui.screens.library.CollectionScreen
import com.hamzalabs.maktaba.tv.ui.screens.library.LibraryTabsScreen
import com.hamzalabs.maktaba.tv.ui.screens.library.LocalTvNav
import com.hamzalabs.maktaba.tv.ui.screens.library.PlaylistScreen
import com.hamzalabs.maktaba.tv.ui.screens.library.PlaylistsTab
import com.hamzalabs.maktaba.tv.ui.screens.library.SeasonScreen
import com.hamzalabs.maktaba.tv.ui.screens.library.SeriesScreen
import com.hamzalabs.maktaba.tv.ui.screens.library.TvNav
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** A place in the app: the tabs, or one of the screens opened from them. Back goes up the stack. */
sealed interface Place {
    data object Tabs : Place
    data class Library(val id: String, val name: String) : Place
    data class Video(val id: String) : Place
    data class Player(val id: String, val title: String, val startMs: Long, val queued: Boolean = false) : Place
    data class Series(val id: String, val season: Int? = null) : Place
    data class Season(val seriesId: String, val season: Int) : Place
    data class Collection(val library: String, val collection: TvCollection) : Place
    data class Playlist(val id: String) : Place
    data object Playlists : Place
    data class LivePlayer(val channelId: String) : Place
}

/** The tab bar's sections, in order. */
enum class TvTab { Home, Libraries, Search, Live, Account }

/**
 * The tabs shown: Live TV only when this user may watch a channel ([hasLive]). With Live TV off
 * on the home ([features], story 51.11) a viewer has no Live TV tab, and an admin keeps it: its
 * screen says Live TV is off and how to turn it on.
 */
fun tvTabs(hasLive: Boolean, features: Capabilities = Capabilities.AllOn): List<TvTab> {
    val live = when {
        features.explains(Feature.LIVE_TV) -> true
        features.hides(Feature.LIVE_TV) -> false
        else -> hasLive
    }
    return TvTab.entries.filter { it != TvTab.Live || live }
}

/**
 * Signed in at a home: the tab bar (Home, Libraries, Search, Live TV, Account) and what opens
 * from it. [capabilities] are what the home has on (the session's).
 */
@Composable
fun SignedInApp(
    app: MaktabaTVApp,
    user: User,
    client: ApiClient,
    capabilities: StateFlow<Capabilities> = app.session.capabilities,
) {
    val api = remember(client) { TvApi(client) }
    // Epic 56: one server play queue behind every Play and Shuffle
    val queue = remember(client) { PlayQueueModel(ApiQueueTransport(client), "tv-${android.os.Build.MODEL ?: "android"}") }
    val scope = rememberCoroutineScope()
    var notice by remember(client) { mutableStateOf<String?>(null) }
    var menu by remember(client) { mutableStateOf<Pair<String, String?>?>(null) }
    val queueEmpty = stringResource(R.string.queue_empty)
    val queueFailed = stringResource(R.string.queue_error_load)
    val queueStale = stringResource(R.string.queue_error_stale)
    val stack = remember(client) { mutableStateListOf<Place>(Place.Tabs) }
    val search = remember(client) { SearchState() }
    val live = remember(client) { LiveState() }
    val features by capabilities.collectAsState()
    val liveOn = features.isOn(Feature.LIVE_TV)
    // Live TV shows once the home says this user has a channel to watch (nothing asked while it is off)
    var hasLive by remember(client) { mutableStateOf(false) }
    LaunchedEffect(api, liveOn) { if (liveOn) hasLive = api.hasChannels() }
    val offered = tvTabs(hasLive, features)
    var chosen by rememberSaveable { mutableStateOf(TvTab.Home) }
    // a tab that goes while it is shown stays until another is chosen: an open screen is not torn down
    val offeredBefore = remember(client) { mutableSetOf<TvTab>() }
    offeredBefore += offered
    val shown = if (chosen !in offered && chosen in offeredBefore) (offered + chosen).sortedBy { it.ordinal } else offered
    val tab = if (chosen in shown) chosen else TvTab.Home
    // a tab's first item takes focus at launch and back from a screen; not while the tab bar has it
    var focusContent by remember(client) { mutableStateOf(true) }
    val signedOut = { app.session.signedOut() }

    fun open(p: Place) {
        focusContent = true
        stack.add(p)
    }
    fun back() { if (stack.size > 1) stack.removeAt(stack.lastIndex) }
    fun playItem(item: QueueItem, replace: Boolean) {
        val startMs = resumeAt(item.positionSec, item.durationSec, item.completed == true)?.let { (it * 1000).toLong() } ?: 0L
        val p = Place.Player(item.id, item.name, startMs, queued = true)
        if (replace && stack.last() is Place.Player) stack[stack.lastIndex] = p else open(p)
        if (queue.staleSeen) { notice = queueStale; queue.staleSeen = false }
    }
    var starting by remember(client) { mutableStateOf(false) }
    val nav = TvNav(
        openVideo = { open(Place.Video(it)) },
        openSeries = { open(Place.Series(it)) },
        openSeason = { id, n -> open(Place.Season(id, n)) },
        openCollection = { lib, c -> open(Place.Collection(lib, c)) },
        openPlaylist = { open(Place.Playlist(it)) },
        play = { source, shuffle, start ->
            if (!starting) {
                starting = true
                scope.launch {
                    try {
                        queue.create(source, shuffle, start)?.let { playItem(it, replace = false) } ?: run { notice = queueEmpty }
                    } catch (e: CancellationException) {
                        throw e
                    } catch (e: EmptySource) {
                        notice = queueEmpty
                    } catch (e: ApiException.SignedOut) {
                        signedOut()
                    } catch (e: Exception) {
                        AppLog.failure("tv play queue", e)
                        notice = queueFailed
                    } finally {
                        starting = false
                    }
                }
            }
        },
        cardMenu = { v, s -> menu = v to s },
        signedOut = signedOut,
    )
    BackHandler(enabled = stack.size > 1) { back() }

    val tabs = shown.map {
        stringResource(
            when (it) {
                TvTab.Home -> R.string.tab_home
                TvTab.Libraries -> R.string.tab_libraries
                TvTab.Search -> R.string.tab_search
                TvTab.Live -> R.string.tab_live
                TvTab.Account -> R.string.tab_account
            },
        )
    }

    // a failure that says the home does not answer offers another home
    // what is down on the home: a top strip, never over the player (Epic 53 story 53.06)
    CompositionLocalProvider(LocalChooseHome provides { app.session.switchHome() }, LocalTvNav provides nav) {
        HomeStatusHost(
            client,
            admin = user.isAdmin,
            enabled = stack.last() !is Place.Player && stack.last() !is Place.LivePlayer,
            liveTvOff = !hasLive,
        ) {
          Box(Modifier.fillMaxSize()) {
            when (val top = stack.last()) {
                Place.Tabs -> Box(Modifier.fillMaxSize()) {
                    if (tab == TvTab.Home) LibraryBackdrop()
                    Column(Modifier.fillMaxSize()) {
                        TopBar(
                            tabs = tabs,
                            selectedIndex = shown.indexOf(tab),
                            onSelect = { i -> shown.getOrNull(i)?.let { if (it != tab) focusContent = false; chosen = it } },
                        )
                        when (tab) {
                            TvTab.Home -> HomeScreen(api, focusFirst = focusContent, onOpen = { open(Place.Video(it)) }, signedOut = signedOut,
                                queue = queue, onContinue = { playItem(it, replace = false) })
                            TvTab.Libraries -> LibrariesScreen(api, focusFirst = focusContent, onOpen = { id, name -> open(Place.Library(id, name)) }, signedOut = signedOut)
                            TvTab.Search -> SearchScreen(api, search, focusFirst = focusContent, onOpen = { open(Place.Video(it)) }, signedOut = signedOut)
                            TvTab.Live -> if (features.explains(Feature.LIVE_TV)) {
                                LiveOffNote(client.home.toString(), focusFirst = focusContent)
                            } else {
                                LiveScreen(api, live, focusFirst = focusContent, onTune = { open(Place.LivePlayer(it)) }, signedOut = signedOut)
                            }
                            TvTab.Account -> AccountScreen(app, user, onPlaylists = { open(Place.Playlists) })
                        }
                    }
                }
                is Place.Library -> LibraryTabsScreen(api, top.id, top.name)
                is Place.Series -> SeriesScreen(api, top.id, top.season)
                is Place.Season -> SeasonScreen(api, top.seriesId, top.season)
                is Place.Collection -> CollectionScreen(api, top.library, top.collection)
                is Place.Playlist -> PlaylistScreen(api, top.id)
                Place.Playlists -> Column(Modifier.fillMaxSize().padding(top = 32.dp)) {
                    SectionTitle(stringResource(R.string.playlists_title), Modifier.padding(horizontal = Gutter))
                    PlaylistsTab(api, null)
                }
                is Place.Video -> VideoScreen(
                    api,
                    top.id,
                    // a video on its own still plays in a queue: an episode goes on through its series
                    onPlay = { _, startMs ->
                        scope.launch {
                            val item = runCatching { queue.create(QueueSource("video", top.id), false) }.getOrNull()
                            if (item != null) open(Place.Player(item.id, item.name, startMs, queued = true)) else notice = queueFailed
                        }
                    },
                    signedOut = signedOut,
                    onBack = ::back,
                    onSeries = { open(Place.Series(it)) },
                )
                is Place.Player -> PlayerScreen(
                    app, client, top.id, top.title, top.startMs, onBack = ::back, signedOut = signedOut,
                    queue = if (top.queued) queue else null,
                    onQueueItem = { playItem(it, replace = true) },
                )
                is Place.LivePlayer -> LivePlayerScreen(app, api, top.channelId, onBack = ::back, signedOut = signedOut)
            }
            menu?.let { (video, series) ->
                CardMenu(api, video, series, onClose = { menu = null }, onDone = { notice = it }, queue = queue)
            }
            notice?.let { n ->
                LaunchedEffect(n) { delay(4_000); notice = null }
                Text(n, style = MaterialTheme.typography.titleMedium, color = MaktabaPalette.Gold, modifier = Modifier.align(Alignment.BottomCenter).padding(32.dp).testTag("notice"))
            }
          }
        }
    }
}
