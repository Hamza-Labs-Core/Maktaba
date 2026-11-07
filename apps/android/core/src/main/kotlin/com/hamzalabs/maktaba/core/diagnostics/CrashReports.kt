package com.hamzalabs.maktaba.core.diagnostics

import android.app.ActivityManager
import android.content.Context
import android.os.Build
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.sendCrashEnvelope
import com.hamzalabs.maktaba.core.crash.CrashOutbox
import com.hamzalabs.maktaba.core.crash.Envelope
import com.hamzalabs.maktaba.core.crash.KeptCrash
import com.hamzalabs.maktaba.core.session.Home
import java.io.File
import java.util.UUID

/**
 * Whether this person, on this device, sends crash reports to a home (Epic 53 story 53.14 §3):
 * off until they say yes. Asked once per home after signing in, with two equal buttons; kept
 * in the app's preferences and changed in the account menu. An admin cannot turn it on for
 * someone else's device: it lives here, and the home refuses an envelope without its consent.
 */
class CrashConsent(context: Context) {
    private val prefs = context.getSharedPreferences("crash_reports", Context.MODE_PRIVATE)

    /** null: not asked yet (a fresh install, or one updated from a release before this). */
    fun answer(home: Home): Boolean? = key(home).let { if (prefs.contains(it)) prefs.getBoolean(it, false) else null }

    fun isOn(home: Home): Boolean = answer(home) == true

    fun set(on: Boolean, home: Home) = prefs.edit().putBoolean(key(home), on).apply()

    companion object {
        fun key(home: Home): String = "home." + (home.id ?: home.url)

        /** Off under Robolectric, so the apps' screen tests never meet the question unless they ask. */
        @Volatile
        var questionAllowed: Boolean = Build.FINGERPRINT != "robolectric"
    }
}

/**
 * The crashes this app kept (Epic 53 story 53.14 §2): [CrashLog]'s `last-crash.txt` and, on
 * Android 11 and later, the process exits Android recorded since the last start (an ANR with its
 * trace, a native crash, a low-memory kill), each kept in the [CrashOutbox] under
 * `files/crashes/`, the newest five.
 */
object CrashReports {
    private const val SEEN = "seen"

    fun outbox(context: Context) = CrashOutbox(File(context.filesDir, "crashes"))

    /** At start, once: what crashed since the last start goes into the outbox. */
    fun collect(context: Context) {
        val outbox = outbox(context)
        val prefs = context.getSharedPreferences("crash_reports_seen", Context.MODE_PRIVATE)
        val seen = prefs.getLong(SEEN, 0)
        var newest = seen
        File(context.filesDir, CrashLog.FILE).takeIf { it.exists() && it.lastModified() > seen }?.let { f ->
            KeptCrash.fromCrashLog(f.readText(), Build.MODEL)?.let { outbox.keep(it.copy(osVersion = osVersion())) }
            newest = maxOf(newest, f.lastModified())
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            val am = context.getSystemService(ActivityManager::class.java)
            val exits = runCatching { am.getHistoricalProcessExitReasons(context.packageName, 0, 10) }.getOrDefault(emptyList())
            for (x in exits.filter { it.timestamp > seen }) {
                // an ANR's trace is text; a native crash's is a binary tombstone, not kept
                val trace = if (x.reason != ExitReasons.ANR) null
                else runCatching { x.traceInputStream?.use { input -> readUpTo(input, KeptCrash.TRACE_MAX) }?.let(::String) }.getOrNull()
                ExitReasons.kept(x.reason, x.timestamp, x.description, trace, appVersion(context), osVersion(), Build.MODEL)?.let(outbox::keep)
                newest = maxOf(newest, x.timestamp)
            }
        }
        if (newest > seen) prefs.edit().putLong(SEEN, newest).apply()
    }

    /** At most [max] bytes of [input] (InputStream.readNBytes needs API 33). */
    private fun readUpTo(input: java.io.InputStream, max: Int): ByteArray {
        val out = java.io.ByteArrayOutputStream()
        val buf = ByteArray(8192)
        while (out.size() < max) {
            val n = input.read(buf, 0, minOf(buf.size, max - out.size()))
            if (n < 0) break
            out.write(buf, 0, n)
        }
        return out.toByteArray()
    }

    fun header(context: Context, tv: Boolean = false): Envelope.Header = Envelope.Header(
        app = LogRing.service(context.packageName, tv),
        appVersion = appVersion(context),
        platform = "android",
        osVersion = osVersion(),
        deviceModel = "${Build.MANUFACTURER} ${Build.MODEL}".trim(),
        installId = installId(context),
    )

    /** Sends what is due to [client]'s home when the person turned crash reports on for [home]. */
    suspend fun flush(context: Context, client: ApiClient, home: Home, tv: Boolean = false): Int =
        outbox(context).flush(CrashConsent(context).isOn(home), header(context, tv), { AppLog.ring?.lines().orEmpty() }, Redact::scrub) { env ->
            try {
                client.sendCrashEnvelope(env)
                CrashOutbox.SendResult.Sent
            } catch (e: ApiException.Status) {
                when (e.code) {
                    403 -> CrashOutbox.SendResult.Refused
                    429 -> CrashOutbox.SendResult.Later(e.retryAfterSec?.let { it * 1000 })
                    else -> CrashOutbox.SendResult.Later()
                }
            } catch (e: ApiException.SignedOut) {
                CrashOutbox.SendResult.Refused
            } catch (e: Exception) {
                CrashOutbox.SendResult.Later()
            }
        }

    /** A random id made once per install (what the home's limit counts, never a person). */
    fun installId(context: Context): String {
        val p = context.getSharedPreferences("crash_reports_seen", Context.MODE_PRIVATE)
        return p.getString("install_id", null) ?: UUID.randomUUID().toString().also { p.edit().putString("install_id", it).apply() }
    }

    private fun appVersion(context: Context) =
        runCatching { context.packageManager.getPackageInfo(context.packageName, 0).versionName }.getOrNull() ?: "-"

    private fun osVersion() = "Android ${Build.VERSION.RELEASE} (API ${Build.VERSION.SDK_INT})"
}

/** The process exits a crash report is made of (API 30+): an ANR, a native crash, a low-memory kill. */
object ExitReasons {
    // ApplicationExitInfo.REASON_* (API 30), as their values: this code also runs on Android 8
    const val LOW_MEMORY = 3
    const val CRASH_NATIVE = 5
    const val ANR = 6

    /** [reason] is an `ApplicationExitInfo.REASON_*`; others (the person swiped it away) are none. */
    fun kept(reason: Int, at: Long, description: String?, trace: String?, appVersion: String?, osVersion: String?, device: String?): KeptCrash? {
        val (type, mechanism) = when (reason) {
            ANR -> "ANR" to "anr"
            CRASH_NATIVE -> "NativeCrash" to "native"
            LOW_MEMORY -> "LowMemory" to "low_memory"
            else -> return null
        }
        return KeptCrash(at = at, type = type, reason = description.orEmpty().take(500), mechanism = mechanism,
            trace = trace?.take(KeptCrash.TRACE_MAX), appVersion = appVersion, osVersion = osVersion, device = device)
    }
}
