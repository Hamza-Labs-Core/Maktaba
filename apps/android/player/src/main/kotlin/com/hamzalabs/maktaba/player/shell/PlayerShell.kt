package com.hamzalabs.maktaba.player.shell

import androidx.activity.compose.BackHandler
import androidx.compose.material3.Icon
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.adaptive.HingeInfo
import androidx.compose.material3.adaptive.WindowAdaptiveInfo
import androidx.compose.material3.adaptive.currentWindowAdaptiveInfoV2
import androidx.compose.material3.adaptive.navigationsuite.NavigationSuiteScaffold
import androidx.compose.material3.adaptive.navigationsuite.NavigationSuiteType
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
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
import com.hamzalabs.maktaba.core.layout.HingeSplit
import com.hamzalabs.maktaba.core.layout.LocalAdaptiveInfo
import com.hamzalabs.maktaba.core.layout.Panes
import com.hamzalabs.maktaba.core.layout.currentPanes
import com.hamzalabs.maktaba.core.ui.NavigationLabel
import com.hamzalabs.maktaba.core.ui.navigationLabelsFit
import androidx.compose.ui.platform.LocalConfiguration

/** How the sections are offered (FR-8). */
enum class ShellNavigation { Bar, Rail, CollapsibleRail, None }

/**
 * The navigation for a window, from Material's adaptive info (the window, never the device):
 * - a bottom bar in a compact window, or half folded on a table (tabletop: the bar sits on the
 *   flat bottom half, the part that is touched);
 * - the collapsible rail ([CollapsibleRailFrame]: icons, expanded over the content on
 *   demand) half folded like a book (it sits on the start page), and in an expanded window
 *   (a large tablet, a foldable open flat in landscape), where a permanent drawer took 360 dp
 *   and squeezed a list-detail page;
 * - a rail in a medium one (a foldable open flat in portrait, a small tablet), and in a wide
 *   window too short for the collapsible rail's list (a phone on its side: compact height).
 */
fun navigationFor(info: WindowAdaptiveInfo): ShellNavigation {
    val size = info.windowSizeClass
    return when {
        info.windowPosture.isTabletop -> ShellNavigation.Bar
        bookHinge(info) != null -> ShellNavigation.CollapsibleRail
        !size.isWidthAtLeastBreakpoint(WindowSizeClass.WIDTH_DP_MEDIUM_LOWER_BOUND) -> ShellNavigation.Bar
        size.isWidthAtLeastBreakpoint(WindowSizeClass.WIDTH_DP_EXPANDED_LOWER_BOUND) &&
            size.isHeightAtLeastBreakpoint(WindowSizeClass.HEIGHT_DP_MEDIUM_LOWER_BOUND) -> ShellNavigation.CollapsibleRail
        else -> ShellNavigation.Rail
    }
}

/** The hinge when the window is half folded like a book (upright, half open), else null. */
fun bookHinge(info: WindowAdaptiveInfo): HingeInfo? =
    info.windowPosture.hingeList.firstOrNull { it.isVertical && !it.isFlat }

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
 * The signed-in Player: the [sections] in a bar, a rail or the collapsible rail (by the window
 * and its fold, [navigationFor]), each with its own stack of screens (Navigation Compose), as each tab of the
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
    val choose: (PlayerSection) -> Unit = { section -> if (section == current) toRoot++ else currentName = section.name }
    val body: @Composable () -> Unit = {
        stacks.SaveableStateProvider(current.name) {
            val controller = rememberNavController()
            val navigator = remember(controller) { PlayerNavigator(controller) { s -> if (s in sections) currentName = s.name } }
            // At another section's root, Back returns to the first section (Material's guidance).
            // Registered before the screens (the NavHost below): the handler registered last is
            // asked first, so a screen's own Back (a page it keeps, as Profile's Settings) wins.
            val entry by controller.currentBackStackEntryAsState()
            val atRoot = entry != null && controller.previousBackStackEntry == null
            BackHandler(enabled = atRoot && current != sections.first()) { currentName = sections.first().name }
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
        }
    }

    // the screens lay themselves out by the same window (and hinge) as the navigation
    // One tree whatever the navigation, so the screens are never composed anew when it changes
    // (a video going full screen hides it): the suite draws the bar and the rail, the frame
    // inside it the collapsible rail.
    val labels = sections.map { stringResource(it.label) }
    // a cover screen at a large text size: the selected label only (see navigationLabelsFit)
    val allLabels = navigation != ShellNavigation.Bar || navigationLabelsFit(labels, LocalConfiguration.current.screenWidthDp)
    CompositionLocalProvider(LocalAdaptiveInfo provides adaptiveInfo) {
        NavigationSuiteScaffold(
            modifier = modifier.semantics { shellNavigation = navigation },
            layoutType = navigation.suiteType,
            navigationSuiteItems = {
                sections.forEachIndexed { i, section ->
                    val selected = section == current
                    item(
                        selected = selected,
                        onClick = { choose(section) },
                        icon = { Icon(if (selected) section.selectedIcon else section.icon, contentDescription = null) },
                        label = { NavigationLabel(labels[i]) },
                        alwaysShowLabel = allLabels,
                        modifier = Modifier.testTag("section-${section.name}"),
                    )
                }
            },
        ) {
            CollapsibleRailFrame(
                show = navigation == ShellNavigation.CollapsibleRail,
                sections = sections,
                current = current,
                onChoose = choose,
                hinge = bookHinge(adaptiveInfo),
                content = body,
            )
        }
    }
}

private val ShellNavigation.suiteType: NavigationSuiteType
    get() = when (this) {
        ShellNavigation.Bar -> NavigationSuiteType.NavigationBar
        ShellNavigation.Rail -> NavigationSuiteType.NavigationRail
        // drawn by CollapsibleRailFrame, not the suite
        ShellNavigation.CollapsibleRail, ShellNavigation.None -> NavigationSuiteType.None
    }

/** Every route in a section's graph: its root and every screen it can push. */
private fun NavGraphBuilder.playerGraph(screens: PlayerScreens, nav: PlayerNavigator) {
    composable<Route.Home> { OffTheHinge(Route.Home) { screens(Route.Home, nav) } }
    composable<Route.Libraries> { OffTheHinge(Route.Libraries) { screens(Route.Libraries, nav) } }
    composable<Route.LiveTv> { OffTheHinge(Route.LiveTv) { screens(Route.LiveTv, nav) } }
    composable<Route.Search> { OffTheHinge(Route.Search) { screens(Route.Search, nav) } }
    composable<Route.Profile> { OffTheHinge(Route.Profile) { screens(Route.Profile, nav) } }
    composable<Route.Video> { it.toRoute<Route.Video>().let { r -> OffTheHinge(r) { screens(r, nav) } } }
    composable<Route.Series> { it.toRoute<Route.Series>().let { r -> OffTheHinge(r) { screens(r, nav) } } }
    composable<Route.Library> { it.toRoute<Route.Library>().let { r -> OffTheHinge(r) { screens(r, nav) } } }
    composable<Route.Channel> { it.toRoute<Route.Channel>().let { r -> OffTheHinge(r) { screens(r, nav) } } }
    composable<Route.Watch> { it.toRoute<Route.Watch>().let { r -> OffTheHinge(r) { screens(r, nav) } } }
}

/** The screens that split themselves around a tabletop hinge (their page above, their list below). */
private val Route.splitsAtTheHinge: Boolean get() = this == Route.Libraries

/**
 * Half folded on a table, a screen that does not split itself is drawn on the upright top
 * half (looked at), never across the hinge; the flat bottom half keeps the navigation bar.
 */
@Composable
private fun OffTheHinge(route: Route, screen: @Composable () -> Unit) {
    val panes = currentPanes()
    if (panes is Panes.Tabletop && !route.splitsAtTheHinge) {
        HingeSplit(panes, first = { Box(Modifier.fillMaxSize().testTag("tabletop-top")) { screen() } }, second = {})
    } else {
        screen()
    }
}
