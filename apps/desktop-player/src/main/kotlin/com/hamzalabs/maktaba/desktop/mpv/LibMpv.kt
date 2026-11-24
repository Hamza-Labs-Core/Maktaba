package com.hamzalabs.maktaba.desktop.mpv

import com.sun.jna.Callback
import com.sun.jna.Library
import com.sun.jna.Native
import com.sun.jna.NativeLong
import com.sun.jna.Pointer
import com.sun.jna.ptr.PointerByReference
import java.io.File

/**
 * libmpv's client API (mpv/client.h), as much of it as the Player uses (story 13.18). Every
 * call is thread-safe on mpv's side; the Player calls none of them on the UI thread except
 * [mpv_set_property_string] and [mpv_command], which return at once.
 */
@Suppress("FunctionName")
interface LibMpv : Library {
    fun mpv_client_api_version(): NativeLong
    fun mpv_create(): Pointer?
    fun mpv_initialize(ctx: Pointer): Int
    fun mpv_terminate_destroy(ctx: Pointer)
    fun mpv_set_option_string(ctx: Pointer, name: String, data: String): Int
    fun mpv_set_property_string(ctx: Pointer, name: String, data: String): Int
    fun mpv_get_property_string(ctx: Pointer, name: String): Pointer?
    fun mpv_free(data: Pointer)
    /** [args] ends with a null. */
    fun mpv_command(ctx: Pointer, args: Array<String?>): Int
    /** [args] ends with a null; the result comes as an MPV_EVENT_COMMAND_REPLY with [replyUserdata]. */
    fun mpv_command_async(ctx: Pointer, replyUserdata: Long, args: Array<String?>): Int
    fun mpv_observe_property(ctx: Pointer, replyUserdata: Long, name: String, format: Int): Int
    fun mpv_wait_event(ctx: Pointer, timeout: Double): Pointer
    fun mpv_wakeup(ctx: Pointer)
    fun mpv_error_string(error: Int): String

    // render.h: the software renderer (story 13.18: libmpv draws each frame into the Player's
    // own memory, which Compose draws; an LGPL libmpv has no X11 window to draw in)
    fun mpv_render_context_create(res: PointerByReference, mpv: Pointer, params: Pointer): Int
    fun mpv_render_context_set_update_callback(ctx: Pointer, callback: UpdateCallback?, data: Pointer?)
    fun mpv_render_context_update(ctx: Pointer): Long
    fun mpv_render_context_render(ctx: Pointer, params: Pointer): Int
    fun mpv_render_context_free(ctx: Pointer)

    /** mpv_render_update_fn: a new frame may be drawn (called on one of mpv's threads; it must only signal). */
    fun interface UpdateCallback : Callback {
        fun invoke(data: Pointer?)
    }
}

/** mpv's numbers the Player reads (client.h). */
internal object MpvConst {
    const val FORMAT_NONE = 0
    const val FORMAT_FLAG = 3
    const val FORMAT_DOUBLE = 5

    const val EVENT_NONE = 0
    const val EVENT_SHUTDOWN = 1
    const val EVENT_COMMAND_REPLY = 5
    const val EVENT_START_FILE = 6
    const val EVENT_END_FILE = 7
    const val EVENT_FILE_LOADED = 8
    const val EVENT_AUDIO_RECONFIG = 18
    const val EVENT_PLAYBACK_RESTART = 21
    const val EVENT_PROPERTY_CHANGE = 22

    const val RENDER_PARAM_INVALID = 0
    const val RENDER_PARAM_API_TYPE = 1
    const val RENDER_PARAM_SW_SIZE = 17
    const val RENDER_PARAM_SW_FORMAT = 18
    const val RENDER_PARAM_SW_STRIDE = 19
    const val RENDER_PARAM_SW_POINTER = 20
    const val RENDER_UPDATE_FRAME = 1L

    const val END_EOF = 0
    const val END_ERROR = 4

    /** MPV_ERROR_AO_INIT_FAILED: no audio device could be opened (the picture plays on). */
    const val ERROR_AO_INIT_FAILED = -14

    /** dlopen's RTLD_LAZY alone (Linux): local symbols, as RTLD_LOCAL is 0. */
    const val RTLD_LAZY_FLAG = 1

    /** LoadLibraryEx's flag (Windows): a DLL's own folder is searched for the DLLs it needs. */
    const val LOAD_WITH_ALTERED_SEARCH_PATH = 0x8

    /** The client API's major this binding is written against (mpv 0.35 and later: 2). */
    const val API_MAJOR = 2L
}

/**
 * Where libmpv is, and whether it loads. The Player carries its own libmpv and FFmpeg in its
 * install folder (compose's app resources: `compose.application.resources.dir`) and loads it
 * by that absolute path only, never from PATH, the working directory or the system's
 * libraries. [reason] says why it did not load, for the notice and the problem report.
 */
sealed interface MpvLoad {
    data class Loaded(val lib: LibMpv, val path: File) : MpvLoad
    data class Missing(val reason: String) : MpvLoad

    companion object {
        /** The library's file name on this system. */
        fun fileName(os: String = System.getProperty("os.name").orEmpty()): String = when {
            os.startsWith("Windows") -> "libmpv-2.dll"
            os.startsWith("Mac") -> "libmpv.2.dylib"
            else -> "libmpv.so.2"
        }

        /**
         * The library in [dir] (the app's resources), or why it cannot be used. Whether it is
         * used at all is [Engines]' choice (`MAKTABA_PLAYER_ENGINE`, then Profile's setting).
         */
        fun from(dir: String?): MpvLoad {
            if (dir.isNullOrBlank()) return Missing("no app resources folder (a development run)")
            val file = File(dir, fileName())
            if (!file.isFile) return Missing("${file.name} is not in ${file.parent}")
            return try {
                val lib = Native.load(file.absolutePath, LibMpv::class.java, openOptions())
                val major = lib.mpv_client_api_version().toLong() ushr 16
                if (major != MpvConst.API_MAJOR) Missing("libmpv's client API is $major, the Player's ${MpvConst.API_MAJOR}")
                else Loaded(lib, file)
            } catch (e: LinkageError) {
                Missing("${file.name} did not load: ${e.message}")
            }
        }

        /**
         * How libmpv is opened. On Linux, RTLD_LOCAL (no RTLD_GLOBAL): FFmpeg's symbols inside
         * libmpv stay out of the process's namespace, where JavaFX's media plugin finds the
         * system's FFmpeg. On Windows the same option is LoadLibraryEx's flags:
         * LOAD_WITH_ALTERED_SEARCH_PATH, so the DLLs beside libmpv-2.dll are the ones it loads
         * (not 1, DONT_RESOLVE_DLL_REFERENCES, which loads it without its imports).
         */
        fun openOptions(os: String = System.getProperty("os.name").orEmpty()): Map<String, Any> = when {
            os.startsWith("Linux") -> mapOf(Library.OPTION_OPEN_FLAGS to MpvConst.RTLD_LAZY_FLAG)
            os.startsWith("Windows") -> mapOf(Library.OPTION_OPEN_FLAGS to MpvConst.LOAD_WITH_ALTERED_SEARCH_PATH)
            else -> emptyMap()
        }

        /** The packaged Player's libmpv, looked for once. */
        val current: MpvLoad by lazy { from(System.getProperty("compose.application.resources.dir")) }
    }
}
