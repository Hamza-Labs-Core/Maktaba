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
import java.util.concurrent.LinkedBlockingQueue

/**
 * Story 13.22 with a fake libmpv: a stream loaded into the handle (`loadfile … replace`) is the
 * only one whose events count. What the stream before it still says (its end, its error, its
 * position) is dropped, and the new stream's own error is heard, also when it comes before the
 * new entry's id is read.
 */
class MpvVideoEntryTest {
    /** libmpv's calls the Player makes, its events given by the test. */
    private class FakeLib : LibMpv {
        val commands: MutableList<List<String?>> = Collections.synchronizedList(mutableListOf())
        private val queue = LinkedBlockingQueue<Pointer>()
        private val kept: MutableList<Memory> = Collections.synchronizedList(mutableListOf())
        @Volatile private var entries = 0L

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
                "loadfile" -> entries++
                "quit" -> push(MpvConst.EVENT_SHUTDOWN)
            }
            return 0
        }
        override fun mpv_observe_property(ctx: Pointer, replyUserdata: Long, name: String, format: Int) = 0
        override fun mpv_wait_event(ctx: Pointer, timeout: Double): Pointer = queue.take()
        override fun mpv_wakeup(ctx: Pointer) {}
        override fun mpv_error_string(error: Int) = "error $error"
        override fun mpv_render_context_create(res: PointerByReference, mpv: Pointer, params: Pointer): Int {
            res.value = keep(Memory(8))
            return 0
        }
        override fun mpv_render_context_set_update_callback(ctx: Pointer, callback: LibMpv.UpdateCallback?, data: Pointer?) {}
        override fun mpv_render_context_update(ctx: Pointer) = 0L
        override fun mpv_render_context_render(ctx: Pointer, params: Pointer) = 0
        override fun mpv_render_context_free(ctx: Pointer) {}
    }

    private class Heard : VideoEvents {
        override val volume = 1f
        override val muted = false
        val positions: MutableList<Long> = Collections.synchronizedList(mutableListOf())
        val failed: MutableList<String> = Collections.synchronizedList(mutableListOf())
        @Volatile var ended = 0
        override fun ready(durationMs: Long?, audio: List<AudioOption>) {}
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
}
