package com.hamzalabs.maktaba.player

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.async
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** The order of a list's reads and changes (as the web's `lib/readOrder.test.ts`). */
class ReadOrderTest {
    @Test
    fun aReadThatBeganBeforeAChangeIsDropped() {
        val o = ReadOrder()
        val t = o.begin()
        o.changed()
        assertFalse(o.fresh(t))
        assertTrue(o.fresh(o.begin()))
    }

    @Test
    fun anAnswerOlderThanOneAppliedIsDroppedNeverANewerOne() {
        val o = ReadOrder()
        val a = o.begin()
        val b = o.begin()
        assertTrue(o.fresh(b))
        assertFalse(o.fresh(a))
        // a later read still applies: a busy page keeps updating
        assertTrue(o.fresh(o.begin()))
    }

    @Test
    fun noReadAppliesWhileASaveIsUnderWayNorOneThatBeganBeforeItEnded() = runBlocking {
        val o = ReadOrder()
        val save = CompletableDeferred<String>()
        val saving = async(start = CoroutineStart.UNDISPATCHED) { o.saving { save.await() } }
        val during = o.begin()
        assertFalse(o.fresh(during))
        save.complete("ok")
        assertEquals("ok", saving.await())
        assertFalse(o.fresh(during))
        assertTrue(o.fresh(o.begin()))
    }

    @Test
    fun aSaveThatFailsStillMakesOlderReadsStaleAndTheFailureIsTheCallers() = runBlocking {
        val o = ReadOrder()
        val t = o.begin()
        val e = runCatching { o.saving<Unit> { error("no") } }.exceptionOrNull()
        assertEquals("no", e?.message)
        assertFalse(o.fresh(t))
        assertTrue(o.fresh(o.begin()))
    }
}
