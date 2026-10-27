package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.data.TvApi
import com.hamzalabs.maktaba.tv.ui.components.TvTextField
import kotlinx.coroutines.delay

/** What was searched: kept by the signed-in app, so Back from a result finds it again. */
class SearchState {
    var text by mutableStateOf("")
    var query by mutableStateOf("")
}

/** Search the videos by name: the results follow the typing (a short pause, then the home). */
@Composable
fun SearchScreen(api: TvApi, search: SearchState, focusFirst: Boolean, onOpen: (String) -> Unit, signedOut: () -> Unit) {
    var text by search::text
    var query by search::query
    // back from a video: its results take focus again, until a new search (typing keeps focus)
    val shownOnReturn = remember { query }
    LaunchedEffect(text) {
        delay(400)
        query = text.trim()
    }
    Column(Modifier.fillMaxSize().padding(top = 28.dp)) {
        TvTextField(
            value = text,
            onValueChange = { text = it },
            label = stringResource(R.string.search_hint),
            modifier = Modifier.padding(horizontal = Gutter).fillMaxWidth(0.6f),
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
            keyboardActions = KeyboardActions(onSearch = { query = text.trim() }),
        )
        if (query.isNotEmpty()) {
            VideoGrid(
                api,
                key = query,
                signedOut = signedOut,
                onOpen = onOpen,
                empty = stringResource(R.string.search_none, query),
                focusFirst = focusFirst && query == shownOnReturn,
            ) { cursor -> api.videos(query = query, cursor = cursor) }
        }
    }
}
