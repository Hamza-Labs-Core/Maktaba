package com.hamzalabs.maktaba.tv

import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.ErrorKind
import com.hamzalabs.maktaba.tv.ui.screens.Load
import com.hamzalabs.maktaba.tv.ui.screens.runLoad
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

/** A TV screen's load: its value, or a failure a person can read (never the home's answer). */
class TvLoadTest {
    private var signedOut = 0
    private val out = { signedOut++; Unit }

    private fun failed(state: Load<*>): Load.Failed {
        assertTrue("expected a failure, got $state", state is Load.Failed)
        return state as Load.Failed
    }

    @Test
    fun `a load that works is ready`() = runBlocking {
        assertEquals(Load.Ready(3), runLoad("test", out) { 3 })
        assertEquals(0, signedOut)
    }

    @Test
    fun `a 404 with problem JSON is not found, and its body never reaches the sentence`() = runBlocking {
        val body = """{"type":"https://h/problems/video-not-found","title":"Not Found","status":404,"detail":"/volume1/x.mkv"}"""
        val f = failed(runLoad("test", out) { throw ApiException.Status(404, body) }).failure
        assertEquals(ErrorKind.NotFound, f.kind)
        assertEquals(0, signedOut)
    }

    @Test
    fun `a home that does not answer is unreachable`() = runBlocking {
        val f = failed(runLoad("test", out) { throw ApiException.Transport("connect timed out") }).failure
        assertEquals(ErrorKind.Unreachable, f.kind)
    }

    @Test
    fun `a busy home says so`() = runBlocking {
        val f = failed(runLoad("test", out) { throw ApiException.Status(503, "") }).failure
        assertEquals(ErrorKind.Busy, f.kind)
    }

    @Test
    fun `an answer the app cannot read is the home's fault, not a crash`() = runBlocking {
        val f = failed(runLoad("test", out) { throw ApiException.Decoding("Unexpected JSON token") }).failure
        assertEquals(ErrorKind.Server, f.kind)
    }

    @Test
    fun `a sign-out goes back to signing in`() = runBlocking {
        val f = failed(runLoad("test", out) { throw ApiException.SignedOut }).failure
        assertEquals(ErrorKind.SignedOut, f.kind)
        assertEquals(1, signedOut)
    }

    @Test
    fun `a cancelled load stays cancelled`() = runBlocking {
        try {
            runLoad("test", out) { throw CancellationException("left the screen") }
            fail("the cancellation was swallowed")
        } catch (e: CancellationException) {
            assertFalse(signedOut > 0)
        }
    }
}
