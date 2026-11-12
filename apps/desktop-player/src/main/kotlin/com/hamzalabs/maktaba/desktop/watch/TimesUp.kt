package com.hamzalabs.maktaba.desktop.watch

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Button
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.MORE_TIME_MINUTES
import com.hamzalabs.maktaba.core.api.MyTime
import com.hamzalabs.maktaba.core.api.SubjectKind
import com.hamzalabs.maktaba.core.api.isParentPin
import com.hamzalabs.maktaba.core.parental.AskState
import com.hamzalabs.maktaba.core.parental.ParentAsk
import com.hamzalabs.maktaba.core.parental.PinState
import com.hamzalabs.maktaba.core.parental.PinUnlock
import com.hamzalabs.maktaba.core.parental.TimeView
import com.hamzalabs.maktaba.core.parental.TimeWarnings
import com.hamzalabs.maktaba.desktop.LocalTexts
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * Asking for more time from the time's-up screen (stories 57.06, 57.15): a request a parent
 * answers from their own Player or the web, and a parent's PIN typed here when someone set one.
 */
class MoreTime(client: ApiClient, signedOut: () -> Unit = {}, subjectKind: String = SubjectKind.TIME, subjectId: String = "") {
    val ask = ParentAsk(client, subjectKind, subjectId, signedOut)
    val pin = PinUnlock(client, subjectKind, subjectId, signedOut)

    /** For more time (minutes are chosen), not for an item the home does not show the account. */
    val forTime: Boolean = subjectKind == SubjectKind.TIME
}

/**
 * The time's-up screen (Epic 57 story 57.15): why the video stopped, when watching opens
 * again, where it stopped, and how to get more time ([more]: ask a parent, or a parent's PIN).
 * Playback has stopped and the home is not asked again until the viewer chooses Check again.
 */
@Composable
fun TimesUp(up: TimeUp, onCheck: () -> Unit, more: MoreTime? = null) {
    val t = LocalTexts.current
    Column(
        Modifier.padding(24.dp).testTag("parental-times-up").semantics { liveRegion = LiveRegionMode.Polite },
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text(
            t[ParentalTime.titleKey(up.reason)], color = Color.White,
            style = MaterialTheme.typography.headlineSmall, modifier = Modifier.semantics { heading() },
        )
        up.nextAllowedAt?.let { Text(t["parental.time.next", ParentalTime.next(it, t.lang)], color = Color.White) }
        if (up.positionMs >= 1000) Text(t["parental.time.saved", ParentalTime.clock(up.positionMs)], color = Color.White)
        Text(t["parental.time.askParent"], color = Color.White)
        if (more != null) ParentHelpButtons(more, onUnlocked = onCheck)
        Button(onClick = onCheck, modifier = Modifier.testTag("parental-time-check")) { Text(t["parental.time.check"]) }
    }
}

/**
 * Ask for more time (15, 30 or 60 minutes) or, on the blocked page, Ask a parent; and Enter
 * parent PIN when someone has one. [color] is the words' colour (white over the player).
 */
@Composable
fun ParentHelpButtons(more: MoreTime, onUnlocked: () -> Unit, color: Color = Color.White) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    val asked by more.ask.state.collectAsState()
    val holders by more.pin.holders.collectAsState()
    val pinState by more.pin.state.collectAsState()
    var choosing by remember { mutableStateOf(false) }
    var pinOpen by remember { mutableStateOf(false) }
    var who by remember { mutableStateOf<String?>(null) }
    var pin by remember { mutableStateOf("") }
    var minutes by remember { mutableStateOf(MORE_TIME_MINUTES[1]) }
    LaunchedEffect(more) { more.pin.load() }
    LaunchedEffect(holders) { if (holders.size == 1) who = holders.single().id }
    // once per unlock: the state is put back, so showing this screen again never asks by itself
    LaunchedEffect(pinState) {
        if (pinState == PinState.Unlocked) {
            more.pin.again()
            onUnlocked()
        }
    }
    // a lock counts down on screen and clears by itself (story 57.06)
    LaunchedEffect(more, pinState is PinState.Locked) { more.pin.waitOutLock() }
    // the request this screen is about, as the home has it: followed while it waits and the
    // window is in front; a parent's yes opens what was asked for, once
    val inFront = androidx.compose.ui.platform.LocalWindowInfo.current.isWindowFocused
    LaunchedEffect(more, asked is AskState.Waiting, inFront) { if (inFront) more.ask.follow() }
    LaunchedEffect(asked) {
        if (asked is AskState.Approved) {
            more.ask.done()
            onUnlocked()
        }
    }
    // the home knows nothing to ask for: nothing more is offered
    if (asked == AskState.NothingToAsk || pinState == PinState.NothingToUnlock) return
    Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        if (asked !is AskState.Waiting) {
            OutlinedButton(
                onClick = { if (more.forTime) choosing = !choosing else scope.launch { more.ask.ask() } },
                enabled = asked != AskState.Sending, modifier = Modifier.testTag("parental-ask"),
            ) {
                Text(t[if (asked is AskState.Denied) "parental.ask.again" else if (more.forTime) "parental.time.askMore" else "parental.blocked.ask"])
            }
        }
        if (holders.isNotEmpty()) {
            OutlinedButton(onClick = { more.pin.again(); pinOpen = !pinOpen }, modifier = Modifier.testTag("parental-pin")) { Text(t["parental.blocked.pin"]) }
        }
    }
    if (choosing && more.forTime && asked !is AskState.Waiting) {
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            MORE_TIME_MINUTES.forEach { m ->
                OutlinedButton(onClick = { choosing = false; scope.launch { more.ask.ask(m) } }, modifier = Modifier.testTag("parental-ask-$m")) {
                    Text(ParentalTime.words(m * 60L, t))
                }
            }
        }
    }
    val askLine = when (asked) {
        is AskState.Waiting -> t["parental.ask.pending"] + "\n" + t["parental.ask.sent"]
        AskState.TooMany -> t["parental.ask.tooMany"]
        AskState.Failed -> t["parental.ask.failed"]
        is AskState.Denied -> t["parental.ask.denied"]
        else -> null
    }
    if (askLine != null) Text(askLine, color = color, modifier = Modifier.testTag("parental-ask-state"))
    if (pinOpen && holders.isNotEmpty()) {
        Column(Modifier.testTag("parental-pin-panel"), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(t[if (who == null) "parental.pin.choose" else "parental.pin.enter"], color = color)
            if (holders.size > 1) {
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    holders.forEach { h ->
                        FilterChip(selected = who == h.id, onClick = { who = h.id }, label = { Text(h.username) }, modifier = Modifier.testTag("parental-pin-who-${h.id}"))
                    }
                }
            }
            if (who != null) {
                if (more.forTime) Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    MORE_TIME_MINUTES.forEach { m ->
                        FilterChip(selected = minutes == m, onClick = { minutes = m }, label = { Text(ParentalTime.words(m * 60L, t)) }, modifier = Modifier.testTag("parental-pin-minutes-$m"))
                    }
                }
                // the digits keep their order in Arabic too: the field is left to right
                CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
                    OutlinedTextField(
                        value = pin,
                        onValueChange = { v ->
                            pin = v.filter { it in '0'..'9' }.take(6)
                            if (pinState == PinState.Wrong || pinState == PinState.Failed) more.pin.again()
                        },
                        label = { Text(t["parental.pin.field"]) },
                        singleLine = true,
                        visualTransformation = PasswordVisualTransformation(),
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.NumberPassword),
                        modifier = Modifier.width(240.dp).testTag("parental-pin-field"),
                    )
                }
                Button(
                    onClick = {
                        val id = who ?: return@Button
                        val typed = pin
                        pin = ""
                        scope.launch { more.pin.submit(id, typed, if (more.forTime) minutes else null) }
                    },
                    enabled = isParentPin(pin) && pinState != PinState.Checking && pinState !is PinState.Locked,
                    modifier = Modifier.testTag("parental-pin-submit"),
                ) { Text(t["parental.pin.unlock"]) }
            }
            val line = when (val s = pinState) {
                PinState.Wrong -> t["parental.pin.wrong"]
                is PinState.Locked -> s.retryAfterSec?.let { t["parental.pin.locked", ParentalTime.words(it.coerceAtLeast(60), t)] } ?: t["parental.pin.lockedLater"]
                PinState.Unlocked -> t["parental.pin.unlocked"]
                PinState.Failed -> t["parental.pin.failed"]
                else -> null
            }
            if (line != null) Text(line, color = color, modifier = Modifier.testTag("parental-pin-state").semantics { liveRegion = LiveRegionMode.Polite })
        }
    }
}

/**
 * Over the title bar while a restricted account watches: the time the home says is left once
 * 15 minutes or fewer remain, a warning five minutes and one minute before the end, and that
 * the home could not check the time at the last progress post.
 */
@Composable
fun TimeNotes(time: MyTime?, cannotCheck: Boolean) {
    val t = LocalTexts.current
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
    warned?.let {
        Text(
            t["parental.time.warning", ParentalTime.words(it, t)], color = MaterialTheme.colorScheme.primary,
            style = MaterialTheme.typography.labelLarge,
            modifier = Modifier.padding(horizontal = 8.dp).testTag("parental-time-warning").semantics { liveRegion = LiveRegionMode.Assertive },
        )
    }
    val left = TimeView.left(time)
    if (left != null && TimeView.showsInPlayer(time)) {
        Text(
            t["parental.time.left", ParentalTime.words(left, t)], color = Color.White, style = MaterialTheme.typography.labelLarge,
            modifier = Modifier.padding(horizontal = 8.dp).testTag("parental-time-left"),
        )
    }
    if (cannotCheck) {
        Text(
            t["parental.time.cannotCheck"], color = Color.White, style = MaterialTheme.typography.labelLarge,
            modifier = Modifier.padding(horizontal = 8.dp).testTag("parental-cannot-check"),
        )
    }
}

/**
 * Home's line for a restricted account (story 57.15): the time the home says is left today
 * whenever a rule covers now, or that the time is over and when watching opens again. Nothing
 * for an account with no time rule now. The home is asked only while Home shows.
 */
@Composable
fun HomeTime(parental: com.hamzalabs.maktaba.core.parental.ParentalState, modifier: Modifier = Modifier) {
    val mine by parental.mine.collectAsState()
    val time by parental.time.collectAsState()
    LaunchedEffect(parental, mine.restricted) { if (mine.restricted) parental.followTime() }
    if (!mine.restricted) return
    HomeTimeLine(time, modifier)
}

@Composable
fun HomeTimeLine(time: MyTime?, modifier: Modifier = Modifier) {
    val t = LocalTexts.current
    val over = TimeView.refusal(time)
    val left = TimeView.left(time)
    if (over == null && left == null) return
    Column(modifier.padding(horizontal = 16.dp).testTag("parental-home-time").semantics { liveRegion = LiveRegionMode.Polite }) {
        if (over != null) {
            Text(t[ParentalTime.titleKey(over.reason)], style = MaterialTheme.typography.titleMedium, modifier = Modifier.testTag("parental-home-over"))
            over.nextAllowedAt?.let {
                Text(t["parental.time.next", ParentalTime.next(it, t.lang)], style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        } else if (left != null) {
            Text(t["parental.time.left", ParentalTime.words(left, t)], style = MaterialTheme.typography.titleMedium, modifier = Modifier.testTag("parental-home-left"))
        }
    }
}

/** "Your account is limited to 9+.": the limit in force, or null when the sign-in has none to name. */
fun limitLine(mine: com.hamzalabs.maktaba.core.api.MyParental, t: com.hamzalabs.maktaba.desktop.Texts): String? {
    val tier = mine.maxTier ?: mine.deviceLimit ?: return null
    val words = when {
        tier == com.hamzalabs.maktaba.core.api.MyParental.NOTHING_ALLOWED -> t["parental.tier.none"]
        tier <= 0 -> t["parental.tier.all"]
        else -> t["parental.tier.plus", tier]
    }
    // the device's limit is named when it is the one in force on an unrestricted account
    val device = !mine.restricted && mine.deviceLimit != null
    return t[if (device) "parental.blocked.device" else "parental.blocked.account", words]
}

/**
 * "This isn't available" (story 57.05): what a sign-in under a limit sees for something the home
 * does not show it. It never names what was asked for (the home sends nothing about it: a hidden
 * video answers as a missing one). [help] offers Ask a parent and Enter parent PIN; once the
 * home says there is nothing to ask for, both go. [onReturn] runs each time the window comes back
 * to the front while this shows: a parent may have approved from their own device meanwhile, and
 * the page then reads again (story 57.06).
 */
@Composable
fun BlockedPage(
    mine: com.hamzalabs.maktaba.core.api.MyParental,
    help: MoreTime?,
    onUnlocked: () -> Unit,
    modifier: Modifier = Modifier,
    onReturn: (() -> Unit)? = null,
) {
    val t = LocalTexts.current
    val focused = androidx.compose.ui.platform.LocalWindowInfo.current.isWindowFocused
    var wasAway by remember { mutableStateOf(false) }
    LaunchedEffect(focused) {
        if (!focused) wasAway = true
        else if (wasAway) {
            wasAway = false
            onReturn?.invoke()
        }
    }
    Column(
        modifier.padding(24.dp).testTag("parental-blocked"),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text(t["parental.blocked.title"], style = MaterialTheme.typography.headlineSmall, modifier = Modifier.testTag("parental-blocked-title").semantics { heading() })
        limitLine(mine, t)?.let { Text(it, modifier = Modifier.testTag("parental-blocked-reason")) }
        if (help != null) ParentHelpButtons(help, onUnlocked, color = MaterialTheme.colorScheme.onSurface)
    }
}
