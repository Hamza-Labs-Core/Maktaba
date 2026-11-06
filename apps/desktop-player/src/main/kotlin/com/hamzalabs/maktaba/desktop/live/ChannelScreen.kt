package com.hamzalabs.maktaba.desktop.live

import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.KeyboardArrowUp
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.ChannelGuide
import com.hamzalabs.maktaba.core.api.Feature
import com.hamzalabs.maktaba.core.api.LiveSource
import com.hamzalabs.maktaba.core.api.LiveStream
import com.hamzalabs.maktaba.desktop.LocalTexts
import com.hamzalabs.maktaba.desktop.Playback
import com.hamzalabs.maktaba.desktop.StreamProxy
import com.hamzalabs.maktaba.desktop.VideoSurface
import com.hamzalabs.maktaba.desktop.deviceLabel
import com.hamzalabs.maktaba.desktop.shell.LocalDesktopHome
import com.hamzalabs.maktaba.desktop.shell.featureUnavailableSentence
import javafx.application.Platform
import javafx.scene.media.Media
import javafx.scene.media.MediaPlayer
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.ZoneId
import java.time.temporal.ChronoUnit

/**
 * A channel's player (story 55.07): the picture with a LIVE badge, what is on now and next, the
 * channel's guide for the rest of the day, and the controls below the picture: Pause / Play,
 * Back to live, Previous and Next channel (also Page Up / Page Down and Up / Down) and Close
 * (Escape). Away from home it plays through a remote session, ended when the screen is left.
 */
@Composable
fun ChannelScreen(id: String) {
    val t = LocalTexts.current
    val home = LocalDesktopHome.current
    val live = home.liveTv()
    val scope = rememberCoroutineScope()
    var player by remember { mutableStateOf<MediaPlayer?>(null) }
    val proxy = remember(home.client) { StreamProxy(home.client) }
    val playback = remember(home.client) {
        var cp: ChannelPlayback? = null
        val source = LiveSource(live.api, home.background, deviceLabel(), onEnded = { cp?.ended() })
        proxy.relayToken = { source.token.value }
        ChannelPlayback(
            source, scope,
            local = { s ->
                when (s) {
                    is LiveStream.Lan -> proxy.local(s.url)
                    is LiveStream.Relay -> proxy.relayLocal(s.url)
                }
            },
            show = { url ->
                val next = Playback.onFx { MediaPlayer(Media(url)).apply { setOnReady { play() } } }
                player?.let { old -> Platform.runLater { old.dispose() } }
                player = next
            },
            hide = {
                player?.let { old -> Platform.runLater { old.dispose() } }
                player = null
            },
            signedOut = home::signedOut,
        ).also { cp = it }
    }
    LaunchedEffect(id) {
        playback.open(id)
        if (live.now == null) live.loadNow()
    }
    DisposableEffect(playback) {
        onDispose {
            home.background.launch {
                playback.close()
                proxy.close()
            }
        }
    }
    val current = playback.channelId ?: id
    val entry = live.entry(current)
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }
    val togglePause = {
        val p = player
        if (playback.paused) {
            p?.let { Platform.runLater { it.play() } }
            playback.resume()
        } else {
            p?.let { Platform.runLater { it.pause() } }
            playback.pause()
        }
    }
    Column(
        Modifier.fillMaxSize().background(Color.Black).testTag("channel")
            .focusRequester(focus).focusable()
            .onPreviewKeyEvent { e ->
                if (e.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                when (e.key) {
                    Key.PageDown, Key.DirectionDown -> playback.next(live.lineup)
                    Key.PageUp, Key.DirectionUp -> playback.previous(live.lineup)
                    Key.Spacebar, Key.MediaPlayPause -> togglePause()
                    Key.Escape -> home.nav.back()
                    else -> return@onPreviewKeyEvent false
                }
                true
            },
    ) {
        Box(Modifier.weight(1f).fillMaxWidth(), contentAlignment = Alignment.Center) {
            when (val s = playback.status) {
                ChannelStatus.Tuning -> Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    CircularProgressIndicator()
                    Text(t["live.tuning"], color = Color.White)
                }
                ChannelStatus.Playing -> VideoSurface(player, Modifier.fillMaxSize())
                is ChannelStatus.Failed -> Stopped(s.key?.let { t[it] } ?: t["live.cantPlay"], retry = if (s.retryable) playback::retry else null, close = { home.nav.back() })
                ChannelStatus.Ended -> Stopped(t["live.sessionEnded"], retry = null, close = { home.nav.back() })
                ChannelStatus.Off -> Stopped(t.featureUnavailableSentence(Feature.LIVE_TV), retry = null, close = { home.nav.back() })
            }
        }
        Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Surface(color = MaterialTheme.colorScheme.error, shape = RoundedCornerShape(4.dp)) {
                Text(t["live.badge"], color = Color.White, style = MaterialTheme.typography.labelMedium, modifier = Modifier.padding(horizontal = 6.dp, vertical = 2.dp))
            }
            Column(Modifier.weight(1f)) {
                entry?.let { e ->
                    Text(t["live.channel", e.channel.number, e.channel.name], color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.labelLarge)
                    Text(e.current?.title ?: t["live.nothingOn"], color = Color.White, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    e.next?.let { n -> Text(t["live.next", n.startMs?.let { clockOf(it) } ?: "", n.title], color = Color.LightGray, style = MaterialTheme.typography.bodySmall, maxLines = 1) }
                }
            }
            IconButton(onClick = { playback.previous(live.lineup) }, modifier = Modifier.testTag("channel-previous")) { Icon(Icons.Filled.KeyboardArrowUp, t["live.previousChannel"], tint = Color.White) }
            IconButton(onClick = togglePause, modifier = Modifier.testTag("channel-pause")) {
                if (playback.paused) Icon(Icons.Filled.PlayArrow, t["watch.play"], tint = Color.White) else Icon(Icons.Filled.Pause, t["watch.pause"], tint = Color.White)
            }
            if (playback.paused) OutlinedButton(onClick = playback::backToLive, modifier = Modifier.testTag("back-to-live")) { Text(t["live.backToLive"]) }
            IconButton(onClick = { playback.next(live.lineup) }, modifier = Modifier.testTag("channel-next")) { Icon(Icons.Filled.KeyboardArrowDown, t["live.nextChannel"], tint = Color.White) }
            IconButton(onClick = { home.nav.back() }, modifier = Modifier.testTag("channel-close")) { Icon(Icons.Filled.Close, t["common.close"], tint = Color.White) }
        }
        DayGuide(current, live)
    }
}

private fun clockOf(ms: Long): String =
    "⁦" + java.time.format.DateTimeFormatter.ofPattern("HH:mm").withZone(ZoneId.systemDefault()).format(Instant.ofEpochMilli(ms)) + "⁩"

/** The channel's guide for the rest of the day. */
@Composable
private fun DayGuide(channelId: String, live: LiveTvState) {
    val guide by produceState<ChannelGuide?>(null, channelId) {
        val now = Instant.ofEpochMilli(live.serverNow())
        val end = now.atZone(ZoneId.systemDefault()).toLocalDate().plusDays(1).atStartOfDay(ZoneId.systemDefault()).toInstant()
        value = runCatching { live.api.channelGuide(channelId, now.truncatedTo(ChronoUnit.MINUTES), end) }.getOrNull()
    }
    val blocks = guide?.blocks.orEmpty().filter { !it.isFiller }
    if (blocks.isEmpty()) return
    LazyRow(Modifier.fillMaxWidth().height(72.dp).padding(horizontal = 16.dp, vertical = 4.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        items(blocks, key = { it.start }) { p ->
            Surface(color = MaterialTheme.colorScheme.surfaceContainerHigh, shape = RoundedCornerShape(8.dp), modifier = Modifier.width(200.dp)) {
                Column(Modifier.padding(8.dp)) {
                    Text(timeRange(p), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    Text(p.title, style = MaterialTheme.typography.labelLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }
        }
    }
}

@Composable
private fun Stopped(sentence: String, retry: (() -> Unit)?, close: () -> Unit) {
    val t = LocalTexts.current
    Column(Modifier.padding(24.dp).testTag("channel-stopped"), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(t["live.cantPlay"], color = Color.White, style = MaterialTheme.typography.titleMedium)
        Text(sentence, color = Color.White, textAlign = TextAlign.Center)
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            if (retry != null) Button(onClick = retry) { Text(t["common.retry"]) }
            OutlinedButton(onClick = close) { Text(t["common.close"], color = Color.White) }
        }
    }
}
