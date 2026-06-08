package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
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
import com.hamzalabs.maktaba.tv.ui.components.MediaCard

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

    when {
        loading -> CenterMessage("Loading…")
        error != null -> CenterMessage(error!!)
        else -> TvLazyColumn(
            modifier = Modifier
                .fillMaxSize()
                .padding(horizontal = 48.dp),
            verticalArrangement = Arrangement.spacedBy(36.dp),
            contentPadding = androidx.compose.foundation.layout.PaddingValues(vertical = 32.dp),
        ) {
            items(rails) { rail ->
                RailRow(rail = rail, onPlay = onPlay)
            }
        }
    }
}

@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
private fun RailRow(rail: MediaRail, onPlay: (String) -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(16.dp)) {
        Text(text = rail.title, style = MaterialTheme.typography.headlineMedium)
        TvLazyRow(horizontalArrangement = Arrangement.spacedBy(24.dp)) {
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

@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
fun CenterMessage(text: String) {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Text(text = text, style = MaterialTheme.typography.titleLarge)
    }
}
