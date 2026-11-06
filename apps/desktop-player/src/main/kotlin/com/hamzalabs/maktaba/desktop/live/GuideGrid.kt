package com.hamzalabs.maktaba.desktop.live

import androidx.compose.foundation.ScrollState
import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextDirection
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.GuideRow
import com.hamzalabs.maktaba.core.api.LiveApi
import com.hamzalabs.maktaba.core.api.Programme
import com.hamzalabs.maktaba.core.live.GuideWindow
import com.hamzalabs.maktaba.desktop.choosable
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/** The guide's scale: a minute is this wide (an hour is 240 dp). */
val GuideDpPerMinute: Dp = 4.dp
private val ChannelColumn = 180.dp
private val RowHeight = 64.dp

private val clock = DateTimeFormatter.ofPattern("HH:mm")

/** "18:00–18:40", read left to right in either language. */
fun timeRange(p: Programme, zone: ZoneId = ZoneId.systemDefault()): String {
    val s = p.startMs?.let { clock.withZone(zone).format(Instant.ofEpochMilli(it)) } ?: ""
    val e = p.stopMs?.let { clock.withZone(zone).format(Instant.ofEpochMilli(it)) } ?: ""
    return "⁦$s–$e⁩"
}

/**
 * The guide (story 55.07): channels down the start side, time across (from the right in
 * Arabic: the layout direction runs it), blocks sized by length and cut to [window] by `:client`'s
 * [GuideWindow], and the now line. The ruler and every row scroll together; the wheel scrolls
 * the rows, Shift+wheel the time.
 */
@Composable
fun GuideGrid(window: GuideWindow, rows: List<GuideRow>, nowMs: Long, api: LiveApi, onChannel: (String) -> Unit) {
    val across = rememberScrollState()
    val nowAt = window.nowLine(nowMs)
    LazyColumn(Modifier.fillMaxSize().testTag("guide-rows")) {
        item {
            Row(Modifier.fillMaxWidth().height(28.dp)) {
                Box(Modifier.width(ChannelColumn))
                Track(window, across) {
                    var m = 0L
                    while (m < window.minutes) {
                        Text(
                            "⁦" + clock.withZone(ZoneId.systemDefault()).format(Instant.ofEpochMilli(window.startMs + m * 60_000)) + "⁩",
                            Modifier.offset(x = GuideDpPerMinute * m.toFloat()).padding(start = 6.dp, top = 4.dp),
                            style = MaterialTheme.typography.labelMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        m += 30
                    }
                    if (nowAt != null) NowLine(nowAt, Modifier.testTag("now-line"))
                }
            }
            HorizontalDivider()
        }
        items(rows, key = { it.channel.id }) { row ->
            Row(Modifier.fillMaxWidth().height(RowHeight).testTag("guide-row-${row.channel.id}")) {
                Row(
                    Modifier.width(ChannelColumn).fillMaxHeight().choosable({ onChannel(row.channel.id) }).padding(horizontal = 8.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    ChannelLogo(row.channel, api, 40.dp)
                    Text(row.channel.name, style = MaterialTheme.typography.labelLarge, maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
                Track(window, across) {
                    for (p in row.blocks) {
                        if (p.isFiller) continue
                        val (from, to) = window.cut(p) ?: continue
                        val airing = p.isAiring(nowMs)
                        Box(
                            Modifier.offset(x = GuideDpPerMinute * from).width(GuideDpPerMinute * (to - from)).fillMaxHeight().padding(2.dp)
                                .clip(RoundedCornerShape(6.dp))
                                .background(if (airing) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surfaceContainerHigh)
                                .choosable({ onChannel(row.channel.id) }, RoundedCornerShape(6.dp))
                                .testTag("programme-${p.channelId}-${p.start}"),
                        ) {
                            Column(Modifier.padding(horizontal = 8.dp, vertical = 4.dp)) {
                                Text(p.title, style = MaterialTheme.typography.labelLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                Text(timeRange(p), style = MaterialTheme.typography.labelSmall.copy(textDirection = TextDirection.Ltr), color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
                            }
                        }
                    }
                    if (nowAt != null) NowLine(nowAt)
                }
            }
            HorizontalDivider()
        }
    }
}

@Composable
private fun Track(window: GuideWindow, across: ScrollState, content: @Composable () -> Unit) {
    Box(Modifier.fillMaxHeight().horizontalScroll(across)) {
        Box(Modifier.width(GuideDpPerMinute * window.minutes.toFloat()).fillMaxHeight()) { content() }
    }
}

@Composable
private fun NowLine(minutes: Float, modifier: Modifier = Modifier) {
    Box(Modifier.offset(x = GuideDpPerMinute * minutes - 1.dp).then(modifier).width(2.dp).fillMaxHeight().background(MaterialTheme.colorScheme.primary))
}
