package com.hamzalabs.maktaba.core.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.longOrNull
import kotlinx.serialization.json.put
import okhttp3.HttpUrl
import java.time.Instant

// Parental controls as the apps meet them (Epic 57): the account's own limit and time, the
// home's refusals, asking a parent, the parent PIN and live TV's "not now".
//
// The home filters, counts and refuses by itself. Nothing here decides what an account may
// watch: the apps read these answers to say plainly what the home said, and to offer nothing
// the home would refuse. A call that fails never hides or allows anything on its own.
//
//   GET  /api/me/parental              the caller's own limit            myParental
//   GET  /api/me/parental/time         the caller's time now             myTime
//   POST /api/me/parental/requests     ask a parent (an item, or time)   askParent, askForTime
//   GET  /api/me/parental/guardians    who has a parent PIN              parentPinHolders
//   POST /api/me/parental/unlock       a parent types their PIN          unlockWithPin, moreTimeWithPin
//   GET  /api/channels/{id}/access     may the channel be watched now    channelAccess
//   POST /api/channels/{id}/play       the channel's signed address      playChannel

// ---------- the account's limit ----------

/**
 * `GET /api/me/parental`. [maxTier] is the highest age tier the sign-in may open (0, 9, 13, 16,
 * 18; -1 nothing that has a tier), [deviceLimit] the limit its device set, present even when
 * the account itself is not [restricted].
 */
@Serializable
data class MyParental(
    val restricted: Boolean = false,
    @SerialName("max_tier") val maxTier: Int? = null,
    @SerialName("allow_only") val allowOnly: Boolean = false,
    @SerialName("device_limit") val deviceLimit: Int? = null,
    @SerialName("device_pending") val devicePending: Boolean = false,
    val version: Long = 0,
) {
    /** Under a limit of any kind: the account's, or the device's on an unrestricted account. */
    val limited: Boolean get() = restricted || deviceLimit != null

    companion object {
        /** Before the home answered, and on a home older than Epic 57: nobody is restricted. */
        val None = MyParental()

        /** The tier limit that never shows a number: nothing that has a tier. */
        const val NOTHING_ALLOWED = -1
    }
}

/** The caller's own limit; a home too old to have the route (404) restricts nobody. */
suspend fun ApiClient.myParental(): MyParental = try {
    get("/api/me/parental")
} catch (e: ApiException.Status) {
    if (e.code == 404 || e.code == 405) MyParental.None else throw e
}

// ---------- the account's time ----------

/** Why the home refuses on time (`reason` of `GET /api/me/parental/time` and of `parental-time`). */
object TimeReason {
    const val OUTSIDE_SLOT = "outside_slot"
    const val LIMIT_REACHED = "limit_reached"
    const val NO_HEARTBEAT = "no_heartbeat"
}

/**
 * `GET /api/me/parental/time`: whether the caller may watch now, for how long and when next.
 * [remainingSeconds] counts down only when a rule covers this moment ([limited]); [now] is the
 * home's clock for the answer. The apps keep no count of their own: they show this.
 */
data class MyTime(
    val allowed: Boolean = true,
    val limited: Boolean = false,
    val reason: String = "",
    val remainingSeconds: Long? = null,
    val slotEndsAt: Instant? = null,
    val nextAllowedAt: Instant? = null,
    val now: Instant? = null,
) {
    companion object {
        fun parse(body: String): MyTime? {
            val o = runCatching { ApiClient.json.parseToJsonElement(body) as? JsonObject }.getOrNull() ?: return null
            fun s(k: String) = (o[k] as? JsonPrimitive)?.takeIf { it.isString }?.contentOrNull
            fun at(k: String) = s(k)?.let(::instantOrNull)
            return MyTime(
                // only an explicit false refuses: the home refuses the stream itself anyway
                allowed = (o["allowed"] as? JsonPrimitive)?.booleanOrNull != false,
                limited = (o["limited"] as? JsonPrimitive)?.booleanOrNull == true,
                reason = s("reason").orEmpty(),
                remainingSeconds = (o["remaining_seconds"] as? JsonPrimitive)?.takeIf { !it.isString }?.longOrNull,
                slotEndsAt = at("slot_ends_at"),
                nextAllowedAt = at("next_allowed_at"),
                now = at("now"),
            )
        }
    }
}

/** The caller's time now; with [libraryId], that library's rules are counted too. */
suspend fun ApiClient.myTime(libraryId: String? = null): MyTime {
    val query = if (libraryId != null) mapOf("library_id" to libraryId) else emptyMap()
    return MyTime.parse(String(send(request("GET", "/api/me/parental/time", query))))
        ?: throw ApiException.Decoding("not a time answer")
}

internal fun instantOrNull(s: String): Instant? =
    runCatching { java.time.OffsetDateTime.parse(s).toInstant() }.getOrNull()

// ---------- the home's refusals ----------

/** The problem a stream, a progress post or a watch gets when the account's time is over. */
const val PARENTAL_TIME_SLUG = "parental-time"
const val PARENTAL_TIME_CODE = "parental_time"

/** The home's 403 `parental-time`: why, and when watching opens again (null: the home named no time). */
data class TimeRefusal(val reason: String, val nextAllowedAt: Instant? = null)

/** The time refusal in a 403's [problem], or null when it is anything else. */
fun timeRefusal(status: Int, problem: Problem?): TimeRefusal? {
    if (status != 403 || problem == null) return null
    if (problem.slug != PARENTAL_TIME_SLUG && problem.code != PARENTAL_TIME_CODE) return null
    return TimeRefusal(problem.reason, problem.nextAllowedAt.takeIf { it.isNotEmpty() }?.let(::instantOrNull))
}

/** The time refusal in what a call threw, or null when it is anything else. */
fun Throwable.timeRefusal(): TimeRefusal? = statusCause()?.let { timeRefusal(it.code, it.problem) }

/** A 403 `restricted`: not for this account (or, with [Problem.reason] `programme`, not now). */
fun Throwable.isRestricted(): Boolean = statusCause()?.let { it.code == 403 && it.problem?.code == "restricted" } == true

/**
 * Live TV's 403 `restricted` with reason `programme`: what plays now is above the limit.
 * [retryAt] is when to ask again (null: nothing in the next 12 hours changes it).
 */
data class ProgrammeRefusal(val retryAt: Instant? = null)

fun Throwable.programmeRefusal(): ProgrammeRefusal? {
    val s = statusCause() ?: return null
    val p = s.problem ?: return null
    if (s.code != 403 || p.code != "restricted" || p.reason != "programme") return null
    return ProgrammeRefusal(p.retryAt.takeIf { it.isNotEmpty() }?.let(::instantOrNull))
}

private fun Throwable.statusCause(): ApiException.Status? {
    var c: Throwable? = this
    while (c != null) {
        if (c is ApiException.Status) return c
        if (c.cause === c) return null
        c = c.cause
    }
    return null
}

/** What the home said to a progress post ([ApiClient.reportProgress]). */
sealed interface ProgressAnswer {
    /** Taken: the stream goes on. */
    data object Ok : ProgressAnswer

    /** 404: the stream ended on the home (its session is gone, or its video is no longer this account's). */
    data object Ended : ProgressAnswer

    /** 403 `parental-time`: the account's time is over. Playback stops; nothing asks again by itself. */
    data class TimeOver(val refusal: TimeRefusal) : ProgressAnswer

    /** 503: the home cannot check the account's time right now; ask again after [retryAfterSec]. */
    data class CannotCheck(val retryAfterSec: Long?) : ProgressAnswer

    /** Anything else (the network, a busy home): the stream plays on and the next post says more. */
    data class Failed(val error: Throwable) : ProgressAnswer

    companion object {
        fun of(e: Throwable): ProgressAnswer {
            val over = e.timeRefusal()
            val status = e as? ApiException.Status
            return when {
                over != null -> TimeOver(over)
                status?.code == 404 -> Ended
                status?.code == 503 -> CannotCheck(status.retryAfterSec)
                else -> Failed(e)
            }
        }
    }
}

// ---------- asking a parent ----------

/** What a request or a PIN names (`subject_kind`). */
object SubjectKind {
    const val VIDEO = "video"
    const val SERIES = "series"
    const val COLLECTION = "collection"
    const val LIBRARY = "library"
    const val CHANNEL = "channel"
    const val TIME = "time"
}

/** A request as the home keeps it; [minutes] only on a request for time. */
@Serializable
data class ParentalRequest(
    val id: String = "",
    @SerialName("subject_kind") val subjectKind: String = "",
    @SerialName("subject_id") val subjectId: String = "",
    val minutes: Int? = null,
    val status: String = "pending",
    @SerialName("expires_at") val expiresAt: String? = null,
)

@Serializable
private data class RequestEnvelope(val request: ParentalRequest = ParentalRequest())

/** What asking gave. */
sealed interface AskAnswer {
    /** The request waits for a parent (made now, or already waiting). */
    data class Waiting(val request: ParentalRequest) : AskAnswer

    /**
     * 404: there is nothing by that id this account can ask for. A hidden item answers the same
     * as a missing one, so the app offers nothing more.
     */
    data object NothingToAsk : AskAnswer

    /** 429 `too_many_requests`: 20 requests already wait. */
    data object TooMany : AskAnswer

    /** 403 `not_restricted`: this account has no limit to ask about. */
    data object NotRestricted : AskAnswer
}

/**
 * Asks the account's guardians to open [subjectId] of [subjectKind]
 * (`POST /api/me/parental/requests`). Anything but the answers of [AskAnswer] is thrown.
 */
suspend fun ApiClient.askParent(subjectKind: String, subjectId: String): AskAnswer {
    val body = buildJsonObject {
        put("subject_kind", subjectKind)
        put("subject_id", subjectId)
    }
    return try {
        AskAnswer.Waiting(decode<RequestEnvelope>(send(request("POST", "/api/me/parental/requests", body = body))).request)
    } catch (e: ApiException.Status) {
        when {
            e.code == 404 -> AskAnswer.NothingToAsk
            e.code == 429 && e.problem?.code == "too_many_requests" -> AskAnswer.TooMany
            e.code == 403 && e.problem?.code == "not_restricted" -> AskAnswer.NotRestricted
            else -> throw e
        }
    }
}

/** The minutes a child may ask for at once (the time's-up screen's buttons). */
val MORE_TIME_MINUTES = listOf(15, 30, 60)

/** Asks for [minutes] more today: `subject_kind` `time`, the minutes as text. */
suspend fun ApiClient.askForTime(minutes: Int): AskAnswer = askParent(SubjectKind.TIME, minutes.toString())

// ---------- the parent PIN ----------

/** Someone who can unlock with their PIN on this device: a guardian, or an admin, who set one. */
@Serializable
data class PinHolder(val id: String = "", val username: String = "")

@Serializable
private data class PinHolders(val guardians: List<PinHolder> = emptyList())

/**
 * Who the PIN screen offers (`GET /api/me/parental/guardians`). Empty when nobody set a PIN, the
 * account is not restricted (403), or the home is too old to say: Enter parent PIN is then not shown.
 */
suspend fun ApiClient.parentPinHolders(): List<PinHolder> = try {
    get<PinHolders>("/api/me/parental/guardians").guardians.filter { it.id.isNotEmpty() }
} catch (e: ApiException.Status) {
    if (e.code == 403 || e.code == 404 || e.code == 405) emptyList() else throw e
}

/** What a typed PIN gave. The home says the same for a wrong PIN and for someone who is no guardian. */
sealed interface UnlockAnswer {
    /** Opened until the home's day ends (an item), or the minutes were added for today (time). */
    data object Unlocked : UnlockAnswer

    /** 403 `pin_wrong`: nothing was opened. */
    data object Wrong : UnlockAnswer

    /** 429: PIN entry is locked for [retryAfterSec] more seconds (null: the home did not say). */
    data class Locked(val retryAfterSec: Long?) : UnlockAnswer

    /** 404: nothing by that id this account can reach; the PIN was not looked at. */
    data object NothingToUnlock : UnlockAnswer

    /** 403 `not_restricted`. */
    data object NotRestricted : UnlockAnswer
}

/** Whether [pin] has the shape the home takes: six digits. The device checks nothing else. */
fun isParentPin(pin: String): Boolean = pin.length == 6 && pin.all { it in '0'..'9' }

/**
 * A parent types their PIN on the child's device (`POST /api/me/parental/unlock`): [subjectKind]
 * and [subjectId] name the item, or `time` and the minutes as text. The PIN goes to the home
 * and is kept nowhere on the device.
 */
suspend fun ApiClient.unlockWithPin(guardianId: String, pin: String, subjectKind: String, subjectId: String): UnlockAnswer {
    val body = buildJsonObject {
        put("guardian_id", guardianId)
        put("pin", pin)
        put("subject_kind", subjectKind)
        put("subject_id", subjectId)
    }
    return try {
        send(request("POST", "/api/me/parental/unlock", body = body))
        UnlockAnswer.Unlocked
    } catch (e: ApiException.Status) {
        val code = e.problem?.code.orEmpty()
        when {
            e.code == 403 && code == "not_restricted" -> UnlockAnswer.NotRestricted
            e.code == 403 -> UnlockAnswer.Wrong
            e.code == 429 -> UnlockAnswer.Locked(e.retryAfterSec)
            e.code == 404 -> UnlockAnswer.NothingToUnlock
            else -> throw e
        }
    }
}

/** [minutes] more today, on a parent's PIN. */
suspend fun ApiClient.moreTimeWithPin(guardianId: String, pin: String, minutes: Int): UnlockAnswer =
    unlockWithPin(guardianId, pin, SubjectKind.TIME, minutes.toString())

// ---------- live TV ----------

/** `GET /api/channels/{id}/access`: may what the channel plays now be watched, and when that may change. */
data class ChannelAccess(val restricted: Boolean = false, val allowed: Boolean = true, val until: Instant? = null)

/** `POST /api/channels/{id}/play`: the channel's playlist with its signature, under this home. */
data class ChannelPlay(val url: HttpUrl, val expiresAt: Instant? = null, val until: Instant? = null)

private fun channelSegment(id: String) = java.net.URLEncoder.encode(id, Charsets.UTF_8.name()).replace("+", "%20")

/** Whether the caller may watch [channelId] now. A hidden channel is a 404, as a missing one. */
suspend fun ApiClient.channelAccess(channelId: String): ChannelAccess {
    val o = decode<JsonObject>(send(request("GET", "/api/channels/${channelSegment(channelId)}/access")))
    return ChannelAccess(
        restricted = (o["restricted"] as? JsonPrimitive)?.booleanOrNull == true,
        allowed = (o["allowed"] as? JsonPrimitive)?.booleanOrNull != false,
        until = (o["until"] as? JsonPrimitive)?.takeIf { it.isString }?.contentOrNull?.let(::instantOrNull),
    )
}

/**
 * The channel's signed address. A programme above the limit is thrown as the home's 403
 * ([programmeRefusal] reads it); an address that is not this home's `/stream/` is refused here.
 */
suspend fun ApiClient.playChannel(channelId: String): ChannelPlay {
    val o = decode<JsonObject>(send(request("POST", "/api/channels/${channelSegment(channelId)}/play")))
    fun s(k: String) = (o[k] as? JsonPrimitive)?.takeIf { it.isString }?.contentOrNull
    val ref = s("url") ?: throw ApiException.Decoding("the home gave no address to play")
    // relative to the home (under its base path), as a stream session's addresses are
    val under = basePath.isNotEmpty() && ref.startsWith("/") && !ref.startsWith("//") && !ref.startsWith("$basePath/")
    val url = home.resolve(if (under) basePath + ref else ref)?.takeIf(::isStream) ?: throw ApiException.Decoding("not this home's stream")
    return ChannelPlay(url, s("expires_at")?.let(::instantOrNull), s("until")?.let(::instantOrNull))
}

/** What a channel is to a limited account now ([channelNow]). */
sealed interface ChannelNow {
    /** It may be watched, at [url]; [until] is when a programme above the limit starts (null: none known). */
    data class Playing(val url: HttpUrl, val until: Instant?) : ChannelNow

    /** "Not available right now": the programme on air is above the limit until [retryAt] (null: not known). */
    data class NotNow(val retryAt: Instant?) : ChannelNow
}

/**
 * Asks to play [channelId] for an account under a limit: its signed address, or "not now" when
 * the home answers 403 `restricted` for the programme on air. Every other failure is thrown
 * (a hidden channel is the 404 of a missing one).
 */
suspend fun ApiClient.channelNow(channelId: String): ChannelNow = try {
    playChannel(channelId).let { ChannelNow.Playing(it.url, it.until) }
} catch (e: ApiException.Status) {
    e.programmeRefusal()?.let { ChannelNow.NotNow(it.retryAt) } ?: throw e
}

/** Never ask the home about a channel sooner than this after an answer. */
const val CHANNEL_ASK_MIN_MS = 5_000L

/** With no time named, a refusal is asked about again this often. */
const val CHANNEL_ASK_AGAIN_MS = 60_000L

/**
 * How long to wait before asking about a channel again: a second after [at] (the time the home
 * named), never sooner than [CHANNEL_ASK_MIN_MS]; with no time, [CHANNEL_ASK_AGAIN_MS] while it
 * is refused and never (null) while it plays.
 */
fun channelAskAgainMs(at: Instant?, allowed: Boolean, nowMs: Long): Long? = when {
    at != null -> (at.toEpochMilli() - nowMs + 1_000).coerceAtLeast(CHANNEL_ASK_MIN_MS)
    allowed -> null
    else -> CHANNEL_ASK_AGAIN_MS
}
