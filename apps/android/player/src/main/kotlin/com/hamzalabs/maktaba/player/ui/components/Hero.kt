package com.hamzalabs.maktaba.player.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowForward
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import coil3.ImageLoader
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.MaktabaTokens
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.player.R

/**
 * Home's hero (FR-10, `featured`): the backdrop (the full poster), a line above the title (the
 * series, or "Featured" when an admin picked it), the title in Amiri, a line of facts, how far in
 * the person is, then **Resume** (when [resume]) or **Play**, and **Details**.
 *
 * The backdrop is 16:9 at the window's width, at most [maxBackdropHeight] tall on a tablet; the
 * words sit on a scrim that fades the picture into the page, so they keep their contrast over any
 * poster (NFR-8). Nothing moves: no parallax or scale (NFR-9). The box grows with the text at a
 * large font (NFR-7); the buttons wrap under each other when they no longer fit side by side.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun Hero(
    title: String,
    backdropUrl: String?,
    onPlay: () -> Unit,
    onDetails: () -> Unit,
    modifier: Modifier = Modifier,
    eyebrow: String? = null,
    meta: String? = null,
    resume: Boolean = false,
    progress: Float? = null,
    maxBackdropHeight: Dp = 420.dp,
    imageLoader: ImageLoader = homeImageLoader(),
) {
    val page = MaterialTheme.colorScheme.background
    BoxWithConstraints(modifier.fillMaxWidth().testTag("hero")) {
        val backdrop = (maxWidth / POSTER_ASPECT).coerceAtMost(maxBackdropHeight)
        Box(Modifier.fillMaxWidth().heightIn(min = backdrop)) {
            HomePicture(backdropUrl, Modifier.matchParentSize(), imageLoader = imageLoader)
            // the picture fades into the page under the words, in both themes
            Box(
                Modifier.matchParentSize().background(
                    Brush.verticalGradient(0f to Color.Transparent, 0.35f to page.copy(alpha = 0.35f), 0.75f to page.copy(alpha = 0.9f), 1f to page),
                ),
            )
            Column(
                Modifier
                    .align(Alignment.BottomStart)
                    .fillMaxWidth()
                    .padding(top = backdrop * 0.35f)
                    .padding(horizontal = MaktabaTokens.Spacing.s4, vertical = MaktabaTokens.Spacing.s4)
                    .widthIn(max = 720.dp),
                verticalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s2),
            ) {
                if (!eyebrow.isNullOrBlank()) {
                    Text(eyebrow, style = MaterialTheme.typography.labelLarge, color = MaktabaTheme.brand.accentText)
                }
                Text(
                    title,
                    Modifier.semantics { heading() },
                    style = MaterialTheme.typography.headlineLarge,
                    color = MaterialTheme.colorScheme.onBackground,
                )
                if (!meta.isNullOrBlank()) {
                    Text(meta, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                if (progress != null && progress > 0f) {
                    ProgressStrip(progress, Modifier.widthIn(max = 280.dp))
                }
                FlowRow(
                    Modifier.padding(top = MaktabaTokens.Spacing.s2),
                    horizontalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s3),
                    verticalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s2),
                ) {
                    Button(
                        onClick = onPlay,
                        modifier = Modifier.heightIn(min = 48.dp).testTag("hero-play"),
                        colors = ButtonDefaults.buttonColors(containerColor = MaktabaTokens.Dark.accent, contentColor = MaktabaTokens.Dark.accentFg),
                    ) {
                        Icon(Icons.Filled.PlayArrow, contentDescription = null, modifier = Modifier.size(20.dp))
                        Text(
                            stringResource(if (resume) R.string.components_resume else R.string.components_play),
                            Modifier.padding(start = MaktabaTokens.Spacing.s2),
                        )
                    }
                    OutlinedButton(onClick = onDetails, modifier = Modifier.heightIn(min = 48.dp).testTag("hero-details")) {
                        Text(stringResource(R.string.components_details), Modifier.padding(end = MaktabaTokens.Spacing.s2))
                        Icon(Icons.AutoMirrored.Filled.ArrowForward, contentDescription = null, modifier = Modifier.size(18.dp))
                    }
                }
            }
        }
    }
}

@Preview(name = "Hero", backgroundColor = 0xFF0B111C, showBackground = true, widthDp = 400)
@Composable
private fun HeroPreview() = MaktabaTheme(ThemeMode.Dark) {
    Hero("Lion of the Desert", backdropUrl = null, onPlay = {}, onDetails = {}, eyebrow = "Featured", meta = "1 h 30 min", resume = true, progress = 0.3f)
}
