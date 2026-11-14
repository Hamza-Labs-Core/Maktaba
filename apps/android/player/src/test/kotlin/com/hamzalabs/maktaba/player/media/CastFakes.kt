@file:OptIn(UnstableApi::class)

package com.hamzalabs.maktaba.player.media

import androidx.annotation.OptIn
import androidx.media3.cast.CastPlayer
import androidx.media3.common.DeviceInfo
import androidx.media3.common.ForwardingPlayer
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import com.google.android.gms.cast.MediaInfo
import com.google.android.gms.cast.MediaQueueItem
import org.json.JSONObject
import java.io.File

/*
 * There is no Cast emulator (story 58.06's honest note): the tests drive a fake receiver in
 * place of Media3's CastPlayer, and a fake Cast session in place of Google's.
 */

/**
 * Media3's `CastPlayer` as the tests need it: the phone's player until [startCasting], then a
 * receiver at [remoteAtMs], playing. The handover goes through the app's own
 * [CastPlayer.TransferCallback], as Media3's does; [handed] is what the receiver was given,
 * through the app's converter. [receiverPlays] is the receiver's media status: what it says it
 * plays (Maktaba's receiver reports `video_id`, `queue_id` and `queue_pos`, never the grant).
 */
class FakeCastPlayer(private val local: ExoPlayer, private val transfer: CastPlayer.TransferCallback) : ForwardingPlayer(local) {
    private var casting = false
    var remoteAtMs = 0L
    val handed = mutableListOf<MediaQueueItem>()
    private var status: MediaQueueItem? = null
    private val listeners = mutableListOf<Player.Listener>()

    /** The person casts with the phone at [atMs]: Media3 hands the item and the position over. */
    fun startCasting(atMs: Long) {
        local.seekTo(atMs)
        remoteAtMs = atMs
        casting = true
        transfer.transferState(local, this)
        // Media3 stops the player it left
        local.stop()
        listeners.toList().forEach {
            it.onDeviceInfoChanged(deviceInfo)
            it.onIsPlayingChanged(true)
        }
    }

    /** The receiver's media status: it plays [videoId] (at [queuePos] of [queueId]) at [atMs]. */
    fun receiverPlays(videoId: String, atMs: Long, queueId: String? = null, queuePos: Int? = null, title: String? = null) {
        remoteAtMs = atMs
        val public = JSONObject().put("video_id", videoId).put("lang", "en")
        if (queueId != null) public.put("queue_id", queueId)
        if (queuePos != null) public.put("queue_pos", queuePos)
        val meta = com.google.android.gms.cast.MediaMetadata(com.google.android.gms.cast.MediaMetadata.MEDIA_TYPE_MOVIE)
        title?.let { meta.putString(com.google.android.gms.cast.MediaMetadata.KEY_TITLE, it) }
        status = MediaQueueItem.Builder(
            MediaInfo.Builder(videoId).setStreamType(MediaInfo.STREAM_TYPE_BUFFERED).setContentType("video/mp4")
                .setMetadata(meta).setCustomData(public).build(),
        ).build()
        listeners.toList().forEach { it.onMediaItemTransition(currentMediaItem, Player.MEDIA_ITEM_TRANSITION_REASON_AUTO) }
    }

    /** The person ends the cast: the video comes back to the phone. */
    fun stopCasting() {
        transfer.transferState(this, local)
        casting = false
        // Media3 prepares the player it moves to
        local.prepare()
        status = null
        listeners.toList().forEach {
            it.onDeviceInfoChanged(deviceInfo)
        }
    }

    override fun addListener(listener: Player.Listener) {
        listeners += listener
        super.addListener(listener)
    }

    override fun removeListener(listener: Player.Listener) {
        listeners -= listener
        super.removeListener(listener)
    }

    override fun getDeviceInfo(): DeviceInfo =
        if (casting) DeviceInfo.Builder(DeviceInfo.PLAYBACK_TYPE_REMOTE).build() else super.getDeviceInfo()

    override fun isPlaying(): Boolean = if (casting) true else super.isPlaying()
    override fun getPlayWhenReady(): Boolean = if (casting) true else super.getPlayWhenReady()
    override fun getPlaybackState(): Int = if (casting) Player.STATE_READY else super.getPlaybackState()
    override fun getCurrentPosition(): Long = if (casting) remoteAtMs else super.getCurrentPosition()
    override fun getCurrentMediaItem(): MediaItem? =
        if (casting) (status ?: handed.lastOrNull())?.let(CastItems()::toMediaItem) else super.getCurrentMediaItem()

    override fun setMediaItem(mediaItem: MediaItem, startPositionMs: Long) {
        if (casting) {
            handed += CastItems().toMediaQueueItem(mediaItem)
            remoteAtMs = startPositionMs
        } else {
            super.setMediaItem(mediaItem, startPositionMs)
        }
    }

    /** How many times it was told to stop (the receiver stops playing), and whether it was let go of. */
    var stops = 0
        private set
    var released = false
        private set

    override fun stop() {
        stops++
        if (!casting) super.stop()
    }

    override fun release() {
        released = true
        // the phone's player is the playback's own to release
    }

    override fun prepare() { if (!casting) super.prepare() }
    override fun setPlayWhenReady(playWhenReady: Boolean) { if (!casting) super.setPlayWhenReady(playWhenReady) }
}

/** Google's Cast session as the sender sees it: [sent] the namespace messages, [receiver] the app the next session launches. */
class FakeCastChannel : CastChannel {
    override var device: String? = null
    val sent = mutableListOf<String>()
    var receiver: String? = null
    var ends = 0
    var sender: CastSender? = null

    /** A session starts on the TV called [name]. */
    fun connect(name: String = "Living Room TV") {
        device = name
        sender?.connected(name)
    }

    override fun send(message: String) {
        sent += message
    }

    override fun end() {
        ends++
        device = null
        sender?.ended()
    }

    override fun useReceiver(appId: String) {
        receiver = appId
    }

    override fun listen(sender: CastSender) {
        this.sender = sender
    }
}

/** A file of the repository's shared/ directory, found from the test's working directory up. */
fun sharedFile(path: String): File {
    var dir: File? = File("").absoluteFile
    while (dir != null) {
        val f = File(dir, "shared/$path")
        if (f.isFile) return f
        dir = dir.parentFile
    }
    error("shared/$path not found above ${File("").absolutePath}")
}
