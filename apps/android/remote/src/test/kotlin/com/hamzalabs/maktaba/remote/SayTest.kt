package com.hamzalabs.maktaba.remote

import com.hamzalabs.maktaba.core.api.CatalogRelease
import com.hamzalabs.maktaba.core.api.HealthFailing
import com.hamzalabs.maktaba.core.api.HealthWaiting
import com.hamzalabs.maktaba.core.api.ProcessEvent
import com.hamzalabs.maktaba.core.api.ProcessStatus
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import com.hamzalabs.maktaba.core.api.StageHealth
import com.hamzalabs.maktaba.core.api.TvCheck
import com.hamzalabs.maktaba.core.api.UpdateRun
import com.hamzalabs.maktaba.core.api.UpdatesOverview
import com.hamzalabs.maktaba.core.api.UpdatesPlan
import com.hamzalabs.maktaba.core.api.UpdatesStuck
import com.hamzalabs.maktaba.core.api.UpdatesTick
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import java.time.ZoneOffset

/** The status screens say what the web admin says, and show a value they have no words for as sent. */
class SayTest {
    private val utc = ZoneOffset.UTC
    private val en = Say(XmlLookup.english, utc)
    private val ar = Say(XmlLookup.arabic, utc)

    private fun plan(p: UpdatesPlan, run: UpdateRun? = null) = UpdatesOverview(plan = p, run = run)

    @Test
    fun theNextUpdateReadsAsASentence() {
        assertEquals("Nothing to install: up to date.", en.planNext(plan(UpdatesPlan())))
        val target = CatalogRelease(version = "0.3.44")
        assertEquals(
            "Version 0.3.44 installs on Sun 1 Nov, 02:00.",
            en.planNext(plan(UpdatesPlan(target = target, nextRunAt = "2026-11-01T02:00:00Z"))),
        )
        assertEquals(
            "Version 0.3.44 is available; automatic install is off.",
            en.planNext(plan(UpdatesPlan(target = target, waiting = "auto_install_off"))),
        )
        assertEquals(
            "Version 0.3.44 waits for your approval (Live TV -> Changes).",
            en.planNext(plan(UpdatesPlan(target = target, held = true, reason = "held"))),
        )
        // the run's gates, in the run's words
        assertEquals(
            "The last try failed: the next one in a few minutes.",
            en.planNext(plan(UpdatesPlan(target = target, waiting = "retry_backoff"))),
        )
        // a reason this app has no words for
        assertEquals("Version 0.3.44: moon phase.", en.planNext(plan(UpdatesPlan(target = target, waiting = "moon_phase"))))
        assertEquals(
            "Updating to 0.3.44 now.",
            en.planNext(plan(UpdatesPlan(target = target), UpdateRun(id = "r", version = "0.3.44", state = "rolling_out"))),
        )
    }

    @Test
    fun theSchedulersReasonIsSaid() {
        val ov = UpdatesOverview(
            plan = UpdatesPlan(target = CatalogRelease(version = "1.0.0")),
            lastTick = UpdatesTick(action = "blocked", reason = "recent_crash"),
        )
        assertEquals("Not started now: a part of the home's server crashed in the last ten minutes", en.tick(ov))
        assertNull(en.tick(ov.copy(lastTick = UpdatesTick(action = "started", reason = "x"))))
    }

    @Test
    fun statesAndCodesUseTheWebsWords() {
        assertEquals("downloading the update", en.runState("ordered"))
        assertEquals("ready: installs at its turn", en.runState("app_staged"))
        assertEquals("watching the new release", en.runState("watching"))
        assertEquals("needs you", en.runState("needs_person"))
        assertEquals("quantum flux", en.runState("quantum_flux"))
        assertEquals("Download and verify", en.step("stage"))
        assertEquals("warp drive", en.step("warp_drive"))
        assertEquals("The release's signature is not valid", en.code("verify_signature"))
        assertEquals("The release could not be verified", en.code("verify_something_new"))
        assertEquals("Keep: failed", en.code("commit_failed"))
        assertEquals("gremlins", en.code("gremlins"))
        assertEquals(
            "0.3.43 is blocked: its migrations failed on a copy of the database",
            en.blocked(CatalogRelease(version = "0.3.43", blockedReason = "rehearsal")),
        )
        assertEquals("0.3.43 is blocked: cosmic rays", en.blocked(CatalogRelease(version = "0.3.43", blockedReason = "cosmic_rays")))
    }

    @Test
    fun aHeldTvSaysWhereItStays() {
        assertEquals("Installed. TV held on 0.3.41: its check failed", en.tv("held", "0.3.41", "0.3.42"))
        assertEquals("Installed. TV held on 0.3.41: its check failed", en.tv(null, "0.3.41", "0.3.42"))
        assertEquals("Installed. The TV change passed on a retry: the TV runs 0.3.42", en.tv("live", "0.3.41", "0.3.42"))
        assertEquals("TV: on vacation", en.tv("on_vacation", "0.3.41", "0.3.42"))
    }

    @Test
    fun processesAreNamedByRoleAndSlot() {
        assertEquals("API", en.process("api", null))
        assertEquals("Live TV and streaming", en.process("streaming", ""))
        assertEquals("TV slot · amber", en.process("tv-slot", "amber"))
        assertEquals("postgres", en.process("postgres", null))
        assertEquals("Not responding", en.processState("unhealthy"))
        assertEquals("levitating", en.processState("levitating"))
        assertEquals("Standing by", en.processState(ProcessStatus(role = "tv-slot", instance = "amber", wanted = false, state = "stopped")))
        assertEquals("Stopped", en.processState(ProcessStatus(role = "api", state = "stopped")))
        assertEquals(
            "Installing the transcription tools (40%).",
            en.processDetail(ProcessStatus(role = "pipeline", detailCode = "venv_building", detail = "venv 40%", detailParams = buildJsonObject { put("percent", 40) })),
        )
        assertEquals("something new", en.processDetail(ProcessStatus(role = "pipeline", detailCode = "brand_new", detail = "something new")))
        assertEquals("killed by SIGKILL", en.exit("crash", -1, "SIGKILL"))
        assertEquals("exited with code 1", en.exit("crash", 1, null))
        assertEquals("stopped responding and was restarted", en.exit("hang", -1, null))
        assertEquals("TV slot · blue: restarting", en.event(ProcessEvent(role = "tv-slot", instance = "blue", kind = "restarting")))
        assertEquals("1h 2m", en.duration(3725.0))
        assertEquals("2d 3h", en.duration(2 * 86400.0 + 3 * 3600))
    }

    @Test
    fun recentCrashesAreTheLastFiveNewestFirst() {
        val events = (1..8).map { ProcessEvent(seq = it.toLong(), role = "api", kind = "exited", reason = if (it == 2) "clean" else "crash") } +
            ProcessEvent(seq = 9, role = "api", kind = "restarting")
        assertEquals(listOf(8L, 7L, 6L, 5L, 4L), recentCrashes(events).map { it.seq })
    }

    @Test
    fun aStageSaysWhyItWaitsAndHowItIs() {
        assertEquals("Plex not answering, retrying", en.queueWaiting("service_retrying", "plex", "Sync"))
        assertEquals("Waiting for a worker that serves Transcribe", en.queueWaiting("no_worker", null, "Transcribe"))
        assertEquals("Waiting for a free worker", en.queueWaiting("pool_full", null, "x"))
        assertEquals("solar flare", en.queueWaiting("solar_flare", null, "x"))
        assertNull(en.queueWaiting(null, null, "x"))
        assertEquals("the share smb://nas/O", en.waitingOn("dependency:share:smb://nas/O"))
        val failing = StageHealth(state = "failing", needsYou = 2, failing = HealthFailing(signature = "CUDA out of memory", count = 3))
        assertEquals(listOf("3 failing: CUDA out of memory", "4 failed", "2 need you"), en.health(failing, 4))
        val waiting = StageHealth(state = "waiting", waiting = listOf(HealthWaiting(on = "dependency:model:tagcheck", count = 2), HealthWaiting(on = "resource:disk", count = 1)))
        assertEquals(listOf("3 waiting for the model, resume by themselves"), en.health(waiting, 0))
        assertEquals(listOf("2 failing"), en.health(null, 2))
    }

    @Test
    fun aTimeThatIsNotOneIsShownAsSent() {
        assertEquals("soon", en.whenText("soon"))
        assertEquals("soon", ar.whenText("soon"))
    }

    // ---------- in Arabic: the web's ar.json words, numbers and dates in Arabic ----------

    @Test
    fun theNextUpdateReadsAsAnArabicSentence() {
        val target = CatalogRelease(version = "0.3.44")
        assertEquals("لا شيء للتثبيت: محدَّث.", ar.planNext(plan(UpdatesPlan())))
        assertEquals(
            "الإصدار 0.3.44 ينتظر موافقتك (البث المباشر -> التغييرات).",
            ar.planNext(plan(UpdatesPlan(target = target, held = true, reason = "held"))),
        )
        assertEquals(
            "الإصدار 0.3.44 متاح؛ التثبيت التلقائي متوقف.",
            ar.planNext(plan(UpdatesPlan(target = target, waiting = "auto_install_off"))),
        )
        // the day, the month and the digits are Arabic; the version stays as the home sent it
        assertEquals(
            "يُثبَّت الإصدار 0.3.44 في الأحد ١ نوفمبر, ٠٢:٠٠.",
            ar.planNext(plan(UpdatesPlan(target = target, nextRunAt = "2026-11-01T02:00:00Z"))),
        )
        assertEquals(
            "يجري التحديث إلى 0.3.44 الآن.",
            ar.planNext(plan(UpdatesPlan(target = target), UpdateRun(id = "r", version = "0.3.44", state = "rolling_out"))),
        )
        // a reason the app has no words for: shown as sent, in an Arabic sentence
        assertEquals("الإصدار 0.3.44: moon phase.", ar.planNext(plan(UpdatesPlan(target = target, waiting = "moon_phase"))))
    }

    @Test
    fun runStatesAndCodesInArabic() {
        assertEquals("يُنزَّل التحديث", ar.runState("ordered"))
        assertEquals("يحتاجك", ar.runState("needs_person"))
        assertEquals("quantum flux", ar.runState("quantum_flux"))
        assertEquals("الإبقاء: فشل", ar.code("commit_failed"))
        assertEquals("gremlins", ar.code("gremlins"))
        assertEquals("لا يستجيب", ar.processState("unhealthy"))
        assertEquals("منفذ البث التلفزيوني · blue: يُعاد تشغيله", ar.event(ProcessEvent(role = "tv-slot", instance = "blue", kind = "restarting")))
        assertEquals("بانتظار عامل يخدم Transcribe", ar.queueWaiting("no_worker", null, "Transcribe"))
        assertEquals("توقف برمز الخروج ١", ar.exit("crash", 1, null))
        assertEquals("١ س ٢ د", ar.duration(3725.0))
    }

    @Test
    fun countsTakeArabicPlurals() {
        assertEquals("1 job", en.count(R.plurals.jobs, 1))
        assertEquals("3 jobs", en.count(R.plurals.jobs, 3))
        assertEquals("مهمة واحدة", ar.count(R.plurals.jobs, 1))
        assertEquals("مهمتان", ar.count(R.plurals.jobs, 2))
        assertEquals("٣ مهام", ar.count(R.plurals.jobs, 3))
        assertEquals("١١ مهمة", ar.count(R.plurals.jobs, 11))
        assertEquals("2 restarts", en.count(R.plurals.restarts_count, 2))
        val stuck = UpdatesStuck(version = "0.3.43", code = "gremlins", windows = 3)
        assertEquals("0.3.43 has not installed in 3 windows: gremlins", en.stuck(stuck))
        assertEquals("0.3.43 has not installed in 1 window: gremlins", en.stuck(stuck.copy(windows = 1)))
        assertEquals("لم يُثبَّت 0.3.43 في ٣ نوافذ: gremlins", ar.stuck(stuck))
        assertEquals("لم يُثبَّت 0.3.43 في نافذتين: gremlins", ar.stuck(stuck.copy(windows = 2)))
    }

    @Test
    fun webWordsHaveTheirStringsInBothLanguages() {
        val maps = listOf(
            WebWords.updateState, WebWords.updateStep, WebWords.updateCode, WebWords.updateWaiting, WebWords.blockedReason,
            WebWords.blocker, WebWords.processState, WebWords.processRole, WebWords.queueState, WebWords.jobState, WebWords.machineStatus,
            WebWords.tvCheck, WebWords.tvResult, WebWords.trigger,
        )
        for (lookup in listOf(XmlLookup.english, XmlLookup.arabic)) {
            maps.flatMap { it.values }.forEach { assert(lookup.text(it).isNotBlank()) }
            (WebWords.planWaiting + WebWords.queueWaiting + WebWords.processDetail + WebWords.tvValue).values.forEach { t ->
                assert(lookup.text(t.id, *t.args.map { "x" }.toTypedArray()).isNotBlank())
            }
        }
    }

    // ---------- a TV change's check: its code and params, in this app's language ----------

    private val mismatch = TvCheck(
        check = "slot.health", result = "fail", code = "slot.release_mismatch",
        value = "the staging slot runs another release (release_mismatch)",
    )

    @Test
    fun aTvCheckIsSaidByItsCode() {
        assertEquals("Staging process: fail · The staging slot runs another release", en.tvCheck(mismatch))
        assertEquals("عملية التجهيز: فاشل · خانة التجهيز تشغّل إصدارًا آخر", ar.tvCheck(mismatch))
        val started = TvCheck(
            check = "slot.health", result = "fail", code = "slot.start_failed",
            value = "the staging slot did not start: no slots", params = buildJsonObject { put("error", "no slots") },
        )
        assertEquals("لم تبدأ خانة التجهيز: no slots", ar.tvValue(started))
        val gaps = TvCheck(check = "guide.coverage", channelId = "c1", result = "fail", code = "guide.gaps",
            value = "3 gaps in the next 24 h", params = buildJsonObject { put("gaps", "3") })
        assertEquals("تغطية الدليل (c1): فاشل · 3 فجوات في الساعات الـ24 القادمة", ar.tvCheck(gaps))
    }

    @Test
    fun aTvCheckWithAnUnknownCodeShowsTheHomesEnglish() {
        val newer = TvCheck(check = "moon.phase", result = "fail", code = "moon.waxing", value = "the moon is waxing")
        assertEquals("moon.phase: فاشل · the moon is waxing", ar.tvCheck(newer))
        // an older home sends no code: its English
        assertEquals("the staging slot runs another release (release_mismatch)", ar.tvValue(mismatch.copy(code = null)))
    }

    @Test
    fun whoStartedAnUpdateIsSaid() {
        assertEquals("the quiet window", en.trigger("auto"))
        assertEquals("النافذة الهادئة", ar.trigger("auto"))
        assertEquals("جهاز يلحق بالبقية", ar.trigger("catch_up"))
        assertEquals("solar flare", ar.trigger("solar_flare"))
    }
}
