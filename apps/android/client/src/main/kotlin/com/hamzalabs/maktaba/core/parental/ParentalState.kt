package com.hamzalabs.maktaba.core.parental

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.AskAnswer
import com.hamzalabs.maktaba.core.api.MyParental
import com.hamzalabs.maktaba.core.api.MyTime
import com.hamzalabs.maktaba.core.api.ParentalRequest
import com.hamzalabs.maktaba.core.api.PinHolder
import com.hamzalabs.maktaba.core.api.SubjectKind
import com.hamzalabs.maktaba.core.api.UnlockAnswer
import com.hamzalabs.maktaba.core.api.askParent
import com.hamzalabs.maktaba.core.api.instantOrNull
import com.hamzalabs.maktaba.core.api.myParental
import com.hamzalabs.maktaba.core.api.myRequests
import com.hamzalabs.maktaba.core.api.myTime
import com.hamzalabs.maktaba.core.api.parentPinHolders
import com.hamzalabs.maktaba.core.api.unlockWithPin
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * What the signed-in account is under parental controls, for one home's screens (Epic 57):
 * its limit ([mine], `GET /api/me/parental`) and its time ([time], `GET /api/me/parental/time`,
 * asked only for a restricted account and only while a screen follows it).
 *
 * Until the home answers, and when it does not, nobody is restricted here: an app never hides
 * or refuses on its own, because the home does both whatever the app shows.
 *
 * When the limit changes (its `version`, the tier in force, the device's limit: story 57.05)
 * what the app kept for the account was read under another limit: [onChange] drops it (kept
 * Home rows, page states, the queue window, pictures) and [epoch] moves, so the screens read
 * again. [kept] remembers the last limit on the device, so a change made while the app was
 * closed is seen at the first answer of the next launch.
 */
class ParentalState(private val client: ApiClient, private val kept: KeptLimit? = null) {
    private val _mine = MutableStateFlow(MyParental.None)
    val mine: StateFlow<MyParental> = _mine.asStateFlow()

    private val _epoch = MutableStateFlow(0)

    /** Moves each time the limit changed: what was loaded under the limit before is read again. */
    val epoch: StateFlow<Int> = _epoch.asStateFlow()

    /** Drops what the app kept for the account; runs before [epoch] moves. */
    var onChange: (suspend () -> Unit)? = null

    /** The home answered in this launch (a home too old to have the route counts). */
    private var answered = false

    /**
     * Whether nothing kept on the device may show yet: the sign-in is under a limit, or was when
     * the home last said and has not answered in this launch (the limit may have tightened).
     */
    val keepsNothing: Boolean
        get() = _mine.value.limited || (!answered && kept?.read()?.limited == true)

    private val _time = MutableStateFlow<MyTime?>(null)
    val time: StateFlow<MyTime?> = _time.asStateFlow()

    /** Reads the limit again (at sign-in, and when the app comes back to the front). */
    suspend fun refresh() {
        val next = try {
            client.myParental()
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            // no answer: what is known stays
            null
        }
        if (next != null) {
            val now = KeptLimit.Mark(next.signature(), next.limited)
            // against this launch's last answer, or the one before the app was closed
            val before = if (answered) KeptLimit.Mark(_mine.value.signature(), _mine.value.limited) else kept?.read()
            answered = true
            _mine.value = next
            if (before != now) {
                kept?.write(now)
                // never asked on this device and no limit now: nothing was kept under one
                if (before != null || next.limited) {
                    onChange?.invoke()
                    _epoch.value++
                }
            }
        }
        if (!_mine.value.restricted) _time.value = null
    }

    /** Asks the home for the time now; the last answer stays when it does not say. */
    suspend fun askTime(libraryId: String? = null): MyTime? {
        if (!_mine.value.restricted) return null
        try {
            _time.value = client.myTime(libraryId)
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
        }
        return _time.value
    }

    /**
     * Follows the time while a screen shows it: asks now, then every [TimeView.POLL_MS], sooner
     * near the end. Returns at once for an account that is not restricted (only they have time
     * rules); cancel it when the screen goes.
     */
    suspend fun followTime(libraryId: String? = null, wait: suspend (Long) -> Unit = { delay(it) }) {
        if (!_mine.value.restricted) return
        while (true) {
            val t = askTime(libraryId)
            wait(TimeView.pollMs(t))
        }
    }
}

/** Where asking a parent stands on a screen. */
sealed interface AskState {
    data object Idle : AskState
    data object Sending : AskState

    /** The request waits for a parent. */
    data class Waiting(val request: ParentalRequest) : AskState

    /** The home knows nothing by that id for this account: nothing more is offered. */
    data object NothingToAsk : AskState
    data object TooMany : AskState

    /** The request did not go out (the network, the home): the person may try again. */
    data object Failed : AskState

    /** A parent approved the request this screen was waiting on: the screen opens what was asked for, once. */
    data class Approved(val request: ParentalRequest) : AskState

    /** A parent said no: the screen says so and offers Ask again and the PIN. */
    data class Denied(val request: ParentalRequest) : AskState
}

/** Asks for [subjectId] of [subjectKind] and says where that stands; it never throws but for a sign-out. */
suspend fun ApiClient.ask(subjectKind: String, subjectId: String): AskState = try {
    when (val a = askParent(subjectKind, subjectId)) {
        is AskAnswer.Waiting -> AskState.Waiting(a.request)
        AskAnswer.NothingToAsk, AskAnswer.NotRestricted -> AskState.NothingToAsk
        AskAnswer.TooMany -> AskState.TooMany
    }
} catch (e: CancellationException) {
    throw e
} catch (e: ApiException.SignedOut) {
    throw e
} catch (_: Exception) {
    AskState.Failed
}

/** Where typing a parent PIN stands on a screen. */
sealed interface PinState {
    /** Choosing whose PIN, or typing it. */
    data object Entering : PinState
    data object Checking : PinState
    data object Wrong : PinState
    data class Locked(val retryAfterSec: Long?) : PinState
    data object Unlocked : PinState

    /** The home knows nothing by that id for this account: nothing more is offered. */
    data object NothingToUnlock : PinState
    data object Failed : PinState
}

/**
 * The parent PIN flow of one screen: who can type one ([holders]; empty hides Enter parent PIN),
 * and the home's answer to what was typed. The PIN is sent and forgotten: nothing on the device
 * can check one. A sign-in the home no longer takes is told to [signedOut] (the app's own
 * sign-out) and never thrown: the screens call [submit] from a scope with nobody to catch it.
 */
class PinUnlock(
    private val client: ApiClient,
    val subjectKind: String,
    val subjectId: String,
    private val signedOut: () -> Unit = {},
) {
    private val _holders = MutableStateFlow<List<PinHolder>>(emptyList())
    val holders: StateFlow<List<PinHolder>> = _holders.asStateFlow()

    private val _state = MutableStateFlow<PinState>(PinState.Entering)
    val state: StateFlow<PinState> = _state.asStateFlow()

    suspend fun load() {
        _holders.value = try {
            client.parentPinHolders()
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            emptyList()
        }
    }

    /** Sends [pin] as [guardianId]'s; for time, [minutes] replaces the subject's id. */
    suspend fun submit(guardianId: String, pin: String, minutes: Int? = null) {
        _state.value = PinState.Checking
        val id = if (subjectKind == SubjectKind.TIME) (minutes ?: subjectId.toIntOrNull() ?: 30).toString() else subjectId
        _state.value = try {
            when (val a = client.unlockWithPin(guardianId, pin, subjectKind, id)) {
                UnlockAnswer.Unlocked -> PinState.Unlocked
                UnlockAnswer.Wrong -> PinState.Wrong
                is UnlockAnswer.Locked -> PinState.Locked(a.retryAfterSec)
                UnlockAnswer.NothingToUnlock, UnlockAnswer.NotRestricted -> PinState.NothingToUnlock
            }
        } catch (e: CancellationException) {
            throw e
        } catch (_: ApiException.SignedOut) {
            _state.value = PinState.Failed
            signedOut()
            return
        } catch (_: Exception) {
            PinState.Failed
        }
    }

    fun again() {
        _state.value = PinState.Entering
    }

    /**
     * Counts a lock down while the panel shows it: the seconds left go down in
     * [PinState.Locked] (from the home's `Retry-After`) and at zero the PIN can be typed again.
     * Counted by [wait]s of a second, not by the device's clock (a child cannot shorten it by
     * setting the clock; the home refuses a PIN typed too soon anyway). Returns at once when
     * the state is not a lock, and waits for nothing when the home named no time.
     */
    suspend fun waitOutLock(wait: suspend (Long) -> Unit = { delay(it) }) {
        var left = (_state.value as? PinState.Locked)?.retryAfterSec ?: return
        while (left > 0) {
            wait(1_000)
            // answered, closed or typed again meanwhile: not this lock's to count
            if (_state.value != PinState.Locked(left)) return
            left--
            if (left > 0) _state.value = PinState.Locked(left)
        }
        again()
    }
}

/**
 * Asking a parent from one screen: for an item ([subjectKind], [subjectId]) or, with
 * [SubjectKind.TIME], for minutes chosen at [ask]. One request at a time; the home keeps it.
 * A sign-in the home no longer takes is told to [signedOut], as [PinUnlock] does.
 */
class ParentAsk(
    private val client: ApiClient,
    val subjectKind: String,
    val subjectId: String = "",
    private val signedOut: () -> Unit = {},
) {
    companion object {
        /** How often a waiting request is read again. */
        const val FOLLOW_MS = 15_000L

        /** A request waiting this long with no change is no longer read by itself. */
        const val FOLLOW_FOR_MS = 30 * 60_000L
    }

    private val _state = MutableStateFlow<AskState>(AskState.Idle)
    val state: StateFlow<AskState> = _state.asStateFlow()

    /** The home has no route for a child's own requests (an older home): it is not asked again. */
    private var unsupported = false

    /**
     * Reads the caller's own requests (`GET /api/me/parental/requests`) and takes the newest one
     * for this screen: waiting, approved (only when this screen saw it waiting), denied (for
     * time, until the request's own end), or ended, which offers asking again. What the screen
     * remembered is only a first hint: the home's answer replaces it. A home that does not
     * answer changes nothing.
     */
    suspend fun read() {
        if (unsupported || _state.value == AskState.Sending) return
        val mine = try {
            client.myRequests()
        } catch (e: CancellationException) {
            throw e
        } catch (_: ApiException.SignedOut) {
            signedOut()
            return
        } catch (_: Exception) {
            return
        }
        if (mine == null) {
            unsupported = true
            return
        }
        val was = _state.value
        if (was == AskState.Sending) return
        val r = mine.firstOrNull { it.isFor(subjectKind, subjectId) }
        val waitedOn = (was as? AskState.Waiting)?.request?.id
        _state.value = when (r?.status) {
            ParentalRequest.PENDING -> AskState.Waiting(r)
            // approved before this screen asked (an earlier day's): nothing to act on
            ParentalRequest.APPROVED -> if (waitedOn == r.id) AskState.Approved(r) else if (was is AskState.Approved) was else AskState.Idle
            ParentalRequest.DENIED -> if (subjectKind == SubjectKind.TIME && ended(r)) AskState.Idle else AskState.Denied(r)
            // not in the list (not asked, or the list has not caught up with a request just
            // sent): what the screen knows stays, but for a denial that has left the list
            null -> if (was is AskState.Denied) AskState.Idle else was
            // ended, or a status this app does not know: asking is offered again
            else -> AskState.Idle
        }
    }

    /** Whether [r]'s own end has passed, by the home's clock. */
    private fun ended(r: ParentalRequest): Boolean =
        r.expiresAt?.let(::instantOrNull)?.let { it.toEpochMilli() <= client.homeNowMs() } == true

    /**
     * Follows this screen's request while it shows and the app is in front: reads now, then
     * every [FOLLOW_MS] while a request waits, and stops when none does or after
     * [FOLLOW_FOR_MS] with no change (Check again stays the person's own). Cancel it when the
     * screen goes or the app leaves the front.
     */
    suspend fun follow(wait: suspend (Long) -> Unit = { delay(it) }) {
        read()
        var unchanged = 0L
        while (!unsupported && _state.value is AskState.Waiting && unchanged < FOLLOW_FOR_MS) {
            wait(FOLLOW_MS)
            val before = _state.value
            read()
            unchanged = if (_state.value == before) unchanged + FOLLOW_MS else 0
        }
    }

    /** The approval was acted on (the page reloaded, the video resumed): asking starts over. */
    fun done() {
        if (_state.value is AskState.Approved) _state.value = AskState.Idle
    }

    suspend fun ask(minutes: Int? = null) {
        if (_state.value == AskState.Sending) return
        _state.value = AskState.Sending
        val id = if (subjectKind == SubjectKind.TIME) (minutes ?: 30).toString() else subjectId
        _state.value = try {
            client.ask(subjectKind, id)
        } catch (e: CancellationException) {
            _state.value = AskState.Idle
            throw e
        } catch (_: ApiException.SignedOut) {
            _state.value = AskState.Failed
            signedOut()
            return
        }
    }
}

/** What tells one limit from another: any change of it means the home shows the account other things. */
private fun MyParental.signature(): String = "$restricted|${maxTier ?: ""}|$allowOnly|${deviceLimit ?: ""}|$version"

/**
 * The sign-in's last limit as this device remembers it between launches ([ParentalState]): what
 * it was (to see a change made while the app was closed) and whether it was a limit at all.
 */
interface KeptLimit {
    data class Mark(val signature: String, val limited: Boolean)

    fun read(): Mark?
    fun write(mark: Mark)

    /** In a small file, one per home and person; a file that cannot be read is no mark. */
    class InFile(private val file: java.io.File) : KeptLimit {
        override fun read(): Mark? = runCatching {
            val lines = file.readLines()
            Mark(lines[1], lines[0] == "1")
        }.getOrNull()

        override fun write(mark: Mark) {
            runCatching {
                file.parentFile?.mkdirs()
                file.writeText((if (mark.limited) "1" else "0") + "\n" + mark.signature)
            }
        }

        companion object {
            const val FOLDER = "parental"

            /** The mark for [userId] on [home] under [dir]: the file is named by a hash, as the kept Home rows are. */
            fun of(dir: java.io.File, home: okhttp3.HttpUrl, userId: String): InFile {
                val digest = java.security.MessageDigest.getInstance("SHA-256").digest("$home|$userId".toByteArray())
                val name = digest.take(16).joinToString("") { Integer.toHexString((it.toInt() and 0xff) or 0x100).substring(1) }
                return InFile(java.io.File(java.io.File(dir, FOLDER), "$name.limit"))
            }
        }
    }
}
