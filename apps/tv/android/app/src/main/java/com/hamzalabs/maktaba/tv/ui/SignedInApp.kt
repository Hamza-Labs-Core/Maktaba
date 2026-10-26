package com.hamzalabs.maktaba.tv.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableIntStateOf
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
import com.hamzalabs.maktaba.tv.ui.screens.PlayerScreen
import com.hamzalabs.maktaba.tv.ui.screens.SearchScreen
import com.hamzalabs.maktaba.tv.ui.screens.VideoScreen

/** A place in the app: the tabs, or one of the screens opened from them. Back goes up the stack. */
sealed interface Place {
    data object Tabs : Place
    data class Library(val id: String, val name: String) : Place
    data class Video(val id: String) : Place
    data class Player(val id: String, val title: String, val startMs: Long) : Place
}

/** Signed in at a home: the tab bar (Home, Libraries, Search, Account) and what opens from it. */
@Composable
fun SignedInApp(app: MaktabaTVApp, user: User, client: ApiClient) {
    val api = remember(client) { TvApi(client) }
    val stack = remember(client) { mutableStateListOf<Place>(Place.Tabs) }
    var tab by rememberSaveable { mutableIntStateOf(0) }
    val signedOut = { app.session.signedOut() }

    fun open(p: Place) { stack.add(p) }
    fun back() { if (stack.size > 1) stack.removeAt(stack.lastIndex) }
    BackHandler(enabled = stack.size > 1) { back() }

    val tabs = listOf(
        stringResource(R.string.tab_home),
        stringResource(R.string.tab_libraries),
        stringResource(R.string.tab_search),
        stringResource(R.string.tab_account),
    )

    when (val top = stack.last()) {
        Place.Tabs -> Box(Modifier.fillMaxSize()) {
            if (tab == 0) LibraryBackdrop()
            Column(Modifier.fillMaxSize()) {
                TopBar(tabs = tabs, selectedIndex = tab, onSelect = { tab = it })
                when (tab) {
                    0 -> HomeScreen(api, onOpen = { open(Place.Video(it)) }, signedOut = signedOut)
                    1 -> LibrariesScreen(api, onOpen = { id, name -> open(Place.Library(id, name)) }, signedOut = signedOut)
                    2 -> SearchScreen(api, onOpen = { open(Place.Video(it)) }, signedOut = signedOut)
                    else -> AccountScreen(app, user)
                }
            }
        }
        is Place.Library -> LibraryScreen(api, top.id, top.name, onOpen = { open(Place.Video(it)) }, signedOut = signedOut)
        is Place.Video -> VideoScreen(
            api,
            top.id,
            onPlay = { title, startMs -> open(Place.Player(top.id, title, startMs)) },
            signedOut = signedOut,
        )
        is Place.Player -> PlayerScreen(app, client, top.id, top.title, top.startMs, onBack = ::back, signedOut = signedOut)
    }
}
