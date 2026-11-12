package com.hamzalabs.maktaba.player.harness

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.Description
import org.junit.runners.model.Statement
import java.io.File
import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicReference

/** [NoStall]: a stalled test fails by its name with its stack; one that ends in time is left alone. */
class NoStallTest {
    private val long = Duration.ofMinutes(5)
    private fun test(name: String) = Description.createTestDescription("SomeTest", name)
    private fun body(run: () -> Unit) = object : Statement() {
        override fun evaluate() = run()
    }

    @Test
    fun aStalledTestIsInterruptedAndFailsByItsNameWithItsStack() {
        val gaveUp = AtomicReference<String?>()
        val rule = NoStall(limit = Duration.ofMillis(200), grace = long, giveUp = { gaveUp.set(it) })
        val never = CountDownLatch(1)
        val e = assertThrows(AssertionError::class.java) { rule.apply(body { never.await() }, test("stalls")).evaluate() }
        assertTrue(e.message, e.message!!.startsWith("stalls(SomeTest) was still running after"))
        assertTrue("where it stood: ${e.message}", e.message!!.contains("CountDownLatch.await"))
        assertTrue("what it was interrupted out of", e.cause is InterruptedException)
        assertNull("it stopped when interrupted: the run goes on", gaveUp.get())
        assertFalse("the next test starts without the interrupt", Thread.currentThread().isInterrupted)
        val kept = File("build/reports/stalled-tests/SomeTest.stalls.txt")
        assertTrue("kept for CI's report", kept.readText().startsWith("stalls(SomeTest) was still running after"))
        kept.delete()
    }

    @Test
    fun aTestThatEndsInTimeIsLeftAlone() {
        var ran = false
        NoStall(limit = long, grace = long, giveUp = { throw AssertionError("gave up") }).apply(body { ran = true }, test("ends")).evaluate()
        assertTrue(ran)
        val failure = IllegalStateException("its own failure")
        val thrown = assertThrows(IllegalStateException::class.java) {
            NoStall(limit = long, grace = long).apply(body { throw failure }, test("fails")).evaluate()
        }
        assertEquals("a test's own failure is passed on as it is", failure, thrown)
    }

    @Test
    fun aThreadThatDoesNotStopWhenInterruptedEndsTheRun() {
        val gaveUp = CountDownLatch(1)
        val said = AtomicReference<String?>()
        val rule = NoStall(limit = Duration.ofMillis(200), grace = Duration.ofMillis(200), giveUp = { said.set(it); gaveUp.countDown() })
        // as a player's release() waits: it goes back to waiting however often it is interrupted
        val deaf = body {
            while (gaveUp.count > 0) {
                try {
                    gaveUp.await()
                } catch (_: InterruptedException) {
                }
            }
        }
        assertThrows(AssertionError::class.java) { rule.apply(deaf, test("deaf")).evaluate() }
        assertTrue(said.get(), said.get()!!.startsWith("deaf(SomeTest) did not stop when interrupted"))
        File("build/reports/stalled-tests/SomeTest.deaf.txt").delete()
    }
}
