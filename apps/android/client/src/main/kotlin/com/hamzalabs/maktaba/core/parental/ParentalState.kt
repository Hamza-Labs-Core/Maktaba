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
import com.hamzalabs.maktaba.core.api.myParental
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
 */
class ParentalState(private val client: ApiClient) {
    private val _mine = MutableStateFlow(MyParental.None)
    val mine: StateFlow<MyParental> = _mine.asStateFlow()

    private val _time = MutableStateFlow<MyTime?>(null)
    val time: StateFlow<MyTime?> = _time.asStateFlow()

    /** Reads the limit again (at sign-in, and when the app comes back to the front). */
    suspend fun refresh() {
        try {
            _mine.value = client.myParental()
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            // no answer: what is known stays
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
 * can check one.
 */
class PinUnlock(private val client: ApiClient, val subjectKind: String, val subjectId: String) {
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
        } catch (e: ApiException.SignedOut) {
            throw e
        } catch (_: Exception) {
            PinState.Failed
        }
    }

    fun again() {
        _state.value = PinState.Entering
    }
}

/**
 * Asking a parent from one screen: for an item ([subjectKind], [subjectId]) or, with
 * [SubjectKind.TIME], for minutes chosen at [ask]. One request at a time; the home keeps it.
 */
class ParentAsk(private val client: ApiClient, val subjectKind: String, val subjectId: String = "") {
    private val _state = MutableStateFlow<AskState>(AskState.Idle)
    val state: StateFlow<AskState> = _state.asStateFlow()

    suspend fun ask(minutes: Int? = null) {
        if (_state.value == AskState.Sending) return
        _state.value = AskState.Sending
        val id = if (subjectKind == SubjectKind.TIME) (minutes ?: 30).toString() else subjectId
        _state.value = try {
            client.ask(subjectKind, id)
        } catch (e: CancellationException) {
            _state.value = AskState.Idle
            throw e
        } catch (e: ApiException.SignedOut) {
            AskState.Failed
        }
    }
}
