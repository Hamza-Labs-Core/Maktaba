package com.hamzalabs.maktaba.player.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Movie
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import coil3.ImageLoader
import coil3.compose.AsyncImage
import coil3.compose.AsyncImagePainter
import coil3.request.ImageRequest
import com.hamzalabs.maktaba.core.ui.MaktabaTokens

/** What a picture is doing: loading, shown, or none (no URL, a 404, the home unreachable). */
enum class PictureState { Loading, Shown, Missing }

/**
 * A home's picture filling its box: quiet ([loadingColor]) while it loads, the picture once it
 * decodes, and with none (no URL, a 404) [fallback] on [missingFill] (the brand's colour), so a
 * card without a poster is still a card. The request is sized by the layout (NFR-4: decoded at
 * display size). Decorative: the card or hero around it carries the words for TalkBack.
 * [onState] says what it is doing (a channel's logo shows its number instead when it is missing).
 */
@Composable
fun HomePicture(
    url: String?,
    modifier: Modifier = Modifier,
    contentScale: ContentScale = ContentScale.Crop,
    imageLoader: ImageLoader = homeImageLoader(),
    missingFill: Brush? = brandFill(),
    loadingColor: Color? = MaterialTheme.colorScheme.surfaceVariant,
    onState: (PictureState) -> Unit = {},
    fallback: @Composable () -> Unit = {},
) {
    var state by remember(url) { mutableStateOf(if (url == null) PictureState.Missing else PictureState.Loading) }
    LaunchedEffect(state) { onState(state) }
    Box(
        modifier.testTag(
            when (state) {
                PictureState.Loading -> "picture-loading"
                PictureState.Shown -> "picture-shown"
                PictureState.Missing -> "picture-missing"
            },
        ),
    ) {
        when (state) {
            PictureState.Missing -> Box(
                Modifier.fillMaxSize().then(if (missingFill != null) Modifier.background(missingFill) else Modifier),
                contentAlignment = Alignment.Center,
            ) { fallback() }
            else -> if (loadingColor != null) Box(Modifier.fillMaxSize().background(loadingColor))
        }
        if (url != null) {
            val context = LocalContext.current
            val request = remember(url) { ImageRequest.Builder(context).data(url).build() }
            AsyncImage(
                model = request,
                contentDescription = null,
                imageLoader = imageLoader,
                contentScale = contentScale,
                modifier = Modifier.fillMaxSize(),
                onState = {
                    state = when (it) {
                        is AsyncImagePainter.State.Success -> PictureState.Shown
                        is AsyncImagePainter.State.Error -> PictureState.Missing
                        else -> if (state == PictureState.Missing) PictureState.Missing else PictureState.Loading
                    }
                },
            )
        }
    }
}

/** The brand's colour behind a missing picture: brass deepening to ink, in both themes. */
@Composable
internal fun brandFill(): Brush = Brush.linearGradient(
    listOf(MaktabaTokens.Brand.brand800, MaktabaTokens.Brand.brand900, MaktabaTokens.Dark.bg),
)

/** A missing poster's mark: a film frame in soft brass (the title is under the card already). */
@Composable
internal fun MissingPosterMark(modifier: Modifier = Modifier) {
    Icon(
        Icons.Outlined.Movie,
        contentDescription = null,
        modifier = modifier.size(36.dp),
        tint = MaktabaTokens.Brand.brand300.copy(alpha = 0.7f),
    )
}
