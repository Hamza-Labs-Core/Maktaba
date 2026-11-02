package com.hamzalabs.maktaba.core.layout

import android.content.Context
import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.gestures.Orientation
import androidx.compose.foundation.gestures.draggable
import androidx.compose.foundation.gestures.rememberDraggableState
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsDraggedAsState
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.interaction.collectIsHoveredAsState
import androidx.compose.foundation.hoverable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.VerticalDivider
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.CustomAccessibilityAction
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.customActions
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.R
import kotlin.math.roundToInt

/** The list pane is never narrower than this beside a page (one column of cards). */
val ListPaneMinWidth = 240.dp

/** The page beside a list is never narrower than this. */
val DetailPaneMinWidth = 320.dp

/** How far one widen or narrow (TalkBack's action, an arrow key) moves the divider. */
val SplitStep = 48.dp

/** The divider's touch target: Material's minimum, across and along. */
val SplitHandleTarget = 48.dp

/**
 * Where a list-detail split may stand in a [total] wide row (dp, divider included): the list's
 * least and most width. When the row is too narrow for both minimums, the two share it in
 * their minimums' proportion and the divider has nowhere to go (least == most).
 */
fun splitRange(total: Float, listMin: Float = ListPaneMinWidth.value, detailMin: Float = DetailPaneMinWidth.value): ClosedFloatingPointRange<Float> {
    if (total <= 0f) return 0f..0f
    if (total < listMin + detailMin) {
        val share = total * listMin / (listMin + detailMin)
        return share..share
    }
    return listMin..(total - detailMin)
}

/**
 * The list's width in a [total] wide row (dp): the [chosen] share of it (null: none chosen
 * yet, then [preferred] but never more than 40 % of the row, so the page keeps the room), always
 * inside [splitRange].
 */
fun splitListWidth(total: Float, chosen: Float?, preferred: Float = ListPaneWidth.value): Float {
    val range = splitRange(total)
    val want = if (chosen != null && !chosen.isNaN()) chosen * total else minOf(preferred, total * 0.4f)
    return want.coerceIn(range.start, range.endInclusive)
}

/** The chosen splits, kept across launches: a share of the row per screen [key]. */
private fun splits(context: Context) = context.getSharedPreferences("maktaba.splits", Context.MODE_PRIVATE)

/**
 * A list beside a page (Panes.ListDetail) with a divider that can be dragged to give either
 * more room, inside [ListPaneMinWidth] and [DetailPaneMinWidth]. The divider has a visible
 * handle with a 48 dp target; TalkBack offers "Widen the list" and "Narrow the list", and the
 * arrow keys move it when it has focus. Right to left the list is on the right and dragging is
 * mirrored. The chosen split is kept per [key] (saved state, and across launches).
 *
 * Half folded the split is the hinge instead ([HingeSplit]): any other place puts a pane under it.
 */
@Composable
fun ListDetailSplit(
    key: String,
    modifier: Modifier = Modifier,
    preferredList: Dp = ListPaneWidth,
    list: @Composable () -> Unit,
    detail: @Composable () -> Unit,
) {
    val context = LocalContext.current
    val density = LocalDensity.current
    val rtl = LocalLayoutDirection.current == LayoutDirection.Rtl
    // NaN: nothing chosen yet (the default share)
    var share by rememberSaveable(key) { mutableFloatStateOf(splits(context).getFloat(key, Float.NaN)) }
    val save = { splits(context).edit().putFloat(key, share).apply() }

    BoxWithConstraints(modifier.fillMaxSize()) {
        val total = maxWidth.value
        val range = splitRange(total)
        val listWidth = splitListWidth(total, share, preferredList.value)
        val movable = range.endInclusive - range.start >= 1f
        // from the state, not this composition's width: several drags can land in one frame
        val nudge = { dp: Float ->
            share = (splitListWidth(total, share, preferredList.value) + dp).coerceIn(range.start, range.endInclusive) / total
        }
        val moveBy = { dp: Float -> nudge(dp); save() }

        Row(Modifier.fillMaxSize()) {
            Box(Modifier.width(listWidth.dp).fillMaxHeight().testTag("split-list")) { list() }
            VerticalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            Box(Modifier.weight(1f).fillMaxHeight().testTag("split-detail")) { detail() }
        }

        if (movable) {
            val interaction = remember { MutableInteractionSource() }
            val dragged by interaction.collectIsDraggedAsState()
            val hovered by interaction.collectIsHoveredAsState()
            val focused by interaction.collectIsFocusedAsState()
            val active = dragged || hovered || focused
            val widen = stringResource(R.string.split_widen_list)
            val narrow = stringResource(R.string.split_narrow_list)
            val label = stringResource(R.string.split_divider)
            val state = stringResource(R.string.split_list_share, (listWidth / total * 100).roundToInt())
            val step = SplitStep.value
            Box(
                Modifier
                    .align(Alignment.CenterStart)
                    // centred on the divider (offset is mirrored right to left, as the Row is)
                    .offset(x = listWidth.dp + 0.5.dp - SplitHandleTarget / 2)
                    .size(SplitHandleTarget, SplitHandleTarget * 2)
                    .testTag("split-handle")
                    .hoverable(interaction)
                    .draggable(
                        rememberDraggableState { px -> nudge(with(density) { px.toDp().value }) },
                        Orientation.Horizontal,
                        interactionSource = interaction,
                        // right to left the list is on the right: dragging left widens it
                        reverseDirection = rtl,
                        onDragStopped = { save() },
                    )
                    .onKeyEvent { e ->
                        if (e.type != KeyEventType.KeyDown) return@onKeyEvent false
                        val towardsDetail = if (rtl) Key.DirectionLeft else Key.DirectionRight
                        val towardsList = if (rtl) Key.DirectionRight else Key.DirectionLeft
                        when (e.key) {
                            towardsDetail -> { moveBy(step); true }
                            towardsList -> { moveBy(-step); true }
                            else -> false
                        }
                    }
                    .focusable(interactionSource = interaction)
                    .semantics {
                        contentDescription = label
                        stateDescription = state
                        customActions = listOf(
                            CustomAccessibilityAction(widen) { moveBy(step); true },
                            CustomAccessibilityAction(narrow) { moveBy(-step); true },
                        )
                    },
                contentAlignment = Alignment.Center,
            ) {
                Box(
                    Modifier
                        .size(if (active) 8.dp else 4.dp, 48.dp)
                        .background(
                            if (active) MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.outline,
                            RoundedCornerShape(50),
                        ),
                )
            }
        }
    }
}
