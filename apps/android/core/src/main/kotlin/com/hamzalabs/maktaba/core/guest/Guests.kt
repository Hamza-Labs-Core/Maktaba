package com.hamzalabs.maktaba.core.guest

import android.content.Context
import android.content.pm.ApplicationInfo
import androidx.annotation.StringRes
import androidx.compose.runtime.Composable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.res.stringResource
import com.hamzalabs.maktaba.core.R
import com.hamzalabs.maktaba.core.diagnostics.AppLog
import com.hamzalabs.maktaba.core.parental.KeptLimit
import com.hamzalabs.maktaba.core.session.KeystoreSecretBox
import com.hamzalabs.maktaba.core.session.PreferencesStore
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import java.io.File

/**
 * Story 50.27 §2 on Android: where an app's guest space keeps its passes, which edge it talks to,
 * whether Watch a broadcast shows at all, and the words of the edge's states. The Player (phone,
 * foldable, tablet) and the TV app build their screens on it.
 */
object Guests {
    /** The preferences file of the debug edge (the UI tests' mock edge); never read by a release build. */
    const val PREFS = "guest"
    const val KEY_EDGE = "edge"

    /**
     * The saved broadcasts of [context]'s app: sealed with a Keystore key that never leaves the
     * device (`<app>.guest`), in a preferences file the apps' backup rules keep out of cloud
     * backups and device transfers (`data_extraction_rules.xml`, `allowBackup=false`). A pass
     * counts as one of its invite's devices, so it must not move to another one.
     */
    fun store(context: Context): GuestPassStore {
        val app = context.packageName
        return GuestPassStore(PreferencesStore(context, "$app.guest"), KeystoreSecretBox("$app.guest"))
    }

    /** The edge's guest API for [context]'s app, in the app's language. */
    fun client(context: Context): GuestClient {
        val app = context.applicationContext
        return GuestClient(edge(app).toHttpUrl(), language = { language(app) }, log = AppLog::info)
    }

    /**
     * `watch.maktaba.tv`; a debuggable build whose `guest` preferences name an edge on this
     * device (`http://127.0.0.1:<port>`, the UI tests' mock edge) talks to it instead.
     */
    fun edge(context: Context): String {
        val debuggable = (context.applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE) != 0
        if (debuggable) {
            val local = PreferencesStore(context, PREFS).get(KEY_EDGE)?.toHttpUrlOrNull()
            if (local != null && local.host in setOf("127.0.0.1", "localhost")) return local.toString().trimEnd('/')
        }
        return GuestEdge.PRODUCTION
    }

    /** `ar` or `en`, as the app shows itself (the edge answers its messages in it). */
    fun language(context: Context): String =
        if (context.resources.configuration.locales[0]?.language == "ar") "ar" else "en"

    /**
     * Whether Watch a broadcast shows on this device before anyone signs in (the home picker): not
     * when a sign-in on this device was under a parental limit when its home last said (Epic 57's
     * kept marks, `files/parental/`). A broadcast is outside any home's ratings, and the edge never
     * learns who watches, so this cannot be a server rule: hiding the entry is a convenience, and
     * the device's own restrictions are the boundary (as on Apple, #1400).
     */
    fun allowedOnDevice(context: Context): Boolean = allowedOnDevice(File(context.filesDir, KeptLimit.InFile.FOLDER))

    internal fun allowedOnDevice(marks: File): Boolean =
        marks.listFiles { f -> f.name.endsWith(".limit") }.orEmpty().none { KeptLimit.InFile(it).read()?.limited == true }

    /** The words of a state or refusal key (`broadcast.tuning`, `guest.badCode`); "site.error"'s for a key the app does not know. */
    @StringRes
    fun words(key: String): Int = when (key) {
        "broadcast.tuning" -> R.string.broadcast_tuning
        "broadcast.homeOffline" -> R.string.broadcast_home_offline
        "broadcast.off" -> R.string.broadcast_off
        "broadcast.ended" -> R.string.broadcast_ended
        "broadcast.geoBlocked" -> R.string.broadcast_geo_blocked
        "broadcast.full" -> R.string.broadcast_full
        "broadcast.waiting" -> R.string.broadcast_waiting
        "broadcast.ipLimit" -> R.string.broadcast_ip_limit
        "broadcast.tooManyTries" -> R.string.broadcast_too_many_tries
        "broadcast.dailyLimit" -> R.string.broadcast_daily_limit
        "broadcast.invite.used" -> R.string.broadcast_invite_used
        "broadcast.invite.expired" -> R.string.broadcast_invite_expired
        "broadcast.invite.invalid" -> R.string.broadcast_invite_invalid
        "broadcast.revoked" -> R.string.broadcast_revoked
        "broadcast.inviteRequired" -> R.string.broadcast_invite_required
        "site.verify" -> R.string.guest_verify
        "guest.network" -> R.string.guest_network
        "guest.badCode" -> R.string.guest_bad_code
        "guest.badInvite" -> R.string.guest_bad_invite
        else -> R.string.guest_error
    }

    /** Every key [words] knows (its test checks each has its own words). */
    internal val keys = listOf(
        "broadcast.tuning", "broadcast.homeOffline", "broadcast.off", "broadcast.ended", "broadcast.geoBlocked", "broadcast.full",
        "broadcast.waiting", "broadcast.ipLimit", "broadcast.tooManyTries", "broadcast.dailyLimit", "broadcast.invite.used",
        "broadcast.invite.expired", "broadcast.invite.invalid", "broadcast.revoked", "broadcast.inviteRequired", "site.verify",
        "site.error", "guest.network", "guest.badCode", "guest.badInvite",
    )
}

/** The words of [key] (a state's or a refusal's), in the app's language. */
@Composable
fun guestWords(key: String): String = stringResource(Guests.words(key))

/**
 * Opens the app's Watch a broadcast from the home picker; null in an app without it (the Remote)
 * or where it is hidden (a parental limit on this device). The Player provides it.
 */
val LocalWatchBroadcast = staticCompositionLocalOf<(() -> Unit)?> { null }
