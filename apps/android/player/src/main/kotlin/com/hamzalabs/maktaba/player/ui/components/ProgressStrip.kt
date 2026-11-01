package com.hamzalabs.maktaba.player.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.ProgressBarRangeInfo
import androidx.compose.ui.semantics.progressBarRangeInfo
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.MaktabaTokens
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.player.R

/**
 * How far into a video the person is: a brass bar on a quiet track, as the web's card shows it.
 * The bar starts at the layout's start, so it fills from the right in Arabic (NFR-11). It reads
 * as a progress bar with "40% watched" to TalkBack; a test finds it by [progressBarRangeInfo].
 */
@Composable
fun ProgressStrip(
    fraction: Float,
    modifier: Modifier = Modifier,
    height: Dp = 4.dp,
    track: Color = MaterialTheme.colorScheme.outlineVariant,
    fill: Color = MaterialTheme.colorScheme.primary,
) {
    val f = fraction.coerceIn(0f, 1f)
    val label = stringResource(R.string.components_watched_pct, watchedPercent(f))
    Box(
        modifier
            .fillMaxWidth()
            .height(height)
            .clip(RoundedCornerShape(MaktabaTokens.Radius.full))
            .background(track)
            .semantics {
                progressBarRangeInfo = ProgressBarRangeInfo(f, 0f..1f)
                stateDescription = label
            },
    ) {
        Box(Modifier.fillMaxHeight().fillMaxWidth(f).background(fill))
    }
}

/** A fraction as the whole percent a person reads: 0.004 is 0 %, 0.996 is 99 % (100 % is "watched"). */
fun watchedPercent(fraction: Float): Int = when {
    fraction >= 1f -> 100
    else -> (fraction.coerceAtLeast(0f) * 100).toInt().coerceAtMost(99)
}

@Preview(name = "ProgressStrip", backgroundColor = 0xFF0B111C, showBackground = true)
@Composable
private fun ProgressStripPreview() = MaktabaTheme(ThemeMode.Dark) {
    Box(Modifier.width(200.dp).padding(16.dp)) { ProgressStrip(0.4f) }
}
