package com.hamzalabs.maktaba.desktop.libraries

import com.hamzalabs.maktaba.desktop.TestHome
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.CountDownLatch

/** Story 55.05 T01–T05. */
class LibrariesStateTest {
    private val summary = """{"items":[{"id":"a","name":"Alpha","videos":3},{"id":"b","name":"Beta","videos":2,"pinned":true,"pin_position":0},{"id":"c","name":"Gamma"}]}"""

    @Test
    fun thePinnedComeFirst() = TestHome().use { fake ->
        fake.answer("GET /api/libraries/summary", summary)
        runBlocking {
            val s = LibrariesState(fake.client) {}
            s.load()
            assertEquals(listOf("b", "a", "c"), s.libraries.map { it.id })
        }
    }

    @Test
    fun aPinStaysWhenAnOlderReadAnswersUnpinned() = TestHome().use { fake ->
        fake.answer("GET /api/libraries/summary", summary)
        fake.answer("PUT /api/me/pinned-libraries", """{"library_ids":["b","c"]}""")
        runBlocking {
            val s = LibrariesState(fake.client) {}
            s.load()
            val gate = CountDownLatch(1)
            fake.held("GET /api/libraries/summary", { summary }, gate)
            val read = async { s.load() }
            while (fake.asked("GET /api/libraries/summary").size < 2) delay(10)
            s.togglePin("c")
            assertEquals(listOf("b", "c", "a"), s.libraries.map { it.id })
            gate.countDown()
            read.await()
            assertEquals(listOf("b", "c", "a"), s.libraries.map { it.id })
            assertTrue(fake.asked("PUT /api/me/pinned-libraries").single().body!!.utf8().contains("\"library_ids\":[\"b\",\"c\"]"))
        }
    }

    @Test
    fun aFailedPinIsUndoneAndSaysSo() = TestHome().use { fake ->
        fake.answer("GET /api/libraries/summary", summary)
        fake.answer("PUT /api/me/pinned-libraries", "{}", 500)
        runBlocking {
            val s = LibrariesState(fake.client) {}
            s.load()
            s.togglePin("c")
            assertEquals(listOf("b", "a", "c"), s.libraries.map { it.id })
            assertTrue(s.saveFailed)
        }
    }

    @Test
    fun aFilterChangeMidPageRestartsTheGridAndDropsTheOlderPage() = TestHome().use { fake ->
        fake.answer("GET /api/libraries/l1/hub", """{"library":{"id":"l1","name":"Lectures"}}""")
        fake.answer("GET /api/videos/facets", """{"languages":[{"code":"ar","videos":2}]}""")
        val gate = CountDownLatch(1)
        fake.answer("GET /api/videos") { r ->
            if (r.url.queryParameter("cursor") == "p2") {
                gate.await()
                TestHome.json("""{"items":[{"id":"old"}]}""")
            } else if (r.url.queryParameter("language") == "ar") {
                TestHome.json("""{"items":[{"id":"ar1","title":"A"},{"id":"ar2","title":"B"}],"next":"q2"}""")
            } else TestHome.json("""{"items":[{"id":"v1"},{"id":"v2"}],"next":"p2"}""")
        }
        runBlocking {
            val v = LibraryVideos(fake.client, "l1") {}
            v.open()
            assertEquals("Lectures", v.name)
            assertEquals(listOf("v1", "v2"), v.videos.map { it.id })
            val older = async { v.more() }
            while (fake.asked("GET /api/videos").none { it.url.queryParameter("cursor") == "p2" }) delay(10)
            v.choose(sort = LibrarySort.Title, language = "ar")
            gate.countDown()
            older.await()
            assertEquals(listOf("ar1", "ar2"), v.videos.map { it.id })
            val q = fake.asked("GET /api/videos").last { it.url.queryParameter("language") == "ar" }.url
            assertEquals("title", q.queryParameter("sort"))
            assertEquals("asc", q.queryParameter("order"))
            assertEquals("q2", v.next)
        }
    }

    @Test
    fun aLibraryThatIsNotSharedIsNotAvailable() = TestHome().use { fake ->
        runBlocking {
            val v = LibraryVideos(fake.client, "gone") {}
            v.open()
            assertTrue(v.notAvailable)
        }
    }
}
