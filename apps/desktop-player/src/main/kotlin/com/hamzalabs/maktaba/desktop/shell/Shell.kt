package com.hamzalabs.maktaba.desktop.shell

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.AccountCircle
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.LiveTv
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.VideoLibrary
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationRail
import androidx.compose.material3.NavigationRailItem
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEvent
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isAltPressed
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isMetaPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onKeyEvent
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.input.pointer.PointerButton
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.input.pointer.onPointerEvent
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.desktop.LocalTexts
import com.hamzalabs.maktaba.desktop.home.HomeScreen
import com.hamzalabs.maktaba.desktop.libraries.LibrariesScreen
import com.hamzalabs.maktaba.desktop.libraries.LibraryPage
import com.hamzalabs.maktaba.desktop.libraries.SeriesPage
import com.hamzalabs.maktaba.desktop.live.ChannelScreen
import com.hamzalabs.maktaba.desktop.live.LiveTvScreen
import com.hamzalabs.maktaba.desktop.profile.PlaylistPage
import com.hamzalabs.maktaba.desktop.profile.ProfileScreen
import com.hamzalabs.maktaba.desktop.search.SearchScreen
import com.hamzalabs.maktaba.desktop.video.VideoPage

private fun Section.icon(): ImageVector = when (this) {
    Section.Home -> Icons.Filled.Home
    Section.Libraries -> Icons.Filled.VideoLibrary
    Section.LiveTv -> Icons.Filled.LiveTv
    Section.Search -> Icons.Filled.Search
    Section.Profile -> Icons.Filled.AccountCircle
}

/** Alt+Left (Alt+Right in Arabic) goes back; Ctrl+F (Cmd+F on a Mac) opens Search. */
internal fun shellKey(e: KeyEvent, rtl: Boolean, nav: Navigator): Boolean {
    if (e.type != KeyEventType.KeyDown) return false
    if (e.isAltPressed && e.key == (if (rtl) Key.DirectionRight else Key.DirectionLeft)) return nav.back()
    if ((e.isCtrlPressed || e.isMetaPressed) && e.key == Key.F) {
        nav.askSearch()
        return true
    }
    return false
}

/**
 * The signed-in window (story 55.01): the sections on a rail at the start side (the right in
 * Arabic), the page beside it with a Back button when the section has a page to return to. The
 * mouse's back button goes back too, and `/` (outside a text field) opens Search.
 */
@OptIn(ExperimentalComposeUiApi::class)
@Composable
fun Shell(home: DesktopHome) {
    val t = LocalTexts.current
    val nav = home.nav
    val features = home.features()
    val rtl = LocalLayoutDirection.current == LayoutDirection.Rtl
    // Live TV went off for a viewer: the rail drops it and a viewer on it is moved to Home
    LaunchedEffect(features) { nav.follow(features) }
    CompositionLocalProvider(LocalDesktopHome provides home) {
        Row(
            Modifier.fillMaxSize()
                .onPreviewKeyEvent { shellKey(it, rtl, nav) }
                .onKeyEvent { e ->
                    if (e.type == KeyEventType.KeyDown && e.key == Key.Slash && !e.isCtrlPressed && !e.isAltPressed) {
                        nav.askSearch()
                        true
                    } else false
                }
                .onPointerEvent(PointerEventType.Press) { if (it.button == PointerButton.Back) nav.back() },
        ) {
            NavigationRail(Modifier.fillMaxHeight().testTag("rail")) {
                for (s in Section.shown(features)) {
                    NavigationRailItem(
                        selected = nav.section == s,
                        onClick = { nav.select(s) },
                        icon = { Icon(s.icon(), contentDescription = null) },
                        label = { Text(t[s.key]) },
                        modifier = Modifier.testTag("rail-${s.name}"),
                    )
                }
            }
            Column(Modifier.weight(1f).fillMaxHeight()) {
                if (nav.canGoBack) {
                    TextButton(onClick = { nav.back() }, modifier = Modifier.padding(start = 8.dp, top = 4.dp).testTag("back")) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = null)
                        Text(t["shell.back"], modifier = Modifier.padding(start = 6.dp), style = MaterialTheme.typography.labelLarge)
                    }
                }
                Box(Modifier.fillMaxWidth().weight(1f)) { Page(nav.current) }
            }
        }
    }
}

/** The screen for [route]. */
@Composable
fun Page(route: Route) {
    when (route) {
        Route.Home -> HomeScreen()
        Route.Libraries -> LibrariesScreen()
        Route.LiveTv -> LiveTvScreen()
        Route.Search -> SearchScreen()
        Route.Profile -> ProfileScreen()
        is Route.Video -> VideoPage(route.id, route.passage)
        is Route.Series -> SeriesPage(route.id)
        is Route.Library -> LibraryPage(route.id)
        is Route.Channel -> ChannelScreen(route.id)
        is Route.Playlist -> PlaylistPage(route.id)
    }
}
