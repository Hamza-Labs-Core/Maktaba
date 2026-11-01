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
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
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
    val say = rememberSay()
    AdminScreen(
        title = stringResource(R.string.place_updates),
        remote = remote,
        account = account,
        refreshing = refreshing,
        onRefresh = onRefresh,
        chosenTitle = when {
            showRun -> stringResource(R.string.update_to, run!!.version)
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
            item { if (!remote.hasProblem) Message(stringResource(R.string.loading)) }
        } else {
            updatesRows(ov, run, chosen, say, choose)
        }
    }
}

private fun LazyListScope.updatesRows(ov: UpdatesOverview, run: UpdateRunView?, chosen: String?, say: Say, choose: (String?) -> Unit) {
    val active = ov.activeRun
    if (ov.checkStale) item { Callout(say.t(R.string.check_stale), error = true) }
    ov.lastCheckError?.takeIf { it.isNotEmpty() }?.let { e -> item { Callout(say.t(R.string.check_failed), error = true, detail = e) } }
    ov.stuck?.takeIf { active == null }?.let { s ->
        item { Callout(say.stuck(s), detail = say.t(R.string.stuck_body)) }
    }
    items(ov.blockedReleases, key = { "blocked:${it.version}" }) { r -> Callout(say.blocked(r)) }
    ov.tvHeld?.let { tv ->
        item {
            Callout(
                say.tv(tv.state, tv.heldOn, tv.version),
                error = (tv.state ?: "held") == "held",
                detail = tv.checks.map(say::tvCheck).ifEmpty { tv.failed.map(say::tvCheckName) }.joinToString("\n").ifEmpty { null },
            )
        }
    }

    item {
        Header(say.t(R.string.updates_this_home))
        Column(Modifier.padding(horizontal = 16.dp, vertical = 4.dp)) {
            Fact(say.t(R.string.running_version), ov.machines.firstOrNull { it.role == "home" }?.version?.ifEmpty { null } ?: ov.currentVersion.ifEmpty { "-" })
            Fact(say.t(R.string.next), say.planNext(ov))
            say.tick(ov)?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
            val plan = ov.plan
            plan.target?.let { t -> Fact(say.t(R.string.target), t.version) }
            if (plan.held) Fact(say.t(R.string.held), say.t(R.string.yes))
            plan.reason?.takeIf { it.isNotEmpty() && it != plan.waiting }?.let { Fact(say.t(R.string.reason), say.waiting(it, plan.target?.version ?: "")) }
            plan.eligibleAt?.let { Fact(say.t(R.string.old_enough_on), say.whenText(it)) }
            Fact(say.t(R.string.automatic_updates), if (ov.policy.autoInstall) listOf(say.t(R.string.auto_on), ov.policy.channel, ov.policy.window).filter { it.isNotEmpty() }.joinToString(" · ") else say.t(R.string.auto_paused))
            ov.lastCheckOk?.let { Fact(say.t(R.string.last_checked), say.whenText(it)) }
        }
    }

    val r = active ?: ov.lastRun
    if (r != null) {
        item {
            Header(say.t(if (active != null) R.string.update_under_way else R.string.last_update))
            val error = (run?.takeIf { it.id == r.id }?.error ?: r.error).text("code")
            ListItem(
                headlineContent = { Text("${r.version} · ${say.runState(r.state)}") },
                supportingContent = {
                    Column {
                        if (r.step.isNotEmpty()) Text(say.step(r.step))
                        run?.takeIf { it.id == r.id }?.detail?.waiting?.takeIf { it.isNotEmpty() }?.let { Text(say.runWaiting(it)) }
                        error?.let { Text(say.code(it), color = MaterialTheme.colorScheme.error) }
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
                            Text(say.runState(t.state))
                            t.error.text("code")?.let { Text(say.code(it), color = MaterialTheme.colorScheme.error, maxLines = 2) }
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
        item { Header(say.t(R.string.machines)) }
        items(ov.machines, key = { "machine:${it.id}" }) { m ->
            ListItem(
                headlineContent = { Text(m.name.ifEmpty { m.id.take(8) }) },
                supportingContent = {
                    Text(listOfNotNull(m.version.ifEmpty { null }, say.runState(m.state), m.target?.let { say.t(R.string.to_version, it) }).joinToString(" · "))
                },
                leadingContent = { Dot(toneOf(m.state)) },
                colors = chosenColors(chosen == "machine:${m.id}"),
                modifier = Modifier.clickable { choose("machine:${m.id}") },
            )
            Divider()
        }
    }
}

/**
 * "0.3.43 → 0.3.44", kept left to right in an Arabic line (Unicode isolates), so the arrow still
 * points from the old version to the new one.
 */
internal fun versions(from: String, to: String): String =
    listOf(from, to).filter { it.isNotEmpty() }.joinToString(" → ").let { "\u2066$it\u2069" }

@Composable
private fun RunDetail(v: UpdateRunView) {
    val say = rememberSay()
    Detail {
        Fact(stringResource(R.string.version), versions(v.fromVersion, v.version))
        Fact(stringResource(R.string.state), say.runState(v.state))
        if (v.step.isNotEmpty()) Fact(stringResource(R.string.step), say.step(v.step))
        if (v.trigger.isNotEmpty()) Fact(stringResource(R.string.started_by), say.trigger(v.trigger))
        v.startedAt?.let { Fact(stringResource(R.string.started), say.whenText(it)) }
        v.finishedAt?.let { Fact(stringResource(R.string.ended), say.whenText(it)) }
        v.detail.waiting?.takeIf { it.isNotEmpty() }?.let { Text(say.runWaiting(it), color = MaterialTheme.colorScheme.onSurfaceVariant) }
        v.error.text("code")?.let { Text(say.code(it), color = MaterialTheme.colorScheme.error) }
        v.error.text("detail")?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        v.detail.tv?.let { tv ->
            Text(say.tv(tv.state, tv.heldOn, v.version), color = MaterialTheme.colorScheme.tertiary)
            tv.checks.forEach { Text(say.tvCheck(it), style = MaterialTheme.typography.bodySmall) }
        }
        if (v.detail.waitingHome.isNotEmpty()) {
            Text(stringResource(R.string.waiting_for_home), style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary)
            v.detail.waitingHome.forEach { Text(listOfNotNull(it.name, it.version).joinToString(" · ")) }
        }
        if (v.steps.isNotEmpty()) {
            Text(stringResource(R.string.steps), style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary)
            v.steps.forEach { s ->
                Column {
                    Text("${s.host} · ${say.step(s.step)}", style = MaterialTheme.typography.bodyLarge)
                    Text(say.runState(s.state), color = toneOf(s.state), style = MaterialTheme.typography.bodySmall)
                    s.detail.text("detail")?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                }
            }
        }
        if (v.targets.isNotEmpty()) {
            Text(stringResource(R.string.on_machines), style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary)
            v.targets.forEach { t -> Fact(t.name.ifEmpty { t.nodeId.take(8) }, say.runState(t.state)) }
        }
    }
}

@Composable
private fun TargetDetail(t: UpdateTarget) {
    val say = rememberSay()
    Detail {
        Fact(stringResource(R.string.state), say.runState(t.state))
        Fact(stringResource(R.string.version), versions(t.fromVersion, t.toVersion))
        if (t.platform.isNotEmpty()) Fact(stringResource(R.string.system), t.platform)
        if (t.kind.isNotEmpty()) Fact(stringResource(R.string.install), t.kind)
        Fact(stringResource(R.string.attempts), say.number(t.attempts))
        t.appliedAt?.let { Fact(stringResource(R.string.updated), say.whenText(it)) }
        t.healthyAt?.let { Fact(stringResource(R.string.healthy), say.whenText(it)) }
        t.error.text("code")?.let { Text(say.code(it), color = MaterialTheme.colorScheme.error) }
        t.error.text("detail")?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
    }
}

@Composable
private fun MachineUpdateDetail(m: UpdateMachine) {
    val say = rememberSay()
    Detail {
        Fact(stringResource(R.string.version), m.version.ifEmpty { "-" })
        Fact(stringResource(R.string.state), say.runState(m.state))
        m.target?.let { Fact(stringResource(R.string.updating_to), it) }
        m.platform?.let { Fact(stringResource(R.string.system), it) }
        val last = m.lastUpdate
        Fact(
            stringResource(R.string.last_update),
            if (last == null) stringResource(R.string.none_recorded) else listOfNotNull(last.to, (last.endedAt ?: last.at)?.let(say::whenText), Say.spaced(last.source).ifEmpty { null }).joinToString(" · "),
        )
    }
}

// ---------- processes (on Machines) ----------

/** The home's processes as the supervisor reports them, and its recent crashes. */
internal fun LazyListScope.processRows(doc: ProcessesDoc?, chosen: String?, say: Say, choose: (String) -> Unit) {
    item { Header(doc?.host?.takeIf { it.isNotEmpty() }?.let { say.t(R.string.processes_on, it) } ?: say.t(R.string.processes)) }
    when {
        doc == null -> item { Text(say.t(R.string.loading), Modifier.padding(16.dp), color = MaterialTheme.colorScheme.onSurfaceVariant) }
        !doc.available -> item {
            Callout(say.t(R.string.supervisor_unreachable), detail = doc.reason)
        }
        else -> {
            items(doc.processes, key = { "proc:${it.id}" }) { p ->
                ListItem(
                    headlineContent = { Text(say.process(p)) },
                    supportingContent = {
                        Column {
                            Text(processLine(p, say))
                            say.processDetail(p)?.let { Text(it, maxLines = 2, color = MaterialTheme.colorScheme.onSurfaceVariant) }
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
                Header(say.t(R.string.recent_crashes))
                if (crashes.isEmpty()) {
                    Text(say.t(R.string.no_crashes), Modifier.padding(horizontal = 16.dp, vertical = 4.dp), color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                crashes.forEach { e ->
                    Text(
                        listOfNotNull(e.at?.let(say::whenText), say.event(e)).joinToString(" · "),
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

private fun processLine(p: ProcessStatus, say: Say): String = listOfNotNull(
    say.processState(p),
    say.t(R.string.flag_wanted).takeIf { p.wanted == true },
    say.t(R.string.flag_crash_loop).takeIf { p.crashLoop },
    say.count(R.plurals.restarts_count, p.restarts).takeIf { p.restarts > 0 },
    say.t(R.string.up_for_short, say.duration(p.uptimeSec)).takeIf { p.pid != null && p.uptimeSec > 0 },
).joinToString(" · ")

@Composable
internal fun ProcessDetail(p: ProcessStatus, events: List<ProcessEvent>) {
    val say = rememberSay()
    Detail {
        Fact(stringResource(R.string.state), say.processState(p))
        if (p.wanted == true) Fact(stringResource(R.string.wanted), stringResource(R.string.yes))
        Fact(stringResource(R.string.restarts), say.number(p.restarts))
        if (p.pid != null) Fact(stringResource(R.string.up_for), say.duration(p.uptimeSec))
        p.nextRestartAt?.let { Fact(stringResource(R.string.next_restart), say.whenText(it)) }
        p.lastExit?.let { e ->
            val label = stringResource(if (e.reason in Say.FAILURE_REASONS) R.string.last_crash else R.string.last_exit)
            Fact(label, say.exit(e))
            e.at?.let { Fact(stringResource(R.string.at), say.whenText(it)) }
            e.error?.takeIf { it.isNotEmpty() }?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        }
        p.lastProbe?.takeIf { !it.ok }?.let { Fact(stringResource(R.string.health_check), stringResource(R.string.probe_failing, it.consecutiveFailures)) }
        if (p.crashLoop) {
            Text(
                stringResource(R.string.crash_loop_body),
                color = MaterialTheme.colorScheme.error,
            )
        }
        say.processDetail(p)?.let { Text(it, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        val mine = events.filter { it.role == p.role && (it.instance ?: "") == (p.instance ?: "") }.takeLast(8).reversed()
        if (mine.isNotEmpty()) {
            Text(stringResource(R.string.recent_events), style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary)
            mine.forEach { e ->
                Text(listOfNotNull(e.at?.let(say::whenText), say.event(e), e.message).joinToString(" · "), style = MaterialTheme.typography.bodySmall)
            }
        }
    }
}

// ---------- running jobs and the queue's health (on Queue) ----------

/** The health strip: the CPU, the backlog, the NAS. */
internal fun LazyListScope.healthRow(h: HealthNow?, say: Say) {
    if (h == null) return
    val parts = buildList {
        h.cpu?.pct?.let { add(say.t(R.string.health_cpu, it.toInt())) }
        h.backlog?.let { b ->
            add(say.count(R.plurals.health_backlog, b.ready) + (b.oldestWaitingSec?.let { say.t(R.string.health_oldest, say.minutes(it)) } ?: ""))
        }
        h.nas?.let { n ->
            add(say.t(R.string.health_nas, n.readsBusy, n.readsLimit) + if (n.timeouts10m > 0) " · " + say.t(R.string.health_nas_timeouts, n.timeouts10m) else "")
        }
    }
    if (parts.isEmpty()) return
    item {
        Header(say.t(R.string.system_health))
        parts.forEach { Text(it, Modifier.padding(horizontal = 16.dp, vertical = 2.dp), style = MaterialTheme.typography.bodyMedium) }
    }
}

/** The jobs running now, across every queue. */
internal fun LazyListScope.runningRows(jobs: List<JobRef>?, labelOf: (String) -> String, chosen: String?, say: Say, choose: (String) -> Unit) {
    item { Header(say.t(R.string.running_now)) }
    when {
        jobs == null -> {}
        jobs.isEmpty() -> item { Text(say.t(R.string.nothing_running), Modifier.padding(horizontal = 16.dp, vertical = 4.dp), color = MaterialTheme.colorScheme.onSurfaceVariant) }
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
    val say = rememberSay()
    Detail {
        Fact(stringResource(R.string.queue), stageLabel)
        Fact(stringResource(R.string.state), say.jobState(j.state))
        Fact(stringResource(R.string.item), "${Say.spaced(j.subjectKind)} ${j.subjectId}".trim())
        if (j.maxAttempts > 0) Fact(stringResource(R.string.attempt), stringResource(R.string.of_total, j.attempts, j.maxAttempts))
        j.startedAt?.let { Fact(stringResource(R.string.started), say.whenText(it)) }
        j.claimedBy?.let { Fact(stringResource(R.string.worker), it) }
        j.progress?.let { p ->
            if (p.step.isNotEmpty()) Fact(stringResource(R.string.step), Say.spaced(p.step) + if (p.steps.size > 1) stringResource(R.string.step_of, p.steps.indexOf(p.step) + 1, p.steps.size) else "")
            p.fraction?.let { LinearProgressIndicator(progress = { it }, Modifier.fillMaxWidth()) }
            p.message?.takeIf { it.isNotEmpty() }?.let { Text(it, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        }
    }
}

/** A stage's failures by cause (`GET /api/queues/{stage}/failures`). */
@Composable
internal fun StageFailures(groups: List<FailureGroup>?) {
    val say = rememberSay()
    Text(stringResource(R.string.failures_by_cause), style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary)
    when {
        groups == null -> Busy()
        groups.isEmpty() -> Text(stringResource(R.string.none), color = MaterialTheme.colorScheme.onSurfaceVariant)
        else -> groups.forEach { g ->
            Column {
                Text(stringResource(R.string.count_label, g.count, g.label), style = MaterialTheme.typography.bodyLarge)
                g.sampleMessage?.takeIf { it.isNotEmpty() }?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 3) }
                g.lastAt?.let { Text(stringResource(R.string.last_at, say.whenText(it)), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
            }
        }
    }
}
