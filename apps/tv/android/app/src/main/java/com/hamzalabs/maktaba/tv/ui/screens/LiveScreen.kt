package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
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
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.itemsIndexed
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.tv.material3.Card
import androidx.tv.material3.CardDefaults
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.api.GuideAnswer
import com.hamzalabs.maktaba.core.api.LiveChannel
import com.hamzalabs.maktaba.core.api.NowAnswer
import com.hamzalabs.maktaba.core.api.NowEntry
import com.hamzalabs.maktaba.core.api.Programme
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.data.TvApi
import com.hamzalabs.maktaba.tv.ui.components.CardCorner
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton
import com.hamzalabs.maktaba.tv.ui.components.ReadingPanel
import com.hamzalabs.maktaba.tv.ui.components.goldFocusBorder
import com.hamzalabs.maktaba.tv.ui.components.maktabaCardColors
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import java.time.Instant

/** Live TV's two views: what is on now on every channel, and the guide. */
enum class LiveView { Now, Guide }

/** What the Live TV tab keeps while a channel plays (back from the player finds the same view). */
class LiveState {
    var view by mutableStateOf(LiveView.Now)
}

/**
 * The Live TV tab (FR-21, FR-22 on the TV): On now (a grid of channels with the programme on
 * air and its progress) and the Guide (channels down, time across, the now-line), both driven
 * by the D-pad alone. Select tunes a channel ([onTune]); a programme still to come opens its
 * sheet instead.
 */
@Composable
fun LiveScreen(api: TvApi, state: LiveState, focusFirst: Boolean, onTune: (String) -> Unit, signedOut: () -> Unit) {
    val content = remember { FocusRequester() }
    Column(Modifier.fillMaxSize()) {
        Row(
            Modifier.padding(start = Gutter, end = Gutter, top = 16.dp, bottom = 4.dp),
            horizontalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            LiveView.entries.forEach { v ->
                MaktabaButton(onClick = { state.view = v }, modifier = Modifier.testTag("live-view-${v.name}")) {
                    Text(
                        (if (v == state.view) "● " else "") +
                            stringResource(if (v == LiveView.Now) R.string.live_now else R.string.live_guide),
                    )
                }
            }
        }
        when (state.view) {
            LiveView.Now -> NowView(api, focusFirst, content, onTune, signedOut)
            LiveView.Guide -> GuideView(api, focusFirst, content, onTune, signedOut)
        }
    }
}

// ---------- On now ----------

@Composable
private fun NowView(api: TvApi, focusFirst: Boolean, first: FocusRequester, onTune: (String) -> Unit, signedOut: () -> Unit) {
    val (state, retry) = rememberLoad(api, signedOut, where = "tv live now") { Timed<NowAnswer>(api.live.now()) }
    LoadState(state, retry) { answer ->
        val now = rememberServerNow(answer)
        val channels = remember(answer) { answer.value.channels.sortedWith(compareBy({ it.channel.number }, { it.channel.name })) }
        if (channels.isEmpty()) {
            Centered { Text(stringResource(R.string.live_no_channels), style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text) }
            return@LoadState
        }
        LaunchedEffect(channels, focusFirst) { if (focusFirst) runCatching { first.requestFocus() } }
        LazyVerticalGrid(
            columns = GridCells.Fixed(3),
            modifier = Modifier.fillMaxSize().testTag("live-now"),
            contentPadding = PaddingValues(start = Gutter, end = Gutter, top = 16.dp, bottom = 48.dp),
            horizontalArrangement = Arrangement.spacedBy(24.dp),
            verticalArrangement = Arrangement.spacedBy(24.dp),
        ) {
            itemsIndexed(channels, key = { _, e -> e.channel.id }) { i, e ->
                NowCard(api, e, now, if (i == 0) Modifier.focusRequester(first) else Modifier) { onTune(e.channel.id) }
            }
        }
    }
}

@Composable
private fun NowCard(api: TvApi, e: NowEntry, nowMs: Long, modifier: Modifier, onClick: () -> Unit) {
    val time = rememberTimeFormat()
    // the block the home said was on, else (the answer has aged) the next one once it began
    val current = e.current?.takeIf { it.isAiring(nowMs) } ?: e.next?.takeIf { it.isAiring(nowMs) } ?: e.current
    val next = if (current === e.next) null else e.next
    Card(
        onClick = onClick,
        modifier = modifier.fillMaxWidth().height(168.dp).testTag("live-channel-${e.channel.id}"),
        shape = CardDefaults.shape(CardCorner),
        colors = maktabaCardColors(),
        scale = CardDefaults.scale(focusedScale = 1.05f),
        border = goldFocusBorder(),
    ) {
        Column(Modifier.fillMaxSize().padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                ChannelBadge(api.live, e.channel, size = 44.dp)
                Text(
                    "${e.channel.number}  ${e.channel.name}",
                    style = MaterialTheme.typography.titleMedium,
                    color = MaktabaPalette.Muted,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            Text(
                current?.title?.takeIf { it.isNotBlank() } ?: stringResource(R.string.live_no_content),
                style = MaterialTheme.typography.titleLarge,
                color = MaktabaPalette.Text,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            current?.progressAt(nowMs)?.let { LiveProgress(it) }
            Spacer(Modifier.weight(1f))
            if (next != null) {
                Text(
                    stringResource(R.string.live_next, next.startMs?.let(time)?.let { "$it  ${next.title}" } ?: next.title),
                    style = MaterialTheme.typography.labelLarge,
                    color = MaktabaPalette.Faint,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

// ---------- the guide ----------

/** How far ahead the guide reads. */
private const val GUIDE_AHEAD_MS = 6 * 60 * 60_000L

private val ChannelColumn = 220.dp
private val RowHeight = 84.dp

@Composable
private fun GuideView(api: TvApi, focusFirst: Boolean, focus: FocusRequester, onTune: (String) -> Unit, signedOut: () -> Unit) {
    val (state, retry) = rememberLoad(api, signedOut, where = "tv live guide") {
        // read from half an hour early (this TV's clock may be behind the home's); the window
        // then starts at the half hour now on the home's clock
        val local = floorHalfHour(System.currentTimeMillis()) - HALF_HOUR_MS
        val answer = Timed<GuideAnswer>(
            api.live.guide(Instant.ofEpochMilli(local), Instant.ofEpochMilli(local + GUIDE_AHEAD_MS + 2 * HALF_HOUR_MS)),
        )
        floorHalfHour(answer.value.serverNow(answer.atLocalMs)) to answer
    }
    LoadState(state, retry) { (from, answer) ->
        val rows = remember(answer) { guideRows(answer.value.channels, from, from + GUIDE_AHEAD_MS) }
        if (rows.isEmpty()) {
            Centered { Text(stringResource(R.string.live_no_channels), style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text) }
            return@LoadState
        }
        val now = rememberServerNow(answer)
        var sheet by remember { mutableStateOf<Pair<LiveChannel, Programme>?>(null) }
        var refocus by remember { mutableIntStateOf(0) }
        GuideGrid(api, rows, from, now, focusFirst || refocus > 0, focus, refocus, onTune) { ch, p -> sheet = ch to p }
        sheet?.let { (ch, p) ->
            ProgrammeSheet(
                ch,
                p,
                onWatch = { sheet = null; onTune(ch.id) },
                onClose = { sheet = null; refocus++ },
            )
        }
    }
}

@Composable
private fun GuideGrid(
    api: TvApi,
    rows: List<GuideChannel>,
    fromMs: Long,
    nowMs: Long,
    takeFocus: Boolean,
    focus: FocusRequester,
    refocus: Int,
    onTune: (String) -> Unit,
    onSheet: (LiveChannel, Programme) -> Unit,
) {
    val nav = remember(rows) { GuideNav(rows, fromMs) }
    var at by remember(rows) { mutableStateOf(nav.start(nowMs)) }
    var focused by remember { mutableStateOf(false) }
    val rtl = LocalLayoutDirection.current == LayoutDirection.Rtl
    val list = rememberLazyListState()
    val time = rememberTimeFormat()

    LaunchedEffect(rows, takeFocus, refocus) { if (takeFocus) runCatching { focus.requestFocus() } }
    // the focused channel stays the third row down while there are rows above it
    LaunchedEffect(at?.row) { at?.let { list.animateScrollToItem((it.row - 2).coerceAtLeast(0)) } }

    val f = at ?: return
    val viewStart = f.viewStartMs
    val viewEnd = viewStart + nav.spanMs

    Column(
        Modifier
            .fillMaxSize()
            .padding(start = Gutter, end = Gutter, top = 12.dp)
            .testTag("live-guide")
            .focusRequester(focus)
            .onFocusChanged { focused = it.isFocused }
            .onPreviewKeyEvent { e ->
                if (e.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                val key = when (e.key) {
                    Key.DirectionUp -> GuideKey.Up
                    Key.DirectionDown -> GuideKey.Down
                    Key.DirectionLeft -> GuideKey.Left
                    Key.DirectionRight -> GuideKey.Right
                    Key.DirectionCenter, Key.Enter, Key.NumPadEnter -> {
                        val cell = nav.cell(f) ?: return@onPreviewKeyEvent true
                        val p = cell.programme
                        val ch = rows[f.row].channel
                        // on air (or nothing scheduled): tune; still to come: its sheet
                        if (p == null || p.isAiring(nowMs) || (p.stopMs ?: 0) <= nowMs) onTune(ch.id) else onSheet(ch, p)
                        return@onPreviewKeyEvent true
                    }
                    else -> return@onPreviewKeyEvent false
                }
                val next = nav.move(f, key, rtl)
                if (next != null) at = next
                // ▲ from the first channel goes up to the tab bar; every other edge holds focus
                next != null || key != GuideKey.Up
            }
            .focusable(),
    ) {
        // the times across the top, every half hour of the view
        Row(Modifier.fillMaxWidth().height(36.dp)) {
            Spacer(Modifier.width(ChannelColumn))
            BoxWithConstraints(Modifier.weight(1f).fillMaxHeight()) {
                val w = maxWidth
                var t = viewStart
                while (t < viewEnd) {
                    val x = w * ((t - viewStart).toFloat() / nav.spanMs)
                    Text(
                        time(t),
                        style = MaterialTheme.typography.labelLarge,
                        color = MaktabaPalette.Muted,
                        modifier = Modifier.offset(x = x + 8.dp).align(Alignment.CenterStart),
                    )
                    t += HALF_HOUR_MS
                }
            }
        }
        LazyColumn(state = list, modifier = Modifier.fillMaxSize(), userScrollEnabled = false) {
            itemsIndexed(rows, key = { _, r -> r.channel.id }) { i, r ->
                GuideRowView(api, r, viewStart, viewEnd, nowMs, focusedCell = if (focused && i == f.row) f.cell else null, selected = i == f.row)
            }
        }
    }
}

@Composable
private fun GuideRowView(api: TvApi, row: GuideChannel, viewStart: Long, viewEnd: Long, nowMs: Long, focusedCell: Int?, selected: Boolean) {
    val span = (viewEnd - viewStart).toFloat()
    val time = rememberTimeFormat()
    Row(Modifier.fillMaxWidth().height(RowHeight).padding(vertical = 4.dp)) {
        Row(
            Modifier
                .width(ChannelColumn)
                .fillMaxHeight()
                .padding(end = 8.dp)
                .background(if (selected) MaktabaPalette.Raised else MaktabaPalette.Surface, CardCorner)
                .padding(horizontal = 12.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            ChannelBadge(api.live, row.channel, size = 48.dp)
            Column {
                Text(row.channel.number.toString(), style = MaterialTheme.typography.labelLarge, color = MaktabaPalette.Gold)
                Text(row.channel.name, style = MaterialTheme.typography.titleSmall, color = MaktabaPalette.Text, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
        BoxWithConstraints(Modifier.weight(1f).fillMaxHeight()) {
            val w = maxWidth
            row.cells.forEachIndexed { i, c ->
                if (c.stopMs <= viewStart || c.startMs >= viewEnd) return@forEachIndexed
                val s = maxOf(c.startMs, viewStart)
                val e = minOf(c.stopMs, viewEnd)
                val isFocused = i == focusedCell
                val airing = nowMs in c.startMs until c.stopMs
                Box(
                    Modifier
                        .offset(x = w * ((s - viewStart) / span))
                        .width(w * ((e - s) / span))
                        .fillMaxHeight()
                        .padding(end = 4.dp)
                        .background(
                            when {
                                isFocused -> MaktabaPalette.Gold
                                airing -> MaktabaPalette.Raised
                                else -> MaktabaPalette.Surface
                            },
                            RoundedCornerShape(8.dp),
                        )
                        .then(if (isFocused) Modifier.border(3.dp, MaktabaPalette.GoldLight, RoundedCornerShape(8.dp)) else Modifier)
                        .padding(horizontal = 10.dp, vertical = 6.dp)
                        .testTag(if (isFocused) "guide-focused" else "guide-cell"),
                ) {
                    Column {
                        Text(
                            c.programme?.title?.takeIf { it.isNotBlank() } ?: stringResource(R.string.live_no_content),
                            style = MaterialTheme.typography.titleSmall,
                            color = if (isFocused) MaktabaPalette.OnGold else MaktabaPalette.Text,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                        if (c.programme != null) {
                            Text(
                                time(c.programme.startMs ?: c.startMs) + " – " + time(c.programme.stopMs ?: c.stopMs),
                                style = MaterialTheme.typography.labelMedium,
                                color = if (isFocused) MaktabaPalette.OnGold else MaktabaPalette.Faint,
                                maxLines = 1,
                            )
                        }
                    }
                }
            }
            // the now-line, where the home's clock is
            if (nowMs in viewStart until viewEnd) {
                Box(
                    Modifier
                        .offset(x = w * ((nowMs - viewStart) / span))
                        .width(2.dp)
                        .fillMaxHeight()
                        .background(MaktabaPalette.Live)
                        .testTag("guide-now-line"),
                )
            }
        }
    }
}

/** A programme still to come: its time, length and words, and the channel to watch now. */
@Composable
private fun ProgrammeSheet(channel: LiveChannel, p: Programme, onWatch: () -> Unit, onClose: () -> Unit) {
    val time = rememberTimeFormat()
    val watch = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { watch.requestFocus() } }
    Dialog(onDismissRequest = onClose) {
        ReadingPanel(Modifier.widthIn(max = 640.dp).testTag("programme-sheet"), contentPadding = 32.dp) {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text(p.title, style = MaterialTheme.typography.headlineSmall, color = MaktabaPalette.Text)
                p.episodeLabel?.let { Text(it, style = MaterialTheme.typography.titleMedium, color = MaktabaPalette.Muted) }
                val start = p.startMs
                val stop = p.stopMs
                if (start != null && stop != null) {
                    Text(
                        stringResource(R.string.live_starts_at, time(start)) + " · " +
                            stringResource(R.string.live_minutes, ((stop - start) / 60_000).toInt()) + " · " +
                            "${channel.number} ${channel.name}",
                        style = MaterialTheme.typography.titleSmall,
                        color = MaktabaPalette.Gold,
                    )
                }
                p.desc?.takeIf { it.isNotBlank() }?.let {
                    Text(it, style = MaterialTheme.typography.bodyLarge, color = MaktabaPalette.Muted, maxLines = 6, overflow = TextOverflow.Ellipsis)
                }
                Row(Modifier.padding(top = 8.dp), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                    MaktabaButton(onClick = onWatch, modifier = Modifier.focusRequester(watch).testTag("sheet-watch")) {
                        Text(stringResource(R.string.live_watch_channel, channel.number, channel.name))
                    }
                    MaktabaButton(onClick = onClose, modifier = Modifier.testTag("sheet-close")) {
                        Text(stringResource(R.string.live_close))
                    }
                }
            }
        }
    }
}
