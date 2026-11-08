package com.hamzalabs.maktaba.core.crash

import kotlinx.serialization.Serializable

/**
 * One frame of a native crash's crashed thread, as kept (Epic 53 story 53.14 §2): the library's
 * file name only (never its path, which can hold an install's or a person's folder), the pc
 * relative to it, the function and the offset into it when the unwinder knew them, and the
 * library's build id, which symbolicates the frame.
 */
@Serializable
data class NativeFrame(
    val module: String,
    val relPc: Long,
    val function: String? = null,
    val functionOffset: Long = 0,
    val buildId: String? = null,
    /** a library of the app's own install, not the system's */
    val inApp: Boolean = false,
)

/**
 * A native crash's tombstone, as Android 12 and later records it in `ApplicationExitInfo
 * .traceInputStream`: a protobuf `Tombstone` (AOSP `system/core/debuggerd/proto/tombstone.proto`).
 * Only what a crash report needs is read, with a small reader of the wire format: the signal,
 * the abort message, the crashed thread's backtrace. Anything that does not parse is none, so
 * the crash is still kept with its reason alone.
 */
object Tombstone {
    /** What a tombstone is read up to: the threads come before the memory maps and logs. */
    const val READ_MAX = 2 shl 20
    const val FUNCTION_MAX = 256
    const val ABORT_MAX = 500

    // tombstone.proto field numbers
    private const val T_TID = 6
    private const val T_SIGNAL = 10
    private const val T_ABORT = 14
    private const val T_THREADS = 16
    private const val SIG_NAME = 2
    private const val SIG_CODE_NAME = 4
    private const val MAP_KEY = 1
    private const val MAP_VALUE = 2
    private const val TH_ID = 1
    private const val TH_BACKTRACE = 4
    private const val F_REL_PC = 1
    private const val F_FUNCTION = 4
    private const val F_FUNCTION_OFFSET = 5
    private const val F_FILE = 6
    private const val F_BUILD_ID = 8

    data class Parsed(val signal: String?, val code: String?, val abortMessage: String?, val frames: List<NativeFrame>)

    /**
     * The tombstone in [bytes], or null when it is not one. A stream cut short (read up to
     * [READ_MAX]) keeps what came before the cut; [ownPackage] marks the app's own libraries.
     */
    fun parse(bytes: ByteArray, ownPackage: String = "com.hamzalabs.maktaba", scrub: (String) -> String = { it }): Parsed? = try {
        var tid: Long? = null
        var signal: String? = null
        var code: String? = null
        var abort: String? = null
        val threads = mutableMapOf<Long, List<NativeFrame>>()
        Wire(bytes, 0, bytes.size).fields(tolerateCut = true) { n, w, r ->
            when {
                n == T_TID && w == Wire.VARINT -> tid = r.varint()
                n == T_SIGNAL && w == Wire.LEN -> r.message().fields { sn, sw, sr ->
                    when {
                        sn == SIG_NAME && sw == Wire.LEN -> signal = sr.string()
                        sn == SIG_CODE_NAME && sw == Wire.LEN -> code = sr.string()
                        else -> sr.skip(sw)
                    }
                }
                n == T_ABORT && w == Wire.LEN -> abort = r.string()
                n == T_THREADS && w == Wire.LEN -> {
                    var key: Long? = null
                    var frames: List<NativeFrame> = emptyList()
                    var id: Long? = null
                    r.message().fields { en, ew, er ->
                        when {
                            en == MAP_KEY && ew == Wire.VARINT -> key = er.varint()
                            en == MAP_VALUE && ew == Wire.LEN -> {
                                val fs = mutableListOf<NativeFrame>()
                                er.message().fields { tn, tw, tr ->
                                    when {
                                        tn == TH_ID && tw == Wire.VARINT -> id = tr.varint()
                                        tn == TH_BACKTRACE && tw == Wire.LEN -> fs += frame(tr.message(), ownPackage, scrub)
                                        else -> tr.skip(tw)
                                    }
                                }
                                frames = fs
                            }
                            else -> er.skip(ew)
                        }
                    }
                    (key ?: id)?.let { threads[it] = frames }
                }
                else -> r.skip(w)
            }
        }
        // the crashed thread is the tombstone's tid; with none, its only thread (never another one)
        val crashed = if (tid != null && tid != 0L) threads[tid].orEmpty() else threads.values.singleOrNull().orEmpty()
        val sig = signal?.takeIf { SIGNAL.matches(it) }
        if (sig == null && crashed.isEmpty()) null
        else Parsed(
            signal = sig,
            code = code?.takeIf { SIGNAL_CODE.matches(it) },
            abortMessage = abort?.let { scrub(paths(it)).take(ABORT_MAX) }?.takeIf { it.isNotBlank() },
            frames = crashed.take(Envelope.MAX_FRAMES),
        )
    } catch (e: WireException) {
        null
    }

    private fun frame(r: Wire, ownPackage: String, scrub: (String) -> String): NativeFrame {
        var relPc = 0L
        var function: String? = null
        var offset = 0L
        var file = ""
        var buildId: String? = null
        r.fields { n, w, fr ->
            when {
                n == F_REL_PC && w == Wire.VARINT -> relPc = fr.varint()
                n == F_FUNCTION && w == Wire.LEN -> function = fr.string()
                n == F_FUNCTION_OFFSET && w == Wire.VARINT -> offset = fr.varint()
                n == F_FILE && w == Wire.LEN -> file = fr.string()
                n == F_BUILD_ID && w == Wire.LEN -> buildId = fr.string()
                else -> fr.skip(w)
            }
        }
        return NativeFrame(
            module = module(file),
            relPc = relPc,
            function = function?.takeIf { it.isNotEmpty() }?.let { scrub(paths(it)).take(FUNCTION_MAX) },
            functionOffset = offset,
            buildId = buildId?.takeIf { BUILD_ID.matches(it) },
            inApp = file.contains("/$ownPackage") || file.contains("$ownPackage-"),
        )
    }

    /**
     * A library's file name only: `/data/app/~~x/com.y-z/lib/arm64/libmpv.so` and
     * `/data/app/.../base.apk!libmpv.so` are `libmpv.so`; an anonymous map (`[anon:dalvik-jit-code-cache]`) is kept as it is.
     */
    fun module(file: String): String {
        if (file.isEmpty()) return "?"
        if (ANON.matches(file)) return file
        return file.substringAfterLast('/').substringAfterLast('!').take(128).ifEmpty { "?" }
    }

    /** Each absolute path in [s] becomes its library's name, or `<path>` when it is not a library. */
    fun paths(s: String): String = PATH.replace(s) { m ->
        val last = m.value.substringAfterLast('/')
        if (LIBRARY.matches(last)) last else "<path>"
    }

    private val SIGNAL = Regex("SIG[A-Z0-9]{2,8}")
    private val SIGNAL_CODE = Regex("[A-Z][A-Z0-9_]{1,31}")
    private val BUILD_ID = Regex("[0-9a-fA-F]{8,128}")
    // a folder's name has no space; the file's may ("Family holiday.mkv"), up to its extension
    private val PATH = Regex(
        """(?<![\w.])/(?:[^\s/'"(),:]+/)+(?:[^\s/'"(),:][^/'"(),:\n]*?\.[A-Za-z0-9]{1,5}(?=$|[\s'"(),:])|[^\s/'"(),:]*)""",
    )
    private val ANON = Regex("\\[[\\w:.-]{1,62}]")
    private val LIBRARY = Regex("[\\w.+-]+\\.so(\\.\\d+)*")
}

private class WireException(msg: String) : Exception(msg)

/** A protobuf wire reader over [b] from [pos] to [end]: varints, lengths, fixed 32 and 64. */
private class Wire(private val b: ByteArray, private var pos: Int, private val end: Int) {
    fun varint(): Long {
        var out = 0L
        var shift = 0
        while (shift < 64) {
            if (pos >= end) throw WireException("cut in a varint")
            val x = b[pos++].toInt()
            out = out or ((x and 0x7f).toLong() shl shift)
            if (x and 0x80 == 0) return out
            shift += 7
        }
        throw WireException("varint too long")
    }

    private fun length(): Int {
        val n = varint()
        if (n < 0 || n > end - pos) throw WireException("length past the end")
        return n.toInt()
    }

    fun message(): Wire {
        val n = length()
        return Wire(b, pos, pos + n).also { pos += n }
    }

    fun string(): String {
        val n = length()
        return String(b, pos, n, Charsets.UTF_8).also { pos += n }
    }

    fun skip(wire: Int) {
        when (wire) {
            VARINT -> varint()
            I64 -> advance(8)
            LEN -> advance(length())
            I32 -> advance(4)
            else -> throw WireException("wire type $wire")
        }
    }

    private fun advance(n: Int) {
        if (n > end - pos) throw WireException("cut")
        pos += n
    }

    /**
     * Each field, in order, to [f], which reads or skips its value. With [tolerateCut], a field
     * cut short by the end stops the reading and keeps what came before it.
     */
    fun fields(tolerateCut: Boolean = false, f: (Int, Int, Wire) -> Unit) {
        while (pos < end) {
            val start = pos
            try {
                val tag = varint()
                val number = (tag ushr 3).toInt()
                if (number <= 0 || tag ushr 3 > 0x1fffffff) throw WireException("field $number")
                f(number, (tag and 7).toInt(), this)
            } catch (e: WireException) {
                // a cut is the end of the bytes read; anything else is not a tombstone
                if (tolerateCut && start > 0 && cutFrom(start)) return
                throw e
            }
        }
    }

    /** Whether the field from [start] runs past the end (a stream read up to its limit). */
    private fun cutFrom(start: Int): Boolean {
        pos = start
        return try {
            val tag = varint()
            when ((tag and 7).toInt()) {
                LEN -> varint() > end - pos
                VARINT -> { varint(); false }
                I64 -> 8 > end - pos
                I32 -> 4 > end - pos
                else -> false
            }
        } catch (e: WireException) {
            pos >= end
        }
    }

    companion object {
        const val VARINT = 0
        const val I64 = 1
        const val LEN = 2
        const val I32 = 5
    }
}
