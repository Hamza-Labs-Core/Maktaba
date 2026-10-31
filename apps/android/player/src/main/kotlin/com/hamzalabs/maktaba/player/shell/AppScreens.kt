package com.hamzalabs.maktaba.player.shell

import com.hamzalabs.maktaba.player.home.HomeScreen
import com.hamzalabs.maktaba.player.libraries.LibrariesScreen
import com.hamzalabs.maktaba.player.libraries.LibraryScreen
import com.hamzalabs.maktaba.player.libraries.SeriesScreen
import com.hamzalabs.maktaba.player.live.ChannelScreen
import com.hamzalabs.maktaba.player.live.LiveTvScreen
import com.hamzalabs.maktaba.player.profile.ProfileScreen
import com.hamzalabs.maktaba.player.search.SearchScreen
import com.hamzalabs.maktaba.player.video.VideoScreen
import com.hamzalabs.maktaba.player.watch.WatchRouteScreen

/**
 * Each route's screen. The screens live in their own folders (`home/`, `libraries/`, `video/`,
 * `search/`, `live/`, `profile/`, `watch/`), one per screen story; until a story merges, its
 * screen is a placeholder and its section stays out of [PlayerSection.enabled].
 */
val AppScreens: PlayerScreens = { route, nav ->
    when (route) {
        Route.Home -> HomeScreen(nav)
        Route.Libraries -> LibrariesScreen(nav)
        Route.LiveTv -> LiveTvScreen(nav)
        Route.Search -> SearchScreen(nav)
        Route.Profile -> ProfileScreen(nav)
        is Route.Video -> VideoScreen(route.id, nav)
        is Route.Series -> SeriesScreen(route.id, nav)
        is Route.Library -> LibraryScreen(route.id, nav)
        is Route.Channel -> ChannelScreen(route.id, nav)
        is Route.Watch -> WatchRouteScreen(route.videoId, route.startSec, nav)
    }
}
