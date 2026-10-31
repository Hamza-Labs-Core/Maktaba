package com.hamzalabs.maktaba.remote

import com.hamzalabs.maktaba.core.api.CatalogRelease
import com.hamzalabs.maktaba.core.api.HealthFailing
import com.hamzalabs.maktaba.core.api.HealthWaiting
import com.hamzalabs.maktaba.core.api.ProcessEvent
import com.hamzalabs.maktaba.core.api.ProcessStatus
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import com.hamzalabs.maktaba.core.api.StageHealth
import com.hamzalabs.maktaba.core.api.UpdateRun
import com.hamzalabs.maktaba.core.api.UpdatesOverview
import com.hamzalabs.maktaba.core.api.UpdatesPlan
import com.hamzalabs.maktaba.core.api.UpdatesTick
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import java.time.ZoneOffset
import java.util.Locale

/** The status screens say what the web admin says, and show a value they have no words for as sent. */
class SayTest {
    private val utc = ZoneOffset.UTC
    private val en = Locale.ENGLISH

    private fun plan(p: UpdatesPlan, run: UpdateRun? = null) = UpdatesOverview(plan = p, run = run)

    @Test
    fun theNextUpdateReadsAsASentence() {
        assertEquals("Nothing to install: up to date.", Say.planNext(plan(UpdatesPlan()), utc, en))
        val target = CatalogRelease(version = "0.3.44")
        assertEquals(
            "Version 0.3.44 installs on Sun 1 Nov, 02:00.",
            Say.planNext(plan(UpdatesPlan(target = target, nextRunAt = "2026-11-01T02:00:00Z")), utc, en),
        )
        assertEquals(
            "Version 0.3.44 is available; automatic install is off.",
            Say.planNext(plan(UpdatesPlan(target = target, waiting = "auto_install_off")), utc, en),
        )
        assertEquals(
            "Version 0.3.44 waits for your approval (Live TV -> Changes).",
            Say.planNext(plan(UpdatesPlan(target = target, held = true, reason = "held")), utc, en),
        )
        // the run's gates, in the run's words
        assertEquals(
            "The last try failed: the next one in a few minutes.",
            Say.planNext(plan(UpdatesPlan(target = target, waiting = "retry_backoff")), utc, en),
        )
        // a reason this app has no words for
        assertEquals("Version 0.3.44: moon phase.", Say.planNext(plan(UpdatesPlan(target = target, waiting = "moon_phase")), utc, en))
        assertEquals(
            "Updating to 0.3.44 now.",
            Say.planNext(plan(UpdatesPlan(target = target), UpdateRun(id = "r", version = "0.3.44", state = "rolling_out")), utc, en),
        )
    }

    @Test
    fun theSchedulersReasonIsSaid() {
        val ov = UpdatesOverview(
            plan = UpdatesPlan(target = CatalogRelease(version = "1.0.0")),
            lastTick = UpdatesTick(action = "blocked", reason = "recent_crash"),
        )
        assertEquals("Not started now: a part of the home's server crashed in the last ten minutes", Say.tick(ov))
        assertNull(Say.tick(ov.copy(lastTick = UpdatesTick(action = "started", reason = "x"))))
    }

    @Test
    fun statesAndCodesUseTheWebsWords() {
        assertEquals("downloading the update", Say.runState("ordered"))
        assertEquals("ready: installs at its turn", Say.runState("app_staged"))
        assertEquals("watching the new release", Say.runState("watching"))
        assertEquals("needs you", Say.runState("needs_person"))
        assertEquals("quantum flux", Say.runState("quantum_flux"))
        assertEquals("Download and verify", Say.step("stage"))
        assertEquals("warp drive", Say.step("warp_drive"))
        assertEquals("The release's signature is not valid", Say.code("verify_signature"))
        assertEquals("The release could not be verified", Say.code("verify_something_new"))
        assertEquals("Keep: failed", Say.code("commit_failed"))
        assertEquals("gremlins", Say.code("gremlins"))
        assertEquals(
            "0.3.43 is blocked: its migrations failed on a copy of the database",
            Say.blocked(CatalogRelease(version = "0.3.43", blockedReason = "rehearsal")),
        )
        assertEquals("0.3.43 is blocked: cosmic rays", Say.blocked(CatalogRelease(version = "0.3.43", blockedReason = "cosmic_rays")))
    }

    @Test
    fun aHeldTvSaysWhereItStays() {
        assertEquals("Installed. TV held on 0.3.41: its check failed", Say.tv("held", "0.3.41", "0.3.42"))
        assertEquals("Installed. TV held on 0.3.41: its check failed", Say.tv(null, "0.3.41", "0.3.42"))
        assertEquals("Installed. The TV change passed on a retry: the TV runs 0.3.42", Say.tv("live", "0.3.41", "0.3.42"))
        assertEquals("TV: on vacation", Say.tv("on_vacation", "0.3.41", "0.3.42"))
    }

    @Test
    fun processesAreNamedByRoleAndSlot() {
        assertEquals("API", Say.process("api", null))
        assertEquals("Live TV and streaming", Say.process("streaming", ""))
        assertEquals("TV slot · amber", Say.process("tv-slot", "amber"))
        assertEquals("postgres", Say.process("postgres", null))
        assertEquals("Not responding", Say.processState("unhealthy"))
        assertEquals("levitating", Say.processState("levitating"))
        assertEquals("Standing by", Say.processState(ProcessStatus(role = "tv-slot", instance = "amber", wanted = false, state = "stopped")))
        assertEquals("Stopped", Say.processState(ProcessStatus(role = "api", state = "stopped")))
        assertEquals(
            "Installing the transcription tools (40%).",
            Say.processDetail(ProcessStatus(role = "pipeline", detailCode = "venv_building", detail = "venv 40%", detailParams = buildJsonObject { put("percent", 40) })),
        )
        assertEquals("something new", Say.processDetail(ProcessStatus(role = "pipeline", detailCode = "brand_new", detail = "something new")))
        assertEquals("killed by SIGKILL", Say.exit("crash", -1, "SIGKILL"))
        assertEquals("exited with code 1", Say.exit("crash", 1, null))
        assertEquals("stopped responding and was restarted", Say.exit("hang", -1, null))
        assertEquals("TV slot · blue: restarting", Say.event(ProcessEvent(role = "tv-slot", instance = "blue", kind = "restarting")))
        assertEquals("1h 2m", Say.duration(3725.0))
        assertEquals("2d 3h", Say.duration(2 * 86400.0 + 3 * 3600))
    }

    @Test
    fun recentCrashesAreTheLastFiveNewestFirst() {
        val events = (1..8).map { ProcessEvent(seq = it.toLong(), role = "api", kind = "exited", reason = if (it == 2) "clean" else "crash") } +
            ProcessEvent(seq = 9, role = "api", kind = "restarting")
        assertEquals(listOf(8L, 7L, 6L, 5L, 4L), recentCrashes(events).map { it.seq })
    }

    @Test
    fun aStageSaysWhyItWaitsAndHowItIs() {
        assertEquals("Plex not answering, retrying", Say.queueWaiting("service_retrying", "plex", "Sync"))
        assertEquals("Waiting for a worker that serves Transcribe", Say.queueWaiting("no_worker", null, "Transcribe"))
        assertEquals("Waiting for a free worker", Say.queueWaiting("pool_full", null, "x"))
        assertEquals("solar flare", Say.queueWaiting("solar_flare", null, "x"))
        assertNull(Say.queueWaiting(null, null, "x"))
        assertEquals("the share smb://nas/O", Say.waitingOn("dependency:share:smb://nas/O"))
        val failing = StageHealth(state = "failing", needsYou = 2, failing = HealthFailing(signature = "CUDA out of memory", count = 3))
        assertEquals(listOf("3 failing: CUDA out of memory", "4 failed", "2 need you"), Say.health(failing, 4))
        val waiting = StageHealth(state = "waiting", waiting = listOf(HealthWaiting(on = "dependency:model:tagcheck", count = 2), HealthWaiting(on = "resource:disk", count = 1)))
        assertEquals(listOf("3 waiting for the model, resume by themselves"), Say.health(waiting, 0))
        assertEquals(listOf("2 failing"), Say.health(null, 2))
    }

    @Test
    fun aTimeThatIsNotOneIsShownAsSent() {
        assertEquals("soon", Say.whenText("soon", utc, en))
    }
}
