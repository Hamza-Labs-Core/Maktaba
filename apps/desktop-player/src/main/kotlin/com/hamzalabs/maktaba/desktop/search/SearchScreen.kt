package com.hamzalabs.maktaba.desktop.search

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Clear
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.History
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.Feature
import com.hamzalabs.maktaba.core.api.Passage
import com.hamzalabs.maktaba.core.api.TranscriptHit
import com.hamzalabs.maktaba.desktop.EmptyBox
import com.hamzalabs.maktaba.desktop.ErrorBox
import com.hamzalabs.maktaba.desktop.Loading
import com.hamzalabs.maktaba.desktop.LocalTexts
import com.hamzalabs.maktaba.desktop.PlayRequest
import com.hamzalabs.maktaba.desktop.PosterCard
import com.hamzalabs.maktaba.desktop.choosable
import com.hamzalabs.maktaba.desktop.formatDuration
import com.hamzalabs.maktaba.desktop.shell.DesktopHome
import com.hamzalabs.maktaba.desktop.shell.FeatureOffNote
import com.hamzalabs.maktaba.desktop.shell.LocalDesktopHome
import com.hamzalabs.maktaba.desktop.shell.Route
import com.hamzalabs.maktaba.desktop.shell.features
import com.hamzalabs.maktaba.desktop.video.VideoPage
import kotlinx.coroutines.launch

/** The window is wide enough for a video page beside the results. */
private val WIDE = 1100.dp

/** This home's Search state, kept while it is signed in. */
fun DesktopHome.searchModel(): SearchModel = keep("search") {
    SearchModel(client, ::signedOut, meaningOn = { capabilities.value.isOn(Feature.SEMANTIC_SEARCH) })
}

/** A snippet with the matched words (`<mark>…</mark>`) in bold. */
fun marked(snippet: String): AnnotatedString = buildAnnotatedString {
    var rest = snippet
    while (true) {
        val a = rest.indexOf("<mark>")
        if (a < 0) break
        val b = rest.indexOf("</mark>", a)
        if (b < 0) break
        append(rest.substring(0, a))
        withStyle(SpanStyle(fontWeight = FontWeight.Bold)) { append(rest.substring(a + 6, b)) }
        rest = rest.substring(b + 7)
    }
    append(rest.replace("<mark>", "").replace("</mark>", ""))
}

/** Search (story 55.04): the field focused when the section opens, and by Ctrl+F or `/` from anywhere. */
@Composable
fun SearchScreen() {
    val home = LocalDesktopHome.current
    val model = home.searchModel()
    SearchContent(model, home, home.features())
}

@Composable
fun SearchContent(model: SearchModel, home: DesktopHome, features: Capabilities) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    val field = remember { FocusRequester() }
    LaunchedEffect(home.nav.searchAsked) { runCatching { field.requestFocus() } }
    LaunchedEffect(model) { model.loadRecent() }
    val admin = home.user.isAdmin
    BoxWithConstraints(Modifier.fillMaxSize().testTag("search")) {
        val wide = maxWidth >= WIDE
        // a title suggestion opens its video: beside the results on a wide window, else on its own page
        val openVideo: (String) -> Unit = { id -> if (wide) model.chosen = id else home.nav.push(Route.Video(id)) }
        Row(Modifier.fillMaxSize()) {
            Column(Modifier.weight(1f).fillMaxHeight().padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(
                    model.query,
                    { q -> model.query = q; scope.launch { model.typing(q) } },
                    singleLine = true,
                    placeholder = { Text(t["search.hint"]) },
                    leadingIcon = { Icon(Icons.Filled.Search, null) },
                    trailingIcon = {
                        if (model.query.isNotEmpty()) IconButton(onClick = { model.clear(); scope.launch { model.loadRecent() } }, modifier = Modifier.testTag("search-clear")) {
                            Icon(Icons.Filled.Clear, t["search.clear"])
                        }
                    },
                    keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                    keyboardActions = KeyboardActions(onSearch = { scope.launch { model.enter(openVideo) } }),
                    modifier = Modifier.fillMaxWidth().focusRequester(field).testTag("search-field").onPreviewKeyEvent { e ->
                        if (e.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                        when (e.key) {
                            Key.DirectionDown -> { model.move(1); model.suggestions.isNotEmpty() }
                            Key.DirectionUp -> { model.move(-1); model.suggestions.isNotEmpty() }
                            Key.Enter, Key.NumPadEnter -> { scope.launch { model.enter(openVideo) }; true }
                            else -> false
                        }
                    },
                )
                if (admin && !features.isOn(Feature.SEMANTIC_SEARCH)) {
                    Text(t["search.wordsOnly"], color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.testTag("words-only"))
                }
                Box(Modifier.weight(1f)) { Results(model, home, features, wide, openVideo) }
            }
            if (wide && model.answer?.isEmpty == false) {
                Box(Modifier.weight(1f).fillMaxHeight().testTag("search-preview")) {
                    val id = model.chosen
                    if (id == null) EmptyBox(t["search.previewHint"]) else VideoPage(id, null)
                }
            }
        }
    }
}

@Composable
private fun Results(model: SearchModel, home: DesktopHome, features: Capabilities, wide: Boolean, openVideo: (String) -> Unit) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    val answer = model.answer
    val problem = model.problem
    // a refined search (the same words) keeps its results, order and filters on screen until it answers
    val refining = model.searching && answer != null && answer.q == model.submitted?.trim()
    val refine: @Composable () -> Unit = {
        if (answer != null) {
            Refine(
                model,
                answer,
                onSort = { scope.launch { model.sortBy(it) } },
                onToggle = { f, b -> scope.launch { model.toggle(f, b) } },
                onRemove = { f, v -> scope.launch { model.remove(f, v) } },
                onClear = { scope.launch { model.clearFilters() } },
                onDidYouMean = { scope.launch { model.acceptDidYouMean() } },
            )
        }
    }
    when {
        model.query.isNotBlank() && model.suggestions.isNotEmpty() && answer?.q != model.query.trim() -> LazyColumn(Modifier.testTag("suggestions")) {
            items(model.suggestions.withIndex().toList(), key = { "${it.index}-${it.value.kindWire}-${it.value.text}" }) { (i, s) ->
                ListItem(
                    headlineContent = { Text(s.text) },
                    supportingContent = s.kind?.let { k -> { Text(t.kind(k)) } },
                    leadingContent = { Icon(kindIcon(s.kind), null) },
                    colors = if (i == model.highlighted) ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerHigh) else ListItemDefaults.colors(),
                    modifier = Modifier.choosable({ scope.launch { model.choose(s, openVideo) } }).testTag("suggestion-$i"),
                )
            }
        }
        model.searching && !refining -> Loading()
        problem != null && answer == null -> ErrorBox(t["search.failed"], problem, onRetry = { scope.launch { model.retry() } })
        answer == null || model.query.isBlank() -> Recent(model)
        // nothing found: the filters that left nothing can be taken off here
        answer.isEmpty -> LazyColumn(Modifier.fillMaxSize().testTag("results"), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            item { refine() }
            item { Text(t["search.noResults", answer.q], style = MaterialTheme.typography.bodyLarge, modifier = Modifier.testTag("search-none")) }
        }
        else -> {
            val transcriptsHidden = features.hides(Feature.TRANSCRIPTS)
            val transcriptsNote = features.explains(Feature.TRANSCRIPTS)
            LazyColumn(Modifier.fillMaxSize().testTag("results"), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                item { refine() }
                if (answer.degraded) item { Text(t["search.degraded"], color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.testTag("degraded")) }
                if (answer.titles.isNotEmpty()) {
                    item { Text(t["search.titles"], style = MaterialTheme.typography.titleMedium, modifier = Modifier.semantics { heading() }) }
                    item {
                        LazyRow(horizontalArrangement = Arrangement.spacedBy(12.dp), contentPadding = PaddingValues(vertical = 4.dp), modifier = Modifier.testTag("titles")) {
                            items(answer.titles, key = { it.videoId }) { hit ->
                                PosterCard(
                                    hit.title, home.posters, hit.videoId,
                                    onClick = { if (wide) model.chosen = hit.videoId else home.nav.push(Route.Video(hit.videoId)) },
                                    modifier = Modifier.testTag("title-${hit.videoId}"),
                                    subtitle = hit.libraryName.ifBlank { null },
                                )
                            }
                        }
                    }
                    if (answer.moreTitles) item { TextButton(onClick = { scope.launch { model.moreTitles() } }, modifier = Modifier.testTag("more-titles")) { Text(t["search.more"]) } }
                }
                if (transcriptsNote) item { FeatureOffNote(Feature.TRANSCRIPTS, home.webUrl, open = home.actions.openUrl) }
                if (!transcriptsHidden && !transcriptsNote && answer.transcripts.isNotEmpty()) {
                    item { Text(t["search.transcripts"], style = MaterialTheme.typography.titleMedium, modifier = Modifier.semantics { heading() }.testTag("transcripts")) }
                    items(answer.transcripts, key = { "t-" + it.videoId }) { hit -> TranscriptBlock(hit, home, onOpen = { p ->
                        if (wide) model.chosen = hit.videoId
                        else home.nav.push(Route.Video(hit.videoId, p?.let { Route.Passage(it.startSec, it.snippet.replace("<mark>", "").replace("</mark>", "")) }))
                    }) }
                    if (answer.moreTranscripts) item { TextButton(onClick = { scope.launch { model.moreTranscripts() } }, modifier = Modifier.testTag("more-transcripts")) { Text(t["search.more"]) } }
                }
            }
        }
    }
}

@Composable
private fun TranscriptBlock(hit: TranscriptHit, home: DesktopHome, onOpen: (Passage?) -> Unit) {
    val t = LocalTexts.current
    Column(Modifier.fillMaxWidth().testTag("transcript-${hit.videoId}"), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Row(Modifier.fillMaxWidth().choosable({ onOpen(hit.passages.firstOrNull()) }).padding(4.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(hit.title, style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (hit.matches > 0) Text(t["search.matches", hit.matches], style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        for (p in hit.passages) {
            Row(Modifier.fillMaxWidth().padding(start = 12.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(marked(p.snippet), style = MaterialTheme.typography.bodyMedium, modifier = Modifier.weight(1f))
                OutlinedButton(
                    onClick = { home.play(PlayRequest(hit.videoId, hit.title, hit.durationSec, startSec = p.startSec.toInt())) },
                    modifier = Modifier.testTag("play-from-${hit.videoId}-${p.startSec.toInt()}"),
                ) { Text(t["search.playFrom", formatDuration(p.startSec)]) }
            }
        }
        HorizontalDivider()
    }
}

@Composable
private fun Recent(model: SearchModel) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    if (model.recent.isEmpty()) {
        EmptyBox(t["search.emptyHint"])
        return
    }
    LazyColumn(Modifier.fillMaxSize().testTag("recent")) {
        item {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Text(t["search.recent"], style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f).semantics { heading() })
                TextButton(onClick = { scope.launch { model.clearRecent() } }, modifier = Modifier.testTag("recent-clear")) { Text(t["search.recentClear"]) }
            }
        }
        items(model.recent, key = { it.searchId }) { r ->
            ListItem(
                headlineContent = { Text(r.q) },
                supportingContent = { Text(t["search.recentCounts", r.titleCount, r.transcriptCount]) },
                leadingContent = { Icon(Icons.Filled.History, null) },
                trailingContent = {
                    IconButton(onClick = { scope.launch { model.forget(r) } }, modifier = Modifier.testTag("recent-remove-${r.searchId}")) {
                        Icon(Icons.Filled.Close, t["search.recentRemove", r.q])
                    }
                },
                // a recent search runs with the order and filters on screen, as did-you-mean does
                modifier = Modifier.choosable({ scope.launch { model.submit(r.q, keepFilters = true) } }).testTag("recent-${r.searchId}"),
            )
        }
    }
}
