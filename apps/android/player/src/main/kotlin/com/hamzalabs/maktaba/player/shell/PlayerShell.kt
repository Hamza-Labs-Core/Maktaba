package com.hamzalabs.maktaba.player.shell

import androidx.activity.compose.BackHandler
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.material3.adaptive.WindowAdaptiveInfo
import androidx.compose.material3.adaptive.currentWindowAdaptiveInfoV2
import androidx.compose.material3.adaptive.navigationsuite.NavigationSuiteScaffold
import androidx.compose.material3.adaptive.navigationsuite.NavigationSuiteType
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.saveable.rememberSaveableStateHolder
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.SemanticsPropertyReceiver
import androidx.compose.ui.semantics.semantics
import androidx.navigation.NavGraphBuilder
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import androidx.window.core.layout.WindowSizeClass

/** How the sections are offered (FR-8). */
enum class ShellNavigation { Bar, Rail, Drawer, None }

/**
 * The navigation for a window, from Material's adaptive info (the window, never the device):
 * a bottom bar in a compact window or tabletop posture (the bottom half flat on a table is what
 * is touched), a rail in a medium one (a foldable open flat, a small tablet), a permanent drawer
 * in an expanded one (a large tablet). A wide window too short for a drawer's list (a phone
 * turned sideways: compact height) keeps the rail.
 */
fun navigationFor(info: WindowAdaptiveInfo): ShellNavigation {
    val size = info.windowSizeClass
    return when {
        info.windowPosture.isTabletop -> ShellNavigation.Bar
        !size.isWidthAtLeastBreakpoint(WindowSizeClass.WIDTH_DP_MEDIUM_LOWER_BOUND) -> ShellNavigation.Bar
        size.isWidthAtLeastBreakpoint(WindowSizeClass.WIDTH_DP_EXPANDED_LOWER_BOUND) &&
            size.isHeightAtLeastBreakpoint(WindowSizeClass.HEIGHT_DP_MEDIUM_LOWER_BOUND) -> ShellNavigation.Drawer
        else -> ShellNavigation.Rail
    }
}

/** The navigation the shell shows, for tests: one of [ShellNavigation]'s names. */
val ShellNavigationKey = SemanticsPropertyKey<ShellNavigation>("ShellNavigation")
private var SemanticsPropertyReceiver.shellNavigation by ShellNavigationKey

/** What a screen of the Player is given to move: open a screen or a section, go back. */
class PlayerNavigator internal constructor(
    private val controller: NavHostController,
    private val select: (PlayerSection) -> Unit,
) {
    /** Opens [route]: a section's root selects that section; any other screen is pushed on this section's stack. */
    fun open(route: Route) {
        val section = PlayerSection.of(route)
        if (section != null) select(section) else controller.navigate(route)
    }

    /** Back one screen; false at the section's root, which always stays (never a blank section). */
    fun back(): Boolean = canGoBack && controller.popBackStack()

    /** True when Back has a screen to return to in this section. */
    val canGoBack: Boolean get() = controller.previousBackStackEntry != null

    private val reselectListeners = mutableListOf<() -> Unit>()

    /**
     * The section was selected again while shown: back to its root, and each listener told (a
     * screen that keeps its own pages, as today's library browser does, returns to its first one).
     */
    internal fun reselect(root: Route) {
        controller.popBackStack(root, inclusive = false)
        reselectListeners.toList().forEach { it() }
    }

    internal fun addReselectListener(listener: () -> Unit) { reselectListeners += listener }
    internal fun removeReselectListener(listener: () -> Unit) { reselectListeners -= listener }
}

/**
 * Runs [action] each time the person selects the shown section again (its root is already
 * shown, so a screen with pages of its own goes back to its first one).
 */
@Composable
fun OnReselect(nav: PlayerNavigator, action: () -> Unit) {
    val latest by rememberUpdatedState(action)
    DisposableEffect(nav) {
        val listener = { latest() }
        nav.addReselectListener(listener)
        onDispose { nav.removeReselectListener(listener) }
    }
}

/** Draws a route's screen. The app's is [AppScreens]; a test may give its own. */
typealias PlayerScreens = @Composable (route: Route, nav: PlayerNavigator) -> Unit

/**
 * The signed-in Player: the [sections] in Material's navigation suite (bar, rail or drawer, by
 * the window), each with its own stack of screens (Navigation Compose), as each tab of the
 * Apple Player holds its own NavigationStack:
 * - switching sections keeps each one's stack and scroll positions;
 * - re-selecting the shown section returns it to its root;
 * - Back pops the section's stack, then from another section's root returns to the first section,
 *   then leaves the app.
 *
 * The navigation hides while a video plays full screen ([immersive]) and when only one section
 * is reachable (a bar of one is no choice).
 *
 * [link] is a screen a deep link asked for (`maktaba://video/{id}`, FR-9): it is pushed on the
 * shown section's stack, then [onLinkOpened] says it was, so it opens once.
 */
@Composable
fun PlayerShell(
    modifier: Modifier = Modifier,
    sections: List<PlayerSection> = PlayerSection.enabled,
    adaptiveInfo: WindowAdaptiveInfo = currentWindowAdaptiveInfoV2(),
    immersive: Boolean = false,
    screens: PlayerScreens = AppScreens,
    link: Route? = null,
    onLinkOpened: () -> Unit = {},
) {
    require(sections.isNotEmpty()) { "the shell needs a section" }
    var currentName by rememberSaveable { mutableStateOf(sections.first().name) }
    val current = sections.firstOrNull { it.name == currentName } ?: sections.first()
    // asks the shown section to return to its root (re-selected); counts, so each tap is one ask
    var toRoot by remember { mutableStateOf(0) }
    val stacks = rememberSaveableStateHolder()

    val navigation = if (immersive || sections.size < 2) ShellNavigation.None else navigationFor(adaptiveInfo)
    NavigationSuiteScaffold(
        modifier = modifier.semantics { shellNavigation = navigation },
        layoutType = navigation.suiteType,
        navigationSuiteItems = {
            sections.forEach { section ->
                val selected = section == current
                item(
                    selected = selected,
                    onClick = { if (selected) toRoot++ else currentName = section.name },
                    icon = { Icon(if (selected) section.selectedIcon else section.icon, contentDescription = null) },
                    label = { Text(stringResource(section.label)) },
                    modifier = Modifier.testTag("section-${section.name}"),
                )
            }
        },
    ) {
        stacks.SaveableStateProvider(current.name) {
            val controller = rememberNavController()
            val navigator = remember(controller) { PlayerNavigator(controller) { s -> if (s in sections) currentName = s.name } }
            NavHost(controller, startDestination = current.root) { playerGraph(screens, navigator) }

            // a deep link: its screen on this section's stack, once
            LaunchedEffect(link) {
                if (link != null) {
                    navigator.open(link)
                    onLinkOpened()
                }
            }

            // re-selected: back to the section's root
            var handled by remember { mutableStateOf(toRoot) }
            LaunchedEffect(toRoot) {
                if (handled != toRoot) {
                    handled = toRoot
                    navigator.reselect(current.root)
                }
            }
            // at another section's root, Back returns to the first section (Material's guidance)
            val entry by controller.currentBackStackEntryAsState()
            val atRoot = entry != null && controller.previousBackStackEntry == null
            BackHandler(enabled = atRoot && current != sections.first()) { currentName = sections.first().name }
        }
    }
}

private val ShellNavigation.suiteType: NavigationSuiteType
    get() = when (this) {
        ShellNavigation.Bar -> NavigationSuiteType.NavigationBar
        ShellNavigation.Rail -> NavigationSuiteType.NavigationRail
        ShellNavigation.Drawer -> NavigationSuiteType.NavigationDrawer
        ShellNavigation.None -> NavigationSuiteType.None
    }

/** Every route in a section's graph: its root and every screen it can push. */
private fun NavGraphBuilder.playerGraph(screens: PlayerScreens, nav: PlayerNavigator) {
    composable<Route.Home> { screens(Route.Home, nav) }
    composable<Route.Libraries> { screens(Route.Libraries, nav) }
    composable<Route.LiveTv> { screens(Route.LiveTv, nav) }
    composable<Route.Search> { screens(Route.Search, nav) }
    composable<Route.Profile> { screens(Route.Profile, nav) }
    composable<Route.Video> { screens(it.toRoute<Route.Video>(), nav) }
    composable<Route.Series> { screens(it.toRoute<Route.Series>(), nav) }
    composable<Route.Library> { screens(it.toRoute<Route.Library>(), nav) }
    composable<Route.Channel> { screens(it.toRoute<Route.Channel>(), nav) }
    composable<Route.Watch> { screens(it.toRoute<Route.Watch>(), nav) }
}
