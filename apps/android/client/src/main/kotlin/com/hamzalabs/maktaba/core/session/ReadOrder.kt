package com.hamzalabs.maktaba.core.session

/**
 * The order of a list's reads and changes. A read that began before a change was answered
 * before it (its answer still has the removed row, the old pin), and an answer that arrives
 * after a newer one is older than what is on screen: neither may be applied, or the old state
 * comes back on screen while the home has the new one. As the web's `lib/readOrder`.
 *
 * A read takes a ticket ([begin]) and applies its answer only when it is [fresh]; a removal or a
 * toggle runs its call in [saving]. One per list, used from the main thread (as the screens'
 * state is); nothing here is shared between threads.
 */
class ReadOrder {
    private var started = 0

    /** The oldest ticket that may still be applied. */
    private var floor = 0

    /** The saves under way. */
    private var pending = 0

    /** A read begins: its ticket, for [fresh] once it is answered. */
    fun begin(): Int = ++started

    /**
     * The read with [ticket] was answered: true when it may be applied, that is it began after
     * every change and is no older than any answer applied. Then the reads that began before it
     * are older and are dropped (a newer one still applies).
     */
    fun fresh(ticket: Int): Boolean {
        if (pending > 0 || ticket < floor) return false
        floor = ticket
        return true
    }

    /** A change is on screen or saved: every read under way is older than it. */
    fun changed() {
        floor = started + 1
    }

    /**
     * Runs [save] (a removal, a toggle): no read is applied while it is under way, and every read
     * that began before it ends is older than it.
     */
    suspend fun <T> saving(save: suspend () -> T): T {
        pending++
        try {
            return save()
        } finally {
            pending--
            floor = started + 1
        }
    }
}
