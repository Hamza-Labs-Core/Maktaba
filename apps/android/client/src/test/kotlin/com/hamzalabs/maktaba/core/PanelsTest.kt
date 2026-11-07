package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.PanelText
import com.hamzalabs.maktaba.core.api.PanelView
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

// Story 54.20 T04: a panel's hostile values are plain text, cut, never a URL the app uses.
class PanelsTest {
    private val panel = ApiClient.json.decodeFromString<PanelView>(
        """{"plugin_id":"dev.example.loudness","plugin":"Loudness","title":"Report","blocks":[
          {"type":"kv","value":[{"label":"Link","value":"https://evil.example/x.png"},{"label":"Peak","value":-1.5}]},
          {"type":"badges","value":[{"text":"‮gnol","tone":"warning"}]},
          {"type":"chips","value":["[click](https://evil.example)"]},
          {"type":"markers","value":[{"t_ms":61500,"label":"pause"},{"t_ms":"x","label":"bad"}]},
          {"type":"image","image":"https://evil.example/x.png"},
          {"type":"text","value":"**bold**"}]}""",
    )

    @Test fun valuesArePlainText() {
        val kv = PanelText.list(panel.blocks[0].value)
        assertEquals("https://evil.example/x.png", PanelText.plain(PanelText.field(kv[0], "value")))
        assertEquals("-1.5", PanelText.plain(PanelText.field(kv[1], "value")))
        assertEquals("gnol", PanelText.plain(PanelText.field(PanelText.list(panel.blocks[1].value)[0], "text")))
        assertEquals("**bold**", PanelText.plain(panel.blocks[5].value))
        assertEquals("", PanelText.plain(buildJsonObject { put("a", "b") }))
        assertEquals(PanelText.MAX_SHOWN, PanelText.plain("ع".repeat(500)).length)
        assertTrue(PanelText.plain("ع".repeat(500)).endsWith("…"))
    }

    @Test fun onlyTheHomesPanelImage() {
        assertNull(PanelText.safeImage(panel.blocks[4].image))
        assertNull(PanelText.safeImage("//evil.example/x"))
        assertEquals("/api/panels/image?subject=video:1&plugin=dev.example.x", PanelText.safeImage("/api/panels/image?subject=video:1&plugin=dev.example.x"))
    }

    @Test fun markersNeedATime() {
        val m = PanelText.list(panel.blocks[3].value)
        assertEquals(61500.0, PanelText.number(PanelText.field(m[0], "t_ms"))!!, 0.0)
        assertNull(PanelText.number(PanelText.field(m[1], "t_ms")))
        assertNull(PanelText.number(JsonPrimitive("12")))
        assertEquals("1:01", PanelText.clock(61500.0))
        assertEquals("1:02:03", PanelText.clock(3_723_000.0))
    }

    @Test fun theTvShowsKvBadgesAndMarkers() {
        assertEquals(listOf("kv", "badges", "markers"), PanelText.shown(panel.blocks, tv = true).map { it.type })
        assertEquals(6, PanelText.shown(panel.blocks, tv = false).size)
    }

    @Test fun panelsAreOffUnlessTheHomeSaysOn() {
        assertFalse(Capabilities.parse("""{"features":{"live_tv":"on"}}""")!!.panels)
        assertFalse(Capabilities.parse("""{"features":{"plugin_panels":"off"}}""")!!.panels)
        assertTrue(Capabilities.parse("""{"features":{"plugin_panels":"on"}}""")!!.panels)
        assertFalse(Capabilities.AllOn.panels)
    }
}
