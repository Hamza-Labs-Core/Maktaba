package com.hamzalabs.maktaba.core

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.io.File
import java.time.Instant

/**
 * The demo library (story 60.09, `tests/fixtures/demo-library`) as a home answers it in one
 * language: the route table `routes.mjs` writes to `routes/<lang>/routes.json`, read from the
 * repository at test time (never copied into Kotlin). Each answer's `{{now±N}}` times are filled
 * from the library's pinned [clock]; an image route answers its JPEG from `art/`.
 *
 * ```
 * val demo = DemoLibrary.load("ar")
 * demo.answer("GET", "/api/videos/v-sintel")   // the Arabic film page's JSON
 * ```
 */
class DemoLibrary private constructor(val dir: File, val lang: String) {
    /** What the home answers: its status, content type and body. */
    class Answer(val status: Int, val contentType: String, val body: ByteArray)

    private class Route(val method: String, val segments: List<String>, val status: Int, val file: String?, val art: String?)

    private val json = Json { ignoreUnknownKeys = true }

    /** The library's pinned clock (`library.json`'s `clock`): every time in an answer is relative to it. */
    val clock: Instant = Instant.parse(
        json.parseToJsonElement(File(dir, "library.json").readText()).jsonObject["clock"]!!.jsonPrimitive.content,
    )

    private val routes: List<Route> = json.parseToJsonElement(File(dir, "routes/$lang/routes.json").readText()).jsonArray.map {
        val o = it.jsonObject
        Route(
            method = o["method"]!!.jsonPrimitive.content,
            segments = o["path"]!!.jsonPrimitive.content.trim('/').split('/'),
            status = o["status"]!!.jsonPrimitive.int,
            file = o["file"]?.jsonPrimitive?.content,
            art = o["art"]?.jsonPrimitive?.content,
        )
    }

    /** `GET /api/me`: who is signed in. */
    val me: JsonObject get() = json.parseToJsonElement(text("get-me.json")).jsonObject

    /** The signed-in account's name (`username`). */
    val username: String get() = me["username"]!!.jsonPrimitive.content

    /** Whether the signed-in account is the home's admin. */
    val isAdmin: Boolean get() = me["is_admin"]?.jsonPrimitive?.boolean == true

    /** The signed-in account's id. */
    val userId: String get() = me["id"]!!.jsonPrimitive.content

    /** The home's name (`GET /api/auth/me`'s `home_name`). */
    val homeName: String get() = json.parseToJsonElement(text("get-auth-me.json")).jsonObject["home_name"]!!.jsonPrimitive.content

    /** An answer's file in this language, its times filled. */
    fun text(file: String): String = fillTimes(File(dir, "routes/$lang/$file").readText(), clock)

    /**
     * The answer to [method] [path] (no query): an exact route first, then one whose `{name}`
     * segments match any one segment; null when the table has none.
     */
    fun answer(method: String, path: String): Answer? {
        val asked = path.trim('/').split('/')
        val candidates = routes.filter { it.method.equals(method, ignoreCase = true) && it.segments.size == asked.size }
        val route = candidates.firstOrNull { it.segments == asked }
            ?: candidates.firstOrNull { r -> r.segments.indices.all { i -> r.segments[i] == asked[i] || r.segments[i].startsWith("{") } }
            ?: return null
        return when {
            route.art != null -> Answer(route.status, "image/jpeg", File(dir, "art/${route.art}").readBytes())
            route.file != null -> Answer(route.status, "application/json", text(route.file).toByteArray())
            else -> Answer(route.status, "application/json", ByteArray(0))
        }
    }

    companion object {
        private val TIME = Regex("""\{\{now([+-]\d+)?\}\}""")

        /** `{{now…}}` in [text] as RFC 3339 times from [clock], as `routes.mjs`' `fillTimes` writes them. */
        fun fillTimes(text: String, clock: Instant): String =
            TIME.replace(text) { m -> clock.plusSeconds(m.groupValues[1].ifEmpty { "0" }.toLong()).toString() }

        /**
         * The fixture's folder: `tests/fixtures/demo-library` in the first folder up from the
         * test's working directory (the Gradle module's) that has one.
         */
        fun folder(): File {
            var at: File? = File(System.getProperty("user.dir")).absoluteFile
            while (at != null) {
                val dir = File(at, "tests/fixtures/demo-library")
                if (File(dir, "library.json").isFile) return dir
                at = at.parentFile
            }
            error("tests/fixtures/demo-library not found above ${System.getProperty("user.dir")}")
        }

        /** The demo library in [lang] (`en` or `ar`). */
        fun load(lang: String): DemoLibrary = DemoLibrary(folder(), lang)
    }
}
