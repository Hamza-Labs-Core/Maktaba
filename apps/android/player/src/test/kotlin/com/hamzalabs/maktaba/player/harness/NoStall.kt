package com.hamzalabs.maktaba.player.harness

import org.junit.rules.TestRule
import org.junit.runner.Description
import org.junit.runners.model.Statement
import java.io.File
import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference

/**
 * A test that stalls fails by its name, with where every thread stood, instead of holding the
 * whole job until the runner cancels it with no report.
 *
 * After [limit] the test's thread is interrupted (the waits of these tests end on that) and the
 * test fails with the stacks. A thread that cannot be interrupted (a player's `release()` waits
 * that way) gets [grace] more; then the test JVM is ended, so the task fails at once. Either way
 * the stacks are also written to `build/reports/stalled-tests/`, which CI keeps.
 *
 * JUnit's own `Timeout` rule runs the test on another thread, which Robolectric's main looper
 * does not allow; this one watches from a thread of its own and leaves the test where it is.
 * ```
 * @get:Rule(order = NoStall.FIRST) val noStall = NoStall()
 * ```
 */
class NoStall(
    private val limit: Duration = Duration.ofMinutes(4),
    private val grace: Duration = Duration.ofSeconds(30),
    /** What ends a test thread that does not stop when interrupted. */
    private val giveUp: (String) -> Unit = { Runtime.getRuntime().halt(70) },
) : TestRule {
    override fun apply(base: Statement, description: Description): Statement = object : Statement() {
        override fun evaluate() {
            val test = Thread.currentThread()
            val done = CountDownLatch(1)
            val stalled = AtomicReference<String?>()
            val watch = Thread({
                if (done.await(limit.toMillis(), TimeUnit.MILLISECONDS)) return@Thread
                val report = stacks("${description.displayName} was still running after ${limit.seconds} s", test)
                stalled.set(report)
                keep(description, report)
                test.interrupt()
                if (done.await(grace.toMillis(), TimeUnit.MILLISECONDS)) return@Thread
                val stuck = stacks("${description.displayName} did not stop when interrupted; ending the test JVM", test)
                keep(description, stuck)
                giveUp(stuck)
            }, "no-stall").apply { isDaemon = true }
            watch.start()
            try {
                base.evaluate()
            } catch (t: Throwable) {
                throw stalled.get()?.let { AssertionError(it, t) } ?: t
            } finally {
                done.countDown()
                watch.join()
                // the interrupt is this test's own: the next one on this thread starts without it
                if (stalled.get() != null) Thread.interrupted()
            }
            stalled.get()?.let { throw AssertionError(it) }
        }
    }

    private fun stacks(what: String, test: Thread): String = buildString {
        appendLine(what)
        appendLine("the test's thread (${test.name}):")
        test.stackTrace.forEach { appendLine("    at $it") }
        Thread.getAllStackTraces().filterKeys { it !== test && it !== Thread.currentThread() }.forEach { (t, frames) ->
            appendLine("${t.name} (${t.state}):")
            frames.take(30).forEach { appendLine("    at $it") }
        }
    }

    private fun keep(description: Description, report: String) {
        System.err.println(report)
        runCatching {
            val dir = File("build/reports/stalled-tests").apply { mkdirs() }
            File(dir, "${description.className}.${description.methodName}.txt").writeText(report)
        }
    }

    companion object {
        /** Outside the test's other rules (JUnit runs a lower order outside), so their set-up and clean-up are watched too. */
        const val FIRST = -100
    }
}
