package com.hamzalabs.maktaba.desktop.mpv

import com.hamzalabs.maktaba.desktop.AudioOption
import com.hamzalabs.maktaba.desktop.VideoEvents
import com.sun.jna.Memory
import com.sun.jna.NativeLong
import com.sun.jna.Pointer
import com.sun.jna.ptr.PointerByReference
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.Collections
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CountDownLatch
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger

/**
 * Story 13.22 with a fake libmpv: a stream loaded into the handle (`loadfile … replace`) is the
 * only one whose events count. What the stream before it still says (its end, its error, its
 * position) is dropped, and the new stream's own error is heard, also when it comes before the
 * new entry's id is read; of several loads at once, the last's. A frame mpv drew before it says
 * the picture started is shown once it says so, also when no other follows (paused).
 */
class MpvVideoEntryTest {
    /** libmpv's calls the Player makes, its events given by the test. */
    private class FakeLib : LibMpv {
        val commands: MutableList<List<String?>> = Collections.synchronizedList(mutableListOf())
        private val queue = LinkedBlockingQueue<Pointer>()
        private val kept: MutableList<Memory> = Collections.synchronizedList(mutableListOf())
        @Volatile private var entries = 0L

        /** A `loadfile` of a URL here waits for its latch, its entry made. */
        val gates = ConcurrentHashMap<String, CountDownLatch>()

        /** How many frames mpv has to draw (each render update says so once), and how many were drawn. */
        private val frames = AtomicInteger()
        val drawn = AtomicInteger()
        @Volatile private var update: LibMpv.UpdateCallback? = null

        /** mpv has a frame to draw. */
        fun frame() {
            frames.incrementAndGet()
            update?.invoke(null)
        }

        /** Whether the events pushed so far were taken. */
        fun taken(): Boolean = queue.isEmpty()

        private fun keep(m: Memory): Memory = m.also { kept += it }

        fun push(id: Int, data: Pointer? = null) {
            val ev = keep(Memory(24)).apply {
                clear()
                setInt(0, id)
                if (data != null) setPointer(16, data)
            }
            queue.put(ev)
        }

        fun startFile(entry: Long) = push(MpvConst.EVENT_START_FILE, keep(Memory(8)).apply { setLong(0, entry) })

        fun endFile(entry: Long, reason: Int, error: Int = 0) = push(
            MpvConst.EVENT_END_FILE,
            keep(Memory(32)).apply {
                clear()
                setInt(0, reason)
                setInt(4, error)
                setLong(8, entry)
            },
        )

        fun timePos(seconds: Double) {
            val name = keep(Memory(16)).apply { setString(0, "time-pos") }
            val value = keep(Memory(8)).apply { setDouble(0, seconds) }
            push(
                MpvConst.EVENT_PROPERTY_CHANGE,
                keep(Memory(24)).apply {
                    clear()
                    setPointer(0, name)
                    setInt(8, MpvConst.FORMAT_DOUBLE)
                    setPointer(16, value)
                },
            )
        }

        override fun mpv_client_api_version() = NativeLong(2L shl 16)
        override fun mpv_create(): Pointer = keep(Memory(8))
        override fun mpv_initialize(ctx: Pointer) = 0
        override fun mpv_terminate_destroy(ctx: Pointer) {}
        override fun mpv_set_option_string(ctx: Pointer, name: String, data: String) = 0
        override fun mpv_set_property_string(ctx: Pointer, name: String, data: String) = 0
        override fun mpv_get_property_string(ctx: Pointer, name: String): Pointer? {
            val v = when (name) {
                // a loadfile replace leaves the playlist with its one entry
                "playlist/0/id" -> entries.toString()
                else -> return null
            }
            return keep(Memory(v.length + 1L)).apply { setString(0, v) }
        }
        override fun mpv_free(data: Pointer) {}
        override fun mpv_command(ctx: Pointer, args: Array<String?>): Int {
            commands += args.toList()
            when (args[0]) {
                "loadfile" -> {
                    entries++
                    gates[args[1]]?.await(10, TimeUnit.SECONDS)
                }
                "quit" -> push(MpvConst.EVENT_SHUTDOWN)
            }
            return 0
        }
        override fun mpv_command_async(ctx: Pointer, replyUserdata: Long, args: Array<String?>): Int = 0
        override fun mpv_observe_property(ctx: Pointer, replyUserdata: Long, name: String, format: Int) = 0
        override fun mpv_wait_event(ctx: Pointer, timeout: Double): Pointer = queue.take()
        override fun mpv_wakeup(ctx: Pointer) {}
        override fun mpv_error_string(error: Int) = "error $error"
        override fun mpv_render_context_create(res: PointerByReference, mpv: Pointer, params: Pointer): Int {
            res.value = keep(Memory(8))
            return 0
        }
        override fun mpv_render_context_set_update_callback(ctx: Pointer, callback: LibMpv.UpdateCallback?, data: Pointer?) {
            update = callback
        }
        override fun mpv_render_context_update(ctx: Pointer) =
            if (frames.getAndUpdate { maxOf(0, it - 1) } > 0) MpvConst.RENDER_UPDATE_FRAME else 0L
        override fun mpv_render_context_render(ctx: Pointer, params: Pointer): Int {
            drawn.incrementAndGet()
            return 0
        }
        override fun mpv_render_context_free(ctx: Pointer) {}
    }

    private class Heard : VideoEvents {
        override val volume = 1f
        override val muted = false
        val positions: MutableList<Long> = Collections.synchronizedList(mutableListOf())
        val failed: MutableList<String> = Collections.synchronizedList(mutableListOf())
        @Volatile var ended = 0
        val ready = AtomicInteger()
        override fun ready(durationMs: Long?, audio: List<AudioOption>) {
            ready.incrementAndGet()
        }
        override fun position(ms: Long) {
            positions += ms
        }
        override fun playing(on: Boolean) {}
        override fun failed(why: String) {
            failed += why
        }
        override fun ended() {
            ended++
        }
    }

    private fun until(what: String, ok: () -> Boolean) {
        val end = System.currentTimeMillis() + 10_000
        while (!ok()) {
            if (System.currentTimeMillis() > end) throw AssertionError("never: $what")
            Thread.sleep(5)
        }
    }

    @Test
    fun noAudioDeviceEndsALiveStreamThatPlayedAndFailsOneThatDidNot() {
        // CI's runners have no sound card: mpv ends the file with AO_INIT_FAILED even after the
        // picture played to its end
        val lib = FakeLib()
        val heard = Heard()
        val v = MpvVideo(lib, "http://127.0.0.1:1/c1.m3u8", 0, true, heard, null, live = true)
        try {
            until("the first loadfile") { lib.commands.any { it[0] == "loadfile" } }
            lib.startFile(1)
            lib.endFile(1, MpvConst.END_ERROR, MpvConst.ERROR_AO_INIT_FAILED)
            until("a stream that never showed failed") { heard.failed.size == 1 }
            v.load("http://127.0.0.1:1/c2.m3u8")
            lib.startFile(2)
            lib.push(MpvConst.EVENT_FILE_LOADED)
            lib.push(MpvConst.EVENT_PLAYBACK_RESTART)
            until("the second stream started") { v.started }
            lib.endFile(2, MpvConst.END_ERROR, MpvConst.ERROR_AO_INIT_FAILED)
            until("the second stream ended") { heard.ended == 1 }
            assertEquals(1, heard.failed.size)
        } finally {
            v.dispose()
        }
    }

    @Test
    fun onlyTheNewestLoadsEventsCount() {
        val lib = FakeLib()
        val heard = Heard()
        val v = MpvVideo(lib, "http://127.0.0.1:1/c1.m3u8", 0, true, heard, null, live = true)
        try {
            until("the first loadfile") { lib.commands.any { it[0] == "loadfile" } }
            lib.startFile(1)
            lib.push(MpvConst.EVENT_FILE_LOADED)
            lib.push(MpvConst.EVENT_PLAYBACK_RESTART)
            until("the first stream started") { v.started }
            lib.timePos(1.0)
            until("its position") { heard.positions == listOf(1000L) }

            // the next channel loaded into the handle; the stream before it still ends, fails
            // and moves on before the new one starts: none of it is the new one's
            v.load("http://127.0.0.1:1/c2.m3u8")
            lib.timePos(2.0)
            lib.endFile(1, MpvConst.END_ERROR, -13)
            lib.endFile(1, MpvConst.END_EOF)
            lib.startFile(2)
            lib.push(MpvConst.EVENT_FILE_LOADED)
            lib.push(MpvConst.EVENT_PLAYBACK_RESTART)
            val end = System.currentTimeMillis() + 10_000
            while (3000L !in heard.positions && System.currentTimeMillis() < end) {
                lib.timePos(3.0)
                Thread.sleep(20)
            }
            assertEquals(listOf("loadfile", "http://127.0.0.1:1/c2.m3u8", "replace", null), lib.commands.last { it[0] == "loadfile" })
            assertTrue("the replaced stream's position", 2000L !in heard.positions)
            assertEquals(3000L, heard.positions.last())
            assertEquals("the replaced stream's error", emptyList<String>(), heard.failed.toList())
            assertEquals("the replaced stream's end", 0, heard.ended)

            // the new stream's own failure is heard, at once after its load too
            lib.endFile(2, MpvConst.END_ERROR, -13)
            until("the new stream's failure") { heard.failed.size == 1 }
            v.load("http://127.0.0.1:1/c3.m3u8")
            lib.endFile(3, MpvConst.END_EOF)
            until("the third stream's end") { heard.ended == 1 }
            assertEquals(listOf("error -13"), heard.failed.toList())
        } finally {
            v.dispose()
        }
    }

    @Test
    fun aStreamOpenedPausedShowsTheFrameDrawnBeforeItsPictureStarted() {
        val lib = FakeLib()
        val v = MpvVideo(lib, "/film.mkv", 0, autoplay = false, Heard(), null)
        try {
            v.resize(16, 9)
            until("the loadfile") { lib.commands.any { it[0] == "loadfile" } }
            // mpv draws the first frame, says the picture started, and draws no other (paused)
            until("drawn at the screen's size") { lib.drawn.get() > 0 }
            val before = lib.drawn.get()
            lib.frame()
            until("the first frame drawn") { lib.drawn.get() > before }
            assertEquals("shown before its picture started", null, v.frame)
            lib.startFile(1)
            lib.push(MpvConst.EVENT_FILE_LOADED)
            lib.push(MpvConst.EVENT_PLAYBACK_RESTART)
            until("the paused picture") { v.frame != null }
            assertEquals(0, v.shown!!.load)
            // and it is not drawn again and again: nothing changes while paused
            val shown = lib.drawn.get()
            Thread.sleep(600)
            assertEquals(shown, lib.drawn.get())
        } finally {
            v.dispose()
        }
    }

    @Test
    fun ofSeveralLoadsAtOnceTheLastsEventsCount() {
        val lib = FakeLib()
        val heard = Heard()
        // the first loadfile is still on its way when two more loads are asked for
        val first = CountDownLatch(1)
        lib.gates["u1"] = first
        val v = MpvVideo(lib, "u1", 0, true, heard, null, live = true)
        try {
            until("the first loadfile") { lib.commands.any { it[0] == "loadfile" } }
            v.load("u2")
            v.load("u3")
            // the last one's id is held back too: its end comes before its id is read
            val last = CountDownLatch(1)
            lib.gates["u3"] = last
            first.countDown()
            until("the last loadfile") { lib.commands.any { it[0] == "loadfile" && it[1] == "u3" } }
            lib.startFile(1)
            lib.endFile(1, MpvConst.END_ERROR, -13)
            lib.startFile(2)
            lib.push(MpvConst.EVENT_FILE_LOADED)
            lib.push(MpvConst.EVENT_PLAYBACK_RESTART)
            lib.timePos(2.0)
            lib.endFile(2, MpvConst.END_EOF)
            lib.startFile(3)
            lib.push(MpvConst.EVENT_FILE_LOADED)
            lib.push(MpvConst.EVENT_PLAYBACK_RESTART)
            until("heard") { lib.taken() }
            Thread.sleep(100)
            assertEquals("ready before its id", 0, heard.ready.get())
            last.countDown()
            until("the last stream opened") { heard.ready.get() == 1 }
            val end = System.currentTimeMillis() + 10_000
            while (3000L !in heard.positions && System.currentTimeMillis() < end) {
                lib.timePos(3.0)
                Thread.sleep(20)
            }
            assertEquals("the last stream's positions alone", setOf(3000L), heard.positions.toSet())
            assertEquals(2, v.loads)
            assertEquals("a replaced stream's error", emptyList<String>(), heard.failed.toList())
            assertEquals("a replaced stream's end", 0, heard.ended)
            lib.endFile(3, MpvConst.END_EOF)
            until("the last stream's end") { heard.ended == 1 }
            assertEquals("ready once", 1, heard.ready.get())
        } finally {
            first.countDown()
            v.dispose()
        }
    }

    @Test
    fun theLastLoadsEndHeardBeforeItsIdIsItsOwn() {
        val lib = FakeLib()
        val heard = Heard()
        val v = MpvVideo(lib, "u1", 0, true, heard, null, live = true)
        val last = CountDownLatch(1)
        try {
            until("the first loadfile") { lib.commands.any { it[0] == "loadfile" } }
            lib.gates["u3"] = last
            v.load("u2")
            v.load("u3")
            until("the last loadfile") { lib.commands.any { it[0] == "loadfile" && it[1] == "u3" } }
            lib.endFile(2, MpvConst.END_ERROR, -13)
            lib.endFile(3, MpvConst.END_EOF)
            until("heard") { lib.taken() }
            Thread.sleep(100)
            assertEquals(0, heard.ended)
            last.countDown()
            until("its end") { heard.ended == 1 }
            assertEquals(emptyList<String>(), heard.failed.toList())
        } finally {
            last.countDown()
            v.dispose()
        }
    }
}
