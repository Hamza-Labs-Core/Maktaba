@file:OptIn(UnstableApi::class)

package com.hamzalabs.maktaba.player.guest

import android.net.Uri
import androidx.annotation.OptIn
import androidx.media3.common.C
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.BaseDataSource
import androidx.media3.datasource.DataSourceException
import androidx.media3.datasource.DataSpec
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.guest.GuestLive
import com.hamzalabs.maktaba.core.guest.WholeBodyDataSource
import com.hamzalabs.maktaba.core.guest.guestDataSource
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.OkHttpClient
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import org.junit.runner.RunWith
import java.io.EOFException
import java.io.IOException
import java.util.Collections

/**
 * Story 50.27: the guest player's data source. A body cut short is a load error (the player loads
 * it again), never a whole segment; each request goes out with the newest viewer token in its
 * path; an address that is not the channel's own is refused.
 */
@RunWith(AndroidJUnit4::class)
class GuestDataSourceTest {
    /** Declares [declared] bytes (or none) and delivers [body]. */
    private class Stub(private val body: ByteArray, private val declared: Long) : BaseDataSource(true) {
        private var at = 0
        private var uri: Uri? = null
        override fun open(dataSpec: DataSpec): Long {
            uri = dataSpec.uri
            at = 0
            return declared
        }
        override fun read(buffer: ByteArray, offset: Int, length: Int): Int {
            if (at >= body.size) return C.RESULT_END_OF_INPUT
            val n = minOf(length, body.size - at)
            System.arraycopy(body, at, buffer, offset, n)
            at += n
            return n
        }
        override fun getUri() = uri
        override fun close() {}
    }

    private fun readAll(declared: Long, body: ByteArray, path: String = "/vt/ch1/low/seg0.ts"): Int {
        val s = WholeBodyDataSource(Stub(body, declared))
        s.open(DataSpec(Uri.parse("https://live.maktaba.tv$path")))
        val buf = ByteArray(100)
        var total = 0
        while (true) {
            val n = s.read(buf, 0, buf.size)
            if (n == C.RESULT_END_OF_INPUT) break
            total += n
        }
        s.close()
        return total
    }

    private fun assertCut(block: () -> Unit) {
        try {
            block()
            fail("a cut-short body was read as whole")
        } catch (e: DataSourceException) {
            assertTrue(e.cause is EOFException)
        }
    }

    @Test
    fun aBodyShorterThanItsDeclaredLengthIsAnError() {
        assertCut { readAll(1000, ByteArray(400)) }
        assertCut { readAll(1000, ByteArray(400), "/vt/ch1/low/index.m3u8") }
        assertEquals(1000, readAll(1000, ByteArray(1000)))
    }

    @Test
    fun aTransportStreamWithNoLengthMustEndOnAPacket() {
        assertCut { readAll(C.LENGTH_UNSET.toLong(), ByteArray(188 * 3 + 50)) }
        assertEquals(188 * 3, readAll(C.LENGTH_UNSET.toLong(), ByteArray(188 * 3)))
        // a playlist has no packets: any length is whole
        assertEquals(77, readAll(C.LENGTH_UNSET.toLong(), ByteArray(77), "/vt/ch1/low/index.m3u8"))
    }

    @Test
    fun eachRequestGoesOutWithTheNewestTokenAndOnlyToTheChannel() {
        val server = MockWebServer()
        val paths: MutableList<String> = Collections.synchronizedList(mutableListOf())
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                paths += request.url.encodedPath
                return MockResponse.Builder().code(200).body("x".repeat(188)).build()
            }
        }
        server.start()
        try {
            val base = server.url("/").toString().trimEnd('/')
            val live = GuestLive.of("$base/vt-1/ch1/master.m3u8")!!
            val factory = guestDataSource(OkHttpClient()) { live }
            fun fetch(url: String) {
                val s = factory.createDataSource()
                try {
                    s.open(DataSpec(Uri.parse(url)))
                    val buf = ByteArray(512)
                    while (s.read(buf, 0, buf.size) != C.RESULT_END_OF_INPUT) Unit
                } finally {
                    s.close()
                }
            }
            fetch("$base/vt-1/ch1/low/seg0.ts")
            // the heartbeat brought a new token: an address from the old playlist uses the new one
            assertTrue(live.update("$base/vt-2/ch1/master.m3u8"))
            fetch("$base/vt-1/ch1/low/seg1.ts")
            assertEquals(listOf("/vt-1/ch1/low/seg0.ts", "/vt-2/ch1/low/seg1.ts"), paths.toList())
            for (bad in listOf("$base/vt-1/ch2/low/seg0.ts", "https://evil.example/vt-1/ch1/low/seg0.ts")) {
                try {
                    fetch(bad)
                    fail("$bad was fetched")
                } catch (_: IOException) {
                }
            }
            assertEquals(2, paths.size)
        } finally {
            server.close()
        }
    }
}
