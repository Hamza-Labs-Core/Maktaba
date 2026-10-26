package com.hamzalabs.maktaba.tv.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Card
import androidx.tv.material3.CardDefaults
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette

/**
 * A focusable video tile, the building block of every grid and rail: the video's 16:9 picture,
 * a gold bar for how much is watched, its name under it.
 *
 * D-pad focus is the TV focus engine's; `tv-material`'s [Card] lifts the focused card toward the
 * viewer (scale) and draws the theme's 3dp gold ring; the name brightens with it.
 */
@Composable
fun MediaCard(
    client: ApiClient,
    video: String,
    title: String,
    modifier: Modifier = Modifier,
    subtitle: String? = null,
    progress: Float = 0f,
    width: Dp = 300.dp,
    onClick: () -> Unit = {},
) {
    var focused by remember { mutableStateOf(false) }

    Column(modifier = modifier.width(width)) {
        Card(
            onClick = onClick,
            modifier = Modifier
                .fillMaxWidth()
                .aspectRatio(16f / 9f)
                .onFocusChanged { focused = it.isFocused || it.hasFocus },
            shape = CardDefaults.shape(CardCorner),
            colors = maktabaCardColors(),
            scale = CardDefaults.scale(focusedScale = 1.08f),
            border = goldFocusBorder(),
        ) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.BottomStart) {
                Poster(client, video, Modifier.fillMaxSize())
                if (progress > 0f) {
                    Box(Modifier.fillMaxWidth().height(6.dp).background(MaktabaPalette.Background.copy(alpha = 0.7f))) {
                        Box(Modifier.fillMaxHeight().fillMaxWidth(progress.coerceIn(0f, 1f)).background(MaktabaPalette.Gold))
                    }
                }
            }
        }
        Text(
            text = title,
            style = MaterialTheme.typography.titleMedium,
            color = if (focused) MaktabaPalette.Text else MaktabaPalette.Muted,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.padding(top = 10.dp),
        )
        if (subtitle != null) {
            Text(
                text = subtitle,
                style = MaterialTheme.typography.labelMedium,
                color = MaktabaPalette.Faint,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}
