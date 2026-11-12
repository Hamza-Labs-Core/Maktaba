package com.hamzalabs.maktaba.core.ui

import android.content.res.Resources
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material.icons.outlined.Schedule
import androidx.compose.material3.Button
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalConfiguration
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
import androidx.compose.ui.text.style.TextDirection
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.R
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.MORE_TIME_MINUTES
import com.hamzalabs.maktaba.core.api.MyParental
import com.hamzalabs.maktaba.core.api.MyTime
import com.hamzalabs.maktaba.core.api.PinHolder
import com.hamzalabs.maktaba.core.api.SubjectKind
import com.hamzalabs.maktaba.core.api.TimeRefusal
import com.hamzalabs.maktaba.core.api.isParentPin
import com.hamzalabs.maktaba.core.parental.AskState
import com.hamzalabs.maktaba.core.parental.ParentAsk
import com.hamzalabs.maktaba.core.parental.PinState
import com.hamzalabs.maktaba.core.parental.PinUnlock
import com.hamzalabs.maktaba.core.parental.TimeView
import com.hamzalabs.maktaba.core.parental.TimeWarnings
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.time.Instant
import java.util.Locale

// Parental controls on the Android screens (Epic 57 stories 57.05, 57.06, 57.10, 57.15): the
// words every Android app says (the phone and tablet Player here, the TV with its own
// focus-first layouts over the same words), and the phone's screens.
//
// The home filters, counts and refuses. These screens say what it answered; they hide nothing
// by themselves and keep no clock of their own.

// ---------- words ----------

/** A length of time in words: "45 minutes", "1 hour and 5 minutes", "less than a minute". */
fun Resources.timeWords(seconds: Long): String {
    val p = TimeView.parts(seconds)
    if (p.underMinute) return getString(R.string.parental_time_under_minute)
    val h = getQuantityString(R.plurals.parental_time_hours, p.hours, p.hours)
    val m = getQuantityString(R.plurals.parental_time_minutes, p.minutes, p.minutes)
    return when {
        p.hours == 0 -> m
        p.minutes == 0 -> h
        else -> getString(R.string.parental_time_hours_minutes, h, m)
    }
}

/** A tier limit as a person reads it: "All ages", "9+" (its plus kept left to right in Arabic). */
fun Resources.tierWords(tier: Int): String = when {
    tier == MyParental.NOTHING_ALLOWED -> getString(R.string.parental_tier_none)
    tier <= 0 -> getString(R.string.parental_tier_all)
    else -> getString(R.string.parental_tier_plus, tier)
}

/** "9+" for a limit of 9; null when the sign-in has no limit to name. */
fun Resources.limitLine(mine: MyParental): String? {
    val tier = mine.maxTier ?: mine.deviceLimit ?: return null
    // the device's limit is named when it is the one in force on an unrestricted account
    val device = !mine.restricted && mine.deviceLimit != null
    return getString(if (device) R.string.parental_blocked_device else R.string.parental_blocked_account, tierWords(tier))
}

/** The time's-up screen's title: "Not watching time yet" outside a slot, else "Time's up for today". */
fun timesUpTitle(reason: String): Int =
    if (TimeView.outsideSlot(reason)) R.string.parental_time_outside_title else R.string.parental_time_up_title

/** The locale the screen's words are in (the app's language, which may differ from the device's). */
@Composable
fun wordsLocale(): Locale = LocalConfiguration.current.locales[0] ?: Locale.ENGLISH

/** "7:30 PM" today, "Thursday 7:00 AM" another day, in the app's language. */
@Composable
fun nextWords(at: Instant): String = TimeView.nextWords(at, wordsLocale())

// ---------- the time left ----------

/**
 * "12 minutes left today": in a player once 15 minutes or fewer remain, on Home ([always])
 * whenever a rule covers now. Nothing when the home named no countdown.
 */
@Composable
fun TimeLeftLine(time: MyTime?, modifier: Modifier = Modifier, always: Boolean = false, color: Color = LocalContentColor.current) {
    val left = TimeView.left(time) ?: return
    if (!always && !TimeView.showsInPlayer(time)) return
    val words = LocalContext.current.resources.timeWords(left)
    Text(
        stringResource(R.string.parental_time_left, words),
        style = MaterialTheme.typography.labelLarge,
        color = if (left <= TimeView.WARNINGS_S.first()) MaterialTheme.colorScheme.error else color,
        modifier = modifier.testTag("parental-time-left"),
    )
}

/** The five-minute and one-minute warnings, each shown for a while as the home's answer crosses its line. */
@Composable
fun TimeWarning(time: MyTime?, modifier: Modifier = Modifier) {
    val warnings = remember { TimeWarnings() }
    var shown by remember { mutableStateOf<Long?>(null) }
    LaunchedEffect(time) {
        val line = warnings.crossed(time) ?: return@LaunchedEffect
        shown = line
        delay(TimeView.WARNING_SHOWN_MS)
        if (shown == line) shown = null
    }
    val line = shown ?: return
    val words = LocalContext.current.resources.timeWords(line)
    Surface(
        color = MaterialTheme.colorScheme.inverseSurface,
        contentColor = MaterialTheme.colorScheme.inverseOnSurface,
        shape = MaterialTheme.shapes.medium,
        modifier = modifier.semantics { liveRegion = LiveRegionMode.Assertive },
    ) {
        Text(
            stringResource(R.string.parental_time_warning, words),
            Modifier.padding(horizontal = 16.dp, vertical = 10.dp).testTag("parental-time-warning"),
        )
    }
}

/**
 * Home's line: the time left today whenever a rule covers now, or that the time is over and
 * when watching opens again. Nothing when no rule covers now.
 */
@Composable
fun HomeTimeLine(time: MyTime?, modifier: Modifier = Modifier) {
    val over = TimeView.refusal(time)
    if (over == null && TimeView.left(time) == null) return
    Surface(
        color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.92f),
        contentColor = MaterialTheme.colorScheme.onSurfaceVariant,
        shape = MaterialTheme.shapes.medium,
        modifier = modifier.testTag("parental-home-time"),
    ) {
        Column(Modifier.padding(horizontal = 14.dp, vertical = 6.dp), horizontalAlignment = Alignment.CenterHorizontally) {
            if (over != null) {
                Text(stringResource(timesUpTitle(over.reason)), style = MaterialTheme.typography.labelLarge, modifier = Modifier.testTag("parental-home-over"))
                over.nextAllowedAt?.let { Text(stringResource(R.string.parental_time_next, nextWords(it)), style = MaterialTheme.typography.labelMedium) }
            } else {
                TimeLeftLine(time, always = true)
            }
        }
    }
}

// ---------- asking a parent, the parent PIN ----------

/**
 * What a blocked or time's-up screen offers: asking a parent ([ask]) and, when someone set a
 * PIN, typing it here ([pin]). Made per screen for the thing it is about.
 */
class ParentHelp(client: ApiClient, val subjectKind: String, subjectId: String = "") {
    val ask = ParentAsk(client, subjectKind, subjectId)
    val pin = PinUnlock(client, subjectKind, subjectId)
    val forTime: Boolean get() = subjectKind == SubjectKind.TIME
}

/** Where asking stands, as a line under the buttons; nothing before the first ask. */
@Composable
fun AskLine(state: AskState, modifier: Modifier = Modifier) {
    val text = when (state) {
        AskState.Idle, AskState.Sending, AskState.NothingToAsk -> return
        is AskState.Waiting -> stringResource(R.string.parental_ask_pending) + "\n" + stringResource(R.string.parental_ask_sent)
        AskState.TooMany -> stringResource(R.string.parental_ask_too_many)
        AskState.Failed -> stringResource(R.string.parental_ask_failed)
    }
    Text(
        text,
        textAlign = TextAlign.Center,
        style = MaterialTheme.typography.bodyMedium,
        modifier = modifier.widthIn(max = 480.dp).testTag("parental-ask-state").semantics { liveRegion = LiveRegionMode.Polite },
    )
}

/**
 * Ask a parent, and Enter parent PIN when someone has one. For time, asking first offers
 * 15, 30 or 60 minutes. Once the home answers that there is nothing to ask for (a hidden item
 * is a missing one), both buttons go: nothing is offered.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun ParentHelpButtons(help: ParentHelp, onUnlocked: () -> Unit, modifier: Modifier = Modifier) {
    val scope = rememberCoroutineScope()
    val asked by help.ask.state.collectAsState()
    val holders by help.pin.holders.collectAsState()
    val pinState by help.pin.state.collectAsState()
    var choosing by rememberSaveable { mutableStateOf(false) }
    var pinOpen by rememberSaveable { mutableStateOf(false) }
    LaunchedEffect(help) { help.pin.load() }
    val nothing = asked == AskState.NothingToAsk || pinState == PinState.NothingToUnlock
    Column(modifier, horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(8.dp)) {
        if (!nothing) {
            FlowRow(
                horizontalArrangement = Arrangement.spacedBy(8.dp, Alignment.CenterHorizontally),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                if (asked !is AskState.Waiting) {
                    Button(
                        onClick = { if (help.forTime) choosing = !choosing else scope.launch { help.ask.ask() } },
                        enabled = asked != AskState.Sending,
                        modifier = Modifier.heightIn(min = 48.dp).testTag("parental-ask"),
                    ) { Text(stringResource(if (help.forTime) R.string.parental_time_ask_more else R.string.parental_blocked_ask)) }
                }
                if (holders.isNotEmpty()) {
                    OutlinedButton(
                        onClick = { help.pin.again(); pinOpen = true },
                        modifier = Modifier.heightIn(min = 48.dp).testTag("parental-pin"),
                    ) { Text(stringResource(R.string.parental_blocked_pin)) }
                }
            }
            if (choosing && help.forTime && asked !is AskState.Waiting) {
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp, Alignment.CenterHorizontally)) {
                    val res = LocalContext.current.resources
                    MORE_TIME_MINUTES.forEach { m ->
                        OutlinedButton(
                            onClick = { choosing = false; scope.launch { help.ask.ask(m) } },
                            modifier = Modifier.heightIn(min = 48.dp).testTag("parental-ask-$m"),
                        ) { Text(res.timeWords(m * 60L)) }
                    }
                }
            }
        }
        AskLine(asked)
    }
    if (pinOpen) {
        ParentPinDialog(
            holders = holders,
            state = pinState,
            forTime = help.forTime,
            onSubmit = { who, pin, minutes -> scope.launch { help.pin.submit(who, pin, minutes) } },
            onAgain = help.pin::again,
            onDismiss = {
                pinOpen = false
                if (pinState == PinState.Unlocked) onUnlocked()
            },
        )
    }
}

/**
 * A parent types their PIN on the child's device: whose it is, six digits and, for time, how
 * many minutes. A panel in the page (under the buttons that opened it), not a window of its
 * own. The digits are typed left to right in every language and are never kept.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun ParentPinDialog(
    holders: List<PinHolder>,
    state: PinState,
    forTime: Boolean,
    onSubmit: (guardianId: String, pin: String, minutes: Int?) -> Unit,
    onAgain: () -> Unit,
    onDismiss: () -> Unit,
) {
    var who by rememberSaveable { mutableStateOf(holders.singleOrNull()?.id) }
    // not saved across a rotation or the app's death: a PIN lives only while it is typed
    var pin by remember { mutableStateOf("") }
    var minutes by rememberSaveable { mutableStateOf(MORE_TIME_MINUTES[1]) }
    val res = LocalContext.current.resources
    val done = state == PinState.Unlocked
    Surface(
        color = MaterialTheme.colorScheme.surfaceContainerHigh,
        contentColor = MaterialTheme.colorScheme.onSurface,
        shape = MaterialTheme.shapes.large,
        modifier = Modifier.widthIn(max = 420.dp).testTag("parental-pin-dialog"),
    ) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text(
                stringResource(if (who == null) R.string.parental_pin_choose else R.string.parental_pin_enter),
                style = MaterialTheme.typography.titleMedium,
            )
            if (holders.size > 1 || who == null) {
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    holders.forEach { h ->
                        FilterChip(
                            selected = who == h.id,
                            onClick = { who = h.id },
                            label = { Text(h.username) },
                            modifier = Modifier.testTag("parental-pin-who-${h.id}"),
                        )
                    }
                }
            }
            if (who != null && !done) {
                if (forTime) {
                    Text(stringResource(R.string.parental_pin_minutes), style = MaterialTheme.typography.labelLarge)
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        MORE_TIME_MINUTES.forEach { m ->
                            FilterChip(
                                selected = minutes == m,
                                onClick = { minutes = m },
                                label = { Text(res.timeWords(m * 60L)) },
                                modifier = Modifier.testTag("parental-pin-minutes-$m"),
                            )
                        }
                    }
                }
                // the digits keep their order in Arabic too: the field is left to right
                CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
                    OutlinedTextField(
                        value = pin,
                        onValueChange = { v ->
                            pin = v.filter { it in '0'..'9' }.take(6)
                            if (state == PinState.Wrong || state == PinState.Failed) onAgain()
                        },
                        label = { Text(stringResource(R.string.parental_pin_field)) },
                        singleLine = true,
                        enabled = state != PinState.Checking,
                        visualTransformation = PasswordVisualTransformation(),
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.NumberPassword),
                        textStyle = MaterialTheme.typography.headlineSmall.copy(textDirection = TextDirection.Ltr),
                        modifier = Modifier.testTag("parental-pin-field"),
                    )
                }
            }
            val line = when (state) {
                PinState.Wrong -> stringResource(R.string.parental_pin_wrong)
                is PinState.Locked -> state.retryAfterSec
                    ?.let { stringResource(R.string.parental_pin_locked, res.timeWords(it.coerceAtLeast(60))) }
                    ?: stringResource(R.string.parental_pin_locked_later)
                PinState.Unlocked -> stringResource(R.string.parental_pin_unlocked)
                PinState.Failed -> stringResource(R.string.parental_pin_failed)
                PinState.NothingToUnlock -> stringResource(R.string.parental_blocked_title)
                PinState.Entering, PinState.Checking -> null
            }
            if (line != null) {
                Text(
                    line,
                    color = if (done) LocalContentColor.current else MaterialTheme.colorScheme.error,
                    modifier = Modifier.testTag("parental-pin-state").semantics { liveRegion = LiveRegionMode.Polite },
                )
            }
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                if (done || state == PinState.NothingToUnlock) {
                    Button(onClick = onDismiss, modifier = Modifier.heightIn(min = 48.dp).testTag("parental-pin-done")) { Text(stringResource(R.string.close)) }
                } else {
                    Button(
                        onClick = { who?.let { onSubmit(it, pin, if (forTime) minutes else null); pin = "" } },
                        enabled = who != null && isParentPin(pin) && state != PinState.Checking && state !is PinState.Locked,
                        modifier = Modifier.heightIn(min = 48.dp).testTag("parental-pin-submit"),
                    ) { Text(stringResource(R.string.parental_pin_unlock)) }
                    TextButton(onClick = onDismiss, modifier = Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.cancel)) }
                }
            }
        }
    }
}

// ---------- the time's-up screen ----------

/**
 * The home said this account's watching time is over: why, when watching opens again, where
 * the video stopped, and how to get more (ask a parent, or a parent's PIN). Playback has
 * stopped; nothing asks the home again until the person taps Check again.
 */
@Composable
fun TimesUpScreen(
    refusal: TimeRefusal,
    positionMs: Long,
    help: ParentHelp?,
    onCheck: () -> Unit,
    modifier: Modifier = Modifier,
    onBack: (() -> Unit)? = null,
) {
    Column(
        modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp).testTag("parental-times-up"),
        verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Icon(Icons.Outlined.Schedule, null, Modifier.size(48.dp))
        Text(
            stringResource(timesUpTitle(refusal.reason)),
            style = MaterialTheme.typography.headlineSmall,
            textAlign = TextAlign.Center,
            modifier = Modifier.testTag("parental-times-up-title").semantics { heading(); liveRegion = LiveRegionMode.Polite },
        )
        refusal.nextAllowedAt?.let {
            Text(stringResource(R.string.parental_time_next, nextWords(it)), textAlign = TextAlign.Center, modifier = Modifier.testTag("parental-time-next"))
        }
        if (positionMs >= 1000) {
            Text(
                stringResource(R.string.parental_time_saved, TimeView.clock(positionMs)),
                textAlign = TextAlign.Center,
                modifier = Modifier.testTag("parental-time-saved"),
            )
        }
        Text(stringResource(R.string.parental_time_ask_parent), textAlign = TextAlign.Center)
        if (help != null) ParentHelpButtons(help, onUnlocked = onCheck)
        OutlinedButton(onClick = onCheck, modifier = Modifier.heightIn(min = 48.dp).testTag("parental-time-check")) {
            Text(stringResource(R.string.parental_time_check))
        }
        if (onBack != null) TextButton(onClick = onBack, modifier = Modifier.testTag("parental-back")) { Text(stringResource(R.string.error_back)) }
    }
}

// ---------- the blocked page ----------

/**
 * "This isn't available": what a limited sign-in sees for something the home does not show it.
 * It never names what was asked for (the home sends nothing about it). [stopped]: a video that
 * was playing when the home ended it. [help] null offers nothing (an account with no limit).
 */
@Composable
fun BlockedPage(
    mine: MyParental,
    help: ParentHelp?,
    onUnlocked: () -> Unit,
    modifier: Modifier = Modifier,
    stopped: Boolean = false,
    onBack: (() -> Unit)? = null,
) {
    val res = LocalContext.current.resources
    val lock = stringResource(R.string.parental_blocked_a11y)
    Column(
        modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp).testTag("parental-blocked"),
        verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Icon(Icons.Outlined.Lock, null, Modifier.size(48.dp).semantics { contentDescription = lock })
        Text(
            stringResource(R.string.parental_blocked_title),
            style = MaterialTheme.typography.headlineSmall,
            textAlign = TextAlign.Center,
            modifier = Modifier.testTag("parental-blocked-title").semantics { heading() },
        )
        if (stopped) Text(stringResource(R.string.parental_player_stopped), textAlign = TextAlign.Center, modifier = Modifier.testTag("parental-stopped"))
        res.limitLine(mine)?.let { Text(it, textAlign = TextAlign.Center, modifier = Modifier.testTag("parental-blocked-reason")) }
        if (help != null) ParentHelpButtons(help, onUnlocked = onUnlocked)
        if (onBack != null) TextButton(onClick = onBack, modifier = Modifier.testTag("parental-back")) { Text(stringResource(R.string.error_back)) }
    }
}

// ---------- live TV ----------

/**
 * A programme's title as the guide shows it: "Not available" for one above the account's limit
 * (the home sends its time only, story 57.10).
 */
@Composable
fun programmeTitle(p: com.hamzalabs.maktaba.core.api.Programme): String =
    if (p.restricted) stringResource(R.string.live_parental_not_available) else p.title

/** Live TV's slate: the programme on air is above the limit; the channel comes back at [retryAt]. */
@Composable
fun NotNowSlate(retryAt: Instant?, modifier: Modifier = Modifier) {
    Column(
        modifier.padding(24.dp).testTag("live-parental-slate").semantics { liveRegion = LiveRegionMode.Polite },
        verticalArrangement = Arrangement.spacedBy(8.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Icon(Icons.Outlined.Lock, null, Modifier.size(40.dp))
        Text(stringResource(R.string.live_parental_slate), textAlign = TextAlign.Center, modifier = Modifier.widthIn(max = 480.dp))
        retryAt?.let { Text(stringResource(R.string.live_parental_back, nextWords(it)), modifier = Modifier.testTag("live-parental-back")) }
    }
}
