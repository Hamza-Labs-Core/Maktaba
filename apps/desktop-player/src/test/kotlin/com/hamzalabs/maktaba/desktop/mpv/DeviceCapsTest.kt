package com.hamzalabs.maktaba.desktop.mpv

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeFalse
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File

/**
 * Story 13.19 with recorded probe answers: the report built from them (T01), the height from
 * software's speed (T02), the probe's cache (T03), and the problem report's line (T11).
 */
class DeviceCapsTest {
    @get:Rule val tmp = TemporaryFolder()

    /** The Linux key's readers: POSIX paths and symbolic links, which Windows' runner does not make. */
    private fun unixOnly() = assumeFalse("Linux only", System.getProperty("os.name").startsWith("Windows"))

    /** A machine with hardware HEVC and VP9, AV1 in software at 20 fps at 2160p, H.264 and MPEG-2 in software. */
    private val recorded = listOf(
        ClipResult("h264", 8, decoded = true, hwdec = "no", softwareFps = 90.0),
        ClipResult("hevc", 8, decoded = true, hwdec = "d3d11va-copy"),
        ClipResult("hevc", 10, decoded = true, hwdec = "d3d11va-copy"),
        ClipResult("vp9", 8, decoded = true, hwdec = "vaapi-copy"),
        ClipResult("vp9", 10, decoded = true, hwdec = "vaapi-copy"),
        ClipResult("av1", 8, decoded = true, hwdec = "no", softwareFps = 26.0),
        ClipResult("av1", 10, decoded = true, hwdec = "no", softwareFps = 20.0),
        ClipResult("mpeg2video", 8, decoded = true, hwdec = "no", softwareFps = 200.0),
    )

    private fun video(j: JsonObject, codec: String) =
        j.getValue("video").jsonArray.map { it.jsonObject }.firstOrNull { it.getValue("codec").jsonPrimitive.content == codec }

    @Test
    fun t01_theReportFromRecordedAnswers() {
        val j = DesktopCaps.build(recorded, 6).toJson()
        assertEquals(
            Json.parseToJsonElement("""{"codec":"hevc","max_height":2160,"bit_depth":10,"hardware":true}"""),
            video(j, "hevc"),
        )
        assertEquals(
            Json.parseToJsonElement("""{"codec":"vp9","max_height":2160,"bit_depth":10,"hardware":true}"""),
            video(j, "vp9"),
        )
        // AV1 in software: the slower (10-bit) clip's height, 20 fps at 2160p being 1080
        assertEquals(
            Json.parseToJsonElement("""{"codec":"av1","max_height":1080,"bit_depth":10,"hardware":false}"""),
            video(j, "av1"),
        )
        // H.264 and MPEG-2 have no 10-bit clip: no bit depth said
        assertEquals(Json.parseToJsonElement("""{"codec":"h264","max_height":2160,"hardware":false}"""), video(j, "h264"))
        assertEquals("6", j.getValue("max_audio_channels").jsonPrimitive.content)
        // the profile's containers, audio and HDR stand: their keys are left out
        for (k in listOf("containers", "audio", "hdr", "passthrough", "hls_segments", "dash")) assertFalse(k, k in j)
        assertEquals(listOf("h264", "hevc", "vp9", "av1", "mpeg2video"), j.getValue("video").jsonArray.map { it.jsonObject.getValue("codec").jsonPrimitive.content })
    }

    @Test
    fun aCodecThatDidNotDecodeIsLeftOutAndChannelsAreBounded() {
        val clips = listOf(
            ClipResult("hevc", 8, decoded = true, hwdec = "nvdec-copy"),
            ClipResult("hevc", 10, decoded = false),
            ClipResult("av1", 8, decoded = false),
            ClipResult("av1", 10, decoded = false),
        )
        val caps = DesktopCaps.build(clips, 12)
        // AV1 did not decode: left out; H.264, VP9 and MPEG-2 were not probed: listed without limits
        assertEquals(listOf("h264", "hevc", "vp9", "mpeg2video"), caps.video.map { it.codec })
        // the 10-bit clip did not decode: 8-bit
        assertEquals(8, caps.video.single { it.codec == "hevc" }.bitDepth)
        assertEquals(
            Json.parseToJsonElement("""{"codec":"vp9"}"""),
            video(caps.toJson(), "vp9"),
        )
        // more channels than the profile's 8 are 8
        assertEquals(8, caps.maxAudioChannels)
        // a device mpv could not open leaves the channels out
        assertFalse("max_audio_channels" in DesktopCaps.build(clips, null).toJson())
        // a machine with no GPU driver: every codec in software, still narrowed by the CPU
        val vm = DesktopCaps.build(listOf(ClipResult("hevc", 8, decoded = true, hwdec = "no", softwareFps = 14.0)), 2)
        assertEquals(1080, vm.video.single { it.codec == "hevc" }.maxHeight)
        assertEquals(false, vm.video.single { it.codec == "hevc" }.hardware)
        // the 10-bit clip not reached: no bit depth said
        assertNull(vm.video.single { it.codec == "hevc" }.bitDepth)
        // a clip that ran out of time, or that the last probe stopped in: software at the safe height
        val fallback = DesktopCaps.build(
            listOf(
                ClipResult("av1", 8, decoded = true, hwdec = "no", softwareFps = 60.0),
                ClipResult("av1", 10, decoded = true, hwdec = "no", fallback = ClipResult.TIMED_OUT),
                ClipResult("mpeg2video", 8, decoded = true, hwdec = "no", fallback = ClipResult.STOPPED_HERE),
            ),
            null,
        )
        assertEquals(720, fallback.video.single { it.codec == "av1" }.maxHeight)
        assertEquals(720, fallback.video.single { it.codec == "mpeg2video" }.maxHeight)
    }

    @Test
    fun t02_theHeightFromSoftwaresSpeed() {
        assertEquals(listOf(2160, 1440, 1080, 720), listOf(50.0, 30.0, 12.0, 5.0).map(DesktopCaps::heightFor))
        // the edges: 45 fps, 45 × 9/16, 45 / 4
        assertEquals(2160, DesktopCaps.heightFor(45.0))
        assertEquals(1440, DesktopCaps.heightFor(25.3125))
        assertEquals(1080, DesktopCaps.heightFor(11.25))
        assertEquals(720, DesktopCaps.heightFor(11.2))
        // a clip's speed is scaled to 2160p by pixel count: MPEG-2's 1080p clip at 100 fps is 25 at 2160p
        val mpeg2 = Probe.CLIPS.single { it.codec == "mpeg2video" }
        assertEquals(25.0, Probe.fpsAt2160(Played("no", 24, 240_000_000), mpeg2), 0.001)
        assertEquals(100.0, Probe.fpsAt2160(Played("no", 24, 240_000_000), Probe.CLIPS.first()), 0.001)
    }

    /** A decoder answering from [recorded], counting its plays. */
    private class FakeDecoder(
        override val version: String,
        private val answers: List<ClipResult>,
        private val during: (String) -> Unit = {},
    ) : ProbeDecoder {
        val plays = mutableListOf<String>()
        var warmUps = 0
        var closed = false
        override fun warmUp(clip: File, timeoutMs: Long) {
            warmUps++
        }
        override fun play(clip: File, hwdec: String, timeoutMs: Long): Play {
            plays += clip.name
            during(clip.name)
            val c = Probe.CLIPS.single { it.file == clip.name }
            val a = answers.single { it.codec == c.codec && it.bitDepth == c.bitDepth }
            if (!a.decoded) return PlayFailed
            if (a.fallback == ClipResult.TIMED_OUT) return PlayTimedOut
            if (a.hardware) return Played(a.hwdec!!, 23, 100_000_000)
            // frames in a time that gives the recorded speed at 2160p
            val scale = (c.height / 2160.0).let { it * it }
            return Played("no", 23, (23 / (a.softwareFps!! / scale) * 1e9).toLong())
        }
        override fun channels(clip: File, timeoutMs: Long): Int? = 6
        override fun close() {
            closed = true
        }
    }

    @Test
    fun t03_theCacheIsKeyedByGpuDriverCpuAndLibmpv() {
        val cache = ProbeCache(File(tmp.root, "probe.json"))
        val clips = tmp.newFolder("clips")
        fun key(driver: String = "31.0.15.5222", libmpv: String = "mpv v0.40.0 ffmpeg 7.1") =
            ProbeKey(listOf("NVIDIA GeForce RTX 3060 $driver"), "AMD Ryzen 5 5600X x12", libmpv).toString()

        val first = FakeDecoder("mpv v0.40.0", recorded)
        val r = Probe(first, clips, cache, key()).run()
        assertEquals(Probe.CLIPS.size, first.plays.size)
        // libmpv warmed up once, before the first timed clip
        assertEquals(1, first.warmUps)
        assertTrue(r.complete)
        assertEquals(recorded.map { it.codec to it.hardware }, r.clips.map { it.codec to it.hardware })
        assertEquals(1080, DesktopCaps.build(r.clips, 6).video.single { it.codec == "av1" }.maxHeight)
        assertTrue(File(tmp.root, "probe.json").isFile)

        // the same GPU, driver, CPU and libmpv: read back, nothing played
        val again = FakeDecoder("mpv v0.40.0", recorded)
        assertEquals(r, Probe(again, clips, cache, key()).run())
        assertEquals(emptyList<String>(), again.plays)

        // a new driver: probed again
        val newDriver = FakeDecoder("mpv v0.40.0", recorded)
        assertEquals(key(driver = "32.0.15.6094"), Probe(newDriver, clips, cache, key(driver = "32.0.15.6094")).run().key)
        assertEquals(Probe.CLIPS.size, newDriver.plays.size)

        // a new libmpv (an update): probed again
        val newMpv = FakeDecoder("mpv v0.41.0", recorded)
        Probe(newMpv, clips, cache, key(driver = "32.0.15.6094", libmpv = "mpv v0.41.0 ffmpeg 7.1")).run()
        assertEquals(Probe.CLIPS.size, newMpv.plays.size)
    }

    @Test
    fun anIncompleteProbeNarrowsNothingItDidNotReachAndIsRetriedAfterABackOff() {
        val cache = ProbeCache(File(tmp.root, "probe.json"))
        var nanos = 0L
        var clock = 1_000_000L
        var plays = 0
        val slow = object : ProbeDecoder {
            override val version = "mpv"
            override fun play(clip: File, hwdec: String, timeoutMs: Long): Play {
                plays++
                nanos += 7_000_000_000L // seven seconds a clip
                return Played("no", 23, 7_000_000_000L)
            }
            override fun channels(clip: File, timeoutMs: Long): Int? = null
        }
        val r = Probe(slow, tmp.root, cache, "k", nanoTime = { nanos }, now = { clock }).run()
        // 20 s: three clips, the rest not reached
        assertEquals(3, r.clips.size)
        assertFalse(r.complete)
        assertEquals(clock, r.at)
        // the codecs not reached are listed without limits: an MPEG-2 file still plays as it is
        val caps = DesktopCaps.build(r.clips, null)
        assertEquals(listOf("h264", "hevc", "vp9", "av1", "mpeg2video"), caps.video.map { it.codec })
        assertNull(caps.video.single { it.codec == "mpeg2video" }.maxHeight)
        assertNull(caps.video.single { it.codec == "av1" }.maxHeight)
        // the next start, within a day: the incomplete result stands, nothing is played
        plays = 0
        clock += Probe.backoffMs(1) - 1
        assertEquals(r, Probe(slow, tmp.root, cache, "k", nanoTime = { nanos }, now = { clock }).run())
        assertEquals(0, plays)
        // after it: tried again, and the back-off doubles while it stays incomplete
        clock += 2
        val second = Probe(slow, tmp.root, cache, "k", nanoTime = { nanos }, now = { clock }).run()
        assertTrue(plays > 0)
        assertEquals(2, second.attempts)
        assertEquals(2 * Probe.backoffMs(1), Probe.backoffMs(2))
        assertEquals(30L * 24 * 60 * 60 * 1000, Probe.backoffMs(9))
        // a clip that runs out of its time: software at the safe height, not "does not decode"
        val timedOut = recorded.map { if (it.codec == "av1" && it.bitDepth == 10) it.copy(fallback = ClipResult.TIMED_OUT) else it }
        val t = Probe(FakeDecoder("mpv", timedOut), tmp.root, ProbeCache(File(tmp.root, "t.json")), "k").run()
        val av1 = t.clips.single { it.codec == "av1" && it.bitDepth == 10 }
        assertTrue(av1.decoded && av1.fallback == ClipResult.TIMED_OUT)
        assertEquals(720, DesktopCaps.build(t.clips, null).video.single { it.codec == "av1" }.maxHeight)
        assertFalse("a timed-out clip is tried again later", t.complete)
    }

    @Test
    fun aVideoOpenedDuringAClipHasItMeasuredAgain() {
        var opened = 0L
        var playing = false
        var waits = 0
        // a watch screen opens during the HEVC 10-bit clip, and closes three checks later
        val decoder = FakeDecoder("mpv", recorded, during = { name ->
            if (name == "hevc-10.mkv" && opened == 0L) {
                opened++
                playing = true
            }
        })
        val busy = { if (playing && ++waits > 3) playing = false; playing }
        val r = Probe(decoder, tmp.root, ProbeCache(File(tmp.root, "probe.json")), "k", busy = busy, videosOpened = { opened }).run()
        assertEquals(listOf("hevc-10.mkv", "hevc-10.mkv"), decoder.plays.filter { it == "hevc-10.mkv" })
        assertTrue(r.complete)
        assertEquals(Probe.CLIPS.size, r.clips.size)
    }

    @Test
    fun aClipTheLastProbeStoppedInIsSkipped() {
        val marker = File(tmp.root, "probe.running")
        val cache = ProbeCache(File(tmp.root, "probe.json"))
        // the probe before crashed in the AV1 10-bit clip: its marker is still there
        marker.writeText("av1-10.mkv")
        val d = FakeDecoder("mpv", recorded)
        val r = Probe(d, tmp.root, cache, "k", marker = marker).run()
        assertFalse("av1-10.mkv" in d.plays)
        val av1 = r.clips.single { it.codec == "av1" && it.bitDepth == 10 }
        assertEquals(ClipResult.STOPPED_HERE, av1.fallback)
        assertEquals(720, DesktopCaps.build(r.clips, null).video.single { it.codec == "av1" }.maxHeight)
        assertFalse("the marker is gone once the probe ends", marker.exists())
        assertFalse(r.complete)
        // the problem report says so
        val report = DeviceReport().apply { probe = r }
        assertTrue(report.describe(), report.describe().contains("av1 10-bit: software 720p (skipped: the last probe stopped in this clip)"))
        // while a clip plays, the marker names it
        val seen = mutableListOf<String>()
        Probe(FakeDecoder("mpv", recorded, during = { seen += marker.readText() }), tmp.root, ProbeCache(File(tmp.root, "x.json")), "k", marker = marker).run()
        assertEquals(Probe.CLIPS.map { it.file }, seen)
    }

    @Test
    fun theProbesHandleIsFreedWhenItEnds() {
        val d = FakeDecoder("mpv", recorded)
        val report = DeviceReport()
        val runner = ReportRunner(report, { d }, { tmp.root }, ProbeCache(File(tmp.root, "probe.json")), File(tmp.root, "probe.running"), { false }, { 0L })
        runner.start()
        val end = System.currentTimeMillis() + 10_000
        while ((report.probe == null || !d.closed) && System.currentTimeMillis() < end) Thread.sleep(10)
        assertTrue("probe ${report.probe}, error ${report.probeError}, closed ${d.closed}", report.probe != null && d.closed)
        assertEquals(6, report.channels)
    }

    @Test
    fun vaApiDriversAreInTheLinuxKey() {
        unixOnly()
        val dri = tmp.newFolder("dri")
        File(dri, "radeonsi_drv_video.so").writeBytes(ByteArray(12))
        File(dri, "radeonsi_dri.so").writeBytes(ByteArray(3))
        File(dri, "radeonsi_drv_video.so").setLastModified(1_700_000_000_000)
        assertEquals(listOf("va:radeonsi_drv_video.so 12 1700000000"), ProbeKey.vaDrivers(listOf(dri, File(tmp.root, "none"))))
        assertEquals(listOf(File("/a"), File("/b")), ProbeKey.vaDriverDirs("/a::/b"))
        assertTrue(ProbeKey.vaDriverDirs(null).any { it.path == "/usr/lib/x86_64-linux-gnu/dri" })
    }

    @Test
    fun linuxGpusAreReadFromDrm() {
        unixOnly()
        val sys = tmp.newFolder("sys")
        val drm = File(sys, "class/drm").apply { mkdirs() }
        val dev = File(sys, "devices/pci0000:00/0000:00:02.0").apply { mkdirs() }
        File(dev, "vendor").writeText("0x8086\n")
        File(dev, "device").writeText("0x9a49\n")
        val driver = File(sys, "bus/pci/drivers/i915").apply { mkdirs() }
        java.nio.file.Files.createSymbolicLink(File(dev, "driver").toPath(), driver.toPath())
        val card = File(drm, "card0").apply { mkdirs() }
        java.nio.file.Files.createSymbolicLink(File(card, "device").toPath(), dev.toPath())
        // a connector is not a card
        File(drm, "card0-HDMI-A-1").mkdirs()
        assertEquals(listOf("0x8086:0x9a49 i915 6.8.0-49-generic"), ProbeKey.linuxGpus(drm, kernel = "6.8.0-49-generic"))
        // a driver with its own version (NVIDIA's module) names that, not the kernel's
        File(sys, "module/i915").mkdirs()
        File(sys, "module/i915/version").writeText("550.120\n")
        assertEquals(listOf("0x8086:0x9a49 i915 550.120"), ProbeKey.linuxGpus(drm, kernel = "6.8.0-49-generic"))
    }

    @Test
    fun t11_theProblemReportListsEachCodecTheChannelsAndTheKey() {
        val report = DeviceReport()
        assertNull("no report before the probe has run", report.caps())
        assertTrue(report.describe(), report.describe().contains("probe not run yet"))
        report.probe = ProbeResult("gpu=NVIDIA GeForce RTX 3060 31.0.15.5222; cpu=AMD Ryzen 5 5600X x12; libmpv=mpv v0.40.0", recorded, 1840)
        report.channels = 6
        val line = report.logLine(java.time.Instant.parse("2026-11-24T10:00:00Z"))
        val msg = Json.parseToJsonElement(line).jsonObject.getValue("msg").jsonPrimitive.content
        for (want in listOf(
            "hevc: hardware (d3d11va-copy) 2160p", "hevc 10-bit: hardware (d3d11va-copy) 2160p",
            "av1 10-bit: software 1080p (20.0 fps at 2160p)", "av1: software 1440p (26.0 fps at 2160p)",
            "h264: software 2160p", "mpeg2video: software 2160p", "audio channels: 6", "probe 1840 ms",
            "cache key: gpu=NVIDIA GeForce RTX 3060 31.0.15.5222; cpu=AMD Ryzen 5 5600X x12; libmpv=mpv v0.40.0",
        )) assertTrue("$want in $msg", msg.contains(want))
        assertTrue(report.json()!!.containsKey("video"))
    }
}
