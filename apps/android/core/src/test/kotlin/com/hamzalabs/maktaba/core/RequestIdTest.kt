package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.RequestId
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** Epic 53 story 53.08 T01: one id per action, a UUIDv7. */
class RequestIdTest {
    @Test
    fun `a uuid v7`() {
        val id = RequestId.v7(1_730_000_000_000)
        assertTrue(id, Regex("^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$").matches(id))
        assertEquals(8, RequestId.short(id).length)
    }

    @Test
    fun `calls of one action share the id`() {
        val t = 2_000_000_000_000
        val a = RequestId.next(t)
        assertEquals(a, RequestId.next(t + 300))
        assertNotEquals(a, RequestId.next(t + 2_000))
        assertTrue(RequestId.recent().contains(a))
    }
}
