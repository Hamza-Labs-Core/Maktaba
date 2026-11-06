package com.hamzalabs.maktaba.core.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

// Word review (Epic 52, story 52.13): the suspect words of every transcript, for a home admin,
// in the Remote. Every route is the home admins' only (a library role grants nothing): anyone
// else gets 403, which the screens show as "Only home admins can review words". Reasons and
// statuses are kept as the home's strings and read through the enums below, an unknown code
// being Unknown, never a crash.

/** A reason a word was flagged (the web's and the API's list). */
enum class ReviewReason(val code: String) {
    LowConfidence("low_confidence"), NotInDictionary("not_in_dictionary"),
    ListenersDisagree("listeners_disagree"), QuranMismatch("quran_mismatch"),
    NamedEntity("named_entity"), NearVocabulary("near_vocabulary"), Manual("manual"),
    PreviousFix("previous_fix"), OverBudget("over_budget"), Unknown("");

    companion object {
        fun of(code: String): ReviewReason = entries.firstOrNull { it.code == code && it != Unknown } ?: Unknown
    }
}

/** A flag's status. */
enum class ReviewStatus(val code: String) {
    Open("open"), Accepted("accepted"), Corrected("corrected"), Name("name"), Dictionary("dictionary"),
    Skipped("skipped"), Stale("stale"), Unknown("");

    companion object {
        fun of(code: String): ReviewStatus = entries.firstOrNull { it.code == code && it != Unknown } ?: Unknown
    }
}

/** What a reviewer does with a word. */
enum class ReviewAction(val code: String) { Accept("accept"), Correct("correct"), Name("name"), Dictionary("dictionary"), Skip("skip") }

@Serializable
data class ReviewCandidate(val text: String, val source: String = "", val score: Double = 0.0)

@Serializable
data class QuranRef(
    val surah: Int = 0,
    val aya: Int? = null,
    @SerialName("label_ar") val labelAr: String? = null,
    @SerialName("label_en") val labelEn: String? = null,
    val expected: String? = null,
    val missing: String? = null,
    val kind: String? = null,
)

@Serializable
data class ReviewFlag(
    val id: String,
    val kind: String = "word",
    @SerialName("video_id") val videoId: String = "",
    @SerialName("video_title") val videoTitle: String = "",
    @SerialName("library_id") val libraryId: String = "",
    @SerialName("segment_seq") val segmentSeq: Int = 0,
    @SerialName("char_start") val charStart: Int = 0,
    @SerialName("char_len") val charLen: Int = 0,
    @SerialName("start_sec") val startSec: Double? = null,
    @SerialName("end_sec") val endSec: Double? = null,
    val original: String = "",
    val context: String = "",
    val language: String = "",
    val direction: String = "ltr",
    @Serializable(with = StringList::class) val reasons: List<String> = emptyList(),
    @Serializable(with = CandidateList::class) val candidates: List<ReviewCandidate> = emptyList(),
    val status: String = "open",
    val priority: Double = 0.0,
    @SerialName("locked_by") val lockedBy: String? = null,
    @SerialName("locked_by_name") val lockedByName: String? = null,
    @SerialName("locked_until") val lockedUntil: String? = null,
    // the detail's extras (absent in the list)
    val before: String = "",
    val after: String = "",
    @SerialName("same_key_open") val sameKeyOpen: Int = 0,
    val verse: QuranRef? = null,
    @SerialName("reviewers_note") val reviewersNote: String = "",
) {
    val reasonCodes: List<ReviewReason> get() = reasons.map(ReviewReason::of)
    val state: ReviewStatus get() = ReviewStatus.of(status)
    val rightToLeft: Boolean get() = direction == "rtl"
    val isQuran: Boolean get() = ReviewReason.QuranMismatch in reasonCodes

    /** The sentence around the word, in code points (Kotlin strings index UTF-16 units). */
    fun parts(): Triple<String, String, String> {
        val cps = context.codePoints().toArray()
        val a = charStart.coerceIn(0, cps.size)
        val b = (charStart + charLen).coerceIn(a, cps.size)
        fun str(from: Int, to: Int) = String(cps, from, to - from)
        return Triple(str(0, a), str(a, b), str(b, cps.size))
    }
}

/** Lenient lists for the flag's arrays: an element that cannot be read is left out. */
object StringList : kotlinx.serialization.KSerializer<List<String>> by LenientList(kotlinx.serialization.serializer())
object CandidateList : kotlinx.serialization.KSerializer<List<ReviewCandidate>> by LenientList(ReviewCandidate.serializer())

@Serializable
data class ReviewPage(
    @Serializable(with = FlagList::class) val items: List<ReviewFlag> = emptyList(),
    @SerialName("next_cursor") val nextCursor: String? = null,
)

object FlagList : kotlinx.serialization.KSerializer<List<ReviewFlag>> by LenientList(ReviewFlag.serializer())

@Serializable
data class ReviewClip(val url: String, val start: Double = 0.0, val end: Double = 0.0, @SerialName("expires_at") val expiresAt: String = "") {
    /** The clip's address on [home] (the API answers a path; a home under a base path keeps it). */
    fun absolute(home: okhttp3.HttpUrl): String? {
        val base = home.encodedPath.trimEnd('/')
        val path = if (base.isNotEmpty() && url.startsWith("/") && !url.startsWith("//") && !url.startsWith("$base/")) base + url else url
        return home.resolve(path)?.toString()
    }
}

@Serializable
data class ReviewResult(
    val status: String = "",
    val changed: Int = 0,
    @SerialName("edit_id") val editId: String? = null,
    @SerialName("batch_id") val batchId: String? = null,
)

@Serializable
data class ReviewSummary(val open: Int = 0)

/** `GET /api/review/flags`: open words by priority. */
suspend fun ApiClient.reviewFlags(library: String? = null, reason: String? = null, cursor: String? = null): ReviewPage =
    get("/api/review/flags", buildMap {
        library?.let { put("library", it) }
        reason?.let { put("reason", it) }
        cursor?.let { put("cursor", it) }
        put("limit", "50")
    })

/** `GET /api/review/flags/{id}`: the word with its context (and a 5-minute lock). */
suspend fun ApiClient.reviewFlag(id: String): ReviewFlag = get("/api/review/flags/${pathSegment(id)}")

/** `GET /api/review/flags/{id}/clip`: a signed URL for the audio around the word. */
suspend fun ApiClient.reviewClip(id: String): ReviewClip = get("/api/review/flags/${pathSegment(id)}/clip")

/** `POST /api/review/flags/{id}/resolve`. */
suspend fun ApiClient.resolveFlag(
    id: String,
    action: ReviewAction,
    text: String? = null,
    applyToSameKey: Boolean = false,
    force: Boolean = false,
): ReviewResult = decode(send(request("POST", "/api/review/flags/${pathSegment(id)}/resolve", body = resolveBody(action, text, applyToSameKey, force))))

internal fun resolveBody(action: ReviewAction, text: String?, applyToSameKey: Boolean, force: Boolean): JsonObject =
    buildJsonObject {
        put("action", action.code)
        text?.let { put("text", it) }
        if (applyToSameKey) put("apply_to_same_key", true)
        if (force) put("force", true)
    }

/** Undo the last action: its batch, else its edit. */
suspend fun ApiClient.undoReview(result: ReviewResult) {
    when {
        result.batchId != null -> send(request("POST", "/api/review/batches/${pathSegment(result.batchId)}/undo"))
        result.editId != null -> send(request("POST", "/api/review/edits/${pathSegment(result.editId)}/undo"))
    }
}

/** `GET /api/review/summary`: the open count (Word review's badge). */
suspend fun ApiClient.reviewSummary(): ReviewSummary = get("/api/review/summary")

internal fun pathSegment(s: String): String = java.net.URLEncoder.encode(s, Charsets.UTF_8.name()).replace("+", "%20")

/** A 403 from a review route: the caller is not (or no longer) a home admin. */
fun ApiException.isReviewForbidden(): Boolean = this is ApiException.Status && code == 403

/** A 409's code from the home (locked, stale, resolved, changed_since), or null. */
fun ApiException.reviewConflict(): String? =
    (this as? ApiException.Status)?.takeIf { it.code == 409 }?.problem?.slug
