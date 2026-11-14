package com.hamzalabs.maktaba.player.media

import android.content.Context
import android.os.Bundle
import android.util.Log
import androidx.annotation.MainThread
import androidx.annotation.OptIn
import androidx.core.net.toUri
import androidx.media3.cast.CastPlayer
import androidx.media3.cast.MediaItemConverter
import androidx.media3.cast.RemoteCastPlayer
import androidx.media3.common.DeviceInfo
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata as Media3Metadata
import androidx.media3.common.MimeTypes
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import com.google.android.gms.cast.CastMediaControlIntent
import com.google.android.gms.cast.MediaInfo
import com.google.android.gms.cast.MediaMetadata
import com.google.android.gms.cast.MediaQueueItem
import com.google.android.gms.cast.framework.CastOptions
import com.google.android.gms.cast.framework.OptionsProvider
import com.google.android.gms.cast.framework.SessionProvider
import com.google.android.gms.common.ConnectionResult
import com.google.android.gms.common.GoogleApiAvailability
import com.hamzalabs.maktaba.player.BuildConfig
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import org.json.JSONObject
import java.net.URI
import kotlin.math.floor
import androidx.media3.cast.Cast as Media3Cast

/*
 * Cast (FR-38; Epic 58 story 58.06): the Player casts to Maktaba's own receiver (story 58.07),
 * named by the application ID shared/cast/app.json keeps (the build's CAST_APP_ID). The phone
 * mints a cast grant on the home and hands it to the receiver in the load's custom data, never
 * in a header; the receiver then opens its own stream sessions, walks a play queue and reports
 * the progress itself, so the phone can lock or leave and the home still knows where the TV is.
 * The contract: docs/developers/contributing/casting.md.
 *
 * No application ID (no receiver registered yet): no Cast at all, no button and no Cast
 * framework started. A home older than the cast grant: Google's Default Media Receiver with the
 * session's plain signed URL, as the Player always cast (CastSender).
 */

/** Maktaba's receiver, from shared/cast/app.json by way of the build; empty: Cast is off. */
val CAST_APP_ID: String get() = BuildConfig.CAST_APP_ID

/** The Cast namespace Maktaba's receiver and its senders talk on (shared/cast/app.json). */
const val CAST_NAMESPACE = "urn:x-cast:tv.maktaba.cast"

/**
 * The Cast framework reads this class from the manifest's
 * `com.google.android.gms.cast.framework.OPTIONS_PROVIDER_CLASS_NAME`. It is asked only once
 * [castStarted] starts the framework, which it never does without [CAST_APP_ID]; the Default
 * Media Receiver stands in only so the framework never gets an empty ID.
 */
class CastOptionsProvider : OptionsProvider {
    override fun getCastOptions(context: Context): CastOptions = CastOptions.Builder()
        .setReceiverApplicationId(CAST_APP_ID.ifBlank { CastMediaControlIntent.DEFAULT_MEDIA_RECEIVER_APPLICATION_ID })
        // ending the cast brings the video back to the phone, where it was
        .setRemoteToLocalEnabled(true)
        .setStopReceiverApplicationWhenEndingSession(true)
        .build()

    override fun getAdditionalSessionProviders(context: Context): List<SessionProvider>? = null
}

// ---------- what the receiver is handed (shared/cast/load-vectors.json) ----------

/** What one cast load names: the home's addresses, the grant, the item, where, the language. */
data class CastLoadInput(
    /** The address this phone reaches the home at (the client's base URL). */
    val senderBase: String,
    /** The home's base URLs from `GET /api/discovery/home`: the LAN ones first, the relay last. */
    val discoveryUrls: List<String>,
    val grant: String,
    val videoId: String? = null,
    val queueId: String? = null,
    val queuePos: Int? = null,
    val startSec: Double = 0.0,
    val lang: String = "en",
    val apiLevel: Int = 1,
)

private val IPV4 = Regex("""^\d{1,3}(\.\d{1,3}){3}$""")

/**
 * [raw] without its trailing slash when a TV can load from it: HTTPS (the receiver page is HTTPS,
 * and its browser blocks plain-HTTP media), and not localhost, a .local name or a bare IP (no
 * certificate names those, so the TV's check would fail); else null.
 */
fun tvReachable(raw: String): String? {
    val u = runCatching { URI(raw.trim()) }.getOrNull() ?: return null
    if (!u.scheme.equals("https", ignoreCase = true)) return null
    val host = u.host?.lowercase()?.trimEnd('.') ?: return null
    if (host.isEmpty() || host == "localhost" || host.endsWith(".localhost") || host.endsWith(".local")) return null
    if (IPV4.matches(host) || host.startsWith("[") || host.contains(':')) return null
    val port = if (u.port == -1 || u.port == 443) "" else ":${u.port}"
    return "https://$host$port${u.rawPath.orEmpty()}".trimEnd('/')
}

/**
 * The load's custom data (`MediaInfo.customData`), or null when the home has no address a TV
 * can reach. `home` is the first such address of the home's own list, then this phone's;
 * `urls` keeps them in order; `queue_pos` only with a queue. The rules and their cases:
 * shared/cast/load-vectors.json, which the web and Apple senders run too.
 */
fun castData(input: CastLoadInput): JsonObject? {
    val urls = (input.discoveryUrls + input.senderBase).mapNotNull(::tvReachable).distinct()
    if (urls.isEmpty()) return null
    val start = if (input.startSec.isFinite()) floor(input.startSec).toInt().coerceAtLeast(0) else 0
    return buildJsonObject {
        put("home", urls.first())
        put("urls", buildJsonArray { urls.forEach { add(JsonPrimitive(it)) } })
        put("grant", input.grant)
        if (!input.queueId.isNullOrEmpty()) {
            put("queue_id", input.queueId)
            if (input.queuePos != null && input.queuePos >= 0) put("queue_pos", input.queuePos)
        } else if (!input.videoId.isNullOrEmpty()) {
            put("video_id", input.videoId)
        }
        put("start_sec", start)
        put("lang", if (input.lang == "ar") "ar" else "en")
        put("api_level", input.apiLevel)
    }
}

/** The extra of a [MediaItem]'s request metadata that carries its cast load's custom data. */
const val CAST_DATA_EXTRA = "tv.maktaba.cast.data"

/** [item] with the custom data its cast load hands the receiver ([CastItems] reads it). */
fun MediaItem.withCastData(data: JsonObject): MediaItem = buildUpon()
    .setRequestMetadata(
        requestMetadata.buildUpon()
            .setExtras(Bundle(requestMetadata.extras ?: Bundle()).apply { putString(CAST_DATA_EXTRA, data.toString()) })
            .build(),
    )
    .build()

/** The custom data [withCastData] put on this item, if any. */
val MediaItem.castData: JsonObject?
    get() = requestMetadata.extras?.getString(CAST_DATA_EXTRA)?.let { runCatching { Json.parseToJsonElement(it) as? JsonObject }.getOrNull() }

/** What the receiver says it plays (its media status' custom data): never the grant. */
data class ReceiverItem(val videoId: String?, val queueId: String?, val queuePos: Int?)

/** The receiver's item, read back from a [MediaItem] [CastItems] made of its media status. */
val MediaItem.receiverItem: ReceiverItem?
    get() {
        val d = castData ?: return null
        fun s(k: String) = (d[k] as? JsonPrimitive)?.contentOrNull?.takeIf { it.isNotEmpty() }
        return ReceiverItem(s("video_id") ?: mediaId.takeIf { it.isNotEmpty() }, s("queue_id"), d["queue_pos"]?.jsonPrimitive?.intOrNull)
    }

/**
 * Turns the app's items into the receiver's and back. With custom data ([withCastData]) the
 * load names the video (`contentId`) and carries the grant in `customData`, nothing else: the
 * receiver opens its own session with it. Without (a home older than the cast grant), the
 * receiver is handed the session's signed URL (`?sig=`, gap G2) and the title, as before; it
 * fetches that URL without the app's token (the home's web port lets a signed session path
 * through: story 48.12).
 *
 * Either way no header or token is copied: Media3's own converter would copy the whole
 * [MediaItem], headers included, into the receiver's custom data.
 *
 * The URL is the session's HLS master (`manifest.m3u8`) when the home transcodes or remuxes, or
 * the file itself (`/stream/direct/{video}`) for direct play: the home opens a phone's session
 * as HLS, never DASH (DASH is opt-in per session), so the receiver never needs the DASH paths.
 */
@OptIn(UnstableApi::class)
class CastItems : MediaItemConverter {
    override fun toMediaQueueItem(mediaItem: MediaItem): MediaQueueItem {
        val meta = MediaMetadata(MediaMetadata.MEDIA_TYPE_MOVIE)
        mediaItem.mediaMetadata.title?.let { meta.putString(MediaMetadata.KEY_TITLE, it.toString()) }
        val data = mediaItem.castData
        val info = if (data != null) {
            val id = mediaItem.mediaId.takeIf { it.isNotEmpty() && it != MediaItem.DEFAULT_MEDIA_ID }
                ?: requireNotNull((data["video_id"] ?: data["queue_id"])?.jsonPrimitive?.contentOrNull) { "a cast item names its video" }
            MediaInfo.Builder(id)
                .setStreamType(MediaInfo.STREAM_TYPE_BUFFERED)
                .setContentType(MimeTypes.VIDEO_MP4)
                .setMetadata(meta)
                .setCustomData(JSONObject(data.toString()))
                .build()
        } else {
            val local = requireNotNull(mediaItem.localConfiguration) { "a cast item needs a URL" }
            val url = local.uri.toString()
            MediaInfo.Builder(url)
                .setContentUrl(url)
                .setStreamType(MediaInfo.STREAM_TYPE_BUFFERED)
                .setContentType(local.mimeType ?: MimeTypes.VIDEO_MP4)
                .setMetadata(meta)
                .build()
        }
        return MediaQueueItem.Builder(info).build()
    }

    override fun toMediaItem(mediaQueueItem: MediaQueueItem): MediaItem {
        val info = requireNotNull(mediaQueueItem.media) { "a cast item has its media" }
        val url = info.contentUrl ?: info.contentId
        val b = MediaItem.Builder()
            .setUri(url.toUri())
            .setMimeType(info.contentType)
            .setMediaMetadata(Media3Metadata.Builder().setTitle(info.metadata?.getString(MediaMetadata.KEY_TITLE)).build())
        val custom = info.customData
        if (custom != null && info.contentUrl == null) {
            // what the receiver plays (its media status): the video, the queue and where in it;
            // the grant and the addresses are never kept on this side
            val public = buildJsonObject {
                for (k in listOf("video_id", "queue_id")) custom.optString(k).takeIf { it.isNotEmpty() }?.let { put(k, it) }
                if (custom.has("queue_pos")) put("queue_pos", custom.optInt("queue_pos"))
            }
            b.setMediaId((public["video_id"] as? JsonPrimitive)?.contentOrNull ?: info.contentId)
            b.setRequestMetadata(MediaItem.RequestMetadata.Builder().setExtras(Bundle().apply { putString(CAST_DATA_EXTRA, public.toString()) }).build())
        }
        return b.build()
    }
}

/** Seeks on the receiver as on the phone: ±10 s (FR-36). */
const val SEEK_INCREMENT_MS = 10_000L

/**
 * The player the app drives: [local] on the phone, which a Cast session takes over (Media3's
 * [CastPlayer] hands over to the receiver through [transfer], and back when the cast ends).
 * [local] alone where Cast cannot run: no receiver application ID ([appId] empty), no Google
 * Play services (an emulator without them, the JVM tests), or the Cast framework failed to start.
 */
@OptIn(UnstableApi::class)
@MainThread
fun castable(context: Context, local: ExoPlayer, transfer: CastPlayer.TransferCallback, appId: String = CAST_APP_ID): Player {
    if (!castStarted(context, appId)) return local
    return try {
        val remote = RemoteCastPlayer.Builder(context)
            .setMediaItemConverter(CastItems())
            .setSeekBackIncrementMs(SEEK_INCREMENT_MS)
            .setSeekForwardIncrementMs(SEEK_INCREMENT_MS)
            .build()
        CastPlayer.Builder(context).setLocalPlayer(local).setRemotePlayer(remote).setTransferCallback(transfer).build()
    } catch (e: RuntimeException) {
        Log.w("Maktaba", "cast: not available (${e.javaClass.simpleName})")
        local
    }
}

/** True while [player] plays on a receiver, not on this device. */
fun isRemote(player: Player?): Boolean = player?.deviceInfo?.playbackType == DeviceInfo.PLAYBACK_TYPE_REMOTE

/**
 * Starts the Cast framework once (Media3's [Media3Cast] singleton, which loads the framework's
 * context in the background); false where it cannot run, and always without [appId]: then no
 * Cast context is ever started.
 */
@OptIn(UnstableApi::class)
@MainThread
fun castStarted(context: Context, appId: String = CAST_APP_ID): Boolean {
    if (appId.isBlank()) return false
    val app = context.applicationContext
    val services = runCatching { GoogleApiAvailability.getInstance().isGooglePlayServicesAvailable(app) }.getOrDefault(ConnectionResult.SERVICE_MISSING)
    if (services != ConnectionResult.SUCCESS) return false
    return try {
        val cast = Media3Cast.getSingletonInstance(app)
        if (cast.needsInitialization()) cast.initialize()
        cast.castContextLoadFailure == null
    } catch (e: RuntimeException) {
        Log.w("Maktaba", "cast: the framework did not start (${e.javaClass.simpleName})")
        false
    }
}
