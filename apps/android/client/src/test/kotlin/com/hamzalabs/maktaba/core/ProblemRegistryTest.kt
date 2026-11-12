package com.hamzalabs.maktaba.core

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/**
 * Epic 60 story 60.06 (T07): every problem type and code the Android apps compare against is in
 * the registry (shared/api/problems.json), so each has its page on maktaba.tv and the API sends it.
 *
 * Types: every `maktaba.tv/problems/<slug>` in the apps' sources, every literal compared with a
 * `slug`, and every member of a `…Slugs` set. Codes: every literal compared with `code` or
 * `reason` in the problem classifier ([CLASSIFIERS]) and every `problem.code == "…"` anywhere.
 */
class ProblemRegistryTest {
    private val repo: File = generateSequence(File("").absoluteFile) { it.parentFile }
        .first { File(it, "shared/api/problems.json").exists() }

    private val entries = Json.parseToJsonElement(File(repo, "shared/api/problems.json").readText())
        .jsonObject.getValue("problems").jsonArray.map { it.jsonObject }
    private val slugs = entries.map { it.getValue("slug").jsonPrimitive.content }.toSet()
    private val codes = entries.flatMap { e -> e.getValue("codes").jsonArray.map { it.jsonPrimitive.content } }.toSet()

    /** The files that sort the home's problems into what a person reads. */
    private val CLASSIFIERS = listOf("apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/api/HomeError.kt")

    /** Codes compared there that are not the API's problem codes, each with why. */
    private val notProblemCodes = mapOf(
        "relay_needs_plan" to "Maktaba Cloud's relay names it in its own `error` field (story 50.19), not an API problem",
        "stream_limit" to "Maktaba Cloud's relay names it in its own `error` field (story 50.19), not an API problem",
    )

    private val uri = Regex("""maktaba\.tv/problems/([a-z0-9_-]+)""")
    private val slugCompare = Regex("""\b[sS]lug\s*==\s*"([a-z0-9_-]+)"""")
    private val slugSet = Regex("""[A-Za-z]*Slugs\s*=\s*setOf\(([^)]*)\)""")
    private val codeCompare = Regex("""\b(?:code|reason)\s*==\s*"([a-z0-9_]+)"""")
    private val problemCode = Regex("""problem\??\.code\s*==\s*"([a-z0-9_]+)"""")
    private val quoted = Regex(""""([a-z0-9_-]+)"""")

    private fun sources(): List<File> = File(repo, "apps/android").walkTopDown()
        .onEnter { it.name != "build" && it.name != "test" && it.name != "androidTest" && it.name != "testFixtures" }
        .filter { it.isFile && it.extension == "kt" && "/src/main/" in it.invariantSeparatorsPath }
        .toList()

    @Test
    fun `every problem type and code the apps compare is registered`() {
        val files = sources()
        assertTrue("found no sources under apps/android: the scan is broken", files.size > 100)
        val types = mutableMapOf<String, String>()
        val found = mutableMapOf<String, String>()
        for (f in files) {
            val text = f.readText()
            val at = f.relativeTo(repo).invariantSeparatorsPath
            (uri.findAll(text) + slugCompare.findAll(text)).forEach { types.putIfAbsent(it.groupValues[1], at) }
            slugSet.findAll(text).forEach { m -> quoted.findAll(m.groupValues[1]).forEach { types.putIfAbsent(it.groupValues[1], at) } }
            problemCode.findAll(text).forEach { found.putIfAbsent(it.groupValues[1], at) }
            if (at in CLASSIFIERS) codeCompare.findAll(text).forEach { found.putIfAbsent(it.groupValues[1], at) }
        }
        assertTrue("found no problem type in the apps: the scan is broken", types.isNotEmpty())
        assertTrue("found no problem code in the classifier: the scan is broken", found.isNotEmpty())

        val badTypes = types.filterKeys { it !in slugs }.map { (s, at) -> "type $s ($at)" }
        val badCodes = found.filterKeys { it !in codes && it !in notProblemCodes }.map { (c, at) -> "code $c ($at)" }
        assertEquals(
            "not in shared/api/problems.json (add the entry there, or, for a code that is no API problem's, " +
                "to notProblemCodes with why)",
            emptyList<String>(), badTypes + badCodes,
        )
        val stale = notProblemCodes.keys.filter { it !in found } + notProblemCodes.keys.filter { it in codes }
        assertEquals("notProblemCodes lists codes the apps no longer compare, or that are registered", emptyList<String>(), stale)
    }
}
