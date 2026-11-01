package com.hamzalabs.maktaba.tv.ui

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
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.tv.MaktabaTVApp
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.data.TvApi
import com.hamzalabs.maktaba.tv.ui.components.LibraryBackdrop
import com.hamzalabs.maktaba.tv.ui.components.TopBar
import com.hamzalabs.maktaba.tv.ui.screens.AccountScreen
import com.hamzalabs.maktaba.tv.ui.screens.HomeScreen
import com.hamzalabs.maktaba.tv.ui.screens.LibrariesScreen
import com.hamzalabs.maktaba.tv.ui.screens.LibraryScreen
import com.hamzalabs.maktaba.tv.ui.screens.LivePlayerScreen
import com.hamzalabs.maktaba.tv.ui.screens.LiveScreen
import com.hamzalabs.maktaba.tv.ui.screens.LiveState
import com.hamzalabs.maktaba.tv.ui.screens.LocalChooseHome
import com.hamzalabs.maktaba.tv.ui.screens.PlayerScreen
import com.hamzalabs.maktaba.tv.ui.screens.SearchScreen
import com.hamzalabs.maktaba.tv.ui.screens.SearchState
import com.hamzalabs.maktaba.tv.ui.screens.VideoScreen

/** A place in the app: the tabs, or one of the screens opened from them. Back goes up the stack. */
sealed interface Place {
    data object Tabs : Place
    data class Library(val id: String, val name: String) : Place
    data class Video(val id: String) : Place
    data class Player(val id: String, val title: String, val startMs: Long) : Place
    data class LivePlayer(val channelId: String) : Place
}

/** The tab bar's sections, in order. */
enum class TvTab { Home, Libraries, Search, Live, Account }

/** The tabs shown: Live TV only when this user may watch a channel ([hasLive]). */
fun tvTabs(hasLive: Boolean): List<TvTab> = TvTab.entries.filter { it != TvTab.Live || hasLive }

/** Signed in at a home: the tab bar (Home, Libraries, Search, Live TV, Account) and what opens from it. */
@Composable
fun SignedInApp(app: MaktabaTVApp, user: User, client: ApiClient) {
    val api = remember(client) { TvApi(client) }
    val stack = remember(client) { mutableStateListOf<Place>(Place.Tabs) }
    val search = remember(client) { SearchState() }
    val live = remember(client) { LiveState() }
    // Live TV shows once the home says this user has a channel to watch
    var hasLive by remember(client) { mutableStateOf(false) }
    LaunchedEffect(api) { hasLive = api.hasChannels() }
    val shown = tvTabs(hasLive)
    var chosen by rememberSaveable { mutableStateOf(TvTab.Home) }
    val tab = if (chosen in shown) chosen else TvTab.Home
    // a tab's first item takes focus at launch and back from a screen; not while the tab bar has it
    var focusContent by remember(client) { mutableStateOf(true) }
    val signedOut = { app.session.signedOut() }

    fun open(p: Place) {
        focusContent = true
        stack.add(p)
    }
    fun back() { if (stack.size > 1) stack.removeAt(stack.lastIndex) }
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
    CompositionLocalProvider(LocalChooseHome provides { app.session.switchHome() }) {
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
                        TvTab.Home -> HomeScreen(api, focusFirst = focusContent, onOpen = { open(Place.Video(it)) }, signedOut = signedOut)
                        TvTab.Libraries -> LibrariesScreen(api, focusFirst = focusContent, onOpen = { id, name -> open(Place.Library(id, name)) }, signedOut = signedOut)
                        TvTab.Search -> SearchScreen(api, search, focusFirst = focusContent, onOpen = { open(Place.Video(it)) }, signedOut = signedOut)
                        TvTab.Live -> LiveScreen(api, live, focusFirst = focusContent, onTune = { open(Place.LivePlayer(it)) }, signedOut = signedOut)
                        TvTab.Account -> AccountScreen(app, user)
                    }
                }
            }
            is Place.Library -> LibraryScreen(api, top.id, top.name, onOpen = { open(Place.Video(it)) }, signedOut = signedOut)
            is Place.Video -> VideoScreen(
                api,
                top.id,
                onPlay = { title, startMs -> open(Place.Player(top.id, title, startMs)) },
                signedOut = signedOut,
                onBack = ::back,
            )
            is Place.Player -> PlayerScreen(app, client, top.id, top.title, top.startMs, onBack = ::back, signedOut = signedOut)
            is Place.LivePlayer -> LivePlayerScreen(app, api, top.channelId, onBack = ::back, signedOut = signedOut)
        }
    }
}
