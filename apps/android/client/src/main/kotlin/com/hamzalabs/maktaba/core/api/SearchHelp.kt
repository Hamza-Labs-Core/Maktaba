package com.hamzalabs.maktaba.core.api

import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.nullable
import kotlinx.serialization.descriptors.SerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder
import kotlinx.serialization.json.JsonDecoder
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import java.util.Locale

// Story 49.11: what the apps read and send for search's help (story 49.8's API), shared by the
// Android Player, the Android TV app and the Player for Windows and Linux: the suggestions' kinds,
// the results' order, the facets and the filters chosen from them, and did-you-mean. Each app
// keeps its own screen state and words; the request bodies and the rules are here, once.
// An older home (before 49.8) sends none of the new fields: every one reads as absent.

/** The order of a search's results (`sort` on `POST /api/search`). */
enum class SearchSort(val wire: String) {
    Relevance("relevance"),
    Newest("newest"),
    Matches("matches");

    companion object {
        /** The home's word for an order; relevance for none or one this app does not know. */
        fun of(wire: String?): SearchSort = entries.firstOrNull { it.wire == wire } ?: Relevance
    }
}

/** What a suggestion is; a home before 49.8 sends texts only, which have none. */
enum class SuggestKind(val wire: String) {
    /** one of the caller's own earlier searches */
    History("history"),
    /** the query with its last word completed from the transcripts' words */
    Term("term"),
    /** a video's title: choosing it opens the video */
    Title("title"),
    /** a named speaker */
    Speaker("speaker");

    companion object {
        fun of(wire: String?): SuggestKind? = entries.firstOrNull { it.wire == wire }
    }
}

/** One suggestion of `GET /api/search/suggest`'s `items`. */
@Serializable
data class Suggestion(
    val text: String = "",
    @SerialName("kind") val kindWire: String = "",
    @SerialName("video_id") val videoId: String? = null,
) {
    val kind: SuggestKind? get() = SuggestKind.of(kindWire)

    /** The video a title suggestion opens; null for any other, which is searched as its text. */
    val opensVideo: String? get() = videoId?.takeIf { kind == SuggestKind.Title && it.isNotBlank() }
}

/** A facet of `POST /api/search`'s `facets`, in the order the apps show them. */
enum class Facet(val wire: String) {
    Library("library"),
    Language("language"),
    Speaker("speaker"),
    Year("year"),
    Duration("duration");

    /** A year and a duration band are one range each in the request: a second replaces the first. */
    val single: Boolean get() = this == Year || this == Duration
}

/** One bucket of a facet: its value, its name (a library's), how many videos, and for a year or band its filter. */
@Serializable
data class FacetBucket(
    val value: String = "",
    val label: String? = null,
    val count: Int = 0,
    /** For a year or a duration band: the request filter (`date` or `duration_sec`) that narrows to it. */
    val filter: JsonObject? = null,
) {
    /** A chosen bucket the narrowed facets no longer list: shown pressed, without a count. */
    val counted: Boolean get() = count >= 0
}

/** `facets` on `POST /api/search`: counts of the videos found, with the first page only. */
@Serializable
data class SearchFacets(
    @Serializable(with = LenientList::class) val library: List<FacetBucket> = emptyList(),
    @Serializable(with = LenientList::class) val language: List<FacetBucket> = emptyList(),
    @Serializable(with = LenientList::class) val speaker: List<FacetBucket> = emptyList(),
    @Serializable(with = LenientList::class) val year: List<FacetBucket> = emptyList(),
    @Serializable(with = LenientList::class) val duration: List<FacetBucket> = emptyList(),
    /** how many videos were counted */
    val videos: Int = 0,
) {
    /** A facet's buckets, those with a value only. */
    fun of(f: Facet): List<FacetBucket> = when (f) {
        Facet.Library -> library
        Facet.Language -> language
        Facet.Speaker -> speaker
        Facet.Year -> year
        Facet.Duration -> duration
    }.filter { it.value.isNotBlank() }
}

/** `facets`: anything but an object is none, so one odd answer never fails the search. */
internal object LenientFacets : KSerializer<SearchFacets?> {
    private val strict = SearchFacets.serializer().nullable
    override val descriptor: SerialDescriptor = strict.descriptor
    override fun serialize(encoder: Encoder, value: SearchFacets?) = strict.serialize(encoder, value)
    override fun deserialize(decoder: Decoder): SearchFacets? {
        val d = decoder as? JsonDecoder ?: return strict.deserialize(decoder)
        val e = d.decodeJsonElement() as? JsonObject ?: return null
        return runCatching { d.json.decodeFromJsonElement(SearchFacets.serializer(), e) }.getOrNull()
    }
}

/** The duration bands, shortest first, as the home names them (api/internal/handlers/search/facets.go). */
val DURATION_BANDS: List<String> = listOf("under_5m", "5m_20m", "20m_60m", "over_60m")

/** The seconds of a band, as the home's band bucket gives them; null for a band this app does not know. */
fun bandRange(band: String): JsonObject? = when (band) {
    "under_5m" -> secs(0, 300)
    "5m_20m" -> secs(300, 1200)
    "20m_60m" -> secs(1200, 3600)
    "over_60m" -> secs(3600, null)
    else -> null
}

private fun secs(gte: Int, lt: Int?) = buildJsonObject {
    put("gte", gte)
    if (lt != null) put("lt", lt)
}

/** The dates of a year, as the home's year bucket gives them (UTC). */
fun yearRange(year: String): JsonObject {
    val y = year.toIntOrNull() ?: 0
    return buildJsonObject {
        put("gte", "%04d-01-01T00:00:00.000Z".format(Locale.ROOT, y))
        put("lt", "%04d-01-01T00:00:00.000Z".format(Locale.ROOT, y + 1))
    }
}

/**
 * The facet buckets chosen: libraries, languages and speakers are lists (any of them), a year
 * and a duration band one each (the request has one date and one duration range). A year or
 * band keeps its bucket's own filter, from the facets it was chosen on.
 */
data class FacetChoice(
    val library: List<String> = emptyList(),
    val language: List<String> = emptyList(),
    val speaker: List<String> = emptyList(),
    val year: String? = null,
    val duration: String? = null,
    /** the chosen year's `date` filter, from its bucket; null: the home's range for the year */
    val date: JsonObject? = null,
    /** the chosen band's `duration_sec` filter, from its bucket; null: the home's range for the band */
    val durationSec: JsonObject? = null,
) {
    /** The chosen buckets, facet by facet in [Facet]'s order: what "Filters in use" lists. */
    val chosen: List<Pair<Facet, String>>
        get() = Facet.entries.flatMap { f -> chosenIn(f).map { f to it } }

    val count: Int get() = chosen.size
    val isEmpty: Boolean get() = count == 0

    fun chosenIn(f: Facet): List<String> = when (f) {
        Facet.Library -> library
        Facet.Language -> language
        Facet.Speaker -> speaker
        Facet.Year -> listOfNotNull(year)
        Facet.Duration -> listOfNotNull(duration)
    }

    fun has(f: Facet, value: String): Boolean = value in chosenIn(f)

    /**
     * Chooses [bucket] of [f], or takes it off when it is chosen. A list facet adds it to the
     * others; a year or a band replaces the one before, keeping the bucket's own filter.
     */
    fun toggle(f: Facet, bucket: FacetBucket): FacetChoice = toggle(f, bucket.value, bucket.filter)

    fun toggle(f: Facet, value: String, filter: JsonObject? = null): FacetChoice {
        val on = !has(f, value)
        fun List<String>.flip() = if (on) this + value else this - value
        return when (f) {
            Facet.Library -> copy(library = library.flip())
            Facet.Language -> copy(language = language.flip())
            Facet.Speaker -> copy(speaker = speaker.flip())
            Facet.Year -> if (on) copy(year = value, date = filter?.get("date") as? JsonObject) else copy(year = null, date = null)
            Facet.Duration -> if (on) copy(duration = value, durationSec = filter?.get("duration_sec") as? JsonObject) else copy(duration = null, durationSec = null)
        }
    }

    /** Takes [value] of [f] off; nothing when it is not chosen. */
    fun remove(f: Facet, value: String): FacetChoice = if (has(f, value)) toggle(f, value) else this

    /** `filters` for `POST /api/search`; null when nothing is chosen. */
    fun filters(): JsonObject? {
        if (isEmpty) return null
        return buildJsonObject {
            if (library.isNotEmpty()) put("library_id", JsonArray(library.map(::JsonPrimitive)))
            if (language.isNotEmpty()) put("language", JsonArray(language.map(::JsonPrimitive)))
            if (speaker.isNotEmpty()) put("speaker", JsonArray(speaker.map(::JsonPrimitive)))
            year?.let { put("date", date ?: yearRange(it)) }
            duration?.let { d -> (durationSec ?: bandRange(d))?.let { put("duration_sec", it) } }
        }
    }

    /**
     * What a facet's chips show: the home's buckets for [f], with each chosen value the
     * narrowed facets no longer list put first, without a count ([FacetBucket.counted]), so
     * it can be taken off where it was put on.
     */
    fun shown(facets: SearchFacets?, f: Facet): List<FacetBucket> {
        val listed = facets?.of(f).orEmpty()
        val gone = chosenIn(f).filter { v -> listed.none { it.value == v } }.map { FacetBucket(value = it, count = -1) }
        return gone + listed
    }
}

/**
 * The library names seen in the facets, kept by id: a library still chosen that the narrowed
 * facets no longer list is named by them.
 */
fun Map<String, String>.withLibraries(facets: SearchFacets?): Map<String, String> =
    this + facets?.library.orEmpty().mapNotNull { b -> b.label?.trim()?.takeIf { it.isNotEmpty() }?.let { b.value to it } }

/**
 * The did-you-mean worth showing for [q]: the answer's, unless it is blank or the query again
 * (letter case and spaces aside).
 */
fun didYouMean(q: String, answer: String?): String? {
    val d = answer?.trim().orEmpty()
    if (d.isEmpty()) return null
    val norm = { s: String -> s.trim().replace(Regex("\\s+"), " ").lowercase(Locale.ROOT) }
    return d.takeIf { norm(it) != norm(q) }
}

/** A language code's name in [locale] (`ar` → Arabic, العربية), or the code when the device has no name for it. */
fun languageName(code: String, locale: Locale): String {
    val c = code.trim()
    if (c.isEmpty()) return code
    val name = runCatching { Locale.forLanguageTag(c).getDisplayLanguage(locale) }.getOrNull().orEmpty()
    return if (name.isBlank() || name.equals(c, ignoreCase = true)) code else name
}
