package com.hamzalabs.maktaba.remote

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.FailureGroup
import com.hamzalabs.maktaba.core.api.HealthNow
import com.hamzalabs.maktaba.core.api.JobRef
import com.hamzalabs.maktaba.core.api.ProcessEvent
import com.hamzalabs.maktaba.core.api.ProcessStatus
import com.hamzalabs.maktaba.core.api.ProcessesDoc
import com.hamzalabs.maktaba.core.api.UpdateMachine
import com.hamzalabs.maktaba.core.api.UpdateRunView
import com.hamzalabs.maktaba.core.api.UpdateTarget
import com.hamzalabs.maktaba.core.api.UpdatesOverview
import com.hamzalabs.maktaba.core.api.text
import com.hamzalabs.maktaba.core.ui.Message

// The state of the home's running work, read only, as the web admin shows it: its updates,
// its processes (on Machines) and the jobs running now (on Queue).

/** A section's title in a list. */
@Composable
internal fun Header(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.titleSmall,
        color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.fillMaxWidth().padding(start = 16.dp, end = 16.dp, top = 20.dp, bottom = 4.dp),
    )
}

/** A line that needs the owner's eye, above the rest. */
@Composable
internal fun Callout(text: String, error: Boolean = false, detail: String? = null) {
    Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 6.dp)) {
        Text(text, color = if (error) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.tertiary)
        detail?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
    }
}

@Composable
private fun Divider() = HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)

/** How a state reads at a glance: good, under way, needs someone, or neither. */
@Composable
internal fun toneOf(state: String): Color = when (state) {
    "running", "up_to_date", "healthy", "committed", "done", "installed" -> MaterialTheme.colorScheme.primary
    "failed", "failed_prepare", "failed_rollback", "rolled_back", "needs_person", "held", "unhealthy",
    "apply_failed", "prepare_failed", "done_needs_person", "done_with_failures", "offline" -> MaterialTheme.colorScheme.error
    "starting", "backoff", "applying", "watching", "ordered", "app_staged", "updating", "preparing", "restarting",
    "rolling_out", "home_applying", "home_preparing", "draining", "behind" -> MaterialTheme.colorScheme.tertiary
    else -> MaterialTheme.colorScheme.outline
}

// ---------- Updates ----------

/**
 * The home's updates: the version it runs and what it does next, blocked releases, a held TV,
 * the run under way (or the last one) with its steps and machines, and every machine's version.
 */
@Composable
fun UpdatesScreen(remote: Remote, account: @Composable () -> Unit) {
    var ov by remember { mutableStateOf<UpdatesOverview?>(null) }
    var run by remember { mutableStateOf<UpdateRunView?>(null) }
    var chosen by remember { mutableStateOf<String?>(null) }
    var refreshing by remember { mutableStateOf(false) }
    suspend fun load() {
        val o = remote.load { updates() } ?: return
        ov = o
        val id = (o.activeRun ?: o.lastRun)?.id
        run = if (id.isNullOrEmpty()) null else remote.load { updateRun(id) } ?: run?.takeIf { it.id == id }
    }
    Polling(15_000) { load() }
    UpdatesContent(ov, run, chosen, { chosen = it }, remote, account, refreshing) {
        refreshing = true; remote.clear(); load(); refreshing = false
    }
}

/** The Updates screen for [ov] and its [run], apart from how they are read. */
@Composable
internal fun UpdatesContent(
    ov: UpdatesOverview?,
    run: UpdateRunView?,
    chosen: String?,
    choose: (String?) -> Unit,
    remote: Remote,
    account: @Composable () -> Unit,
    refreshing: Boolean,
    onRefresh: suspend () -> Unit,
) {
    val target = run?.targets?.firstOrNull { "target:${it.nodeId}" == chosen }
    val machine = ov?.machines?.firstOrNull { "machine:${it.id}" == chosen }
    val showRun = chosen == "run" && run != null
    AdminScreen(
        title = "Updates",
        remote = remote,
        account = account,
        refreshing = refreshing,
        onRefresh = onRefresh,
        chosenTitle = when {
            showRun -> "Update to ${run!!.version}"
            target != null -> target.name.ifEmpty { target.nodeId.take(8) }
            machine != null -> machine.name.ifEmpty { machine.id.take(8) }
            else -> null
        },
        onUnchoose = { choose(null) },
        detail = when {
            showRun -> { { RunDetail(run!!) } }
            target != null -> { { TargetDetail(target) } }
            machine != null -> { { MachineUpdateDetail(machine) } }
            else -> null
        },
    ) {
        if (ov == null) {
            item { if (!remote.hasProblem) Message("Loading…") }
        } else {
            updatesRows(ov, run, chosen, choose)
        }
    }
}

private fun LazyListScope.updatesRows(ov: UpdatesOverview, run: UpdateRunView?, chosen: String?, choose: (String?) -> Unit) {
    val active = ov.activeRun
    if (ov.checkStale) item { Callout("No successful update check for 14 days. Check the network or GitHub access.", error = true) }
    ov.lastCheckError?.takeIf { it.isNotEmpty() }?.let { e -> item { Callout("The last check for updates failed", error = true, detail = e) } }
    ov.stuck?.takeIf { active == null }?.let { s ->
        item { Callout(Say.stuck(s), detail = "Install it now: the TV and the front door pause while the home restarts.") }
    }
    items(ov.blockedReleases, key = { "blocked:${it.version}" }) { r -> Callout(Say.blocked(r)) }
    ov.tvHeld?.let { tv ->
        item {
            Callout(
                Say.tv(tv.state, tv.heldOn, tv.version),
                error = (tv.state ?: "held") == "held",
                detail = tv.checks.map(Say::tvCheck).ifEmpty { tv.failed.map(Say::spaced) }.joinToString("\n").ifEmpty { null },
            )
        }
    }

    item {
        Header("This home")
        Column(Modifier.padding(horizontal = 16.dp, vertical = 4.dp)) {
            Fact("Running", ov.machines.firstOrNull { it.role == "home" }?.version?.ifEmpty { null } ?: ov.currentVersion.ifEmpty { "-" })
            Fact("Next", Say.planNext(ov))
            Say.tick(ov)?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
            val plan = ov.plan
            plan.target?.let { t -> Fact("Target", t.version) }
            if (plan.held) Fact("Held", "yes")
            plan.reason?.takeIf { it.isNotEmpty() && it != plan.waiting }?.let { Fact("Reason", Say.waiting(it, plan.target?.version ?: "")) }
            plan.eligibleAt?.let { Fact("Old enough on", Say.whenText(it)) }
            Fact("Automatic updates", if (ov.policy.autoInstall) listOf("On", ov.policy.channel, ov.policy.window).filter { it.isNotEmpty() }.joinToString(" · ") else "Paused")
            ov.lastCheckOk?.let { Fact("Last checked", Say.whenText(it)) }
        }
    }

    val r = active ?: ov.lastRun
    if (r != null) {
        item {
            Header(if (active != null) "Update under way" else "Last update")
            val error = (run?.takeIf { it.id == r.id }?.error ?: r.error).text("code")
            ListItem(
                headlineContent = { Text("${r.version} · ${Say.runState(r.state)}") },
                supportingContent = {
                    Column {
                        if (r.step.isNotEmpty()) Text(Say.step(r.step))
                        run?.takeIf { it.id == r.id }?.detail?.waiting?.takeIf { it.isNotEmpty() }?.let { Text(Say.runWaiting(it)) }
                        error?.let { Text(Say.code(it), color = MaterialTheme.colorScheme.error) }
                    }
                },
                leadingContent = { Dot(toneOf(r.state)) },
                colors = chosenColors(chosen == "run"),
                modifier = Modifier.clickable(enabled = run?.id == r.id) { choose("run") },
            )
            Divider()
        }
        val view = run?.takeIf { it.id == r.id }
        if (view != null && view.targets.isNotEmpty()) {
            items(view.targets, key = { "target:${it.nodeId}" }) { t ->
                ListItem(
                    headlineContent = { Text(t.name.ifEmpty { t.nodeId.take(8) }) },
                    supportingContent = {
                        Column {
                            Text(Say.runState(t.state))
                            t.error.text("code")?.let { Text(Say.code(it), color = MaterialTheme.colorScheme.error, maxLines = 2) }
                        }
                    },
                    leadingContent = { Dot(toneOf(t.state)) },
                    trailingContent = { Text(t.toVersion, color = MaterialTheme.colorScheme.onSurfaceVariant) },
                    colors = chosenColors(chosen == "target:${t.nodeId}"),
                    modifier = Modifier.clickable { choose("target:${t.nodeId}") },
                )
                Divider()
            }
        }
    }

    if (ov.machines.isNotEmpty()) {
        item { Header("Machines") }
        items(ov.machines, key = { "machine:${it.id}" }) { m ->
            ListItem(
                headlineContent = { Text(m.name.ifEmpty { m.id.take(8) }) },
                supportingContent = {
                    Text(listOfNotNull(m.version.ifEmpty { null }, Say.runState(m.state), m.target?.let { "to $it" }).joinToString(" · "))
                },
                leadingContent = { Dot(toneOf(m.state)) },
                colors = chosenColors(chosen == "machine:${m.id}"),
                modifier = Modifier.clickable { choose("machine:${m.id}") },
            )
            Divider()
        }
    }
}

@Composable
private fun RunDetail(v: UpdateRunView) {
    Detail {
        Fact("Version", if (v.fromVersion.isNotEmpty()) "${v.fromVersion} → ${v.version}" else v.version)
        Fact("State", Say.runState(v.state))
        if (v.step.isNotEmpty()) Fact("Step", Say.step(v.step))
        if (v.trigger.isNotEmpty()) Fact("Started by", Say.spaced(v.trigger))
        v.startedAt?.let { Fact("Started", Say.whenText(it)) }
        v.finishedAt?.let { Fact("Ended", Say.whenText(it)) }
        v.detail.waiting?.takeIf { it.isNotEmpty() }?.let { Text(Say.runWaiting(it), color = MaterialTheme.colorScheme.onSurfaceVariant) }
        v.error.text("code")?.let { Text(Say.code(it), color = MaterialTheme.colorScheme.error) }
        v.error.text("detail")?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        v.detail.tv?.let { tv ->
            Text(Say.tv(tv.state, tv.heldOn, v.version), color = MaterialTheme.colorScheme.tertiary)
            tv.checks.forEach { Text(Say.tvCheck(it), style = MaterialTheme.typography.bodySmall) }
        }
        if (v.detail.waitingHome.isNotEmpty()) {
            Text("Waiting for the home", style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary)
            v.detail.waitingHome.forEach { Text(listOfNotNull(it.name, it.version).joinToString(" · ")) }
        }
        if (v.steps.isNotEmpty()) {
            Text("Steps", style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary)
            v.steps.forEach { s ->
                Column {
                    Text("${s.host} · ${Say.step(s.step)}", style = MaterialTheme.typography.bodyLarge)
                    Text(Say.runState(s.state), color = toneOf(s.state), style = MaterialTheme.typography.bodySmall)
                    s.detail.text("detail")?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                }
            }
        }
        if (v.targets.isNotEmpty()) {
            Text("On the machines, one at a time", style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary)
            v.targets.forEach { t -> Fact(t.name.ifEmpty { t.nodeId.take(8) }, Say.runState(t.state)) }
        }
    }
}

@Composable
private fun TargetDetail(t: UpdateTarget) {
    Detail {
        Fact("State", Say.runState(t.state))
        Fact("Version", listOf(t.fromVersion, t.toVersion).filter { it.isNotEmpty() }.joinToString(" → "))
        if (t.platform.isNotEmpty()) Fact("System", t.platform)
        if (t.kind.isNotEmpty()) Fact("Install", t.kind)
        Fact("Attempts", "${t.attempts}")
        t.appliedAt?.let { Fact("Updated", Say.whenText(it)) }
        t.healthyAt?.let { Fact("Healthy", Say.whenText(it)) }
        t.error.text("code")?.let { Text(Say.code(it), color = MaterialTheme.colorScheme.error) }
        t.error.text("detail")?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
    }
}

@Composable
private fun MachineUpdateDetail(m: UpdateMachine) {
    Detail {
        Fact("Version", m.version.ifEmpty { "-" })
        Fact("State", Say.runState(m.state))
        m.target?.let { Fact("Updating to", it) }
        m.platform?.let { Fact("System", it) }
        val last = m.lastUpdate
        Fact(
            "Last update",
            if (last == null) "None recorded" else listOfNotNull(last.to, (last.endedAt ?: last.at)?.let(Say::whenText), Say.spaced(last.source).ifEmpty { null }).joinToString(" · "),
        )
    }
}

// ---------- processes (on Machines) ----------

/** The home's processes as the supervisor reports them, and its recent crashes. */
internal fun LazyListScope.processRows(doc: ProcessesDoc?, chosen: String?, choose: (String) -> Unit) {
    item { Header("Processes" + (doc?.host?.takeIf { it.isNotEmpty() }?.let { " on $it" } ?: "")) }
    when {
        doc == null -> item { Text("Loading…", Modifier.padding(16.dp), color = MaterialTheme.colorScheme.onSurfaceVariant) }
        !doc.available -> item {
            Callout("The home's supervisor is not reachable: process supervision is not available.", detail = doc.reason)
        }
        else -> {
            items(doc.processes, key = { "proc:${it.id}" }) { p ->
                ListItem(
                    headlineContent = { Text(Say.process(p)) },
                    supportingContent = {
                        Column {
                            Text(processLine(p))
                            Say.processDetail(p)?.let { Text(it, maxLines = 2, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                        }
                    },
                    leadingContent = { Dot(toneOf(p.state)) },
                    colors = chosenColors(chosen == "proc:${p.id}"),
                    modifier = Modifier.clickable { choose("proc:${p.id}") },
                )
                Divider()
            }
            val crashes = recentCrashes(doc.events)
            item {
                Header("Recent crashes")
                if (crashes.isEmpty()) {
                    Text("No crashes recorded.", Modifier.padding(horizontal = 16.dp, vertical = 4.dp), color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                crashes.forEach { e ->
                    Text(
                        listOfNotNull(e.at?.let(Say::whenText), Say.event(e)).joinToString(" · "),
                        Modifier.padding(horizontal = 16.dp, vertical = 2.dp),
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
            }
        }
    }
}

/** The last five crashes and hangs, newest first (as the web's card). */
internal fun recentCrashes(events: List<ProcessEvent>): List<ProcessEvent> =
    events.filter { it.kind == "exited" && it.reason in Say.FAILURE_REASONS }.takeLast(5).reversed()

private fun processLine(p: ProcessStatus): String = buildString {
    append(Say.processState(p))
    if (p.wanted == true) append(" · wanted")
    if (p.crashLoop) append(" · crash loop")
    if (p.restarts > 0) append(if (p.restarts == 1) " · 1 restart" else " · ${p.restarts} restarts")
    if (p.pid != null && p.uptimeSec > 0) append(" · up ${Say.duration(p.uptimeSec)}")
}

@Composable
internal fun ProcessDetail(p: ProcessStatus, events: List<ProcessEvent>) {
    Detail {
        Fact("State", Say.processState(p))
        if (p.wanted == true) Fact("Wanted", "yes")
        Fact("Restarts", "${p.restarts}")
        if (p.pid != null) Fact("Up for", Say.duration(p.uptimeSec))
        p.nextRestartAt?.let { Fact("Next restart", Say.whenText(it)) }
        p.lastExit?.let { e ->
            val label = if (e.reason in Say.FAILURE_REASONS) "Last crash" else "Last exit"
            Fact(label, Say.exit(e))
            e.at?.let { Fact("At", Say.whenText(it)) }
            e.error?.takeIf { it.isNotEmpty() }?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        }
        p.lastProbe?.takeIf { !it.ok }?.let { Fact("Health check", "failing (${it.consecutiveFailures} in a row)") }
        if (p.crashLoop) {
            Text(
                "Crash loop: it keeps exiting soon after it starts. The other processes keep running, and this one is retried slowly.",
                color = MaterialTheme.colorScheme.error,
            )
        }
        Say.processDetail(p)?.let { Text(it, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        val mine = events.filter { it.role == p.role && (it.instance ?: "") == (p.instance ?: "") }.takeLast(8).reversed()
        if (mine.isNotEmpty()) {
            Text("Recent events", style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary)
            mine.forEach { e ->
                Text(listOfNotNull(e.at?.let(Say::whenText), Say.event(e), e.message).joinToString(" · "), style = MaterialTheme.typography.bodySmall)
            }
        }
    }
}

// ---------- running jobs and the queue's health (on Queue) ----------

/** The health strip: the CPU, the backlog, the NAS. */
internal fun LazyListScope.healthRow(h: HealthNow?) {
    if (h == null) return
    val parts = buildList {
        h.cpu?.pct?.let { add("CPU ${it.toInt()}%") }
        h.backlog?.let { b ->
            add("Backlog ${b.ready} jobs waiting" + (b.oldestWaitingSec?.let { " (oldest ${minutes(it)})" } ?: ""))
        }
        h.nas?.let { n ->
            add("NAS reads ${n.readsBusy} of ${n.readsLimit} slots busy" + if (n.timeouts10m > 0) " · ${n.timeouts10m} NAS timeouts in 10 min" else "")
        }
    }
    if (parts.isEmpty()) return
    item {
        Header("System health")
        parts.forEach { Text(it, Modifier.padding(horizontal = 16.dp, vertical = 2.dp), style = MaterialTheme.typography.bodyMedium) }
    }
}

/** The jobs running now, across every queue. */
internal fun LazyListScope.runningRows(jobs: List<JobRef>?, labelOf: (String) -> String, chosen: String?, choose: (String) -> Unit) {
    item { Header("Running now") }
    when {
        jobs == null -> {}
        jobs.isEmpty() -> item { Text("Nothing is running.", Modifier.padding(horizontal = 16.dp, vertical = 4.dp), color = MaterialTheme.colorScheme.onSurfaceVariant) }
        else -> items(jobs, key = { "job:${it.id}" }) { j ->
            ListItem(
                headlineContent = { Text(j.subjectLabel.ifEmpty { j.subjectId }, maxLines = 2) },
                supportingContent = {
                    Column {
                        Text(listOfNotNull(labelOf(j.stage), j.progress?.step?.ifEmpty { null }?.let(Say::spaced), j.claimedBy).joinToString(" · "))
                        j.progress?.fraction?.let { LinearProgressIndicator(progress = { it }, Modifier.fillMaxWidth().padding(top = 6.dp)) }
                    }
                },
                colors = chosenColors(chosen == "job:${j.id}"),
                modifier = Modifier.clickable { choose("job:${j.id}") },
            )
            Divider()
        }
    }
}

@Composable
internal fun JobDetail(j: JobRef, stageLabel: String) {
    Detail {
        Fact("Queue", stageLabel)
        Fact("State", Say.spaced(j.state))
        Fact("Item", "${Say.spaced(j.subjectKind)} ${j.subjectId}".trim())
        if (j.maxAttempts > 0) Fact("Attempt", "${j.attempts} of ${j.maxAttempts}")
        j.startedAt?.let { Fact("Started", Say.whenText(it)) }
        j.claimedBy?.let { Fact("Worker", it) }
        j.progress?.let { p ->
            if (p.step.isNotEmpty()) Fact("Step", Say.spaced(p.step) + if (p.steps.size > 1) " (${p.steps.indexOf(p.step) + 1} of ${p.steps.size})" else "")
            p.fraction?.let { LinearProgressIndicator(progress = { it }, Modifier.fillMaxWidth()) }
            p.message?.takeIf { it.isNotEmpty() }?.let { Text(it, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        }
    }
}

/** A stage's failures by cause (`GET /api/queues/{stage}/failures`). */
@Composable
internal fun StageFailures(groups: List<FailureGroup>?) {
    Text("Failures by cause", style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary)
    when {
        groups == null -> Busy()
        groups.isEmpty() -> Text("None", color = MaterialTheme.colorScheme.onSurfaceVariant)
        else -> groups.forEach { g ->
            Column {
                Text("${g.count}  ${g.label}", style = MaterialTheme.typography.bodyLarge)
                g.sampleMessage?.takeIf { it.isNotEmpty() }?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 3) }
                g.lastAt?.let { Text("Last: ${Say.whenText(it)}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
            }
        }
    }
}
