package com.hamzalabs.maktaba.desktop

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestCoroutineScheduler
import java.util.concurrent.TimeUnit
import java.util.concurrent.locks.LockSupport

/**
 * The Player's main thread for a test of [Playback] against a [TestHome]: the test's own thread,
 * on a clock only the test moves. What the playback queues (a step after the home's answer, its
 * one-second watch) runs when the test looks ([run], [until], [settle]) or moves the clock
 * ([pass]); nothing waits for a length of real time. The home's answers still come from OkHttp's
 * threads: [until] waits for a state, [settle] for every answer on its way to have been read.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class TestMain(private val home: TestHome) {
    private val time = TestCoroutineScheduler()
    private val dispatcher = StandardTestDispatcher(time)
    val scope = CoroutineScope(SupervisorJob() + dispatcher)

    /** The scope that outlives the screen (a stopped video's last calls). */
    val background = CoroutineScope(SupervisorJob() + dispatcher)

    /** The playback's own time: this clock, from a fixed date. */
    val nowMs: () -> Long = { 1_794_000_000_000L + time.currentTime }

    /** Runs what is due, then [block], then what that queued. */
    fun <T> run(block: () -> T): T {
        time.runCurrent()
        return block().also { time.runCurrent() }
    }

    /** Waits for [ok]; it returns as soon as it holds, and the bound is only the net under a stall. */
    fun until(what: String, ok: () -> Boolean) {
        val end = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(STALL_MS)
        while (true) {
            time.runCurrent()
            if (ok()) return
            if (System.nanoTime() > end) {
                throw AssertionError("never: $what; the home was asked ${home.seen.map { it.method + " " + it.url.encodedPath }}")
            }
            // nothing due: the answer is still on another thread, which is given the processor
            LockSupport.parkNanos(500_000)
        }
    }

    /**
     * Lets everything on its way land: returns once no call to the home is queued or running,
     * no background work is alive, the home was asked nothing new, and what those queued has
     * run. (OkHttp counts a call as running until its callback returns, and the client resumes
     * its caller inside that callback: a call that ends has already queued its next step.)
     */
    fun settle() {
        fun quiet() = home.idle && background.coroutineContext[Job]!!.children.none()
        val end = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(STALL_MS)
        while (true) {
            val asked = home.seen.size
            val before = quiet()
            time.runCurrent()
            if (before && quiet() && asked == home.seen.size) return
            if (System.nanoTime() > end) {
                throw AssertionError("the home's answers never settled; it was asked ${home.seen.map { it.method + " " + it.url.encodedPath }}")
            }
            LockSupport.parkNanos(500_000)
        }
    }

    /**
     * [ms] pass with nobody touching anything, a second at a time (the watch ticks every
     * second), each second's calls answered and read before the next.
     */
    fun pass(ms: Long) {
        settle()
        repeat((ms / 1_000).toInt()) {
            time.advanceTimeBy(1_000)
            settle()
        }
    }

    fun close() {
        time.runCurrent()
        scope.cancel()
        background.cancel()
    }
}
