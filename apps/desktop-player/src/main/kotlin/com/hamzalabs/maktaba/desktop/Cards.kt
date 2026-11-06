package com.hamzalabs.maktaba.desktop

import androidx.compose.foundation.ContextMenuArea
import androidx.compose.foundation.ContextMenuItem
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.focusable
import androidx.compose.foundation.hoverable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.interaction.collectIsHoveredAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.toComposeImageBitmap
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.errorKind
import org.jetbrains.skia.Image as SkiaImage

// The pieces every page of the desktop Player is built from (story 55.01): posters, cards that
// take a click, Enter and show a brass border on hover and on keyboard focus, rows that scroll
// sideways, and the loading, error and empty states.

/** Posters already fetched, a few hundred at most (pages are browsed a part at a time). */
class Posters(private val client: ApiClient?, private val max: Int = 300) {
    private val cache = object : LinkedHashMap<String, ImageBitmap?>(64, 0.75f, true) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, ImageBitmap?>) = size > max
    }

    suspend fun get(video: String): ImageBitmap? {
        val c = client ?: return null
        synchronized(cache) { if (cache.containsKey(video)) return cache[video] }
        val img = runCatching { SkiaImage.makeFromEncoded(c.poster(video)).toComposeImageBitmap() }.getOrNull()
        synchronized(cache) { cache[video] = img }
        return img
    }
}

/** [video]'s poster filling its box (nothing while it loads, or when there is none). */
@Composable
fun PosterImage(posters: Posters, video: String?, modifier: Modifier = Modifier) {
    val poster by produceState<ImageBitmap?>(null, video) { value = video?.let { posters.get(it) } }
    Box(modifier.background(MaterialTheme.colorScheme.surfaceContainerHigh)) {
        poster?.let { Image(it, contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize()) }
    }
}

/**
 * Click and Enter run [onClick]; the border is brass on hover and on keyboard focus (big
 * screen's arrows move the focus between cards, Epic 46).
 */
@Composable
fun Modifier.choosable(onClick: () -> Unit, shape: RoundedCornerShape = RoundedCornerShape(10.dp)): Modifier {
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val hovered by interaction.collectIsHoveredAsState()
    val lit = focused || hovered
    return this
        .clip(shape)
        .border(if (lit) 3.dp else 0.dp, if (lit) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.surface.copy(alpha = 0f), shape)
        .onKeyEvent {
            if (it.type == KeyEventType.KeyUp && (it.key == Key.Enter || it.key == Key.NumPadEnter)) {
                onClick()
                true
            } else false
        }
        .hoverable(interaction)
        .clickable(interactionSource = interaction, indication = null, onClick = onClick)
        .focusable(interactionSource = interaction)
        .semantics { role = Role.Button }
}

/** How far in a person is, as a thin brass strip under a poster. */
@Composable
fun ProgressStrip(progress: Float, modifier: Modifier = Modifier) {
    LinearProgressIndicator(progress = { progress.coerceIn(0f, 1f) }, modifier = modifier.fillMaxWidth().height(4.dp))
}

/** A menu item of a card's right-click menu (and its More options button). */
class CardAction(val label: String, val onClick: () -> Unit)

/**
 * A video's or a series' card: its poster (16:9, or 2:3 when [tall]), a progress strip, the
 * title and a line under it. [actions] are its right-click menu.
 */
@Composable
fun PosterCard(
    title: String,
    posters: Posters,
    posterVideo: String?,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    subtitle: String? = null,
    progress: Float? = null,
    watched: Boolean = false,
    tall: Boolean = false,
    width: Dp? = 220.dp,
    actions: List<CardAction> = emptyList(),
) {
    val card = @Composable {
        Column((if (width != null) modifier.width(width) else modifier).choosable(onClick).padding(4.dp)) {
            Box(Modifier.fillMaxWidth().aspectRatio(if (tall) 2f / 3f else 16f / 9f).clip(RoundedCornerShape(8.dp))) {
                PosterImage(posters, posterVideo, Modifier.fillMaxSize())
                if (watched) {
                    Icon(
                        Icons.Filled.CheckCircle, LocalTexts.current["series.watched"],
                        tint = MaterialTheme.colorScheme.primary,
                        modifier = Modifier.align(Alignment.TopEnd).padding(6.dp),
                    )
                }
                if (progress != null && progress > 0f) ProgressStrip(progress, Modifier.align(Alignment.BottomCenter))
            }
            Text(title, maxLines = 2, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(top = 6.dp))
            subtitle?.let {
                Text(it, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
    }
    if (actions.isEmpty()) card() else ContextMenuArea(items = { actions.map { ContextMenuItem(it.label, it.onClick) } }) { card() }
}

/** A titled row of cards that scrolls sideways (from the right in Arabic), with See all. */
@Composable
fun Rail(title: String, modifier: Modifier = Modifier, onSeeAll: (() -> Unit)? = null, content: LazyListScope.() -> Unit) {
    val t = LocalTexts.current
    Column(modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(title, style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f).semantics { heading() })
            if (onSeeAll != null) TextButton(onClick = onSeeAll) { Text(t["home.seeAll"]) }
        }
        LazyRow(
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            contentPadding = PaddingValues(horizontal = 16.dp),
            content = content,
        )
    }
}

/** A page's loading state. */
@Composable
fun Loading(modifier: Modifier = Modifier) {
    Box(modifier.fillMaxWidth().padding(32.dp).testTag("loading"), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
}

/** A page that could not load: [title], what went wrong, and Try again. */
@Composable
fun ErrorBox(title: String, error: Throwable?, onRetry: (() -> Unit)?, modifier: Modifier = Modifier) {
    val t = LocalTexts.current
    Column(
        modifier.fillMaxWidth().padding(32.dp).testTag("error"),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text(title, style = MaterialTheme.typography.titleMedium, textAlign = TextAlign.Center)
        error?.let { Text(t.sentenceFor(it), color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = TextAlign.Center) }
        if (onRetry != null) Button(onClick = onRetry) { Text(t["common.retry"]) }
    }
}

/** A page with nothing in it: [title] and why. */
@Composable
fun EmptyBox(title: String, detail: String? = null, modifier: Modifier = Modifier) {
    Column(
        modifier.fillMaxWidth().padding(32.dp).testTag("empty"),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Text(title, style = MaterialTheme.typography.titleMedium, textAlign = TextAlign.Center)
        detail?.let { Text(it, color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = TextAlign.Center) }
    }
}

/** What went wrong, in a sentence of this language. */
fun Texts.sentenceFor(e: Throwable): String = sentence(e.errorKind())

/** 1:02:03 or 4:05, read left to right in either language. */
fun formatDuration(seconds: Double): String {
    val s = seconds.toLong().coerceAtLeast(0)
    val h = s / 3600
    val m = (s % 3600) / 60
    val sec = s % 60
    val text = if (h > 0) "%d:%02d:%02d".format(h, m, sec) else "%d:%02d".format(m, sec)
    return "⁦$text⁩"
}

/** A gap of [dp] between blocks of a page. */
@Composable
fun Gap(dp: Dp = 16.dp) = Box(Modifier.height(dp))
