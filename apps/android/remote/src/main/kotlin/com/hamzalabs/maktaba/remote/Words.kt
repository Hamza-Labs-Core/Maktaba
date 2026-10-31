package com.hamzalabs.maktaba.remote

import com.hamzalabs.maktaba.core.api.CatalogRelease
import com.hamzalabs.maktaba.core.api.HealthWaiting
import com.hamzalabs.maktaba.core.api.ProcessEvent
import com.hamzalabs.maktaba.core.api.ProcessExit
import com.hamzalabs.maktaba.core.api.ProcessStatus
import com.hamzalabs.maktaba.core.api.StageHealth
import com.hamzalabs.maktaba.core.api.TvCheck
import com.hamzalabs.maktaba.core.api.UpdatesOverview
import com.hamzalabs.maktaba.core.api.UpdatesStuck
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/**
 * What the status screens say, in the web admin's words (WebWords): a value the home sends that
 * has no words here is shown as sent, its underscores as spaces.
 */
internal object Say {
    fun spaced(v: String): String = v.replace('_', ' ')

    private fun word(words: Map<String, String>, v: String): String = words[v] ?: spaced(v)

    private fun fill(template: String, vararg vars: Pair<String, Any>): String =
        vars.fold(template) { s, (k, v) -> s.replace("{$k}", v.toString()) }

    // ---------- updates ----------

    fun runState(s: String): String = word(WebWords.updateState, s)

    fun step(s: String): String = word(WebWords.updateStep, s)

    /** An error code's words, as the web's reason(): a step's failure the step's, else the code. */
    fun code(c: String): String {
        WebWords.updateCode[c]?.let { return it }
        if (c.startsWith("verify_")) return WebWords.updateCode.getValue("verify_failed")
        Regex("^([a-z]+)_failed$").matchEntire(c)?.let { m ->
            WebWords.updateStep[m.groupValues[1]]?.let { return "$it: ${runState("failed")}" }
        }
        return spaced(c)
    }

    /** What the home does next about its update, as the web's "Next" says it. */
    fun planNext(ov: UpdatesOverview, zone: ZoneId = ZoneId.systemDefault(), locale: Locale = Locale.getDefault()): String {
        ov.activeRun?.let { return "Updating to ${it.version} now." }
        val plan = ov.plan
        val version = plan.target?.version ?: return "Nothing to install: up to date."
        if (plan.held) return fill("Version {version} waits for your approval (Live TV -> Changes).", "version" to version)
        plan.nextRunAt?.let { at -> return fill("Version {version} installs on {at}.", "version" to version, "at" to whenText(at, zone, locale)) }
        return waiting(plan.waiting ?: "auto_install_off", version)
    }

    /** Why the planned [version] does not install now, as a sentence. */
    fun waiting(w: String, version: String): String =
        WebWords.planWaiting[w]?.let { fill(it, "version" to version) }
            ?: WebWords.updateWaiting[w]
            ?: "Version $version: ${spaced(w)}."

    /** A run's gate (detail.waiting): tv_busy, outside_window, tv_swapping, ... */
    fun runWaiting(w: String): String = WebWords.updateWaiting[w] ?: spaced(w)

    /** Why the scheduler did not start a due release (last_tick), or null when it says nothing. */
    fun tick(ov: UpdatesOverview): String? {
        val t = ov.lastTick ?: return null
        val reason = t.reason?.takeIf { it.isNotEmpty() } ?: return null
        if (ov.activeRun != null || ov.plan.target == null) return null
        val why = when (t.action) {
            "blocked" -> word(WebWords.blocker, reason)
            "none" -> runWaiting(reason)
            else -> return null
        }
        return "Not started now: $why"
    }

    fun blocked(r: CatalogRelease): String =
        "${r.version} is blocked: ${word(WebWords.blockedReason, r.blockedReason.orEmpty())}"

    fun stuck(s: UpdatesStuck): String = "${s.version} has not installed in ${s.windows} windows: ${code(s.code)}"

    /** A held TV's line: held, or what became of it since. */
    fun tv(state: String?, heldOn: String?, release: String): String {
        val on = heldOn?.takeIf { it.isNotEmpty() } ?: "the previous version"
        return when (val s = state ?: "held") {
            "held" -> "Installed. TV held on $on: its check failed"
            "retrying" -> "Installed. The TV change is checked again; the TV runs $on until it passes"
            "live" -> "Installed. The TV change passed on a retry: the TV runs $release"
            "dropped" -> "Installed. The TV change was dropped: the TV stayed on $on"
            "replaced" -> "Installed. A later release's TV change replaced this one"
            "failed" -> "Installed. The TV change failed again after its retry: see TV changes"
            "gone" -> "Installed. The TV was held on $on; its change is no longer there"
            "pending" -> "Installed. The TV change goes on by itself (it waits for an approval or a quiet window); the TV runs $on until it is on the air"
            else -> "TV: ${spaced(s)}"
        }
    }

    fun tvCheck(c: TvCheck): String = buildString {
        append(spaced(c.check))
        c.channelId?.takeIf { it.isNotEmpty() }?.let { append(" ($it)") }
        append(": ").append(spaced(c.result))
        c.value?.takeIf { it.isNotEmpty() }?.let { append(" · ").append(it) }
    }

    // ---------- processes ----------

    /** A process's name: its role's, and a TV slot's colour (blue, green, amber). */
    fun process(role: String, instance: String?): String {
        val name = WebWords.processRole[role] ?: role
        return if (instance.isNullOrEmpty()) name else "$name · $instance"
    }

    fun process(p: ProcessStatus): String = process(p.role, p.instance)

    fun processState(s: String): String = WebWords.processState[s] ?: spaced(s)

    /** A process's state as the web says it: a stopped one not wanted now is standing by. */
    fun processState(p: ProcessStatus): String = processState(if (p.wanted == false && p.state == "stopped") "standby" else p.state)

    /** Why a process is not started, in the web's words (its detail_code with its values), else the home's English. */
    fun processDetail(p: ProcessStatus): String? {
        p.detailCode?.let { code ->
            WebWords.processDetail[code]?.let { template ->
                val vars = p.detailParams.orEmpty().mapNotNull { (k, v) ->
                    (v as? kotlinx.serialization.json.JsonPrimitive)?.takeUnless { it is kotlinx.serialization.json.JsonNull }?.let { k to it.content }
                }
                return fill(template, *vars.toTypedArray())
            }
        }
        return p.detail?.takeIf { it.isNotEmpty() }
    }

    /** Exits worth calling a crash: not clean exits or shutdowns. */
    val FAILURE_REASONS = setOf("crash", "hang", "start_failed")

    fun exit(reason: String, code: Int?, signal: String?): String = when {
        reason == "hang" -> "stopped responding and was restarted"
        reason == "start_failed" -> "could not start"
        !signal.isNullOrEmpty() -> "killed by $signal"
        reason == "crash" || reason.isEmpty() -> "exited with code ${code ?: -1}"
        else -> spaced(reason) + (code?.takeIf { it != 0 }?.let { " ($it)" } ?: "")
    }

    fun exit(e: ProcessExit): String = exit(e.reason, e.code, e.signal)

    /** An event of the supervisor's log, in a few words. */
    fun event(e: ProcessEvent): String {
        val what = when (e.kind) {
            "exited" -> exit(e.reason.orEmpty(), e.code, e.signal)
            "started" -> "started"
            "restarting" -> "restarting"
            "failed" -> "crash loop"
            "hung" -> "stopped responding"
            else -> spaced(e.kind)
        }
        return "${process(e.role, e.instance)}: $what"
    }

    fun duration(sec: Double): String {
        val s = maxOf(0L, sec.toLong())
        val d = s / 86400
        val h = s % 86400 / 3600
        val m = s % 3600 / 60
        return when {
            d > 0 -> "${d}d ${h}h"
            h > 0 -> "${h}h ${m}m"
            m > 0 -> "${m}m"
            else -> "${s}s"
        }
    }

    // ---------- the queue ----------

    /** Why a stage's jobs wait (waiting_reason), or null when it runs. */
    fun queueWaiting(reason: String?, service: String?, stage: String): String? {
        val r = reason?.takeIf { it.isNotEmpty() } ?: return null
        val w = WebWords.queueWaiting[r] ?: return spaced(r)
        return fill(w, "service" to (service?.takeIf { it.isNotEmpty() }?.let(::serviceName) ?: "A service"), "stage" to stage)
    }

    private fun serviceName(s: String): String = when (s) {
        "plex" -> "Plex"
        "dizquetv" -> "dizqueTV"
        else -> s
    }

    /** What parked jobs wait on: "the model", "the share smb://nas/O", "disk space". */
    fun waitingOn(on: String): String {
        val parts = on.split(":")
        val head = parts.getOrElse(0) { "" }
        val kind = parts.getOrElse(1) { "" }
        val name = parts.drop(2).joinToString(":")
        return when {
            head == "dependency" && kind == "model" -> "the model"
            head == "dependency" && kind == "share" -> "the share $name"
            head == "resource" && kind == "disk" -> "disk space"
            head == "resource" && kind == "shm" -> "database memory"
            kind.isNotEmpty() -> if (name.isNotEmpty()) "$kind $name" else kind
            else -> on
        }
    }

    /** A stage's health as the web's queue card says it: failing, waiting, failed, needs you. */
    fun health(h: StageHealth?, failedOpen: Int): List<String> = buildList {
        if (h == null) {
            if (failedOpen > 0) add("$failedOpen failing")
            return@buildList
        }
        val f = h.failing
        if (h.state == "failing" && f != null) add("${f.count} failing: ${f.signature}")
        val first: HealthWaiting? = h.waiting.firstOrNull()
        if (h.state == "waiting" && first != null) {
            add("${h.waiting.sumOf { it.count }} waiting for ${waitingOn(first.on)}, resume by themselves")
        }
        if (failedOpen > 0) add("$failedOpen failed")
        if (h.needsYou > 0) add("${h.needsYou} need you")
    }

    // ---------- time ----------

    /** A time the home sent (RFC 3339), in this device's zone; as sent when it is not one. */
    fun whenText(iso: String, zone: ZoneId = ZoneId.systemDefault(), locale: Locale = Locale.getDefault()): String =
        runCatching {
            OffsetDateTime.parse(iso).atZoneSameInstant(zone).format(DateTimeFormatter.ofPattern("EEE d MMM, HH:mm", locale))
        }.getOrElse { iso }
}
