package com.hamzalabs.maktaba.core.guest

import com.hamzalabs.maktaba.core.session.KeyValueStore
import com.hamzalabs.maktaba.core.session.SecretBox
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull
import java.net.URI
import java.time.Instant
import java.time.OffsetDateTime
import java.util.Base64

// Epic 50 story 50.27 §2: Watch a broadcast on Android and Android TV, as MaktabaKit's Guest.swift
// does it on Apple (#1400). A guest watches a home's published channels with no Maktaba or home
// account: an invite (a link, or the publisher's handle and an `MK-` short code) or a phone's
// pairing gives the device a guest pass, kept sealed with this device's Keystore key; the edge
// (`watch.maktaba.tv`) answers the lineup and a play with the viewer token, and the channel plays
// from `live.maktaba.tv` with the newest token put in each request's path ([GuestLive]). The edge
// enforces every right (the credential, its channels, the limits); the app only shows what it
// answers. Plain Kotlin: the Android core, the TV app and their JVM tests share it.

/** The edge the guest space talks to. */
object GuestEdge {
    const val HOST = "watch.maktaba.tv"
    const val PRODUCTION = "https://$HOST"
}

// region the pass

/** A saved broadcast: one publisher's credential on this device. */
@Serializable
data class GuestPass(
    /** The publisher's id (the home's server id, as in its watch address). */
    val publisher: String,
    /** "{publisher}" as the lineup names it ("" until the lineup is read). */
    val name: String = "",
    val credential: String,
    val kind: Kind = Kind.Pass,
    /** The channels the credential opens (an invite's; empty: the publisher's open ones). */
    val channels: List<String> = emptyList(),
    /** When the credential ends, in seconds since 1970. */
    val expiresAt: Long,
    val savedAt: Long = 0,
) {
    @Serializable
    enum class Kind {
        /** A guest pass from an invite (`mkbg1.`, 30 days, renewed while its invite lasts). */
        @SerialName("pass") Pass,

        /** An open session (`mkbo1.`, 12 hours), from a phone's pairing. */
        @SerialName("open") Open,
    }

    /** What the guest reads for this broadcast: its name, or its handle before the lineup named it. */
    val label: String get() = name.ifBlank { publisher }

    // the credential is a secret: never in a log line or a crash report
    override fun toString() = "GuestPass(publisher=$publisher, kind=$kind, channels=$channels, expiresAt=$expiresAt)"

    companion object {
        /**
         * The expiry a credential says it has (its payload's `exp`), unverified: the edge checks the
         * signature; the app only needs to know when to forget it.
         */
        fun claimedExpiry(credential: String): Long? {
            val parts = credential.split('.')
            if (parts.size != 3) return null
            return runCatching {
                val json = String(Base64.getUrlDecoder().decode(parts[1].trimEnd('=')))
                Json.parseToJsonElement(json).let { it as JsonObject }["exp"]?.jsonPrimitive?.let { p ->
                    p.longOrNull ?: p.contentOrNull?.toDoubleOrNull()?.toLong()
                }
            }.getOrNull()
        }
    }
}

/**
 * The saved broadcasts: one entry (`broadcasts`) of [store] holding the list, newest first, sealed
 * by [box] (the Android Keystore's key, which never leaves the device; the apps' backup rules keep
 * the file out of backups and device transfers). A pass counts as one of its invite's devices, so
 * it must never move to another device: a value that no longer opens reads as no broadcasts. An
 * expired pass is dropped the next time the list is read.
 */
class GuestPassStore(
    private val store: KeyValueStore,
    private val box: SecretBox,
    private val now: () -> Long = { System.currentTimeMillis() / 1000 },
) {
    private val lock = Any()

    /** The saved broadcasts still valid, the most recently saved first. */
    fun list(): List<GuestPass> = synchronized(lock) {
        val all = read()
        val live = all.filter { it.expiresAt > now() }
        if (live.size != all.size) write(live)
        live
    }

    fun pass(publisher: String): GuestPass? = list().firstOrNull { it.publisher == publisher }

    /**
     * Saves [pass], replacing the publisher's earlier one, at the top of the list. A name the
     * earlier one had is kept when the new one has none yet.
     */
    fun save(pass: GuestPass) = synchronized(lock) {
        val all = read().filter { it.expiresAt > now() }
        val old = all.firstOrNull { it.publisher == pass.publisher }
        val p = pass.copy(name = pass.name.ifEmpty { old?.name.orEmpty() }, savedAt = now())
        write(listOf(p) + all.filter { it.publisher != pass.publisher })
    }

    /** The edge renewed the pass (`X-Maktaba-Pass` on a play): kept with its new expiry. */
    fun renew(publisher: String, credential: String) = synchronized(lock) {
        val all = read()
        if (all.none { it.publisher == publisher }) return@synchronized
        write(all.map { if (it.publisher == publisher) it.copy(credential = credential, expiresAt = GuestPass.claimedExpiry(credential) ?: it.expiresAt) else it })
    }

    /** The lineup's name for the publisher. */
    fun name(publisher: String, name: String) = synchronized(lock) {
        val all = read()
        if (name.isEmpty() || all.none { it.publisher == publisher && it.name != name }) return@synchronized
        write(all.map { if (it.publisher == publisher) it.copy(name = name) else it })
    }

    /** Remove: the pass leaves the device (the edge keeps counting the invite's device use). */
    fun remove(publisher: String) = synchronized(lock) { write(read().filter { it.publisher != publisher }) }

    private fun read(): List<GuestPass> {
        val sealed = store.get(KEY) ?: return emptyList()
        return runCatching {
            json.decodeFromString(ListSerializer(GuestPass.serializer()), String(box.open(Base64.getDecoder().decode(sealed))))
        }.getOrDefault(emptyList())
    }

    private fun write(all: List<GuestPass>) {
        if (all.isEmpty()) {
            store.put(KEY, null)
            return
        }
        val text = json.encodeToString(ListSerializer(GuestPass.serializer()), all)
        store.put(KEY, Base64.getEncoder().encodeToString(box.seal(text.toByteArray())))
    }

    companion object {
        const val KEY = "broadcasts"
        private val json = Json { ignoreUnknownKeys = true }
    }
}

// endregion

// region what the guest sees

/**
 * The states a guest can see (story 50.27 §3), from an edge answer: the same mapping as the watch
 * pages' (`site/states.ts`) and MaktabaKit's `GuestViewerState`, all tested against
 * `shared/broadcast/viewer-states.json`.
 */
data class GuestViewerState(
    val state: Name,
    /** The message's key (`broadcast.tuning`), "" for a channel playing with nothing to say. */
    val key: String,
    /** The player plays (live, tuning while the channel wakes, offline on the slate). */
    val plays: Boolean,
    /** A Try again action helps. */
    val retry: Boolean,
) {
    @Suppress("EnumEntryName")
    enum class Name { live, tuning, offline, off_air, full, busy, geoBlocked, ended, inviteUsed, inviteExpired, inviteRequired, revoked, verify, dailyLimit, error }

    companion object {
        val Live = GuestViewerState(Name.live, "", plays = true, retry = false)

        fun of(status: Int, channelState: String? = null, error: String? = null): GuestViewerState {
            fun s(n: Name, k: String, p: Boolean, r: Boolean) = GuestViewerState(n, k, p, r)
            if (status in 200..299) {
                return when (channelState) {
                    "waiting" -> s(Name.tuning, "broadcast.tuning", true, false)
                    "offline_slate" -> s(Name.offline, "broadcast.homeOffline", true, false)
                    "off_air" -> s(Name.off_air, "broadcast.off", false, true)
                    "revoked" -> s(Name.ended, "broadcast.ended", false, false)
                    else -> Live
                }
            }
            if (status == 451 || error == "geo_blocked") return s(Name.geoBlocked, "broadcast.geoBlocked", false, false)
            return when (error.orEmpty()) {
                "broadcast_full" -> s(Name.full, "broadcast.full", false, true)
                "broadcast_wait" -> s(Name.full, "broadcast.waiting", false, true)
                "broadcast_ip_limit" -> s(Name.full, "broadcast.ipLimit", false, true)
                "rate_limited" -> s(Name.busy, "broadcast.tooManyTries", false, true)
                "broadcast_daily_limit" -> s(Name.dailyLimit, "broadcast.dailyLimit", false, false)
                "invite_used" -> s(Name.inviteUsed, "broadcast.invite.used", false, false)
                "invite_expired" -> s(Name.inviteExpired, "broadcast.invite.expired", false, false)
                "invite_revoked", "pass_revoked", "revoked", "session_ended" -> s(Name.revoked, "broadcast.revoked", false, true)
                "invite_required", "not_invited" -> s(Name.inviteRequired, "broadcast.inviteRequired", false, false)
                "turnstile_required" -> s(Name.verify, "site.verify", false, false)
                "broadcast_off", "channel_unknown" -> s(Name.ended, "broadcast.ended", false, false)
                else -> s(Name.error, "site.error", false, true)
            }
        }
    }
}

/** A refusal from the edge: its status and `error` code (status 0: no answer at all). */
class GuestError(val status: Int, val code: String) : Exception("the broadcast edge answered $status $code") {
    val state: GuestViewerState get() = GuestViewerState.of(status, error = code)

    /**
     * The key of the message the guest reads: the state's, or for a code the states do not name (a
     * wrong short code, a bad invite link, no network) the invite's own.
     */
    val messageKey: String
        get() = when (code) {
            "code_unknown", "bad_token", "bad_signature", "unknown_key", "bad_request" -> "broadcast.invite.invalid"
            "network" -> "guest.network"
            else -> state.key
        }

    override fun equals(other: Any?) = other is GuestError && other.status == status && other.code == code
    override fun hashCode() = status * 31 + code.hashCode()

    companion object {
        /** No answer (no network, a timeout). */
        val Offline get() = GuestError(0, "network")
        val BadAnswer get() = GuestError(502, "bad_answer")
    }
}

// endregion

// region the edge's answers

/** A time as the edge writes it (RFC 3339), null when it is not one. */
internal fun edgeTime(text: String?): Instant? = text?.let {
    runCatching { OffsetDateTime.parse(it).toInstant() }.getOrNull() ?: runCatching { Instant.parse(it) }.getOrNull()
}

/** A programme on a channel's guide; [attribution] for a CC BY one (50.23). */
@Serializable
data class GuestProgramme(
    @SerialName("start") val startText: String? = null,
    @SerialName("end") val endText: String? = null,
    val title: String = "",
    val attribution: Attribution? = null,
) {
    @Serializable
    data class Attribution(val title: String? = null, val by: String? = null, val licence: String? = null, val url: String? = null)

    val start: Instant? get() = edgeTime(startText)
    val end: Instant? get() = edgeTime(endText)
}

/** `GET /api/v1/lineup`: the channels the credential opens, with now and next. */
@Serializable
data class GuestLineup(val publisher: Publisher, val channels: List<Channel> = emptyList()) {
    @Serializable
    data class Publisher(val id: String, val name: String = "")

    @Serializable
    data class Channel(
        val id: String,
        val name: String = "",
        val number: Int = 0,
        /** live, waiting, offline_slate, off_air, revoked. */
        val state: String = "live",
        val now: GuestProgramme? = null,
        val next: GuestProgramme? = null,
    )
}

/** `POST /api/v1/play`: a session and its master playlist on the edge. */
data class GuestPlay(
    val session: String,
    val beatKey: String,
    val masterUrl: String,
    val refreshAfter: Int,
    val state: GuestViewerState,
    /** The pass renewed on the way (`X-Maktaba-Pass`), to keep. */
    val renewedPass: String?,
)

/** `POST /api/v1/play/<session>/beat`: the channel's state, and a new playlist address when the viewer token was refreshed. */
data class GuestBeat(val state: GuestViewerState, val masterUrl: String?, val refreshAfter: Int)

/** `POST /api/v1/pair`: a TV's pairing (10 minutes). The QR code carries [url] (the nonce only); [pollKey] stays on the TV. */
@Serializable
data class GuestPairing(
    val nonce: String,
    @SerialName("poll_key") val pollKey: String,
    /** Shown beside the QR code; the phone's page shows it too. */
    val code: String,
    val url: String,
    @SerialName("expires_at") val expiresAtText: String? = null,
) {
    override fun toString() = "GuestPairing(nonce=$nonce, code=$code)"
}

/** One answer of the TV's long-poll. */
sealed interface GuestPairPoll {
    /** Nothing yet: ask again. */
    data object Waiting : GuestPairPoll

    /** The phone sent a credential. */
    data class Paired(val pass: GuestPass) : GuestPairPoll

    /** The pairing ended (10 minutes, or taken): show a new code. */
    data object Gone : GuestPairPoll
}

// endregion

/** What a guest typed or opened: an invite link (`…/i/<token>`), a bare invite, a short code, a handle. */
object GuestInput {
    /** The longest invite token the edge mints, and so the longest the app reads. */
    const val MAX_TOKEN_BYTES = 4096

    /**
     * The invite token in a link or as typed; null when it is not one. A pasted link is read as
     * [inviteInLink] reads an opened one: only the watch address's (or `maktaba://watch`'s) `/i/<token>`.
     */
    fun invite(text: String): String? {
        val v = text.trim()
        if ("://" in v) return inviteInLink(v)
        return token(v)
    }

    /**
     * The invite in a link the app was opened with (`https://watch.maktaba.tv/i/<token>`, or
     * `maktaba://watch/i/<token>`), null for any other link: another host or scheme, user info, a
     * port, or a path other than exactly `/i/<token>`.
     */
    fun inviteInLink(link: String?): String? {
        if (link == null || link.length > MAX_TOKEN_BYTES + 64) return null
        val u = runCatching { URI(link) }.getOrNull() ?: return null
        val scheme = u.scheme?.lowercase()
        val host = u.host?.lowercase()
        val isWatch = (scheme == "https" && host == GuestEdge.HOST) || (scheme == "maktaba" && host == "watch")
        if (!isWatch || u.rawUserInfo != null || u.port != -1) return null
        // "", "i", "<token>"
        val parts = (u.path ?: return null).split('/')
        if (parts.size != 3 || parts[0].isNotEmpty() || parts[1] != "i") return null
        return token(parts[2])
    }

    /** An invite token as the edge mints it: `mkbn1.<base64url payload>.<base64url signature>`, at most 4 KiB. */
    fun token(v: String): String? {
        if (v.toByteArray().size > MAX_TOKEN_BYTES) return null
        val parts = v.split('.')
        fun b64url(s: String) = s.isNotEmpty() && s.all { it in 'A'..'Z' || it in 'a'..'z' || it in '0'..'9' || it == '-' || it == '_' }
        return v.takeIf { parts.size == 3 && parts[0] == "mkbn1" && b64url(parts[1]) && b64url(parts[2]) }
    }

    /**
     * A short code as the edge wants it (`MK-` and four letters or digits), from what was typed
     * ("mk 7q2x", "7Q2X"); null when it cannot be one.
     */
    fun code(text: String): String? {
        var s = text.uppercase().filter { it in 'A'..'Z' || it in '0'..'9' }
        if (s.startsWith("MK") && s.length == 6) s = s.drop(2)
        return if (s.length == 4) "MK-$s" else null
    }

    /** The publisher's handle as typed (its watch address works too: the part after the language). */
    fun publisher(text: String): String? {
        var v = text.trim()
        val u = if ("://" in v) runCatching { URI(v) }.getOrNull() else null
        if (u?.host != null) {
            val parts = u.path.orEmpty().split('/').filter { it.isNotEmpty() }
            v = (if (parts.firstOrNull() in setOf("ar", "en")) parts.drop(1) else parts).firstOrNull().orEmpty()
        }
        return v.takeIf { it.length in 1..64 && it.all { c -> c in 'A'..'Z' || c in 'a'..'z' || c in '0'..'9' || c == '-' } }
    }
}

// region the token in the path

/**
 * The edge address a guest's channel plays from, holding the newest viewer token: made from a
 * play's `master_url` (`https://live.maktaba.tv/<token>/<channel>/master.m3u8`). The player asks
 * [rewrite] for each request, so a playlist or segment address resolved against an older token's
 * master goes out with the newest one, and the 10-minute token refreshes without restarting the
 * picture. Only the channel's own paths on its edge are allowed.
 */
class GuestLive private constructor(val channel: String, private val origin: String, token: String) {
    @Volatile private var token: String = token

    val currentToken: String get() = token

    /** The master playlist with the newest token. */
    val master: String get() = "$origin/$token/$channel/master.m3u8"

    /** A new viewer token from the heartbeat (the same channel's master playlist); false for an address of another channel or edge. */
    fun update(master: String): Boolean {
        val next = of(master) ?: return false
        if (next.channel != channel || next.origin != origin) return false
        token = next.token
        return true
    }

    /**
     * [url] with the newest token, or null when it is not a file of this channel on this edge
     * (the player then refuses it).
     */
    fun rewrite(url: String): String? {
        val u = runCatching { URI(url) }.getOrNull() ?: return null
        if (originOf(u) != origin || u.rawUserInfo != null) return null
        val parts = u.rawPath.orEmpty().split('/')
        // "", "<token>", "<channel>", rest…
        if (parts.size < 4 || parts[0].isNotEmpty() || parts[2] != channel || parts.drop(3).any { it.isEmpty() || it == ".." || it == "." }) return null
        val rest = parts.drop(3).joinToString("/")
        return "$origin/$token/$channel/$rest" + (u.rawQuery?.let { "?$it" } ?: "")
    }

    override fun toString() = "GuestLive(channel=$channel, origin=$origin)"

    companion object {
        internal val LOOPBACK = setOf("127.0.0.1", "localhost")

        fun of(master: String): GuestLive? {
            val u = runCatching { URI(master) }.getOrNull() ?: return null
            val origin = originOf(u) ?: return null
            val parts = u.rawPath.orEmpty().split('/')
            if (parts.size != 4 || parts[0].isNotEmpty() || parts[3] != "master.m3u8" || parts[1].isEmpty() || parts[2].isEmpty()) return null
            return GuestLive(parts[2], origin, parts[1])
        }

        private fun originOf(u: URI): String? {
            val scheme = u.scheme?.lowercase() ?: return null
            val host = u.host?.lowercase() ?: return null
            // https, or plain http only to this device (the tests' mock edge)
            if (scheme != "https" && !(scheme == "http" && host in LOOPBACK)) return null
            return "$scheme://$host" + (if (u.port != -1) ":${u.port}" else "")
        }
    }
}

// endregion
