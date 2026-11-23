package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.R as CoreR
import com.hamzalabs.maktaba.core.api.AiExchange
import com.hamzalabs.maktaba.core.api.AiPhase
import com.hamzalabs.maktaba.core.api.AiRefusal
import com.hamzalabs.maktaba.core.api.AiState
import com.hamzalabs.maktaba.core.search.SearchModel
import com.hamzalabs.maktaba.core.search.SearchTab
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton
import com.hamzalabs.maktaba.tv.ui.components.TvTextField
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette

/** Results and AI mode, two buttons the D-pad moves between (story 49.14); the shown one is ticked. */
@Composable
internal fun TabsRow(search: SearchModel) {
    Row(Modifier.padding(horizontal = Gutter).testTag("tv-search-tabs"), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        for ((tab, words) in listOf(SearchTab.Results to CoreR.string.search_tab_results, SearchTab.Ai to CoreR.string.search_tab_ai)) {
            val on = search.tab == tab
            MaktabaButton(
                onClick = { search.tab = tab },
                modifier = Modifier.testTag(if (tab == SearchTab.Ai) "tv-tab-ai" else "tv-tab-results").semantics { this.selected = on },
            ) { Text((if (on) "✓ " else "") + stringResource(words), maxLines = 1) }
        }
    }
}

/**
 * AI mode on the TV (story 49.14): for each question, the answer's sentences, each with a row
 * of its citations (OK plays the video from the cited moment), then the passages read as a row
 * of buttons (shown first, while the answer is written); under them the field for a follow-up.
 * Every part is a row the D-pad moves between.
 */
internal fun LazyListScope.aiItems(
    state: AiState,
    ask: (String) -> Unit,
    again: () -> Unit,
    play: (videoId: String, startSec: Double) -> Unit,
    helpUrl: (String) -> String,
) {
    if (state.isEmpty) item(key = "ai-intro") { AiNote(stringResource(CoreR.string.search_ai_intro), "tv-ai-intro") }
    state.exchanges.forEachIndexed { i, e -> exchange(e, last = i == state.exchanges.size - 1, play, again) }
    state.refusal?.let { r -> item(key = "ai-refusal") { Refusal(r, helpUrl, again) } }
    item(key = "ai-ask") { AskField(first = state.exchanges.isEmpty(), ask) }
}

private fun LazyListScope.exchange(e: AiExchange, last: Boolean, play: (String, Double) -> Unit, again: () -> Unit) {
    item(key = "ai-q-${e.id}") {
        Text(
            e.q,
            Modifier.padding(horizontal = Gutter).semantics { heading() }.testTag("tv-ai-question-${e.id}"),
            style = MaterialTheme.typography.headlineSmall,
            color = MaktabaPalette.Text,
        )
    }
    status(e)?.let { words ->
        item(key = "ai-status-${e.id}") {
            Row(Modifier.padding(horizontal = Gutter), horizontalArrangement = Arrangement.spacedBy(16.dp), verticalAlignment = Alignment.CenterVertically) {
                AiNote(stringResource(words.first, *words.second), "tv-ai-status-${e.id}")
                if (last && e.phase == AiPhase.Stopped) {
                    MaktabaButton(onClick = again, modifier = Modifier.testTag("tv-ai-again")) { Text(stringResource(CoreR.string.search_ai_again)) }
                }
            }
        }
    }
    e.sentences.forEachIndexed { si, s ->
        item(key = "ai-s-${e.id}-$si") {
            Column(Modifier.fillMaxWidth().testTag("tv-ai-sentence-${e.id}-$si"), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Text(s.text, Modifier.padding(horizontal = Gutter), style = MaterialTheme.typography.bodyLarge, color = MaktabaPalette.Text)
                if (s.citations.isNotEmpty()) {
                    LazyRow(contentPadding = PaddingValues(horizontal = Gutter), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                        for (c in s.citations) {
                            item(key = "c-${c.n}") {
                                val time = clock(c.startSec)
                                val spoken = stringResource(CoreR.string.search_ai_citation, c.n, e.passage(c)?.title.orEmpty(), time)
                                MaktabaButton(
                                    onClick = { play(c.videoId, c.startSec) },
                                    modifier = Modifier.testTag("tv-ai-cite-${e.id}-$si-${c.n}").semantics { contentDescription = spoken },
                                ) { Text("▶ ${c.n} · $time", maxLines = 1) }
                            }
                        }
                    }
                }
            }
        }
    }
    if (e.passages.isNotEmpty()) {
        item(key = "ai-sources-${e.id}") {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(
                    stringResource(CoreR.string.search_ai_sources),
                    Modifier.padding(horizontal = Gutter).semantics { heading() },
                    style = MaterialTheme.typography.titleMedium,
                    color = MaktabaPalette.Gold,
                )
                LazyRow(contentPadding = PaddingValues(horizontal = Gutter), horizontalArrangement = Arrangement.spacedBy(12.dp), modifier = Modifier.testTag("tv-ai-sources-${e.id}")) {
                    for (p in e.passages) {
                        item(key = "p-${p.n}") {
                            val time = clock(p.startSec)
                            val spoken = stringResource(CoreR.string.search_ai_passage, p.title, time, p.text)
                            MaktabaButton(
                                onClick = { play(p.videoId, p.startSec) },
                                modifier = Modifier.testTag("tv-ai-passage-${e.id}-${p.n}").semantics { contentDescription = spoken },
                            ) {
                                Column(Modifier.padding(vertical = 4.dp)) {
                                    Text("${p.n} · ${p.title} · $time", maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.labelLarge)
                                    Text(p.text, maxLines = 2, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodySmall)
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

/** The status line's words and their arguments; null when the answer itself says enough. */
private fun status(e: AiExchange): Pair<Int, Array<Any>>? = when (e.phase) {
    AiPhase.Asking -> e.queue?.let { CoreR.string.search_ai_queue to arrayOf<Any>(it) } ?: (CoreR.string.search_ai_reading to emptyArray())
    AiPhase.Writing -> if (e.sentences.isEmpty()) CoreR.string.search_ai_writing to emptyArray() else null
    AiPhase.Answered -> if (e.reason == "timeout") CoreR.string.search_ai_timeout to emptyArray() else null
    AiPhase.NoAnswer -> (if (e.reason == "no-passages" || e.passages.isEmpty()) CoreR.string.search_ai_no_passages else CoreR.string.search_ai_no_answer) to emptyArray()
    AiPhase.Stopped -> (if (e.reason == "timeout") CoreR.string.search_ai_timeout else CoreR.string.search_ai_stopped) to emptyArray()
}

@Composable
private fun AiNote(text: String, tag: String) {
    Text(
        text,
        Modifier.padding(horizontal = Gutter).testTag(tag).semantics { liveRegion = LiveRegionMode.Polite },
        style = MaterialTheme.typography.titleMedium,
        color = MaktabaPalette.Muted,
    )
}

/** Why the home writes no answer: the tab's message. A TV opens no browser: the help page's address is shown. */
@Composable
private fun Refusal(r: AiRefusal, helpUrl: (String) -> String, again: () -> Unit) {
    Column(Modifier.padding(horizontal = Gutter).testTag("tv-ai-refusal"), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        when (r) {
            is AiRefusal.Unavailable -> {
                Text(stringResource(CoreR.string.search_ai_unavailable_title), Modifier.semantics { heading() }, style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Gold)
                Text(stringResource(CoreR.string.search_ai_unavailable), Modifier.testTag("tv-ai-unavailable"), style = MaterialTheme.typography.bodyLarge, color = MaktabaPalette.Text)
                Text(stringResource(CoreR.string.search_ai_help) + ": " + helpUrl(r.help), Modifier.testTag("tv-ai-help"), style = MaterialTheme.typography.bodyMedium, color = MaktabaPalette.Muted)
            }
            AiRefusal.Off -> Text(stringResource(CoreR.string.search_ai_off), Modifier.testTag("tv-ai-off"), style = MaterialTheme.typography.bodyLarge, color = MaktabaPalette.Text)
            is AiRefusal.Busy -> Row(horizontalArrangement = Arrangement.spacedBy(16.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(stringResource(CoreR.string.search_ai_busy), Modifier.testTag("tv-ai-busy"), style = MaterialTheme.typography.bodyLarge, color = MaktabaPalette.Text)
                MaktabaButton(onClick = again, modifier = Modifier.testTag("tv-ai-again")) { Text(stringResource(CoreR.string.search_ai_again)) }
            }
            AiRefusal.NotOnThisHome -> Text(stringResource(CoreR.string.search_ai_old_home), Modifier.testTag("tv-ai-old-home"), style = MaterialTheme.typography.bodyLarge, color = MaktabaPalette.Text)
        }
    }
}

/** The follow-up field: the keyboard's Send key, or Ask beside it, asks. */
@Composable
private fun AskField(first: Boolean, ask: (String) -> Unit) {
    var text by rememberSaveable { mutableStateOf("") }
    val send = {
        if (text.isNotBlank()) {
            ask(text)
            text = ""
        }
    }
    Row(Modifier.padding(horizontal = Gutter), horizontalArrangement = Arrangement.spacedBy(16.dp), verticalAlignment = Alignment.Bottom) {
        TvTextField(
            value = text,
            onValueChange = { text = it },
            label = stringResource(if (first) CoreR.string.search_ai_intro_short else CoreR.string.search_ai_follow_up),
            modifier = Modifier.fillMaxWidth(0.5f).testTag("tv-ai-follow-up"),
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Send),
            keyboardActions = KeyboardActions(onSend = { send() }),
        )
        MaktabaButton(onClick = send, modifier = Modifier.testTag("tv-ai-ask")) { Text(stringResource(CoreR.string.search_ai_ask)) }
    }
}
