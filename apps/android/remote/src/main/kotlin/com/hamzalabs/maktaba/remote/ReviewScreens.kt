package com.hamzalabs.maktaba.remote

import android.media.AudioAttributes
import android.media.MediaPlayer
import android.media.PlaybackParams
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.clickable
import androidx.compose.foundation.focusable
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.SnackbarResult
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.ReviewAction
import com.hamzalabs.maktaba.core.api.ReviewClip
import com.hamzalabs.maktaba.core.api.ReviewFlag
import com.hamzalabs.maktaba.core.api.ReviewReason
import com.hamzalabs.maktaba.core.api.ReviewResult
import com.hamzalabs.maktaba.core.api.isReviewForbidden
import com.hamzalabs.maktaba.core.api.resolveFlag
import com.hamzalabs.maktaba.core.api.reviewClip
import com.hamzalabs.maktaba.core.api.reviewConflict
import com.hamzalabs.maktaba.core.api.reviewFlag
import com.hamzalabs.maktaba.core.api.reviewFlags
import com.hamzalabs.maktaba.core.api.reviewSummary
import com.hamzalabs.maktaba.core.api.undoReview
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

// Word review in the Remote (Epic 52, story 52.13): the web's queue on a phone or tablet.
// A home admin opens it from Needs attention; the word screen plays the clip, shows the
// sentence in the transcript's direction with the word bold and underlined, and resolves with
// a tap (or the web's keys on a hardware keyboard). Swipes move next/previous, mirrored in
// right-to-left. A 403 says only home admins review words; a 409 reloads the word.

/** Needs attention, with Word review's entry for a home admin (its open count). */
@Composable
internal fun WithWordReview(remote: Remote, content: @Composable () -> Unit) {
    var reviewing by rememberSaveable { mutableStateOf(false) }
    var open by remember { mutableStateOf(0) }
    if (remote.isAdmin) {
        LaunchedEffect(Unit) { open = safe { remote.client.reviewSummary().open } ?: 0 }
    }
    if (reviewing) {
        ReviewScreen(remote, onBack = { reviewing = false })
        return
    }
    Box(Modifier.fillMaxSize()) {
        content()
        if (remote.isAdmin) {
            ExtendedFloatingActionButton(
                onClick = { reviewing = true },
                modifier = Modifier.align(Alignment.BottomEnd).padding(16.dp).testTag("open-word-review"),
            ) {
                Text(
                    if (open > 0) pluralStringResource(R.plurals.review_open, open, open)
                    else stringResource(R.string.review_title),
                )
            }
        }
    }
}

private suspend fun <T> safe(block: suspend () -> T): T? = try {
    block()
} catch (e: CancellationException) {
    throw e
} catch (e: Exception) {
    null
}

@Composable
internal fun ReviewScreen(remote: Remote, onBack: () -> Unit) {
    BackHandler(onBack = onBack)
    var items by remember { mutableStateOf<List<ReviewFlag>?>(null) }
    var index by rememberSaveable { mutableStateOf(0) }
    var forbidden by remember { mutableStateOf(false) }
    var notice by remember { mutableStateOf<String?>(null) }
    val snackbar = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()
    val stale = stringResource(R.string.review_stale)
    val failed = stringResource(R.string.review_action_failed)
    val undoLabel = stringResource(R.string.review_undo)

    suspend fun load() {
        try {
            items = remote.client.reviewFlags().items.filter { it.kind == "word" }
            index = index.coerceIn(0, maxOf(0, (items?.size ?: 1) - 1))
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException) {
            if (e.isReviewForbidden()) forbidden = true else notice = failed
        }
    }
    LaunchedEffect(Unit) { load() }

    Box(Modifier.fillMaxSize()) {
        Column(Modifier.fillMaxSize().padding(16.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                TextButton(onClick = onBack) { Text(stringResource(R.string.review_back)) }
                Text(stringResource(R.string.review_title), style = MaterialTheme.typography.titleLarge)
            }
            when {
                forbidden -> Text(stringResource(R.string.review_admins_only), Modifier.testTag("review-forbidden"))
                items == null -> Text(stringResource(R.string.review_loading))
                items!!.isEmpty() -> Text(stringResource(R.string.review_empty), Modifier.testTag("review-empty"))
                else -> {
                    val list = items!!
                    val current = list[index.coerceIn(0, list.size - 1)]
                    notice?.let { Text(it) }
                    ReviewWord(
                        remote, current,
                        onMove = { d -> index = (index + d).coerceIn(0, list.size - 1) },
                        onDone = { label, result ->
                            items = list.filterNot { it.id == current.id }
                            index = index.coerceIn(0, maxOf(0, list.size - 2))
                            scope.launch {
                                if (snackbar.showSnackbar(label, undoLabel, duration = androidx.compose.material3.SnackbarDuration.Short) == SnackbarResult.ActionPerformed) {
                                    safe { remote.client.undoReview(result) }
                                    load()
                                }
                            }
                        },
                        onConflict = { notice = stale; scope.launch { load() } },
                        onForbidden = { forbidden = true },
                        onFailed = { notice = failed },
                    )
                }
            }
        }
        SnackbarHost(snackbar, Modifier.align(Alignment.BottomCenter))
    }
}

@Composable
private fun ReviewWord(
    remote: Remote,
    flag: ReviewFlag,
    onMove: (Int) -> Unit,
    onDone: (String, ReviewResult) -> Unit,
    onConflict: () -> Unit,
    onForbidden: () -> Unit,
    onFailed: () -> Unit,
) {
    var detail by remember(flag.id) { mutableStateOf(flag) }
    var clip by remember(flag.id) { mutableStateOf<ReviewClip?>(null) }
    var edit by remember(flag.id) { mutableStateOf(flag.original) }
    var applyAll by remember(flag.id) { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val focus = remember { FocusRequester() }
    val ui = LocalLayoutDirection.current
    LaunchedEffect(flag.id) {
        safe { remote.client.reviewFlag(flag.id) }?.let { detail = it; edit = it.original }
        clip = safe { remote.client.reviewClip(flag.id) }
        runCatching { focus.requestFocus() }
    }
    val player = rememberClipPlayer(clip?.absolute(remote.client.home))
    val doneWords = mapOf(
        ReviewAction.Accept to stringResource(R.string.review_done_accept, detail.original),
        ReviewAction.Name to stringResource(R.string.review_done_name, detail.original),
        ReviewAction.Dictionary to stringResource(R.string.review_done_dictionary, detail.original),
        ReviewAction.Skip to stringResource(R.string.review_done_skip, detail.original),
    )
    val fixedWord = stringResource(R.string.review_fixed, detail.original, edit)

    fun resolve(action: ReviewAction, text: String? = null) {
        if (busy) return
        busy = true
        scope.launch {
            try {
                val res = remote.client.resolveFlag(detail.id, action, text, applyAll)
                onDone(if (action == ReviewAction.Correct) fixedWord.replace(edit, text ?: edit) else doneWords[action] ?: "", res)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ApiException) {
                when {
                    e.isReviewForbidden() -> onForbidden()
                    e.reviewConflict() != null -> onConflict()
                    else -> onFailed()
                }
            } finally {
                busy = false
            }
        }
    }

    // right to left the swipe is mirrored: towards the start is always "next"
    val swipe = Modifier.pointerInput(flag.id, ui) {
        var total = 0f
        detectHorizontalDragGestures(onDragEnd = {
            val next = if (ui == LayoutDirection.Rtl) total > 120 else total < -120
            val prev = if (ui == LayoutDirection.Rtl) total < -120 else total > 120
            if (next) onMove(1) else if (prev) onMove(-1)
            total = 0f
        }) { _, d -> total += d }
    }
    val keys = Modifier.focusRequester(focus).focusable().onKeyEvent { e ->
        if (e.type != KeyEventType.KeyDown) return@onKeyEvent false
        when (e.key) {
            Key.J, Key.DirectionDown -> onMove(1)
            Key.K, Key.DirectionUp -> onMove(-1)
            Key.Spacebar -> player.toggle(if (e.isShiftPressed) 0.75f else 1f)
            Key.R -> player.replay()
            Key.Enter -> resolve(ReviewAction.Accept)
            Key.N -> resolve(ReviewAction.Name)
            Key.D -> if (!detail.isQuran) resolve(ReviewAction.Dictionary)
            Key.S -> resolve(ReviewAction.Skip)
            Key.A -> applyAll = !applyAll
            Key.One, Key.Two, Key.Three, Key.Four, Key.Five -> {
                val n = listOf(Key.One, Key.Two, Key.Three, Key.Four, Key.Five).indexOf(e.key)
                detail.candidates.getOrNull(n)?.let { resolve(ReviewAction.Correct, it.text) }
            }
            Key.Z -> if (e.isCtrlPressed) Unit else return@onKeyEvent false
            else -> return@onKeyEvent false
        }
        true
    }
    Column(Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).then(swipe).then(keys), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        if (detail.reviewersNote.isNotBlank()) Text(detail.reviewersNote, style = MaterialTheme.typography.bodySmall)
        Text(detail.videoTitle, style = MaterialTheme.typography.labelMedium)
        val (pre, word, post) = detail.parts()
        // the sentence reads in the transcript's direction, whatever the app's
        CompositionLocalProvider(LocalLayoutDirection provides if (detail.rightToLeft) LayoutDirection.Rtl else LayoutDirection.Ltr) {
            Column {
                if (detail.before.isNotBlank()) Text(detail.before, style = MaterialTheme.typography.bodySmall)
                Text(
                    buildAnnotatedString {
                        append(pre)
                        withStyle(SpanStyle(fontWeight = FontWeight.Bold, textDecoration = TextDecoration.Underline)) { append(word) }
                        append(post)
                    },
                    style = MaterialTheme.typography.headlineSmall,
                    modifier = Modifier.testTag("review-sentence"),
                )
                if (detail.after.isNotBlank()) Text(detail.after, style = MaterialTheme.typography.bodySmall)
            }
        }
        if (clip == null) Text(stringResource(R.string.review_no_audio))
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            OutlinedButton(onClick = { player.toggle(1f) }) { Text(stringResource(R.string.review_play)) }
            OutlinedButton(onClick = { player.toggle(0.75f) }) { Text(stringResource(R.string.review_slow)) }
            OutlinedButton(onClick = { player.replay() }) { Text(stringResource(R.string.review_replay)) }
        }
        detail.reasonCodes.forEach { r -> Text("• " + stringResource(reasonText(r))) }
        if (detail.isQuran) detail.verse?.let { v ->
            Text(if (ui == LayoutDirection.Rtl) v.labelAr.orEmpty() else v.labelEn.orEmpty())
            val missing = v.missing
            val expected = v.expected
            when {
                missing != null -> Text(stringResource(R.string.review_quran_missing, missing))
                v.kind == "ins" -> Text(stringResource(R.string.review_quran_inserted))
                expected != null -> Text(stringResource(R.string.review_quran_expected, expected))
            }
            Text(stringResource(R.string.review_quran_credit), style = MaterialTheme.typography.bodySmall)
        }
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            detail.candidates.take(5).forEachIndexed { i, c ->
                OutlinedButton(enabled = !busy, onClick = { resolve(ReviewAction.Correct, c.text) },
                    modifier = Modifier.testTag("candidate-$i")) { Text("${i + 1}  ${c.text}") }
            }
        }
        OutlinedTextField(value = edit, onValueChange = { edit = it }, label = { Text(stringResource(R.string.review_edit)) },
            modifier = Modifier.fillMaxWidth())
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(enabled = !busy, onClick = { resolve(ReviewAction.Accept) }) { Text(stringResource(R.string.review_accept)) }
            Button(enabled = !busy && edit.isNotBlank() && edit != detail.original,
                onClick = { resolve(ReviewAction.Correct, edit.trim()) }) { Text(stringResource(R.string.review_correct)) }
            OutlinedButton(enabled = !busy, onClick = { resolve(ReviewAction.Name) }) { Text(stringResource(R.string.review_name)) }
            if (!detail.isQuran) OutlinedButton(enabled = !busy, onClick = { resolve(ReviewAction.Dictionary) }) {
                Text(stringResource(R.string.review_dictionary))
            }
            OutlinedButton(enabled = !busy, onClick = { resolve(ReviewAction.Skip) }) { Text(stringResource(R.string.review_skip)) }
        }
        if (detail.sameKeyOpen > 0) Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.clickable { applyAll = !applyAll }) {
            Checkbox(checked = applyAll, onCheckedChange = { applyAll = it })
            Text(pluralStringResource(R.plurals.review_apply_all, detail.sameKeyOpen + 1, detail.sameKeyOpen + 1))
        }
    }
}

internal fun reasonText(r: ReviewReason): Int = when (r) {
    ReviewReason.LowConfidence -> R.string.review_reason_low_confidence
    ReviewReason.NotInDictionary -> R.string.review_reason_not_in_dictionary
    ReviewReason.ListenersDisagree -> R.string.review_reason_listeners_disagree
    ReviewReason.QuranMismatch -> R.string.review_reason_quran_mismatch
    ReviewReason.NamedEntity -> R.string.review_reason_named_entity
    ReviewReason.NearVocabulary -> R.string.review_reason_near_vocabulary
    ReviewReason.Manual -> R.string.review_reason_manual
    ReviewReason.PreviousFix -> R.string.review_reason_previous_fix
    ReviewReason.OverBudget -> R.string.review_reason_over_budget
    ReviewReason.Unknown -> R.string.review_reason_unknown
}

/** The clip, played with the platform's MediaPlayer (the signed URL needs no session). */
internal class ClipPlayer {
    var player: MediaPlayer? = null
    fun toggle(rate: Float) {
        val p = player ?: return
        runCatching {
            if (p.isPlaying) p.pause() else {
                p.playbackParams = PlaybackParams().setSpeed(rate)
                p.start()
            }
        }
    }
    fun replay() {
        val p = player ?: return
        runCatching { p.seekTo(0); p.start() }
    }
}

@Composable
private fun rememberClipPlayer(url: String?): ClipPlayer {
    val clip = remember { ClipPlayer() }
    DisposableEffect(url) {
        val p = if (url == null) null else runCatching {
            MediaPlayer().apply {
                setAudioAttributes(AudioAttributes.Builder().setContentType(AudioAttributes.CONTENT_TYPE_SPEECH).build())
                setDataSource(url)
                setOnPreparedListener { it.start() } // the clip plays as the word opens
                prepareAsync()
            }
        }.getOrNull()
        clip.player = p
        onDispose {
            runCatching { p?.release() }
            clip.player = null
        }
    }
    return clip
}
