package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.tv.foundation.lazy.grid.TvGridCells
import androidx.tv.foundation.lazy.grid.TvLazyVerticalGrid
import androidx.tv.foundation.lazy.grid.items
import androidx.tv.material3.ExperimentalTvMaterial3Api
import com.hamzalabs.maktaba.tv.data.models.Media
import com.hamzalabs.maktaba.tv.data.repository.MediaRepository
import com.hamzalabs.maktaba.tv.ui.components.MediaCard

/** Poster grid of one library's contents. */
@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
fun MediaGridScreen(
    repository: MediaRepository,
    libraryId: String,
    onPlay: (videoId: String) -> Unit,
) {
    var items by remember { mutableStateOf<List<Media>>(emptyList()) }
    var error by remember { mutableStateOf<String?>(null) }
    var loading by remember { mutableStateOf(true) }

    LaunchedEffect(libraryId) {
        repository.libraryItems(libraryId)
            .onSuccess { items = it; error = null }
            .onFailure { error = it.message }
        loading = false
    }

    when {
        loading -> CenterMessage("Loading…")
        error != null -> CenterMessage(error!!)
        else -> TvLazyVerticalGrid(
            columns = TvGridCells.Fixed(5),
            modifier = Modifier.fillMaxSize().padding(48.dp),
            horizontalArrangement = Arrangement.spacedBy(24.dp),
            verticalArrangement = Arrangement.spacedBy(32.dp),
            contentPadding = PaddingValues(8.dp),
        ) {
            items(items) { media ->
                MediaCard(
                    title = media.title,
                    posterUrl = media.posterUrl,
                    onClick = { onPlay(media.id) },
                )
            }
        }
    }
}
