package com.hamzalabs.maktaba.player.media

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.util.Log
import androidx.annotation.MainThread
import androidx.annotation.OptIn
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.session.MediaSession
import androidx.media3.session.MediaSessionService

/**
 * What plays now, for the system (FR-39): the one player the notification, the lock screen,
 * Bluetooth buttons and a watch drive. The app's [com.hamzalabs.maktaba.player.Playback] hands
 * its player here when a video opens and takes it back when it stops; [PlaybackService] holds
 * the media session around it.
 *
 * Handing a player over starts the service (the app is on screen then, as a video opens);
 * Media3 then keeps it in the foreground, with the notification, while the player plays, so the
 * sound goes on with the screen off. Taking it back stops the service.
 */
object NowPlaying {
    /** The player the system's controls drive; null when nothing plays. */
    var player: Player? = null
        private set

    /** The service's ear (while it runs): told each time the player changes. */
    internal var listener: ((Player?) -> Unit)? = null

    @MainThread
    fun attach(context: Context, p: Player) {
        if (player === p) return
        player = p
        val running = listener
        if (running != null) {
            running(p)
            return
        }
        val app = context.applicationContext
        try {
            app.startService(Intent(app, PlaybackService::class.java))
        } catch (e: IllegalStateException) {
            // not allowed from the background (a video opened as the app left): no notification
            Log.w("Maktaba", "playback: the media service did not start (${e.javaClass.simpleName})")
        }
    }

    @MainThread
    fun detach(p: Player) {
        if (player !== p) return
        player = null
        listener?.invoke(null)
    }
}

/**
 * The media session's service (FR-39): sound continues with the screen off, and the notification
 * has play / pause, ±10 s and the title. It holds no player of its own: [NowPlaying]'s, which the
 * app releases. Tapping the notification opens the app where it is.
 */
@OptIn(UnstableApi::class)
class PlaybackService : MediaSessionService() {
    private var session: MediaSession? = null
    private val ear: (Player?) -> Unit = { use(it) }

    override fun onCreate() {
        super.onCreate()
        NowPlaying.listener = ear
        use(NowPlaying.player)
    }

    private fun use(p: Player?) {
        val s = session
        when {
            p == null -> {
                s?.let { removeSession(it); it.release() }
                session = null
                stopSelf()
            }
            // added at once: the notification needs no controller to connect first
            s == null -> session = MediaSession.Builder(this, p).setSessionActivity(openApp()).build().also { addSession(it) }
            s.player !== p -> s.player = p
        }
    }

    private fun openApp(): PendingIntent {
        val open = packageManager.getLaunchIntentForPackage(packageName)
            ?: Intent().setPackage(packageName)
        open.addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP)
        return PendingIntent.getActivity(this, 0, open, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
    }

    override fun onGetSession(controllerInfo: MediaSession.ControllerInfo): MediaSession? = session

    override fun onDestroy() {
        if (NowPlaying.listener === ear) NowPlaying.listener = null
        session?.release()
        session = null
        super.onDestroy()
    }
}
