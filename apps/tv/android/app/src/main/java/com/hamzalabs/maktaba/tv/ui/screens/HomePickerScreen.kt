package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.tv.material3.ListItem
import androidx.tv.material3.ListItemDefaults
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.FoundHome
import com.hamzalabs.maktaba.core.session.Home
import com.hamzalabs.maktaba.core.session.HomeDiscovery
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.ui.components.LibraryBackdrop
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton
import com.hamzalabs.maktaba.tv.ui.components.ReadingPanel
import com.hamzalabs.maktaba.tv.ui.components.SectionTitle
import com.hamzalabs.maktaba.tv.ui.components.TvTextField
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import kotlinx.coroutines.flow.catch
import com.hamzalabs.maktaba.core.R as CoreR

/**
 * Choosing the home: the ones announcing themselves on this network (mDNS `_maktaba._tcp`, the
 * core's [HomeDiscovery]), the ones this TV used before, or an address typed with the remote.
 */
@Composable
fun HomePickerScreen(session: AppSession) {
    val context = LocalContext.current
    val saved by session.homes.collectAsState()
    var found by remember { mutableStateOf<List<FoundHome>>(emptyList()) }
    var address by remember { mutableStateOf("") }
    var invalid by remember { mutableStateOf(false) }
    val first = remember { FocusRequester() }

    LaunchedEffect(Unit) {
        HomeDiscovery(context).homes().catch { }.collect { found = it }
    }
    LaunchedEffect(Unit) { runCatching { first.requestFocus() } }

    fun connect() {
        val url = Home.normalize(address)
        if (url == null) {
            invalid = true
            return
        }
        invalid = false
        session.use(Home(url, url.substringAfter("://")))
    }

    LibraryBackdrop {
        Row(
            Modifier.fillMaxSize().padding(horizontal = 80.dp, vertical = 48.dp),
            horizontalArrangement = Arrangement.spacedBy(48.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            ReadingPanel(Modifier.weight(1f)) {
                SectionTitle(stringResource(R.string.choose_home))
                Text(
                    stringResource(R.string.choose_home_hint),
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaktabaPalette.Muted,
                    modifier = Modifier.padding(top = 8.dp, bottom = 16.dp),
                )
                val known = saved.map { it.url }.toSet()
                val rows = saved.map { Triple(it.name, it.url, true) } +
                    found.filter { it.url !in known }.map { Triple(it.name, it.url, false) }
                if (rows.isEmpty()) {
                    Text(stringResource(CoreR.string.homes_looking), style = MaterialTheme.typography.bodyLarge, color = MaktabaPalette.Faint)
                }
                LazyColumn(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    items(rows, key = { it.second }) { (name, url, isSaved) ->
                        ListItem(
                            selected = false,
                            onClick = { session.use(Home(url, name)) },
                            modifier = if (rows.first().second == url) Modifier.focusRequester(first) else Modifier,
                            headlineContent = { Text(name, style = MaterialTheme.typography.titleMedium) },
                            supportingContent = { Text(url.substringAfter("://"), style = MaterialTheme.typography.labelMedium) },
                            trailingContent = {
                                if (isSaved) Text(stringResource(CoreR.string.home_saved), style = MaterialTheme.typography.labelMedium)
                            },
                            colors = ListItemDefaults.colors(
                                containerColor = MaktabaPalette.Surface,
                                focusedContainerColor = MaktabaPalette.Gold,
                                focusedContentColor = MaktabaPalette.OnGold,
                            ),
                        )
                    }
                }
            }
            ReadingPanel(Modifier.width(460.dp)) {
                Text(stringResource(R.string.enter_address), style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text)
                TvTextField(
                    value = address,
                    onValueChange = { address = it; invalid = false },
                    label = stringResource(CoreR.string.address_label),
                    modifier = Modifier.fillMaxWidth().padding(vertical = 16.dp)
                        .then(if (saved.isEmpty() && found.isEmpty()) Modifier.focusRequester(first) else Modifier),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Go),
                    keyboardActions = KeyboardActions(onGo = { connect() }),
                )
                if (invalid) {
                    Text(stringResource(CoreR.string.address_invalid), color = MaktabaPalette.Gold, modifier = Modifier.padding(bottom = 8.dp))
                }
                Box { MaktabaButton(onClick = ::connect) { Text(stringResource(CoreR.string.connect)) } }
            }
        }
    }
}
