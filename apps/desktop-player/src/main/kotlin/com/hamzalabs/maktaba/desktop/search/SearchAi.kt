package com.hamzalabs.maktaba.desktop.search

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.PrimaryTabRow
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.AiExchange
import com.hamzalabs.maktaba.core.api.AiPhase
import com.hamzalabs.maktaba.core.api.AiRefusal
import com.hamzalabs.maktaba.desktop.LocalTexts
import com.hamzalabs.maktaba.desktop.Texts
import com.hamzalabs.maktaba.desktop.choosable
import com.hamzalabs.maktaba.desktop.formatDuration
import kotlinx.coroutines.CoroutineScope

/** Results and AI mode (story 49.14): Results is the default; AI mode asks only once opened. Tab reaches them; Enter or a click chooses. */
@Composable
internal fun SearchTabs(model: SearchModel) {
    val t = LocalTexts.current
    PrimaryTabRow(selectedTabIndex = model.tab.ordinal, modifier = Modifier.fillMaxWidth().testTag("search-tabs")) {
        Tab(model.tab == SearchTab.Results, { model.tab = SearchTab.Results }, text = { Text(t["search.tab.results"]) }, modifier = Modifier.testTag("tab-results"))
        Tab(model.tab == SearchTab.Ai, { model.tab = SearchTab.Ai }, text = { Text(t["search.tab.ai"]) }, modifier = Modifier.testTag("tab-ai"))
    }
}

/**
 * AI mode on the desktop (story 49.14): each question, its answer sentence by sentence with a
 * chip per citation (it plays the video from the cited moment), the passages the model read
 * (shown first, while the answer is written), and the field for a follow-up (Enter asks).
 */
@Composable
internal fun AiPane(
    model: SearchModel,
    scope: CoroutineScope,
    play: (videoId: String, title: String, startSec: Double) -> Unit,
    openWeb: (path: String) -> Unit,
) {
    val t = LocalTexts.current
    val state by model.ai.state.collectAsState()
    val list = rememberLazyListState()
    LaunchedEffect(state.exchanges.size) {
        if (state.exchanges.size > 1) list.animateScrollToItem(maxOf(0, list.layoutInfo.totalItemsCount - 1))
    }
    Column(Modifier.fillMaxSize().testTag("ai"), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        LazyColumn(Modifier.weight(1f).fillMaxWidth(), state = list, verticalArrangement = Arrangement.spacedBy(8.dp)) {
            if (state.isEmpty) item(key = "intro") { Text(t["search.ai.intro"], color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.testTag("ai-intro")) }
            state.exchanges.forEachIndexed { i, e ->
                if (i > 0) item(key = "div-${e.id}") { HorizontalDivider() }
                exchange(t, e, last = i == state.exchanges.size - 1, play, again = { model.ai.retry(scope) })
            }
            state.refusal?.let { r -> item(key = "refusal") { Refusal(t, r, openWeb, again = { model.ai.retry(scope) }) } }
        }
        AskField(t, first = state.exchanges.isEmpty()) { q -> model.ai.ask(scope, q, model.choice.filters()) }
    }
}

@OptIn(ExperimentalLayoutApi::class)
private fun LazyListScope.exchange(t: Texts, e: AiExchange, last: Boolean, play: (String, String, Double) -> Unit, again: () -> Unit) {
    item(key = "q-${e.id}") {
        Text(e.q, style = MaterialTheme.typography.titleMedium, modifier = Modifier.semantics { heading() }.testTag("ai-question-${e.id}"))
    }
    status(t, e)?.let { words ->
        item(key = "status-${e.id}") {
            Row(Modifier.fillMaxWidth().testTag("ai-status-${e.id}").semantics { liveRegion = LiveRegionMode.Polite }, horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.CenterVertically) {
                if (e.live) CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                Text(words, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.weight(1f))
                if (last && e.phase == AiPhase.Stopped) TextButton(onClick = again, modifier = Modifier.testTag("ai-again")) { Text(t["search.ai.again"]) }
            }
        }
    }
    if (e.sentences.isNotEmpty()) {
        item(key = "answer-${e.id}") {
            Column(Modifier.fillMaxWidth().testTag("ai-answer-${e.id}"), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                e.sentences.forEachIndexed { si, s ->
                    Column(Modifier.testTag("ai-sentence-${e.id}-$si")) {
                        Text(s.text, style = MaterialTheme.typography.bodyLarge)
                        FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                            for (c in s.citations) {
                                val time = formatDuration(c.startSec)
                                val title = e.passage(c)?.title.orEmpty()
                                val spoken = t["search.ai.citation", c.n, title, time]
                                AssistChip(
                                    onClick = { play(c.videoId, title, c.startSec) },
                                    label = { Text("${c.n} · $time") },
                                    leadingIcon = { Icon(Icons.Filled.PlayArrow, null, Modifier.size(16.dp)) },
                                    modifier = Modifier.testTag("ai-cite-${e.id}-$si-${c.n}").semantics { contentDescription = spoken },
                                )
                            }
                        }
                    }
                }
            }
        }
    }
    if (e.passages.isNotEmpty()) {
        item(key = "sources-${e.id}") {
            Column(Modifier.fillMaxWidth().testTag("ai-sources-${e.id}"), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(t["search.ai.sources"], style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary, modifier = Modifier.semantics { heading() })
                for (p in e.passages) {
                    val time = formatDuration(p.startSec)
                    val spoken = t["search.ai.passage", p.title, time, p.text]
                    Row(
                        Modifier.fillMaxWidth().choosable({ play(p.videoId, p.title, p.startSec) }).semantics { contentDescription = spoken }.padding(6.dp).testTag("ai-passage-${e.id}-${p.n}"),
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                    ) {
                        Text("${p.n}", style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.primary)
                        Column(Modifier.weight(1f)) {
                            Text(listOf(p.title, time).filter { it.isNotBlank() }.joinToString(" · "), style = MaterialTheme.typography.labelLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            Text(p.text, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 3, overflow = TextOverflow.Ellipsis)
                        }
                    }
                }
            }
        }
    }
}

/** The status line; null when the answer itself says enough. */
private fun status(t: Texts, e: AiExchange): String? = when (e.phase) {
    AiPhase.Asking -> e.queue?.let { t["search.ai.queue", it] } ?: t["search.ai.reading"]
    AiPhase.Writing -> if (e.sentences.isEmpty()) t["search.ai.writing"] else null
    AiPhase.Answered -> if (e.reason == "timeout") t["search.ai.timeout"] else null
    AiPhase.NoAnswer -> t[if (e.reason == "no-passages" || e.passages.isEmpty()) "search.ai.noPassages" else "search.ai.noAnswer"]
    AiPhase.Stopped -> t[if (e.reason == "timeout") "search.ai.timeout" else "search.ai.stopped"]
}

/** Why the home writes no answer: the tab's message; when the model is not set up, the setting and the help page in the browser. */
@Composable
private fun Refusal(t: Texts, r: AiRefusal, openWeb: (String) -> Unit, again: () -> Unit) {
    Column(Modifier.fillMaxWidth().testTag("ai-refusal").semantics { liveRegion = LiveRegionMode.Polite }, verticalArrangement = Arrangement.spacedBy(8.dp)) {
        when (r) {
            is AiRefusal.Unavailable -> {
                Text(t["search.ai.unavailableTitle"], style = MaterialTheme.typography.titleMedium, modifier = Modifier.semantics { heading() })
                Text(t["search.ai.unavailable"], modifier = Modifier.testTag("ai-unavailable"))
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    TextButton(onClick = { openWeb(r.settings) }, modifier = Modifier.testTag("ai-settings")) { Text(t["search.ai.settings"]) }
                    TextButton(onClick = { openWeb(r.help) }, modifier = Modifier.testTag("ai-help")) { Text(t["search.ai.help"]) }
                }
            }
            AiRefusal.Off -> Text(t["search.ai.off"], modifier = Modifier.testTag("ai-off"))
            is AiRefusal.Busy -> Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(t["search.ai.busy"], modifier = Modifier.weight(1f).testTag("ai-busy"))
                TextButton(onClick = again, modifier = Modifier.testTag("ai-again")) { Text(t["search.ai.again"]) }
            }
            AiRefusal.NotOnThisHome -> Text(t["search.ai.oldHome"], modifier = Modifier.testTag("ai-old-home"))
        }
    }
}

@Composable
private fun AskField(t: Texts, first: Boolean, ask: (String) -> Unit) {
    var text by remember { mutableStateOf("") }
    val send = {
        if (text.isNotBlank()) {
            ask(text)
            text = ""
        }
    }
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        OutlinedTextField(
            text,
            { text = it },
            singleLine = true,
            placeholder = { Text(t[if (first) "search.ai.first" else "search.ai.followUp"]) },
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Send),
            keyboardActions = KeyboardActions(onSend = { send() }),
            modifier = Modifier.weight(1f).testTag("ai-follow-up").onPreviewKeyEvent { e ->
                if (e.type == KeyEventType.KeyDown && (e.key == Key.Enter || e.key == Key.NumPadEnter)) {
                    send()
                    true
                } else {
                    false
                }
            },
        )
        Button(onClick = send, enabled = text.isNotBlank(), modifier = Modifier.testTag("ai-ask")) { Text(t["search.ai.ask"]) }
    }
}
