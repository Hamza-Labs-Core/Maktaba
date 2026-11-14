package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.CloudTvCode
import com.hamzalabs.maktaba.core.ui.ApproveCode
import com.hamzalabs.maktaba.core.ui.ApproveProblem
import com.hamzalabs.maktaba.core.ui.ApproveStep
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
import org.junit.Before
import org.junit.Test
import java.util.Collections
import java.util.Locale

/**
 * "Approve a TV or device"'s steps (Epic 42 story 42.6, Epic 58 story 58.03), against a home on
 * MockWebServer: the home's code first; a cloud-shaped code the home does not know is looked up
 * through the home, confirmed (platform and country), and only then approved.
 */
class ApproveCodeTest {
    private lateinit var server: MockWebServer
    private val seen: MutableList<RecordedRequest> = Collections.synchronizedList(mutableListOf())
    private val routes = mutableMapOf<String, MockResponse>()
    private val client by lazy { ApiClient(server.url("/"), MemoryTokens("t", "r")) }

    private fun json(code: Int, body: String) =
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    private val homeUnknown get() = json(404, """{"type":"about:blank","title":"not found","status":404}""")
    private val tizenInJordan get() = json(200, """{"code":"ABCD-2345","platform":"tizen","app_version":"1.0.0","country":"JO","expires_at":"2027-01-15T08:10:00Z"}""")
    private val approved get() = json(200, """{"status":"approved","user_id":"u1","platform":"tizen","country":"JO"}""")

    private val paths: List<String> get() = seen.map { "${it.method} ${it.url.encodedPath}" }

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                seen += request
                return routes[request.url.encodedPath] ?: homeUnknown
            }
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    @Test
    fun aCloudCodeTheHomeDoesNotKnowIsLookedUpConfirmedThenApproved() = runBlocking {
        routes["/api/pairing/cloud-code/ABCD2345"] = tizenInJordan
        routes["/api/pairing/cloud-approve"] = approved
        val flow = ApproveCode(client)
        flow.lookUp("abcd-2345")
        val confirm = flow.step as ApproveStep.CloudConfirm
        assertEquals("tizen", confirm.tv.platform)
        assertEquals("Jordan", ApproveCode.countryName(confirm.tv, Locale.ENGLISH))
        assertEquals("Tizen", ApproveCode.platformName(confirm.tv))
        assertNull(flow.problem)
        // asked: the home's code, then the cloud's; nothing approved before the person confirms
        assertEquals(listOf("GET /api/pairing/device/ABCD2345", "GET /api/pairing/cloud-code/ABCD2345"), paths)
        flow.approve()
        assertEquals(ApproveStep.CloudDone, flow.step)
        assertEquals("POST /api/pairing/cloud-approve", paths.last())
        assertEquals("""{"code":"ABCD2345"}""", seen.last().body!!.utf8())
        assertFalse("never the home's approve", paths.contains("POST /api/pairing/approve"))
    }

    @Test
    fun aCodeTheHomeKnowsStaysOnTheHomesPath() = runBlocking {
        // cloud-shaped, yet the home holds it: the home's own code
        routes["/api/pairing/device/ABCD2345"] = json(200, """{"code":"ABCD-2345","client_app":"tv","platform":"androidtv","device":"tv","device_label":"Living room TV"}""")
        routes["/api/pairing/approve"] = json(200, """{"status":"approved"}""")
        val flow = ApproveCode(client)
        flow.lookUp("ABCD-2345")
        assertEquals("Living room TV", (flow.step as ApproveStep.Confirm).request?.deviceLabel)
        flow.approve()
        assertEquals(ApproveStep.Done("Living room TV"), flow.step)
        assertEquals(listOf("GET /api/pairing/device/ABCD2345", "POST /api/pairing/approve"), paths)
        assertTrue(seen.last().body!!.utf8().contains("\"code\":\"ABCD-2345\""))
    }

    @Test
    fun aHomeShapedCodeTheHomeDoesNotKnowIsNotAskedOfTheCloud() = runBlocking {
        val flow = ApproveCode(client)
        flow.lookUp("ABCD-1234")
        assertEquals(ApproveStep.Enter, flow.step)
        assertEquals(ApproveProblem.Unknown, flow.problem)
        assertEquals(listOf("GET /api/pairing/device/ABCD1234"), paths)
    }

    @Test
    fun anOlderHomeStillApprovesWithoutTheLookup() = runBlocking {
        routes["/api/pairing/device/ABCD2345"] = MockResponse.Builder().code(404).body("404 page not found").build()
        routes["/api/pairing/approve"] = json(200, """{"status":"approved"}""")
        val flow = ApproveCode(client)
        flow.lookUp("ABCD-2345")
        assertEquals(ApproveStep.Confirm("ABCD2345", null), flow.step)
        flow.approve()
        assertEquals(ApproveStep.Done(null), flow.step)
        assertFalse(paths.any { it.contains("cloud") })
    }

    @Test
    fun aHomeWithoutRemoteAccessSaysToTurnItOn() = runBlocking {
        routes["/api/pairing/cloud-code/ABCD2345"] = json(409, """{"title":"Conflict","status":409,"code":"cloud_not_linked"}""")
        val flow = ApproveCode(client)
        flow.lookUp("ABCD-2345")
        assertEquals(ApproveStep.Enter, flow.step)
        assertEquals(ApproveProblem.NeedsRemote, flow.problem)
        assertFalse(paths.any { it.startsWith("POST") })
    }

    @Test
    fun anUnknownCloudCodeIsTheUsualNotFound() = runBlocking {
        val flow = ApproveCode(client)
        flow.lookUp("ABCD-2345")
        assertEquals(ApproveProblem.Unknown, flow.problem)
        assertEquals(listOf("GET /api/pairing/device/ABCD2345", "GET /api/pairing/cloud-code/ABCD2345"), paths)
    }

    @Test
    fun aRefusedCloudApprovalSaysWhy() = runBlocking {
        routes["/api/pairing/cloud-code/ABCD2345"] = tizenInJordan
        val flow = ApproveCode(client)
        suspend fun refusedWith(answer: MockResponse): ApproveProblem? {
            routes["/api/pairing/cloud-approve"] = answer
            flow.reset()
            flow.lookUp("ABCD-2345")
            flow.approve()
            assertTrue("the TV stays on screen", flow.step is ApproveStep.CloudConfirm)
            assertFalse(flow.busy)
            return flow.problem
        }
        assertEquals(ApproveProblem.NeedsRemote, refusedWith(json(409, """{"title":"Conflict","status":409,"code":"cloud_not_linked"}""")))
        assertEquals(ApproveProblem.Already, refusedWith(json(409, """{"title":"Conflict","status":409,"code":"already_used"}""")))
        assertEquals(ApproveProblem.Unknown, refusedWith(json(404, """{"title":"not found","status":404}""")))
        assertEquals(ApproveProblem.FromCloud, refusedWith(json(403, """{"title":"Forbidden","status":403}""")))
    }

    @Test
    fun notACodeIsNotAsked() = runBlocking {
        val flow = ApproveCode(client)
        flow.lookUp("nope")
        assertEquals(ApproveProblem.Invalid, flow.problem)
        assertTrue(seen.isEmpty())
    }

    @Test
    fun theCountryIsNamedInTheReadersLanguageOrLeftOut() {
        val tv = CloudTvCode(code = "ABCD-2345", platform = "webos", country = "JO")
        assertEquals("Jordan", ApproveCode.countryName(tv, Locale.ENGLISH))
        assertEquals("الأردن", ApproveCode.countryName(tv, Locale.forLanguageTag("ar")))
        assertNull("the cloud could not tell", ApproveCode.countryName(tv.copy(country = ""), Locale.ENGLISH))
        assertNull(ApproveCode.countryName(tv.copy(country = null), Locale.ENGLISH))
        assertNull("a TV that gave no platform", ApproveCode.platformName(tv.copy(platform = " ")))
    }
}
