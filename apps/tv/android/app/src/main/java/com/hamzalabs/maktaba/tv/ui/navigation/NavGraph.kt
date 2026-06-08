package com.hamzalabs.maktaba.tv.ui.navigation

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.navigation.NavHostController
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import com.hamzalabs.maktaba.tv.data.SettingsStore
import com.hamzalabs.maktaba.tv.data.repository.MediaRepository
import com.hamzalabs.maktaba.tv.ui.components.TopBar
import com.hamzalabs.maktaba.tv.ui.screens.HomeScreen
import com.hamzalabs.maktaba.tv.ui.screens.LibraryScreen
import com.hamzalabs.maktaba.tv.ui.screens.MediaGridScreen
import com.hamzalabs.maktaba.tv.ui.screens.PlayerScreen
import com.hamzalabs.maktaba.tv.ui.screens.SearchScreen
import com.hamzalabs.maktaba.tv.ui.screens.SettingsScreen

/** Route table. Top-level tabs + drill-down destinations. */
object Routes {
    const val HOME = "home"
    const val LIBRARIES = "libraries"
    const val SEARCH = "search"
    const val SETTINGS = "settings"
    const val MEDIA_GRID = "library/{libraryId}"
    const val PLAYER = "player/{videoId}"

    fun mediaGrid(libraryId: String) = "library/$libraryId"
    fun player(videoId: String) = "player/$videoId"
}

private val TABS = listOf(
    Routes.HOME to "Home",
    Routes.LIBRARIES to "Libraries",
    Routes.SEARCH to "Search",
    Routes.SETTINGS to "Settings",
)

@Composable
fun NavGraph(
    repository: MediaRepository,
    settings: SettingsStore,
    startDestination: String = Routes.HOME,
    navController: NavHostController = rememberNavController(),
) {
    val backStack by navController.currentBackStackEntryAsState()
    val currentRoute = backStack?.destination?.route
    val selectedTab = TABS.indexOfFirst { it.first == currentRoute }.coerceAtLeast(0)

    // Hide the tab bar in immersive contexts (the player).
    val showBar = currentRoute != Routes.PLAYER

    Column(Modifier.fillMaxSize()) {
        if (showBar) {
            TopBar(
                tabs = TABS.map { it.second },
                selectedIndex = selectedTab,
                onSelect = { index ->
                    val route = TABS[index].first
                    if (route != currentRoute) {
                        navController.navigate(route) {
                            popUpTo(Routes.HOME) { saveState = true }
                            launchSingleTop = true
                            restoreState = true
                        }
                    }
                },
            )
        }

        NavHost(navController = navController, startDestination = startDestination) {
            composable(Routes.HOME) {
                HomeScreen(repository) { videoId ->
                    navController.navigate(Routes.player(videoId))
                }
            }
            composable(Routes.LIBRARIES) {
                LibraryScreen(repository) { id, _ ->
                    navController.navigate(Routes.mediaGrid(id))
                }
            }
            composable(Routes.SEARCH) {
                SearchScreen(repository) { videoId ->
                    navController.navigate(Routes.player(videoId))
                }
            }
            composable(Routes.SETTINGS) {
                SettingsScreen(repository, settings) {
                    navController.navigate(Routes.HOME) {
                        popUpTo(Routes.SETTINGS) { inclusive = true }
                    }
                }
            }
            composable(
                Routes.MEDIA_GRID,
                arguments = listOf(navArgument("libraryId") { type = NavType.StringType }),
            ) { entry ->
                val libraryId = entry.arguments?.getString("libraryId").orEmpty()
                MediaGridScreen(repository, libraryId) { videoId ->
                    navController.navigate(Routes.player(videoId))
                }
            }
            composable(
                Routes.PLAYER,
                arguments = listOf(navArgument("videoId") { type = NavType.StringType }),
            ) { entry ->
                val videoId = entry.arguments?.getString("videoId").orEmpty()
                PlayerScreen(repository, videoId)
            }
        }
    }
}
