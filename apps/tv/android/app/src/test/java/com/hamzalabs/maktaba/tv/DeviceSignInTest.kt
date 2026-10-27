package com.hamzalabs.maktaba.tv

import com.hamzalabs.maktaba.tv.data.DeviceCode
import com.hamzalabs.maktaba.tv.data.DeviceSignIn
import kotlinx.coroutines.runBlocking
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.util.Collections

class DeviceSignInTest {
    private lateinit var server: MockWebServer
    private val seen: MutableList<RecordedRequest> = Collections.synchronizedList(mutableListOf())
    private var route: (RecordedRequest) -> MockResponse = { json(500, "{}") }
    private var clock = 0L

    private fun json(code: Int, body: String) =
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    private fun code(n: Int) = """{"code":"ABCD-000$n","device_code":"secret-$n",
        "expires_at":"2026-10-26T08:10:00Z","verify_path":"/pair?code=ABCD-000$n","interval":5}"""

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                seen += request
                return route(request)
            }
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    /** A sign-in whose waits only move the clock. */
    private fun signIn() = DeviceSignIn(server.url("/"), "Android TV (test)", now = { clock }, sleep = { clock += it })

    @Test
    fun showsTheCodeAndTakesTheTokensOnceApproved() = runBlocking {
        var polls = 0
        route = { r ->
            when (r.url.encodedPath) {
                "/api/pairing/device" -> json(201, code(1))
                "/api/pairing/device/token" -> if (++polls < 3) json(202, """{"status":"pending"}""")
                    else json(200, """{"access_token":"a1","access_expires_in":900,"refresh_token":"r1","user_id":"u1"}""")
                else -> json(404, "{}")
            }
        }
        val shown = mutableListOf<DeviceCode>()
        val tokens = signIn().run { shown += it }
        assertEquals("a1", tokens.accessToken)
        assertEquals("r1", tokens.refreshToken)
        assertEquals(listOf("ABCD-0001"), shown.map { it.code })
        assertEquals(server.url("/pair?code=ABCD-0001"), signIn().approveUrl(shown.single()))
        assertEquals("native", seen.first().headers["X-Maktaba-Client"])
        // polls carry the TV's secret (never the code on the screen) and its name, at the home's interval
        val polls1 = seen.filter { it.url.encodedPath == "/api/pairing/device/token" }
        assertEquals(3, polls1.size)
        assertTrue(polls1.all { it.body!!.utf8() == """{"device_code":"secret-1","device_label":"Android TV (test)"}""" })
        assertEquals(15_000L, clock)
    }

    @Test
    fun aCodeThatRanOutIsReplacedByANewOne() = runBlocking {
        var starts = 0
        route = { r ->
            when (r.url.encodedPath) {
                "/api/pairing/device" -> json(201, code(++starts))
                "/api/pairing/device/token" ->
                    if (r.body!!.utf8().contains("secret-1")) json(410, """{"title":"code expired"}""")
                    else if (r.body!!.utf8().contains("secret-2")) json(409, """{"title":"already collected"}""")
                    else json(200, """{"access_token":"a2","refresh_token":"r2"}""")
                else -> json(404, "{}")
            }
        }
        val shown = mutableListOf<String>()
        val tokens = signIn().run { shown += it.code }
        assertEquals(listOf("ABCD-0001", "ABCD-0002", "ABCD-0003"), shown)
        assertEquals("a2", tokens.accessToken)
    }

    @Test
    fun aCodeIsNotPolledPastItsExpiry() = runBlocking {
        var starts = 0
        route = { r ->
            when (r.url.encodedPath) {
                "/api/pairing/device" -> json(201, code(++starts))
                else -> if (starts == 1) json(202, "{}") else json(200, """{"access_token":"a","refresh_token":"r"}""")
            }
        }
        val shown = mutableListOf<String>()
        signIn().run { shown += it.code }
        assertEquals(2, shown.size)
        // the TV's clock (0) disagrees with expires_at: ten minutes, one poll each 5 s, then the second code
        assertEquals(121, seen.count { it.url.encodedPath == "/api/pairing/device/token" })
    }

    @Test
    fun aHomeWithoutSignInByCodeSaysSo() = runBlocking {
        // an older home's global gate answers 401 to the unknown route
        route = { json(401, """{"title":"Unauthorized"}""") }
        try {
            signIn().run { fail("no code from an older home") }
            fail("expected Unsupported")
        } catch (e: DeviceSignIn.Unsupported) {
            // the TV offers the password sign-in
        }
    }

    @Test
    fun aHomeThatCannotBeReachedIsReportedAfterAFewTries() = runBlocking {
        server.close()
        try {
            signIn().run { fail("no code") }
            fail("expected a transport failure")
        } catch (e: DeviceSignIn.Unsupported) {
            fail("unreachable is not unsupported")
        } catch (e: Exception) {
            assertTrue(e.message!!.startsWith("Could not reach"))
        }
        assertEquals(10_000L, clock)
    }
}
