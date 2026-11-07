package com.hamzalabs.maktaba.core

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import java.util.concurrent.atomic.AtomicReference
import kotlin.concurrent.thread

/**
 * The tests' in-memory store is read while a session under test writes it from other threads
 * (CloudTest once failed on a ConcurrentModificationException): reading it during writes
 * never throws.
 */
class MapStoreTest {
    @Test
    fun readingWhileAnotherThreadWritesNeverThrows() {
        val store = MapStore()
        val failure = AtomicReference<Throwable?>(null)
        val writer = thread {
            try {
                repeat(20_000) { i ->
                    store.put("k${i % 500}", "v$i")
                    if (i % 3 == 0) store.put("k${(i + 7) % 500}", null)
                }
            } catch (t: Throwable) {
                failure.set(t)
            }
        }
        try {
            while (writer.isAlive) {
                store.map.values.any { it == "never" }
                store.map.keys.toList()
            }
        } catch (t: Throwable) {
            failure.set(t)
        }
        writer.join()
        assertNull(failure.get())
        store.put("a", "b")
        assertEquals("b", store.get("a"))
        store.put("a", null)
        assertNull(store.get("a"))
    }
}
