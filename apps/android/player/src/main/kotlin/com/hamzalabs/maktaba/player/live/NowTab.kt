package com.hamzalabs.maktaba.player.live

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.ProgressBarRangeInfo
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.progressBarRangeInfo
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.NowEntry
import com.hamzalabs.maktaba.core.api.Programme
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.MaktabaTokens
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.ui.components.ChannelLogo
import com.hamzalabs.maktaba.player.ui.components.ProgressStrip
import com.hamzalabs.maktaba.player.ui.components.watchedPercent
import java.time.Instant
import java.time.format.DateTimeFormatter

/** Now (FR-21): each channel's logo, number, name, what is on with its progress, and what is next. */
@Composable
internal fun NowTab(live: LiveTv, channels: List<NowEntry>, nowMs: Long, wide: Boolean, onOpen: (String) -> Unit) {
    val sorted = remember(channels) { channels.sortedBy { it.channel.number } }
    if (wide) {
        LazyVerticalGrid(
            GridCells.Adaptive(340.dp),
            Modifier.fillMaxSize().testTag("now-grid"),
            contentPadding = PaddingValues(16.dp),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            items(sorted, key = { it.channel.id }) { e ->
                Surface(shape = MaterialTheme.shapes.medium, color = MaktabaTheme.brand.raised) {
                    ChannelNow(live, e, nowMs, onOpen)
                }
            }
        }
    } else {
        LazyColumn(Modifier.fillMaxSize().testTag("now-list")) {
            items(sorted, key = { it.channel.id }) { e ->
                ChannelNow(live, e, nowMs, onOpen)
                HorizontalDivider(Modifier.padding(start = 84.dp))
            }
        }
    }
}

@Composable
private fun ChannelNow(live: LiveTv, e: NowEntry, nowMs: Long, onOpen: (String) -> Unit) {
    val time = rememberTimeFormat()
    val current = e.current?.takeIf { !it.isFiller || it.title.isNotBlank() }
    Row(
        Modifier
            .fillMaxWidth()
            .clickable { onOpen(e.channel.id) }
            .padding(horizontal = 16.dp, vertical = 12.dp)
            .testTag("channel-${e.channel.id}"),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        ChannelLogo(e.channel, live.api, size = 56.dp)
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                if (e.channel.number > 0) {
                    Text(
                        "${e.channel.number}",
                        style = MaterialTheme.typography.labelLarge,
                        fontWeight = FontWeight.SemiBold,
                        color = MaktabaTheme.brand.accentText,
                    )
                }
                Text(e.channel.name, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            Text(
                current?.title?.ifBlank { null } ?: stringResource(R.string.live_nothing_on),
                style = MaterialTheme.typography.bodyMedium,
                color = if (current != null) MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            current?.let { p ->
                val f = p.progressAt(nowMs) ?: p.progress.takeIf { p.isLive }
                if (f != null) ProgrammeProgress(f.toFloat(), Modifier.padding(vertical = 2.dp))
            }
            e.next?.let { n ->
                Text(
                    stringResource(R.string.live_next, n.startMs?.let(time) ?: "", n.title),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

/**
 * How far through its programme a channel is: the components' strip, read by TalkBack as
 * "40% through" (not "watched": nobody watched it). A test finds it by its range.
 */
@Composable
internal fun ProgrammeProgress(fraction: Float, modifier: Modifier = Modifier) {
    val f = fraction.coerceIn(0f, 1f)
    val label = stringResource(R.string.live_progress, watchedPercent(f))
    Box(
        modifier.clearAndSetSemantics {
            progressBarRangeInfo = ProgressBarRangeInfo(f, 0f..1f)
            stateDescription = label
        },
    ) { ProgressStrip(f, height = 3.dp) }
}

/** This device's time format for the guide's times, in the shown zone ([LiveEnv.zone]). */
@Composable
internal fun rememberTimeFormat(): (Long) -> String {
    val zone = LocalLiveEnv.current.zone()
    val locale = LocalConfiguration.current.locales[0]
    return remember(zone, locale) {
        val f = DateTimeFormatter.ofPattern("HH:mm", locale).withZone(zone)
        val format: (Long) -> String = { ms -> f.format(Instant.ofEpochMilli(ms)) }
        format
    }
}

/** "21:00 – 22:00". */
@Composable
internal fun timeRange(p: Programme): String {
    val time = rememberTimeFormat()
    return stringResource(R.string.live_time_range, p.startMs?.let(time) ?: "", p.stopMs?.let(time) ?: "")
}

internal val GuideCellShape get() = androidx.compose.foundation.shape.RoundedCornerShape(MaktabaTokens.Radius.md)
