package com.hamzalabs.maktaba.remote

import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.ComposeContentTestRule
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.text.TextLayoutResult
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import kotlin.math.abs

/** Waits up to [timeoutMs] for [check], letting the app settle before each look. */
fun ComposeContentTestRule.settledUntil(timeoutMs: Long = 10_000, check: () -> Boolean) =
    waitUntil(timeoutMs) {
        waitForIdle()
        check()
    }

fun ComposeContentTestRule.has(tag: String): Boolean =
    onAllNodes(hasTestTag(tag), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

/**
 * The bottom bar's labels line up: each one line, all at the same height and top, each centred
 * under its own item and inside the screen (the owner's cover screen: "Needs attention" wrapped
 * to two lines, sat higher than the other four and ran into the screen's left edge). A bar that
 * shows the selected label only (a large text size: Material draws the others transparent, off
 * their place) is checked on the selected one. [selected] is always checked.
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
    }
    val heights = checked.values.map { it.boundsInRoot.height }
    assertTrue("labels of one height: $heights", heights.all { abs(it - heights[0]) <= 1f })
}
