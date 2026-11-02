package com.hamzalabs.maktaba.player.shell

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.MenuOpen
import androidx.compose.material.icons.filled.Menu
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.WideNavigationRail
import androidx.compose.material3.WideNavigationRailItem
import androidx.compose.material3.WideNavigationRailValue
import androidx.compose.material3.adaptive.HingeInfo
import androidx.compose.material3.rememberWideNavigationRailState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.input.pointer.PointerType
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.boundsInWindow
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.onClick
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.player.R
import kotlinx.coroutines.launch

/** The collapsed rail's width (Material's wide navigation rail): the content always starts here. */
val CollapsedRailWidth = 96.dp

/**
 * The sections in a rail of icons at the start edge (a large tablet, a foldable open flat in a
 * wide window, a foldable held like a book). The rail's menu button, or a mouse or trackpad
 * resting on it, expands it to show the names; it closes again once a section is chosen, on a
 * tap outside it, on Back, or when the pointer that opened it leaves.
 *
 * Expanded, the rail lies over the content with a scrim (modal): the content keeps its width
 * and never reflows. Held like a book ([hinge] upright, half open), the expanded rail stays on
 * the start page: it is never wider than the part before the hinge.
 */
@Composable
internal fun CollapsibleRailFrame(
    show: Boolean,
    sections: List<PlayerSection>,
    current: PlayerSection,
    onChoose: (PlayerSection) -> Unit,
    hinge: HingeInfo?,
    modifier: Modifier = Modifier,
    content: @Composable () -> Unit,
) {
    val state = rememberWideNavigationRailState()
    val scope = rememberCoroutineScope()
    val expanded = state.targetValue == WideNavigationRailValue.Expanded
    // opened by a resting pointer: it closes when that pointer leaves
    var byHover by remember { mutableStateOf(false) }
    val expand = { scope.launch { state.expand() }; Unit }
    val collapse = { byHover = false; scope.launch { state.collapse() }; Unit }
    var bounds by remember { mutableStateOf(Rect.Zero) }
    val density = LocalDensity.current
    val rtl = LocalLayoutDirection.current == LayoutDirection.Rtl
    val maxWidth = startPageWidth(hinge, bounds, rtl)?.let { with(density) { it.toDp() } } ?: Dp.Unspecified
    val expandLabel = stringResource(R.string.shell_nav_expand)
    val collapseLabel = stringResource(R.string.shell_nav_collapse)
    // hidden (a video full screen, another window): it opens collapsed when it shows again
    LaunchedEffect(show) { if (!show) state.snapTo(WideNavigationRailValue.Collapsed) }

    Box(modifier.fillMaxSize().onGloballyPositioned { bounds = it.boundsInWindow() }) {
        // always here and first, so the screens stay composed when the rail shows or hides; its
        // width never changes as the rail expands (the rail lies over it)
        Box(Modifier.fillMaxSize().padding(start = if (show) CollapsedRailWidth else 0.dp).testTag("shell-content")) { content() }
        if (!show) return@Box
        if (expanded) {
            Box(
                Modifier
                    .fillMaxSize()
                    .background(MaterialTheme.colorScheme.scrim.copy(alpha = 0.32f))
                    .clickable(interactionSource = remember { MutableInteractionSource() }, indication = null, onClick = collapse)
                    .semantics {
                        contentDescription = collapseLabel
                        onClick(collapseLabel) { collapse(); true }
                    }
                    .testTag("rail-scrim"),
            )
        }
        WideNavigationRail(
            state = state,
            modifier = Modifier
                .fillMaxHeight()
                .widthIn(max = maxWidth)
                .testTag("shell-rail")
                .pointerInput(Unit) {
                    awaitPointerEventScope {
                        while (true) {
                            val event = awaitPointerEvent()
                            if (event.changes.none { it.type == PointerType.Mouse }) continue
                            when (event.type) {
                                PointerEventType.Enter -> if (state.targetValue == WideNavigationRailValue.Collapsed) {
                                    byHover = true
                                    expand()
                                }
                                PointerEventType.Exit -> if (byHover) collapse()
                            }
                        }
                    }
                },
            header = {
                IconButton(onClick = { if (expanded) collapse() else expand() }, modifier = Modifier.size(48.dp).testTag("rail-toggle")) {
                    Icon(
                        if (expanded) Icons.AutoMirrored.Filled.MenuOpen else Icons.Filled.Menu,
                        contentDescription = if (expanded) collapseLabel else expandLabel,
                    )
                }
            },
        ) {
            sections.forEach { section ->
                val selected = section == current
                val name = stringResource(section.label)
                WideNavigationRailItem(
                    selected = selected,
                    onClick = {
                        onChoose(section)
                        collapse()
                    },
                    // collapsed: icons alone, each named for TalkBack
                    icon = { Icon(if (selected) section.selectedIcon else section.icon, contentDescription = if (expanded) null else name) },
                    label = if (expanded) ({ Text(name) }) else null,
                    railExpanded = expanded,
                    modifier = Modifier.testTag("section-${section.name}"),
                )
            }
        }
    }
    // registered as it opens, so after every screen's handler: the last registered is asked first
    if (show && expanded) BackHandler(onBack = collapse)
}

/**
 * The start page's width in px for a book [hinge] (upright, half open) in a frame at [bounds]
 * (window px): from the frame's start edge to the hinge. Null when there is no such hinge, or
 * it is not inside the frame.
 */
internal fun startPageWidth(hinge: HingeInfo?, bounds: Rect, rtl: Boolean): Float? {
    if (hinge == null || hinge.isFlat || !hinge.isVertical || bounds.width <= 0f) return null
    val h = hinge.bounds
    if (h.left < bounds.left || h.right > bounds.right) return null
    return if (rtl) bounds.right - h.right else h.left - bounds.left
}
