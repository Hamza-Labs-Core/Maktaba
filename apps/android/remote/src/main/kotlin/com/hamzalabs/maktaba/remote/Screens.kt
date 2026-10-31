package com.hamzalabs.maktaba.remote

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.outlined.CheckCircle
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.ClusterOverview
import com.hamzalabs.maktaba.core.api.FailureGroup
import com.hamzalabs.maktaba.core.api.JobRef
import com.hamzalabs.maktaba.core.api.ProcessesDoc
import com.hamzalabs.maktaba.core.api.Library
import com.hamzalabs.maktaba.core.api.Machine
import com.hamzalabs.maktaba.core.api.QueueSnapshot
import com.hamzalabs.maktaba.core.api.StageRow
import com.hamzalabs.maktaba.core.ui.Message
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** Loads now and again every [everyMs] while the screen is up; [refreshing] while it runs. */
@Composable
internal fun Polling(everyMs: Long, load: suspend () -> Unit) {
    LaunchedEffect(Unit) {
        while (true) {
            load()
            delay(everyMs)
        }
    }
}

@Composable
internal fun chosenColors(chosen: Boolean) = ListItemDefaults.colors(
    containerColor = if (chosen) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surface,
)

@Composable
internal fun Detail(content: @Composable () -> Unit) {
    Column(
        Modifier.verticalScroll(rememberScrollState()).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) { content() }
}

@Composable
internal fun Fact(label: String, value: String) {
    // the label keeps its width; a long value wraps beside it
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
        Text(label, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(value, Modifier.weight(1f), style = MaterialTheme.typography.bodyLarge, textAlign = TextAlign.End)
    }
}

@Composable
internal fun Busy() = CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)

// ---------- Needs attention ----------

/**
 * "Needs attention": the home's failed jobs grouped by cause, a Retry on each group that can be
 * retried, and Retry all (as on the web's queue page). The home checks each retry is an admin's.
 */
@Composable
fun AttentionScreen(remote: Remote, account: @Composable () -> Unit) {
    var groups by remember { mutableStateOf<List<FailureGroup>?>(null) }
    var chosen by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf<String?>(null) }
    var refreshing by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    suspend fun load() {
        remote.load { needsAttention() }?.let { groups = it }
    }
    suspend fun retry(gs: List<FailureGroup>) {
        busy = if (gs.size == 1) gs[0].id else "all"
        remote.startAction()
        for (g in gs) remote.act(g.label) { retry(g.stage, g.cause) }
        busy = null
        load()
    }
    Polling(15_000) { load() }
    val retryable = groups.orEmpty().filter { it.retryable }
    val group = groups?.firstOrNull { it.id == chosen }
    AdminScreen(
        title = stringResource(R.string.place_attention),
        remote = remote,
        account = account,
        refreshing = refreshing,
        onRefresh = { refreshing = true; remote.clear(); load(); refreshing = false },
        actions = {
            TextButton(onClick = { scope.launch { retry(retryable) } }, enabled = retryable.isNotEmpty() && busy == null) {
                if (busy == "all") Busy() else Text(stringResource(R.string.retry_all))
            }
        },
        chosenTitle = group?.label,
        onUnchoose = { chosen = null },
        detail = group?.let { g ->
            {
                Detail {
                    Text(pluralStringResource(R.plurals.jobs, g.count, g.count), style = MaterialTheme.typography.headlineSmall)
                    Fact(stringResource(R.string.stage), g.stage)
                    Fact(stringResource(R.string.cause), g.cause)
                    g.sampleMessage?.takeIf { it.isNotEmpty() }?.let {
                        Text(it, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    if (g.retryable) {
                        Button(onClick = { scope.launch { retry(listOf(g)) } }, enabled = busy == null) {
                            if (busy == g.id) Busy() else Text(stringResource(R.string.retry))
                        }
                    } else {
                        Text(
                            stringResource(R.string.fixed_on_web, Say.spaced(g.action)),
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
        },
    ) {
        val gs = groups
        when {
            gs == null -> item { if (!remote.hasProblem) Message(stringResource(R.string.loading)) }
            gs.isEmpty() -> item {
                ListItem(
                    headlineContent = { Text(stringResource(R.string.nothing_needs_attention)) },
                    leadingContent = { Icon(Icons.Outlined.CheckCircle, null, tint = MaterialTheme.colorScheme.primary) },
                )
            }
            else -> items(gs, key = { it.id }) { g ->
                ListItem(
                    headlineContent = { Text(stringResource(R.string.count_label, g.count, g.label)) },
                    supportingContent = {
                        Column {
                            Text(g.stage)
                            g.sampleMessage?.takeIf { it.isNotEmpty() }?.let { Text(it, maxLines = 2) }
                        }
                    },
                    trailingContent = {
                        if (g.retryable) {
                            OutlinedButton(onClick = { scope.launch { retry(listOf(g)) } }, enabled = busy == null) {
                                if (busy == g.id) Busy() else Text(stringResource(R.string.retry))
                            }
                        }
                    },
                    colors = chosenColors(g.id == chosen),
                    modifier = Modifier.clickable { chosen = g.id },
                )
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            }
        }
    }
}

// ---------- Queue ----------

/**
 * The processing stages with their counts, why each waits and its health; the jobs running
 * now; pause and resume one stage, or all of them. A chosen stage shows its failures by cause.
 */
@Composable
fun QueueScreen(remote: Remote, account: @Composable () -> Unit) {
    var snap by remember { mutableStateOf<QueueSnapshot?>(null) }
    var running by remember { mutableStateOf<List<JobRef>?>(null) }
    var failures by remember { mutableStateOf<Pair<String, List<FailureGroup>>?>(null) }
    var chosen by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf<String?>(null) }
    var refreshing by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    suspend fun load() {
        remote.load { queues() }?.let { snap = it }
        remote.load { runningJobs() }?.let { running = it }
    }
    fun act(id: String, block: suspend com.hamzalabs.maktaba.core.api.ApiClient.() -> Unit) {
        scope.launch {
            busy = id
            remote.startAction()
            remote.act(block = block)
            busy = null
            load()
        }
    }
    // the snapshot counts every job: a busy home takes seconds to answer, so not too often
    Polling(30_000) { load() }
    // the chosen stage's failures, read when it is chosen
    LaunchedEffect(chosen) {
        val stage = chosen?.takeUnless { it.startsWith("job:") } ?: return@LaunchedEffect
        if (failures?.first != stage) failures = null
        remote.load { stageFailures(stage) }?.let { failures = stage to it }
    }
    QueueContent(
        snap, running, failures, chosen, { chosen = it }, busy, remote, account, refreshing,
        onRefresh = { refreshing = true; remote.clear(); load(); refreshing = false },
        act = ::act,
    )
}

/** The Queue screen for [snap] and the [running] jobs, apart from how they are read. */
@Composable
internal fun QueueContent(
    snap: QueueSnapshot?,
    running: List<JobRef>?,
    failures: Pair<String, List<FailureGroup>>?,
    chosen: String?,
    choose: (String?) -> Unit,
    busy: String?,
    remote: Remote,
    account: @Composable () -> Unit,
    refreshing: Boolean,
    onRefresh: suspend () -> Unit,
    act: (String, suspend com.hamzalabs.maktaba.core.api.ApiClient.() -> Unit) -> Unit,
) {
    val row = snap?.rows?.firstOrNull { it.id == chosen }
    val job = running?.firstOrNull { "job:${it.id}" == chosen }
    val labelOf = { stage: String -> snap?.stages?.firstOrNull { it.stage == stage }?.label?.ifEmpty { null } ?: stage }
    val say = rememberSay()
    AdminScreen(
        title = stringResource(R.string.place_queue),
        remote = remote,
        account = account,
        refreshing = refreshing,
        onRefresh = onRefresh,
        actions = {
            val s = snap
            if (s != null) {
                TextButton(onClick = { act("all") { if (s.pausedAll) resumeAll() else pauseAll() } }, enabled = busy == null) {
                    if (busy == "all") Busy() else Text(stringResource(if (s.pausedAll) R.string.resume_all else R.string.pause_all))
                }
            }
        },
        chosenTitle = row?.label ?: job?.let { it.subjectLabel.ifEmpty { it.subjectId } },
        onUnchoose = { choose(null) },
        detail = when {
            row != null -> { { StageDetail(row, failures?.takeIf { it.first == row.id }?.second, busy == row.id) { act(row.id) { if (row.stage.paused) resumeStage(row.id) else pauseStage(row.id) } } } }
            job != null -> { { JobDetail(job, labelOf(job.stage)) } }
            else -> null
        },
    ) {
        val s = snap
        when {
            s == null -> item { if (!remote.hasProblem) Message(stringResource(R.string.loading)) }
            else -> {
                if (s.pausedAll) {
                    item {
                        Text(
                            stringResource(R.string.all_paused),
                            color = MaterialTheme.colorScheme.primary,
                            modifier = Modifier.padding(16.dp),
                        )
                    }
                }
                healthRow(s.health, say)
                runningRows(running, labelOf, chosen, say) { choose(it) }
                item { Header(stringResource(R.string.queues)) }
                items(s.rows, key = { it.id }) { r ->
                    val c = r.counts
                    val health = say.health(r.summary?.health, c.failedOpen)
                    ListItem(
                        headlineContent = { Text(r.label) },
                        supportingContent = {
                            Column {
                                Text(
                                    listOfNotNull(
                                        stringResource(R.string.queue_counts, c.ready, c.running),
                                        stringResource(R.string.flag_paused).takeIf { r.stage.paused },
                                        stringResource(R.string.flag_off).takeIf { !r.stage.enabled },
                                    ).joinToString(" · "),
                                )
                                waitingOf(r, say)?.let { Text(it, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                                if (health.isNotEmpty()) {
                                    Text(
                                        health.joinToString(" · "),
                                        color = if (r.summary?.health?.state == "failing" || (r.summary?.health == null && c.failedOpen > 0)) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.tertiary,
                                    )
                                }
                            }
                        },
                        trailingContent = { Text(stringResource(R.string.done_today, c.done24h), color = MaterialTheme.colorScheme.onSurfaceVariant) },
                        colors = chosenColors(r.id == chosen),
                        modifier = Modifier.clickable { choose(r.id) },
                    )
                    HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                }
            }
        }
    }
}

/** Why the stage's jobs wait, in the web's words (none while it runs or has nothing waiting). */
private fun waitingOf(r: StageRow, say: Say): String? {
    val q = r.summary ?: return null
    if (q.waitingReason == "idle") return null
    return say.queueWaiting(q.waitingReason, q.retryingService, r.label)
}

@Composable
private fun StageDetail(r: StageRow, failures: List<FailureGroup>?, busy: Boolean, toggle: () -> Unit) {
    val c = r.counts
    val q = r.summary
    val say = rememberSay()
    Detail {
        waitingOf(r, say)?.let { Text(it, style = MaterialTheme.typography.bodyLarge) }
        Fact(stringResource(R.string.waiting), say.number(c.ready))
        Fact(stringResource(R.string.running), say.number(c.running))
        Fact(stringResource(R.string.failed), say.number(c.failedOpen))
        Fact(stringResource(R.string.done_24h), say.number(c.done24h))
        q?.let {
            Fact(stringResource(R.string.rate), stringResource(R.string.rate_per_minute, it.ratePerMin))
            it.etaSec?.let { eta -> Fact(stringResource(R.string.done_in), say.minutes(eta)) }
            it.oldestWaitingSec?.let { w -> Fact(stringResource(R.string.oldest_waiting), say.minutes(w)) }
            if (it.servedBy.isNotEmpty()) Fact(stringResource(R.string.served_by), it.servedBy.joinToString())
            if (it.noWorker) Text(stringResource(R.string.no_worker), color = MaterialTheme.colorScheme.error)
        }
        q?.health?.let { h ->
            Text(stringResource(R.string.health), style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary)
            Fact(stringResource(R.string.state), say.queueState(h.state))
            if (h.windowHours > 0) Fact(stringResource(R.string.failed_in_window, h.windowHours), stringResource(R.string.of_total, h.failedWindow, h.finishedWindow))
            say.health(h, c.failedOpen).forEach { Text(it) }
            h.failing?.example?.takeIf { it.isNotEmpty() }?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
            h.waiting.forEach { w ->
                Text(
                    stringResource(R.string.waiting_for, w.count, say.waitingOn(w.on)).let { line -> w.message?.takeIf { it.isNotEmpty() }?.let { say.t(R.string.say_label_value, line, it) } ?: line },
                    style = MaterialTheme.typography.bodySmall,
                )
            }
        }
        r.stage.disabledReason?.let { Text(it, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        FilledTonalButton(onClick = toggle, enabled = !busy) {
            if (busy) Busy() else Text(stringResource(if (r.stage.paused) R.string.resume else R.string.pause))
        }
        StageFailures(failures)
    }
}


// ---------- Libraries ----------

/** The home's libraries, their folders, and a scan (new or changed files). */
@Composable
fun LibrariesScreen(remote: Remote, account: @Composable () -> Unit) {
    var libraries by remember { mutableStateOf<List<Library>?>(null) }
    var chosen by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf<String?>(null) }
    var scanned by remember { mutableStateOf(setOf<String>()) }
    var refreshing by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    suspend fun load() {
        remote.load { libraries() }?.let { libraries = it }
    }
    fun scan(id: String) {
        scope.launch {
            busy = id
            remote.startAction()
            if (remote.act { scanLibrary(id) }) scanned = scanned + id
            busy = null
        }
    }
    LaunchedEffect(Unit) { load() }
    val lib = libraries?.firstOrNull { it.id == chosen }
    AdminScreen(
        title = stringResource(R.string.place_libraries),
        remote = remote,
        account = account,
        refreshing = refreshing,
        onRefresh = { refreshing = true; remote.clear(); load(); refreshing = false },
        chosenTitle = lib?.name,
        onUnchoose = { chosen = null },
        detail = lib?.let { l ->
            {
                Detail {
                    Text(stringResource(R.string.folders), style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary)
                    l.roots.forEach { Text(it, style = MaterialTheme.typography.bodyMedium) }
                    Button(onClick = { scan(l.id) }, enabled = busy == null) {
                        if (busy == l.id) Busy() else Text(stringResource(if (l.id in scanned) R.string.scan_again else R.string.scan_new))
                    }
                    if (l.id in scanned) Text(stringResource(R.string.scan_queued), color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
        },
    ) {
        val ls = libraries
        when {
            ls == null -> item { if (!remote.hasProblem) Message(stringResource(R.string.loading)) }
            ls.isEmpty() -> item { Message(stringResource(R.string.no_libraries), detail = stringResource(R.string.no_libraries_hint)) }
            else -> items(ls, key = { it.id }) { l ->
                ListItem(
                    headlineContent = { Text(l.name) },
                    supportingContent = { if (l.roots.isNotEmpty()) Text(l.roots.joinToString(), maxLines = 2) },
                    trailingContent = {
                        androidx.compose.material3.IconButton(onClick = { scan(l.id) }, enabled = busy == null) {
                            if (busy == l.id) Busy() else Icon(Icons.Filled.Refresh, stringResource(R.string.scan_named, l.name))
                        }
                    },
                    colors = chosenColors(l.id == chosen),
                    modifier = Modifier.clickable { chosen = l.id },
                )
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            }
        }
    }
}

// ---------- Machines ----------

/**
 * This home, the processes its supervisor runs, and the machines that work for it; drain one
 * (it finishes its jobs, takes no new ones).
 */
@Composable
fun MachinesScreen(remote: Remote, account: @Composable () -> Unit) {
    var overview by remember { mutableStateOf<ClusterOverview?>(null) }
    var processes by remember { mutableStateOf<ProcessesDoc?>(null) }
    var chosen by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf<String?>(null) }
    var refreshing by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    suspend fun load() {
        remote.load { machines() }?.let { overview = it }
        remote.load { processes() }?.let { processes = it }
    }
    Polling(15_000) { load() }
    val machine = overview?.nodes?.firstOrNull { it.id == chosen }
    val process = processes?.processes?.firstOrNull { "proc:${it.id}" == chosen }
    val say = rememberSay()
    AdminScreen(
        title = stringResource(R.string.place_machines),
        remote = remote,
        account = account,
        refreshing = refreshing,
        onRefresh = { refreshing = true; remote.clear(); load(); refreshing = false },
        chosenTitle = machine?.name ?: process?.let { say.process(it) },
        onUnchoose = { chosen = null },
        detail = when {
            machine != null -> {
                {
                    MachineDetail(machine, busy == machine.id) { drain ->
                        scope.launch {
                            busy = machine.id
                            remote.startAction()
                            remote.act { setDrain(machine.id, drain) }
                            busy = null
                            load()
                        }
                    }
                }
            }
            process != null -> { { ProcessDetail(process, processes?.events.orEmpty()) } }
            else -> null
        },
    ) {
        val o = overview
        when {
            o == null -> item { if (!remote.hasProblem) Message(stringResource(R.string.loading)) }
            else -> {
                item {
                    ListItem(
                        headlineContent = { Text(o.home.name.ifEmpty { stringResource(R.string.this_home) }) },
                        supportingContent = { Text(listOfNotNull(o.home.version.takeIf { it.isNotEmpty() }, stringResource(R.string.the_home)).joinToString(" · ")) },
                    )
                    HorizontalDivider()
                }
                processRows(processes, chosen, say) { chosen = it }
                item { Header(stringResource(R.string.other_machines)) }
                if (o.nodes.isEmpty()) item { Message(stringResource(R.string.no_other_machines), detail = stringResource(R.string.no_other_machines_hint)) }
                items(o.nodes, key = { it.id }) { m ->
                    ListItem(
                        headlineContent = { Text(m.name.ifEmpty { m.id.take(8) }) },
                        supportingContent = { Text(machineState(m, say)) },
                        leadingContent = { Dot(if (m.online) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.outline) },
                        colors = chosenColors(m.id == chosen),
                        modifier = Modifier.clickable { chosen = m.id },
                    )
                    HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                }
            }
        }
    }
}

private fun machineState(m: Machine, say: Say): String = listOfNotNull(
    say.t(if (m.online) R.string.online else R.string.offline),
    m.state.takeIf { it != "approved" }?.let(say::machineStatus),
    say.t(R.string.flag_draining).takeIf { m.drain },
    listOfNotNull(m.os, m.arch).takeIf { it.isNotEmpty() }?.joinToString("/"),
).joinToString(" · ")

@Composable
internal fun Dot(color: Color) {
    androidx.compose.foundation.Canvas(Modifier.size(10.dp)) { drawCircle(color) }
}

@Composable
private fun MachineDetail(m: Machine, busy: Boolean, setDrain: (Boolean) -> Unit) {
    val say = rememberSay()
    Detail {
        Fact(stringResource(R.string.state), say.machineStatus(m.state))
        Fact(stringResource(R.string.online), stringResource(if (m.online) R.string.yes else R.string.no))
        listOfNotNull(m.os, m.arch).takeIf { it.isNotEmpty() }?.let { Fact(stringResource(R.string.system), it.joinToString("/")) }
        m.lastSeenAt?.let { Fact(stringResource(R.string.last_seen), say.whenText(it)) }
        if (m.state == "approved") {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(stringResource(R.string.drain), style = MaterialTheme.typography.titleSmall)
                    Text(
                        stringResource(R.string.drain_hint),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                if (busy) Busy() else Switch(checked = m.drain, onCheckedChange = setDrain)
            }
        }
    }
}
