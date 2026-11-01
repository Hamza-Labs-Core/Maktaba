package com.hamzalabs.maktaba.player.ui.components

import android.provider.Settings
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.MaktabaTokens
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.player.R
import kotlinx.coroutines.delay

/**
 * How long a read may take before its placeholder shows: most loads are done by then, so a quick
 * one never flashes one. The web's rule (`web/src/lib/useDelayedFlag.ts`, PLACEHOLDER_DELAY_MS).
 */
const val PLACEHOLDER_DELAY_MS = 400L

/** True once [loading] has stayed true for [delayMs]; false again as soon as it is not (the web's `useDelayedFlag`). */
@Composable
fun rememberPlaceholderShown(loading: Boolean, delayMs: Long = PLACEHOLDER_DELAY_MS): Boolean {
    var late by remember { mutableStateOf(false) }
    LaunchedEffect(loading, delayMs) {
        late = false
        if (loading) {
            delay(delayMs)
            late = true
        }
    }
    return loading && late
}

/**
 * A loading region (the web's `Loading`): says "Loading…" to TalkBack from the start, and shows
 * its [placeholder] only after [PLACEHOLDER_DELAY_MS]. Nothing while not [loading].
 */
@Composable
fun Loading(loading: Boolean, modifier: Modifier = Modifier, placeholder: @Composable () -> Unit) {
    if (!loading) return
    val late = rememberPlaceholderShown(loading)
    val label = stringResource(R.string.components_loading)
    Box(modifier.testTag("loading").semantics { contentDescription = label }) {
        if (late) Box(Modifier.testTag("placeholder").clearAndSetSemantics { }) { placeholder() }
    }
}

/** Whether the person turned animations off (Settings → Accessibility → Remove animations): NFR-9. */
@Composable
fun animationsOff(): Boolean {
    val context = LocalContext.current
    return remember(context) {
        runCatching { Settings.Global.getFloat(context.contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, 1f) == 0f }.getOrDefault(false)
    }
}

/**
 * A placeholder block in the shape of what loads: the quiet surface, with a soft light passing
 * across it every 1.2 s (start to end, so right to left in Arabic). Still when [animate] is false
 * or the person removed animations (NFR-9).
 */
@Composable
fun Shimmer(modifier: Modifier = Modifier, shape: Shape = RoundedCornerShape(MaktabaTokens.Radius.md), animate: Boolean = !animationsOff()) {
    val base = MaterialTheme.colorScheme.surfaceContainerHigh
    val light = lerp(base, MaterialTheme.colorScheme.outlineVariant, 0.7f)
    if (!animate) {
        Box(modifier.clip(shape).background(base))
        return
    }
    val t by rememberInfiniteTransition(label = "shimmer").animateFloat(
        initialValue = -1f,
        targetValue = 2f,
        animationSpec = infiniteRepeatable(tween(1200, easing = LinearEasing), RepeatMode.Restart),
        label = "shimmer-x",
    )
    BoxWithConstraints(modifier.clip(shape)) {
        val w = constraints.maxWidth.toFloat().coerceAtLeast(1f)
        val rtl = LocalLayoutDirection.current == LayoutDirection.Rtl
        val x = if (rtl) w - t * w else t * w
        Box(
            Modifier.matchParentSize().background(
                Brush.linearGradient(listOf(base, light, base), start = Offset(x - w / 2, 0f), end = Offset(x + w / 2, 0f)),
            ),
        )
    }
}

/** A rail's placeholder: a title line and [cards] poster-shaped blocks (as the web's `RowPlaceholder`). */
@Composable
fun RailPlaceholder(modifier: Modifier = Modifier, cards: Int = 4, cardWidth: Dp = PosterCardWidth, animate: Boolean = !animationsOff()) {
    Column(modifier.fillMaxWidth().padding(vertical = MaktabaTokens.Spacing.s2), verticalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s3)) {
        Shimmer(Modifier.padding(horizontal = MaktabaTokens.Spacing.s4).width(140.dp).height(20.dp), animate = animate)
        // as wide as its cards: the window cuts the last one, as a rail does
        Row(
            Modifier.horizontalScroll(rememberScrollState(), enabled = false).padding(horizontal = MaktabaTokens.Spacing.s4),
            horizontalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s3),
        ) {
            repeat(cards) {
                Column(verticalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s2)) {
                    Shimmer(Modifier.width(cardWidth).aspectRatio(POSTER_ASPECT), animate = animate)
                    Shimmer(Modifier.width(cardWidth * 0.8f).height(14.dp), animate = animate)
                }
            }
        }
    }
}

/** The hero's placeholder: a backdrop-shaped block with the title and buttons' lines (the web's `HomePlaceholder`). */
@Composable
fun HeroPlaceholder(modifier: Modifier = Modifier, maxBackdropHeight: Dp = 420.dp, animate: Boolean = !animationsOff()) {
    BoxWithConstraints(modifier.fillMaxWidth()) {
        val h = (maxWidth / POSTER_ASPECT).coerceAtMost(maxBackdropHeight)
        Column(
            Modifier.fillMaxWidth().height(h).padding(MaktabaTokens.Spacing.s4),
            verticalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s3, Alignment.Bottom),
        ) {
            Shimmer(Modifier.width(110.dp).height(14.dp), animate = animate)
            Shimmer(Modifier.width(260.dp).height(30.dp), animate = animate)
            Row(horizontalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s3)) {
                Shimmer(Modifier.width(132.dp).height(48.dp), RoundedCornerShape(MaktabaTokens.Radius.full), animate)
                Shimmer(Modifier.width(112.dp).height(48.dp), RoundedCornerShape(MaktabaTokens.Radius.full), animate)
            }
        }
    }
}

@Preview(name = "Shimmer", backgroundColor = 0xFF0B111C, showBackground = true, widthDp = 400)
@Composable
private fun ShimmerPreview() = MaktabaTheme(ThemeMode.Dark) {
    Column { HeroPlaceholder(animate = false); RailPlaceholder(cards = 2, animate = false) }
}
