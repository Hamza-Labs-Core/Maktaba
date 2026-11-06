package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.diagnostics.LogRing
import com.hamzalabs.maktaba.core.diagnostics.Redact
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File

/**
 * Epic 53 story 53.01: the Android apps' redaction passes the shared cases (T01), and the
 * on-device ring keeps the newest lines in under 1 MB (T06), in the shared schema's shape (T02).
 */
class RedactTest {
    @get:Rule val tmp = TemporaryFolder()

    private val repo: File = generateSequence(File("").absoluteFile) { it.parentFile }
        .first { File(it, "shared/redact/cases.json").exists() }
    private val cases = Json.parseToJsonElement(File(repo, "shared/redact/cases.json").readText()).jsonObject

    @Test
    fun `every shared scrub case passes`() {
        for (c in cases["scrub"]!!.jsonArray) {
            val o = c.jsonObject
            val input = o["in"]!!.jsonPrimitive.content
            assertEquals(input, o["out"]!!.jsonPrimitive.content, Redact.scrub(input))
        }
    }

    @Test
    fun `every shared key case passes`() {
        for (c in cases["keys"]!!.jsonArray) {
            val o = c.jsonObject
            val key = o["key"]!!.jsonPrimitive.content
            assertEquals(key, o["masked"]!!.jsonPrimitive.boolean, Redact.keySensitive(key, o["string"]!!.jsonPrimitive.boolean))
        }
    }

    @Test
    fun `the ring keeps the newest lines under one megabyte`() {
        val ring = LogRing(tmp.newFolder("logs"), mapOf("service" to "android.player", "app_version" to "1.0", "platform" to "android"))
        repeat(3000) { ring.write("info", "tick", mapOf("i" to it, "pad" to "x".repeat(300))) }
        ring.write("error", "screen_failed", mapOf("url" to "/a?token=abc", "who" to "a@b.co"))
        assertTrue(ring.current.length() + ring.previous.length() <= 1024 * 1024)
        val lines = ring.lines()
        assertTrue(lines.size in 1001..3000)
        val last = Json.parseToJsonElement(lines.last()).jsonObject
        assertEquals("screen_failed", last["msg"]!!.jsonPrimitive.content)
        assertEquals("error", last["level"]!!.jsonPrimitive.content)
        assertEquals("android.player", last["service"]!!.jsonPrimitive.content)
        assertTrue(Regex("""\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z""").matches(last["ts"]!!.jsonPrimitive.content))
        val attrs = last["attrs"]!!.jsonObject
        assertEquals("/a?token=[REDACTED]", attrs["url"]!!.jsonPrimitive.content)
        assertEquals("[EMAIL]", attrs["who"]!!.jsonPrimitive.content)
        val first = Json.parseToJsonElement(lines.first()).jsonObject
        assertTrue(first["attrs"]!!.jsonObject["i"]!!.jsonPrimitive.int > 0)
    }

    @Test
    fun `an app's service name`() {
        assertEquals("android.remote", LogRing.service("com.hamzalabs.maktaba.remote", tv = false))
        assertEquals("android.player", LogRing.service("com.hamzalabs.maktaba.player", tv = false))
        assertEquals("android.tv", LogRing.service("com.hamzalabs.maktaba.tv", tv = true))
    }
}
