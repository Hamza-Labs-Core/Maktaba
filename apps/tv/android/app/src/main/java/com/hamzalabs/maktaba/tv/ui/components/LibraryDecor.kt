package com.hamzalabs.maktaba.tv.ui.components

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Border
import androidx.tv.material3.Button
import androidx.tv.material3.ButtonDefaults
import androidx.tv.material3.CardDefaults
import androidx.tv.material3.ExperimentalTvMaterial3Api
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.R
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette

/** Corner radius shared by cards, panels and the focus ring. */
val CardCorner = RoundedCornerShape(12.dp)

/** Width of the gold focus ring (owner spec: 3px gold outline). */
val FocusRingWidth: Dp = 3.dp

/**
 * Full-screen library scene (bookshelves, brass lamp, calm middle) with
 * the readability fades the theme requires: a top fade so the nav band
 * and headline never sit on raw art, and a bottom fade into the
 * background colour behind the rails. Text must still go on a
 * [ReadingPanel] or the faded region — never directly on the art.
 */
@Composable
fun LibraryBackdrop(
    modifier: Modifier = Modifier,
    content: @Composable BoxScope.() -> Unit = {},
) {
    Box(modifier.fillMaxSize().background(MaktabaPalette.Background)) {
        Image(
            painter = painterResource(R.drawable.library_dark_wide),
            contentDescription = null, // decorative
            contentScale = ContentScale.Crop,
            modifier = Modifier.fillMaxSize(),
        )
        Box(
            Modifier
                .fillMaxSize()
                .background(
                    Brush.verticalGradient(
                        // Top: under the nav band.
                        0.00f to MaktabaPalette.Background.copy(alpha = 0.80f),
                        0.16f to MaktabaPalette.Background.copy(alpha = 0.20f),
                        // Middle: the lamp and arches show through.
                        0.36f to MaktabaPalette.Background.copy(alpha = 0.15f),
                        // Bottom: rails sit on (almost) solid background.
                        0.48f to MaktabaPalette.Background.copy(alpha = 0.88f),
                        0.62f to MaktabaPalette.Background.copy(alpha = 0.96f),
                        1.00f to MaktabaPalette.Background,
                    ),
                ),
        )
        content()
    }
}

/**
 * Translucent reading panel for any text that sits over the art:
 * background at 78% + a hairline border. (No backdrop blur — see
 * [MaktabaPalette.Panel].)
 */
@Composable
fun ReadingPanel(
    modifier: Modifier = Modifier,
    contentPadding: Dp = 24.dp,
    content: @Composable ColumnScope.() -> Unit,
) {
    Column(
        modifier = modifier
            .background(MaktabaPalette.Panel, CardCorner)
            .border(1.dp, MaktabaPalette.Border, CardCorner)
            .padding(contentPadding),
        content = content,
    )
}

/** Section heading with the gold ۞ marker before it. */
@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
fun SectionTitle(text: String, modifier: Modifier = Modifier) {
    Row(
        modifier = modifier,
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text(
            text = "۞",
            style = MaterialTheme.typography.titleLarge,
            color = MaktabaPalette.Gold,
        )
        Text(
            text = text,
            style = MaterialTheme.typography.headlineMedium,
            color = MaktabaPalette.Text,
        )
    }
}

/** Card focus: 3dp gold ring that follows the card's corners. */
@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
fun goldFocusBorder() = CardDefaults.border(
    focusedBorder = Border(
        border = BorderStroke(FocusRingWidth, MaktabaPalette.Gold),
        shape = CardCorner,
    ),
)

/** Surface-coloured card that keeps its fill on focus (the ring shows focus). */
@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
fun maktabaCardColors() = CardDefaults.colors(
    containerColor = MaktabaPalette.Surface,
    contentColor = MaktabaPalette.Text,
    focusedContainerColor = MaktabaPalette.Raised,
    focusedContentColor = MaktabaPalette.Text,
)

/**
 * Themed button: raised surface at rest, gold fill + ink text + a light
 * gold ring when focused, so the focused button reads from across the room.
 */
@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
fun MaktabaButton(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    content: @Composable RowScope.() -> Unit,
) {
    Button(
        onClick = onClick,
        modifier = modifier,
        enabled = enabled,
        colors = ButtonDefaults.colors(
            containerColor = MaktabaPalette.Raised,
            contentColor = MaktabaPalette.Text,
            focusedContainerColor = MaktabaPalette.Gold,
            focusedContentColor = MaktabaPalette.OnGold,
            pressedContainerColor = MaktabaPalette.GoldLight,
            pressedContentColor = MaktabaPalette.OnGold,
        ),
        // tv-material's button is a full pill (CircleShape); the ring matches it.
        border = ButtonDefaults.border(
            border = Border(BorderStroke(1.dp, MaktabaPalette.Border), shape = CircleShape),
            focusedBorder = Border(BorderStroke(FocusRingWidth, MaktabaPalette.GoldLight), shape = CircleShape),
        ),
        content = content,
    )
}

/** Fill the parent with [content] centred — used for loading/error states. */
@Composable
fun CenteredPanel(content: @Composable ColumnScope.() -> Unit) {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        ReadingPanel(contentPadding = 32.dp, content = content)
    }
}
