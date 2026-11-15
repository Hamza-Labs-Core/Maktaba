package com.hamzalabs.maktaba.player.guest

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.LiveTv
import androidx.compose.material.icons.outlined.QrCodeScanner
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.R as CoreR
import com.hamzalabs.maktaba.core.guest.GuestAccess
import com.hamzalabs.maktaba.core.guest.GuestDoor
import com.hamzalabs.maktaba.core.guest.GuestGate
import com.hamzalabs.maktaba.core.guest.GuestLineup
import com.hamzalabs.maktaba.core.guest.GuestPass
import com.hamzalabs.maktaba.core.guest.GuestPlayer
import com.hamzalabs.maktaba.core.guest.GuestProgramme
import com.hamzalabs.maktaba.core.guest.GuestSpace
import com.hamzalabs.maktaba.core.guest.GuestVideo
import com.hamzalabs.maktaba.core.guest.GuestWatch
import com.hamzalabs.maktaba.core.guest.guestWords
import com.hamzalabs.maktaba.core.ui.QrScanner
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import kotlin.math.abs

// Story 50.27 §2 on the phone, foldable and tablet: Watch a broadcast, a space apart from the
// app's homes and accounts. The saved broadcasts, the ways in (the publisher's handle and an MK-
// code, an invite link pasted or opened, a scanned QR code), a publisher's channels with now and
// next, and the player, where a vertical swipe changes the channel. Every word is the core's
// guest_* and broadcast_* strings, in English and Arabic.

/** Where the guest space is. */
private sealed interface GuestPage {
    data object Home : GuestPage
    data class Channels(val pass: GuestPass) : GuestPage
    data class Watching(val pass: GuestPass, val channels: List<GuestLineup.Channel>, val index: Int) : GuestPage
}

/**
 * The guest space as [door] says, over the app: open, an invite's link held while the signed-in
 * home has not said its limit, or refused under a parental limit. It follows [gate], so an open
 * guest space (and its player) closes the moment a limit turns on.
 */
@Composable
fun GuestDoorHost(
    door: GuestDoor,
    gate: GuestGate,
    space: GuestSpace,
    open: @Composable (invite: String?, onClose: () -> Unit) -> Unit = { invite, close -> GuestSpaceScreen(space, invite, close) },
) {
    val access by gate.access.collectAsState(initial = remember(gate) { gate.now() })
    val shown by door.shown.collectAsState()
    // whenever the answer or what is shown changes: nothing stays open that the answer shuts
    LaunchedEffect(door, access, shown) { door.update(access) }
    when (val shown = shown) {
        GuestDoor.Shown.Shut -> Unit
        is GuestDoor.Shown.Open -> open(shown.invite, door::close)
        is GuestDoor.Shown.Held -> GuestDoorNotice("guest.held", "guest-held", door::close)
        GuestDoor.Shown.Refused -> GuestDoorNotice("guest.limited", "guest-refused", door::close)
    }
}

@Composable
private fun GuestDoorNotice(key: String, tag: String, onClose: () -> Unit) {
    AlertDialog(
        onDismissRequest = onClose,
        title = { Text(stringResource(CoreR.string.guest_watch_broadcast)) },
        text = { Text(guestWords(key), Modifier.testTag(tag)) },
        confirmButton = { TextButton(onClick = onClose, modifier = Modifier.testTag("$tag-close")) { Text(stringResource(CoreR.string.guest_close)) } },
    )
}

/** Watch a broadcast for the entry (the home picker, Profile): null while [access] keeps it shut. */
fun guestEntry(door: GuestDoor, access: GuestAccess): (() -> Unit)? =
    if (access == GuestAccess.Allowed) ({ door.request(null, GuestAccess.Allowed) }) else null

/**
 * The guest space, full screen: from the home picker, Profile, or an invite's link ([invite], the
 * token: redeemed at once). [onClose] leaves it for the app.
 */
@Composable
fun GuestSpaceScreen(
    space: GuestSpace,
    invite: String?,
    onClose: () -> Unit,
    scanner: @Composable (onScan: (String) -> Unit, onClose: () -> Unit) -> Unit = { onScan, close ->
        QrScanner(stringResource(CoreR.string.guest_scan), stringResource(CoreR.string.guest_scan_hint), onScan, close)
    },
    /** The picture's player; null draws none (the JVM tests, which check the screens and the edge's answers). */
    playerFor: (android.content.Context) -> GuestPlayer? = { GuestPlayer(it) },
) {
    var page by remember { mutableStateOf<GuestPage>(GuestPage.Home) }
    LaunchedEffect(invite) {
        if (invite != null) space.redeemInvite(invite)?.let { page = GuestPage.Channels(it) }
    }
    Surface(Modifier.fillMaxSize()) {
        when (val p = page) {
            GuestPage.Home -> {
                BackHandler(onBack = onClose)
                GuestHome(
                    space, onClose, scanner,
                    onOpen = { page = GuestPage.Channels(it) },
                    onRedeemed = { page = GuestPage.Channels(it) },
                )
            }
            is GuestPage.Channels -> {
                BackHandler { page = GuestPage.Home }
                GuestChannels(space, p.pass, onBack = { page = GuestPage.Home }, onWatch = { list, i -> page = GuestPage.Watching(p.pass, list, i) })
            }
            is GuestPage.Watching -> {
                BackHandler { page = GuestPage.Channels(p.pass) }
                GuestWatching(space, p.pass, p.channels, p.index, playerFor, onBack = { page = GuestPage.Channels(space.store.pass(p.pass.publisher) ?: p.pass) })
            }
        }
    }
    // the next visit starts with no refusal shown
    DisposableEffect(Unit) { onDispose { space.clearProblem() } }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun GuestHome(
    space: GuestSpace,
    onClose: () -> Unit,
    scanner: @Composable (onScan: (String) -> Unit, onClose: () -> Unit) -> Unit,
    onOpen: (GuestPass) -> Unit,
    onRedeemed: (GuestPass) -> Unit,
) {
    val saved by space.saved.collectAsState()
    val busy by space.busy.collectAsState()
    val problem by space.problem.collectAsState()
    var publisher by rememberSaveable { mutableStateOf("") }
    var code by rememberSaveable { mutableStateOf("") }
    var link by rememberSaveable { mutableStateOf("") }
    var scanning by rememberSaveable { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    LaunchedEffect(Unit) { space.reload() }

    if (scanning) {
        BackHandler { scanning = false }
        scanner({ text ->
            scanning = false
            scope.launch { space.redeemInvite(text)?.let(onRedeemed) }
        }, { scanning = false })
        return
    }

    fun byCode() = scope.launch { space.redeemCode(publisher, code)?.let(onRedeemed) }
    fun byLink() = scope.launch { space.redeemInvite(link)?.let(onRedeemed) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(CoreR.string.guest_watch_broadcast)) },
                navigationIcon = {
                    IconButton(onClick = onClose, modifier = Modifier.testTagGuest("guest-close")) {
                        Icon(Icons.Outlined.Close, stringResource(CoreR.string.guest_close))
                    }
                },
            )
        },
        modifier = Modifier.testTagGuest("guest-home"),
    ) { pad ->
        LazyColumn(
            Modifier.fillMaxSize().padding(pad),
            contentPadding = PaddingValues(horizontal = 20.dp, vertical = 12.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            item { Text(stringResource(CoreR.string.guest_intro), style = MaterialTheme.typography.bodyLarge, modifier = Modifier.widthIn(max = 560.dp).fillMaxWidth()) }

            item { Heading(stringResource(CoreR.string.guest_saved)) }
            if (saved.isEmpty()) {
                item { Text(stringResource(CoreR.string.guest_no_saved), color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.widthIn(max = 560.dp).fillMaxWidth().testTagGuest("guest-no-saved")) }
            }
            items(saved, key = { it.publisher }) { p ->
                ListItem(
                    headlineContent = { Text(p.label) },
                    supportingContent = { Text(stringResource(CoreR.string.guest_until, day(p.expiresAt))) },
                    leadingContent = { Icon(Icons.Outlined.LiveTv, null) },
                    trailingContent = {
                        IconButton(onClick = { space.remove(p) }, modifier = Modifier.testTagGuest("guest-remove-${p.publisher}")) {
                            Icon(Icons.Outlined.Delete, stringResource(CoreR.string.guest_remove_named, p.label))
                        }
                    },
                    modifier = Modifier.widthIn(max = 560.dp).fillMaxWidth().clickable { onOpen(p) }.testTagGuest("guest-saved-${p.publisher}"),
                )
            }

            item { Heading(stringResource(CoreR.string.guest_add_code)) }
            item {
                Column(Modifier.widthIn(max = 560.dp).fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(stringResource(CoreR.string.guest_enter_code), style = MaterialTheme.typography.bodyMedium)
                    OutlinedTextField(
                        value = publisher,
                        onValueChange = { publisher = it; space.clearProblem() },
                        label = { Text(stringResource(CoreR.string.guest_publisher)) },
                        supportingText = { Text(stringResource(CoreR.string.guest_publisher_hint)) },
                        singleLine = true,
                        enabled = !busy,
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Next, autoCorrectEnabled = false),
                        modifier = Modifier.fillMaxWidth().testTagGuest("guest-publisher"),
                    )
                    OutlinedTextField(
                        value = code,
                        onValueChange = { code = it.take(8); space.clearProblem() },
                        label = { Text(stringResource(CoreR.string.guest_code)) },
                        singleLine = true,
                        enabled = !busy,
                        keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Characters, keyboardType = KeyboardType.Ascii, imeAction = ImeAction.Go, autoCorrectEnabled = false),
                        keyboardActions = KeyboardActions(onGo = { byCode() }),
                        modifier = Modifier.fillMaxWidth().testTagGuest("guest-code"),
                    )
                    Button(onClick = { byCode() }, enabled = !busy && publisher.isNotBlank() && code.isNotBlank(), modifier = Modifier.fillMaxWidth().testTagGuest("guest-watch-code")) {
                        Text(stringResource(CoreR.string.guest_watch))
                    }
                }
            }

            item { Heading(stringResource(CoreR.string.guest_add_invite)) }
            item {
                Column(Modifier.widthIn(max = 560.dp).fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedTextField(
                        value = link,
                        onValueChange = { link = it; space.clearProblem() },
                        label = { Text(stringResource(CoreR.string.guest_invite)) },
                        supportingText = { Text(stringResource(CoreR.string.guest_invite_hint)) },
                        singleLine = true,
                        enabled = !busy,
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Go, autoCorrectEnabled = false),
                        keyboardActions = KeyboardActions(onGo = { byLink() }),
                        modifier = Modifier.fillMaxWidth().testTagGuest("guest-link"),
                    )
                    Button(onClick = { byLink() }, enabled = !busy && link.isNotBlank(), modifier = Modifier.fillMaxWidth().testTagGuest("guest-watch-link")) {
                        Text(stringResource(CoreR.string.guest_watch))
                    }
                    OutlinedButton(onClick = { space.clearProblem(); scanning = true }, enabled = !busy, modifier = Modifier.fillMaxWidth().testTagGuest("guest-scan")) {
                        Icon(Icons.Outlined.QrCodeScanner, null, Modifier.size(18.dp))
                        Spacer(Modifier.width(8.dp))
                        Text(stringResource(CoreR.string.guest_scan))
                    }
                }
            }
            if (busy) item { CircularProgressIndicator(Modifier.testTagGuest("guest-busy")) }
            problem?.let { key ->
                item {
                    Text(
                        guestWords(key),
                        color = MaterialTheme.colorScheme.error,
                        modifier = Modifier.widthIn(max = 560.dp).fillMaxWidth().testTagGuest("guest-problem").semantics { liveRegion = LiveRegionMode.Polite },
                    )
                }
            }
            item { Text(stringResource(CoreR.string.guest_free), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun GuestChannels(space: GuestSpace, pass: GuestPass, onBack: () -> Unit, onWatch: (List<GuestLineup.Channel>, Int) -> Unit) {
    var lineup by remember { mutableStateOf<Result<GuestLineup>?>(null) }
    var reload by remember { mutableIntStateOf(0) }
    LaunchedEffect(pass.publisher, reload) {
        lineup = null
        lineup = space.lineup(pass)
    }
    val name = lineup?.getOrNull()?.publisher?.name?.takeIf { it.isNotBlank() } ?: pass.label
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(CoreR.string.guest_on_maktaba, name)) },
                navigationIcon = { IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Outlined.ArrowBack, stringResource(CoreR.string.guest_back)) } },
            )
        },
        modifier = Modifier.testTagGuest("guest-channels"),
    ) { pad ->
        val l = lineup
        when {
            l == null -> Box(Modifier.fillMaxSize().padding(pad), contentAlignment = Alignment.Center) {
                CircularProgressIndicator()
            }
            l.isFailure -> Problem(
                (l.exceptionOrNull() as? com.hamzalabs.maktaba.core.guest.GuestError)?.messageKey ?: "site.error",
                retry = true, onRetry = { reload++ }, modifier = Modifier.padding(pad),
            )
            else -> {
                val channels = l.getOrThrow().channels.sortedBy { it.number }
                if (channels.isEmpty()) {
                    Box(Modifier.fillMaxSize().padding(pad).padding(24.dp), contentAlignment = Alignment.Center) {
                        Text(stringResource(CoreR.string.guest_no_channels), textAlign = TextAlign.Center, modifier = Modifier.testTagGuest("guest-no-channels"))
                    }
                } else {
                    LazyColumn(Modifier.fillMaxSize().padding(pad), contentPadding = PaddingValues(vertical = 8.dp)) {
                        item { Heading(stringResource(CoreR.string.guest_channels), Modifier.padding(horizontal = 16.dp)) }
                        items(channels.size, key = { channels[it].id }) { i ->
                            val c = channels[i]
                            ListItem(
                                overlineContent = { Text("${c.number}") },
                                headlineContent = { Text(c.name) },
                                supportingContent = { NowNext(c) },
                                modifier = Modifier.clickable { onWatch(channels, i) }.testTagGuest("guest-channel-${c.id}"),
                            )
                            HorizontalDivider()
                        }
                        item {
                            Text(
                                stringResource(CoreR.string.guest_free),
                                style = MaterialTheme.typography.labelMedium,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                modifier = Modifier.padding(16.dp),
                            )
                        }
                    }
                }
            }
        }
    }
}

/** A channel's now (until when) and next. */
@Composable
private fun NowNext(c: GuestLineup.Channel) {
    Column {
        c.now?.let { Text(stringResource(CoreR.string.guest_now) + " · " + it.title + (it.end?.let { e -> " · " + stringResource(CoreR.string.guest_until, time(e)) } ?: "")) }
        c.next?.let { Text(stringResource(CoreR.string.guest_next) + " · " + it.title, color = MaterialTheme.colorScheme.onSurfaceVariant) }
    }
}

/** The player, full screen and dark: a vertical swipe changes the channel; Back returns to the channels. */
@Composable
private fun GuestWatching(
    space: GuestSpace,
    pass: GuestPass,
    channels: List<GuestLineup.Channel>,
    start: Int,
    playerFor: (android.content.Context) -> GuestPlayer?,
    onBack: () -> Unit,
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val watch = remember(pass.publisher) { GuestWatch(pass, space.client, space.store, scope) }
    val player = remember { playerFor(context) }
    var index by rememberSaveable { mutableIntStateOf(start.coerceIn(0, channels.lastIndex)) }
    var failed by remember { mutableStateOf(false) }
    val shown by watch.shown.collectAsState()
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
    }
    LaunchedEffect(shown) { player?.show(shown) }
    val threshold = with(LocalDensity.current) { 72.dp.toPx() }
    val channel = channels[index]
    Box(
        Modifier.fillMaxSize().background(Color.Black)
            .pointerInput(channels.size) {
                var dragged = 0f
                detectVerticalDragGestures(
                    onDragStart = { dragged = 0f },
                    onDragEnd = {
                        // up: the next channel; down: the one before (wrapping, as a TV's remote does)
                        if (abs(dragged) > threshold && channels.size > 1) {
                            index = (index + (if (dragged < 0) 1 else -1) + channels.size) % channels.size
                        }
                    },
                    onVerticalDrag = { _, dy -> dragged += dy },
                )
            }
            .testTagGuest("guest-player"),
    ) {
        player?.let { GuestVideo(it, Modifier.fillMaxSize()) }
        Column(Modifier.align(Alignment.TopStart).fillMaxWidth().safeDrawingPadding().padding(8.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Outlined.ArrowBack, stringResource(CoreR.string.guest_back), tint = Color.White) }
                Column(Modifier.weight(1f)) {
                    Text("${channel.number} · ${channel.name}", color = Color.White, style = MaterialTheme.typography.titleMedium, modifier = Modifier.testTagGuest("guest-playing-${channel.id}"))
                    channel.now?.let { Text(it.title, color = Color.White.copy(alpha = 0.8f), style = MaterialTheme.typography.bodyMedium) }
                }
            }
            channel.now?.attribution?.let { a -> Credit(a) }
        }
        val key = if (failed) "site.error" else shown.messageKey
        when {
            key.isNotEmpty() -> Problem(
                key,
                retry = failed || shown.canRetry,
                onRetry = { failed = false; watch.retry() },
                modifier = Modifier.align(Alignment.Center),
                onDark = true,
            )
            shown.live == null -> CircularProgressIndicator(Modifier.align(Alignment.Center), color = Color.White)
        }
        if (channels.size > 1) {
            Text(
                stringResource(CoreR.string.guest_switch_hint),
                color = Color.White.copy(alpha = 0.7f),
                style = MaterialTheme.typography.labelMedium,
                modifier = Modifier.align(Alignment.BottomCenter).safeDrawingPadding().padding(16.dp),
            )
        }
    }
}

/** A CC BY programme's credit (50.23): "Title · by Name (CC BY 4.0)". */
@Composable
private fun Credit(a: GuestProgramme.Attribution) {
    val by = a.by?.takeIf { it.isNotBlank() }?.let { stringResource(CoreR.string.guest_by, it) }
    val line = listOfNotNull(a.title?.takeIf { it.isNotBlank() }, by).joinToString(" · ") + (a.licence?.takeIf { it.isNotBlank() }?.let { " ($it)" } ?: "")
    if (line.isNotBlank()) Text(line, color = Color.White.copy(alpha = 0.8f), style = MaterialTheme.typography.labelMedium, modifier = Modifier.padding(start = 56.dp).testTagGuest("guest-credit"))
}

@Composable
private fun Problem(key: String, retry: Boolean, onRetry: () -> Unit, modifier: Modifier = Modifier, onDark: Boolean = false) {
    Column(
        modifier.padding(24.dp).semantics { liveRegion = LiveRegionMode.Polite },
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text(
            guestWords(key),
            textAlign = TextAlign.Center,
            color = if (onDark) Color.White else MaterialTheme.colorScheme.onSurface,
            modifier = Modifier.widthIn(max = 480.dp).testTagGuest("guest-state"),
        )
        if (retry) TextButton(onClick = onRetry, modifier = Modifier.testTagGuest("guest-retry")) { Text(stringResource(CoreR.string.guest_try_again)) }
    }
}

@Composable
private fun Heading(text: String, modifier: Modifier = Modifier) {
    Text(
        text,
        style = MaterialTheme.typography.titleMedium,
        color = MaterialTheme.colorScheme.primary,
        modifier = modifier.widthIn(max = 560.dp).fillMaxWidth().semantics { heading() },
    )
}

private fun Modifier.testTagGuest(tag: String) = testTag(tag)

/** A time of day in the app's language. */
@Composable
private fun time(at: Instant): String {
    val locale = androidx.compose.ui.platform.LocalConfiguration.current.locales[0]
    return DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT).withLocale(locale).withZone(ZoneId.systemDefault()).format(at)
}

/** A day in the app's language. */
@Composable
private fun day(epochSec: Long): String {
    val locale = androidx.compose.ui.platform.LocalConfiguration.current.locales[0]
    return DateTimeFormatter.ofLocalizedDate(FormatStyle.MEDIUM).withLocale(locale).withZone(ZoneId.systemDefault()).format(Instant.ofEpochSecond(epochSec))
}
