package com.hamzalabs.maktaba.player.video

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.SemanticsPropertyReceiver
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.min
import coil3.ImageLoader
import coil3.compose.AsyncImage
import coil3.request.ImageRequest
import coil3.size.Precision
import coil3.size.Scale
import coil3.size.Size
import com.hamzalabs.maktaba.core.ui.MaktabaTokens
import com.hamzalabs.maktaba.player.ui.components.POSTER_ASPECT
import com.hamzalabs.maktaba.player.ui.components.PictureState
import com.hamzalabs.maktaba.player.ui.components.brandFill
import com.hamzalabs.maktaba.player.ui.components.homeImageLoader

/** What the page's poster is doing, for tests: loading, shown or missing (a 404, no poster). */
val PosterStateKey = SemanticsPropertyKey<PictureState>("VideoPosterState")
private var SemanticsPropertyReceiver.posterState by PosterStateKey

/**
 * A video page's poster (FR-18, the owner's TestFlight report on 0.3.44: "no thumbnail on video
 * page"): at the top of the page, **at its own shape**. A portrait video's poster stays portrait
 * (never cropped to 16:9): the box takes the picture's own width-to-height once it decodes, as wide
 * as the page allows and at most [maxHeight] tall, centred.
 *
 * Until it decodes the box is 16:9, quiet; with no poster (a 404, the home unreachable) the box
 * stays 16:9 and shows [title] on the brand's colour, so the page never opens on a hole.
 *
 * The picture is decoded at the box's largest size ([maxHeight] by the page's width, NFR-4),
 * scaled to fit, never cropped. Decorative for TalkBack: the page's title says what it is.
 */
@Composable
fun VideoPoster(
    title: String,
    url: String?,
    maxHeight: Dp,
    modifier: Modifier = Modifier,
    imageLoader: ImageLoader = homeImageLoader(),
) {
    var state by remember(url) { mutableStateOf(if (url == null) PictureState.Missing else PictureState.Loading) }
    var aspect by remember(url) { mutableStateOf<Float?>(null) }
    val shape = RoundedCornerShape(MaktabaTokens.Radius.lg)
    BoxWithConstraints(modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
        val ratio = if (state == PictureState.Shown) aspect ?: POSTER_ASPECT else POSTER_ASPECT
        // as wide as the page, unless that would be taller than maxHeight (a portrait poster)
        val pageWidth = maxWidth
        val width = min(pageWidth, maxHeight * ratio)
        Box(
            Modifier
                .width(width)
                .aspectRatio(ratio)
                .clip(shape)
                .testTag("video-poster")
                .semantics { posterState = state },
        ) {
            when (state) {
                PictureState.Missing -> Box(
                    Modifier.fillMaxSize().background(brandFill()).testTag("video-poster-missing"),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(
                        title,
                        Modifier.padding(MaktabaTokens.Spacing.s6),
                        style = MaterialTheme.typography.headlineSmall,
                        color = MaktabaTokens.Brand.brand100,
                        textAlign = TextAlign.Center,
                    )
                }
                else -> Box(Modifier.fillMaxSize().background(MaterialTheme.colorScheme.surfaceVariant))
            }
            if (url != null && state != PictureState.Missing) {
                val context = LocalContext.current
                val density = LocalDensity.current
                val px = with(density) { Size(pageWidth.roundToPx().coerceAtLeast(1), maxHeight.roundToPx().coerceAtLeast(1)) }
                val request = remember(url, px) {
                    ImageRequest.Builder(context).data(url).size(px).scale(Scale.FIT).precision(Precision.INEXACT).build()
                }
                AsyncImage(
                    model = request,
                    contentDescription = null,
                    imageLoader = imageLoader,
                    contentScale = ContentScale.Fit,
                    modifier = Modifier.fillMaxSize(),
                    onSuccess = { s ->
                        val size = s.painter.intrinsicSize
                        aspect = if (size.width > 0f && size.height > 0f) size.width / size.height else null
                        state = PictureState.Shown
                    },
                    onError = { state = PictureState.Missing },
                )
            }
        }
    }
}
