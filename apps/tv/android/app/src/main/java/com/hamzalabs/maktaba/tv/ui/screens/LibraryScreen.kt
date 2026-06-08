package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.tv.foundation.lazy.grid.TvGridCells
import androidx.tv.foundation.lazy.grid.TvLazyVerticalGrid
import androidx.tv.foundation.lazy.grid.items
import androidx.tv.material3.Card
import androidx.tv.material3.ExperimentalTvMaterial3Api
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.tv.data.models.Library
import com.hamzalabs.maktaba.tv.data.repository.MediaRepository

/** Grid of libraries; selecting one opens its media grid. */
@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
fun LibraryScreen(
    repository: MediaRepository,
    onOpenLibrary: (id: String, name: String) -> Unit,
) {
    var libraries by remember { mutableStateOf<List<Library>>(emptyList()) }
    var error by remember { mutableStateOf<String?>(null) }
    var loading by remember { mutableStateOf(true) }

    LaunchedEffect(Unit) {
        repository.libraries()
            .onSuccess { libraries = it; error = null }
            .onFailure { error = it.message }
        loading = false
    }

    when {
        loading -> CenterMessage("Loading…")
        error != null -> CenterMessage(error!!)
        else -> TvLazyVerticalGrid(
            columns = TvGridCells.Fixed(4),
            modifier = Modifier.fillMaxSize().padding(48.dp),
            horizontalArrangement = Arrangement.spacedBy(32.dp),
            verticalArrangement = Arrangement.spacedBy(32.dp),
            contentPadding = PaddingValues(8.dp),
        ) {
            items(libraries) { library ->
                LibraryCard(library) { onOpenLibrary(library.id, library.name) }
            }
        }
    }
}

@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
private fun LibraryCard(library: Library, onClick: () -> Unit) {
    Card(onClick = onClick, modifier = Modifier.height(180.dp)) {
        Column(
            Modifier.fillMaxSize().padding(24.dp),
            verticalArrangement = Arrangement.Center,
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text(text = library.name, style = MaterialTheme.typography.titleLarge, maxLines = 1)
            library.totalVideos?.let {
                Text(
                    text = "$it items",
                    style = MaterialTheme.typography.labelLarge,
                    color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.6f),
                )
            }
        }
    }
}
