package com.hamzalabs.maktaba.remote

import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.FailureGroup
import com.hamzalabs.maktaba.core.api.HealthFailing
import com.hamzalabs.maktaba.core.api.StageHealth
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File
import java.time.ZoneOffset

/**
 * The codes the home sends (stage ids, cause codes, a job's steps, update and install words) read
 * in the app's language: every one the home can send has its words in English and in Arabic, read
 * from the home's own lists (the pipeline's graph and its first-party plugins' manifests, the
 * API's causes, the stages' progress calls), so a new stage, cause or step without words fails here.
 * Only a code this app has no words for shows as the home sent it.
 */
class ServerWordsTest {
    private val en = Say(XmlLookup.english, ZoneOffset.UTC)
    private val ar = Say(XmlLookup.arabic, ZoneOffset.UTC)

    /** The repository's root (the tests run in apps/android/remote). */
    private val repo = File("../../..")

    private fun arabic(s: String) = Regex("[؀-ۿ]").containsMatchIn(s)

    /** Arabic words with no English word left in them (a brand, Plex or YouTube, may stay). */
    private fun allArabic(s: String) = arabic(s) &&
        Regex("[A-Za-z][A-Za-z0-9]+").findAll(s).all { it.value in setOf("Plex", "YouTube", "MP4", "NAS", "Tartube", "ffmpeg", "Postgres", "Docker", "Whisper") }

    // ---------- the home's lists ----------

    /** name -> label of each stage of the graph (engine/graph.py and its _fp() stages' manifests), as stageWords.test.ts reads it. */
    private fun graphStages(): Map<String, String> {
        val src = File(repo, "pipeline/src/maktaba_pipeline/engine/graph.py").readText()
        val out = linkedMapOf<String, String>()
        Regex("""\bname="([a-z0-9_]+)",\s*label="([^"]+)"""").findAll(src).forEach { out[it.groupValues[1]] = it.groupValues[2] }
        Regex("""_legacy\(\s*"([a-z0-9_]+)",\s*"([^"]+)"""").findAll(src).forEach { out[it.groupValues[1]] = it.groupValues[2] }
        val declared = manifestStages()
        Regex("""\b_fp\(\s*"([a-z0-9_]+)"\s*\)""").findAll(src).forEach { m ->
            val name = m.groupValues[1]
            out[name] = declared[name] ?: error("graph.py takes $name from a manifest that does not declare it")
        }
        // a stage a first-party plugin declares outside the graph (Plex Live TV) is the home's too
        declared.forEach { (name, label) -> out.putIfAbsent(name, label) }
        return out
    }

    /** name -> label of each [[stage]] of every first-party plugin's manifest. */
    private fun manifestStages(): Map<String, String> {
        val out = linkedMapOf<String, String>()
        File(repo, "plugins/first-party").listFiles().orEmpty().sortedBy { it.name }.forEach { plugin ->
            val manifest = File(plugin, "manifest.toml").takeIf { it.exists() } ?: return@forEach
            manifest.readText().split(Regex("""(?m)^\[\[stage]]\s*$""")).drop(1).forEach { table ->
                val body = table.split(Regex("""(?m)^\[""")).first()
                val fields = Regex("""(?m)^([a-z_]+)\s*=\s*"([^"]*)"\s*$""").findAll(body).associate { it.groupValues[1] to it.groupValues[2] }
                val name = fields["name"] ?: return@forEach
                out[name] = fields["label"] ?: name
            }
        }
        return out
    }

    /** The causes the home groups failures by: the API's labels and kinds (queues/causes.go) and the engine's codes (engine/causes.py). */
    private fun homeCauses(): Set<String> {
        val go = File(repo, "api/internal/handlers/queues/causes.go").readText()
        val consts = Regex("""(Cause[A-Za-z]+)\s*=\s*"([a-z_]+)"""").findAll(go).associate { it.groupValues[1] to it.groupValues[2] }
        val labels = go.substringAfter("var causeLabels").substringBefore("\n}")
        val kinds = go.substringAfter("var kindLabels").substringBefore("\n}")
        val out = mutableSetOf<String>()
        Regex("""(?m)^\s*"([a-z_]+)":""").findAll(labels + "\n" + kinds).forEach { out += it.groupValues[1] }
        Regex("""(?m)^\s*(Cause[A-Za-z]+):""").findAll(labels).forEach { out += consts.getValue(it.groupValues[1]) }
        val py = File(repo, "pipeline/src/maktaba_pipeline/engine/causes.py").readText()
        Regex("""\(\s*"([a-z_]+)",\s*r["']""").findAll(py).forEach { out += it.groupValues[1] }
        return out
    }

    /** Each step a stage reports (`.progress("<step>", ...)`) in the pipeline and its first-party plugins. */
    private fun progressSteps(): Set<String> {
        val out = mutableSetOf<String>()
        listOf("pipeline/src", "plugins/first-party").forEach { dir ->
            File(repo, dir).walkTopDown().filter { it.isFile && it.extension == "py" }.forEach { f ->
                Regex("""\.progress\(\s*"([^"]+)"""").findAll(f.readText()).forEach { out += it.groupValues[1] }
            }
        }
        return out
    }

    // ---------- every code the home can send has words in both languages ----------

    @Test
    fun everyStageOfTheGraphHasItsTitleInEnglishAndArabic() {
        val stages = graphStages()
        // a parse that lost the graph or the manifests would fall far below this
        assertTrue("${stages.size} stages", stages.size > 50)
        for ((stage, label) in stages) {
            val id = stageWords[stage]
            assertTrue("no words for the stage $stage ($label)", id != null)
            // the English is the graph's label, as the web's
            assertEquals("en $stage", label, en.t(id!!))
            val a = ar.t(id)
            assertTrue("ar $stage: $a", allArabic(a))
            assertNotEquals("ar $stage", label, a)
        }
    }

    @Test
    fun everyCauseTheHomeGroupsByHasItsWordsInEnglishAndArabic() {
        val causes = homeCauses()
        assertTrue("$causes", causes.size > 30 && "disk_full" in causes && "plugin_loop" in causes && "awaiting_review" in causes && "model_unavailable" in causes)
        for (cause in causes) {
            val id = causeWords[cause]
            assertTrue("no words for the cause $cause", id != null)
            assertTrue("en $cause", en.t(id!!).isNotBlank())
            assertTrue("ar $cause: ${ar.t(id)}", arabic(ar.t(id)))
        }
    }

    @Test
    fun everyStepAStageReportsHasItsWordsInEnglishAndArabic() {
        val steps = progressSteps()
        assertTrue("$steps", steps.size > 15 && "exact dates" in steps && "embed" in steps)
        for (step in steps) {
            // its own words, or a stage's title when the stage reports its own name
            val code = step.replace(' ', '_')
            assertTrue("no words for the step $step", code in jobStepWords || code in stageWords)
            assertTrue("en $step", en.jobStep(step).isNotBlank())
            assertTrue("ar $step: ${ar.jobStep(step)}", allArabic(ar.jobStep(step)))
        }
    }

    @Test
    fun everyWordOfTheOtherCodesIsArabicInArabic() {
        val maps = mapOf(
            "job step" to jobStepWords, "subject" to subjectWords, "install kind" to installKindWords,
            "update source" to updateSourceWords, "channel" to channelWords, "disabled" to disabledWords, "cause" to causeWords,
        )
        for ((what, words) in maps) {
            for ((code, id) in words) {
                assertTrue("en $what $code", en.t(id).isNotBlank())
                assertTrue("ar $what $code: ${ar.t(id)}", arabic(ar.t(id)))
            }
        }
    }

    // ---------- each mapping ----------

    @Test
    fun aStageIsTitledInTheAppsLanguage() {
        assertEquals("Chapter frames", en.stageTitle("chapter_thumbs", "Chapter pictures"))
        assertEquals("إطارات الفصول", ar.stageTitle("chapter_thumbs", "Chapter pictures"))
        assertEquals("التفريغ", ar.stageTitle("transcribe"))
        assertEquals("البصمة", ar.stageTitle("fingerprint"))
        assertEquals("المدة", ar.stageTitle("probe", "Probe"))
        assertEquals("البث المباشر في Plex", ar.stageTitle("plex_livetv", "Plex Live TV"))
        // a stage this app has no words for: the home's label, else its id in words
        assertEquals("Future step", ar.stageTitle("from_the_future", "Future step"))
        assertEquals("from the future", ar.stageTitle("from_the_future"))
    }

    @Test
    fun aCauseIsTitledInTheAppsLanguage() {
        assertEquals("لا مساحة متبقية على القرص", ar.causeTitle("disk_full"))
        assertEquals("تعطّلت الإضافة", ar.causeTitle("plugin_trap", "The plugin crashed"))
        assertEquals("The plugin crashed", en.causeTitle("plugin_trap", "The plugin crashed"))
        val group = FailureGroup(stage = "index", cause = "plugin_loop", label = "Stopped a loop between plugins", count = 1, action = "open_settings")
        assertEquals("أُوقفت حلقة بين الإضافات", ar.groupTitle(group))
        // a stage's failing signature is a cause: in words
        val failing = StageHealth(state = "failing", failing = HealthFailing(signature = "nas_timeout", count = 3))
        assertTrue(ar.health(failing, 0).single().endsWith("تفشل: انتهت مهلة القراءة من NAS"))
        assertEquals(listOf("3 failing: NAS read timed out"), en.health(failing, 0))
    }

    @Test
    fun aJobsStepAndSubjectAreSaidInTheAppsLanguage() {
        assertEquals("Reading exact dates", en.jobStep("exact dates"))
        assertEquals("قراءة التواريخ الدقيقة", ar.jobStep("exact dates"))
        assertEquals("الفهرسة للبحث", ar.jobStep("embed"))
        // a stage reporting its own name
        assertEquals("التفريغ", ar.jobStep("transcribe"))
        assertEquals("warp drive", ar.jobStep("warp_drive"))
        assertEquals("فيديو", ar.subjectKind("video"))
        assertEquals("Video", en.subjectKind("video"))
        assertEquals("hologram", ar.subjectKind("hologram"))
    }

    @Test
    fun updatesWordsAreTheWebs() {
        assertEquals("On · Stable channel · quiet window \u206603:00–05:00\u2069", en.autoUpdates(true, "stable", "03:00-05:00"))
        assertEquals("مفعَّلة · قناة مستقر · فترة الهدوء \u206603:00–05:00\u2069", ar.autoUpdates(true, "stable", "03:00-05:00"))
        assertEquals("مفعَّلة · قناة تجريبي", ar.autoUpdates(true, "beta", ""))
        assertEquals(ar.t(R.string.auto_paused), ar.autoUpdates(false, "stable", "03:00-05:00"))
        assertEquals("تطبيق سطح المكتب", ar.installKind("appbundle"))
        assertEquals("desktop app", en.installKind("appbundle"))
        assertEquals("تحديث العنقود", ar.updateSource("run"))
        assertEquals("the cluster's update", en.updateSource("run"))
        assertEquals("مُطفأ", ar.disabledReason("operator"))
        assertEquals("غير مُعدّ", ar.disabledReason("not_configured"))
        assertNull(ar.disabledReason("component_off"))
    }

    @Test
    fun anUpdateStepNamesItsMachineNeverTheHomesHostId() {
        val names = mapOf("n1" to "Upstairs computer")
        assertNull(stepHost("home", names))
        assertNull(stepHost("", names))
        assertEquals("Upstairs computer", stepHost("n1", names))
        assertEquals("0b3c4d5e", stepHost("0b3c4d5e-aaaa", names))
    }

    // ---------- the layout ----------

    @Test
    fun aSevenInchTabletShowsOnePaneATime() {
        // a 7" tablet upright (600 dp) and a foldable open flat (673 dp), less the 80 dp rail
        assertFalse(twoPanesFit(520.dp))
        assertFalse(twoPanesFit(593.dp))
        // a 10" tablet in landscape, a phone on its side
        assertTrue(twoPanesFit(1200.dp))
        assertTrue(twoPanesFit(RemoteListMinWidth + 320.dp))
    }

    @Test
    fun aFactsValueGoesUnderItsLabelRatherThanLetterByLetter() {
        // "Automatic updates" leaves 40 dp of a 223 dp pane: under it
        assertFalse(factBeside(223.dp, 40.dp, 95.dp))
        // on a phone: beside
        assertTrue(factBeside(379.dp, 213.dp, 95.dp))
        // a word longer than what is left: under
        assertFalse(factBeside(379.dp, 213.dp, 230.dp))
    }
}
