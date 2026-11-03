package com.hamzalabs.maktaba.player.harness

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.adaptive.HingeInfo
import androidx.compose.material3.adaptive.Posture
import androidx.compose.ui.geometry.Rect
import androidx.compose.material3.adaptive.WindowAdaptiveInfo
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.junit4.ComposeContentTestRule
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.unit.Density
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import kotlin.math.abs
import androidx.compose.ui.unit.dp
import androidx.window.core.layout.WindowSizeClass
import androidx.window.core.layout.computeWindowSizeClass
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.player.shell.AppScreens
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerScreens
import com.hamzalabs.maktaba.player.shell.PlayerSection
import com.hamzalabs.maktaba.player.shell.PlayerShell
import com.hamzalabs.maktaba.player.shell.Route

/** The windows the tests lay the Player out in, in dp, with the Robolectric qualifiers that match. */
enum class Window(val widthDp: Int, val heightDp: Int, val qualifiers: String) {
    /** A phone in portrait (Pixel 9). */
    Phone(411, 891, "w411dp-h891dp-xxhdpi"),
    /** A foldable open flat (Pixel 9 Pro Fold, inner screen). */
    Foldable(673, 841, "w673dp-h841dp-xhdpi"),
    /** A foldable open flat held sideways (Pixel 9 Pro Fold, inner screen): an expanded window. */
    FoldableWide(841, 673, "w841dp-h673dp-xhdpi"),
    /** A large tablet in landscape. */
    Tablet(1280, 800, "w1280dp-h800dp-xhdpi"),
    /** Galaxy Z Fold 5/6 closed (cover): 904 × 2316 px at 420 dpi. */
    FoldCover(344, 882, "w344dp-h882dp-420dpi"),
    /**
     * Motorola Razr Fold (2026) closed (cover): 1080 × 2520 px, 6.6" (~415 ppi). Its density is
     * not published: 440 dpi (2.75) is an estimate from a device screenshot (the bar's 80 dp
     * and the list's 20 sp lines measure ~2.7 px per dp). Its inner screen (2232 × 2484 px,
     * 8.1") opens as an expanded window, as [FoldableWide].
     */
    RazrFoldCover(393, 916, "w393dp-h916dp-440dpi"),
    /** Pixel 9 Pro Fold closed (outer): 1080 × 2424 px at 420 dpi. */
    PixelFoldCover(411, 923, "w411dp-h923dp-420dpi"),
}

/**
 * Material's adaptive info for [this] window half folded at [hinge] (window px, as WindowManager
 * reports it): upright when [vertical] (held like a book), else across (tabletop). The JVM
 * cannot fold a window, so the shell is given this one.
 */
fun Window.folded(hinge: Rect, vertical: Boolean) = WindowAdaptiveInfo(
    WindowSizeClass.BREAKPOINTS_V2.computeWindowSizeClass(widthDp.toFloat(), heightDp.toFloat()),
    Posture(
        isTabletop = !vertical,
        hingeList = listOf(HingeInfo(hinge, isFlat = false, isVertical = vertical, isSeparating = true, isOccluding = false)),
    ),
)

/** A 12 px hinge upright at the middle of a window [widthPx] × [heightPx]. */
fun uprightHinge(widthPx: Int, heightPx: Int) = Rect(widthPx / 2f - 6f, 0f, widthPx / 2f + 6f, heightPx.toFloat())

/** A 12 px hinge across the middle of a window [widthPx] × [heightPx]. */
fun acrossHinge(widthPx: Int, heightPx: Int) = Rect(0f, heightPx / 2f - 6f, widthPx.toFloat(), heightPx / 2f + 6f)

/** Material's adaptive info for a [widthDp] × [heightDp] window, half folded flat on a table or not. */
fun adaptiveInfo(widthDp: Int, heightDp: Int, tabletop: Boolean = false) = WindowAdaptiveInfo(
    WindowSizeClass.BREAKPOINTS_V2.computeWindowSizeClass(widthDp.toFloat(), heightDp.toFloat()),
    Posture(isTabletop = tabletop),
)

fun Window.info(tabletop: Boolean = false) = adaptiveInfo(widthDp, heightDp, tabletop)

/**
 * The shell in the app's dark theme, as MainActivity shows it, with [home] signed in (its
 * screens read it) and the window's adaptive info given rather than measured.
 */
fun ComposeContentTestRule.setShell(
    home: FakeHome? = null,
    sections: List<PlayerSection> = PlayerSection.entries,
    window: Window = Window.Phone,
    tabletop: Boolean = false,
    immersive: Boolean = false,
    info: WindowAdaptiveInfo = window.info(tabletop),
    fontScale: Float = 1f,
    screens: PlayerScreens = AppScreens,
) {
    setContent {
        val density = LocalDensity.current
        CompositionLocalProvider(LocalDensity provides Density(density.density, fontScale)) { MaktabaTheme(ThemeMode.Dark) {
            Surface {
                if (home != null) {
                    CompositionLocalProvider(LocalPlayerHome provides home.home) {
                        PlayerShell(sections = sections, adaptiveInfo = info, immersive = immersive, screens = screens)
                    }
                } else {
                    PlayerShell(sections = sections, adaptiveInfo = info, immersive = immersive, screens = screens)
                }
            }
        } }
    }
}

/**
 * The bottom bar's labels line up: each shown one is one line, whole, inside the screen and
 * centred under its item, all at the same top and height (seen on a real cover screen: a label that
 * wrapped sat higher than the others and ran into the screen's edge). A bar that shows the
 * selected label only (a large text size: Material draws the others transparent, off their
 * place) is checked on the selected one. [selected] is always checked.
 */
fun ComposeContentTestRule.assertBarLabelsAligned(labels: Map<String, String>, selected: String = labels.keys.first()) {
    val width = onRoot().fetchSemanticsNode().size.width
    val nodes = labels.mapValues { (tag, text) ->
        onAllNodes(hasText(text) and hasAnyAncestor(hasTestTag(tag)), useUnmergedTree = true).fetchSemanticsNodes().single()
    }
    // every label at one top: all shown; else the selected one only
    val all = nodes.values.map { it.boundsInRoot.top }.let { tops -> tops.all { abs(it - tops[0]) <= 1f } }
    val checked = if (all) nodes else nodes.filterKeys { it == selected }
    checked.forEach { (tag, node) ->
        val text = labels.getValue(tag)
        val item = onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().single().boundsInRoot
        val layout = mutableListOf<TextLayoutResult>()
        node.config[SemanticsActions.GetTextLayoutResult].action!!.invoke(layout)
        assertEquals("$text: one line", 1, layout.single().lineCount)
        assertFalse("$text: whole, not cut", layout.single().isLineEllipsized(0))
        val b = node.boundsInRoot
        assertTrue("$text inside the screen: $b in 0..$width", b.left >= 0f && b.right <= width.toFloat())
        assertTrue("$text centred under its item: $b, item $item", abs(b.center.x - item.center.x) <= 2f)
        assertTrue("$text inside its item: $b, item $item", b.left >= item.left - 1f && b.right <= item.right + 1f)
        // the glyphs themselves, centred in the label, keep off the screen's edges (an end label touched them)
        val drawn = layout.single().multiParagraph.getLineWidth(0)
        val edge = 2 * density.density
        val glyphs = b.center.x - drawn / 2 to b.center.x + drawn / 2
        assertTrue("$text off the screen's edges: $glyphs in 0..$width", glyphs.first >= edge && glyphs.second <= width - edge)
    }
    val heights = checked.values.map { it.boundsInRoot.height }
    assertTrue("labels of one height: $heights", heights.all { abs(it - heights[0]) <= 1f })
}

/**
 * Screens for testing the shell alone: every section's root is a list of 60 rows tagged
 * `list` (a row opens a video, `row-<n>`); a video says `video <id>`.
 */
val ListScreens: PlayerScreens = { route, nav ->
    when (route) {
        is Route.Video -> Text("video ${route.id}", Modifier.testTag("video"))
        else -> LazyColumn(Modifier.fillMaxSize().testTag("list")) {
            items((1..60).toList()) { n ->
                Text(
                    "$route row $n",
                    Modifier.testTag("row-$n").clickable { nav.open(Route.Video("v$n")) }.padding(16.dp),
                )
            }
        }
    }
}
