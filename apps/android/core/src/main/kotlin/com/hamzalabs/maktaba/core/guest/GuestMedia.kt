@file:OptIn(UnstableApi::class)

package com.hamzalabs.maktaba.core.guest

import android.content.Context
import android.net.Uri
import android.os.Handler
import android.os.Looper
import android.view.ViewGroup
import androidx.annotation.OptIn
import androidx.compose.foundation.background
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.viewinterop.AndroidView
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.DataSourceException
import androidx.media3.datasource.DataSpec
import androidx.media3.datasource.ResolvingDataSource
import androidx.media3.datasource.TransferListener
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.hls.HlsMediaSource
import androidx.media3.ui.AspectRatioFrameLayout
import androidx.media3.ui.PlayerView
import com.hamzalabs.maktaba.core.diagnostics.AppLog
import okhttp3.OkHttpClient
import java.io.EOFException
import java.io.IOException

/**
 * A data source that never takes a cut-short body for a whole one. A segment whose connection
 * ends before the length its answer declared (`Content-Length`, or a range's), or a transport
 * stream segment (`.ts`) with no declared length that ends partway through a 188-byte packet, is
 * a load error, so the player loads it again instead of playing a broken picture. The packet rule
 * holds only for a body whose first packets start with the sync byte (0x47): an AES-128 segment
 * (`EXT-X-KEY`) reaches this source still encrypted, padded to 16 bytes, not to packets. OkHttp already
 * fails a body shorter than its `Content-Length`; this keeps the rule whatever the stack below
 * does, and covers a body that ends with its connection.
 */
class WholeBodyDataSource(private val upstream: DataSource) : DataSource {
    private var expected = C.LENGTH_UNSET.toLong()
    private var read = 0L
    private var packets = false

    override fun addTransferListener(transferListener: TransferListener) = upstream.addTransferListener(transferListener)

    override fun open(dataSpec: DataSpec): Long {
        read = 0
        val length = upstream.open(dataSpec)
        expected = length
        packets = length == C.LENGTH_UNSET.toLong() && dataSpec.position == 0L && dataSpec.uri.path.orEmpty().endsWith(".ts")
        return length
    }

    override fun read(buffer: ByteArray, offset: Int, length: Int): Int {
        val n = upstream.read(buffer, offset, length)
        if (n == C.RESULT_END_OF_INPUT) {
            if (expected != C.LENGTH_UNSET.toLong() && read < expected) throw cut("ended after $read of $expected bytes")
            if (packets && read % TS_PACKET != 0L) throw cut("ended partway through a transport stream packet ($read bytes)")
        } else if (n > 0) {
            if (packets) checkSync(buffer, offset, n)
            read += n
        }
        return n
    }

    /** Clear transport stream starts each packet with 0x47; anything else (an encrypted segment) has no packets to end on. */
    private fun checkSync(buffer: ByteArray, offset: Int, n: Int) {
        val limit = SYNC_PACKETS * TS_PACKET
        if (read >= limit) return
        var at = (read + TS_PACKET - 1) / TS_PACKET * TS_PACKET
        while (at < read + n && at < limit) {
            if (buffer[offset + (at - read).toInt()] != SYNC) {
                packets = false
                return
            }
            at += TS_PACKET
        }
    }

    private fun cut(why: String): DataSourceException {
        AppLog.info("stream body cut short: $why")
        return DataSourceException(EOFException("the answer $why"), PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_FAILED)
    }

    override fun getUri(): Uri? = upstream.uri
    override fun getResponseHeaders(): Map<String, List<String>> = upstream.responseHeaders
    override fun close() = upstream.close()

    class Factory(private val upstream: DataSource.Factory) : DataSource.Factory {
        override fun createDataSource(): DataSource = WholeBodyDataSource(upstream.createDataSource())
    }

    companion object {
        const val TS_PACKET = 188L
        private const val SYNC: Byte = 0x47

        /** How many leading packets must start with [SYNC] for the body to count as clear transport stream. */
        private const val SYNC_PACKETS = 4
    }
}

/**
 * The data source a guest's channel plays through: each request goes out with [live]'s newest
 * viewer token in its path ([GuestLive.rewrite]), so the 10-minute token refreshes with no restart,
 * and an address that is not the channel's own on its edge is refused. A cut-short body is a load
 * error ([WholeBodyDataSource]).
 */
fun guestDataSource(http: OkHttpClient, live: () -> GuestLive?): DataSource.Factory =
    ResolvingDataSource.Factory(WholeBodyDataSource.Factory(OkHttpDataSource.Factory(http))) { spec ->
        val l = live() ?: throw IOException("no broadcast is playing")
        val url = l.rewrite(spec.uri.toString()) ?: throw IOException("not this channel's address")
        spec.withUri(Uri.parse(url))
    }

/**
 * The player of a guest channel, shared by the Player and the TV app: it plays what a
 * [GuestWatch] shows ([show]), starting again only for a new play, so a refreshed token never
 * restarts the picture. A load that fails is tried again a few times (a segment cut short, a
 * moment without network) before [onFailed] says so.
 */
class GuestPlayer(context: Context, http: OkHttpClient = GuestClient.defaultHttp) {
    @Volatile private var live: GuestLive? = null
    private var play = -1
    private var retries = 0
    private val main = Handler(Looper.getMainLooper())

    /** The picture failed for good (after its tries). */
    var onFailed: (PlaybackException) -> Unit = {}

    val player: ExoPlayer = ExoPlayer.Builder(context)
        .setMediaSourceFactory(HlsMediaSource.Factory(guestDataSource(http) { live }))
        .build()
        .also { p ->
            p.addListener(object : Player.Listener {
                override fun onPlaybackStateChanged(state: Int) {
                    if (state == Player.STATE_READY) retries = 0
                }

                override fun onPlayerError(error: PlaybackException) {
                    if (retries++ < RETRIES) {
                        main.postDelayed({ if (live != null) p.prepare() }, RETRY_DELAY_MS)
                    } else {
                        AppLog.failure("guest player", error)
                        onFailed(error)
                    }
                }
            })
        }

    /** Plays what [shown] says: a new play's master, paused while the state does not play, stopped when nothing does. */
    fun show(shown: GuestWatch.Shown) {
        val l = shown.live
        if (l == null) {
            live = null
            play = shown.play
            player.stop()
            player.clearMediaItems()
            return
        }
        if (shown.play != play) {
            play = shown.play
            live = l
            retries = 0
            player.setMediaItem(MediaItem.fromUri(l.master))
            player.prepare()
        }
        player.playWhenReady = shown.plays
    }

    fun release() {
        main.removeCallbacksAndMessages(null)
        live = null
        player.release()
    }

    companion object {
        const val RETRIES = 3
        const val RETRY_DELAY_MS = 2_000L
    }
}

/** The guest channel's picture, with no controls (the screens draw their own). */
@Composable
fun GuestVideo(player: GuestPlayer, modifier: Modifier = Modifier) {
    AndroidView(
        factory = { ctx ->
            PlayerView(ctx).apply {
                layoutParams = ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT)
                useController = false
                resizeMode = AspectRatioFrameLayout.RESIZE_MODE_FIT
                this.player = player.player
            }
        },
        update = { it.player = player.player },
        modifier = modifier.background(Color.Black),
    )
}
