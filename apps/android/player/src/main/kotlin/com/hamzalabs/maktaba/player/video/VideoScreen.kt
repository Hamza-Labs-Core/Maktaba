package com.hamzalabs.maktaba.player.video

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Favorite
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Replay
import androidx.compose.material.icons.outlined.CheckCircle
import androidx.compose.material.icons.outlined.FavoriteBorder
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.adaptive.currentWindowAdaptiveInfoV2
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalLocale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.window.core.layout.WindowSizeClass
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.Chapter
import com.hamzalabs.maktaba.core.api.Episode
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.api.MyVideo
import com.hamzalabs.maktaba.core.api.NotAvailable
import com.hamzalabs.maktaba.core.api.PlaybackState
import com.hamzalabs.maktaba.core.api.Segment
import com.hamzalabs.maktaba.core.api.SeriesName
import com.hamzalabs.maktaba.core.api.VideoDetail
import com.hamzalabs.maktaba.core.api.chapters
import com.hamzalabs.maktaba.core.api.markWatched
import com.hamzalabs.maktaba.core.api.myVideo
import com.hamzalabs.maktaba.core.api.segments
import com.hamzalabs.maktaba.core.api.seriesEpisodes
import com.hamzalabs.maktaba.core.api.setFavorite
import com.hamzalabs.maktaba.core.api.videoDetail
import com.hamzalabs.maktaba.core.api.watchedFraction
import com.hamzalabs.maktaba.core.ui.ErrorState
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.MaktabaTokens
import com.hamzalabs.maktaba.core.ui.Message
import com.hamzalabs.maktaba.core.ui.formatDuration
import com.hamzalabs.maktaba.player.BackButton
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerNavigator
import com.hamzalabs.maktaba.player.shell.Route
import com.hamzalabs.maktaba.player.ui.components.PosterCard
import com.hamzalabs.maktaba.player.ui.components.PosterSize
import com.hamzalabs.maktaba.player.ui.components.ProgressStrip
import com.hamzalabs.maktaba.player.ui.components.Rail
import com.hamzalabs.maktaba.player.ui.components.posterUrl
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.launch
import java.time.LocalDate
import java.time.OffsetDateTime
import java.time.format.DateTimeFormatter
import java.time.format.DecimalStyle
import java.time.format.FormatStyle
import java.util.Locale
import com.hamzalabs.maktaba.core.R as CoreR

/** How many lines of the transcript the page shows (FR-18's excerpt). */
internal const val TRANSCRIPT_LINES = 5

/** How many of a series' next episodes the page offers. */
internal const val NEXT_EPISODES = 10

/**
 * A video's page as it loads (FR-18): the video (`GET /api/videos/{id}`, with the caller's
 * `playback_state`), the caller's own state (`GET /api/me/videos/{id}`: favorite, watched, the
 * series), its chapters, a transcript excerpt and, in a series, the next episodes. Only the video
 * itself is required: a 404 is [notAvailable] (a video the person may not see answers 404 too,
 * so the page cannot tell them apart and says the same); the rest is left out when it fails.
 */
@Stable
class VideoPage(private val client: ApiClient, val id: String) {
    var video by mutableStateOf<VideoDetail?>(null)
        private set
    var series by mutableStateOf<SeriesName?>(null)
        private set
    /** This video as its series lists it (its season and episode). */
    var episode by mutableStateOf<Episode?>(null)
        private set
    var chapters by mutableStateOf<List<Chapter>>(emptyList())
        private set
    var transcript by mutableStateOf<List<Segment>>(emptyList())
        private set
    var nextEpisodes by mutableStateOf<List<Episode>>(emptyList())
        private set
    var favorite by mutableStateOf(false)
        private set
    var watched by mutableStateOf(false)
        private set
    var loading by mutableStateOf(true)
        private set
    var notAvailable by mutableStateOf(false)
        private set
    var problem by mutableStateOf<Failure?>(null)
        private set

    /** Where Resume starts, in seconds: the saved position, unless watched or not started. */
    val resumeAtSec: Double? get() = if (watched) null else video?.resumeAtSec

    suspend fun load() {
        loading = true
        problem = null
        try {
            coroutineScope {
                val detail = async { client.videoDetail(id) }
                val mine = async { optional { client.myVideo(id) } }
                val chapters = async { optional { client.chapters(id) }.orEmpty() }
                val lines = async { optional { client.segments(id, limit = TRANSCRIPT_LINES).items }.orEmpty() }
                val v = detail.await()
                val m: MyVideo? = mine.await()
                video = v
                notAvailable = false
                favorite = m?.favorite ?: (v.favorite == true)
                watched = m?.completed ?: (v.playbackState?.completed == true)
                series = m?.series
                this@VideoPage.chapters = chapters.await().sortedBy { it.startSec }
                transcript = lines.await().take(TRANSCRIPT_LINES)
                m?.series?.let { s ->
                    val all = optional { client.seriesEpisodes(s.id) }?.seasons?.flatMap { it.episodes }.orEmpty()
                    val at = all.indexOfFirst { it.videoId == id }
                    episode = all.getOrNull(at)
                    nextEpisodes = if (at < 0) emptyList() else all.drop(at + 1).take(NEXT_EPISODES)
                }
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            if (e is NotAvailable) notAvailable = true else problem = Failure.of(e)
        } finally {
            loading = false
        }
    }

    /** Favorite on or off (`POST /api/me/favorites`); false when the home did not take it (the toggle goes back). */
    suspend fun toggleFavorite(): Boolean {
        val want = !favorite
        favorite = want
        return saved { client.setFavorite(listOf(id), want) }.also { if (!it) favorite = !want }
    }

    /**
     * Watched on or off (`POST /api/me/watched`); the home starts the video over either way
     * (position 0), so Resume goes. False when the home did not take it.
     */
    suspend fun toggleWatched(): Boolean {
        val want = !watched
        val before = video
        watched = want
        video = before?.copy(playbackState = PlaybackState(positionSec = 0.0, completed = want))
        return saved { client.markWatched(listOf(id), want) }.also {
            if (!it) {
                watched = !want
                video = before
            }
        }
    }

    private suspend fun saved(block: suspend () -> Unit): Boolean = try {
        block()
        true
    } catch (e: CancellationException) {
        throw e
    } catch (_: Exception) {
        false
    }

    private suspend fun <T> optional(block: suspend () -> T): T? = try {
        block()
    } catch (e: CancellationException) {
        throw e
    } catch (_: Exception) {
        null
    }
}

/**
 * A video's page (FR-18): the poster at its own shape, the series and episode, the title, length
 * and date, **Resume** from `playback_state` with **Play from start**, or **Play**; **Favorite**
 * and **Mark watched**; the description; **Chapters** (each plays from its time); a transcript
 * excerpt; in a series, the next episodes. A 404 says "not available" (deep links land here too,
 * `maktaba://video/{id}`, [VideoLink]).
 *
 * The poster is the small copy (`?size=small`) in a compact window (a phone) and the full one in
 * a wider window (a foldable open, a tablet), as 48.19 on iPhone and iPad.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun VideoScreen(id: String, nav: PlayerNavigator) {
    val home = LocalPlayerHome.current
    val client = home.client
    val page = remember(client, id) { VideoPage(client, id) }
    val scope = rememberCoroutineScope()
    val snackbar = remember { SnackbarHostState() }
    val saveFailed = stringResource(R.string.video_save_failed)
    LaunchedEffect(page) { page.load() }

    Scaffold(
        modifier = Modifier.testTag("video-page"),
        topBar = {
            TopAppBar(
                title = {},
                navigationIcon = { if (nav.canGoBack) BackButton { nav.back() } },
            )
        },
        snackbarHost = { SnackbarHost(snackbar) },
    ) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            val video = page.video
            when {
                page.notAvailable -> Message(
                    stringResource(R.string.video_not_available),
                    Modifier.testTag("video-not-available"),
                    detail = stringResource(R.string.video_not_available_detail),
                )
                page.problem != null -> ErrorState(
                    page.problem!!,
                    title = stringResource(CoreR.string.error_title),
                    video = true,
                    onRetry = { scope.launch { page.load() } },
                    onBack = if (nav.canGoBack) ({ nav.back() }) else null,
                )
                video == null -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
                else -> VideoDetails(
                    page = page,
                    video = video,
                    client = client,
                    onOpen = { nav.open(it) },
                    onFavorite = { scope.launch { if (!page.toggleFavorite()) snackbar.showSnackbar(saveFailed) } },
                    onWatched = { scope.launch { if (!page.toggleWatched()) snackbar.showSnackbar(saveFailed) } },
                )
            }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun VideoDetails(
    page: VideoPage,
    video: VideoDetail,
    client: ApiClient,
    onOpen: (Route) -> Unit,
    onFavorite: () -> Unit,
    onWatched: () -> Unit,
) {
    val wide = currentWindowAdaptiveInfoV2().windowSizeClass.isWidthAtLeastBreakpoint(WindowSizeClass.WIDTH_DP_MEDIUM_LOWER_BOUND)
    val posterHeight = (LocalConfiguration.current.screenHeightDp * if (wide) 0.6f else 0.55f).dp.coerceIn(200.dp, 640.dp)
    val gutter = MaktabaTokens.Spacing.s4
    Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).testTag("video-details"),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Column(
            Modifier.widthIn(max = 840.dp).fillMaxWidth().padding(bottom = MaktabaTokens.Spacing.s8),
            verticalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s3),
        ) {
            VideoPoster(
                title = video.displayTitle,
                url = posterUrl(client, video.id, if (wide) PosterSize.Full else PosterSize.Small),
                maxHeight = posterHeight,
                modifier = Modifier.padding(horizontal = gutter),
            )

            // the series and episode, the title, the facts
            Column(Modifier.padding(horizontal = gutter), verticalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s1)) {
                val s = page.series
                if (s != null) {
                    val ep = page.episode?.let { episodeLabel(it) }
                    Text(
                        listOfNotNull(s.name.takeIf { it.isNotBlank() }, ep).joinToString(" · "),
                        Modifier.clickable { onOpen(Route.Series(s.id)) }.testTag("video-series"),
                        style = MaterialTheme.typography.labelLarge,
                        color = MaktabaTheme.brand.accentText,
                    )
                }
                Text(
                    video.displayTitle,
                    Modifier.semantics { heading() }.testTag("video-title"),
                    style = MaterialTheme.typography.headlineMedium,
                    color = MaterialTheme.colorScheme.onBackground,
                )
                val facts = listOfNotNull(video.durationSec?.takeIf { it > 0 }?.let(::formatDuration), dateLabel(video.publishedAt ?: video.createdAt))
                if (facts.isNotEmpty()) {
                    Text(facts.joinToString(" · "), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }

            // Resume (and Play from start), or Play
            val resumeAt = page.resumeAtSec
            Column(Modifier.padding(horizontal = gutter), verticalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s2)) {
                if (resumeAt != null) {
                    watchedFraction(resumeAt, video.durationSec, false)?.let { ProgressStrip(it.toFloat(), Modifier.widthIn(max = 320.dp).fillMaxWidth()) }
                }
                FlowRow(horizontalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s3), verticalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s2)) {
                    if (resumeAt != null) {
                        Button(onClick = { onOpen(Route.Watch(video.id, resumeAt.toInt())) }, Modifier.heightIn(min = 48.dp).testTag("video-resume")) {
                            Icon(Icons.Filled.PlayArrow, null, Modifier.size(20.dp))
                            Text(stringResource(R.string.video_resume_at, formatDuration(resumeAt)), Modifier.padding(start = MaktabaTokens.Spacing.s2))
                        }
                        OutlinedButton(onClick = { onOpen(Route.Watch(video.id, 0)) }, Modifier.heightIn(min = 48.dp).testTag("video-play-start")) {
                            Icon(Icons.Filled.Replay, null, Modifier.size(20.dp))
                            Text(stringResource(R.string.video_play_from_start), Modifier.padding(start = MaktabaTokens.Spacing.s2))
                        }
                    } else {
                        Button(onClick = { onOpen(Route.Watch(video.id, 0)) }, Modifier.heightIn(min = 48.dp).testTag("video-play")) {
                            Icon(Icons.Filled.PlayArrow, null, Modifier.size(20.dp))
                            Text(stringResource(R.string.video_play), Modifier.padding(start = MaktabaTokens.Spacing.s2))
                        }
                    }
                }
                FlowRow(horizontalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s2)) {
                    FilterChip(
                        selected = page.favorite,
                        onClick = onFavorite,
                        label = { Text(stringResource(R.string.video_favorite)) },
                        leadingIcon = { Icon(if (page.favorite) Icons.Filled.Favorite else Icons.Outlined.FavoriteBorder, null, Modifier.size(18.dp)) },
                        modifier = Modifier.heightIn(min = 48.dp).testTag("video-favorite"),
                    )
                    FilterChip(
                        selected = page.watched,
                        onClick = onWatched,
                        label = { Text(stringResource(R.string.video_mark_watched)) },
                        leadingIcon = { Icon(if (page.watched) Icons.Filled.CheckCircle else Icons.Outlined.CheckCircle, null, Modifier.size(18.dp)) },
                        modifier = Modifier.heightIn(min = 48.dp).testTag("video-watched"),
                    )
                }
            }

            video.description?.takeIf { it.isNotBlank() }?.let {
                Text(it, Modifier.padding(horizontal = gutter), style = MaterialTheme.typography.bodyLarge, color = MaterialTheme.colorScheme.onSurface)
            }

            if (page.chapters.isNotEmpty()) {
                Section(stringResource(R.string.video_chapters))
                Column {
                    page.chapters.forEach { c ->
                        TimedLine(
                            time = c.startSec,
                            text = c.title.ifBlank { formatDuration(c.startSec) },
                            onClick = { onOpen(Route.Watch(video.id, c.startSec.toInt())) },
                            modifier = Modifier.testTag("chapter-${c.seq}"),
                        )
                    }
                }
            }

            if (page.transcript.isNotEmpty()) {
                Section(stringResource(R.string.video_transcript))
                Column {
                    page.transcript.forEach { s ->
                        TimedLine(
                            time = s.startSec,
                            text = s.text,
                            onClick = { onOpen(Route.Watch(video.id, s.startSec.toInt())) },
                            modifier = Modifier.testTag("segment-${s.seq}"),
                        )
                    }
                }
            }

            if (page.nextEpisodes.isNotEmpty()) {
                Rail(stringResource(R.string.video_next_episodes), Modifier.testTag("video-next")) {
                    items(page.nextEpisodes, key = { it.videoId }) { e ->
                        PosterCard(
                            title = e.title.ifBlank { episodeLabel(e) ?: e.videoId },
                            posterUrl = posterUrl(client, e.videoId, PosterSize.Small),
                            onClick = { onOpen(Route.Video(e.videoId)) },
                            subtitle = episodeLabel(e),
                            progress = (e.progressPct / 100.0).toFloat().takeIf { it > 0f },
                            watched = e.watched,
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun Section(title: String) {
    Text(
        title,
        Modifier.padding(start = MaktabaTokens.Spacing.s4, end = MaktabaTokens.Spacing.s4, top = MaktabaTokens.Spacing.s4).semantics { heading() },
        style = MaterialTheme.typography.titleMedium,
        color = MaterialTheme.colorScheme.onBackground,
    )
}

/** A line that plays from [time]: a chapter, a transcript line. */
@Composable
private fun TimedLine(time: Double, text: String, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Row(
        modifier
            .fillMaxWidth()
            .heightIn(min = 48.dp)
            .clickable(onClickLabel = stringResource(R.string.video_play_from, formatDuration(time)), onClick = onClick)
            .padding(horizontal = MaktabaTokens.Spacing.s4, vertical = MaktabaTokens.Spacing.s2),
        horizontalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s3),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(formatDuration(time), style = MaterialTheme.typography.labelLarge, color = MaktabaTheme.brand.accentText)
        Text(text, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurface)
    }
}

/** "Season 1, Episode 3", or "Episode 3"; null when the series does not number it. */
@Composable
private fun episodeLabel(e: Episode): String? {
    val n = e.episode ?: e.absoluteNumber ?: return null
    val season = e.season
    return if (season != null && season > 0) stringResource(R.string.video_season_episode, season, n) else stringResource(R.string.video_episode, n)
}

/** A date from the home (RFC 3339 or a plain date) in the app's language, or null. */
@Composable
private fun dateLabel(text: String?): String? {
    val locale = LocalLocale.current.platformLocale
    return formatDate(text, locale)
}

/** [text] (RFC 3339 or `yyyy-MM-dd`) as [locale] writes a medium date, or null when it is not a date. */
internal fun formatDate(text: String?, locale: Locale): String? {
    val t = text?.trim()?.takeIf { it.isNotEmpty() } ?: return null
    val date = runCatching { OffsetDateTime.parse(t).toLocalDate() }.getOrNull()
        ?: runCatching { LocalDate.parse(t.take(10)) }.getOrNull()
        ?: return null
    return DateTimeFormatter.ofLocalizedDate(FormatStyle.MEDIUM).withLocale(locale).withDecimalStyle(DecimalStyle.of(locale)).format(date)
}
