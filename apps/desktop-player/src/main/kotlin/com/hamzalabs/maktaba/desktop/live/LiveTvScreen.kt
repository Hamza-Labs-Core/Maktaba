package com.hamzalabs.maktaba.desktop.live

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowLeft
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.PrimaryTabRow
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.Feature
import com.hamzalabs.maktaba.core.api.LiveApi
import com.hamzalabs.maktaba.core.api.NowEntry
import com.hamzalabs.maktaba.core.live.GuideWindow
import com.hamzalabs.maktaba.desktop.EmptyBox
import com.hamzalabs.maktaba.desktop.ErrorBox
import com.hamzalabs.maktaba.desktop.Loading
import com.hamzalabs.maktaba.desktop.LocalTexts
import com.hamzalabs.maktaba.desktop.ProgressStrip
import com.hamzalabs.maktaba.desktop.choosable
import com.hamzalabs.maktaba.desktop.shell.DesktopHome
import com.hamzalabs.maktaba.desktop.shell.FeatureOffNote
import com.hamzalabs.maktaba.desktop.shell.LocalDesktopHome
import com.hamzalabs.maktaba.desktop.shell.Route
import com.hamzalabs.maktaba.desktop.shell.features
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/** How often Now and the guide are asked again while shown. */
const val LIVE_REFRESH_MS = 60_000L

/** This home's Live TV state, kept while it is signed in. */
fun DesktopHome.liveTv(): LiveTvState = keep("live") { LiveTvState(LiveApi(client), ::signedOut) }

/** The Live TV section (story 55.07). */
@Composable
fun LiveTvScreen() {
    val home = LocalDesktopHome.current
    LiveTvContent(home.liveTv(), home, home.features())
}

@Composable
fun LiveTvContent(state: LiveTvState, home: DesktopHome, features: Capabilities) {
    val t = LocalTexts.current
    // the shell hides the section from a viewer; an admin reads why, and how to turn it on
    if (!features.isOn(Feature.LIVE_TV)) {
        FeatureOffNote(Feature.LIVE_TV, home.webUrl, centered = true, open = home.actions.openUrl)
        return
    }
    LaunchedEffect(state) {
        while (true) {
            state.loadNow()
            delay(LIVE_REFRESH_MS)
        }
    }
    Column(Modifier.fillMaxSize().testTag("live")) {
        PrimaryTabRow(selectedTabIndex = state.tab.ordinal) {
            Tab(state.tab == LiveTab.Now, { state.tab = LiveTab.Now }, text = { Text(t["live.now"]) }, modifier = Modifier.testTag("tab-now"))
            Tab(state.tab == LiveTab.Guide, { state.tab = LiveTab.Guide }, text = { Text(t["live.guide"]) }, modifier = Modifier.testTag("tab-guide"))
        }
        when (state.tab) {
            LiveTab.Now -> NowTab(state, home)
            LiveTab.Guide -> GuideTab(state, home)
        }
    }
}

private val clock = DateTimeFormatter.ofPattern("HH:mm")

@Composable
private fun NowTab(state: LiveTvState, home: DesktopHome) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    val now = state.now
    val problem = state.nowProblem
    when {
        now == null && problem != null -> ErrorBox(t["live.cantLoad"], problem, onRetry = { scope.launch { state.loadNow() } })
        now == null -> Loading()
        now.channels.isEmpty() -> EmptyBox(t["live.none"], t["live.noneDetail"])
        else -> {
            val at = state.serverNow()
            LazyColumn(Modifier.fillMaxSize().testTag("now")) {
                items(now.channels.sortedBy { it.channel.number }, key = { it.channel.id }) { e -> NowRow(e, at, state.api) { home.nav.push(Route.Channel(e.channel.id)) } }
            }
        }
    }
}

@Composable
private fun NowRow(e: NowEntry, nowMs: Long, api: LiveApi, onOpen: () -> Unit) {
    val t = LocalTexts.current
    Row(
        Modifier.fillMaxWidth().choosable(onOpen).padding(16.dp).testTag("now-${e.channel.id}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        ChannelLogo(e.channel, api, 56.dp)
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(t["live.channel", e.channel.number, e.channel.name], style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.primary)
            val cur = e.current
            Text(cur?.title ?: t["live.nothingOn"], style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
            cur?.progressAt(nowMs)?.let { f ->
                ProgressStrip(f.toFloat())
                Text(t["live.progress", (f * 100).toInt()], style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            e.next?.let { n ->
                val at = n.startMs?.let { "⁦" + clock.withZone(ZoneId.systemDefault()).format(Instant.ofEpochMilli(it)) + "⁩" } ?: ""
                Text(t["live.next", at, n.title], style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
    }
    HorizontalDivider()
}

@Composable
private fun GuideTab(state: LiveTvState, home: DesktopHome) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    val zone = remember { ZoneId.systemDefault() }
    val base = remember { state.serverNow() }
    var nowMs by remember { mutableLongStateOf(state.serverNow()) }
    val window = remember(state.guideShift) { GuideWindow.around(base, zone, GuideWindow.hours(wide = true)).shifted(state.guideShift * GUIDE_STEP_HOURS) }
    LaunchedEffect(window) {
        while (true) {
            state.loadGuide(window)
            nowMs = state.serverNow()
            delay(LIVE_REFRESH_MS)
        }
    }
    Column(Modifier.fillMaxSize().testTag("guide")) {
        Row(Modifier.fillMaxWidth().padding(8.dp), verticalAlignment = Alignment.CenterVertically) {
            TextButton(onClick = { state.guideShift-- }, modifier = Modifier.testTag("guide-earlier")) {
                Icon(Icons.AutoMirrored.Filled.KeyboardArrowLeft, null)
                Text(t["live.earlier"])
            }
            val day = window.dayFrom(nowMs, zone)
            Text(
                when (day) {
                    0 -> t["live.today"]
                    1 -> t["live.tomorrow"]
                    else -> DateTimeFormatter.ofPattern("EEE d").withLocale(java.util.Locale.forLanguageTag(t.lang.code)).withZone(zone).format(Instant.ofEpochMilli(window.startMs))
                },
                style = MaterialTheme.typography.titleSmall,
                modifier = Modifier.weight(1f).testTag("guide-day"),
            )
            TextButton(onClick = { state.guideShift++ }, modifier = Modifier.testTag("guide-later")) {
                Text(t["live.later"])
                Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, null)
            }
        }
        val guide = state.guide?.takeIf { state.guideWindow == window }
        val problem = state.guideProblem
        when {
            guide == null && problem != null -> ErrorBox(t["live.cantLoad"], problem, onRetry = { scope.launch { state.loadGuide(window) } })
            guide == null -> Loading()
            guide.channels.isEmpty() -> EmptyBox(t["live.none"], t["live.noneDetail"])
            else -> Box(Modifier.fillMaxSize()) {
                GuideGrid(window, guide.channels.sortedBy { it.channel.number }, nowMs, state.api) { home.nav.push(Route.Channel(it)) }
            }
        }
    }
}
