package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.api.Facet
import com.hamzalabs.maktaba.core.api.FacetBucket
import com.hamzalabs.maktaba.core.api.SearchSort
import com.hamzalabs.maktaba.core.api.SuggestKind
import com.hamzalabs.maktaba.core.api.Suggestion
import com.hamzalabs.maktaba.core.api.TitleHit
import com.hamzalabs.maktaba.core.api.TranscriptHit
import com.hamzalabs.maktaba.core.api.languageName
import com.hamzalabs.maktaba.core.search.SUGGEST_DELAY_MS
import com.hamzalabs.maktaba.core.search.SearchAnswer
import com.hamzalabs.maktaba.core.search.SearchModel
import com.hamzalabs.maktaba.core.search.SearchTab
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.collectAsState
import com.hamzalabs.maktaba.core.ui.sentence
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.data.TvApi
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton
import com.hamzalabs.maktaba.tv.ui.components.MediaCard
import com.hamzalabs.maktaba.tv.ui.components.TvTextField
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * Search on the TV (story 49.11), the Android apps' [SearchModel] against `POST /api/search`:
 * suggestions follow the typing (a title opens its video, any other is searched), the keyboard's
 * Search key runs what is typed, and the results are **Titles** and **In the transcript**, a row
 * each (a transcript card plays from its first passage). Above them, for the remote: Did you
 * mean, the order, the filters in use (OK takes one off) and **Filters**, which opens a row of
 * chips per facet with its counts. Every part is a row the D-pad moves between; each choice runs
 * the search again and keeps the focus where it was. Kept by the signed-in app, so Back from a
 * result finds it again with that result's row focused.
 */
@Composable
fun SearchScreen(
    api: TvApi,
    search: SearchModel,
    focusFirst: Boolean,
    onOpen: (String) -> Unit,
    onPlay: (videoId: String, startSec: Double) -> Unit,
    showTranscripts: Boolean = true,
) {
    val scope = rememberCoroutineScope()
    var typing by remember { mutableStateOf(false) }
    // suggestions follow the typing once it rests, until the words are searched
    LaunchedEffect(search.query, typing) {
        if (typing) {
            delay(SUGGEST_DELAY_MS)
            search.suggest(search.query)
        }
    }
    val submit: (String) -> Unit = { q ->
        if (q.isNotBlank()) {
            typing = false
            scope.launch { search.submit(q) }
        }
    }
    val choose: (Suggestion) -> Unit = { s ->
        val video = s.opensVideo
        if (video != null) {
            typing = false
            onOpen(video)
        } else {
            submit(s.text)
        }
    }
    var filtersOpen by rememberSaveable { mutableStateOf(false) }
    val answer = search.answer
    val firstResult = remember { FocusRequester() }
    // back from a result: its row takes the focus again, once (a refined answer later keeps the
    // focus where the remote put it; typing keeps it in the field)
    var refocused by remember { mutableStateOf(false) }
    val hasResults = answer != null && !answer.isEmpty
    LaunchedEffect(focusFirst, hasResults) {
        if (focusFirst && hasResults && !refocused) {
            refocused = true
            runCatching { firstResult.requestFocus() }
        }
    }
    // AI mode asks only on its tab (story 49.14): opening it asks the search's words, once;
    // leaving it, or Search, stops the answer still coming
    val ai by search.ai.state.collectAsState()
    LaunchedEffect(search.tab, search.submitted) {
        if (search.tab == SearchTab.Ai) search.ai.open(scope, search.submitted ?: search.query, search.choice.filters())
    }
    DisposableEffect(search.tab) { onDispose { search.ai.stop() } }
    Column(Modifier.fillMaxSize().padding(top = 28.dp).testTag("tv-search")) {
        TvTextField(
            value = search.query,
            onValueChange = { search.query = it; typing = true },
            label = stringResource(R.string.search_hint),
            modifier = Modifier.padding(horizontal = Gutter).fillMaxWidth(0.6f).testTag("tv-search-field"),
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
            keyboardActions = KeyboardActions(onSearch = { submit(search.query) }),
        )
        val showSuggestions = typing && search.query.isNotBlank() && search.suggestions.isNotEmpty()
        LazyColumn(
            Modifier.fillMaxSize().testTag("tv-search-list"),
            contentPadding = PaddingValues(top = 16.dp, bottom = 32.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            if (showSuggestions) {
                item(key = "suggestions") { Suggestions(search.suggestions, choose) }
            }
            item(key = "tabs") { TabsRow(search) }
            val problem = search.problem
            when {
                search.tab == SearchTab.Ai -> aiItems(
                    ai,
                    ask = { q -> search.ai.ask(scope, q, search.choice.filters()) },
                    again = { search.ai.retry(scope) },
                    play = onPlay,
                    helpUrl = { path -> api.client.home.toString().trimEnd('/') + path },
                )
                search.searching && (answer == null || answer.q != search.submitted?.trim()) ->
                    item(key = "loading") { Note(stringResource(R.string.loading)) }
                problem != null -> item(key = "problem") {
                    Row(Modifier.padding(horizontal = Gutter), horizontalArrangement = Arrangement.spacedBy(16.dp), verticalAlignment = Alignment.CenterVertically) {
                        Text(stringResource(R.string.could_not_load, stringResource(problem.kind.sentence())), style = MaterialTheme.typography.bodyLarge, color = MaktabaPalette.Gold)
                        MaktabaButton(onClick = { scope.launch { search.retry() } }, modifier = Modifier.testTag("tv-search-retry")) { Text(stringResource(R.string.try_again)) }
                    }
                }
                answer != null -> {
                    answer.didYouMean?.let { d -> item(key = "dym") { DidYouMean(d) { scope.launch { search.acceptDidYouMean() } } } }
                    item(key = "refine") {
                        RefineRow(search, answer, filtersOpen, onFilters = { filtersOpen = !filtersOpen }, onSort = { scope.launch { search.sortBy(it) } }, onClear = { scope.launch { search.clearFilters() } })
                    }
                    if (!search.choice.isEmpty) {
                        item(key = "active") { ActiveFilters(search) { f, v -> scope.launch { search.remove(f, v) } } }
                    }
                    if (filtersOpen) {
                        for (f in Facet.entries) {
                            val buckets = search.choice.shown(answer.facets, f)
                            if (buckets.isNotEmpty()) {
                                item(key = "facet-${f.wire}") { FacetRow(search, f, buckets) { b -> scope.launch { search.toggle(f, b) } } }
                            }
                        }
                    }
                    val transcripts = if (showTranscripts) answer.transcripts else emptyList()
                    if (answer.titles.isEmpty() && transcripts.isEmpty()) {
                        item(key = "none") { Note(stringResource(R.string.search_none, answer.q)) }
                    }
                    if (answer.titles.isNotEmpty()) {
                        item(key = "titles") {
                            TitlesRow(api, search, answer, if (focusFirst) firstResult else null, onOpen)
                        }
                    }
                    if (transcripts.isNotEmpty()) {
                        item(key = "transcripts") {
                            TranscriptsRow(api, search, answer, transcripts, if (focusFirst && answer.titles.isEmpty()) firstResult else null, onPlay)
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun Note(text: String) {
    Text(text, Modifier.padding(horizontal = Gutter).testTag("tv-search-note"), style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text)
}

@Composable
private fun RowTitle(text: String) {
    Text(
        text,
        Modifier.padding(horizontal = Gutter).semantics { heading() },
        style = MaterialTheme.typography.titleMedium,
        color = MaktabaPalette.Gold,
    )
}

/** The suggestions, a row of buttons: each its text and its kind (an older home's have none). */
@Composable
private fun Suggestions(items: List<Suggestion>, choose: (Suggestion) -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        RowTitle(stringResource(R.string.search_suggestions))
        LazyRow(contentPadding = PaddingValues(horizontal = Gutter), horizontalArrangement = Arrangement.spacedBy(12.dp), modifier = Modifier.testTag("tv-suggestions")) {
            itemsIndexed(items, key = { i, s -> "$i-${s.kindWire}-${s.text}" }) { i, s ->
                val kind = s.kind?.let { stringResource(kindText(it)) }
                MaktabaButton(onClick = { choose(s) }, modifier = Modifier.testTag("tv-suggestion-$i")) {
                    Text(s.text, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    if (kind != null) Text("  ·  $kind", maxLines = 1, style = MaterialTheme.typography.labelMedium)
                }
            }
        }
    }
}

private fun kindText(k: SuggestKind): Int = when (k) {
    SuggestKind.History -> R.string.search_kind_history
    SuggestKind.Term -> R.string.search_kind_term
    SuggestKind.Title -> R.string.search_kind_title
    SuggestKind.Speaker -> R.string.search_kind_speaker
}

/** "Did you mean …?": the corrected words are the button. */
@Composable
private fun DidYouMean(q: String, onClick: () -> Unit) {
    val (before, after) = stringResource(R.string.search_did_you_mean, "\u0000").split("\u0000").let { it[0] to it.getOrElse(1) { "" } }
    Row(Modifier.padding(horizontal = Gutter).testTag("tv-did-you-mean"), horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.CenterVertically) {
        if (before.isNotBlank()) Text(before.trim(), style = MaterialTheme.typography.titleMedium, color = MaktabaPalette.Text)
        MaktabaButton(onClick = onClick, modifier = Modifier.testTag("tv-did-you-mean-run")) { Text(q) }
        if (after.isNotBlank()) Text(after.trim(), style = MaterialTheme.typography.titleMedium, color = MaktabaPalette.Text)
    }
}

/** The order (three buttons, the current one ticked), Filters (opens the facets' rows) and Clear all filters. */
@Composable
private fun RefineRow(search: SearchModel, answer: SearchAnswer, filtersOpen: Boolean, onFilters: () -> Unit, onSort: (SearchSort) -> Unit, onClear: () -> Unit) {
    val hasFacets = Facet.entries.any { search.choice.shown(answer.facets, it).isNotEmpty() }
    LazyRow(
        contentPadding = PaddingValues(horizontal = Gutter),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier.testTag("tv-refine"),
    ) {
        item(key = "sort-label") { Text(stringResource(R.string.search_sort), style = MaterialTheme.typography.titleMedium, color = MaktabaPalette.Muted) }
        for (s in SearchSort.entries) {
            item(key = "sort-${s.wire}") {
                Chip(sortName(s), answer.sort == s, Modifier.testTag("tv-sort-${s.wire}")) { onSort(s) }
            }
        }
        if (hasFacets) {
            item(key = "filters") {
                val n = search.choice.count
                val label = if (n == 0) stringResource(R.string.search_filters) else stringResource(R.string.search_filters_count, n)
                Chip(label, filtersOpen, Modifier.testTag("tv-filters"), onFilters)
            }
        }
        if (!search.choice.isEmpty) {
            item(key = "clear") { MaktabaButton(onClick = onClear, modifier = Modifier.testTag("tv-filters-clear")) { Text(stringResource(R.string.search_filters_clear)) } }
        }
    }
}

/** A choice button: ticked when on. */
@Composable
private fun Chip(label: String, on: Boolean, modifier: Modifier = Modifier, onClick: () -> Unit) {
    MaktabaButton(onClick = onClick, modifier = modifier) { Text((if (on) "✓ " else "") + label, maxLines = 1) }
}

@Composable
private fun sortName(s: SearchSort): String = stringResource(
    when (s) {
        SearchSort.Relevance -> R.string.search_sort_relevance
        SearchSort.Newest -> R.string.search_sort_newest
        SearchSort.Matches -> R.string.search_sort_matches
    },
)

@Composable
private fun facetName(f: Facet): String = stringResource(
    when (f) {
        Facet.Library -> R.string.search_facet_library
        Facet.Language -> R.string.search_facet_language
        Facet.Speaker -> R.string.search_facet_speaker
        Facet.Year -> R.string.search_facet_year
        Facet.Duration -> R.string.search_facet_duration
    },
)

/** A bucket's name: a library's (or the one seen before), a language's, a year, a band's words, a speaker. */
@Composable
private fun bucketName(f: Facet, value: String, label: String?, libraries: Map<String, String>): String {
    val locale = LocalConfiguration.current.locales[0]
    return when (f) {
        Facet.Library -> label?.trim()?.takeIf { it.isNotEmpty() } ?: libraries[value] ?: value
        Facet.Language -> remember(value, locale) { languageName(value, locale) }
        Facet.Year -> value.toIntOrNull()?.let { String.format(locale, "%d", it) } ?: value
        Facet.Duration -> when (value) {
            "under_5m" -> stringResource(R.string.search_duration_under_5m, 5)
            "5m_20m" -> stringResource(R.string.search_duration_5m_20m, 5, 20)
            "20m_60m" -> stringResource(R.string.search_duration_20m_60m, 20, 60)
            "over_60m" -> stringResource(R.string.search_duration_over_60m)
            else -> value
        }
        Facet.Speaker -> value
    }
}

/** The filters in use, "✕ Facet: value" each; OK takes one off. */
@Composable
private fun ActiveFilters(search: SearchModel, onRemove: (Facet, String) -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        RowTitle(stringResource(R.string.search_filters_active))
        LazyRow(contentPadding = PaddingValues(horizontal = Gutter), horizontalArrangement = Arrangement.spacedBy(12.dp), modifier = Modifier.testTag("tv-active-filters")) {
            for ((f, v) in search.choice.chosen) {
                item(key = "${f.wire}:$v") {
                    val text = stringResource(R.string.search_filter_chip, facetName(f), bucketName(f, v, null, search.libraryNames))
                    MaktabaButton(onClick = { onRemove(f, v) }, modifier = Modifier.testTag("tv-active-${f.wire}-$v")) { Text("✕ $text", maxLines = 1) }
                }
            }
        }
    }
}

/** A facet's row: its name, then a button per bucket with its count, ticked when chosen. */
@Composable
private fun FacetRow(search: SearchModel, f: Facet, buckets: List<FacetBucket>, onToggle: (FacetBucket) -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        RowTitle(facetName(f))
        LazyRow(contentPadding = PaddingValues(horizontal = Gutter), horizontalArrangement = Arrangement.spacedBy(12.dp), modifier = Modifier.testTag("tv-facet-${f.wire}")) {
            for (b in buckets) {
                item(key = b.value) {
                    val name = bucketName(f, b.value, b.label, search.libraryNames)
                    val label = if (b.counted) stringResource(R.string.search_bucket_count, name, b.count) else name
                    Chip(label, search.choice.has(f, b.value), Modifier.testTag("tv-facet-${f.wire}-${b.value}")) { onToggle(b) }
                }
            }
        }
    }
}

/** Titles: a row of cards; the next page is read near its end. */
@Composable
private fun TitlesRow(api: TvApi, search: SearchModel, answer: SearchAnswer, first: FocusRequester?, onOpen: (String) -> Unit) {
    val scope = rememberCoroutineScope()
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        RowTitle(stringResource(R.string.search_titles))
        LazyRow(contentPadding = PaddingValues(horizontal = Gutter), horizontalArrangement = Arrangement.spacedBy(24.dp), modifier = Modifier.testTag("tv-search-titles")) {
            itemsIndexed(answer.titles, key = { _, h -> h.videoId }) { i, h: TitleHit ->
                if (i >= answer.titles.size - 4 && answer.moreTitles && !search.paging) LaunchedEffect(answer.titles.size) { scope.launch { search.moreTitles() } }
                MediaCard(
                    client = api.client,
                    video = h.videoId,
                    title = h.title,
                    subtitle = listOfNotNull(h.libraryName.takeIf { it.isNotBlank() }, h.durationSec?.takeIf { it > 0 }?.let { clock(it) }).joinToString(" · ").ifEmpty { null },
                    width = 240.dp,
                    cardModifier = Modifier.testTag("tv-title-${h.videoId}").then(if (i == 0 && first != null) Modifier.focusRequester(first) else Modifier),
                    onClick = { onOpen(h.videoId) },
                )
            }
        }
    }
}

/** In the transcript: a card per video, its first passage under the title; OK plays from that moment. */
@Composable
private fun TranscriptsRow(api: TvApi, search: SearchModel, answer: SearchAnswer, transcripts: List<TranscriptHit>, first: FocusRequester?, onPlay: (String, Double) -> Unit) {
    val scope = rememberCoroutineScope()
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        RowTitle(stringResource(R.string.search_transcripts))
        LazyRow(contentPadding = PaddingValues(horizontal = Gutter), horizontalArrangement = Arrangement.spacedBy(24.dp), modifier = Modifier.testTag("tv-search-transcripts")) {
            itemsIndexed(transcripts, key = { _, h -> "x-" + h.videoId }) { i, h ->
                if (i >= transcripts.size - 4 && answer.moreTranscripts && !search.paging) LaunchedEffect(transcripts.size) { scope.launch { search.moreTranscripts() } }
                val p = h.passages.firstOrNull()
                val at = p?.startSec ?: 0.0
                val line = p?.let { stringResource(R.string.search_play_from, clock(it.startSec)) + " · " + com.hamzalabs.maktaba.core.ui.isolated(it.snippet.replace("<mark>", "").replace("</mark>", "")) }
                MediaCard(
                    client = api.client,
                    video = h.videoId,
                    title = h.title,
                    subtitle = line,
                    width = 240.dp,
                    cardModifier = Modifier.testTag("tv-transcript-${h.videoId}").then(if (i == 0 && first != null) Modifier.focusRequester(first) else Modifier),
                    onClick = { onPlay(h.videoId, at) },
                )
            }
        }
    }
}
