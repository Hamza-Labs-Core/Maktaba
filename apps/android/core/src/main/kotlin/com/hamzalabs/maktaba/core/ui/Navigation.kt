package com.hamzalabs.maktaba.core.ui

import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.text.BasicText
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.LocalTextStyle
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.takeOrElse
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.unit.takeOrElse

/**
 * The one text size a bar's labels share: each label reports the largest size it fits whole at
 * in its own slot, and all of them draw at the smallest of those (an end slot is narrower than a
 * middle one; sized one by one, the five came out at two sizes).
 */
class NavigationLabelSizes {
    private val fits = mutableStateMapOf<String, Float>()

    /** The shared size in sp, or null before any label has measured. */
    val size: Float? get() = fits.values.minOrNull()

    internal fun report(text: String, sp: Float) {
        if (fits[text] != sp) fits[text] = sp
    }
}

/** One [NavigationLabelSizes] for a bar of [labels], anew when the labels, width or text size change. */
@Composable
fun rememberNavigationLabelSizes(labels: List<String>): NavigationLabelSizes {
    val density = LocalDensity.current
    val width = LocalConfiguration.current.screenWidthDp
    return remember(labels, width, density) { NavigationLabelSizes() }
}

/**
 * A bottom bar's or a rail's label: one line, centred under its icon, kept 4 dp off its slot's
 * sides (an end label otherwise met the screen's edge). A foldable's cover screen gives five items
 * ~65 dp each; a label that wrapped to two lines sat higher than the others, and its left-aligned
 * lines ran into the screen's edge. A word that does not fit at the person's text size steps down
 * until it fits whole (ellipsised only below [MIN_LABEL_SIZE]), to the size the bar's [sizes] share.
 */
@Composable
fun NavigationLabel(text: String, sizes: NavigationLabelSizes? = null, modifier: Modifier = Modifier) {
    val style = LocalTextStyle.current
    val color = style.color.takeOrElse { LocalContentColor.current }
    val measurer = rememberTextMeasurer()
    BoxWithConstraints(modifier.padding(horizontal = 4.dp)) {
        val room = constraints.maxWidth
        val base = style.fontSize.takeOrElse { 12.sp }.value
        val fit = remember(text, room, style, base) {
            var sp = base
            while (sp > MIN_LABEL_SIZE &&
                measurer.measure(text, style.copy(fontSize = sp.sp), maxLines = 1, softWrap = false).size.width > room
            ) sp -= STEP
            sp.coerceAtLeast(MIN_LABEL_SIZE)
        }
        SideEffect { sizes?.report(text, fit) }
        val shown = minOf(fit, sizes?.size ?: fit)
        BasicText(
            text,
            style = style.merge(color = color, fontSize = shown.sp, textAlign = TextAlign.Center),
            maxLines = 1,
            softWrap = false,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/** The smallest a navigation label steps down to before it is cut, in sp. */
private const val MIN_LABEL_SIZE = 5f

/** How far a label steps down at a time, in sp. */
private const val STEP = 0.5f

/**
 * True when every one of [labels] fits whole on one line in a bottom bar [widthDp] wide, at the
 * person's text size (Material's bar: the items share the width, 8 dp apart and from its ends). When they do not
 * (a cover screen at a large text size), the bar shows the selected item's label only
 * (`alwaysShowLabel = false`, Material's answer for a crowded bar): five labels at one size, or
 * one, never five cut short or of five sizes.
 */
@Composable
fun navigationLabelsFit(labels: List<String>, widthDp: Int): Boolean {
    if (labels.isEmpty()) return true
    val measurer = rememberTextMeasurer()
    val style = MaterialTheme.typography.labelMedium
    val density = LocalDensity.current
    return remember(labels, widthDp, style, density) {
        // the bar: 8 dp at each end and between items (NavigationBarHorizontalPadding, NavigationBarItemHorizontalPadding)
        val gap = 8.dp
        val each = with(density) { ((widthDp.dp - gap * (labels.size + 1)) / labels.size).toPx() }
        labels.all { measurer.measure(it, style, maxLines = 1, softWrap = false).size.width <= each }
    }
}

/**
 * The page shown is the one read last and a fresh read is under way: a thin bar over its top,
 * never a blank page or a spinner in its place (tag `revalidating`).
 */
@Composable
fun Revalidating(shown: Boolean, modifier: Modifier = Modifier) {
    if (shown) LinearProgressIndicator(modifier.fillMaxWidth().testTag("revalidating"))
}
