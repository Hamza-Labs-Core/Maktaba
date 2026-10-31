package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.CatalogRelease
import com.hamzalabs.maktaba.core.api.UpdatesOverview
import com.hamzalabs.maktaba.core.api.Versions
import com.hamzalabs.maktaba.core.api.text
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

/** The Remote's read-only status routes, decoded from answers shaped as the home sends them. */
class StatusTest {
    private lateinit var server: MockWebServer
    private val seen: MutableList<RecordedRequest> = Collections.synchronizedList(mutableListOf())
    private val token = jwt(4_000_000_000L)

    private fun fixture(name: String): String =
        requireNotNull(javaClass.getResource("/status/$name")) { "no fixture $name" }.readText()

    private val routes = mapOf(
        "/api/cluster/updates" to "updates.json",
        "/api/cluster/updates/runs/8d7c1f1e-5b8a-4c1e-9d0e-0a1b2c3d4e5f" to "update-run.json",
        "/api/system/processes" to "processes.json",
        "/api/queues/running" to "running.json",
        "/api/queues" to "queues.json",
        "/api/queues/transcribe/failures" to "failures.json",
    )

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                seen += request
                val file = routes[request.url.encodedPath]
                    ?: return MockResponse.Builder().code(404).body("{}").build()
                return MockResponse.Builder().code(200).setHeader("Content-Type", "application/json").body(fixture(file)).build()
            }
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    private fun client() = ApiClient(server.url("/"), MemoryTokens(token, "r"))

    @Test
    fun updatesDecodeWithUnknownStatesAndFields() = runBlocking {
        val ov = client().updates()
        assertEquals("0.3.41", ov.currentVersion)
        assertTrue(ov.checkStale)
        assertEquals("github: 403 rate limited", ov.lastCheckError)
        assertEquals("0.3.44", ov.plan.target?.version)
        assertEquals("outside_window", ov.plan.waiting)
        assertEquals("2026-11-01T02:00:00Z", ov.plan.nextRunAt)
        assertEquals("rolling_out", ov.activeRun?.state)
        assertEquals("did_not_stay_up", ov.lastRun?.error.text("code"))
        assertEquals("api exited twice", ov.lastRun?.error.text("detail"))
        assertEquals(3, ov.stuck?.windows)
        // a state this app has no words for is kept as sent
        assertEquals("quantum_flux", ov.machines[1].state)
        assertEquals("0.3.41", ov.machines[0].lastUpdate?.to)
        assertEquals("0.3.44", ov.machines[0].target)
        val tv = requireNotNull(ov.tvHeld)
        assertEquals("held", tv.state)
        assertEquals("0.3.41", tv.heldOn)
        assertEquals("no segment in 30s", tv.checks.single().value)
        assertEquals("Bearer $token", seen.single().headers["Authorization"])
    }

    @Test
    fun anEndedRunIsNotActive() {
        val ov = ApiClient.json.decodeFromString<UpdatesOverview>(
            """{"run":{"id":"r","version":"1.0.0","state":"done"},"plan":{}}""",
        )
        assertNull(ov.activeRun)
        // an unknown run state counts as under way: the home has not said it is over
        val odd = ApiClient.json.decodeFromString<UpdatesOverview>("""{"run":{"id":"r","state":"warping"}}""")
        assertEquals("warping", odd.activeRun?.state)
    }

    @Test
    fun anUpdateRunDecodesItsStepsAndMachines() = runBlocking {
        val run = client().updateRun("8d7c1f1e-5b8a-4c1e-9d0e-0a1b2c3d4e5f")
        assertEquals("tv_busy", run.detail.waiting)
        assertEquals("retrying", run.detail.tv?.state)
        assertEquals("pi", run.detail.waitingHome.single().name)
        assertEquals(3, run.steps.size)
        assertEquals("downloaded 212 MB", run.steps[1].detail.text("detail"))
        // a step whose detail is not an object still decodes; it says nothing
        assertEquals("warp_drive", run.steps[2].step)
        assertNull(run.steps[2].detail.text("detail"))
        assertEquals(listOf("watching", "needs_person", "teleporting"), run.targets.map { it.state })
        assertEquals("needs_admin", run.targets[1].error.text("code"))
    }

    @Test
    fun processesDecodeTheSlotsAndUnknownStates() = runBlocking {
        val doc = client().processes()
        assertTrue(doc.available)
        assertEquals("mac-mini.local", doc.host)
        assertEquals(listOf("api", "tv-slot/blue", "tv-slot/amber", "pipeline", "hyperdrive"), doc.processes.map { it.id })
        val amber = doc.processes[2]
        assertEquals(true, amber.wanted)
        assertEquals("1024", (amber.detailParams?.get("bytes") as kotlinx.serialization.json.JsonPrimitive).content)
        assertEquals(false, doc.processes[0].wanted)
        assertNull(doc.processes[3].wanted)
        assertEquals("venv_build", amber.detailCode)
        val pipeline = doc.processes[3]
        assertTrue(pipeline.crashLoop)
        assertEquals(6, pipeline.restarts)
        assertEquals("crash", pipeline.lastExit?.reason)
        assertEquals(4, pipeline.lastProbe?.consecutiveFailures)
        assertEquals("levitating", doc.processes[4].state)
        assertEquals("killed", doc.processes[1].lastExit?.signal)
        assertEquals(listOf("exited", "restarting", "failed", "wormhole"), doc.events.map { it.kind })
        assertEquals(-1, doc.events[0].code)
    }

    @Test
    fun anUnsupervisedHomeSaysWhy() {
        val doc = ApiClient.json.decodeFromString<com.hamzalabs.maktaba.core.api.ProcessesDoc>(
            """{"available":false,"reason":"not running under the maktaba-server supervisor","processes":[],"events":[]}""",
        )
        assertFalse(doc.available)
        assertTrue(doc.processes.isEmpty())
    }

    @Test
    fun runningJobsDecodeAndNarrowByHost() = runBlocking {
        val jobs = client().runningJobs()
        assertEquals(2, jobs.size)
        assertEquals("Lecture 4: Fiqh of prayer", jobs[0].subjectLabel)
        assertEquals(0.25f, jobs[0].progress?.fraction)
        assertEquals("w-home-1", jobs[0].claimedBy)
        assertEquals("superposed", jobs[1].state)
        assertNull(jobs[1].progress)
        client().runningJobs("home")
        assertEquals("home", seen.last().url.queryParameter("host"))
    }

    @Test
    fun theQueueCarriesWhyItWaitsAndItsHealth() = runBlocking {
        val snap = client().queues()
        val q = snap.queues.single()
        assertEquals("service_retrying", q.waitingReason)
        assertEquals("plex", q.retryingService)
        val h = requireNotNull(q.health)
        assertEquals("failing", h.state)
        assertEquals("CUDA out of memory", h.failing?.signature)
        assertEquals("dependency:model:tagcheck", h.waiting.single().on)
        assertEquals(1, h.needsYou)
        assertEquals(42.5, snap.health?.cpu?.pct)
        assertEquals(5, snap.health?.backlog?.ready)
        assertEquals(1, snap.health?.nas?.timeouts10m)
    }

    @Test
    fun aStagesFailuresAreGroupedByCause() = runBlocking {
        val groups = client().stageFailures("transcribe")
        assertEquals(listOf("oom", "flux"), groups.map { it.cause })
        assertTrue(groups[0].retryable)
        assertEquals("2026-10-31T07:50:00Z", groups[0].lastAt)
        assertFalse(groups[1].retryable)
        assertEquals("/api/queues/transcribe/failures", seen.last().url.encodedPath)
    }

    // ---------- blocked releases ----------

    private fun ov(current: String, watermark: String = "", vararg catalog: CatalogRelease) =
        UpdatesOverview(currentVersion = current, watermark = watermark, catalog = catalog.toList())

    private fun rel(v: String, blocked: String? = null, withdrawn: Boolean = false) =
        CatalogRelease(version = v, blockedReason = blocked, withdrawnAt = if (withdrawn) "2026-10-30T00:00:00Z" else null)

    @Test
    fun aBlockedReleaseANewerOnePassesIsHistory() = runBlocking {
        // the fixture: 0.3.43 and 0.3.42 are blocked, but 0.3.44 is not, so nothing is shown
        assertEquals(emptyList<String>(), client().updates().blockedReleases.map { it.version })
    }

    @Test
    fun theNewestBlockedReleaseIsShown() {
        val o = ov("0.3.41", "", rel("0.3.44", "rehearsal"), rel("0.3.43"), rel("0.3.42", "attempts"))
        // 0.3.42 is passed by 0.3.43 (not blocked); 0.3.44 is the one to come
        assertEquals(listOf("0.3.44"), o.blockedReleases.map { it.version })
    }

    @Test
    fun onlyReleasesNewerThanTheWatermarkAreBlocked() {
        // the watermark (what the cluster installed) wins over the running version
        val o = ov("0.3.40", "0.3.45", rel("0.3.44", "rolled_back"), rel("0.3.46", "verify"))
        assertEquals(listOf("0.3.46"), o.blockedReleases.map { it.version })
        // no watermark: the running version
        val p = ov("0.3.40", "", rel("0.3.44", "rolled_back"), rel("0.3.39", "attempts"))
        assertEquals(listOf("0.3.44"), p.blockedReleases.map { it.version })
    }

    @Test
    fun aWithdrawnReleaseIsNotBlocked() {
        val o = ov("0.3.41", "", rel("0.3.44", "verify", withdrawn = true))
        assertTrue(o.blockedReleases.isEmpty())
        // a withdrawn release does not pass a blocked one by either
        val p = ov("0.3.41", "", rel("0.3.45", withdrawn = true), rel("0.3.44", "attempts"))
        assertEquals(listOf("0.3.44"), p.blockedReleases.map { it.version })
    }

    @Test
    fun aVersionThatIsNotOneIsKept() {
        // a local build's version cannot be compared: the release is shown, as on the web
        val o = ov("dev", "", rel("0.3.44", "attempts"))
        assertEquals(listOf("0.3.44"), o.blockedReleases.map { it.version })
    }

    @Test
    fun versionsOrderAsTheServers() {
        assertTrue(Versions.compare("0.3.10", "0.3.9") > 0)
        assertTrue(Versions.compare("1.0.0-rc.1", "1.0.0") < 0)
        assertTrue(Versions.compare("1.0.0-rc.2", "1.0.0-rc.10") < 0)
        assertTrue(Versions.compare("1.0.0-alpha", "1.0.0-1") > 0)
        assertTrue(Versions.compare("0.2.0-12-gabc1234", "0.2.0") > 0)
        assertTrue(Versions.compare("0.2.0-12-gabc1234-dirty", "0.2.0-12-gabc1234") > 0)
        assertEquals(0, Versions.compare("v1.2.3", "1.2.3"))
        assertEquals(0, Versions.compare("dev", "1.2.3"))
        assertFalse(Versions.valid("dev"))
    }
}
