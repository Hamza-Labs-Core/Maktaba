package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.CloudCode
import com.hamzalabs.maktaba.core.api.CloudCodeLookup
import com.hamzalabs.maktaba.core.api.NotAvailable
import com.hamzalabs.maktaba.core.api.appSignIns
import com.hamzalabs.maktaba.core.api.approveCloudCode
import com.hamzalabs.maktaba.core.api.cloudCode
import com.hamzalabs.maktaba.core.api.cloudNotLinked
import com.hamzalabs.maktaba.core.api.signOutApp
import kotlinx.coroutines.runBlocking
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.util.Collections

/**
 * A TV's Maktaba Cloud code through the home (Epic 58 story 58.03): its shape, the lookup, the
 * approval, and the apps signed in as the person by a code, against a home on MockWebServer in
 * the API's shapes (`api/internal/handlers/discovery/cloudcode.go`).
 */
class CloudCodeTest {
    private lateinit var server: MockWebServer
    private val seen: MutableList<RecordedRequest> = Collections.synchronizedList(mutableListOf())
    private var route: (RecordedRequest) -> MockResponse = { MockResponse.Builder().code(404).build() }
    private val client by lazy { ApiClient(server.url("/"), MemoryTokens("t", "r")) }

    private fun json(code: Int, body: String, type: String = "application/json") =
        MockResponse.Builder().code(code).setHeader("Content-Type", type).body(body).build()

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

    @Test
    fun aCloudCodeHasTheCloudsShape() {
        assertTrue(CloudCode.isCloudShaped("abcd-2345"))
        assertTrue(CloudCode.isCloudShaped(" ABCD 2345 "))
        assertFalse("0 and 1 are the home's only", CloudCode.isCloudShaped("ABCD-1234"))
        assertFalse("too short", CloudCode.isCloudShaped("ABCD-234"))
        assertFalse("U is in neither alphabet", CloudCode.isCloudShaped("ABCU-2345"))
        assertEquals("ABCD2345", CloudCode.compact("abcd-2345"))
    }

    @Test
    fun theLookupSaysWhatTvAsksThenTheApprovalSendsTheCompactCode() = runBlocking {
        route = { r ->
            when (r.url.encodedPath) {
                "/api/pairing/cloud-code/ABCD2345" -> json(200, """{"code":"ABCD-2345","platform":"tizen","app_version":"1.0.0","country":"JO","expires_at":"2027-01-15T08:10:00Z"}""")
                "/api/pairing/cloud-approve" -> json(200, """{"status":"approved","user_id":"u1","platform":"tizen","country":"JO"}""")
                else -> json(404, """{"title":"not found"}""")
            }
        }
        val found = client.cloudCode("abcd-2345") as CloudCodeLookup.Found
        assertEquals("tizen", found.tv.platform)
        assertEquals("JO", found.tv.countryCode)
        assertEquals("1.0.0", found.tv.appVersion)
        assertTrue(found.tv.expiresMillis != null)
        assertEquals("GET", seen[0].method)
        assertEquals("Bearer t", seen[0].headers["Authorization"])
        client.approveCloudCode("abcd-2345")
        assertEquals("/api/pairing/cloud-approve", seen[1].url.encodedPath)
        assertEquals("POST", seen[1].method)
        assertEquals("""{"code":"ABCD2345"}""", seen[1].body!!.utf8())
    }

    @Test
    fun anEmptyCountryIsNone() = runBlocking {
        route = { json(200, """{"code":"ABCD-2345","platform":"webos","country":"","expires_at":"2027-01-15T08:10:00Z"}""") }
        val found = client.cloudCode("ABCD2345") as CloudCodeLookup.Found
        assertNull(found.tv.countryCode)
    }

    @Test
    fun aHomeWithoutRemoteAccessAndAnUnknownCodeAreToldApart() = runBlocking {
        route = { json(409, """{"type":"about:blank","title":"Conflict","status":409,"code":"cloud_not_linked"}""", "application/problem+json") }
        assertEquals(CloudCodeLookup.NotLinked, client.cloudCode("ABCD-2345"))
        route = { json(404, """{"type":"about:blank","title":"not found","status":404}""", "application/problem+json") }
        assertEquals(CloudCodeLookup.NotFound, client.cloudCode("ABCD-2345"))
        route = { MockResponse.Builder().code(404).body("404 page not found").build() }
        assertEquals("a home from before cloud codes", CloudCodeLookup.NotFound, client.cloudCode("ABCD-2345"))
        route = { json(409, """{"title":"Conflict","status":409,"code":"already_used"}""") }
        try {
            client.cloudCode("ABCD-2345")
            fail("another conflict is the home's answer")
        } catch (e: ApiException.Status) {
            assertEquals(409, e.code)
            assertFalse(e.cloudNotLinked)
        }
    }

    @Test
    fun aRefusedApprovalIsTheHomesAnswer() = runBlocking {
        route = { json(409, """{"title":"Conflict","status":409,"code":"cloud_not_linked"}""") }
        try {
            client.approveCloudCode("ABCD-2345")
            fail("refused")
        } catch (e: ApiException.Status) {
            assertTrue(e.cloudNotLinked)
        }
    }

    @Test
    fun theAppSignInsAreReadAndOneSignsOut() = runBlocking {
        route = { r ->
            when (r.url.encodedPath) {
                "/api/me/app-sign-ins" -> json(200, """{"items":[
                    {"id":"a1","kind":"tv","label":"tizen","via":"cloud_code","created_at":"2026-11-10T09:00:00Z","last_seen_at":"2026-11-13T20:00:00Z","expires_at":"2026-12-10T09:00:00Z","current":false},
                    {"id":"a2","kind":"tv","label":"Living room TV","via":"","created_at":"2026-11-01T09:00:00Z","last_seen_at":"2026-11-12T20:00:00Z","expires_at":"2026-12-01T09:00:00Z","current":false},
                    {"kind":"tv"}
                ]}""")
                "/api/me/app-sign-ins/a1" -> MockResponse.Builder().code(204).build()
                else -> json(404, """{"title":"not found","status":404}""")
            }
        }
        val apps = client.appSignIns()
        assertEquals("a row without an id is left out", listOf("a1", "a2"), apps.map { it.id })
        assertTrue(apps[0].viaCloudCode)
        assertEquals("tizen", apps[0].name)
        assertFalse(apps[1].viaCloudCode)
        assertEquals("Living room TV", apps[1].name)
        client.signOutApp("a1")
        assertEquals("DELETE", seen.last().method)
        assertEquals("/api/me/app-sign-ins/a1", seen.last().url.encodedPath)
        try {
            client.signOutApp("a9")
            fail("not the caller's")
        } catch (e: NotAvailable) {
            // the home answered 404
        }
    }

    @Test
    fun aHomeFromBeforeTheListHasNone() = runBlocking {
        route = { MockResponse.Builder().code(404).body("404 page not found").build() }
        assertEquals(emptyList<Any>(), client.appSignIns())
        route = { json(405, "") }
        assertEquals(emptyList<Any>(), client.appSignIns())
    }
}
