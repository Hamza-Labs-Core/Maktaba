package com.hamzalabs.maktaba.core.crash

import com.hamzalabs.maktaba.core.crash.TombstoneBytes.CRASHED
import com.hamzalabs.maktaba.core.crash.TombstoneBytes.OTHER
import com.hamzalabs.maktaba.core.diagnostics.Redact
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import kotlin.random.Random

// Epic 53 story 53.14 §2: a native crash's tombstone (Android 12+) is kept with its signal, its
// scrubbed abort message and the crashed thread's frames, and sent as a scrubbed envelope.
class TombstoneTest {
    private fun tombstone(tail: Boolean = true) = TombstoneBytes.tombstone(
        tid = 4321,
        abort = "FORTIFY: read: count 9 > SSIZE_MAX on /storage/emulated/0/Movies/Family holiday.mkv for me@example.com",
        threads = mapOf(4300L to OTHER, 4321L to CRASHED),
        tail = tail,
    )
    private val full = tombstone()

    private fun parse(b: ByteArray) = Tombstone.parse(b, "com.hamzalabs.maktaba.tv", Redact::scrub)

    @Test
    fun theCrashedThreadsFramesAreReadTopFirst() {
        val t = parse(full)!!
        assertEquals("SIGSEGV", t.signal)
        assertEquals("SEGV_MAPERR", t.code)
        assertEquals(listOf("libc.so", "libmpv.so", "libplayer.so"), t.frames.map { it.module })
        assertEquals(listOf(0x5c1e4L, 0x1f2a30L, 0x4410L), t.frames.map { it.relPc })
        assertEquals(listOf("abort", "mpv_render_context_render", null), t.frames.map { it.function })
        assertEquals(listOf(0xa4L, 0x1cL, 0L), t.frames.map { it.functionOffset })
        assertEquals("0123456789abcdef0123456789abcdef01234567", t.frames[1].buildId)
        assertEquals(listOf(false, true, true), t.frames.map { it.inApp })
    }

    @Test
    fun theAbortMessageKeepsNoPathNorAddress() {
        val m = parse(full)!!.abortMessage!!
        assertEquals("FORTIFY: read: count 9 > SSIZE_MAX on <path> for [EMAIL]", m)
    }

    @Test
    fun theFramesReachTheEnvelopeScrubbed() {
        val t = parse(full)!!
        val crash = KeptCrash(at = 1_800_000_000_000, type = t.signal!!, reason = t.abortMessage!!, mechanism = "native", nativeFrames = t.frames)
        // kept as a file and read back, as the outbox does
        val kept = KeptCrash.decode(crash.encode())!!
        val env = Envelope.build(Envelope.Header("tv", "0.3.95", "android", "Android 15", "Google Pixel 8", "i-1"), kept.event("com.hamzalabs.maktaba"), emptyList(), Redact::scrub)
        val text = String(env)
        val event = Json.parseToJsonElement(text.lines()[2]).jsonObject
        assertEquals("native", event["platform"]!!.jsonPrimitive.content)
        val ex = event["exception"]!!.jsonObject["values"]!!.jsonArray[0].jsonObject
        assertEquals("SIGSEGV", ex["type"]!!.jsonPrimitive.content)
        val frames = ex["stacktrace"]!!.jsonObject["frames"]!!.jsonArray.map { it.jsonObject }
        // oldest first: the crashing frame is last
        assertEquals(
            listOf("libplayer.so + 0x4410", "mpv_render_context_render+0x1c", "abort+0xa4"),
            frames.map { it["function"]!!.jsonPrimitive.content },
        )
        assertEquals(listOf("libplayer.so", "libmpv.so", "libc.so"), frames.map { it["package"]!!.jsonPrimitive.content })
        assertEquals("0x5c1e4", frames.last()["instruction_addr"]!!.jsonPrimitive.content)
        assertEquals(listOf(true, true, false), frames.map { it["in_app"]!!.jsonPrimitive.content.toBoolean() })
        val images = event["debug_meta"]!!.jsonObject["images"]!!.jsonArray.map { it.jsonObject }
        assertEquals(listOf("libc.so", "libmpv.so", "libplayer.so"), images.map { it["code_file"]!!.jsonPrimitive.content })
        assertEquals("0123456789abcdef0123456789abcdef01234567", images[1]["code_id"]!!.jsonPrimitive.content)
        frames.forEach { f ->
            val i = f["addr_mode"]!!.jsonPrimitive.content.removePrefix("rel:").toInt()
            assertEquals(f["package"], images[i]["code_file"])
        }
        // nothing of the device's folders, the person, the other thread or the process
        listOf("/data/", "/apex/", "/storage", "Movies", "example.com", "a@b.co", "__futex_wait", "untrusted_app", "release-keys", "~~Zx9a")
            .forEach { assertFalse("$it in the envelope", text.contains(it)) }
    }

    @Test
    fun theOnlyThreadIsTheCrashedOneWhenTheTidIsMissing() {
        assertEquals(3, parse(TombstoneBytes.tombstone(tid = 0, threads = mapOf(7L to CRASHED)))!!.frames.size)
        // a known tid that is not there: no frames, never another thread's
        assertEquals(emptyList<NativeFrame>(), parse(TombstoneBytes.tombstone(tid = 9, threads = mapOf(7L to CRASHED)))!!.frames)
    }

    @Test
    fun aStreamCutAfterTheThreadsKeepsThem() {
        // the read stops at READ_MAX, inside the memory maps or the logs
        val noTail = tombstone(tail = false).size
        val t = parse(full.copyOf(noTail + 5))!!
        assertEquals(3, t.frames.size)
    }

    @Test
    fun aStreamCutAnywhereNeverThrowsAndNeverInventsFrames() {
        for (n in 0..full.size) {
            val t = parse(full.copyOf(n)) ?: continue
            assertTrue(t.frames.isEmpty() || t.frames == parse(full)!!.frames)
        }
        // cut before the signal: not a tombstone
        assertNull(parse(full.copyOf(20)))
    }

    @Test
    fun garbageIsNotATombstone() {
        assertNull(parse(ByteArray(0)))
        assertNull(parse("\"main\" prio=5 tid=1 Native\n  | group=\"main\"".toByteArray()))
        assertNull(parse(byteArrayOf(0x08, 0xff.toByte(), 0xff.toByte())))
        val r = Random(53_14)
        repeat(5_000) {
            val b = ByteArray(r.nextInt(1, 400)).also(r::nextBytes)
            parse(b)?.let { t -> assertTrue(t.frames.all { f -> !f.module.contains('/') }) }
        }
        // a tombstone with one byte flipped in the crashed thread does not crash the reader
        repeat(full.size) { i -> parse(full.copyOf().also { it[i] = (it[i].toInt() xor 0x5a).toByte() }) }
    }

    @Test
    fun aLibrarysPathBecomesItsNameAndOtherPathsGo() {
        assertEquals("libmpv.so", Tombstone.module(TombstoneBytes.OWN_LIB))
        assertEquals("libplayer.so", Tombstone.module("/data/app/x/base.apk!libplayer.so"))
        assertEquals("[anon:dalvik-jit-code-cache]", Tombstone.module("[anon:dalvik-jit-code-cache]"))
        assertEquals("x", Tombstone.module("[anon:/data/x"))
        assertEquals("?", Tombstone.module(""))
        assertEquals("dlopen of libc++_shared.so.1 at <path> failed", Tombstone.paths("dlopen of /system/lib64/libc++_shared.so.1 at /sdcard/Download/mine failed"))
        assertEquals("(<path>)", Tombstone.paths("(/storage/emulated/0/Movies/Family holiday.mkv)"))
        assertEquals("open <path>: ENOENT", Tombstone.paths("open /data/user/0/com.hamzalabs.maktaba/files/x.db: ENOENT"))
    }

    @Test
    fun aCrashKeptBeforeTombstonesStillReads() {
        val old = """{"kind":"maktaba.crash","at":1,"type":"NativeCrash","reason":"crash","mechanism":"native"}"""
        val c = KeptCrash.decode(old)
        assertNotNull(c)
        assertTrue(c!!.nativeFrames.isEmpty())
        val event: JsonObject = c.event()
        assertEquals("java", event["platform"]!!.jsonPrimitive.content)
        assertNull(event["debug_meta"])
    }
}
