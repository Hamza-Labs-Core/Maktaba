package com.hamzalabs.maktaba.remote

import android.content.Intent
import android.net.Uri
import android.text.format.Formatter
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextDirection
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.RecoveryAction
import com.hamzalabs.maktaba.core.api.RecoveryBackup
import com.hamzalabs.maktaba.core.api.RecoveryException
import com.hamzalabs.maktaba.core.api.RecoveryLine
import com.hamzalabs.maktaba.core.api.RecoveryPart
import com.hamzalabs.maktaba.core.api.RecoveryState
import com.hamzalabs.maktaba.core.api.recoveryAction
import com.hamzalabs.maktaba.core.api.recoveryBackups
import com.hamzalabs.maktaba.core.api.recoveryDiagnostics
import com.hamzalabs.maktaba.core.api.recoveryDiagnosticsName
import com.hamzalabs.maktaba.core.api.recoveryLines
import com.hamzalabs.maktaba.core.api.recoveryState
import com.hamzalabs.maktaba.core.diagnostics.AppLog
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import com.hamzalabs.maktaba.core.R as CoreR

/**
 * The recovery screen's state (Epic 53 stories 53.04, 53.05): what the console says, the lines,
 * and an action being confirmed or run. The console is the rule (admins only): a refusal says
 * so, stops the reads (each one counts toward its lockout) and never signs the app out.
 */
class RecoveryModel(private val client: ApiClient) {
    enum class Phase { Loading, Ready, Denied, NotHere, Unreachable }

    /** An action waiting for the person's confirmation [step] of [need]. */
    data class Asking(val action: RecoveryAction, val step: Int, val need: Int)

    /** The last action's outcome: done, or why not ("" when there are no words for it). */
    sealed interface Outcome {
        data object Done : Outcome
        data class Failed(val why: String) : Outcome

        /** A restore was accepted: the home restarts to put the backup back. */
        data object Restoring : Outcome

        /** The home has no backup to restore. */
        data object NoBackups : Outcome

        /** The diagnostics bundle was written where the person chose. */
        data object Saved : Outcome
    }

    /** Reading the backups or the diagnostics bundle. */
    enum class Fetching { Backups, Diagnostics }

    var phase by mutableStateOf(Phase.Loading)
        private set
    var state by mutableStateOf<RecoveryState?>(null)
        private set
    var lines by mutableStateOf<List<RecoveryLine>>(emptyList())
        private set
    var minutes by mutableStateOf(15)
        private set
    var running by mutableStateOf<RecoveryAction?>(null)
        private set
    var asking by mutableStateOf<Asking?>(null)
        private set
    var outcome by mutableStateOf<Outcome?>(null)
        private set

    /** The backups to choose from while the person picks one to restore (null: not choosing). */
    var backups by mutableStateOf<List<RecoveryBackup>?>(null)
        private set
    var fetching by mutableStateOf<Fetching?>(null)
        private set

    /** The diagnostics bundle, held only until the person chooses where it goes (or cancels). */
    var diagnostics by mutableStateOf<ByteArray?>(null)
        private set

    /** Reads the state and the lines; what was read stays while the home does not answer for a moment. */
    suspend fun load() {
        try {
            state = client.recoveryState()
            lines = client.recoveryLines(minutes)
            phase = Phase.Ready
        } catch (e: CancellationException) {
            throw e
        } catch (e: RecoveryException.Denied) {
            phase = Phase.Denied
        } catch (e: RecoveryException.NotHere) {
            phase = Phase.NotHere
        } catch (e: Exception) {
            AppLog.failure("recovery", e)
            if (state == null) phase = Phase.Unreachable
        }
    }

    /** 15 more minutes of lines. */
    suspend fun loadMore() {
        minutes = (minutes + 15).coerceAtMost(24 * 60)
        try {
            lines = client.recoveryLines(minutes)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            AppLog.failure("recovery lines", e)
        }
    }

    /** Reads again every [everyMs] (the web page's 10 s) until cancelled or turned down. */
    suspend fun run(everyMs: Long = 10_000) {
        while (true) {
            load()
            if (phase == Phase.Denied || phase == Phase.NotHere) return
            delay(everyMs)
            // an action or a read meanwhile may have been turned down: no more tries (each counts)
            if (phase == Phase.Denied) return
        }
    }

    /** Restore a backup: the home's backups, to choose one from (or it says it has none). */
    suspend fun chooseBackup() {
        outcome = null
        fetching = Fetching.Backups
        try {
            val list = client.recoveryBackups()
            if (list.isEmpty()) outcome = Outcome.NoBackups else backups = list
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            fail(e)
        } finally {
            fetching = null
        }
    }

    /** The person chose [backup]: the restore's first confirmation. */
    fun pick(backup: RecoveryBackup) {
        backups = null
        ask(RecoveryAction.Restore(backup.name))
    }

    fun closeBackups() {
        backups = null
    }

    /** Fetches the diagnostics bundle; the screen then asks where to save it. */
    suspend fun fetchDiagnostics() {
        outcome = null
        fetching = Fetching.Diagnostics
        try {
            diagnostics = client.recoveryDiagnostics()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            fail(e)
        } finally {
            fetching = null
        }
    }

    /** Writes the bundle with [write] (to the document the person chose), then lets it go. */
    suspend fun saveDiagnostics(write: (ByteArray) -> Unit) {
        val bytes = diagnostics ?: return
        diagnostics = null
        try {
            withContext(Dispatchers.IO) { write(bytes) }
            outcome = Outcome.Saved
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            AppLog.failure("recovery diagnostics", e)
            outcome = Outcome.Failed(e.message.orEmpty())
        }
    }

    /** The person cancelled the save: the bundle goes. */
    fun dropDiagnostics() {
        diagnostics = null
    }

    private fun fail(e: Exception) {
        when (e) {
            is RecoveryException.Refused -> outcome = Outcome.Failed(e.why)
            is RecoveryException.Denied -> {
                outcome = Outcome.Failed("")
                phase = Phase.Denied
            }
            else -> {
                AppLog.failure("recovery", e)
                outcome = Outcome.Failed("")
            }
        }
    }

    /** The person chose [action]: the first confirmation. */
    fun ask(action: RecoveryAction) {
        outcome = null
        asking = Asking(action, 1, action.confirms)
    }

    fun cancel() {
        asking = null
    }

    /** The person confirmed: the next confirmation, or the action. */
    suspend fun confirm() {
        val a = asking ?: return
        if (a.step < a.need) {
            asking = a.copy(step = a.step + 1)
            return
        }
        asking = null
        running = a.action
        try {
            client.recoveryAction(a.action, a.need)
            outcome = if (a.action is RecoveryAction.Restore) Outcome.Restoring else Outcome.Done
        } catch (e: CancellationException) {
            throw e
        } catch (e: RecoveryException.Confirm) {
            if (e.need > a.need) {
                // through the relay: one more confirmation
                asking = Asking(a.action, a.need + 1, e.need)
                return
            }
            outcome = Outcome.Failed("")
        } catch (e: RecoveryException.Refused) {
            outcome = Outcome.Failed(e.why)
        } catch (e: RecoveryException.Denied) {
            outcome = Outcome.Failed("")
            phase = Phase.Denied
        } catch (e: Exception) {
            AppLog.failure("recovery action", e)
            outcome = Outcome.Failed("")
        } finally {
            running = null
        }
        load()
    }
}

/**
 * The recovery console on the Remote: what the web's recovery page shows and does, from the
 * same routes, which the supervisor answers with the API or the database down. Each group's and
 * part's state with its last error, Restart a part, Restart everything, safe mode on and off,
 * going back a version and restoring a backup (each confirmed twice, as the web page asks), the
 * diagnostics bundle saved where the person chooses (the system's document picker), the disk and
 * the last warnings and errors.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun RecoveryScreen(remote: Remote, onBack: () -> Unit, everyMs: Long = 10_000) {
    val model = remember(remote.client) { RecoveryModel(remote.client) }
    val scope = rememberCoroutineScope()
    var refreshing by remember { mutableStateOf(false) }
    LaunchedEffect(model) {
        // a fresh pass while the API answers, for the next time it does not
        launch { remote.client.refreshRecoveryPass() }
        model.run(everyMs)
    }
    val context = LocalContext.current
    // the bundle goes straight into the document the person creates; nothing is kept in the app
    val save = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/gzip")) { uri ->
        if (uri == null) model.dropDiagnostics()
        else scope.launch {
            model.saveDiagnostics { bytes ->
                (context.contentResolver.openOutputStream(uri) ?: error("the document could not be opened")).use { it.write(bytes) }
            }
        }
    }
    val pending = model.diagnostics
    LaunchedEffect(pending) { if (pending != null) save.launch(recoveryDiagnosticsName()) }
    BackHandler(onBack = onBack)
    Scaffold(
        modifier = Modifier.testTag("recovery"),
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.recovery_title)) },
                navigationIcon = {
                    IconButton(onClick = onBack, modifier = Modifier.testTag("recovery-back")) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, stringResource(CoreR.string.error_back))
                    }
                },
            )
        },
        // the last action's outcome, pinned below the list: a part's Restart and the actions are
        // far down it, and a line at its top would be out of sight (and not composed) when it lands
        bottomBar = { model.outcome?.let { OutcomeBar(it) } },
    ) { padding ->
        PullToRefreshBox(
            isRefreshing = refreshing,
            onRefresh = { scope.launch { refreshing = true; model.load(); refreshing = false } },
            modifier = Modifier.padding(padding).fillMaxSize(),
        ) {
            LazyColumn(Modifier.fillMaxSize().testTag("recovery-rows")) {
                when (model.phase) {
                    RecoveryModel.Phase.Loading -> item { Text(stringResource(R.string.diag_loading), Modifier.padding(16.dp)) }
                    RecoveryModel.Phase.Denied -> item {
                        Column(Modifier.padding(16.dp).testTag("recovery-denied"), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            Text(stringResource(R.string.recovery_denied), style = MaterialTheme.typography.titleSmall)
                            Text(stringResource(R.string.recovery_later), color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    }
                    RecoveryModel.Phase.NotHere -> item {
                        Text(stringResource(R.string.recovery_not_here), Modifier.padding(16.dp).testTag("recovery-not-here"))
                    }
                    RecoveryModel.Phase.Unreachable -> item {
                        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            Text(stringResource(R.string.recovery_unreachable), color = MaterialTheme.colorScheme.error,
                                modifier = Modifier.testTag("recovery-unreachable"))
                            OutlinedButton(onClick = { scope.launch { model.load() } }) { Text(stringResource(CoreR.string.try_again)) }
                        }
                    }
                    RecoveryModel.Phase.Ready -> model.state?.let { recoveryRows(it, model, remote) { a -> model.ask(a) } }
                }
            }
        }
    }
    model.asking?.let { a ->
        AlertDialog(
            onDismissRequest = { model.cancel() },
            title = { Text(actionTitle(a.action)) },
            text = { Text(stringResource(if (a.step > 1) R.string.recovery_confirm_again else R.string.recovery_confirm)) },
            confirmButton = {
                TextButton(onClick = { scope.launch { model.confirm() } }, modifier = Modifier.testTag("recovery-confirm")) {
                    // Restore (the backup is in the title), else the action's title
                    Text(if (a.action is RecoveryAction.Restore) stringResource(R.string.recovery_restore_button) else actionTitle(a.action))
                }
            },
            dismissButton = { TextButton(onClick = { model.cancel() }) { Text(stringResource(R.string.recovery_cancel)) } },
        )
    }
    model.backups?.let { list -> BackupPicker(list, pick = { model.pick(it) }, cancel = { model.closeBackups() }) }
}

/** The backups the home can restore, newest first; choosing one asks twice before anything is sent. */
@Composable
private fun BackupPicker(backups: List<RecoveryBackup>, pick: (RecoveryBackup) -> Unit, cancel: () -> Unit) {
    val context = LocalContext.current
    AlertDialog(
        onDismissRequest = cancel,
        modifier = Modifier.testTag("recovery-backups"),
        title = { Text(stringResource(R.string.recovery_choose_backup)) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(stringResource(R.string.recovery_restore_note), style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant)
                LazyColumn {
                    items(backups, key = { it.name }) { b ->
                        TextButton(onClick = { pick(b) }, modifier = Modifier.fillMaxWidth().testTag("recovery-backup-${b.name}")) {
                            Column(Modifier.fillMaxWidth()) {
                                Text(b.name, fontFamily = FontFamily.Monospace,
                                    style = MaterialTheme.typography.bodyMedium.copy(textDirection = TextDirection.Ltr))
                                Text(listOfNotNull(b.at?.let(::localTime), if (b.size < 0) null else bytes(context, b.size)).joinToString(" · "),
                                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                        }
                    }
                }
            }
        },
        confirmButton = {},
        dismissButton = { TextButton(onClick = cancel) { Text(stringResource(R.string.recovery_cancel)) } },
    )
}

@Composable
private fun OutcomeBar(o: RecoveryModel.Outcome) {
    Surface(tonalElevation = 3.dp, modifier = Modifier.fillMaxWidth()) {
        when (o) {
            RecoveryModel.Outcome.Done -> Text(stringResource(R.string.recovery_done), Modifier.padding(16.dp).testTag("recovery-done"))
            RecoveryModel.Outcome.Restoring ->
                Text(stringResource(R.string.recovery_restoring), Modifier.padding(16.dp).testTag("recovery-restoring"))
            RecoveryModel.Outcome.NoBackups ->
                Text(stringResource(R.string.recovery_no_backups), Modifier.padding(16.dp).testTag("recovery-no-backups"))
            RecoveryModel.Outcome.Saved ->
                Text(stringResource(R.string.recovery_diagnostics_saved), Modifier.padding(16.dp).testTag("recovery-saved"))
            is RecoveryModel.Outcome.Failed -> Text(
                if (o.why.isEmpty()) stringResource(R.string.recovery_failed) else stringResource(R.string.recovery_failed_why, o.why),
                color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(16.dp).testTag("recovery-failed"),
            )
        }
    }
}

private fun LazyListScope.recoveryRows(s: RecoveryState, model: RecoveryModel, remote: Remote, ask: (RecoveryAction) -> Unit) {
    item {
        Column(Modifier.fillMaxWidth().padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            if (s.safeMode) {
                Text(stringResource(CoreR.string.status_safe), color = MaterialTheme.colorScheme.tertiary,
                    modifier = Modifier.testTag("recovery-safe"))
            }
            Text(stringResource(if (s.allWell) R.string.recovery_all_well else R.string.recovery_intro))
        }
    }
    item { Heading(R.string.recovery_status) }
    items(s.groups, key = { "group-${it.first}" }) { (group, state) ->
        Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 6.dp).testTag("recovery-group-$group"),
            verticalAlignment = Alignment.CenterVertically) {
            Text(stringResource(groupWord(group)), Modifier.weight(1f))
            Text(stringResource(stateWord(state)), color = stateColor(state))
        }
    }
    item { Heading(R.string.recovery_parts) }
    items(s.components, key = { "part-${it.id}" }) { part ->
        PartRow(part, busy = model.running == RecoveryAction.Restart(part.id)) { ask(RecoveryAction.Restart(part.id)) }
        HorizontalDivider()
    }
    item { Heading(R.string.recovery_actions) }
    item {
        val context = LocalContext.current
        Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            ActionButton(RecoveryAction.RestartAll, model, ask)
            if (s.safeMode) {
                ActionButton(RecoveryAction.LeaveSafe, model, ask)
                ActionButton(RecoveryAction.StaySafe, model, ask)
            } else {
                ActionButton(RecoveryAction.SafeMode, model, ask)
            }
            s.rollbackVersion?.let { ActionButton(RecoveryAction.Rollback(it), model, ask) }
            FetchButton(RecoveryModel.Fetching.Backups, R.string.recovery_restore, "recovery-action-restore", model) { model.chooseBackup() }
            FetchButton(RecoveryModel.Fetching.Diagnostics, R.string.recovery_diagnostics, "recovery-action-diagnostics", model) {
                model.fetchDiagnostics()
            }
            Text(
                stringResource(R.string.recovery_disk, bytes(context, s.diskFree), bytes(context, s.logsBytes)),
                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.testTag("recovery-disk"),
            )
        }
    }
    item { Heading(R.string.recovery_problems) }
    if (model.lines.isEmpty()) item { Text(stringResource(R.string.recovery_no_lines), Modifier.padding(horizontal = 16.dp)) }
    items(model.lines.size) { i -> LineRow(model.lines[i]) }
    item {
        val scope = rememberCoroutineScope()
        TextButton(onClick = { scope.launch { model.loadMore() } }, modifier = Modifier.padding(horizontal = 8.dp).testTag("recovery-more")) {
            Text(stringResource(R.string.recovery_more))
        }
    }
    item {
        val context = LocalContext.current
        Column(Modifier.fillMaxWidth().padding(16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            TextButton(onClick = {
                runCatching {
                    val url = remote.client.home.newBuilder().encodedPath("/recovery").build().toString()
                    context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
                }
            }, modifier = Modifier.testTag("recovery-web")) { Text(stringResource(R.string.recovery_open_web)) }
        }
    }
}

@Composable
private fun Heading(text: Int) {
    Text(stringResource(text), style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(start = 16.dp, end = 16.dp, top = 16.dp, bottom = 4.dp))
}

@Composable
private fun ActionButton(action: RecoveryAction, model: RecoveryModel, ask: (RecoveryAction) -> Unit) {
    OutlinedButton(onClick = { ask(action) }, enabled = model.running == null && model.fetching == null,
        modifier = Modifier.fillMaxWidth().testTag("recovery-action-${action.wire}")) {
        Text(if (model.running == action) stringResource(R.string.diag_loading) else actionTitle(action))
    }
}

/** Restore a backup and Download diagnostics: a read first (the backups, the bundle), its progress in the button. */
@Composable
private fun FetchButton(kind: RecoveryModel.Fetching, label: Int, tag: String, model: RecoveryModel, run: suspend () -> Unit) {
    val scope = rememberCoroutineScope()
    OutlinedButton(onClick = { scope.launch { run() } }, enabled = model.running == null && model.fetching == null,
        modifier = Modifier.fillMaxWidth().testTag(tag)) {
        Text(stringResource(if (model.fetching == kind) R.string.diag_loading else label))
    }
}

/** One part: its catalog id, its state and reason, restarts, when it last stopped, its last error, and Restart. */
@Composable
private fun PartRow(part: RecoveryPart, busy: Boolean, restart: () -> Unit) {
    Column(Modifier.fillMaxWidth().padding(16.dp).testTag("recovery-part-${part.id}"), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            // the id left to right at the row's start, the state at its end (in Arabic too)
            Text(part.id, fontFamily = FontFamily.Monospace,
                style = MaterialTheme.typography.bodyMedium.copy(textDirection = TextDirection.Ltr))
            Spacer(Modifier.weight(1f))
            Text(stringResource(stateWord(part.state)), color = stateColor(part.state))
        }
        part.reason?.takeIf { it.isNotEmpty() }?.let {
            Text(it, style = MaterialTheme.typography.bodySmall.copy(textDirection = TextDirection.Ltr), fontFamily = FontFamily.Monospace,
                color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        val at = part.lastExit?.let(::localTime)
        Text(
            if (at != null && part.lastCode != null) stringResource(R.string.recovery_restarts_exit, part.restarts, at, part.lastCode!!)
            else stringResource(R.string.recovery_restarts, part.restarts),
            style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        part.lastError?.takeIf { it.isNotEmpty() }?.let {
            Text(it, style = MaterialTheme.typography.bodySmall.copy(textDirection = TextDirection.Ltr), fontFamily = FontFamily.Monospace,
                color = MaterialTheme.colorScheme.error, maxLines = 4, modifier = Modifier.fillMaxWidth())
        }
        if (part.canRestart) {
            Button(onClick = restart, enabled = !busy, modifier = Modifier.testTag("recovery-restart-${part.id}")) {
                Text(stringResource(if (busy) R.string.diag_loading else R.string.recovery_restart))
            }
        }
    }
}

/** One warning or error line: its words, then when, its level and part. */
@Composable
private fun LineRow(line: RecoveryLine) {
    Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 6.dp)) {
        Text(line.msg, style = MaterialTheme.typography.bodySmall.copy(textDirection = TextDirection.Ltr), fontFamily = FontFamily.Monospace,
            color = if (line.level == "error") MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurface, maxLines = 6,
            modifier = Modifier.fillMaxWidth())
        Text(listOfNotNull(line.ts?.let(::localTime), line.level, line.component.ifEmpty { line.service }).filter { it.isNotEmpty() }.joinToString(" · "),
            style = MaterialTheme.typography.labelSmall.copy(textDirection = TextDirection.Ltr), color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.fillMaxWidth())
    }
}

@Composable
private fun actionTitle(a: RecoveryAction): String = when (a) {
    is RecoveryAction.Restart -> stringResource(R.string.recovery_restart)
    RecoveryAction.RestartAll -> stringResource(R.string.recovery_restart_all)
    RecoveryAction.SafeMode -> stringResource(R.string.recovery_safe_mode)
    RecoveryAction.StaySafe -> stringResource(R.string.recovery_stay_safe)
    RecoveryAction.LeaveSafe -> stringResource(R.string.recovery_leave_safe)
    is RecoveryAction.Rollback -> stringResource(R.string.recovery_rollback, a.version)
    is RecoveryAction.Restore -> stringResource(R.string.recovery_restore_named, a.backup)
}

internal fun groupWord(g: String): Int = when (g) {
    "database" -> R.string.recovery_group_database
    "api" -> R.string.recovery_group_api
    "streaming" -> R.string.recovery_group_streaming
    "processing" -> R.string.recovery_group_processing
    "search" -> R.string.recovery_group_search
    "logs" -> R.string.recovery_group_logs
    else -> R.string.recovery_group_other
}

internal fun stateWord(s: String): Int = when (s) {
    "ok" -> R.string.recovery_state_ok
    "starting" -> R.string.recovery_state_starting
    "degraded" -> R.string.recovery_state_degraded
    "restarting" -> R.string.recovery_state_restarting
    "down" -> R.string.recovery_state_down
    else -> R.string.recovery_state_off
}

@Composable
private fun stateColor(s: String): Color = when (s) {
    "ok" -> Color(0xFF3FA55B)
    "starting", "degraded" -> MaterialTheme.colorScheme.tertiary
    "restarting", "down" -> MaterialTheme.colorScheme.error
    else -> MaterialTheme.colorScheme.onSurfaceVariant
}

private fun bytes(context: android.content.Context, n: Long): String = if (n < 0) "?" else Formatter.formatShortFileSize(context, n)

/** An RFC 3339 time in the device's zone and language, or the text as it came. */
private fun localTime(s: String): String = runCatching {
    DateTimeFormatter.ofLocalizedDateTime(FormatStyle.SHORT).format(Instant.parse(s).atZone(ZoneId.systemDefault()))
}.getOrDefault(s)

/** The account menu's entry (Remote only, admins). */
@Composable
fun RecoveryMenuItem(onOpen: () -> Unit) {
    DropdownMenuItem(text = { Text(stringResource(R.string.recovery_title)) }, onClick = onOpen, modifier = Modifier.testTag("account-recovery"))
}
