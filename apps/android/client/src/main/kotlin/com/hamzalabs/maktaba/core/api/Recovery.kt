package com.hamzalabs.maktaba.core.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.put

// The recovery console on the Remote (Epic 53 stories 53.04 and 53.05): the routes the web's
// recovery page calls, `/recovery/api/*` on the home's address, served by the supervisor
// itself, so they answer with the API or the database down. The console takes the app's
// recovery pass (a 7-day JWT the API gives an admin while it is up) or its access token as a
// bearer; anyone else gets 401. Its refusals never sign the app out.

@Serializable
internal data class RecoveryPassAnswer(val pass: String? = null)

/** The status board inside the console's state (the 53.03 document). */
@Serializable
data class RecoveryStatus(
    val mode: String = "normal",
    val overall: String = "ok",
    val components: Map<String, String> = emptyMap(),
)

/**
 * `GET /recovery/api/state`: the status board, each part with its detail, the disk, the version
 * a rollback goes back to and whether the home is in safe mode.
 */
@Serializable
data class RecoveryState(
    val status: RecoveryStatus = RecoveryStatus(),
    val components: List<RecoveryPart> = emptyList(),
    @SerialName("disk_free") val diskFree: Long = -1,
    @SerialName("logs_bytes") val logsBytes: Long = 0,
    val rollback: String? = null,
    @SerialName("safe_mode") val safeMode: Boolean = false,
) {
    /** The groups the status names, in the board's order (the supervisor's), then any other. */
    val groups: List<Pair<String, String>>
        get() {
            val known = GROUP_ORDER.filter { it in status.components }
            val others = status.components.keys.filter { it !in GROUP_ORDER }.sorted()
            return (known + others).map { it to (status.components[it] ?: "ok") }
        }

    /** Every part is working. */
    val allWell: Boolean get() = status.overall == "ok"

    /** The previous version kept, if any. */
    val rollbackVersion: String? get() = rollback?.takeIf { it.isNotEmpty() }

    companion object {
        val GROUP_ORDER = listOf("database", "api", "streaming", "processing", "search", "logs")
    }
}

/** One part (an Epic 51 catalog component) on the console. */
@Serializable
data class RecoveryPart(
    val id: String,
    val group: String = "other",
    val phase: String? = null,
    val reason: String? = null,
    /** ok, starting, degraded, restarting, down or off */
    val state: String = "ok",
    val restarts: Int = 0,
    @SerialName("cut_off") val cutOff: Boolean = false,
    /** RFC 3339 */
    @SerialName("last_exit") val lastExit: String? = null,
    @SerialName("last_code") val lastCode: Int? = null,
    @SerialName("last_error") val lastError: String? = null,
    /** A part held or turned off is not restarted from the console. */
    @SerialName("can_restart") val canRestart: Boolean = false,
) {
    val troubled: Boolean get() = state in setOf("degraded", "restarting", "down")
}

/** One line of `GET /recovery/api/logs` (the 53.01 schema, read from the log files). */
data class RecoveryLine(val ts: String?, val level: String, val service: String, val component: String, val msg: String) {
    companion object {
        /** A line's words: its message, a raw line's text, and its error. */
        fun of(o: JsonObject): RecoveryLine {
            fun str(k: String, from: JsonObject = o) = (from[k] as? JsonPrimitive)?.contentOrNull
            var msg = str("msg").orEmpty()
            val text = (o["attrs"] as? JsonObject)?.let { str("text", it) }
            if (msg == "raw_line" && text != null) msg = text
            val err = (o["err"] as? JsonPrimitive)?.contentOrNull ?: (o["err"] as? JsonObject)?.let { str("msg", it) }
            if (!err.isNullOrEmpty()) msg += ": $err"
            return RecoveryLine(str("ts"), str("level").orEmpty(), str("service").orEmpty(), str("component").orEmpty(), msg)
        }
    }
}

/**
 * What the console can do from the Remote (story 53.04 §4). Restoring a backup and the
 * diagnostics download stay on the web page.
 */
sealed interface RecoveryAction {
    data class Restart(val id: String) : RecoveryAction
    data object RestartAll : RecoveryAction
    data object SafeMode : RecoveryAction
    data object StaySafe : RecoveryAction
    data object LeaveSafe : RecoveryAction
    data class Rollback(val version: String) : RecoveryAction

    /**
     * How many times the person confirms: twice to go back a version (the home asks twice for
     * every action through the relay, and says so with 428).
     */
    val confirms: Int get() = if (this is Rollback) 2 else 1

    /** The action's word on the console. */
    val wire: String
        get() = when (this) {
            is Restart -> "restart"
            RestartAll -> "restart_all"
            SafeMode -> "safe_mode"
            StaySafe -> "stay_safe"
            LeaveSafe -> "leave_safe"
            is Rollback -> "rollback"
        }

    fun body(confirm: Int): JsonObject = buildJsonObject {
        put("action", wire)
        if (this@RecoveryAction is Restart) put("component", id)
        put("confirm", confirm)
    }
}

/** Why the console did not answer as asked. */
sealed class RecoveryException(message: String) : Exception(message) {
    /**
     * 401 or 403: not an admin of this home, a pass that expired or was revoked, or too many
     * tries (the console locks an address for 5 minutes).
     */
    class Denied : RecoveryException("the recovery console refused this app")

    /** 428: the home wants [need] confirmations (an action through the relay). */
    class Confirm(val need: Int) : RecoveryException("the home wants $need confirmations")

    /** The home refused the action, in its words (409: held, nothing to roll back to, …). */
    class Refused(val why: String) : RecoveryException("refused: $why")

    /** 404: a home from before the recovery console. */
    class NotHere : RecoveryException("this home has no recovery console")

    /** No answer. */
    class Unreachable : RecoveryException("the recovery console did not answer")
}

suspend fun ApiClient.recoveryState(): RecoveryState {
    val bytes = recoveryCall(request("GET", "/recovery/api/state"))
    return try {
        ApiClient.json.decodeFromString(RecoveryState.serializer(), String(bytes))
    } catch (e: Exception) {
        throw RecoveryException.Refused(e.message ?: "unexpected answer")
    }
}

/** The warnings and errors of the last [minutes], newest first. */
suspend fun ApiClient.recoveryLines(minutes: Int = 15): List<RecoveryLine> {
    val bytes = recoveryCall(request("GET", "/recovery/api/logs", mapOf("minutes" to minutes.toString())))
    val lines = runCatching { ApiClient.json.parseToJsonElement(String(bytes)).jsonObject["lines"]?.jsonArray }.getOrNull()
    return lines.orEmpty().mapNotNull { (it as? JsonObject)?.let(RecoveryLine::of) }
}

/** Runs [action], confirmed [confirm] times. */
suspend fun ApiClient.recoveryAction(action: RecoveryAction, confirm: Int) {
    // the console's guard against a cross-site post: a header only its callers send
    val r = request("POST", "/recovery/api/action", body = action.body(confirm)).newBuilder()
        .header("X-Maktaba-Recovery", "1").build()
    recoveryCall(r)
}
