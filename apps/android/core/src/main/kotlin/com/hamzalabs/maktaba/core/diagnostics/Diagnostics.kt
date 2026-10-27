package com.hamzalabs.maktaba.core.diagnostics

import android.content.Context
import android.os.Build
import android.util.Log
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.errorKind
import kotlinx.coroutines.CoroutineExceptionHandler
import java.io.File
import java.time.Instant

/**
 * The apps' log lines, under one tag (`adb logcat -s Maktaba`). A failure is logged by where it
 * happened and what it was (its kind, its class, the home's status and problem slug), never by
 * its message: a transport message names the home's address, a body may name a person.
 */
object AppLog {
    const val TAG = "Maktaba"

    /** [where] failed with [e]: one warning line, with the redacted stack. */
    fun failure(where: String, e: Throwable) {
        Log.w(TAG, "$where failed: ${describe(e)}", Redacted.of(e))
    }

    fun info(message: String) {
        Log.i(TAG, message)
    }

    /**
     * [e] in a line that is safe to log: its [errorKind], its class and, for an answer from the
     * home, the status and the problem's slug (`HTTP 404 rendition-not-found`).
     */
    fun describe(e: Throwable): String {
        val parts = mutableListOf(e.errorKind().name, e.javaClass.simpleName.ifEmpty { e.javaClass.name })
        if (e is ApiException.Status) {
            parts += "HTTP ${e.code}"
            e.problem?.slug?.takeIf { it.isNotEmpty() }?.let { parts += it }
            e.problem?.code?.takeIf { it.isNotEmpty() }?.let { parts += it }
        }
        if (e is ApiException.NotReady) parts += "HTTP ${e.code}"
        return parts.joinToString(" ")
    }

    /** For a scope whose work outlives a screen: a failure there is logged, never a crash. */
    fun handler(where: String): CoroutineExceptionHandler = CoroutineExceptionHandler { _, e -> failure(where, e) }
}

/**
 * A copy of a throwable's classes and stack frames without its messages (they can carry an
 * address, a path or a name): what a log or a crash report keeps.
 */
class Redacted private constructor(val original: String) : Throwable(original) {
    companion object {
        fun of(e: Throwable): Redacted = of(e, HashSet())

        private fun of(e: Throwable, seen: MutableSet<Throwable>): Redacted {
            seen += e
            val r = Redacted(e.javaClass.name)
            r.stackTrace = e.stackTrace
            e.cause?.takeIf { it !in seen }?.let { r.initCause(of(it, seen)) }
            return r
        }
    }

    override fun toString(): String = original
}

/**
 * The last crash, kept on the device: an uncaught exception writes what it was (classes and
 * stack frames, no messages), the app's version, Android's version and when, to
 * `files/last-crash.txt`, then lets Android end the app as before. Nothing personal is kept: no
 * home address, account or token. The next start logs that it is there.
 */
object CrashLog {
    const val FILE = "last-crash.txt"

    /** Records uncaught exceptions for this process (call once, from `Application.onCreate`). */
    fun install(context: Context) {
        val file = File(context.filesDir, FILE)
        val version = runCatching {
            context.packageManager.getPackageInfo(context.packageName, 0).versionName
        }.getOrNull() ?: "?"
        val app = "${context.packageName} $version"
        val os = "Android ${Build.VERSION.RELEASE} (API ${Build.VERSION.SDK_INT})"
        val previous = Thread.getDefaultUncaughtExceptionHandler()
        Thread.setDefaultUncaughtExceptionHandler { thread, e ->
            // never let the record get in the way of the crash itself
            try {
                file.writeText(report(e, thread.name, app, os, Instant.now()))
            } catch (_: Throwable) {
                // the disk is full or gone: the system's own crash log still has it
            }
            previous?.uncaughtException(thread, e)
        }
        if (file.exists()) AppLog.info("the app closed unexpectedly on ${Instant.ofEpochMilli(file.lastModified())}; kept in $FILE")
    }

    /** The last crash's report, or null when there is none. */
    fun last(context: Context): String? = File(context.filesDir, FILE).takeIf { it.exists() }?.readText()

    fun clear(context: Context) {
        File(context.filesDir, FILE).delete()
    }

    /** The report for [e] on [thread]: plain text, without any exception's message. */
    fun report(e: Throwable, thread: String, app: String, os: String, at: Instant): String = buildString {
        appendLine("app: $app")
        appendLine("os: $os")
        appendLine("at: $at")
        appendLine("thread: $thread")
        append(Redacted.of(e).stackTraceToString())
    }
}
