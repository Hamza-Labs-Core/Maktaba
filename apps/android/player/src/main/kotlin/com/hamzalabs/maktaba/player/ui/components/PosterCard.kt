package com.hamzalabs.maktaba.player.ui.components

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import coil3.ImageLoader
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.VideoCard
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.MaktabaTokens
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.player.R

/** A poster's shape: a video frame, 16:9, as the web's card (`mkt-media-card__art`). */
const val POSTER_ASPECT = 16f / 9f

/** A card's width in a rail on a phone; a screen picks wider on a tablet. */
val PosterCardWidth: Dp = 168.dp

/**
 * A video's card (FR-11): its poster (the small copy, G4; the brand's colour with a film mark when
 * it has none), title, series name and how far in the person is; a watched video says so. Tap opens it; [onLongClick] (with [longClickLabel], e.g.
 * "Resume") is the card's context action. One node for TalkBack: "title, series, 40% watched",
 * role button, with the long-press action named.
 *
 * Titles wrap to two lines, and without a limit once the font is large (NFR-7: nothing is cut,
 * the row grows).
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun PosterCard(
    title: String,
    posterUrl: String?,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    subtitle: String? = null,
    progress: Float? = null,
    watched: Boolean = false,
    width: Dp = PosterCardWidth,
    onLongClick: (() -> Unit)? = null,
    longClickLabel: String? = null,
    imageLoader: ImageLoader = homeImageLoader(),
) {
    val pct = progress?.let { stringResource(R.string.components_watched_pct, watchedPercent(it)) }
    val watchedText = if (watched) stringResource(R.string.components_watched) else null
    val spoken = listOfNotNull(title, subtitle?.takeIf { it.isNotBlank() }, watchedText ?: pct).joinToString(", ")
    val large = LocalDensity.current.fontScale > LARGE_FONT
    val shape = RoundedCornerShape(MaktabaTokens.Radius.md)
    Column(
        modifier
            .width(width)
            .clip(shape)
            // the click, the long press (named) and the role are this node's; its words replace the children's
            .combinedClickable(role = Role.Button, onClick = onClick, onLongClick = onLongClick, onLongClickLabel = longClickLabel)
            .clearAndSetSemantics { contentDescription = spoken }
            .padding(bottom = MaktabaTokens.Spacing.s1),
        verticalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s1),
    ) {
        Box(Modifier.fillMaxWidth().aspectRatio(POSTER_ASPECT).clip(shape)) {
            HomePicture(posterUrl, Modifier.fillMaxWidth().aspectRatio(POSTER_ASPECT), imageLoader = imageLoader) {
                MissingPosterMark()
            }
            if (watched) {
                Text(
                    stringResource(R.string.components_watched),
                    Modifier
                        .align(Alignment.TopEnd)
                        .padding(MaktabaTokens.Spacing.s1)
                        .background(MaterialTheme.colorScheme.primary, RoundedCornerShape(MaktabaTokens.Radius.sm))
                        .padding(horizontal = MaktabaTokens.Spacing.s2, vertical = 2.dp),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onPrimary,
                )
            } else if (progress != null && progress > 0f) {
                ProgressStrip(
                    progress,
                    Modifier.align(Alignment.BottomStart).padding(horizontal = MaktabaTokens.Spacing.s2, vertical = MaktabaTokens.Spacing.s2),
                    track = MaktabaTokens.Dark.scrim,
                )
            }
        }
        Text(
            title,
            Modifier.padding(horizontal = 2.dp),
            style = MaterialTheme.typography.titleSmall,
            color = MaterialTheme.colorScheme.onSurface,
            maxLines = if (large) Int.MAX_VALUE else 2,
            overflow = TextOverflow.Ellipsis,
        )
        if (!subtitle.isNullOrBlank()) {
            Text(
                subtitle,
                Modifier.padding(horizontal = 2.dp),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = if (large) Int.MAX_VALUE else 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/** A [VideoCard] of a row as a [PosterCard]: its title, its series, its progress, its small poster. */
@Composable
fun PosterCard(
    card: VideoCard,
    client: ApiClient,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    width: Dp = PosterCardWidth,
    onLongClick: (() -> Unit)? = null,
    longClickLabel: String? = null,
) = PosterCard(
    title = card.displayTitle,
    posterUrl = posterUrl(client, card.id, PosterSize.Small),
    onClick = onClick,
    modifier = modifier,
    subtitle = card.series?.name,
    progress = card.watchedFraction?.toFloat(),
    watched = card.completed == true,
    width = width,
    onLongClick = onLongClick,
    longClickLabel = longClickLabel,
)

/** Above this font scale, titles wrap without a limit (NFR-7). */
internal const val LARGE_FONT = 1.3f

@Preview(name = "PosterCard", backgroundColor = 0xFF0B111C, showBackground = true)
@Composable
private fun PosterCardPreview() = MaktabaTheme(ThemeMode.Dark) {
    PosterCard("The Message", posterUrl = null, onClick = {}, subtitle = "Films", progress = 0.4f)
}
