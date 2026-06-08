package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import androidx.tv.foundation.lazy.grid.TvGridCells
import androidx.tv.foundation.lazy.grid.TvLazyVerticalGrid
import androidx.tv.foundation.lazy.grid.items
import androidx.tv.material3.ExperimentalTvMaterial3Api
import com.hamzalabs.maktaba.tv.data.models.SearchResult
import com.hamzalabs.maktaba.tv.data.repository.MediaRepository
import com.hamzalabs.maktaba.tv.ui.components.MediaCard
import com.hamzalabs.maktaba.tv.ui.components.TvTextField
import kotlinx.coroutines.launch

/**
 * Search screen. The text field's IME exposes the system mic, so voice
 * search works out of the box; a global Assistant intent ("search
 * Maktaba for …") is a documented follow-up. Searching on the IME
 * "search" action avoids hammering the API on every keystroke.
 */
@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
fun SearchScreen(
    repository: MediaRepository,
    onPlay: (videoId: String) -> Unit,
) {
    var query by remember { mutableStateOf("") }
    var results by remember { mutableStateOf<List<SearchResult>>(emptyList()) }
    val scope = rememberCoroutineScope()

    fun runSearch() {
        val q = query.trim()
        if (q.isEmpty()) {
            results = emptyList()
            return
        }
        scope.launch {
            repository.search(q).onSuccess { results = it }.onFailure { results = emptyList() }
        }
    }

    Column(
        modifier = Modifier.fillMaxSize().padding(48.dp),
        verticalArrangement = Arrangement.spacedBy(24.dp),
    ) {
        TvTextField(
            value = query,
            onValueChange = { query = it },
            label = "Search libraries",
            modifier = Modifier.fillMaxWidth(0.6f),
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
            keyboardActions = KeyboardActions(onSearch = { runSearch() }),
        )

        if (results.isEmpty() && query.isNotBlank()) {
            CenterMessage("No results for \"$query\"")
        } else {
            TvLazyVerticalGrid(
                columns = TvGridCells.Fixed(5),
                horizontalArrangement = Arrangement.spacedBy(24.dp),
                verticalArrangement = Arrangement.spacedBy(32.dp),
                contentPadding = PaddingValues(top = 8.dp),
            ) {
                items(results) { result ->
                    MediaCard(
                        title = result.title,
                        posterUrl = result.posterUrl,
                        onClick = { onPlay(result.id) },
                    )
                }
            }
        }
    }
}
