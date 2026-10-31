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
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonPrimitive
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.DecimalStyle

/**
 * What the status screens say, in the web admin's words (WebWords), in the [Lookup]'s language:
 * a value the home sends that has no words here is shown as sent, its underscores as spaces.
 */
internal class Say(private val w: Lookup, private val zone: ZoneId = ZoneId.systemDefault()) {
    /** One of the app's strings, in this language. */
    fun t(id: Int, vararg args: Any): String = w.text(id, *args)

    /** One of the app's counted strings ("1 job", "3 jobs"), in this language. */
    fun count(id: Int, n: Int): String = w.plural(id, n, n)

    /** A counted string with more values than its count. */
    fun plural(id: Int, n: Int, vararg args: Any): String = w.plural(id, n, *args)

    private fun word(words: Map<String, Int>, v: String): String = words[v]?.let { w.text(it) } ?: spaced(v)

    /** A web template with the home's named values; a value it did not send reads as empty. */
    private fun fill(template: Template, vars: Map<String, String>): String =
        w.text(template.id, *template.args.map { vars[it].orEmpty() }.toTypedArray())

    private fun labelled(label: String, value: String): String = w.text(R.string.say_label_value, label, value)

    // ---------- updates ----------

    fun runState(s: String): String = word(WebWords.updateState, s)

    fun step(s: String): String = word(WebWords.updateStep, s)

    /** An error code's words, as the web's reason(): a step's failure the step's, else the code. */
    fun code(c: String): String {
        WebWords.updateCode[c]?.let { return w.text(it) }
        if (c.startsWith("verify_")) return w.text(WebWords.updateCode.getValue("verify_failed"))
        Regex("^([a-z]+)_failed$").matchEntire(c)?.let { m ->
            WebWords.updateStep[m.groupValues[1]]?.let { return labelled(w.text(it), runState("failed")) }
        }
        return spaced(c)
    }

    /** What the home does next about its update, as the web's "Next" says it. */
    fun planNext(ov: UpdatesOverview): String {
        ov.activeRun?.let { return w.text(R.string.say_updating, it.version) }
        val plan = ov.plan
        val version = plan.target?.version ?: return w.text(R.string.say_up_to_date)
        if (plan.held) return w.text(R.string.say_plan_held, version)
        plan.nextRunAt?.let { at -> return w.text(R.string.say_plan_at, version, whenText(at)) }
        return waiting(plan.waiting ?: "auto_install_off", version)
    }

    /** Why the planned [version] does not install now, as a sentence. */
    fun waiting(reason: String, version: String): String =
        WebWords.planWaiting[reason]?.let { fill(it, mapOf("version" to version)) }
            ?: WebWords.updateWaiting[reason]?.let { w.text(it) }
            ?: w.text(R.string.say_plan_unknown, version, spaced(reason))

    /** A run's gate (detail.waiting): tv_busy, outside_window, tv_swapping, ... */
    fun runWaiting(reason: String): String = word(WebWords.updateWaiting, reason)

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
        return w.text(R.string.say_not_started, why)
    }

    fun blocked(r: CatalogRelease): String =
        w.text(R.string.say_blocked, r.version, word(WebWords.blockedReason, r.blockedReason.orEmpty()))

    fun stuck(s: UpdatesStuck): String = w.plural(R.plurals.say_stuck, s.windows, s.version, s.windows, code(s.code))

    /** A held TV's line: held, or what became of it since. */
    fun tv(state: String?, heldOn: String?, release: String): String {
        val on = heldOn?.takeIf { it.isNotEmpty() } ?: w.text(R.string.say_tv_previous)
        return when (val s = state ?: "held") {
            "held" -> w.text(R.string.say_tv_held, on)
            "retrying" -> w.text(R.string.say_tv_retrying, on)
            "live" -> w.text(R.string.say_tv_live, release)
            "dropped" -> w.text(R.string.say_tv_dropped, on)
            "replaced" -> w.text(R.string.say_tv_replaced)
            "failed" -> w.text(R.string.say_tv_failed)
            "gone" -> w.text(R.string.say_tv_gone, on)
            "pending" -> w.text(R.string.say_tv_pending, on)
            else -> w.text(R.string.say_tv_other, spaced(s))
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
        val name = WebWords.processRole[role]?.let { w.text(it) } ?: role
        return if (instance.isNullOrEmpty()) name else "$name · $instance"
    }

    fun process(p: ProcessStatus): String = process(p.role, p.instance)

    fun processState(s: String): String = word(WebWords.processState, s)

    /** A process's state as the web says it: a stopped one not wanted now is standing by. */
    fun processState(p: ProcessStatus): String = processState(if (p.wanted == false && p.state == "stopped") "standby" else p.state)

    /** Why a process is not started, in the web's words (its detail_code with its values), else the home's English. */
    fun processDetail(p: ProcessStatus): String? {
        p.detailCode?.let { code ->
            WebWords.processDetail[code]?.let { template ->
                val vars = p.detailParams.orEmpty().mapNotNull { (k, v) ->
                    (v as? JsonPrimitive)?.takeUnless { it is JsonNull }?.let { k to it.content }
                }.toMap()
                return fill(template, vars)
            }
        }
        return p.detail?.takeIf { it.isNotEmpty() }
    }

    fun exit(reason: String, code: Int?, signal: String?): String = when {
        reason == "hang" -> w.text(R.string.say_exit_hang)
        reason == "start_failed" -> w.text(R.string.say_exit_start_failed)
        !signal.isNullOrEmpty() -> w.text(R.string.say_exit_signal, signal)
        reason == "crash" || reason.isEmpty() -> w.text(R.string.say_exit_code, code ?: -1)
        else -> spaced(reason) + (code?.takeIf { it != 0 }?.let { " (${number(it)})" } ?: "")
    }

    fun exit(e: ProcessExit): String = exit(e.reason, e.code, e.signal)

    /** An event of the supervisor's log, in a few words. */
    fun event(e: ProcessEvent): String {
        val what = when (e.kind) {
            "exited" -> exit(e.reason.orEmpty(), e.code, e.signal)
            "started" -> w.text(R.string.say_event_started)
            "restarting" -> w.text(R.string.say_event_restarting)
            "failed" -> w.text(R.string.say_event_crash_loop)
            "hung" -> w.text(R.string.say_event_hung)
            else -> spaced(e.kind)
        }
        return labelled(process(e.role, e.instance), what)
    }

    fun duration(sec: Double): String {
        val s = maxOf(0L, sec.toLong())
        val d = (s / 86400).toInt()
        val h = (s % 86400 / 3600).toInt()
        val m = (s % 3600 / 60).toInt()
        return when {
            d > 0 -> w.text(R.string.say_dur_days, d, h)
            h > 0 -> w.text(R.string.say_dur_hours, h, m)
            m > 0 -> w.text(R.string.say_dur_minutes, m)
            else -> w.text(R.string.say_dur_seconds, s.toInt())
        }
    }

    /** A wait or an estimate: seconds, minutes, else hours. */
    fun minutes(sec: Double): String = when {
        sec < 90 -> w.text(R.string.say_seconds_short, sec.toInt())
        sec < 5400 -> w.text(R.string.say_minutes_short, (sec / 60).toInt())
        else -> w.text(R.string.say_hours_short, sec / 3600)
    }

    /** A number as this language writes it. */
    fun number(n: Int): String = String.format(w.locale, "%d", n)

    // ---------- the queue ----------

    /** Why a stage's jobs wait (waiting_reason), or null when it runs. */
    fun queueWaiting(reason: String?, service: String?, stage: String): String? {
        val r = reason?.takeIf { it.isNotEmpty() } ?: return null
        val template = WebWords.queueWaiting[r] ?: return spaced(r)
        val who = service?.takeIf { it.isNotEmpty() }?.let(::serviceName) ?: w.text(R.string.say_a_service)
        return fill(template, mapOf("service" to who, "stage" to stage))
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
            head == "dependency" && kind == "model" -> w.text(R.string.say_on_model)
            head == "dependency" && kind == "share" -> w.text(R.string.say_on_share, name)
            head == "resource" && kind == "disk" -> w.text(R.string.say_on_disk)
            head == "resource" && kind == "shm" -> w.text(R.string.say_on_shm)
            kind.isNotEmpty() -> if (name.isNotEmpty()) "$kind $name" else kind
            else -> on
        }
    }

    /** A stage's health as the web's queue card says it: failing, waiting, failed, needs you. */
    fun health(h: StageHealth?, failedOpen: Int): List<String> = buildList {
        if (h == null) {
            if (failedOpen > 0) add(w.text(R.string.say_health_failing_count, failedOpen))
            return@buildList
        }
        val f = h.failing
        if (h.state == "failing" && f != null) add(w.text(R.string.say_health_failing, f.count, f.signature))
        val first: HealthWaiting? = h.waiting.firstOrNull()
        if (h.state == "waiting" && first != null) {
            add(w.text(R.string.say_health_waiting, h.waiting.sumOf { it.count }, waitingOn(first.on)))
        }
        if (failedOpen > 0) add(w.text(R.string.say_health_failed, failedOpen))
        if (h.needsYou > 0) add(w.text(R.string.say_health_needs_you, h.needsYou))
    }

    fun queueState(s: String): String = word(WebWords.queueState, s)

    fun jobState(s: String): String = word(WebWords.jobState, s)

    fun machineStatus(s: String): String = word(WebWords.machineStatus, s)

    // ---------- time ----------

    /** A time the home sent (RFC 3339), in this device's zone and language; as sent when it is not one. */
    fun whenText(iso: String): String =
        runCatching {
            OffsetDateTime.parse(iso).atZoneSameInstant(zone).format(
                DateTimeFormatter.ofPattern("EEE d MMM, HH:mm", w.locale).withDecimalStyle(DecimalStyle.of(w.locale)),
            )
        }.getOrElse { iso }

    companion object {
        fun spaced(v: String): String = v.replace('_', ' ')

        /** Exits worth calling a crash: not clean exits or shutdowns. */
        val FAILURE_REASONS = setOf("crash", "hang", "start_failed")
    }
}
