package com.hamzalabs.maktaba.player.search

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.TrendingUp
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.History
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SearchBar
import androidx.compose.material3.SearchBarDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDirection
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.Passage
import com.hamzalabs.maktaba.core.api.RecentSearch
import com.hamzalabs.maktaba.core.api.TitleHit
import com.hamzalabs.maktaba.core.api.TranscriptHit
import com.hamzalabs.maktaba.core.ui.ErrorState
import com.hamzalabs.maktaba.core.ui.MaktabaTokens
import com.hamzalabs.maktaba.core.ui.Message
import com.hamzalabs.maktaba.core.ui.formatDuration
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.OnReselect
import com.hamzalabs.maktaba.player.shell.PlayerNavigator
import com.hamzalabs.maktaba.player.shell.Route
import com.hamzalabs.maktaba.player.ui.components.HomePicture
import com.hamzalabs.maktaba.player.ui.components.MissingPosterMark
import com.hamzalabs.maktaba.player.ui.components.POSTER_ASPECT
import com.hamzalabs.maktaba.player.ui.components.PosterSize
import com.hamzalabs.maktaba.player.ui.components.posterUrl
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** The widest the results grow on a tablet: a readable column, centred. */
private val ResultsMaxWidth = 840.dp

/** A result's poster width in its row. */
private val ThumbWidth = 128.dp

/**
 * Search (FR-19, FR-20): Material's search bar with suggestions as you type and your recent
 * searches; submit asks the home (`POST /api/search`, hybrid) and shows **Titles** and **In the
 * transcript**. A title opens the video's page; a transcript passage opens the player at its time.
 * The state is kept by the signed-in home, so another section and back finds the same results.
 */
@Composable
fun SearchScreen(nav: PlayerNavigator) {
    val home = LocalPlayerHome.current
    val model = home.keep("search") { SearchModel(home.client, home.session::signedOut) }
    SearchContent(model, home.client, nav)
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun SearchContent(model: SearchModel, client: ApiClient, nav: PlayerNavigator) {
    var expanded by rememberSaveable { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    OnReselect(nav) { expanded = false }
    LaunchedEffect(Unit) { model.loadRecent() }
    // suggestions follow the typing once it rests, only while the field is open
    LaunchedEffect(model.query, expanded) {
        if (expanded) {
            delay(SUGGEST_DELAY_MS)
            model.suggest(model.query)
        }
    }
    val submit: (String) -> Unit = { q ->
        if (q.isNotBlank()) {
            expanded = false
            scope.launch { model.submit(q) }
        }
    }
    val openVideo: (String) -> Unit = { nav.open(Route.Video(it)) }
    val play: (String, Passage) -> Unit = { id, p -> nav.open(Route.Watch(id, p.startSec.toInt())) }

    Column(Modifier.fillMaxSize().testTag("search"), horizontalAlignment = Alignment.CenterHorizontally) {
        SearchBar(
            inputField = {
                SearchBarDefaults.InputField(
                    query = model.query,
                    onQueryChange = { model.query = it },
                    onSearch = submit,
                    expanded = expanded,
                    onExpandedChange = { expanded = it },
                    modifier = Modifier.testTag("search-field"),
                    placeholder = { Text(stringResource(R.string.search_field_hint), maxLines = 1, overflow = TextOverflow.Ellipsis) },
                    leadingIcon = { Icon(Icons.Outlined.Search, contentDescription = null) },
                    trailingIcon = if (model.query.isNotEmpty()) {
                        {
                            IconButton(onClick = { model.query = "" }) {
                                Icon(Icons.Outlined.Close, stringResource(R.string.search_clear_query))
                            }
                        }
                    } else {
                        null
                    },
                )
            },
            expanded = expanded,
            onExpandedChange = { expanded = it },
        ) {
            LazyColumn(Modifier.fillMaxWidth().testTag("search-suggestions")) {
                if (model.query.isNotBlank()) {
                    items(model.suggestions, key = { "s-$it" }) { s ->
                        ListItem(
                            headlineContent = { Text(s) },
                            leadingContent = { Icon(Icons.AutoMirrored.Outlined.TrendingUp, contentDescription = null) },
                            colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerHigh),
                            modifier = Modifier.testTag("suggestion").clickable { submit(s) },
                        )
                    }
                } else {
                    recentItems(model, submit, onForget = { scope.launch { model.forget(it) } }, onClear = { scope.launch { model.clearRecent() } })
                }
            }
        }
        if (!expanded) {
            Box(Modifier.widthIn(max = ResultsMaxWidth).fillMaxSize()) {
                val answer = model.answer
                val problem = model.problem
                when {
                    model.searching && (answer == null || answer.q != model.submitted?.trim()) ->
                        Box(Modifier.fillMaxSize().testTag("search-loading"), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
                    problem != null -> ErrorState(problem, title = stringResource(R.string.search_failed), onRetry = { scope.launch { model.retry() } })
                    answer == null -> LazyColumn(Modifier.fillMaxSize()) {
                        if (model.recent.isEmpty()) {
                            item { Message(stringResource(R.string.search_empty_hint), Modifier.padding(top = 48.dp)) }
                        } else {
                            recentItems(model, submit, onForget = { scope.launch { model.forget(it) } }, onClear = { scope.launch { model.clearRecent() } })
                        }
                    }
                    answer.isEmpty -> Message(stringResource(R.string.search_no_results, answer.q), Modifier.testTag("search-none"))
                    else -> Results(
                        answer,
                        client,
                        paging = model.paging,
                        openVideo = openVideo,
                        play = play,
                        moreTitles = { scope.launch { model.moreTitles() } },
                        moreTranscripts = { scope.launch { model.moreTranscripts() } },
                    )
                }
            }
        }
    }
}

/** The recent searches under a header with Clear all; a tap runs one again, ✕ forgets it. */
private fun LazyListScope.recentItems(
    model: SearchModel,
    submit: (String) -> Unit,
    onForget: (RecentSearch) -> Unit,
    onClear: () -> Unit,
) {
    if (model.recent.isEmpty()) return
    item(key = "recent-header") {
        Row(
            Modifier.fillMaxWidth().padding(start = 16.dp, end = 8.dp, top = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            SectionHeading(stringResource(R.string.search_recent), Modifier.weight(1f))
            TextButton(onClick = onClear, modifier = Modifier.testTag("recent-clear")) { Text(stringResource(R.string.search_recent_clear)) }
        }
    }
    items(model.recent, key = { "r-${it.searchId}" }) { r ->
        ListItem(
            headlineContent = { Text(r.q) },
            supportingContent = { Text(stringResource(R.string.search_recent_counts, r.titleCount, r.transcriptCount)) },
            leadingContent = { Icon(Icons.Outlined.History, contentDescription = null) },
            trailingContent = {
                IconButton(onClick = { onForget(r) }, modifier = Modifier.testTag("recent-remove")) {
                    Icon(Icons.Outlined.Close, stringResource(R.string.search_recent_remove, r.q))
                }
            },
            colors = ListItemDefaults.colors(containerColor = androidx.compose.ui.graphics.Color.Transparent),
            modifier = Modifier.testTag("recent").clickable { submit(r.q) },
        )
    }
}

@Composable
private fun Results(
    answer: SearchAnswer,
    client: ApiClient,
    paging: Boolean,
    openVideo: (String) -> Unit,
    play: (String, Passage) -> Unit,
    moreTitles: () -> Unit,
    moreTranscripts: () -> Unit,
) {
    LazyColumn(Modifier.fillMaxSize().testTag("search-results")) {
        if (answer.degraded) {
            item(key = "degraded") {
                Text(
                    stringResource(R.string.search_degraded),
                    Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        if (answer.titles.isNotEmpty()) {
            item(key = "titles-header") { SectionHeading(stringResource(R.string.search_group_titles), Modifier.padding(16.dp).testTag("titles-header")) }
            items(answer.titles, key = { "t-${it.videoId}" }) { hit -> TitleRow(hit, client) { openVideo(hit.videoId) } }
            if (answer.moreTitles) item(key = "titles-more") { MoreButton(paging, "titles-more", moreTitles) }
        }
        if (answer.transcripts.isNotEmpty()) {
            item(key = "transcripts-header") {
                if (answer.titles.isNotEmpty()) HorizontalDivider(Modifier.padding(top = 8.dp))
                SectionHeading(stringResource(R.string.search_group_transcripts), Modifier.padding(16.dp).testTag("transcripts-header"))
            }
            items(answer.transcripts, key = { "x-${it.videoId}" }) { hit ->
                TranscriptBlock(hit, client, open = { openVideo(hit.videoId) }, play = { play(hit.videoId, it) })
            }
            if (answer.moreTranscripts) item(key = "transcripts-more") { MoreButton(paging, "transcripts-more", moreTranscripts) }
        }
        item(key = "end") { Spacer(Modifier.size(24.dp)) }
    }
}

@Composable
private fun SectionHeading(text: String, modifier: Modifier = Modifier) {
    Text(
        text,
        modifier.semantics { heading() },
        style = MaterialTheme.typography.titleMedium,
        color = MaterialTheme.colorScheme.primary,
    )
}

@Composable
private fun MoreButton(paging: Boolean, tag: String, onClick: () -> Unit) {
    Box(Modifier.fillMaxWidth().padding(8.dp), contentAlignment = Alignment.Center) {
        if (paging) {
            CircularProgressIndicator(Modifier.size(24.dp), strokeWidth = 2.dp)
        } else {
            TextButton(onClick = onClick, modifier = Modifier.testTag(tag)) { Text(stringResource(R.string.search_show_more)) }
        }
    }
}

/** A result's poster: the small copy, the brand's colour with a film mark when it has none. */
@Composable
private fun Thumb(client: ApiClient, videoId: String) {
    val shape = RoundedCornerShape(MaktabaTokens.Radius.sm)
    HomePicture(
        posterUrl(client, videoId, PosterSize.Small),
        Modifier.width(ThumbWidth).aspectRatio(POSTER_ASPECT).clip(shape),
    ) { MissingPosterMark() }
}

/** "Films · 1:30:00": the library and the length, what there is of them. */
private fun details(library: String, durationSec: Double?): String =
    listOfNotNull(library.takeIf { it.isNotBlank() }, durationSec?.takeIf { it > 0 }?.let(::formatDuration)).joinToString(" · ")

/** A video of Titles: poster, the title with the query's words marked, its library and length. One TalkBack node. */
@Composable
private fun TitleRow(hit: TitleHit, client: ApiClient, onClick: () -> Unit) {
    val sub = details(hit.libraryName, hit.durationSec)
    val title = hit.titleSnippet.ifBlank { hit.title }
    Row(
        Modifier
            .fillMaxWidth()
            .testTag("title-${hit.videoId}")
            .clickable(role = Role.Button, onClick = onClick)
            .clearAndSetSemantics {
                contentDescription = listOf(hit.title.ifBlank { plain(title) }, sub).filter { it.isNotBlank() }.joinToString(", ")
                role = Role.Button
            }
            .padding(horizontal = 16.dp, vertical = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Thumb(client, hit.videoId)
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(marked(title), style = MaterialTheme.typography.titleSmall, maxLines = 3, overflow = TextOverflow.Ellipsis)
            if (sub.isNotEmpty()) {
                Text(sub, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
    }
}

/** A video of In the transcript: its header (opens the page), then each passage (plays from its time). */
@Composable
private fun TranscriptBlock(hit: TranscriptHit, client: ApiClient, open: () -> Unit, play: (Passage) -> Unit) {
    val sub = listOf(details(hit.libraryName, hit.durationSec), stringResource(R.string.search_matches, hit.matches))
        .filter { it.isNotBlank() }.joinToString(" · ")
    Column(Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Row(
            Modifier
                .fillMaxWidth()
                .testTag("transcript-${hit.videoId}")
                .clickable(role = Role.Button, onClick = open)
                .clearAndSetSemantics {
                    contentDescription = "${hit.title}, $sub"
                    role = Role.Button
                }
                    .padding(horizontal = 16.dp, vertical = 8.dp),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Thumb(client, hit.videoId)
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(hit.title, style = MaterialTheme.typography.titleSmall, maxLines = 3, overflow = TextOverflow.Ellipsis)
                Text(sub, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
        hit.passages.forEachIndexed { i, p -> PassageRow(p, Modifier.testTag("passage-${hit.videoId}-$i")) { play(p) } }
    }
}

/** A matching line: its time and the passage, the query's words marked. Tap plays from the time. */
@Composable
private fun PassageRow(p: Passage, modifier: Modifier = Modifier, onClick: () -> Unit) {
    val time = formatDuration(p.startSec)
    val spoken = stringResource(R.string.search_play_from, time) + ": " + plain(p.snippet)
    Row(
        modifier
            .fillMaxWidth()
            .clickable(role = Role.Button, onClick = onClick)
            .clearAndSetSemantics {
                contentDescription = spoken
                role = Role.Button
            }
            .padding(start = 16.dp + ThumbWidth + 12.dp, end = 16.dp, top = 6.dp, bottom = 6.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalAlignment = Alignment.Top,
    ) {
        Row(
            Modifier
                .background(MaterialTheme.colorScheme.surfaceContainerHigh, RoundedCornerShape(MaktabaTokens.Radius.sm))
                .padding(horizontal = 6.dp, vertical = 2.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(Icons.Filled.PlayArrow, contentDescription = null, Modifier.size(14.dp), tint = MaterialTheme.colorScheme.primary)
            // a time reads left to right in Arabic too
            Text(time, style = MaterialTheme.typography.labelMedium.copy(textDirection = TextDirection.Ltr), color = MaterialTheme.colorScheme.primary)
        }
        Text(marked(p.snippet), Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium, maxLines = 3, overflow = TextOverflow.Ellipsis)
    }
}

@Composable
private fun marked(snippet: String): AnnotatedString {
    val color = MaterialTheme.colorScheme.primary
    return remember(snippet, color) { markedText(snippet, SpanStyle(color = color, fontWeight = FontWeight.Bold)) }
}

/**
 * The home's snippet as one text: it wraps the query's words in literal `<mark>…</mark>` (and
 * nothing else), shown in [highlight]. One run for the whole snippet, so an Arabic passage keeps
 * the space between a plain word and a marked one (the web's `renderSnippet` found this).
 */
internal fun markedText(snippet: String, highlight: SpanStyle): AnnotatedString = buildAnnotatedString {
    var inMark = false
    for (part in snippet.split(MARK)) {
        when (part) {
            "<mark>" -> inMark = true
            "</mark>" -> inMark = false
            "" -> Unit
            else -> if (inMark) withStyle(highlight) { append(part) } else append(part)
        }
    }
}

/** The snippet's words without the marks (what TalkBack reads). */
internal fun plain(snippet: String): String = snippet.replace("<mark>", "").replace("</mark>", "")

// splits keeping the tags as their own parts
private val MARK = Regex("(?=<mark>)|(?<=<mark>)|(?=</mark>)|(?<=</mark>)")
