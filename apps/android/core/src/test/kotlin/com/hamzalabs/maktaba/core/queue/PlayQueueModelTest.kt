package com.hamzalabs.maktaba.core.queue

import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Epic 56 story 56.02 T09: the queue model's requests and window (a fake home). */
class PlayQueueModelTest {
    private val calls = mutableListOf<Triple<String, String, String?>>()
    private var answers = ArrayDeque<Pair<Int, String>>()

    private fun win(cur: Int, ids: List<String>, version: Int = 1, shuffle: Boolean = false, done: Boolean = false) =
        """{"id":"Q","source":{"kind":"library","id":"L"},"shuffle":$shuffle,"current_pos":$cur,"version":$version,"source_done":$done,
            "items":[${ids.mapIndexed { i, id -> """{"pos":$i,"id":"$id","title":"T$id"}""" }.joinToString(",")}]}"""

    private val model = PlayQueueModel({ m, p, b -> calls += Triple(m, p, b); answers.removeFirst() }, "tv-1")

    @Test
    fun shuffleCreatesThenNextMovesWithTheVersion() = runBlocking {
        answers += 201 to win(0, listOf("a", "b", "c", "d", "e", "f", "g"), shuffle = true)
        val first = model.create(QueueSource("library", "L"), shuffle = true)
        assertEquals("a", first?.id)
        assertTrue(calls[0].third!!.contains("\"shuffle\":true") && calls[0].third!!.contains("\"kind\":\"library\""))
        answers += 200 to win(1, listOf("a", "b", "c", "d", "e", "f", "g"), version = 2, shuffle = true)
        assertEquals("b", model.next()?.id)
        assertEquals(Triple("PUT", "/api/me/play-queues/Q/current", """{"pos":1,"version":1}"""), calls[1])
        assertEquals(2, model.doc?.version)
    }

    @Test
    fun nearTheEdgeTheNextWindowIsRead() = runBlocking {
        answers += 201 to win(0, listOf("a", "b", "c"))
        model.create(QueueSource("series", "S"), shuffle = false)
        answers += 200 to win(1, listOf("a", "b", "c"), version = 2)
        answers += 200 to win(1, listOf("a", "b", "c", "d", "e"), version = 2)
        assertEquals("b", model.next()?.id)
        assertEquals("GET", calls.last().first)
        assertTrue(calls.last().second.startsWith("/api/me/play-queues/Q?around=1"))
        assertEquals("c", model.peekNext()?.id)
    }

    @Test
    fun aStaleMoveTakesTheLatestAndSaysSo() = runBlocking {
        answers += 201 to win(0, listOf("a", "b"))
        model.create(QueueSource("library", "L"), shuffle = false)
        answers += 409 to """{"title":"stale_queue","queue":${win(1, listOf("a", "b"), version = 5, done = true)}}"""
        assertEquals("b", model.moveTo(1)?.id)
        assertTrue(model.staleSeen)
        assertEquals(5, model.doc?.version)
    }

    @Test
    fun previousReshuffleAndAdd() = runBlocking {
        answers += 201 to win(1, listOf("a", "b"), shuffle = true, done = true)
        model.create(QueueSource("library", "L"), shuffle = true)
        answers += 200 to win(0, listOf("a", "b"), version = 2, shuffle = true, done = true)
        assertEquals("a", model.previous()?.id)
        answers += 200 to win(0, listOf("a", "x"), version = 3, shuffle = true, done = true)
        model.reshuffle()
        assertEquals("/api/me/play-queues/Q/reshuffle", calls.last().second)
        answers += 200 to win(0, listOf("a", "n", "x"), version = 4, shuffle = true, done = true)
        model.add(listOf("n"), "next")
        assertEquals("""{"video_ids":["n"],"where":"next"}""", calls.last().third)
    }

    @Test
    fun anEmptySourceAndNoCurrentQueue() = runBlocking {
        answers += 422 to """{"title":"empty_source"}"""
        val failed = runCatching { model.create(QueueSource("library", "L", mapOf("q" to "zz")), false) }
        assertTrue(failed.exceptionOrNull() is EmptySource)
        answers += 204 to ""
        assertNull(model.fetchCurrent())
    }
}
