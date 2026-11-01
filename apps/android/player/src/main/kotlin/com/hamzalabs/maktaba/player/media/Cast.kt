package com.hamzalabs.maktaba.player.media

import android.content.Context
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
import androidx.media3.cast.Cast as Media3Cast

/**
 * Cast (FR-38, open question 3: yes, LAN only): Google's Default Media Receiver, which needs no
 * app registration. The Cast framework reads this class from the manifest's
 * `com.google.android.gms.cast.framework.OPTIONS_PROVIDER_CLASS_NAME`.
 */
class CastOptionsProvider : OptionsProvider {
    override fun getCastOptions(context: Context): CastOptions = CastOptions.Builder()
        .setReceiverApplicationId(CastMediaControlIntent.DEFAULT_MEDIA_RECEIVER_APPLICATION_ID)
        // ending the cast brings the video back to the phone, where it was
        .setRemoteToLocalEnabled(true)
        .setStopReceiverApplicationWhenEndingSession(true)
        .build()

    override fun getAdditionalSessionProviders(context: Context): List<SessionProvider>? = null
}

/**
 * What the receiver is handed: the stream session's signed URL (`?sig=`, gap G2) and the title,
 * nothing else. The receiver fetches the URL itself, without the app's token (the home's web
 * port lets a signed session path through: story 48.12), so no header or token is put in the
 * item (Media3's own converter would copy the whole [MediaItem], headers included, into the
 * receiver's custom data).
 *
 * The URL is the session's HLS master (`manifest.m3u8`) when the home transcodes or remuxes, or
 * the file itself (`/stream/direct/{video}`) for direct play: the home opens a phone's session
 * as HLS, never DASH (DASH is opt-in per session), so the receiver never needs the DASH paths.
 */
@OptIn(UnstableApi::class)
class CastItems : MediaItemConverter {
    override fun toMediaQueueItem(mediaItem: MediaItem): MediaQueueItem {
        val local = requireNotNull(mediaItem.localConfiguration) { "a cast item needs a URL" }
        val url = local.uri.toString()
        val meta = MediaMetadata(MediaMetadata.MEDIA_TYPE_MOVIE)
        mediaItem.mediaMetadata.title?.let { meta.putString(MediaMetadata.KEY_TITLE, it.toString()) }
        val info = MediaInfo.Builder(url)
            .setContentUrl(url)
            .setStreamType(MediaInfo.STREAM_TYPE_BUFFERED)
            .setContentType(local.mimeType ?: MimeTypes.VIDEO_MP4)
            .setMetadata(meta)
            .build()
        return MediaQueueItem.Builder(info).build()
    }

    override fun toMediaItem(mediaQueueItem: MediaQueueItem): MediaItem {
        val info = requireNotNull(mediaQueueItem.media) { "a cast item has its media" }
        val url = info.contentUrl ?: info.contentId
        return MediaItem.Builder()
            .setUri(url.toUri())
            .setMimeType(info.contentType)
            .setMediaMetadata(Media3Metadata.Builder().setTitle(info.metadata?.getString(MediaMetadata.KEY_TITLE)).build())
            .build()
    }
}

/** Seeks on the receiver as on the phone: ±10 s (FR-36). */
const val SEEK_INCREMENT_MS = 10_000L

/**
 * The player the app drives: [local] on the phone, which a Cast session takes over (Media3's
 * [CastPlayer] moves the item and the position to the receiver, and back when the cast ends).
 * [local] alone where Cast cannot run: no Google Play services (an emulator without them, the
 * JVM tests), or the Cast framework failed to start.
 */
@OptIn(UnstableApi::class)
@MainThread
fun castable(context: Context, local: ExoPlayer): Player {
    if (!castStarted(context)) return local
    return try {
        val remote = RemoteCastPlayer.Builder(context)
            .setMediaItemConverter(CastItems())
            .setSeekBackIncrementMs(SEEK_INCREMENT_MS)
            .setSeekForwardIncrementMs(SEEK_INCREMENT_MS)
            .build()
        CastPlayer.Builder(context).setLocalPlayer(local).setRemotePlayer(remote).build()
    } catch (e: RuntimeException) {
        Log.w("Maktaba", "cast: not available (${e.javaClass.simpleName})")
        local
    }
}

/** True while [player] plays on a receiver, not on this device. */
fun isRemote(player: Player?): Boolean = player?.deviceInfo?.playbackType == DeviceInfo.PLAYBACK_TYPE_REMOTE

/**
 * Starts the Cast framework once (Media3's [Media3Cast] singleton, which loads the framework's
 * context in the background); false where it cannot run.
 */
@OptIn(UnstableApi::class)
@MainThread
fun castStarted(context: Context): Boolean {
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
