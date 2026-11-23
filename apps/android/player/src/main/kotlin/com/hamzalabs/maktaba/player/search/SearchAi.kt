package com.hamzalabs.maktaba.player.search

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.AssistChip
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextDirection
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.R as CoreR
import com.hamzalabs.maktaba.core.api.AiCitation
import com.hamzalabs.maktaba.core.api.AiExchange
import com.hamzalabs.maktaba.core.api.AiPassage
import com.hamzalabs.maktaba.core.api.AiPhase
import com.hamzalabs.maktaba.core.api.AiRefusal
import com.hamzalabs.maktaba.core.api.AiState
import com.hamzalabs.maktaba.core.ui.MaktabaTokens
import com.hamzalabs.maktaba.core.ui.formatDuration
import com.hamzalabs.maktaba.core.search.SearchModel
import kotlinx.coroutines.CoroutineScope

/** How many passages an answer shows before Show more. */
private const val PASSAGES_SHOWN = 5

/**
 * Search's AI mode tab (story 49.14): each question, its answer sentence by sentence with a
 * chip per citation (it opens the player at the cited moment), the passages the model read
 * (shown first, while the answer is written), and a field for a follow-up. The home's refusals
 * are the tab's message, not an error.
 */
@Composable
internal fun AiPane(
    model: SearchModel,
    scope: CoroutineScope,
    play: (videoId: String, startSec: Double) -> Unit,
    openHelp: (path: String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val state by model.ai.state.collectAsState()
    val list = rememberLazyListState()
    // the newest question in view as it is asked (it is the last, over the field)
    LaunchedEffect(state.exchanges.size) {
        if (state.exchanges.size > 1) list.animateScrollToItem(maxOf(0, list.layoutInfo.totalItemsCount - 1))
    }
    Column(modifier.fillMaxSize().testTag("ai")) {
        LazyColumn(Modifier.weight(1f).fillMaxWidth(), state = list) {
            if (state.isEmpty) {
                item(key = "intro") {
                    Text(
                        stringResource(CoreR.string.search_ai_intro),
                        Modifier.padding(16.dp).testTag("ai-intro"),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            // oldest first: a follow-up is asked from the field under the newest
            state.exchanges.forEachIndexed { i, e ->
                if (i > 0) item(key = "div-${e.id}") { HorizontalDivider(Modifier.padding(vertical = 8.dp)) }
                exchange(e, last = i == state.exchanges.size - 1, play = play, again = { model.ai.retry(scope) })
            }
            state.refusal?.let { r -> item(key = "refusal") { Refusal(r, openHelp, again = { model.ai.retry(scope) }) } }
            item(key = "end") { Spacer(Modifier.size(16.dp)) }
        }
        FollowUp(
            first = state.exchanges.isEmpty(),
            ask = { q -> model.ai.ask(scope, q, model.choice.filters()) },
        )
    }
}

private fun LazyListScope.exchange(e: AiExchange, last: Boolean, play: (String, Double) -> Unit, again: () -> Unit) {
    item(key = "q-${e.id}") {
        Text(
            e.q,
            Modifier.fillMaxWidth().padding(start = 16.dp, end = 16.dp, top = 12.dp).semantics { heading() }.testTag("ai-question-${e.id}"),
            style = MaterialTheme.typography.titleMedium,
        )
    }
    item(key = "status-${e.id}") { Status(e, last, again) }
    if (e.sentences.isNotEmpty()) {
        item(key = "answer-${e.id}") { Answer(e, play) }
    }
    if (e.passages.isNotEmpty()) {
        item(key = "sources-${e.id}") { Passages(e, play) }
    }
}

/** Where the answer is: its place in line, the passages being found, the answer being written, or why it ended without one. */
@Composable
private fun Status(e: AiExchange, last: Boolean, again: () -> Unit) {
    val text = when (e.phase) {
        AiPhase.Asking -> e.queue?.let { stringResource(CoreR.string.search_ai_queue, it) } ?: stringResource(CoreR.string.search_ai_reading)
        AiPhase.Writing -> if (e.sentences.isEmpty()) stringResource(CoreR.string.search_ai_writing) else null
        AiPhase.Answered -> if (e.reason == "timeout") stringResource(CoreR.string.search_ai_timeout) else null
        AiPhase.NoAnswer -> stringResource(if (e.reason == "no-passages" || e.passages.isEmpty()) CoreR.string.search_ai_no_passages else CoreR.string.search_ai_no_answer)
        AiPhase.Stopped -> stringResource(if (e.reason == "timeout") CoreR.string.search_ai_timeout else CoreR.string.search_ai_stopped)
    } ?: return
    Row(
        Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp).testTag("ai-status-${e.id}")
            .semantics { liveRegion = LiveRegionMode.Polite },
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (e.live) CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
        Text(text, Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        if (last && e.phase == AiPhase.Stopped) {
            TextButton(onClick = again, modifier = Modifier.testTag("ai-again")) { Text(stringResource(CoreR.string.search_ai_again)) }
        }
    }
}

/** The sentences as they come, each followed by a chip per passage it cites. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun Answer(e: AiExchange, play: (String, Double) -> Unit) {
    Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp).testTag("ai-answer-${e.id}"), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        e.sentences.forEachIndexed { si, s ->
            Column(Modifier.testTag("ai-sentence-${e.id}-$si")) {
                Text(s.text, style = MaterialTheme.typography.bodyLarge)
                FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    s.citations.forEach { c -> Citation(e, c, Modifier.testTag("ai-cite-${e.id}-$si-${c.n}")) { play(c.videoId, c.startSec) } }
                }
            }
        }
    }
}

/** A citation: the passage's number and moment; TalkBack reads the video and "play from". */
@Composable
private fun Citation(e: AiExchange, c: AiCitation, modifier: Modifier, onClick: () -> Unit) {
    val time = formatDuration(c.startSec)
    val title = e.passage(c)?.title.orEmpty()
    val spoken = stringResource(CoreR.string.search_ai_citation, c.n, title, time)
    AssistChip(
        onClick = onClick,
        // a time reads left to right in Arabic too
        label = { Text("${c.n} · $time", style = MaterialTheme.typography.labelMedium.copy(textDirection = TextDirection.Ltr)) },
        leadingIcon = { Icon(Icons.Filled.PlayArrow, contentDescription = null, Modifier.size(16.dp)) },
        modifier = modifier.clearAndSetSemantics {
            contentDescription = spoken
            role = Role.Button
        },
    )
}

/** The passages the model read: the first few, then all on Show more. Each plays from its moment. */
@Composable
private fun Passages(e: AiExchange, play: (String, Double) -> Unit) {
    var all by rememberSaveable(e.id) { mutableStateOf(false) }
    val shown = if (all) e.passages else e.passages.take(PASSAGES_SHOWN)
    Column(Modifier.fillMaxWidth().padding(top = 8.dp).testTag("ai-sources-${e.id}")) {
        Text(
            stringResource(CoreR.string.search_ai_sources),
            Modifier.padding(horizontal = 16.dp, vertical = 4.dp).semantics { heading() },
            style = MaterialTheme.typography.titleSmall,
            color = MaterialTheme.colorScheme.primary,
        )
        shown.forEach { p -> PassageRow(p, Modifier.testTag("ai-passage-${e.id}-${p.n}")) { play(p.videoId, p.startSec) } }
        if (!all && e.passages.size > PASSAGES_SHOWN) {
            TextButton(onClick = { all = true }, modifier = Modifier.padding(horizontal = 8.dp).testTag("ai-sources-more-${e.id}")) {
                Text(stringResource(com.hamzalabs.maktaba.player.R.string.search_show_more))
            }
        }
    }
}

@Composable
private fun PassageRow(p: AiPassage, modifier: Modifier, onClick: () -> Unit) {
    val time = formatDuration(p.startSec)
    val spoken = stringResource(CoreR.string.search_ai_passage, p.title, time, p.text)
    Row(
        modifier
            .fillMaxWidth()
            .clickable(role = Role.Button, onClick = onClick)
            .clearAndSetSemantics {
                contentDescription = spoken
                role = Role.Button
            }
            .padding(horizontal = 16.dp, vertical = 6.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalAlignment = Alignment.Top,
    ) {
        Text(
            "${p.n}",
            Modifier.background(MaterialTheme.colorScheme.surfaceContainerHigh, RoundedCornerShape(MaktabaTokens.Radius.sm)).padding(horizontal = 6.dp, vertical = 2.dp),
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.primary,
        )
        Column(Modifier.weight(1f)) {
            Text(
                listOf(p.title, time).filter { it.isNotBlank() }.joinToString(" · "),
                style = MaterialTheme.typography.labelLarge,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(p.text, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 3, overflow = TextOverflow.Ellipsis)
        }
    }
}

/** Why the home writes no answer: the tab's message, with the help page when the model is not set up. */
@Composable
private fun Refusal(r: AiRefusal, openHelp: (String) -> Unit, again: () -> Unit) {
    Column(
        Modifier.fillMaxWidth().padding(16.dp).testTag("ai-refusal").semantics { liveRegion = LiveRegionMode.Polite },
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        when (r) {
            is AiRefusal.Unavailable -> {
                Text(stringResource(CoreR.string.search_ai_unavailable_title), Modifier.semantics { heading() }, style = MaterialTheme.typography.titleMedium)
                Text(stringResource(CoreR.string.search_ai_unavailable), Modifier.testTag("ai-unavailable"), style = MaterialTheme.typography.bodyMedium)
                TextButton(onClick = { openHelp(r.help) }, modifier = Modifier.testTag("ai-help")) { Text(stringResource(CoreR.string.search_ai_help)) }
            }
            AiRefusal.Off -> Text(stringResource(CoreR.string.search_ai_off), Modifier.testTag("ai-off"), style = MaterialTheme.typography.bodyMedium)
            is AiRefusal.Busy -> {
                Text(stringResource(CoreR.string.search_ai_busy), Modifier.testTag("ai-busy"), style = MaterialTheme.typography.bodyMedium)
                busyDetail(r)?.let { Text(it, Modifier.testTag("ai-busy-detail"), style = MaterialTheme.typography.bodySmall) }
                TextButton(onClick = again, modifier = Modifier.testTag("ai-again")) { Text(stringResource(CoreR.string.search_ai_again)) }
            }
            AiRefusal.NotOnThisHome -> Text(stringResource(CoreR.string.search_ai_old_home), Modifier.testTag("ai-old-home"), style = MaterialTheme.typography.bodyMedium)
        }
    }
}

/** The field a question or a follow-up is asked from. */
@Composable
private fun FollowUp(first: Boolean, ask: (String) -> Unit) {
    var text by rememberSaveable { mutableStateOf("") }
    val send = {
        if (text.isNotBlank()) {
            ask(text)
            text = ""
        }
    }
    Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        OutlinedTextField(
            value = text,
            onValueChange = { text = it },
            modifier = Modifier.weight(1f).testTag("ai-follow-up"),
            placeholder = { Text(stringResource(if (first) CoreR.string.search_ai_intro_short else CoreR.string.search_ai_follow_up), maxLines = 1, overflow = TextOverflow.Ellipsis) },
            singleLine = true,
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Send),
            keyboardActions = KeyboardActions(onSend = { send() }),
        )
        IconButton(onClick = send, enabled = text.isNotBlank(), modifier = Modifier.testTag("ai-ask")) {
            Icon(Icons.AutoMirrored.Filled.Send, stringResource(CoreR.string.search_ai_ask))
        }
    }
}

/** A busy home's place in line and when to ask again, as it said them (null: neither). */
@Composable
internal fun busyDetail(r: AiRefusal.Busy): String? = listOfNotNull(
    r.position?.takeIf { it > 0 }?.let { stringResource(CoreR.string.search_ai_busy_ahead, it) },
    r.retryAfterSec?.takeIf { it > 0 }?.let { stringResource(CoreR.string.search_ai_busy_after, it.toInt()) },
).joinToString(" ").ifEmpty { null }
