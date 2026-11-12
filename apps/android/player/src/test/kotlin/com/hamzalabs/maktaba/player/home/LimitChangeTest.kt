package com.hamzalabs.maktaba.player.home

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.HomeRails
import com.hamzalabs.maktaba.core.browse.CachedHome
import com.hamzalabs.maktaba.core.browse.HomeCache
import com.hamzalabs.maktaba.core.parental.KeptLimit
import com.hamzalabs.maktaba.core.queue.QueueDoc
import com.hamzalabs.maktaba.core.queue.QueueSource
import com.hamzalabs.maktaba.core.ui.ThemeStore
import com.hamzalabs.maktaba.player.dropKeptFor
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.NoStall
import com.hamzalabs.maktaba.player.libraries.PlayerQueues
import com.hamzalabs.maktaba.player.shell.PlayerHome
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File

/**
 * Epic 57 story 57.05 on the phone Player: when the sign-in's parental limit changes (its
 * `version`), what was read under the limit before goes: the pages' kept states, Home's rows on
 * disk, the queue's window and the pictures. A sign-in under a limit shows nothing from disk.
 */
@RunWith(AndroidJUnit4::class)
class LimitChangeTest {
    @get:Rule(order = NoStall.FIRST) val noStall = NoStall()
    @get:Rule val home = FakeHome()
    private val context: Context = ApplicationProvider.getApplicationContext()

    private class Remembered(var mark: KeptLimit.Mark? = null) : KeptLimit {
        override fun read() = mark
        override fun write(mark: KeptLimit.Mark) {
            this.mark = mark
        }
    }

    private fun limit(version: Int, tier: Int) = FakeHome.json("""{"restricted":true,"max_tier":$tier,"allow_only":false,"version":$version}""")

    @Test
    fun aChangedLimitDropsThePagesKeptStatesTheQueueWindowAndTheRest() = runBlocking {
        val client = home.home.client
        var dropped = 0
        val signedIn = PlayerHome(home.home.session, home.home.user, client, ThemeStore(MapStore()), limitKept = Remembered(), dropKept = { dropped++ })
        home.answer("/api/me/parental") { limit(1, tier = 13) }
        signedIn.parental.refresh()
        val before = dropped

        val page = signedIn.keep("video.v1") { Any() }
        val background = signedIn.keep("watch.background") { Any() }
        assertSame("kept while the limit is the same", page, signedIn.keep("video.v1") { Any() })
        PlayerQueues.of(client).resume(QueueDoc("q1", QueueSource("library", "l1")))
        signedIn.parental.refresh()
        assertSame(page, signedIn.keep("video.v1") { Any() })
        assertEquals(before, dropped)

        // a guardian lowers the limit to 9+
        home.answer("/api/me/parental") { limit(2, tier = 9) }
        signedIn.parental.refresh()
        assertNotSame("the page reads again under the new limit", page, signedIn.keep("video.v1") { Any() })
        assertSame("the scope of a stopped video's last calls stays", background, signedIn.keep("watch.background") { Any() })
        assertNull("the queue's window goes", PlayerQueues.of(client).doc)
        assertEquals("Home's rows on disk and the pictures go", before + 1, dropped)
    }

    private fun kept(): CachedHome = CachedHome(ApiClient.json.decodeFromString(HomeRails.serializer(), HomeFixtures.HOME))

    @Test
    fun aSignInUnderALimitShowsNothingFromDiskAndKeepsNothingThere() = runBlocking {
        val file = File(context.cacheDir, "limit-test/home.json")
        val cache = HomeCache(file)
        cache.write(kept())
        // the home does not answer: only what is kept could show
        val limited = HomeState(home.home.client, cache, liveNow = { null }, signedOut = {}, keepOnDisk = { false })
        limited.open()
        assertNull("rows read under the limit before are not shown", limited.rails)
        assertNotNull(limited.problem)

        // without a limit the kept rows show at once, as before
        val free = HomeState(home.home.client, cache, liveNow = { null }, signedOut = {}, keepOnDisk = { true })
        free.open()
        assertNotNull(free.rails)

        // and a limited sign-in's fresh answer is not written: the file goes
        HomeFixtures.install(home, now = null)
        val fresh = HomeState(home.home.client, cache, liveNow = { null }, signedOut = {}, keepOnDisk = { false })
        fresh.open()
        assertNotNull(fresh.rails)
        assertFalse("nothing of a limited sign-in's Home stays on disk", file.exists())
    }

    @Test
    fun droppingWhatIsKeptDeletesHomesRowsOnDisk() = runBlocking {
        val url = home.home.client.home
        val cache = HomeCache.of(context.cacheDir, url, "u1")
        cache.write(kept())
        assertNotNull(cache.read())
        dropKeptFor(context, url, "u1")
        assertNull(cache.read())
        assertTrue("the picture cache is emptied", com.hamzalabs.maktaba.player.ui.components.ImageCache.shared(context).second.size == 0L)
    }
}
