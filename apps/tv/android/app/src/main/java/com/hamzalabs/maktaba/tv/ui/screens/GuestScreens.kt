package com.hamzalabs.maktaba.tv.ui.screens

import android.content.Context
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
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
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import androidx.tv.material3.ListItem
import androidx.tv.material3.ListItemDefaults
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.R as CoreR
import com.hamzalabs.maktaba.core.guest.GuestError
import com.hamzalabs.maktaba.core.guest.GuestLineup
import com.hamzalabs.maktaba.core.guest.GuestPairingRun
import com.hamzalabs.maktaba.core.guest.GuestPass
import com.hamzalabs.maktaba.core.guest.GuestPlayer
import com.hamzalabs.maktaba.core.guest.GuestSpace
import com.hamzalabs.maktaba.core.guest.GuestVideo
import com.hamzalabs.maktaba.core.guest.GuestWatch
import com.hamzalabs.maktaba.core.guest.guestWords
import com.hamzalabs.maktaba.tv.ui.components.LibraryBackdrop
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton
import com.hamzalabs.maktaba.tv.ui.components.QrCode
import com.hamzalabs.maktaba.tv.ui.components.ReadingPanel
import com.hamzalabs.maktaba.tv.ui.components.SectionTitle
import com.hamzalabs.maktaba.tv.ui.components.TvTextField
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle

// Story 50.27 §2 on Android TV and Fire TV: Watch a broadcast, a space apart from the TV's homes
// and accounts. The saved broadcasts; the publisher's handle and an MK- code typed with the
// remote; a QR code a phone opens, with the 4-character code both screens show, while the TV
// waits for the phone's pass; a publisher's channels with now and next; the player, where up and
// down change the channel. Every word is the core's guest_* and broadcast_* strings.

private sealed interface TvGuestPage {
    data object Home : TvGuestPage
    data object Code : TvGuestPage
    data object Pair : TvGuestPage
    data class Channels(val pass: GuestPass) : TvGuestPage
    data class Watching(val pass: GuestPass, val channels: List<GuestLineup.Channel>, val index: Int) : TvGuestPage
}

/** The guest space, full screen; [onClose] goes back to where it was opened (the home picker or Account). */
@Composable
fun TvGuestSpace(
    space: GuestSpace,
    onClose: () -> Unit,
    /** The picture's player; null draws none (the JVM tests). */
    playerFor: (Context) -> GuestPlayer? = { GuestPlayer(it) },
) {
    var page by remember { mutableStateOf<TvGuestPage>(TvGuestPage.Home) }
    DisposableEffect(Unit) { onDispose { space.clearProblem() } }
    when (val p = page) {
        TvGuestPage.Home -> {
            BackHandler(onBack = onClose)
            TvGuestHome(space, onClose, onCode = { page = TvGuestPage.Code }, onPair = { page = TvGuestPage.Pair }, onOpen = { page = TvGuestPage.Channels(it) })
        }
        TvGuestPage.Code -> {
            BackHandler { space.clearProblem(); page = TvGuestPage.Home }
            TvGuestCode(space) { page = TvGuestPage.Channels(it) }
        }
        TvGuestPage.Pair -> {
            BackHandler { page = TvGuestPage.Home }
            TvGuestPair(space) { space.paired(it); page = TvGuestPage.Channels(it) }
        }
        is TvGuestPage.Channels -> {
            BackHandler { page = TvGuestPage.Home }
            TvGuestChannels(space, p.pass) { list, i -> page = TvGuestPage.Watching(p.pass, list, i) }
        }
        is TvGuestPage.Watching -> {
            BackHandler { page = TvGuestPage.Channels(space.store.pass(p.pass.publisher) ?: p.pass) }
            TvGuestWatching(space, p.pass, p.channels, p.index, playerFor)
        }
    }
}

@Composable
private fun GuestFrame(content: @Composable androidx.compose.foundation.layout.ColumnScope.() -> Unit) {
    LibraryBackdrop {
        Box(Modifier.fillMaxSize().padding(horizontal = Gutter, vertical = 32.dp), contentAlignment = Alignment.Center) {
            ReadingPanel(Modifier.width(860.dp), contentPadding = 32.dp, content = content)
        }
    }
}

@Composable
private fun TvGuestHome(space: GuestSpace, onClose: () -> Unit, onCode: () -> Unit, onPair: () -> Unit, onOpen: (GuestPass) -> Unit) {
    val saved by space.saved.collectAsState()
    val first = remember { FocusRequester() }
    LaunchedEffect(Unit) {
        space.reload()
        runCatching { first.requestFocus() }
    }
    GuestFrame {
        SectionTitle(stringResource(CoreR.string.guest_watch_broadcast), Modifier.testTag("tv-guest-home"))
        Text(stringResource(CoreR.string.guest_intro), style = MaterialTheme.typography.bodyLarge, color = MaktabaPalette.Muted, modifier = Modifier.padding(top = 8.dp))
        Row(Modifier.padding(top = 20.dp), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
            MaktabaButton(onClick = onCode, modifier = Modifier.focusRequester(first).testTag("tv-guest-add-code")) { Text(stringResource(CoreR.string.guest_add_code)) }
            MaktabaButton(onClick = onPair, modifier = Modifier.testTag("tv-guest-pair")) { Text(stringResource(CoreR.string.guest_pair_phone)) }
            MaktabaButton(onClick = onClose, modifier = Modifier.testTag("tv-guest-close")) { Text(stringResource(CoreR.string.guest_close)) }
        }
        Text(stringResource(CoreR.string.guest_saved), style = MaterialTheme.typography.titleSmall, color = MaktabaPalette.Gold, modifier = Modifier.padding(top = 24.dp, bottom = 8.dp))
        if (saved.isEmpty()) {
            Text(stringResource(CoreR.string.guest_no_saved), color = MaktabaPalette.Faint, modifier = Modifier.testTag("tv-guest-no-saved"))
        }
        LazyColumn(verticalArrangement = Arrangement.spacedBy(8.dp)) {
            items(saved, key = { it.publisher }) { p ->
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    ListItem(
                        selected = false,
                        onClick = { onOpen(p) },
                        modifier = Modifier.weight(1f).testTag("tv-guest-saved-${p.publisher}"),
                        headlineContent = { Text(p.label, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                        supportingContent = { Text(stringResource(CoreR.string.guest_until, day(p.expiresAt)), style = MaterialTheme.typography.labelMedium) },
                        colors = itemColors(),
                    )
                    MaktabaButton(onClick = { space.remove(p) }, modifier = Modifier.testTag("tv-guest-remove-${p.publisher}")) { Text(stringResource(CoreR.string.guest_remove)) }
                }
            }
        }
        Text(stringResource(CoreR.string.guest_free), style = MaterialTheme.typography.labelMedium, color = MaktabaPalette.Faint, modifier = Modifier.padding(top = 16.dp))
    }
}

@Composable
private fun TvGuestCode(space: GuestSpace, onRedeemed: (GuestPass) -> Unit) {
    var publisher by remember { mutableStateOf("") }
    var code by remember { mutableStateOf("") }
    val busy by space.busy.collectAsState()
    val problem by space.problem.collectAsState()
    val scope = rememberCoroutineScope()
    val watch = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { watch.requestFocus() } }
    fun go() = scope.launch { space.redeemCode(publisher, code)?.let(onRedeemed) }
    GuestFrame {
        SectionTitle(stringResource(CoreR.string.guest_add_code))
        Text(stringResource(CoreR.string.guest_enter_code), style = MaterialTheme.typography.bodyLarge, color = MaktabaPalette.Muted, modifier = Modifier.padding(top = 8.dp))
        // a handle and a code read left to right in Arabic too
        CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
            Column(Modifier.padding(top = 16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                TvTextField(
                    value = publisher,
                    onValueChange = { publisher = it; space.clearProblem() },
                    label = stringResource(CoreR.string.guest_publisher),
                    modifier = Modifier.fillMaxWidth().testTag("tv-guest-publisher"),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Next, autoCorrectEnabled = false),
                )
                TvTextField(
                    value = code,
                    onValueChange = { code = it.take(8); space.clearProblem() },
                    label = stringResource(CoreR.string.guest_code),
                    modifier = Modifier.fillMaxWidth().testTag("tv-guest-code"),
                    keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Characters, keyboardType = KeyboardType.Ascii, imeAction = ImeAction.Go, autoCorrectEnabled = false),
                    keyboardActions = KeyboardActions(onGo = { go() }),
                )
            }
        }
        Text(stringResource(CoreR.string.guest_publisher_hint), style = MaterialTheme.typography.labelMedium, color = MaktabaPalette.Faint, modifier = Modifier.padding(top = 4.dp))
        MaktabaButton(onClick = { go() }, enabled = !busy, modifier = Modifier.padding(top = 16.dp).focusRequester(watch).testTag("tv-guest-watch")) {
            Text(stringResource(if (busy) CoreR.string.guest_loading else CoreR.string.guest_watch))
        }
        problem?.let { Text(guestWords(it), color = MaktabaPalette.Gold, modifier = Modifier.padding(top = 12.dp).testTag("tv-guest-problem")) }
    }
}

/** The QR pairing: a phone opens the code's address and sends a pass; both screens show the same 4 characters. */
@Composable
private fun TvGuestPair(space: GuestSpace, onPaired: (GuestPass) -> Unit) {
    val run = remember { GuestPairingRun(space.client) }
    val pairing by run.pairing.collectAsState()
    val problem by run.problem.collectAsState()
    val renew = remember { FocusRequester() }
    LaunchedEffect(Unit) {
        runCatching { renew.requestFocus() }
        onPaired(run.run())
    }
    GuestFrame {
        SectionTitle(stringResource(CoreR.string.guest_pair_title))
        Row(Modifier.padding(top = 16.dp), horizontalArrangement = Arrangement.spacedBy(32.dp), verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.background(Color.White).padding(12.dp)) {
                val p = pairing
                if (p != null) QrCode(p.url, Modifier.testTag("tv-guest-qr")) else Box(Modifier.width(240.dp))
            }
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text(stringResource(CoreR.string.guest_scan_qr), style = MaterialTheme.typography.titleMedium, color = MaktabaPalette.Text)
                pairing?.let { p ->
                    Text(
                        stringResource(CoreR.string.guest_pair_check, p.code),
                        style = MaterialTheme.typography.headlineSmall,
                        color = MaktabaPalette.Gold,
                        modifier = Modifier.testTag("tv-guest-pair-code"),
                    )
                } ?: Text(stringResource(CoreR.string.guest_loading), color = MaktabaPalette.Muted)
                problem?.let { Text(guestWords(it), color = MaktabaPalette.Gold, modifier = Modifier.testTag("tv-guest-problem")) }
                MaktabaButton(onClick = run::renew, modifier = Modifier.focusRequester(renew).testTag("tv-guest-pair-new")) { Text(stringResource(CoreR.string.guest_pair_new)) }
            }
        }
    }
}

@Composable
private fun TvGuestChannels(space: GuestSpace, pass: GuestPass, onWatch: (List<GuestLineup.Channel>, Int) -> Unit) {
    var lineup by remember { mutableStateOf<Result<GuestLineup>?>(null) }
    var reload by remember { mutableIntStateOf(0) }
    val first = remember { FocusRequester() }
    LaunchedEffect(pass.publisher, reload) {
        lineup = null
        lineup = space.lineup(pass)
    }
    val l = lineup
    val name = l?.getOrNull()?.publisher?.name?.takeIf { it.isNotBlank() } ?: pass.label
    GuestFrame {
        SectionTitle(stringResource(CoreR.string.guest_on_maktaba, name), Modifier.testTag("tv-guest-channels"))
        when {
            l == null -> Text(stringResource(CoreR.string.guest_loading), color = MaktabaPalette.Muted, modifier = Modifier.padding(top = 16.dp))
            l.isFailure -> {
                val key = (l.exceptionOrNull() as? GuestError)?.messageKey ?: "site.error"
                Text(guestWords(key), color = MaktabaPalette.Gold, modifier = Modifier.padding(top = 16.dp).testTag("tv-guest-state"))
                val retry = remember { FocusRequester() }
                LaunchedEffect(Unit) { runCatching { retry.requestFocus() } }
                MaktabaButton(onClick = { reload++ }, modifier = Modifier.padding(top = 12.dp).focusRequester(retry)) { Text(stringResource(CoreR.string.guest_try_again)) }
            }
            else -> {
                val channels = l.getOrThrow().channels.sortedBy { it.number }
                if (channels.isEmpty()) {
                    Text(stringResource(CoreR.string.guest_no_channels), color = MaktabaPalette.Muted, modifier = Modifier.padding(top = 16.dp))
                } else {
                    LaunchedEffect(channels) { runCatching { first.requestFocus() } }
                    LazyColumn(Modifier.padding(top = 16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        items(channels.size, key = { channels[it].id }) { i ->
                            val c = channels[i]
                            ListItem(
                                selected = false,
                                onClick = { onWatch(channels, i) },
                                modifier = (if (i == 0) Modifier.focusRequester(first) else Modifier).testTag("tv-guest-channel-${c.id}"),
                                overlineContent = { Text("${c.number}") },
                                headlineContent = { Text(c.name, style = MaterialTheme.typography.titleMedium) },
                                supportingContent = {
                                    Column {
                                        c.now?.let { Text(stringResource(CoreR.string.guest_now) + " · " + it.title + (it.end?.let { e -> " · " + stringResource(CoreR.string.guest_until, time(e)) } ?: "")) }
                                        c.next?.let { Text(stringResource(CoreR.string.guest_next) + " · " + it.title) }
                                    }
                                },
                                colors = itemColors(),
                            )
                        }
                    }
                }
            }
        }
        Text(stringResource(CoreR.string.guest_free), style = MaterialTheme.typography.labelMedium, color = MaktabaPalette.Faint, modifier = Modifier.padding(top = 16.dp))
    }
}

/** The player: up and down (and the remote's channel keys) change the channel; Back returns to the channels. */
@Composable
private fun TvGuestWatching(space: GuestSpace, pass: GuestPass, channels: List<GuestLineup.Channel>, start: Int, playerFor: (Context) -> GuestPlayer?) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val watch = remember(pass.publisher) { GuestWatch(pass, space.client, space.store, scope) }
    val player = remember { playerFor(context) }
    var index by remember { mutableIntStateOf(start.coerceIn(0, channels.lastIndex)) }
    var failed by remember { mutableStateOf(false) }
    val shown by watch.shown.collectAsState()
    val focus = remember { FocusRequester() }
    DisposableEffect(Unit) {
        player?.onFailed = { failed = true }
        onDispose {
            watch.stop()
            player?.release()
        }
    }
    LaunchedEffect(index) {
        failed = false
        watch.play(channels[index].id)
        runCatching { focus.requestFocus() }
    }
    LaunchedEffect(shown) { player?.show(shown) }
    fun tune(step: Int) {
        if (channels.size > 1) index = (index + step + channels.size) % channels.size
    }
    val channel = channels[index]
    val key = if (failed) "site.error" else shown.messageKey
    Box(
        Modifier.fillMaxSize().background(Color.Black)
            .onPreviewKeyEvent { e ->
                if (e.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                when (e.key) {
                    Key.DirectionUp, Key.ChannelUp -> { tune(1); true }
                    Key.DirectionDown, Key.ChannelDown -> { tune(-1); true }
                    else -> false
                }
            }
            .testTag("tv-guest-player"),
    ) {
        player?.let { GuestVideo(it, Modifier.fillMaxSize()) }
        Column(Modifier.align(Alignment.TopStart).padding(horizontal = Gutter, vertical = 28.dp)) {
            Text("${channel.number} · ${channel.name}", style = MaterialTheme.typography.headlineSmall, color = Color.White, modifier = Modifier.testTag("tv-guest-playing-${channel.id}"))
            channel.now?.let { Text(it.title, style = MaterialTheme.typography.titleMedium, color = Color.White.copy(alpha = 0.8f)) }
            channel.now?.attribution?.let { a ->
                val by = a.by?.takeIf { it.isNotBlank() }?.let { stringResource(CoreR.string.guest_by, it) }
                val line = listOfNotNull(a.title?.takeIf { it.isNotBlank() }, by).joinToString(" · ") + (a.licence?.takeIf { it.isNotBlank() }?.let { " ($it)" } ?: "")
                if (line.isNotBlank()) Text(line, style = MaterialTheme.typography.labelLarge, color = Color.White.copy(alpha = 0.8f), modifier = Modifier.testTag("tv-guest-credit"))
            }
        }
        Column(Modifier.align(Alignment.Center).padding(32.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(16.dp)) {
            if (key.isNotEmpty()) {
                Text(guestWords(key), style = MaterialTheme.typography.titleLarge, color = Color.White, modifier = Modifier.width(720.dp).testTag("tv-guest-state"))
            }
            if (key.isNotEmpty() && (failed || shown.canRetry)) {
                MaktabaButton(onClick = { failed = false; watch.retry() }, modifier = Modifier.focusRequester(focus).testTag("tv-guest-retry")) {
                    Text(stringResource(CoreR.string.guest_try_again))
                }
            } else {
                // the remote's keys come here while the picture plays
                Box(Modifier.focusRequester(focus).focusable())
            }
        }
        if (channels.size > 1) {
            Text(
                stringResource(CoreR.string.guest_switch_hint_tv),
                style = MaterialTheme.typography.labelLarge,
                color = Color.White.copy(alpha = 0.7f),
                modifier = Modifier.align(Alignment.BottomCenter).padding(24.dp),
            )
        }
    }
}

@Composable
private fun itemColors() = ListItemDefaults.colors(
    containerColor = MaktabaPalette.Surface,
    focusedContainerColor = MaktabaPalette.Gold,
    focusedContentColor = MaktabaPalette.OnGold,
)

@Composable
private fun time(at: Instant): String {
    val locale = androidx.compose.ui.platform.LocalConfiguration.current.locales[0]
    return DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT).withLocale(locale).withZone(ZoneId.systemDefault()).format(at)
}

@Composable
private fun day(epochSec: Long): String {
    val locale = androidx.compose.ui.platform.LocalConfiguration.current.locales[0]
    return DateTimeFormatter.ofLocalizedDate(FormatStyle.MEDIUM).withLocale(locale).withZone(ZoneId.systemDefault()).format(Instant.ofEpochSecond(epochSec))
}

/**
 * Watch a broadcast is shut by a parental limit (story 50.27): opened under one, or closed because
 * one turned on while the guest space was open. One button, which has focus.
 */
@Composable
fun TvGuestNotice(key: String, onClose: () -> Unit) {
    val close = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { close.requestFocus() } }
    androidx.compose.ui.window.Dialog(onDismissRequest = onClose) {
        ReadingPanel(Modifier.width(640.dp), contentPadding = 32.dp) {
            SectionTitle(stringResource(CoreR.string.guest_watch_broadcast))
            Text(
                guestWords(key), style = MaterialTheme.typography.bodyLarge,
                color = MaktabaPalette.Text, modifier = Modifier.padding(top = 12.dp).testTag("guest-refused"),
            )
            MaktabaButton(onClick = onClose, modifier = Modifier.padding(top = 16.dp).focusRequester(close).testTag("guest-refused-close")) {
                Text(stringResource(CoreR.string.guest_close))
            }
        }
    }
}
