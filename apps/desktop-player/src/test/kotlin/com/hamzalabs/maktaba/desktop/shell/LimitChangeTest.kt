package com.hamzalabs.maktaba.desktop.shell

import com.hamzalabs.maktaba.desktop.TestHome
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertSame
import org.junit.Test

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
}
