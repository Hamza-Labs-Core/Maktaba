package com.hamzalabs.maktaba.desktop.mpv

/**
 * Which of mpv's playlist entries the newest stream given to a handle is (story 13.22). A stream
 * replaced by a load may still open, end, fail or move on before the load is played, and none of
 * that is the new one's; an entry's id is known only once its `loadfile` has run, on another
 * thread than mpv's events and than the caller's, and another load may be asked for meanwhile.
 * Each load has a number of its own ([next]), and every step here is one, so what is heard of
 * the newest is heard once and the rest never, in whatever order the threads come.
 */
internal class NewestLoad {
    /** A stream's end, as mpv's END_FILE says it. */
    data class End(val entry: Long, val reason: Int, val error: Int)

    /** What was heard of an entry before its id was read: opened, its picture started, its ends. */
    data class Heard(val opened: Boolean = false, val restarted: Boolean = false, val ends: List<End> = emptyList())

    // the newest load's entry (PENDING: its id not read yet; ANY: this mpv does not say)
    private var entry = PENDING
    // ends heard meanwhile
    private val early = mutableListOf<End>()
    // the entry mpv started last (START_FILE), the last one it opened, the last one whose
    // playback restarted, and the last one whose opening was given on
    private var started = 0L
    private var loaded = 0L
    private var restarted = 0L
    private var given = 0L

    /** The newest load (0: the first stream). */
    @Volatile var load = 0
        private set

    /**
     * The newest stream's picture has not started: mpv's frames are drawn, not shown, so the
     * frame on screen stays (none at first: what mpv draws before the picture starts is black,
     * not the stream's). Set before [load] moves on, so a frame is never shown as a load's that
     * is still held.
     */
    @Volatile var holding = true
        private set

    /** Another stream is given to the handle: from now on nothing heard is its, until its id is [read]. Its number. */
    @Synchronized fun next(): Int {
        holding = true
        entry = PENDING
        early.clear()
        return ++load
    }

    /** The entry [id] of load [n]'s `loadfile` (null: this mpv does not say): what was heard of it meanwhile; nothing when [n] is not the newest. */
    @Synchronized fun read(n: Int, id: Long?): Heard {
        if (n != load) return Heard()
        entry = id ?: ANY
        val ends = early.filter { id == null || it.entry == id }
        early.clear()
        // opened, or its picture started, before its id was read (a very fast home)
        return Heard(id != null && loaded == id && giveOpened(id), id != null && restarted == id && release(), ends)
    }

    /** START_FILE: mpv starts entry [id]; the events after it are its. */
    @Synchronized fun starts(id: Long) {
        started = id
    }

    /** FILE_LOADED: whether the newest has opened, said once. */
    @Synchronized fun opens(): Boolean {
        loaded = started
        return isNewest(started) && giveOpened(started)
    }

    /** PLAYBACK_RESTART: whether the newest's picture plays; the frame held for it may go. */
    @Synchronized fun restarts(): Boolean {
        restarted = started
        return isNewest(started) && release()
    }

    /** END_FILE: whether it is the newest's (one heard before its id is kept for [read]). */
    @Synchronized fun ends(end: End): Boolean {
        if (entry == PENDING) {
            early += end
            return false
        }
        return isNewest(end.entry)
    }

    /** Whether what mpv plays now is the newest, its picture started (a position is its). */
    @Synchronized fun shows(): Boolean = !holding && isNewest(started)

    /** Whether entry [id]'s picture had started. */
    @Synchronized fun played(id: Long): Boolean = restarted == id

    private fun isNewest(id: Long) = entry == ANY || (entry > 0 && id == entry)

    private fun giveOpened(id: Long): Boolean {
        // where mpv gives no id, each opening is another's
        if (entry != ANY && given == id) return false
        given = id
        return true
    }

    private fun release(): Boolean {
        holding = false
        return true
    }

    @Synchronized override fun toString() = "load $load, holding=$holding, entry=$entry, started=$started"

    companion object {
        private const val PENDING = 0L
        private const val ANY = -1L
    }
}
