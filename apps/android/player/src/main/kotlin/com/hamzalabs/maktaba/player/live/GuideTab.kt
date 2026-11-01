package com.hamzalabs.maktaba.player.live

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.ScrollState
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowLeft
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextDirection
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.api.GuideRow
import com.hamzalabs.maktaba.core.api.LiveChannel
import com.hamzalabs.maktaba.core.api.Programme
import com.hamzalabs.maktaba.core.ui.ErrorState
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.ui.components.ChannelLogo
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.format.DateTimeFormatter

/** The guide's scale: a minute is this wide (an hour is 240 dp). */
val GuideDpPerMinute: Dp = 4.dp

/** The channels' column at the guide's start edge. */
internal val GuideChannelColumn = 96.dp
private val GuideRowHeight = 72.dp

/**
 * The Guide (FR-22): channels down, time across (three hours on a phone, six on a tablet), with
 * the now-line, scrolling both ways; a day picker (today and the six days after, at the same time
 * of day) and Earlier / Later. A programme opens its sheet: title, time and description, with
 * Watch for what is on now. Times run from the start edge, so from the right in Arabic.
 */
@OptIn(ExperimentalFoundationApi::class, ExperimentalMaterial3Api::class)
@Composable
internal fun GuideTab(live: LiveTv, nowMs: Long, wide: Boolean, onWatch: (String) -> Unit) {
    val env = LocalLiveEnv.current
    val zone = env.zone()
    val hours = GuideWindow.hours(wide)
    val day = live.guideDay
    val shift = live.guideShift
    val base = remember { live.serverNow(env.nowMs()) }
    val window = remember(day, shift, hours, zone) { GuideWindow.around(base, zone, hours, day).shifted(shift.toLong() * hours) }
    var sheet by remember { mutableStateOf<Pair<LiveChannel, Programme>?>(null) }
    val scope = rememberCoroutineScope()

    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(window, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                live.loadGuide(window, env.nowMs())
                delay(env.refreshMs)
            }
        }
    }

    Column(Modifier.fillMaxSize().testTag("guide")) {
        DayPicker(day, base, onDay = { live.guideDay = it; live.guideShift = 0 }, onEarlier = { live.guideShift-- }, onLater = { live.guideShift++ })
        val guide = live.guide?.takeIf { live.guideWindow == window }
        val problem = live.guideProblem
        when {
            guide == null && problem != null -> ErrorState(
                Failure.of(problem),
                title = stringResource(R.string.live_cant_load),
                onRetry = { scope.launch { live.loadGuide(window, env.nowMs()) } },
            )
            guide == null -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
            else -> GuideGrid(live, window, guide.channels.sortedBy { it.channel.number }, nowMs, onOpen = { c, p -> sheet = c to p })
        }
    }

    sheet?.let { (channel, p) ->
        ModalBottomSheet(onDismissRequest = { sheet = null }, modifier = Modifier.testTag("programme-sheet")) {
            ProgrammeSheet(channel, p, airing = p.isAiring(nowMs), onWatch = { sheet = null; onWatch(channel.id) })
        }
    }
}

@Composable
private fun DayPicker(day: Int, base: Long, onDay: (Int) -> Unit, onEarlier: () -> Unit, onLater: () -> Unit) {
    val zone = LocalLiveEnv.current.zone()
    val locale = LocalConfiguration.current.locales[0]
    val dayName = remember(zone, locale) { DateTimeFormatter.ofPattern("EEE d", locale).withZone(zone) }
    Row(Modifier.fillMaxWidth().padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        IconButton(onClick = onEarlier, modifier = Modifier.testTag("guide-earlier")) {
            Icon(Icons.AutoMirrored.Filled.KeyboardArrowLeft, stringResource(R.string.live_earlier))
        }
        LazyRow(Modifier.weight(1f), horizontalArrangement = Arrangement.spacedBy(8.dp), contentPadding = PaddingValues(horizontal = 4.dp)) {
            items((0 until DAYS).toList()) { d ->
                val label = when (d) {
                    0 -> stringResource(R.string.live_today)
                    1 -> stringResource(R.string.live_tomorrow)
                    else -> dayName.format(Instant.ofEpochMilli(base).plusSeconds(d * 86_400L))
                }
                FilterChip(selected = d == day, onClick = { onDay(d) }, label = { Text(label) }, modifier = Modifier.testTag("day-$d"))
            }
        }
        IconButton(onClick = onLater, modifier = Modifier.testTag("guide-later")) {
            Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, stringResource(R.string.live_later))
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun GuideGrid(live: LiveTv, window: GuideWindow, rows: List<GuideRow>, nowMs: Long, onOpen: (LiveChannel, Programme) -> Unit) {
    // one horizontal position for the ruler and every row: they scroll together
    val across = rememberScrollState()
    val nowAt = window.minutesTo(nowMs).takeIf { it >= 0 && it < window.minutes }
    LazyColumn(Modifier.fillMaxSize().testTag("guide-rows")) {
        stickyHeader {
            Row(Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surface).height(32.dp)) {
                Spacer(Modifier.width(GuideChannelColumn))
                Track(window, across, Modifier.testTag("guide-track")) {
                    TimeRuler(window)
                    if (nowAt != null) NowLine(nowAt, Modifier.testTag("now-line"), head = true)
                }
            }
            HorizontalDivider()
        }
        items(rows, key = { it.channel.id }) { row ->
            Row(Modifier.fillMaxWidth().height(GuideRowHeight).testTag("guide-row-${row.channel.id}")) {
                Box(Modifier.width(GuideChannelColumn).fillMaxHeight(), contentAlignment = Alignment.Center) {
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        if (row.channel.number > 0) {
                            Text("${row.channel.number}", style = MaterialTheme.typography.labelMedium, color = MaktabaTheme.brand.accentText)
                        }
                        ChannelLogo(row.channel, live.api, size = 44.dp)
                    }
                }
                Track(window, across) {
                    row.blocks.filter { !it.isFiller }.forEach { p -> Block(window, p, nowMs) { onOpen(row.channel, p) } }
                    if (nowAt != null) NowLine(nowAt)
                }
            }
            HorizontalDivider()
        }
    }
}

/** The time across: [window]'s width, scrolled by [across]. */
@Composable
private fun Track(window: GuideWindow, across: ScrollState, modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    Box(Modifier.fillMaxHeight().horizontalScroll(across)) {
        Box(modifier.width(GuideDpPerMinute * window.minutes.toFloat()).fillMaxHeight()) { content() }
    }
}

@Composable
private fun TimeRuler(window: GuideWindow) {
    val time = rememberTimeFormat()
    var m = 0L
    while (m < window.minutes) {
        Text(
            time(window.startMs + m * 60_000),
            Modifier.offset(x = GuideDpPerMinute * m.toFloat()).padding(start = 6.dp, top = 6.dp),
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        m += 30
    }
}

/** The now-line at [minutes] from the window's start (its head, a dot, on the ruler). */
@Composable
private fun NowLine(minutes: Float, modifier: Modifier = Modifier, head: Boolean = false) {
    val brass = MaterialTheme.colorScheme.primary
    Box(Modifier.offset(x = GuideDpPerMinute * minutes - 1.dp).then(modifier).width(2.dp).fillMaxHeight().background(brass))
    if (head) Box(Modifier.offset(x = GuideDpPerMinute * minutes - 4.dp, y = 24.dp).size(8.dp).clip(CircleShape).background(brass))
}

@Composable
private fun Block(window: GuideWindow, p: Programme, nowMs: Long, onClick: () -> Unit) {
    val s = p.startMs ?: return
    val e = p.stopMs ?: return
    val from = window.minutesTo(s).coerceAtLeast(0f)
    val to = window.minutesTo(e).coerceAtMost(window.minutes.toFloat())
    if (to <= from) return
    val airing = p.isAiring(nowMs)
    val range = timeRange(p)
    Box(
        Modifier
            .offset(x = GuideDpPerMinute * from)
            .width(GuideDpPerMinute * (to - from))
            .fillMaxHeight()
            .padding(2.dp)
            .clip(GuideCellShape)
            .background(if (airing) MaktabaTheme.brand.accentSoft else MaktabaTheme.brand.raised)
            .clickable(onClick = onClick)
            .semantics { contentDescription = "${p.title}, $range" }
            .testTag("programme-${p.channelId}-${p.start}"),
    ) {
        Column(Modifier.padding(horizontal = 8.dp, vertical = 6.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(p.title, style = MaterialTheme.typography.labelLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
            // a span of times reads left to right in Arabic too
            Text(range, style = MaterialTheme.typography.labelSmall.copy(textDirection = TextDirection.Ltr), color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
        }
    }
}

@Composable
private fun ProgrammeSheet(channel: LiveChannel, p: Programme, airing: Boolean, onWatch: () -> Unit) {
    Column(Modifier.fillMaxWidth().padding(horizontal = 24.dp).padding(bottom = 32.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        if (airing) Text(stringResource(R.string.live_on_now), style = MaterialTheme.typography.labelLarge, color = MaktabaTheme.brand.accentText)
        Text(p.title, style = MaterialTheme.typography.titleLarge)
        p.episodeLabel?.let { Text(it, style = MaterialTheme.typography.bodyMedium) }
        Text(
            "${channel.name} · ${timeRange(p)}",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        p.desc?.takeIf { it.isNotBlank() }?.let { Text(it, style = MaterialTheme.typography.bodyMedium) }
        if (airing) {
            Button(onClick = onWatch, modifier = Modifier.padding(top = 8.dp).testTag("programme-watch")) { Text(stringResource(R.string.live_watch)) }
        }
    }
}

/** Today and the six days after. */
private const val DAYS = 7
