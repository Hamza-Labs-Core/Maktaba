package com.hamzalabs.maktaba.tv

import android.content.Context
import android.media.MediaCodecInfo.CodecProfileLevel
import android.media.MediaFormat
import android.os.Looper
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.session.TokenStore
import com.hamzalabs.maktaba.tv.playback.DeviceCapsReader
import com.hamzalabs.maktaba.tv.playback.TvPlayback
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows.shadowOf
import org.robolectric.shadows.MediaCodecInfoBuilder
import org.robolectric.shadows.ShadowMediaCodecList
import java.util.Base64
import java.util.Collections

/**
 * Story 58.05 on the TV: the playback capabilities are read from the platform's decoders
 * (hardware video, every audio), and each stream open names this build's profile (`androidtv`
 * on Google Play, `firetv` on the Amazon Appstore) with the report beside it.
 */
@RunWith(AndroidJUnit4::class)
class TvDeviceCapsTest {
    private val context: Context = ApplicationProvider.getApplicationContext()
    private lateinit var server: MockWebServer
    private val bodies: MutableList<String> = Collections.synchronizedList(mutableListOf())
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val background = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private var playback: TvPlayback? = null

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                if (request.url.encodedPath == "/api/stream/sessions" && request.method == "POST") {
                    bodies += request.body!!.utf8()
                    return MockResponse.Builder().code(201).setHeader("Content-Type", "application/json")
                        .body("""{"session_id":"s1","mode":"direct","direct_url":"/stream/s1/v1.mp4?sig=x","expires_at":"2099-01-01T00:00:00Z"}""")
                        .build()
                }
                return MockResponse.Builder().code(204).build()
            }
        }
        server.start()
    }

    @After
    fun stop() {
        playback?.stop()
        scope.cancel()
        background.cancel()
        server.close()
        ShadowMediaCodecList.reset()
    }

    private fun decoder(name: String, mime: String, video: Boolean, vararg profiles: Int, encoder: Boolean = false) =
        MediaCodecInfoBuilder.newBuilder()
            .setName(name)
            .setIsEncoder(encoder)
            .setIsSoftwareOnly(name.startsWith("c2.android."))
            .setCapabilities(
                MediaCodecInfoBuilder.CodecCapabilitiesBuilder.newBuilder()
                    .setMediaFormat(if (video) MediaFormat.createVideoFormat(mime, 3840, 2160) else MediaFormat.createAudioFormat(mime, 48_000, 6))
                    .setIsEncoder(encoder)
                    .apply {
                        if (video) setColorFormats(intArrayOf(android.media.MediaCodecInfo.CodecCapabilities.COLOR_FormatYUV420Flexible))
                        if (profiles.isNotEmpty()) {
                            setProfileLevels(profiles.map { p -> CodecProfileLevel().apply { profile = p; level = 0 } }.toTypedArray())
                        }
                    }
                    .build(),
            )
            .build()

    @Test
    fun theReaderTakesHardwareVideoAndEveryAudioDecoder() {
        ShadowMediaCodecList.addCodec(
            decoder(
                "OMX.amlogic.hevc.decoder.awesome", MediaFormat.MIMETYPE_VIDEO_HEVC, true,
                CodecProfileLevel.HEVCProfileMain, CodecProfileLevel.HEVCProfileMain10, CodecProfileLevel.HEVCProfileMain10HDR10,
            ),
        )
        // Android's own software AV1 decoder: a TV cannot count on it for 4K
        ShadowMediaCodecList.addCodec(decoder("c2.android.av1.decoder", MediaFormat.MIMETYPE_VIDEO_AV1, true, CodecProfileLevel.AV1ProfileMain8))
        // an encoder is never a decoder
        ShadowMediaCodecList.addCodec(decoder("OMX.amlogic.avc.encoder", MediaFormat.MIMETYPE_VIDEO_AVC, true, CodecProfileLevel.AVCProfileHigh, encoder = true))
        // audio decoders are software, and count
        ShadowMediaCodecList.addCodec(decoder("c2.android.aac.decoder", MediaFormat.MIMETYPE_AUDIO_AAC, false))
        ShadowMediaCodecList.addCodec(decoder("OMX.dolby.eac3.decoder", MediaFormat.MIMETYPE_AUDIO_EAC3, false))

        val answers = DeviceCapsReader.answers(context)
        assertEquals(listOf("video/hevc", "audio/mp4a-latm", "audio/eac3"), answers.codecs.map { it.mime })
        assertEquals(listOf("Main", "Main10", "Main10HDR10"), answers.codecs.first().profiles)
        assertTrue("PCM always plays", "PCM_16BIT" in answers.audio.encodings)

        val caps = DeviceCapsReader.read(context)
        assertNotNull(caps)
        val video = caps!!.getValue("video") as JsonArray
        assertEquals(listOf("hevc"), video.map { it.jsonObject.getValue("codec").jsonPrimitive.content })
        assertEquals("10", video.single().jsonObject.getValue("bit_depth").jsonPrimitive.content)
        assertEquals("""["aac","eac3"]""", caps.getValue("audio").toString())
        // Robolectric's display shows no HDR, so none is claimed
        assertEquals("[]", caps.getValue("hdr").toString())
    }

    @Test
    fun profileNamesAreTheCodecProfileLevelOnes() {
        assertEquals("DvheSt", DeviceCapsReader.profileName("video/dolby-vision", CodecProfileLevel.DolbyVisionProfileDvheSt))
        assertEquals("DvheStn", DeviceCapsReader.profileName("video/dolby-vision", CodecProfileLevel.DolbyVisionProfileDvheStn))
        assertEquals("Main10HDR10Plus", DeviceCapsReader.profileName("video/hevc", CodecProfileLevel.HEVCProfileMain10HDR10Plus))
        assertEquals("Main10", DeviceCapsReader.profileName("video/av01", CodecProfileLevel.AV1ProfileMain10))
        assertEquals("Profile2HDR", DeviceCapsReader.profileName("video/x-vnd.on2.vp9", CodecProfileLevel.VP9Profile2HDR))
        assertEquals("High", DeviceCapsReader.profileName("video/avc", CodecProfileLevel.AVCProfileHigh))
        assertNull(DeviceCapsReader.profileName("video/mpeg2", 1))
    }

    @Test
    fun eachOpenNamesThisBuildsProfileAndItsReport() {
        val token = Base64.getUrlEncoder().withoutPadding().let { enc ->
            enc.encodeToString("""{"alg":"RS256"}""".toByteArray()) + "." +
                enc.encodeToString("""{"exp":${System.currentTimeMillis() / 1000 + 86_400}}""".toByteArray()) + ".sig"
        }
        val tokens = object : TokenStore {
            override val access get() = token
            override val refresh get() = "r1"
            override fun save(tokens: AuthTokens) {}
            override fun clear() {}
        }
        val report = buildJsonObject { put("hdr", JsonArray(listOf(JsonPrimitive("hdr10")))) }
        val pb = TvPlayback(
            context, ApiClient(server.url("/"), tokens), "v1", 0, scope, background, signedOut = {},
            deviceCaps = { report },
        ).also { playback = it }
        pb.start()
        val until = System.currentTimeMillis() + 10_000
        while (bodies.isEmpty() && System.currentTimeMillis() < until) {
            shadowOf(Looper.getMainLooper()).idle()
            Thread.sleep(10)
        }
        val body = Json.parseToJsonElement(bodies.first()).jsonObject
        assertEquals(BuildConfig.TV_PLATFORM, body.getValue("client_profile").jsonPrimitive.content)
        assertEquals(report, body.getValue("device_caps"))
    }
}
