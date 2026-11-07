package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.CloudStatusRules
import com.hamzalabs.maktaba.core.api.CloudSummary
import com.hamzalabs.maktaba.core.api.HomeStatusDoc
import com.hamzalabs.maktaba.core.api.StatusStripRules
import com.hamzalabs.maktaba.core.api.StripKey
import kotlinx.coroutines.test.runTest
import okhttp3.HttpUrl.Companion.toHttpUrl
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File
import java.time.Instant

/** Epic 50 story 50.11 (T07, T08): Maktaba Cloud's line in the status strip, from summary.json. */
class CloudStatusTest {
    private val ok = CloudSummary(components = mapOf("relay" to "operational", "api" to "operational", "push" to "operational"))
    private val now = Instant.parse("2026-11-03T12:00:00Z").toEpochMilli()
    private val fmt: (Long) -> Pair<String, String> = { ms ->
        val s = Instant.ofEpochMilli(ms).toString()
        s.take(10) to s.substring(11, 16)
    }
    private fun at(iso: String) = Instant.parse(iso).toEpochMilli()

    @Test
    fun `nothing when all works or before the feed answered`() {
        assertNull(CloudStatusRules.line(ok, now, fmt))
        assertNull(CloudStatusRules.line(null, now, fmt))
    }

    @Test
    fun `remote access down links to the incident`() {
        val s = ok.copy(
            components = ok.components + ("relay" to "outage"),
            incident = CloudSummary.Incident("2026-11-03-relay-down", "investigating", listOf("relay"), url = "https://status.maktaba.tv/i/2026-11-03-relay-down"),
        )
        val l = CloudStatusRules.line(s, now, fmt)!!
        assertEquals(StripKey.RELAY_OUTAGE, l.key)
        assertEquals("cloud:2026-11-03-relay-down", l.id)
        assertEquals("https://status.maktaba.tv/i/2026-11-03-relay-down", l.link)
    }

    @Test
    fun `down before slow, never for notifications alone`() {
        assertEquals(StripKey.API_OUTAGE, CloudStatusRules.line(CloudSummary(mapOf("relay" to "degraded", "api" to "outage")), now, fmt)?.key)
        assertEquals(CloudStatusRules.SITE, CloudStatusRules.line(CloudSummary(mapOf("api" to "outage")), now, fmt)?.link)
        assertEquals(StripKey.RELAY_DEGRADED, CloudStatusRules.line(CloudSummary(mapOf("relay" to "degraded", "api" to "degraded")), now, fmt)?.key)
        assertNull(CloudStatusRules.line(CloudSummary(mapOf("push" to "outage")), now, fmt))
    }

    @Test
    fun `maintenance from 24 hours ahead until it ends`() {
        val m = CloudSummary.Maintenance("db", "2026-11-04T02:00:00Z", "2026-11-04T02:30:00Z", listOf("relay", "api"))
        val s = ok.copy(maintenance = listOf(m))
        assertNull(CloudStatusRules.line(s, at("2026-11-03T01:59:00Z"), fmt))
        val l = CloudStatusRules.line(s, at("2026-11-03T02:00:00Z"), fmt)!!
        assertEquals(StripKey.MAINTENANCE, l.key)
        assertEquals(listOf("2026-11-04", "02:00", "02:30"), l.args)
        assertEquals("cloud:db", l.id)
        assertNull(CloudStatusRules.line(s, at("2026-11-04T02:30:00Z"), fmt))
        assertNull(CloudStatusRules.line(ok.copy(maintenance = listOf(m.copy(components = listOf("push")))), at("2026-11-04T02:10:00Z"), fmt))
    }

    @Test
    fun `comes after the home's own lines, before admin-only ones`() {
        val cloud = CloudStatusRules.line(CloudSummary(mapOf("relay" to "outage")), now, fmt)
        val home = HomeStatusDoc(v = 1, overall = "degraded", components = mapOf("streaming" to "down", "processing" to "down"))
        assertEquals(
            listOf(StripKey.STREAMING, StripKey.RELAY_OUTAGE, StripKey.PROCESSING),
            StatusStripRules.lines(home, online = true, admin = true, cloud = cloud).map { it.key },
        )
        assertEquals(listOf(StripKey.RELAY_OUTAGE), StatusStripRules.lines(null, online = true, admin = false, cloud = cloud).map { it.key })
    }

    @Test
    fun `only through the cloud`() {
        assertTrue(CloudStatusRules.throughCloud("https://skylark-12.relay.maktaba.tv/".toHttpUrl()))
        assertFalse(CloudStatusRules.throughCloud("http://192.168.1.20:8080/".toHttpUrl()))
        assertFalse(CloudStatusRules.throughCloud("https://evilrelay.maktaba.tv/".toHttpUrl()))
        assertEquals("https://status.maktaba.tv/summary.json?t=${now / 60_000}", CloudStatusRules.summaryUrl(now))
    }

    @Test
    fun `the feed reads, and a 404 or a broken body is no line (T08)`() = runTest {
        val server = MockWebServer()
        server.enqueue(
            MockResponse.Builder().body(
                """{"updated_at":"2026-11-03T10:15:00Z","overall":"degraded","components":{"relay":"degraded"},
                   "incident":null,"maintenance":[{"starts_at":"2026-11-05T02:00:00Z","ends_at":"2026-11-05T02:30:00Z",
                   "components":["relay"],"title":{"en":"Upgrade","ar":"ترقية"},"url":null}]}""",
            ).build(),
        )
        server.enqueue(MockResponse.Builder().code(404).build())
        server.enqueue(MockResponse.Builder().body("<html>").build())
        server.start()
        try {
            val url = server.url("/summary.json").toString()
            val s = CloudStatusRules.fetch(ApiClient.defaultHttp, now, url)!!
            assertEquals("degraded", s.components["relay"])
            assertEquals("2026-11-05T02:00:00Z", s.maintenance.single().startsAt)
            assertNull(CloudStatusRules.fetch(ApiClient.defaultHttp, now, url))
            assertNull(CloudStatusRules.fetch(ApiClient.defaultHttp, now, url))
        } finally {
            server.close()
        }
        assertNull(CloudStatusRules.fetch(ApiClient.defaultHttp, now, "http://127.0.0.1:1/summary.json"))
    }

    @Test
    fun `every cloud line has its arabic, with the window's three values`() {
        val root = generateSequence(File("").absoluteFile) { it.parentFile }.first { File(it, "core/src/main/res/values").exists() }
        val ar = File(root, "core/src/main/res/values-ar/strings.xml").readText()
        for (name in listOf("banner_relay_degraded", "banner_relay_outage", "banner_api_degraded", "banner_api_outage", "banner_maintenance")) {
            assertTrue(name, ar.contains("name=\"$name\""))
        }
        val m = Regex("""name="banner_maintenance">([^<]*)<""").find(ar)!!.groupValues[1]
        assertTrue(m, listOf("%1\$s", "%2\$s", "%3\$s").all { it in m })
    }
}
