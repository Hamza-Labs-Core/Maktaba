package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.tv.foundation.lazy.list.TvLazyColumn
import androidx.tv.foundation.lazy.list.TvLazyRow
import androidx.tv.foundation.lazy.list.items
import androidx.tv.material3.ExperimentalTvMaterial3Api
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.tv.data.models.MediaRail
import com.hamzalabs.maktaba.tv.data.repository.MediaRepository
import com.hamzalabs.maktaba.tv.ui.components.CenteredPanel
import com.hamzalabs.maktaba.tv.ui.components.MediaCard
import com.hamzalabs.maktaba.tv.ui.components.ReadingPanel
import com.hamzalabs.maktaba.tv.ui.components.SectionTitle
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette

/**
 * Landing screen: a vertical column of horizontally-scrolling rails
 * (Continue Watching first, then recommendation rows).
 */
@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
fun HomeScreen(
    repository: MediaRepository,
    onPlay: (videoId: String) -> Unit,
) {
    var rails by remember { mutableStateOf<List<MediaRail>>(emptyList()) }
    var error by remember { mutableStateOf<String?>(null) }
    var loading by remember { mutableStateOf(true) }

    LaunchedEffect(Unit) {
        repository.homeRails()
            .onSuccess { rails = it; error = null }
            .onFailure { error = it.message }
        loading = false
    }

    // The library scene is drawn behind this screen by NavGraph. The
    // headline sits on a reading panel in the calm middle of the art,
    // below the lamp; the rails sit in the faded lower half.
    Column(Modifier.fillMaxSize()) {
        Box(
            Modifier
                .fillMaxWidth()
                .height(170.dp)
                .padding(bottom = 12.dp),
            contentAlignment = Alignment.BottomCenter,
        ) {
            ReadingPanel(contentPadding = 20.dp) {
                SectionTitle("Your library")
                Text(
                    text = "Continue where you left off",
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaktabaPalette.Muted,
                )
            }
        }

        when {
            loading -> CenterMessage("Loading…")
            error != null -> CenterMessage(error!!)
            else -> TvLazyColumn(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(horizontal = 48.dp),
                verticalArrangement = Arrangement.spacedBy(36.dp),
                contentPadding = PaddingValues(vertical = 24.dp),
            ) {
                items(rails) { rail ->
                    RailRow(rail = rail, onPlay = onPlay)
                }
            }
        }
    }
}

@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
private fun RailRow(rail: MediaRail, onPlay: (String) -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(16.dp)) {
        SectionTitle(rail.title)
        // Vertical padding leaves room for the focused card's scale + ring.
        TvLazyRow(
            horizontalArrangement = Arrangement.spacedBy(24.dp),
            contentPadding = PaddingValues(vertical = 12.dp, horizontal = 8.dp),
        ) {
            items(rail.items) { item ->
                MediaCard(
                    title = item.title.ifEmpty { rail.title },
                    posterUrl = item.posterUrl,
                    progress = item.progress,
                    onClick = { onPlay(item.videoId) },
                )
            }
        }
    }
}

/**
 * Centred status text (loading / error / empty). Always on a reading
 * panel, so it stays legible whether or not the art is behind it.
 */
@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
fun CenterMessage(text: String) {
    CenteredPanel {
        Text(text = text, style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text)
    }
}
