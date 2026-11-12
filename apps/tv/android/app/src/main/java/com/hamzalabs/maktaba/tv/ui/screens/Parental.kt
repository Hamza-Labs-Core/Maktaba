package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material.icons.outlined.Schedule
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Icon
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.R as CoreR
import com.hamzalabs.maktaba.core.api.MORE_TIME_MINUTES
import com.hamzalabs.maktaba.core.api.MyParental
import com.hamzalabs.maktaba.core.api.MyTime
import com.hamzalabs.maktaba.core.api.TimeRefusal
import com.hamzalabs.maktaba.core.api.isParentPin
import com.hamzalabs.maktaba.core.parental.AskState
import com.hamzalabs.maktaba.core.parental.ParentalState
import com.hamzalabs.maktaba.core.parental.PinState
import com.hamzalabs.maktaba.core.parental.TimeView
import com.hamzalabs.maktaba.core.parental.TimeWarnings
import androidx.lifecycle.repeatOnLifecycle
import com.hamzalabs.maktaba.core.ui.ParentHelp
import com.hamzalabs.maktaba.core.ui.limitLine
import com.hamzalabs.maktaba.core.ui.nextWords
import com.hamzalabs.maktaba.core.ui.timeWords
import com.hamzalabs.maktaba.core.ui.timesUpTitle
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton
import com.hamzalabs.maktaba.tv.ui.components.TvTextField
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.time.Instant

// Parental controls on the TV (Epic 57 stories 57.05, 57.06, 57.10, 57.15): the same words as
// the phone's (the core's strings), laid out for a remote: every action is a button in a row,
// the first focused. The home filters, counts and refuses; these screens say what it answered.

/** The time left once 15 minutes or fewer remain, the five- and one-minute warnings, and "cannot check". */
@Composable
fun TvTimeOverlay(time: MyTime?, cannotCheck: Boolean, modifier: Modifier = Modifier) {
    val res = LocalContext.current.resources
    val warnings = remember { TimeWarnings() }
    var warned by remember { mutableStateOf<Long?>(null) }
    LaunchedEffect(time) {
        val line = warnings.crossed(time) ?: return@LaunchedEffect
        // the time really left, not the line crossed (a video opened with two minutes left)
        val seconds = TimeView.warningSeconds(line, time)
        warned = seconds
        delay(TimeView.WARNING_SHOWN_MS)
        if (warned == seconds) warned = null
    }
    Column(modifier, horizontalAlignment = Alignment.End, verticalArrangement = Arrangement.spacedBy(8.dp)) {
        val chip = Modifier.background(Color.Black.copy(alpha = 0.6f), RoundedCornerShape(8.dp)).padding(horizontal = 14.dp, vertical = 8.dp)
        warned?.let {
            Text(
                stringResource(CoreR.string.parental_time_warning, res.timeWords(it)),
                style = MaterialTheme.typography.titleMedium,
                color = MaktabaPalette.Gold,
                modifier = chip.testTag("parental-time-warning").semantics { liveRegion = LiveRegionMode.Assertive },
            )
        }
        val left = TimeView.left(time)
        if (left != null && TimeView.showsInPlayer(time)) {
            Text(
                stringResource(CoreR.string.parental_time_left, res.timeWords(left)),
                style = MaterialTheme.typography.titleSmall,
                color = MaktabaPalette.Text,
                modifier = chip.testTag("parental-time-left"),
            )
        }
        if (cannotCheck) {
            Text(
                stringResource(CoreR.string.parental_time_cannot_check),
                style = MaterialTheme.typography.titleSmall,
                color = MaktabaPalette.Text,
                modifier = chip.testTag("parental-cannot-check"),
            )
        }
    }
}

/** Home's line for a restricted account: the time left today, or that it is over and when it opens again. */
@Composable
fun TvHomeTime(parental: ParentalState, modifier: Modifier = Modifier) {
    val mine by parental.mine.collectAsState()
    val time by parental.time.collectAsState()
    // not while the TV is in standby or shows another app
    com.hamzalabs.maktaba.core.ui.FollowTimeWhileShown(parental, on = mine.restricted)
    if (!mine.restricted) return
    val res = LocalContext.current.resources
    val over = TimeView.refusal(time)
    val left = TimeView.left(time)
    val text = when {
        over != null -> stringResource(timesUpTitle(over.reason)) +
            (over.nextAllowedAt?.let { "\n" + stringResource(CoreR.string.parental_time_next, nextWords(it)) } ?: "")
        left != null -> stringResource(CoreR.string.parental_time_left, res.timeWords(left))
        else -> return
    }
    Text(
        text,
        style = MaterialTheme.typography.titleSmall,
        color = MaktabaPalette.Text,
        textAlign = TextAlign.End,
        modifier = modifier.background(MaktabaPalette.Panel, RoundedCornerShape(8.dp)).padding(horizontal = 14.dp, vertical = 8.dp).testTag("parental-home-time"),
    )
}

/**
 * Ask a parent, and a parent's PIN when someone set one. For time, asking offers 15, 30 or 60
 * minutes. When the home says there is nothing to ask for (a hidden item is a missing one) both go.
 */
@Composable
private fun TvParentHelp(help: ParentHelp, onUnlocked: () -> Unit) {
    val scope = rememberCoroutineScope()
    val res = LocalContext.current.resources
    val asked by help.ask.state.collectAsState()
    val holders by help.pin.holders.collectAsState()
    val pinState by help.pin.state.collectAsState()
    var choosing by remember { mutableStateOf(false) }
    var pinOpen by remember { mutableStateOf(false) }
    var who by remember { mutableStateOf<String?>(null) }
    var pin by remember { mutableStateOf("") }
    var minutes by remember { mutableStateOf(MORE_TIME_MINUTES[1]) }
    LaunchedEffect(help) { help.pin.load() }
    LaunchedEffect(holders) { if (holders.size == 1) who = holders.single().id }
    // once per unlock: the state is put back, so showing this screen again never asks by itself
    LaunchedEffect(pinState) {
        if (pinState == PinState.Unlocked) {
            help.pin.again()
            onUnlocked()
        }
    }
    // a lock counts down on screen and clears by itself (story 57.06)
    LaunchedEffect(help, pinState is PinState.Locked) { help.pin.waitOutLock() }
    // the request this screen is about, as the home has it: followed while it waits and the TV
    // shows the app; a parent's yes opens what was asked for, once
    val owner = androidx.lifecycle.compose.LocalLifecycleOwner.current
    LaunchedEffect(help, asked is AskState.Waiting, owner) {
        owner.repeatOnLifecycle(androidx.lifecycle.Lifecycle.State.STARTED) { help.ask.follow() }
    }
    LaunchedEffect(asked) {
        if (asked is AskState.Approved) {
            help.ask.done()
            onUnlocked()
        }
    }
    val nothing = asked == AskState.NothingToAsk || pinState == PinState.NothingToUnlock
    if (nothing) return
    Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
        if (asked !is AskState.Waiting) {
            MaktabaButton(
                onClick = { if (help.forTime) choosing = !choosing else scope.launch { help.ask.ask() } },
                enabled = asked != AskState.Sending,
                modifier = Modifier.testTag("parental-ask"),
            ) {
                Text(
                    stringResource(
                        when {
                            asked is AskState.Denied -> CoreR.string.parental_ask_again
                            help.forTime -> CoreR.string.parental_time_ask_more
                            else -> CoreR.string.parental_blocked_ask
                        },
                    ),
                )
            }
        }
        if (holders.isNotEmpty()) {
            MaktabaButton(onClick = { help.pin.again(); pinOpen = !pinOpen }, modifier = Modifier.testTag("parental-pin")) {
                Text(stringResource(CoreR.string.parental_blocked_pin))
            }
        }
    }
    if (choosing && help.forTime && asked !is AskState.Waiting) {
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            MORE_TIME_MINUTES.forEach { m ->
                MaktabaButton(onClick = { choosing = false; scope.launch { help.ask.ask(m) } }, modifier = Modifier.testTag("parental-ask-$m")) {
                    Text(res.timeWords(m * 60L))
                }
            }
        }
    }
    val askLine = when (asked) {
        is AskState.Waiting -> stringResource(CoreR.string.parental_ask_pending) + "\n" + stringResource(CoreR.string.parental_ask_sent)
        AskState.TooMany -> stringResource(CoreR.string.parental_ask_too_many)
        AskState.Failed -> stringResource(CoreR.string.parental_ask_failed)
        is AskState.Denied -> stringResource(CoreR.string.parental_ask_denied)
        else -> null
    }
    if (askLine != null) {
        Text(askLine, style = MaterialTheme.typography.titleSmall, color = MaktabaPalette.Muted, textAlign = TextAlign.Center, modifier = Modifier.testTag("parental-ask-state"))
    }
    if (pinOpen && holders.isNotEmpty()) {
        Column(Modifier.testTag("parental-pin-panel"), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text(
                stringResource(if (who == null) CoreR.string.parental_pin_choose else CoreR.string.parental_pin_enter),
                style = MaterialTheme.typography.titleMedium, color = MaktabaPalette.Text,
            )
            if (holders.size > 1) {
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    holders.forEach { h ->
                        MaktabaButton(onClick = { who = h.id }, modifier = Modifier.testTag("parental-pin-who-${h.id}")) {
                            Text(if (who == h.id) "✓ ${h.username}" else h.username)
                        }
                    }
                }
            }
            if (who != null) {
                if (help.forTime) {
                    Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                        MORE_TIME_MINUTES.forEach { m ->
                            MaktabaButton(onClick = { minutes = m }, modifier = Modifier.testTag("parental-pin-minutes-$m")) {
                                Text((if (minutes == m) "✓ " else "") + res.timeWords(m * 60L))
                            }
                        }
                    }
                }
                // the digits keep their order in Arabic too: the field is left to right
                CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
                    TvTextField(
                        value = pin,
                        onValueChange = { v ->
                            pin = v.filter { it in '0'..'9' }.take(6)
                            if (pinState == PinState.Wrong || pinState == PinState.Failed) help.pin.again()
                        },
                        label = stringResource(CoreR.string.parental_pin_field),
                        modifier = Modifier.width(280.dp).testTag("parental-pin-field"),
                        visualTransformation = PasswordVisualTransformation(),
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.NumberPassword),
                    )
                }
                MaktabaButton(
                    onClick = {
                        val id = who ?: return@MaktabaButton
                        val typed = pin
                        pin = ""
                        scope.launch { help.pin.submit(id, typed, if (help.forTime) minutes else null) }
                    },
                    enabled = isParentPin(pin) && pinState != PinState.Checking && pinState !is PinState.Locked,
                    modifier = Modifier.testTag("parental-pin-submit"),
                ) { Text(stringResource(CoreR.string.parental_pin_unlock)) }
            }
            val line = when (val s = pinState) {
                PinState.Wrong -> stringResource(CoreR.string.parental_pin_wrong)
                is PinState.Locked -> s.retryAfterSec
                    ?.let { stringResource(CoreR.string.parental_pin_locked, res.timeWords(it.coerceAtLeast(60))) }
                    ?: stringResource(CoreR.string.parental_pin_locked_later)
                PinState.Unlocked -> stringResource(CoreR.string.parental_pin_unlocked)
                PinState.Failed -> stringResource(CoreR.string.parental_pin_failed)
                else -> null
            }
            if (line != null) {
                Text(line, style = MaterialTheme.typography.titleSmall, color = MaktabaPalette.Gold, modifier = Modifier.testTag("parental-pin-state").semantics { liveRegion = LiveRegionMode.Polite })
            }
        }
    }
}

/**
 * The time's-up screen: why the video stopped, when watching opens again, where it stopped and
 * how to get more time. Playback has stopped; nothing asks the home again until Check again.
 */
@Composable
fun TvTimesUp(refusal: TimeRefusal, positionMs: Long, help: ParentHelp?, onCheck: () -> Unit, onBack: (() -> Unit)? = null) {
    val first = remember { FocusRequester() }
    LaunchedEffect(refusal) { runCatching { first.requestFocus() } }
    Centered {
        Column(
            Modifier.widthIn(max = 820.dp).verticalScroll(rememberScrollState()).testTag("parental-times-up"),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Icon(Icons.Outlined.Schedule, contentDescription = null, tint = MaktabaPalette.Gold, modifier = Modifier.size(56.dp))
            Text(
                stringResource(timesUpTitle(refusal.reason)),
                style = MaterialTheme.typography.headlineSmall, color = MaktabaPalette.Text, textAlign = TextAlign.Center,
                modifier = Modifier.testTag("parental-times-up-title").semantics { heading(); liveRegion = LiveRegionMode.Polite },
            )
            refusal.nextAllowedAt?.let {
                Text(
                    stringResource(CoreR.string.parental_time_next, nextWords(it)),
                    style = MaterialTheme.typography.titleMedium, color = MaktabaPalette.Text, modifier = Modifier.testTag("parental-time-next"),
                )
            }
            if (positionMs >= 1000) {
                Text(
                    stringResource(CoreR.string.parental_time_saved, TimeView.clock(positionMs)),
                    style = MaterialTheme.typography.titleSmall, color = MaktabaPalette.Muted, modifier = Modifier.testTag("parental-time-saved"),
                )
            }
            Text(stringResource(CoreR.string.parental_time_ask_parent), style = MaterialTheme.typography.titleMedium, color = MaktabaPalette.Muted)
            Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                MaktabaButton(onClick = onCheck, modifier = Modifier.focusRequester(first).testTag("parental-time-check")) {
                    Text(stringResource(CoreR.string.parental_time_check))
                }
                if (onBack != null) {
                    MaktabaButton(onClick = onBack, modifier = Modifier.testTag("parental-back")) { Text(stringResource(CoreR.string.error_back)) }
                }
            }
            if (help != null) TvParentHelp(help, onUnlocked = onCheck)
        }
    }
}

/**
 * "This isn't available" for a sign-in under a limit: never the title or picture of what was
 * asked for (the home sends none). [stopped]: a video the home ended while it played.
 */
@Composable
fun TvBlocked(
    mine: MyParental,
    help: ParentHelp?,
    onUnlocked: () -> Unit,
    stopped: Boolean = false,
    onBack: (() -> Unit)? = null,
    /** Runs each time the TV comes back to the app while this shows: a parent may have approved meanwhile. */
    onReturn: (() -> Unit)? = null,
) {
    com.hamzalabs.maktaba.core.ui.OnReturnToFront(onReturn)
    val first = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { first.requestFocus() } }
    val res = LocalContext.current.resources
    val lock = stringResource(CoreR.string.parental_blocked_a11y)
    Centered {
        Column(
            Modifier.widthIn(max = 820.dp).verticalScroll(rememberScrollState()).testTag("parental-blocked"),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Icon(Icons.Outlined.Lock, contentDescription = null, tint = MaktabaPalette.Gold, modifier = Modifier.size(56.dp).semantics { contentDescription = lock })
            Text(
                stringResource(CoreR.string.parental_blocked_title),
                style = MaterialTheme.typography.headlineSmall, color = MaktabaPalette.Text,
                modifier = Modifier.testTag("parental-blocked-title").semantics { heading() },
            )
            if (stopped) {
                Text(stringResource(CoreR.string.parental_player_stopped), style = MaterialTheme.typography.titleMedium, color = MaktabaPalette.Muted, modifier = Modifier.testTag("parental-stopped"))
            }
            res.limitLine(mine)?.let {
                Text(it, style = MaterialTheme.typography.titleMedium, color = MaktabaPalette.Muted, modifier = Modifier.testTag("parental-blocked-reason"))
            }
            if (onBack != null) {
                MaktabaButton(onClick = onBack, modifier = Modifier.focusRequester(first).testTag("parental-back")) { Text(stringResource(CoreR.string.error_back)) }
            }
            if (help != null) TvParentHelp(help, onUnlocked)
        }
    }
}

/** Live TV's slate: the programme on air is above the limit; the channel comes back at [retryAt]. */
@Composable
fun TvNotNow(retryAt: Instant?, onBack: () -> Unit) {
    val first = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { first.requestFocus() } }
    Centered {
        Column(
            Modifier.widthIn(max = 720.dp).testTag("live-parental-slate").semantics { liveRegion = LiveRegionMode.Polite },
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Icon(Icons.Outlined.Lock, contentDescription = null, tint = MaktabaPalette.Gold, modifier = Modifier.size(56.dp))
            Text(stringResource(CoreR.string.live_parental_slate), style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text, textAlign = TextAlign.Center)
            retryAt?.let {
                Text(stringResource(CoreR.string.live_parental_back, nextWords(it)), style = MaterialTheme.typography.titleMedium, color = MaktabaPalette.Muted, modifier = Modifier.testTag("live-parental-back"))
            }
            MaktabaButton(onClick = onBack, modifier = Modifier.focusRequester(first).testTag("parental-back")) { Text(stringResource(CoreR.string.error_back)) }
        }
    }
}
