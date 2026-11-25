package com.hamzalabs.maktaba.tv

import android.os.NetworkOnMainThreadException
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.AiConversation
import com.hamzalabs.maktaba.core.api.AiRefusal
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.session.TokenStore
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.Call
import okhttp3.Callback
import okhttp3.Request
import okhttp3.Response
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import java.io.IOException
import java.util.concurrent.TimeUnit

/**
 * AI mode refused, on the Android TV emulator (run by tools/ci/firetv-journey.sh): the reason the
 * home gives is shown when its answer's body comes after its headers, asked from the main thread
 * as the screens ask. Android alone forbids reading the network there (#1741).
 */
@RunWith(AndroidJUnit4::class)
class AiRefusalTest {
    private val server = MockWebServer()

    private val signedOut = object : TokenStore {
        override val access: String? = null
        override val refresh: String? = null
        override var recoveryPass: String? = null
        override fun save(tokens: AuthTokens) = Unit
        override fun clear() = Unit
    }

    @Before
    fun start() = server.start()

    @After
    fun stop() = server.close()

    private fun late(code: Int, body: String) = server.enqueue(
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/problem+json").body(body).bodyDelay(500, TimeUnit.MILLISECONDS).build(),
    )

    private fun shown(): AiRefusal? = runBlocking {
        val c = AiConversation(ApiClient(server.url("/"), signedOut))
        withTimeout(20_000) {
            withContext(Dispatchers.Main) { c.ask(CoroutineScope(SupervisorJob() + Dispatchers.Main), "q")!! }.join()
        }
        c.state.value.refusal
    }

    @Test
    fun aRefusalWhoseBodyComesLateIsStillItsReason() {
        late(409, """{"code":"ai-off","status":409}""")
        assertEquals(AiRefusal.Off, shown())
        late(429, """{"code":"ai-busy","status":429,"position":9}""")
        assertEquals(AiRefusal.Busy(9), shown())
    }

    /** Why it matters: such a body cannot be read on the main thread. */
    @Test
    fun androidForbidsReadingALateBodyOnTheMainThread() = runBlocking {
        late(409, """{"code":"ai-off","status":409}""")
        val headers = CompletableDeferred<Response>()
        ApiClient.defaultHttp.newCall(Request.Builder().url(server.url("/")).build()).enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                headers.completeExceptionally(e)
            }

            override fun onResponse(call: Call, response: Response) {
                headers.complete(response)
            }
        })
        val response = withTimeout(20_000) { headers.await() }
        val read = withContext(Dispatchers.Main) { runCatching { response.body.string() } }
        assertTrue("$read", read.exceptionOrNull() is NetworkOnMainThreadException)
        withContext(Dispatchers.IO) { runCatching { response.close() } }
        Unit
    }
}
