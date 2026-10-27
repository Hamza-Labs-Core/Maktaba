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
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.tv.material3.ListItem
import androidx.tv.material3.ListItemDefaults
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.ConnectLink
import com.hamzalabs.maktaba.core.session.Home
import com.hamzalabs.maktaba.core.session.HomeProbe
import com.hamzalabs.maktaba.tv.MaktabaTVApp
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.data.HomeChoices
import com.hamzalabs.maktaba.tv.ui.components.LibraryBackdrop
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton
import com.hamzalabs.maktaba.tv.ui.components.ReadingPanel
import com.hamzalabs.maktaba.tv.ui.components.SectionTitle
import com.hamzalabs.maktaba.tv.ui.components.TvTextField
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import com.hamzalabs.maktaba.core.R as CoreR
import com.hamzalabs.maktaba.core.diagnostics.AppLog

/**
 * Choosing the home (Epic 42 story 42.10): the homes this TV used (Your homes; hold Select on
 * one to rename or remove it), the ones announcing themselves on this network (mDNS
 * `_maktaba._tcp`, `role=home` only, one row per home id: the core's discovery, kept by the
 * app: [MaktabaTVApp.homesFound]) that are not among them, or one by its address or a pasted
 * `maktaba://connect` link, asked whether it is a Maktaba home first ([AppSession.connect]).
 */
@Composable
fun HomePickerScreen(app: MaktabaTVApp, probe: HomeProbe = remember { HomeProbe() }) {
    val session = app.session
    val saved by session.homes.collectAsState()
    val found by app.homesFound.collectAsState()
    var address by remember { mutableStateOf("") }
    var problem by remember { mutableStateOf<String?>(null) }
    var connecting by remember { mutableStateOf(false) }
    var options by remember { mutableStateOf<Home?>(null) }
    val scope = rememberCoroutineScope()
    val first = remember { FocusRequester() }
    val connectButton = remember { FocusRequester() }
    val invalid = stringResource(CoreR.string.address_invalid)
    val failed = stringResource(CoreR.string.connect_failed, "%s")
    val nearby = HomeChoices.nearby(found, saved)

    // a home in the list takes focus (never the text field: focus there opens the keyboard)
    val anyHome = saved.isNotEmpty() || nearby.isNotEmpty()
    LaunchedEffect(anyHome) { runCatching { if (anyHome) first.requestFocus() else connectButton.requestFocus() } }

    fun connect(link: ConnectLink) {
        if (connecting) return
        connecting = true
        problem = null
        scope.launch {
            try {
                session.connect(link, probe)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                AppLog.failure("tv connect", e)
                problem = failed.replace("%s", link.urls.joinToString { it.substringAfter("://") })
            } finally {
                connecting = false
            }
        }
    }

    fun add() {
        val link = HomeChoices.typed(address)
        if (link == null) problem = invalid else connect(link)
    }

    options?.let { home -> HomeOptionsDialog(session, home) { options = null } }

    LibraryBackdrop {
        Row(
            Modifier.fillMaxSize().padding(horizontal = 80.dp, vertical = 48.dp),
            horizontalArrangement = Arrangement.spacedBy(48.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            ReadingPanel(Modifier.weight(1.3f)) {
                SectionTitle(stringResource(R.string.choose_home))
                Text(
                    stringResource(R.string.choose_home_hint),
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaktabaPalette.Muted,
                    modifier = Modifier.padding(top = 8.dp, bottom = 16.dp),
                )
                LazyColumn(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    if (saved.isNotEmpty()) {
                        item { Heading(stringResource(CoreR.string.homes_saved)) }
                        items(saved, key = { "saved:" + it.url }) { home ->
                            HomeItem(
                                name = home.label,
                                address = home.address,
                                enabled = !connecting,
                                onClick = { problem = null; session.use(home) },
                                onLongClick = { options = home },
                                modifier = if (home === saved.first()) Modifier.focusRequester(first) else Modifier,
                            )
                        }
                        item {
                            Text(stringResource(R.string.home_hold_hint), style = MaterialTheme.typography.labelMedium, color = MaktabaPalette.Faint)
                        }
                    }
                    item { Heading(stringResource(CoreR.string.homes_on_network)) }
                    items(nearby, key = { "found:" + it.url }) { f ->
                        HomeItem(
                            name = f.name,
                            address = f.url.substringAfter("://"),
                            enabled = !connecting,
                            onClick = { problem = null; session.use(Home(f.url, f.name, f.id)) },
                            modifier = if (saved.isEmpty() && f === nearby.first()) Modifier.focusRequester(first) else Modifier,
                        )
                    }
                    if (nearby.isEmpty()) {
                        item {
                            Text(
                                stringResource(if (found.isEmpty()) CoreR.string.homes_looking else CoreR.string.homes_all_saved),
                                style = MaterialTheme.typography.bodyLarge,
                                color = MaktabaPalette.Faint,
                            )
                        }
                    }
                }
            }
            ReadingPanel(Modifier.weight(1f)) {
                Text(stringResource(R.string.enter_address), style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text)
                // an address reads left to right in Arabic too
                CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
                    TvTextField(
                        value = address,
                        onValueChange = { address = it; problem = null },
                        label = stringResource(CoreR.string.address_label),
                        modifier = Modifier.fillMaxWidth().padding(vertical = 16.dp).testTag("picker-address"),
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Go),
                        keyboardActions = KeyboardActions(onGo = { add() }),
                    )
                }
                Text(stringResource(R.string.address_hint), style = MaterialTheme.typography.labelMedium, color = MaktabaPalette.Faint)
                if (connecting) {
                    Text(stringResource(CoreR.string.connecting), color = MaktabaPalette.Muted, modifier = Modifier.padding(top = 8.dp))
                }
                problem?.let { Text(it, color = MaktabaPalette.Gold, modifier = Modifier.padding(top = 8.dp)) }
                Box(Modifier.padding(top = 16.dp)) {
                    MaktabaButton(onClick = ::add, enabled = !connecting, modifier = Modifier.focusRequester(connectButton)) {
                        Text(stringResource(CoreR.string.connect))
                    }
                }
            }
        }
    }
}

@Composable
private fun Heading(text: String) {
    Text(text, style = MaterialTheme.typography.titleSmall, color = MaktabaPalette.Gold, modifier = Modifier.padding(top = 8.dp))
}

@Composable
private fun HomeItem(
    name: String,
    address: String,
    enabled: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    onLongClick: (() -> Unit)? = null,
) {
    ListItem(
        selected = false,
        enabled = enabled,
        onClick = onClick,
        onLongClick = onLongClick,
        modifier = modifier,
        headlineContent = { Text(name, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis) },
        supportingContent = {
            CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
                Text(address, style = MaterialTheme.typography.labelMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        },
        colors = ListItemDefaults.colors(
            containerColor = MaktabaPalette.Surface,
            focusedContainerColor = MaktabaPalette.Gold,
            focusedContentColor = MaktabaPalette.OnGold,
        ),
    )
}

/** A saved home's other actions: rename it on this TV, or remove it (and its sign-in). */
@Composable
private fun HomeOptionsDialog(session: AppSession, home: Home, onDismiss: () -> Unit) {
    var name by remember(home.url) { mutableStateOf(home.nickname ?: "") }
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }
    Dialog(onDismissRequest = onDismiss) {
        ReadingPanel(Modifier.width(560.dp), contentPadding = 32.dp) {
            SectionTitle(home.label)
            CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
                Text(home.address, style = MaterialTheme.typography.labelMedium, color = MaktabaPalette.Muted)
            }
            TvTextField(
                value = name,
                onValueChange = { name = it },
                label = stringResource(CoreR.string.rename_label),
                modifier = Modifier.fillMaxWidth().padding(top = 16.dp),
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Done),
                keyboardActions = KeyboardActions(onDone = { session.rename(home, name); onDismiss() }),
            )
            Text(stringResource(CoreR.string.rename_hint), style = MaterialTheme.typography.labelMedium, color = MaktabaPalette.Faint)
            Column(Modifier.padding(top = 20.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    MaktabaButton(onClick = { session.rename(home, name); onDismiss() }, modifier = Modifier.focusRequester(focus)) {
                        Text(stringResource(CoreR.string.save))
                    }
                    MaktabaButton(onClick = { session.forget(home); onDismiss() }) { Text(stringResource(CoreR.string.home_remove)) }
                    MaktabaButton(onClick = onDismiss) { Text(stringResource(CoreR.string.cancel)) }
                }
            }
        }
    }
}

/**
 * A `maktaba://connect` link the app was opened with: nothing is asked of its addresses until
 * the person says Connect (anyone can make a link).
 */
@Composable
fun ConnectLinkDialog(session: AppSession, link: ConnectLink, probe: HomeProbe = remember { HomeProbe() }) {
    var busy by remember(link) { mutableStateOf(false) }
    var problem by remember(link) { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val failed = stringResource(CoreR.string.connect_failed, link.urls.joinToString { it.substringAfter("://") })
    val cancel = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { cancel.requestFocus() } }
    Dialog(onDismissRequest = { if (!busy) session.dismissLink() }) {
        ReadingPanel(Modifier.width(640.dp), contentPadding = 32.dp) {
            SectionTitle(stringResource(CoreR.string.link_title, link.name ?: stringResource(CoreR.string.link_unnamed)))
            Text(stringResource(CoreR.string.link_body), style = MaterialTheme.typography.bodyLarge, color = MaktabaPalette.Muted, modifier = Modifier.padding(top = 12.dp))
            CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
                Column(Modifier.padding(vertical = 8.dp)) {
                    link.urls.forEach { Text(it.substringAfter("://"), style = MaterialTheme.typography.titleSmall, color = MaktabaPalette.Text) }
                }
            }
            if (busy) Text(stringResource(CoreR.string.connecting), color = MaktabaPalette.Muted)
            problem?.let { Text(it, color = MaktabaPalette.Gold) }
            Row(Modifier.padding(top = 16.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                MaktabaButton(
                    enabled = !busy,
                    modifier = Modifier.testTag("link-connect"),
                    onClick = {
                        busy = true
                        problem = null
                        scope.launch {
                            try {
                                session.connect(link, probe)
                            } catch (e: CancellationException) {
                                throw e
                            } catch (e: Exception) {
                                AppLog.failure("tv connect link", e)
                                problem = failed
                            } finally {
                                busy = false
                            }
                        }
                    },
                ) { Text(stringResource(CoreR.string.connect)) }
                MaktabaButton(enabled = !busy, onClick = session::dismissLink, modifier = Modifier.focusRequester(cancel)) {
                    Text(stringResource(CoreR.string.cancel))
                }
            }
        }
    }
}
