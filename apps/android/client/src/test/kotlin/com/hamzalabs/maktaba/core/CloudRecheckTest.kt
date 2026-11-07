package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.CloudStatusRules
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeoutOrNull
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.net.InetAddress

/**
 * Epic 50 story 50.11 §4: a request through the relay that gets no answer or a 502 asks for
 * Maktaba Cloud's status at once (the rules themselves: core's CloudStatusTest).
 */
class CloudRecheckTest {
    private lateinit var server: MockWebServer

    @Before
    fun setUp() {
        CloudStatusRules.resetRecheck()
        server = MockWebServer()
        server.start()
    }

    @After
    fun tearDown() {
        server.close()
        CloudStatusRules.resetRecheck()
    }

    /** A client whose home is under relay.maktaba.tv, answered by [server]. */
    private fun throughRelay(): ApiClient {
        val home: HttpUrl = "http://h1.relay.maktaba.tv:${server.port}/".toHttpUrl()
        val http = OkHttpClient.Builder().dns { listOf(InetAddress.getLoopbackAddress()) }.build()
        return ApiClient(home, MemoryTokens(), http = http, pause = {})
    }

    @Test
    fun `a request the relay fails fires the recheck the strip waits on`() = runBlocking {
        server.enqueue(MockResponse.Builder().code(502).body("bad gateway").build())
        val fired = async(start = CoroutineStart.UNDISPATCHED) {
            withTimeoutOrNull(5_000) { CloudStatusRules.recheck.first() }
        }
        val e = runCatching { throughRelay().libraries() }.exceptionOrNull()
        assertTrue("$e", e is ApiException.Status && e.code == 502)
        assertNotNull("the strip was told to read the feed", fired.await())
    }

    @Test
    fun `no answer at all fires it too, a working request does not`() = runBlocking {
        server.enqueue(MockResponse.Builder().body("""{"items":[],"next":null}""").build())
        val quiet = async(start = CoroutineStart.UNDISPATCHED) {
            withTimeoutOrNull(500) { CloudStatusRules.recheck.first() }
        }
        throughRelay().libraries()
        assertNull("a 200 asks nothing", quiet.await())

        server.close() // nothing listens: the request gets no answer
        val fired = async(start = CoroutineStart.UNDISPATCHED) {
            withTimeoutOrNull(5_000) { CloudStatusRules.recheck.first() }
        }
        val e = runCatching { throughRelay().libraries() }.exceptionOrNull()
        assertTrue("$e", e is ApiException.Transport)
        assertEquals(Unit, fired.await())
    }
}
