package com.hamzalabs.maktaba.core.crash

import java.io.ByteArrayOutputStream

/**
 * Tombstones in the format Android 12+ writes (AOSP `debuggerd/proto/tombstone.proto`), built
 * here from the proto's field numbers so the tests of every module read real-format bytes.
 */
object TombstoneBytes {
    /** A protobuf message, written field by field. */
    class Msg {
        private val out = ByteArrayOutputStream()
        private fun varint(v: Long) {
            var x = v
            while (x and 0x7fL.inv() != 0L) { out.write(((x and 0x7f) or 0x80).toInt()); x = x ushr 7 }
            out.write(x.toInt())
        }
        private fun tag(n: Int, wire: Int) = varint((n.toLong() shl 3) or wire.toLong())
        fun uint(n: Int, v: Long) = apply { tag(n, 0); varint(v) }
        fun bytes(n: Int, b: ByteArray) = apply { tag(n, 2); varint(b.size.toLong()); out.write(b) }
        fun str(n: Int, s: String) = bytes(n, s.toByteArray())
        fun msg(n: Int, m: Msg) = bytes(n, m.bytes())
        fun fixed64(n: Int, v: Long) = apply { tag(n, 1); repeat(8) { out.write(((v ushr (8 * it)) and 0xff).toInt()) } }
        fun bytes(): ByteArray = out.toByteArray()
    }

    data class F(val file: String, val relPc: Long, val function: String? = null, val offset: Long = 0, val buildId: String? = null)

    /** BacktraceFrame: rel_pc 1, pc 2, sp 3, function_name 4, function_offset 5, file_name 6, file_map_offset 7, build_id 8. */
    fun frame(f: F): Msg = Msg().uint(1, f.relPc).uint(2, 0x7a00000000 + f.relPc).uint(3, 0x7ff0001000)
        .apply { f.function?.let { str(4, it); uint(5, f.offset) } }
        .str(6, f.file).uint(7, 0)
        .apply { f.buildId?.let { str(8, it) } }

    /** Thread: id 1, name 2, registers 3, current_backtrace 4, tagged_addr_ctrl 6. */
    fun thread(id: Long, name: String, frames: List<F>): Msg = Msg().uint(1, id).str(2, name)
        .msg(3, Msg().str(1, "x0").uint(2, 0))
        .apply { frames.forEach { msg(4, frame(it)) } }
        .uint(6, 0)

    /**
     * Tombstone: arch 1, build_fingerprint 2, revision 3, timestamp 4, pid 5, tid 6, uid 7,
     * selinux_label 8, command_line 9, signal_info 10 (number 1, name 2, code 3, code_name 4,
     * has_fault_address 8, fault_address 9), abort_message 14, threads 16 (a map: key 1, value
     * 2), memory_mappings 17, log_buffers 18, open_fds 19.
     */
    fun tombstone(
        tid: Long = 4321,
        signal: String? = "SIGSEGV",
        code: String? = "SEGV_MAPERR",
        abort: String? = null,
        threads: Map<Long, List<F>>,
        tail: Boolean = true,
    ): ByteArray = Msg()
        .uint(1, 1)
        .str(2, "google/husky/husky:15/AP3A.241005.015/12366759:user/release-keys")
        .str(4, "2026-11-07 10:00:00.000000000+0300")
        .uint(5, 4300).uint(6, tid).uint(7, 10234)
        .str(8, "u:r:untrusted_app:s0:c512,c768")
        .str(9, "com.hamzalabs.maktaba.tv")
        .apply {
            if (signal != null) msg(10, Msg().uint(1, 11).str(2, signal).uint(3, 1).apply { code?.let { str(4, it) } }
                .uint(8, 1).fixed64(9, 0x10))
        }
        .apply { abort?.let { str(14, it) } }
        .apply { threads.forEach { (id, fs) -> msg(16, Msg().uint(1, id).msg(2, thread(id, if (id == tid) "RenderThread" else "worker", fs))) } }
        .apply {
            if (tail) {
                msg(17, Msg().uint(1, 0x7a00000000).uint(2, 0x7a00100000).str(7, "/data/app/~~a/com.hamzalabs.maktaba.tv-b/lib/arm64/libmpv.so"))
                msg(18, Msg().str(1, "main").msg(2, Msg().str(7, "a log line of a@b.co")))
                msg(19, Msg().uint(1, 42).str(2, "/storage/emulated/0/Movies/Family holiday.mkv"))
            }
        }
        .bytes()

    const val OWN_LIB = "/data/app/~~Zx9a==/com.hamzalabs.maktaba.tv-Qm2w==/lib/arm64/libmpv.so"
    const val LIBC = "/apex/com.android.runtime/lib64/bionic/libc.so"

    /** The crashed thread's frames, top first: an abort in libc called from the app's libmpv. */
    val CRASHED = listOf(
        F(LIBC, 0x5c1e4, "abort", 0xa4, "a1b2c3d4e5f60718293a4b5c6d7e8f90"),
        F(OWN_LIB, 0x1f2a30, "mpv_render_context_render", 0x1c, "0123456789abcdef0123456789abcdef01234567"),
        F("/data/app/~~Zx9a==/com.hamzalabs.maktaba.tv-Qm2w==/base.apk!libplayer.so", 0x4410, null, 0, "feedfacecafebeef"),
    )
    val OTHER = listOf(F(LIBC, 0x9000, "__futex_wait", 0x10))
}
