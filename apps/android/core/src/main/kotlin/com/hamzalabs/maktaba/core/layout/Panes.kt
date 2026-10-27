package com.hamzalabs.maktaba.core.layout

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.navigationBars
import androidx.compose.foundation.layout.statusBars
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.VerticalDivider
import androidx.compose.material3.adaptive.currentWindowAdaptiveInfoV2
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.positionInWindow
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import androidx.window.core.layout.WindowSizeClass

/**
 * How a screen is laid out, from the window (never the device):
 * - [One]: one pane at a time (a phone, a foldable folded or open flat in a narrow window);
 * - [ListDetail]: the list beside the page (a tablet, a foldable open flat);
 * - [Tabletop]: half folded with the hinge across (the bottom half flat on a table): what is
 *   looked at on the upright top half, what is touched on the bottom one;
 * - [Book]: half folded with the hinge upright, held like a book: one pane each side.
 */
sealed interface Panes {
    data object One : Panes
    data object ListDetail : Panes
    /** [hinge] is where the hinge starts and ends, in px from the window's top. */
    data class Tabletop(val hinge: IntRange) : Panes
    /** [hinge] is where the hinge starts and ends, in px from the window's left. */
    data class Book(val hinge: IntRange) : Panes

    val split: Boolean get() = this is Tabletop || this is Book
}

/** A fold the window reports: half open or flat, across or upright, where it is. */
data class Fold(val halfOpen: Boolean, val vertical: Boolean, val start: Int, val end: Int)

/**
 * The layout for a window [widthDp] wide with [fold] (null: none, or not in this window).
 * A half-open fold splits at the hinge (as on the iPhone Duo); otherwise 600dp and wider
 * shows the list beside the page (Material's medium width class and up).
 */
fun panesFor(widthDp: Int, fold: Fold?): Panes = when {
    fold != null && fold.halfOpen && !fold.vertical -> Panes.Tabletop(fold.start..fold.end)
    fold != null && fold.halfOpen && fold.vertical -> Panes.Book(fold.start..fold.end)
    widthDp >= WindowSizeClass.WIDTH_DP_MEDIUM_LOWER_BOUND -> Panes.ListDetail
    else -> Panes.One
}

/** The current window's [Panes], from Jetpack WindowManager's fold (through Material's adaptive info). */
@Composable
fun currentPanes(): Panes {
    val info = currentWindowAdaptiveInfoV2()
    val hinge = info.windowPosture.hingeList.firstOrNull()
    val fold = hinge?.let {
        val b = it.bounds
        Fold(
            halfOpen = !it.isFlat,
            vertical = it.isVertical,
            start = if (it.isVertical) b.left.toInt() else b.top.toInt(),
            end = if (it.isVertical) b.right.toInt() else b.bottom.toInt(),
        )
    }
    return panesFor(info.windowSizeClass.minWidthDp, fold)
}

/**
 * Two panes split at a half-open hinge: [first] on the top half (tabletop) or the left one
 * (book), [second] on the other. The hinge's own pixels stay empty. Falls back to halves.
 */
@Composable
fun HingeSplit(
    panes: Panes,
    modifier: Modifier = Modifier,
    first: @Composable () -> Unit,
    second: @Composable () -> Unit,
) {
    var origin by remember { mutableStateOf(Offset.Zero) }
    var size by remember { mutableStateOf(IntSize.Zero) }
    val density = LocalDensity.current
    val rtl = LocalLayoutDirection.current == LayoutDirection.Rtl
    Box(
        modifier.fillMaxSize().onGloballyPositioned {
            origin = it.positionInWindow()
            size = it.size
        },
    ) {
        when (panes) {
            is Panes.Tabletop -> {
                val (a, b) = cut(panes.hinge, origin.y.toInt(), size.height)
                // the top half does not reach the navigation bar, nor the bottom one the status bar
                Column(Modifier.fillMaxSize()) {
                    Box(Modifier.fillMaxWidth().height(with(density) { a.toDp() }).consumeWindowInsets(WindowInsets.navigationBars)) { first() }
                    Box(Modifier.fillMaxWidth().height(with(density) { (b - a).toDp() }))
                    Box(Modifier.fillMaxWidth().weight(1f).consumeWindowInsets(WindowInsets.statusBars)) { second() }
                }
            }
            is Panes.Book -> {
                // right to left, the Row starts at the right: the first pane is the hinge's right part
                val (lead, gap) = bookCut(panes.hinge, origin.x.toInt(), size.width, rtl)
                Row(Modifier.fillMaxSize()) {
                    Box(Modifier.fillMaxHeight().width(with(density) { lead.toDp() })) { first() }
                    Box(Modifier.fillMaxHeight().width(with(density) { gap.toDp() }))
                    Box(Modifier.fillMaxHeight().weight(1f)) { second() }
                }
            }
            else -> Row(Modifier.fillMaxSize()) {
                Box(Modifier.weight(1f)) { first() }
                VerticalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                Box(Modifier.weight(1f)) { second() }
            }
        }
    }
}

/**
 * Where a hinge at [hinge] (window px) cuts a pane that starts at [origin] and is [length]
 * long: the first pane's end and the second's start, in the pane's px. Halves when the hinge
 * is not inside the pane (or before it is measured).
 */
fun cut(hinge: IntRange, origin: Int, length: Int): Pair<Int, Int> {
    val a = hinge.first - origin
    val b = hinge.last - origin
    if (length <= 0 || a <= 0 || b >= length || b < a) return (length / 2) to (length / 2)
    return a to b
}

/**
 * A book posture's cut for a Row [length] px wide at [origin] (window px) with the hinge at
 * [hinge]: the leading pane's width and the hinge's. A Row leads from the left, or from the
 * right when [rtl], so right to left the leading pane is the part right of the hinge: the
 * hinge's own pixels stay empty either way.
 */
fun bookCut(hinge: IntRange, origin: Int, length: Int, rtl: Boolean): Pair<Int, Int> {
    val (a, b) = cut(hinge, origin, length)
    return (if (rtl) length - b else a) to (b - a)
}

/** The list pane's width beside a page. */
val ListPaneWidth = 380.dp
