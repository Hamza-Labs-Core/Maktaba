package com.hamzalabs.maktaba.player.harness

import okhttp3.OkHttpClient
import okhttp3.Request
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

/** The fake home's record of requests can be read while its server's thread records more. */
class FakeHomeTest {
    @get:Rule val home = FakeHome()

    @Test
    fun requestsCanBeReadWhileTheServerRecordsMore() {
        val http = OkHttpClient()
        val pool = Executors.newFixedThreadPool(4)
        val before = home.requests
        repeat(200) { n ->
            pool.execute {
                val url = home.home.client.home.resolve(if (n % 2 == 0) "/api/videos" else "/api/libraries")!!
                http.newCall(Request.Builder().url(url).build()).execute().close()
            }
        }
        pool.shutdown()
        // reading all along: a live list would throw ConcurrentModificationException here
        while (!pool.isTerminated) {
            home.requests.count { it.url.encodedPath == "/api/videos" }
            home.paths.any { it == "/api/libraries" }
        }
        pool.awaitTermination(30, TimeUnit.SECONDS)

        assertEquals("an earlier copy does not change", 0, before.size)
        assertEquals(200, home.requests.size)
        assertEquals(100, home.paths.count { it == "/api/videos" })
    }
}
