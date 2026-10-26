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

    private fun code(n: Int) = """{"user_code":"ABCD-000$n","device_code":"secret-$n",
        "verification_uri":"http://home:8088/link","verification_uri_complete":"http://home:8088/link?code=ABCD-000$n",
        "expires_in":600,"interval":5}"""

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
                "/api/auth/device/start" -> json(201, code(1))
                "/api/auth/device/token" -> if (++polls < 3) json(202, """{"status":"pending"}""")
                    else json(200, """{"access_token":"a1","access_expires_in":900,"refresh_token":"r1","user_id":"u1"}""")
                else -> json(404, "{}")
            }
        }
        val shown = mutableListOf<DeviceCode>()
        val tokens = signIn().run { shown += it }
        assertEquals("a1", tokens.accessToken)
        assertEquals("r1", tokens.refreshToken)
        assertEquals(listOf("ABCD-0001"), shown.map { it.userCode })
        assertEquals("http://home:8088/link?code=ABCD-0001", shown.single().verificationUriComplete)
        val start = seen.first()
        assertEquals("native", start.headers["X-Maktaba-Client"])
        assertTrue("names the device", start.body!!.utf8().contains("\"device_label\":\"Android TV (test)\""))
        // polls carry the TV's secret, never the code on the screen, at the home's interval
        val polls1 = seen.filter { it.url.encodedPath == "/api/auth/device/token" }
        assertEquals(3, polls1.size)
        assertTrue(polls1.all { it.body!!.utf8() == """{"device_code":"secret-1"}""" })
        assertEquals(15_000L, clock)
    }

    @Test
    fun aCodeThatRanOutIsReplacedByANewOne() = runBlocking {
        var starts = 0
        route = { r ->
            when (r.url.encodedPath) {
                "/api/auth/device/start" -> json(201, code(++starts))
                "/api/auth/device/token" ->
                    if (r.body!!.utf8().contains("secret-1")) json(410, """{"title":"code expired"}""")
                    else json(200, """{"access_token":"a2","refresh_token":"r2"}""")
                else -> json(404, "{}")
            }
        }
        val shown = mutableListOf<String>()
        val tokens = signIn().run { shown += it.userCode }
        assertEquals(listOf("ABCD-0001", "ABCD-0002"), shown)
        assertEquals("a2", tokens.accessToken)
    }

    @Test
    fun aCodeIsNotPolledPastItsExpiry() = runBlocking {
        var starts = 0
        route = { r ->
            when (r.url.encodedPath) {
                "/api/auth/device/start" -> json(201, code(++starts))
                else -> if (starts == 1) json(202, "{}") else json(200, """{"access_token":"a","refresh_token":"r"}""")
            }
        }
        val shown = mutableListOf<String>()
        signIn().run { shown += it.userCode }
        assertEquals(2, shown.size)
        // 600 s at one poll each 5 s: 120 polls for the first code, then one for the second
        assertEquals(121, seen.count { it.url.encodedPath == "/api/auth/device/token" })
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
