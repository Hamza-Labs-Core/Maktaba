package com.hamzalabs.maktaba.desktop.libraries

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.PrimaryTabRow
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.VideoCard
import com.hamzalabs.maktaba.desktop.EmptyBox
import com.hamzalabs.maktaba.desktop.ErrorBox
import com.hamzalabs.maktaba.desktop.Loading
import com.hamzalabs.maktaba.desktop.LocalTexts
import com.hamzalabs.maktaba.desktop.PosterCard
import com.hamzalabs.maktaba.desktop.Rail
import com.hamzalabs.maktaba.desktop.formatDuration
import com.hamzalabs.maktaba.desktop.shell.DesktopHome
import com.hamzalabs.maktaba.desktop.shell.LocalDesktopHome
import com.hamzalabs.maktaba.desktop.shell.Route
import kotlinx.coroutines.launch
import java.util.Locale

/** A window this wide shows the filters as menus above the grid; narrower, as chips in a row. */
val FILTER_MENUS_WIDTH = 900.dp

/** A library's page (story 55.05); null is All videos. Kept while the home is signed in. */
@Composable
fun LibraryPage(id: String?) {
    val home = LocalDesktopHome.current
    val state = home.keep("library:${id ?: "all"}") { LibraryVideos(home.client, id, home::signedOut) }
    LaunchedEffect(state) { if (!state.loaded) state.open() }
    LibraryContent(state, home)
}

@Composable
fun LibraryContent(state: LibraryVideos, home: DesktopHome) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    if (state.notAvailable) {
        EmptyBox(t["lib.notAvailable"], t["lib.notAvailableDetail"], Modifier.testTag("not-available"))
        return
    }
    BoxWithConstraints(Modifier.fillMaxSize().testTag("library-page")) {
        val wide = maxWidth >= FILTER_MENUS_WIDTH
        Column(Modifier.fillMaxSize()) {
            Text(
                if (state.id == null) t["lib.allVideos"] else state.name,
                style = MaterialTheme.typography.headlineSmall,
                modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp).semantics { heading() },
            )
            if (state.id != null) {
                PrimaryTabRow(selectedTabIndex = state.tab.ordinal) {
                    Tab(state.tab == LibraryTab.Videos, { state.tab = LibraryTab.Videos }, text = { Text(t["lib.tab.videos"]) }, modifier = Modifier.testTag("tab-videos"))
                    Tab(state.tab == LibraryTab.Series, { state.tab = LibraryTab.Series }, text = { Text(t["lib.tab.series"]) }, modifier = Modifier.testTag("tab-series"))
                }
            }
            when (state.tab) {
                LibraryTab.Videos -> VideosTab(state, home, wide)
                LibraryTab.Series -> SeriesTab(state, home)
            }
        }
    }
}

@Composable
private fun HubRowsView(state: LibraryVideos, home: DesktopHome) {
    val t = LocalTexts.current
    val r = state.rows
    val open = { v: VideoCard -> home.nav.push(Route.Video(v.id)) }
    @Composable
    fun row(key: String, title: String, cards: List<VideoCard>) {
        if (cards.isEmpty()) return
        Rail(title, Modifier.testTag("hub-$key")) {
            items(cards, key = { it.id }) { v ->
                PosterCard(v.displayTitle, home.posters, v.id, onClick = { open(v) }, subtitle = v.series?.name, progress = v.watchedFraction?.toFloat(), watched = v.completed == true)
            }
        }
    }
    Column(verticalArrangement = Arrangement.spacedBy(16.dp)) {
        row("ondeck", t["home.row.onDeck"], r.onDeck)
        row("continue", t["home.row.continue"], r.continueWatching)
        row("added", t["home.row.recentlyAdded"], r.recentlyAdded)
        row("watched", t["home.row.recentlyWatched"], r.recentlyWatched)
        if (r.series.isNotEmpty()) {
            Rail(t["lib.tab.series"], Modifier.testTag("hub-series")) {
                items(r.series, key = { it.id }) { s ->
                    PosterCard(s.name.ifBlank { t["series.untitled"] }, home.posters, s.posterVideoId, onClick = { home.nav.push(Route.Series(s.id)) })
                }
            }
        }
    }
}

@Composable
private fun VideosTab(state: LibraryVideos, home: DesktopHome, wide: Boolean) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    val problem = state.problem
    LazyVerticalGrid(
        GridCells.Adaptive(220.dp),
        Modifier.fillMaxSize().testTag("videos"),
        contentPadding = PaddingValues(16.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        if (state.id != null) item(span = { GridItemSpan(maxLineSpan) }) { HubRowsView(state, home) }
        item(span = { GridItemSpan(maxLineSpan) }) { Filters(state, wide) }
        val filtered = state.language != null || state.speaker != null
        when {
            !state.loaded && problem != null -> item(span = { GridItemSpan(maxLineSpan) }) {
                ErrorBox(t["section.libraries"], problem, onRetry = { scope.launch { state.restart() } })
            }
            !state.loaded -> item(span = { GridItemSpan(maxLineSpan) }) { Loading() }
            state.videos.isEmpty() -> item(span = { GridItemSpan(maxLineSpan) }) { EmptyBox(if (filtered) t["lib.noMatch"] else t["lib.noVideos"]) }
            else -> {
                items(state.videos, key = { it.id }) { v ->
                    PosterCard(
                        v.displayTitle, home.posters, v.id,
                        onClick = { home.nav.push(Route.Video(v.id)) },
                        modifier = Modifier.testTag("video-${v.id}"),
                        subtitle = v.durationSec?.takeIf { it > 0 }?.let(::formatDuration),
                        progress = v.playbackState?.takeIf { !it.completed }?.let { p -> v.durationSec?.takeIf { it > 0 }?.let { (p.positionSec / it).toFloat() } },
                        watched = v.playbackState?.completed == true,
                        width = null,
                    )
                }
                if (state.next != null) item(span = { GridItemSpan(maxLineSpan) }) {
                    // more as the person reaches the end
                    LaunchedEffect(state.videos.size) { state.more() }
                    Loading()
                }
            }
        }
    }
}

/** A language code in this language's words ("ar" → "العربية"). */
private fun languageName(code: String, lang: String): String =
    Locale.forLanguageTag(code).getDisplayLanguage(Locale.forLanguageTag(lang)).takeIf { it.isNotBlank() } ?: code

@Composable
private fun Filters(state: LibraryVideos, wide: Boolean) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    val langs = state.facets.languages
    val speakers = state.facets.speakers
    val sortLabel = t[state.sort.key]
    val langLabel = state.language?.let { languageName(it, t.lang.code) } ?: t["lib.anyLanguage"]
    val speakerLabel = state.speaker ?: t["lib.anySpeaker"]
    if (wide) {
        FlowRow(horizontalArrangement = Arrangement.spacedBy(12.dp), modifier = Modifier.testTag("filters-menus")) {
            Menu("sort", "${t["lib.sort"]}: $sortLabel", LibrarySort.entries.map { t[it.key] to it }) { scope.launch { state.choose(sort = it) } }
            if (langs.isNotEmpty()) Menu("language", "${t["lib.language"]}: $langLabel", listOf(t["lib.anyLanguage"] to null) + langs.map { languageName(it.code, t.lang.code) to it.code }) {
                scope.launch { state.choose(language = it) }
            }
            if (speakers.isNotEmpty()) Menu("speaker", "${t["lib.speaker"]}: $speakerLabel", listOf(t["lib.anySpeaker"] to null) + speakers.map { it.name to it.name }) {
                scope.launch { state.choose(speaker = it) }
            }
        }
    } else {
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.testTag("filters-chips")) {
            for (s in LibrarySort.entries) FilterChip(state.sort == s, { scope.launch { state.choose(sort = s) } }, label = { Text(t[s.key]) }, modifier = Modifier.testTag("sort-${s.name}"))
            for (l in langs) FilterChip(state.language == l.code, { scope.launch { state.choose(language = if (state.language == l.code) null else l.code) } }, label = { Text(languageName(l.code, t.lang.code)) }, modifier = Modifier.testTag("language-${l.code}"))
            for (s in speakers) FilterChip(state.speaker == s.name, { scope.launch { state.choose(speaker = if (state.speaker == s.name) null else s.name) } }, label = { Text(s.name) })
        }
    }
}

@Composable
private fun <T> Menu(tag: String, label: String, options: List<Pair<String, T>>, onChoose: (T) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Box {
        OutlinedButton(onClick = { open = true }, modifier = Modifier.testTag("menu-$tag")) { Text(label) }
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            options.forEachIndexed { i, (text, v) -> DropdownMenuItem(text = { Text(text) }, onClick = { open = false; onChoose(v) }, modifier = Modifier.testTag("menu-$tag-$i")) }
        }
    }
}

@Composable
private fun SeriesTab(state: LibraryVideos, home: DesktopHome) {
    val t = LocalTexts.current
    LaunchedEffect(state) { if (!state.seriesLoaded) state.loadSeries() }
    when {
        !state.seriesLoaded -> Loading()
        state.series.isEmpty() -> EmptyBox(t["lib.seriesNone"])
        else -> LazyVerticalGrid(
            GridCells.Adaptive(200.dp),
            Modifier.fillMaxSize().testTag("series-grid"),
            contentPadding = PaddingValues(16.dp),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            items(state.series, key = { it.id }) { s ->
                PosterCard(
                    s.name.ifBlank { t["series.untitled"] }, home.posters, s.posterVideoId,
                    onClick = { home.nav.push(Route.Series(s.id)) },
                    modifier = Modifier.testTag("series-${s.id}"),
                    subtitle = t["lib.seriesProgress", s.watched, s.episodes],
                    progress = if (s.episodes > 0) s.watched.toFloat() / s.episodes else null,
                    width = null,
                )
            }
            if (state.moreSeries) item(span = { GridItemSpan(maxLineSpan) }) {
                LaunchedEffect(state.series.size) { state.loadSeries() }
                Loading()
            }
        }
    }
}

/** A page that scrolls as one column. */
@Composable
internal fun PageColumn(modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    Column(modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) { content() }
}
