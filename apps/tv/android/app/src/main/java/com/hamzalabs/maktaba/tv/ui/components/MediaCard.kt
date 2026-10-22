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
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Card
import androidx.tv.material3.CardDefaults
import androidx.tv.material3.ExperimentalTvMaterial3Api
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import coil.compose.AsyncImage
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette

/**
 * A focusable poster tile — the building block of every grid and rail.
 *
 * D-pad focus is driven by the TV focus engine. `tv-material`'s [Card]
 * applies the focused scale and border itself (the standard "lift
 * toward the viewer" affordance); we give it the theme's 3dp gold ring
 * and track focus to brighten the title label underneath.
 */
@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
fun MediaCard(
    title: String,
    posterUrl: String?,
    modifier: Modifier = Modifier,
    progress: Float = 0f,
    onClick: () -> Unit = {},
) {
    var focused by remember { mutableStateOf(false) }

    Column(modifier = modifier.width(220.dp)) {
        Card(
            onClick = onClick,
            modifier = Modifier
                .fillMaxWidth()
                .aspectRatio(2f / 3f)
                .onFocusChanged { focused = it.isFocused },
            shape = CardDefaults.shape(CardCorner),
            colors = maktabaCardColors(),
            scale = CardDefaults.scale(focusedScale = 1.08f),
            border = goldFocusBorder(),
        ) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.BottomStart) {
                AsyncImage(
                    model = posterUrl,
                    contentDescription = title,
                    modifier = Modifier.fillMaxSize(),
                )
                if (progress > 0f) {
                    // Hand-drawn resume bar; avoids pulling in phone Material.
                    Box(
                        Modifier
                            .fillMaxWidth()
                            .height(6.dp)
                            .background(MaktabaPalette.Raised),
                    ) {
                        Box(
                            Modifier
                                .fillMaxHeight()
                                .fillMaxWidth(progress.coerceIn(0f, 1f))
                                .background(MaktabaPalette.Gold),
                        )
                    }
                }
            }
        }
        Text(
            text = title,
            style = MaterialTheme.typography.labelLarge,
            color = if (focused) MaktabaPalette.Text else MaktabaPalette.Muted,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.padding(top = 8.dp),
        )
    }
}
