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
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.ClusterOverview
import com.hamzalabs.maktaba.core.api.FailureGroup
import com.hamzalabs.maktaba.core.api.Library
import com.hamzalabs.maktaba.core.api.Machine
import com.hamzalabs.maktaba.core.api.QueueSnapshot
import com.hamzalabs.maktaba.core.api.StageRow
import com.hamzalabs.maktaba.core.ui.Message
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** Loads now and again every [everyMs] while the screen is up; [refreshing] while it runs. */
@Composable
private fun Polling(everyMs: Long, load: suspend () -> Unit) {
    LaunchedEffect(Unit) {
        while (true) {
            load()
            delay(everyMs)
        }
    }
}

@Composable
private fun chosenColors(chosen: Boolean) = ListItemDefaults.colors(
    containerColor = if (chosen) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surface,
)

@Composable
private fun Detail(content: @Composable () -> Unit) {
    Column(
        Modifier.verticalScroll(rememberScrollState()).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) { content() }
}

@Composable
private fun Fact(label: String, value: String) {
    // the label keeps its width; a long value wraps beside it
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
        Text(label, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(value, Modifier.weight(1f), style = MaterialTheme.typography.bodyLarge, textAlign = TextAlign.End)
    }
}

@Composable
private fun Busy() = CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)

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
        title = "Needs attention",
        remote = remote,
        account = account,
        refreshing = refreshing,
        onRefresh = { refreshing = true; remote.clear(); load(); refreshing = false },
        actions = {
            TextButton(onClick = { scope.launch { retry(retryable) } }, enabled = retryable.isNotEmpty() && busy == null) {
                if (busy == "all") Busy() else Text("Retry all")
            }
        },
        chosenTitle = group?.label,
        onUnchoose = { chosen = null },
        detail = group?.let { g ->
            {
                Detail {
                    Text("${g.count} ${if (g.count == 1) "job" else "jobs"}", style = MaterialTheme.typography.headlineSmall)
                    Fact("Stage", g.stage)
                    Fact("Cause", g.cause)
                    g.sampleMessage?.takeIf { it.isNotEmpty() }?.let {
                        Text(it, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    if (g.retryable) {
                        Button(onClick = { scope.launch { retry(listOf(g)) } }, enabled = busy == null) {
                            if (busy == g.id) Busy() else Text("Retry")
                        }
                    } else {
                        Text(
                            "This one is fixed from the home's web page (${g.action.replace('_', ' ')}).",
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
        },
    ) {
        val gs = groups
        when {
            gs == null -> item { if (remote.problem == null) Message("Loading…") }
            gs.isEmpty() -> item {
                ListItem(
                    headlineContent = { Text("Nothing needs attention") },
                    leadingContent = { Icon(Icons.Outlined.CheckCircle, null, tint = MaterialTheme.colorScheme.primary) },
                )
            }
            else -> items(gs, key = { it.id }) { g ->
                ListItem(
                    headlineContent = { Text("${g.count}  ${g.label}") },
                    supportingContent = {
                        Column {
                            Text(g.stage)
                            g.sampleMessage?.takeIf { it.isNotEmpty() }?.let { Text(it, maxLines = 2) }
                        }
                    },
                    trailingContent = {
                        if (g.retryable) {
                            OutlinedButton(onClick = { scope.launch { retry(listOf(g)) } }, enabled = busy == null) {
                                if (busy == g.id) Busy() else Text("Retry")
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

/** The processing stages with their counts; pause and resume one, or all of them. */
@Composable
fun QueueScreen(remote: Remote, account: @Composable () -> Unit) {
    var snap by remember { mutableStateOf<QueueSnapshot?>(null) }
    var chosen by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf<String?>(null) }
    var refreshing by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    suspend fun load() {
        remote.load { queues() }?.let { snap = it }
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
    val row = snap?.rows?.firstOrNull { it.id == chosen }
    AdminScreen(
        title = "Queue",
        remote = remote,
        account = account,
        refreshing = refreshing,
        onRefresh = { refreshing = true; remote.clear(); load(); refreshing = false },
        actions = {
            val s = snap
            if (s != null) {
                TextButton(onClick = { act("all") { if (s.pausedAll) resumeAll() else pauseAll() } }, enabled = busy == null) {
                    if (busy == "all") Busy() else Text(if (s.pausedAll) "Resume all" else "Pause all")
                }
            }
        },
        chosenTitle = row?.label,
        onUnchoose = { chosen = null },
        detail = row?.let { r -> { StageDetail(r, busy == r.id) { act(r.id) { if (r.stage.paused) resumeStage(r.id) else pauseStage(r.id) } } } },
    ) {
        val s = snap
        when {
            s == null -> item { if (remote.problem == null) Message("Loading…") }
            else -> {
                if (s.pausedAll) {
                    item {
                        Text(
                            "All processing is paused.",
                            color = MaterialTheme.colorScheme.primary,
                            modifier = Modifier.padding(16.dp),
                        )
                    }
                }
                items(s.rows, key = { it.id }) { r ->
                    val c = r.counts
                    ListItem(
                        headlineContent = { Text(r.label) },
                        supportingContent = {
                            Text(
                                buildString {
                                    append("${c.ready} waiting · ${c.running} running")
                                    if (c.failedOpen > 0) append(" · ${c.failedOpen} failed")
                                    if (r.stage.paused) append(" · paused")
                                    if (!r.stage.enabled) append(" · off")
                                },
                            )
                        },
                        trailingContent = { Text("${c.done24h} today", color = MaterialTheme.colorScheme.onSurfaceVariant) },
                        colors = chosenColors(r.id == chosen),
                        modifier = Modifier.clickable { chosen = r.id },
                    )
                    HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                }
            }
        }
    }
}

@Composable
private fun StageDetail(r: StageRow, busy: Boolean, toggle: () -> Unit) {
    val c = r.counts
    val q = r.summary
    Detail {
        Fact("Waiting", "${c.ready}")
        Fact("Running", "${c.running}")
        Fact("Failed", "${c.failedOpen}")
        Fact("Done in 24 h", "${c.done24h}")
        q?.let {
            Fact("Rate", "%.1f a minute".format(it.ratePerMin))
            it.etaSec?.let { eta -> Fact("Done in", minutes(eta)) }
            it.oldestWaitingSec?.let { w -> Fact("Oldest waiting", minutes(w)) }
            if (it.servedBy.isNotEmpty()) Fact("Served by", it.servedBy.joinToString())
            if (it.noWorker) Text("No machine serves this stage now.", color = MaterialTheme.colorScheme.error)
        }
        r.stage.disabledReason?.let { Text(it, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        FilledTonalButton(onClick = toggle, enabled = !busy) {
            if (busy) Busy() else Text(if (r.stage.paused) "Resume" else "Pause")
        }
    }
}

private fun minutes(sec: Double): String = when {
    sec < 90 -> "${sec.toInt()} s"
    sec < 5400 -> "${(sec / 60).toInt()} min"
    else -> "%.1f h".format(sec / 3600)
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
        title = "Libraries",
        remote = remote,
        account = account,
        refreshing = refreshing,
        onRefresh = { refreshing = true; remote.clear(); load(); refreshing = false },
        chosenTitle = lib?.name,
        onUnchoose = { chosen = null },
        detail = lib?.let { l ->
            {
                Detail {
                    Text("Folders", style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary)
                    l.roots.forEach { Text(it, style = MaterialTheme.typography.bodyMedium) }
                    Button(onClick = { scan(l.id) }, enabled = busy == null) {
                        if (busy == l.id) Busy() else Text(if (l.id in scanned) "Scan again" else "Scan for new files")
                    }
                    if (l.id in scanned) Text("Scan queued.", color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
        },
    ) {
        val ls = libraries
        when {
            ls == null -> item { if (remote.problem == null) Message("Loading…") }
            ls.isEmpty() -> item { Message("No libraries yet", detail = "Add one from the home's web Settings.") }
            else -> items(ls, key = { it.id }) { l ->
                ListItem(
                    headlineContent = { Text(l.name) },
                    supportingContent = { if (l.roots.isNotEmpty()) Text(l.roots.joinToString(), maxLines = 2) },
                    trailingContent = {
                        androidx.compose.material3.IconButton(onClick = { scan(l.id) }, enabled = busy == null) {
                            if (busy == l.id) Busy() else Icon(Icons.Filled.Refresh, "Scan ${l.name}")
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

/** This home and the machines that work for it; drain one (it finishes its jobs, takes no new ones). */
@Composable
fun MachinesScreen(remote: Remote, account: @Composable () -> Unit) {
    var overview by remember { mutableStateOf<ClusterOverview?>(null) }
    var chosen by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf<String?>(null) }
    var refreshing by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    suspend fun load() {
        remote.load { machines() }?.let { overview = it }
    }
    Polling(15_000) { load() }
    val machine = overview?.nodes?.firstOrNull { it.id == chosen }
    AdminScreen(
        title = "Machines",
        remote = remote,
        account = account,
        refreshing = refreshing,
        onRefresh = { refreshing = true; remote.clear(); load(); refreshing = false },
        chosenTitle = machine?.name,
        onUnchoose = { chosen = null },
        detail = machine?.let { m ->
            {
                MachineDetail(m, busy == m.id) { drain ->
                    scope.launch {
                        busy = m.id
                        remote.startAction()
                        remote.act { setDrain(m.id, drain) }
                        busy = null
                        load()
                    }
                }
            }
        },
    ) {
        val o = overview
        when {
            o == null -> item { if (remote.problem == null) Message("Loading…") }
            else -> {
                item {
                    ListItem(
                        headlineContent = { Text(o.home.name.ifEmpty { "This home" }) },
                        supportingContent = { Text(listOfNotNull(o.home.version.takeIf { it.isNotEmpty() }, "the home").joinToString(" · ")) },
                    )
                    HorizontalDivider()
                }
                if (o.nodes.isEmpty()) item { Message("No other machines", detail = "Join one from the home's web Settings.") }
                items(o.nodes, key = { it.id }) { m ->
                    ListItem(
                        headlineContent = { Text(m.name.ifEmpty { m.id.take(8) }) },
                        supportingContent = { Text(machineState(m)) },
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

private fun machineState(m: Machine): String = buildString {
    append(if (m.online) "Online" else "Offline")
    if (m.state != "approved") append(" · ${m.state}")
    if (m.drain) append(" · draining")
    listOfNotNull(m.os, m.arch).takeIf { it.isNotEmpty() }?.let { append(" · ${it.joinToString("/")}") }
}

@Composable
private fun Dot(color: Color) {
    androidx.compose.foundation.Canvas(Modifier.size(10.dp)) { drawCircle(color) }
}

@Composable
private fun MachineDetail(m: Machine, busy: Boolean, setDrain: (Boolean) -> Unit) {
    Detail {
        Fact("State", m.state)
        Fact("Online", if (m.online) "yes" else "no")
        listOfNotNull(m.os, m.arch).takeIf { it.isNotEmpty() }?.let { Fact("System", it.joinToString("/")) }
        m.lastSeenAt?.let { Fact("Last seen", it.replace('T', ' ').substringBefore('.')) }
        if (m.state == "approved") {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text("Drain", style = MaterialTheme.typography.titleSmall)
                    Text(
                        "Finishes its jobs and takes no new ones.",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                if (busy) Busy() else Switch(checked = m.drain, onCheckedChange = setDrain)
            }
        }
    }
}
