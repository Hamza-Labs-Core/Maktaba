package com.hamzalabs.maktaba.core.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

// The state of the home's running work, as the web admin shows it (read only): its updates
// (`GET /api/cluster/updates`, `/runs/{id}`), its processes (`GET /api/system/processes`) and
// the jobs running now (`GET /api/queues/running`). Every route is the home admins'; the home
// refuses anyone else. States are strings: a value a newer home sends is shown as it is.

// ---------- updates ----------

/** `GET /api/cluster/updates`: the running version, the plan, the catalog, the machines. */
@Serializable
data class UpdatesOverview(
    @SerialName("current_version") val currentVersion: String = "",
    /** the newest release the cluster installed (never offered again below it) */
    val watermark: String = "",
    val policy: UpdatesPolicy = UpdatesPolicy(),
    val plan: UpdatesPlan = UpdatesPlan(),
    /** the run under way, if any */
    val run: UpdateRun? = null,
    @SerialName("last_run") val lastRun: UpdateRun? = null,
    val catalog: List<CatalogRelease> = emptyList(),
    @SerialName("last_check_ok") val lastCheckOk: String? = null,
    @SerialName("last_check_error") val lastCheckError: String? = null,
    /** no successful check for 14 days */
    @SerialName("check_stale") val checkStale: Boolean = false,
    @SerialName("time_zone") val timeZone: String? = null,
    val machines: List<UpdateMachine> = emptyList(),
    @SerialName("last_tick") val lastTick: UpdatesTick? = null,
    val stuck: UpdatesStuck? = null,
    @SerialName("tv_held") val tvHeld: TvHeldNow? = null,
) {
    /** The run under way: one whose state is not over. */
    val activeRun: UpdateRun? get() = run?.takeIf { it.state !in UpdateRun.ENDED }

    /**
     * The releases to show as blocked, as the web's Updates page does: blocked and not
     * withdrawn, newer than the watermark (else the running version), and passed by no newer
     * release that is not blocked (one a newer release replaced is history, not "blocked").
     */
    val blockedReleases: List<CatalogRelease>
        get() {
            val current = watermark.ifEmpty { currentVersion }
            return catalog.filter { r ->
                !r.blockedReason.isNullOrEmpty() && r.withdrawnAt == null &&
                    (!Versions.valid(current) || !Versions.valid(r.version) || Versions.compare(r.version, current) > 0) &&
                    catalog.none { o ->
                        o.blockedReason.isNullOrEmpty() && o.withdrawnAt == null &&
                            Versions.valid(o.version) && Versions.valid(r.version) && Versions.compare(o.version, r.version) > 0
                    }
            }
        }
}

@Serializable
data class UpdatesPolicy(
    @SerialName("auto_install") val autoInstall: Boolean = false,
    val channel: String = "",
    val window: String = "",
)

@Serializable
data class CatalogRelease(
    val version: String = "",
    val channel: String = "",
    @SerialName("published_at") val publishedAt: String? = null,
    @SerialName("verify_state") val verifyState: String = "",
    @SerialName("withdrawn_at") val withdrawnAt: String? = null,
    @SerialName("held_at") val heldAt: String? = null,
    /** attempts, rolled_back, verify, rehearsal, older_build, failed_tv, ... */
    @SerialName("blocked_reason") val blockedReason: String? = null,
    val attempts: Int = 0,
)

/** The next planned update as the home's policy sees it. */
@Serializable
data class UpdatesPlan(
    val target: CatalogRelease? = null,
    val newest: CatalogRelease? = null,
    val held: Boolean = false,
    @SerialName("stepping_stone") val steppingStone: Boolean = false,
    val reason: String? = null,
    @SerialName("auto_install") val autoInstall: Boolean = false,
    val due: Boolean = false,
    @SerialName("next_run_at") val nextRunAt: String? = null,
    @SerialName("eligible_at") val eligibleAt: String? = null,
    /** why it is not due now: auto_install_off, deferred, release_age, outside_window, ... */
    val waiting: String? = null,
)

/** The scheduler's last decision: why a due release does not start now. */
@Serializable
data class UpdatesTick(
    val action: String = "",
    val reason: String? = null,
    val version: String? = null,
    val at: String? = null,
)

/** A release a gate held for several windows in a row. */
@Serializable
data class UpdatesStuck(
    val version: String = "",
    val code: String = "",
    val windows: Int = 0,
    val since: String? = null,
)

/** One machine of the Updates page: the home, then every joined machine. */
@Serializable
data class UpdateMachine(
    val id: String = "",
    val name: String = "",
    /** "home" or "node" */
    val role: String = "",
    val version: String = "",
    /** up_to_date, behind, updating, ahead_of_home, offline, worker_only, or a run's target state */
    val state: String = "",
    val platform: String? = null,
    val kind: String? = null,
    /** the release the active run brings it to */
    val target: String? = null,
    @SerialName("last_update") val lastUpdate: UpdateAttempt? = null,
)

/** One try at bringing a machine to a release. */
@Serializable
data class UpdateAttempt(
    val at: String? = null,
    @SerialName("ended_at") val endedAt: String? = null,
    /** app, server or run */
    val source: String = "",
    val from: String? = null,
    val to: String = "",
    val outcome: String = "",
)

/** An update run (`run`, `last_run`). */
@Serializable
data class UpdateRun(
    val id: String = "",
    val version: String = "",
    @SerialName("from_version") val fromVersion: String = "",
    val trigger: String = "",
    val state: String = "",
    val step: String = "",
    @SerialName("started_at") val startedAt: String? = null,
    @SerialName("finished_at") val finishedAt: String? = null,
    /** `{code, step, detail, reason, ...}` as the home keeps it */
    val error: JsonElement? = null,
) {
    companion object {
        /** The states of a run that are over (the web's RUN_ENDED). */
        val ENDED = setOf(
            "done", "done_with_failures", "done_needs_person", "held", "failed",
            "rolled_back", "failed_rollback", "cancelled", "superseded",
        )
    }
}

/** `GET /api/cluster/updates/runs/{id}`: a run with its steps and its machines. */
@Serializable
data class UpdateRunView(
    val id: String = "",
    val version: String = "",
    @SerialName("from_version") val fromVersion: String = "",
    val trigger: String = "",
    val state: String = "",
    val step: String = "",
    @SerialName("started_at") val startedAt: String? = null,
    @SerialName("finished_at") val finishedAt: String? = null,
    val error: JsonElement? = null,
    @SerialName("install_kind") val installKind: String = "",
    val detail: UpdateRunDetail = UpdateRunDetail(),
    val steps: List<UpdateRunStep> = emptyList(),
    val targets: List<UpdateTarget> = emptyList(),
)

@Serializable
data class UpdateRunDetail(
    /** the gate the run waits at: outside_window, tv_busy, tv_swapping, ... */
    val waiting: String? = null,
    @SerialName("no_home") val noHome: Boolean = false,
    val tv: RunTv? = null,
    @SerialName("waiting_home") val waitingHome: List<WaitingMachine> = emptyList(),
)

@Serializable
data class WaitingMachine(
    @SerialName("node_id") val nodeId: String = "",
    val name: String = "",
    val version: String? = null,
)

@Serializable
data class UpdateRunStep(
    val seq: Int = 0,
    val host: String = "",
    val step: String = "",
    val state: String = "",
    @SerialName("started_at") val startedAt: String? = null,
    @SerialName("finished_at") val finishedAt: String? = null,
    /** `{detail: "..."}` as the home keeps it */
    val detail: JsonElement? = null,
)

/** One machine of a run. */
@Serializable
data class UpdateTarget(
    @SerialName("node_id") val nodeId: String = "",
    val name: String = "",
    val ord: Int = 0,
    val platform: String = "",
    val kind: String = "",
    /** pending, ordered, app_staged, applying, watching, committed, healthy, held, needs_person, ... */
    val state: String = "",
    @SerialName("from_version") val fromVersion: String = "",
    @SerialName("to_version") val toVersion: String = "",
    val attempts: Int = 0,
    /** `{code, detail, held_by, ...}` */
    val error: JsonElement? = null,
    @SerialName("applied_at") val appliedAt: String? = null,
    @SerialName("healthy_at") val healthyAt: String? = null,
)

/** A TV held at an update: its change's check failed, so the TV stayed on [heldOn]. */
@Serializable
data class RunTv(
    val outcome: String = "",
    @SerialName("change_id") val changeId: String? = null,
    val number: Int? = null,
    @SerialName("held_on") val heldOn: String? = null,
    val failed: List<String> = emptyList(),
    /** held, retrying, live, dropped, replaced, failed, gone, pending */
    val state: String? = null,
    val checks: List<TvCheck> = emptyList(),
)

/** `tv_held` of `GET /api/cluster/updates`: the run whose TV is held now. */
@Serializable
data class TvHeldNow(
    @SerialName("run_id") val runId: String = "",
    val version: String = "",
    val outcome: String = "",
    val number: Int? = null,
    @SerialName("held_on") val heldOn: String? = null,
    val failed: List<String> = emptyList(),
    val state: String? = null,
    val checks: List<TvCheck> = emptyList(),
)

/**
 * A failed check of a TV change's report. [value] is the home's English; [code] names it and
 * [params] fill it (the web's `tvchanges.value.<code>`), so the app says it in its own language.
 */
@Serializable
data class TvCheck(
    val check: String = "",
    @SerialName("channel_id") val channelId: String? = null,
    val result: String = "",
    val value: String? = null,
    val code: String? = null,
    val params: JsonObject? = null,
)

/** A text field of a loose JSON object the home keeps (an error's `code`, a step's `detail`). */
fun JsonElement?.text(field: String): String? {
    val v = (this as? JsonObject)?.get(field) as? JsonPrimitive ?: return null
    return v.content.takeIf { v.isString && it.isNotEmpty() }
}

// ---------- processes ----------

/** `GET /api/system/processes`: the supervisor's processes; not [available] without one. */
@Serializable
data class ProcessesDoc(
    val available: Boolean = false,
    /** why it is not available (English, for the log) */
    val reason: String? = null,
    val host: String? = null,
    val processes: List<ProcessStatus> = emptyList(),
    val events: List<ProcessEvent> = emptyList(),
)

@Serializable
data class ProcessStatus(
    /** api, streaming, pipeline, tv-slot, postgres, ollama, relay, copy, ... */
    val role: String = "",
    /** a TV slot's name: blue, green, amber */
    val instance: String? = null,
    /** an on-demand process asked for (false: one standing by; null: the home does not say) */
    val wanted: Boolean? = null,
    /** starting, running, unhealthy, backoff, failed, exited, stopped */
    val state: String = "",
    val pid: Int? = null,
    val restarts: Int = 0,
    @SerialName("crash_loop") val crashLoop: Boolean = false,
    @SerialName("uptime_sec") val uptimeSec: Double = 0.0,
    @SerialName("last_exit") val lastExit: ProcessExit? = null,
    @SerialName("last_restart_at") val lastRestartAt: String? = null,
    @SerialName("next_restart_at") val nextRestartAt: String? = null,
    @SerialName("last_probe") val lastProbe: ProcessProbe? = null,
    /** what it waits on before it starts, or why that failed (English) */
    val detail: String? = null,
    @SerialName("detail_code") val detailCode: String? = null,
    /** [detailCode]'s values (sizes in bytes, paths whole) */
    @SerialName("detail_params") val detailParams: JsonObject? = null,
    val adopted: Boolean = false,
) {
    val id: String get() = if (instance.isNullOrEmpty()) role else "$role/$instance"
}

@Serializable
data class ProcessExit(
    val at: String? = null,
    val code: Int = 0,
    val signal: String? = null,
    /** crash, clean, hang, start_failed, shutdown, stopped, restarted */
    val reason: String = "",
    val error: String? = null,
    @SerialName("uptime_sec") val uptimeSec: Double = 0.0,
)

@Serializable
data class ProcessProbe(
    val at: String? = null,
    val ok: Boolean = true,
    val error: String? = null,
    @SerialName("consecutive_failures") val consecutiveFailures: Int = 0,
)

@Serializable
data class ProcessEvent(
    val seq: Long = 0,
    val at: String? = null,
    val role: String = "",
    val instance: String? = null,
    /** started, exited, restarting, failed, hung */
    val kind: String = "",
    val code: Int? = null,
    val signal: String? = null,
    val reason: String? = null,
    val message: String? = null,
)

// ---------- running jobs and the queue's health ----------

/** `GET /api/queues/running`: the jobs running now across every queue, oldest first. */
@Serializable
data class RunningJobs(val host: String = "", val jobs: List<JobRef> = emptyList())

@Serializable
data class JobRef(
    val id: Long = 0,
    val stage: String = "",
    /** claimed, running, resuming, ... */
    val state: String = "",
    @SerialName("subject_kind") val subjectKind: String = "",
    @SerialName("subject_id") val subjectId: String = "",
    @SerialName("subject_label") val subjectLabel: String = "",
    @SerialName("library_id") val libraryId: String? = null,
    val attempts: Int = 0,
    @SerialName("max_attempts") val maxAttempts: Int = 0,
    @SerialName("started_at") val startedAt: String? = null,
    val progress: JobProgress? = null,
    /** the worker running it */
    @SerialName("claimed_by") val claimedBy: String? = null,
)

@Serializable
data class JobProgress(
    val step: String = "",
    val steps: List<String> = emptyList(),
    @SerialName("done_units") val doneUnits: Double = 0.0,
    @SerialName("total_units") val totalUnits: Double = 0.0,
    val message: String? = null,
) {
    /** 0..1, or null when the job does not count its work. */
    val fraction: Float? get() = if (totalUnits > 0) (doneUnits / totalUnits).coerceIn(0.0, 1.0).toFloat() else null
}

/** A stage's health (`queues[].health`): ok, waiting (parked on something) or failing. */
@Serializable
data class StageHealth(
    val state: String = "",
    @SerialName("window_hours") val windowHours: Int = 0,
    @SerialName("failed_window") val failedWindow: Int = 0,
    @SerialName("finished_window") val finishedWindow: Int = 0,
    val waiting: List<HealthWaiting> = emptyList(),
    @SerialName("needs_you") val needsYou: Int = 0,
    val ignored: Int = 0,
    val failing: HealthFailing? = null,
)

@Serializable
data class HealthWaiting(
    /** dependency:model:tagcheck, dependency:share:<root>, resource:disk, ... */
    val on: String = "",
    val count: Int = 0,
    val healthy: Boolean? = null,
    val message: String? = null,
    val since: String? = null,
)

@Serializable
data class HealthFailing(
    val signature: String = "",
    @SerialName("class") val kind: String = "",
    val count: Int = 0,
    val example: String = "",
    val subject: String? = null,
)

/** The health strip of `GET /api/queues`. */
@Serializable
data class HealthNow(
    val nas: NasHealth? = null,
    val cpu: CpuHealth? = null,
    val backlog: BacklogHealth? = null,
)

@Serializable
data class NasHealth(
    @SerialName("reads_busy") val readsBusy: Int = 0,
    @SerialName("reads_limit") val readsLimit: Int = 0,
    @SerialName("open_ms_p50") val openMsP50: Double? = null,
    @SerialName("open_ms_p95") val openMsP95: Double? = null,
    @SerialName("timeouts_10m") val timeouts10m: Int = 0,
)

@Serializable
data class CpuHealth(val pct: Double? = null)

@Serializable
data class BacklogHealth(
    val ready: Int = 0,
    @SerialName("oldest_waiting_sec") val oldestWaitingSec: Double? = null,
)

// ---------- versions ----------

/**
 * The server's order of release versions (releaseverify.CompareVersions, the web's
 * compareVersions): a prerelease ranks below its release, and a `git describe` build after the
 * tag it was built from (by its commits, then dirty after clean).
 */
object Versions {
    private val VERSION = Regex("""^v?(\d+)\.(\d+)\.(\d+)(?:-[0-9A-Za-z.-]+)?$""")
    private val DESCRIBE = Regex("""^(\d+\.\d+\.\d+(?:-[0-9a-z.]+)?)-(\d+)-g[0-9a-f]{4,40}$""")

    fun valid(v: String): Boolean = VERSION.matches(v.trim())

    /** -1, 0 or 1; 0 when either is not a version. */
    fun compare(a: String, b: String): Int {
        if (!valid(a) || !valid(b)) return 0
        val da = describe(a)
        val db = describe(b)
        val c = releases(da.tag, db.tag)
        if (c != 0) return c
        if (da.commits != db.commits) return da.commits.compareTo(db.commits).sign()
        return if (da.dirty == db.dirty) 0 else if (da.dirty) 1 else -1
    }

    private class Describe(val tag: String, val commits: Int, val dirty: Boolean)

    private fun describe(v: String): Describe {
        var s = v.trim().lowercase().removePrefix("v")
        s = s.substringBefore('+')
        var dirty = false
        if (s.endsWith("-dirty")) {
            dirty = true
            s = s.removeSuffix("-dirty")
        }
        val m = DESCRIBE.matchEntire(s) ?: return Describe(s, 0, dirty)
        return Describe(m.groupValues[1], m.groupValues[2].toInt(), dirty)
    }

    private fun releases(a: String, b: String): Int {
        val ap = a.split("-")
        val bp = b.split("-")
        val an = ap[0].split(".").map { it.toLongOrNull() ?: 0 }
        val bn = bp[0].split(".").map { it.toLongOrNull() ?: 0 }
        for (i in 0 until maxOf(an.size, bn.size)) {
            val d = (an.getOrElse(i) { 0 }).compareTo(bn.getOrElse(i) { 0 })
            if (d != 0) return d.sign()
        }
        val apre = ap.drop(1).joinToString("-")
        val bpre = bp.drop(1).joinToString("-")
        if (apre.isEmpty() || bpre.isEmpty()) return if (apre == bpre) 0 else if (apre.isEmpty()) 1 else -1
        val xs = apre.split(".")
        val ys = bpre.split(".")
        for (i in 0 until minOf(xs.size, ys.size)) {
            val x = xs[i]
            val y = ys[i]
            val xn = x.isNotEmpty() && x.all(Char::isDigit)
            val yn = y.isNotEmpty() && y.all(Char::isDigit)
            if (xn && yn && x.toBigInteger() != y.toBigInteger()) return x.toBigInteger().compareTo(y.toBigInteger()).sign()
            if (xn != yn) return if (xn) -1 else 1
            if (!xn && x != y) return if (x < y) -1 else 1
        }
        return xs.size.compareTo(ys.size).sign()
    }

    private fun Int.sign(): Int = Integer.signum(this)
}
