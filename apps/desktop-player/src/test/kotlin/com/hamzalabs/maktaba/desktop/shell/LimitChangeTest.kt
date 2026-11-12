package com.hamzalabs.maktaba.desktop.shell

import com.hamzalabs.maktaba.desktop.TestHome
import com.hamzalabs.maktaba.desktop.STALL_MS
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.yield
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger

/**
 * Epic 57 story 57.05 on the desktop Player: when the sign-in's parental limit changes (its
 * `version`), every page's kept state was read under the limit before and goes, so the pages
 * read again; an unchanged limit drops nothing.
 */
class LimitChangeTest {
    private fun limit(version: Int, tier: Int) = """{"restricted":true,"max_tier":$tier,"allow_only":false,"version":$version}"""

    @Test
    fun aChangedLimitDropsEveryPagesKeptState() = TestHome().use { fake ->
        runBlocking {
            fake.answer("GET /api/me/parental", limit(1, tier = 13))
            val home = fake.home()
            home.parental.refresh()
            val epoch = home.keptEpoch
            val page = home.keep("video:v1") { Any() }
            home.parental.refresh()
            assertSame("kept while the limit is the same", page, home.keep("video:v1") { Any() })
            assertEquals(epoch, home.keptEpoch)

            // a guardian lowers the limit to 9+
            fake.answer("GET /api/me/parental", limit(2, tier = 9))
            home.parental.refresh()
            assertNotSame("the page reads again under the new limit", page, home.keep("video:v1") { Any() })
            assertEquals("the pages compose again", epoch + 1, home.keptEpoch)
        }
    }

    /**
     * Two reads of the limit on their way at once (a video opened while the read at sign-in
     * was still out): the earlier read's answer lands last and is older than what is known.
     * It is left: the pages were dropped once and the limit is the newer one.
     */
    @Test
    fun anAnswerToAnEarlierReadOfTheLimitIsLeft() = TestHome().use { fake ->
        val firstArrived = CountDownLatch(1)
        val release = CountDownLatch(1)
        val reads = AtomicInteger()
        fake.answer("GET /api/me/parental") {
            if (reads.incrementAndGet() == 1) {
                firstArrived.countDown()
                release.await(STALL_MS, TimeUnit.MILLISECONDS)
                TestHome.json(limit(1, tier = 13))
            } else {
                TestHome.json(limit(2, tier = 9))
            }
        }
        try {
            runBlocking {
                val home = fake.home()
                val epoch = home.keptEpoch
                val early = launch { home.parental.refresh() }
                yield()
                assertTrue("the first read reached the home", firstArrived.await(STALL_MS, TimeUnit.MILLISECONDS))
                home.parental.refresh()
                assertEquals(9, home.parental.mine.value.maxTier)
                val page = home.keep("video:v1") { Any() }
                release.countDown()
                early.join()
                assertEquals("the older answer is left", 9, home.parental.mine.value.maxTier)
                assertEquals("two reads on their way: the pages compose again once", epoch + 1, home.keptEpoch)
                assertSame(page, home.keep("video:v1") { Any() })
            }
        } finally {
            release.countDown()
        }
    }
}
