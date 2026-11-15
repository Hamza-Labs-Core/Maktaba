package com.hamzalabs.maktaba.core.guest

import com.hamzalabs.maktaba.core.parental.KeptLimit
import com.hamzalabs.maktaba.core.parental.ParentalState
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flowOf

/**
 * Whether Watch a broadcast may open on this device (story 50.27). A broadcast is outside any
 * home's ratings and the edge never learns who watches, so the app keeps a limited sign-in out of
 * it. The rule fails closed: anything not known to be free of a limit keeps the guest space shut.
 */
enum class GuestAccess {
    /** Known: no parental limit on this sign-in or this device. */
    Allowed,

    /** A parental limit is on (the sign-in's, or one kept on this device). */
    Limited,

    /** The signed-in home has not said yet: shut until it does. */
    Unknown,
}

/**
 * The rule behind [GuestAccess], from the device's kept limit marks (each one read, null for a
 * mark that cannot be read) and the signed-in account's limit (null while nobody is signed in):
 * - any kept mark limited, or one that cannot be read: [GuestAccess.Limited];
 * - signed in and the home has not answered: [GuestAccess.Unknown];
 * - signed in and limited: [GuestAccess.Limited];
 * - otherwise (no sign-in: the last limit kept for each one on this device): [GuestAccess.Allowed].
 */
fun guestAccess(marks: List<KeptLimit.Mark?>, known: Boolean?, limited: Boolean): GuestAccess = when {
    marks.any { it == null || it.limited } -> GuestAccess.Limited
    known == null -> GuestAccess.Allowed
    !known -> GuestAccess.Unknown
    limited -> GuestAccess.Limited
    else -> GuestAccess.Allowed
}

/**
 * The app's one gate for Watch a broadcast: it follows the signed-in account's [ParentalState]
 * ([follow], null when nobody is signed in) and reads the device's kept marks ([marks]) each time
 * it is asked, so the entry, an invite link and an open guest space all see the same answer.
 */
class GuestGate(private val marks: () -> List<KeptLimit.Mark?>) {
    private val _following = MutableStateFlow<ParentalState?>(null)

    /** The sign-in whose limit counts; null while nobody is signed in. */
    val following: ParentalState? get() = _following.value

    /** Follows [parental], the signed-in account's limit (null: signed out, the kept marks decide). */
    fun follow(parental: ParentalState?) {
        _following.value = parental
    }

    /** The answer now. */
    fun now(): GuestAccess = of(_following.value)

    /** The answer, again each time the sign-in or its limit changes. */
    @OptIn(ExperimentalCoroutinesApi::class)
    val access: Flow<GuestAccess> = _following.flatMapLatest { p ->
        // each answer once wholly taken (its mark written), and the limit itself
        if (p == null) flowOf(of(null)) else combine(p.answers, p.mine) { _, _ -> of(p) }
    }.distinctUntilChanged()

    private fun of(p: ParentalState?): GuestAccess =
        guestAccess(marks(), p?.known, p?.mine?.value?.limited == true)
}

/**
 * What the guest space shows, kept apart from any screen: opened from the entry or by an
 * invite's link, held while the signed-in home has not said its limit, refused under a limit, and
 * closed the moment a limit turns on ([update]).
 */
class GuestDoor {
    sealed interface Shown {
        /** Nothing: the app as it is. */
        data object Shut : Shown

        /** The guest space, with the invite a link brought (null: none). */
        data class Open(val invite: String?) : Shown

        /** An invite's link waits until the home says this sign-in is free of a limit. */
        data class Held(val invite: String) : Shown

        /** A parental limit is on: the guest space does not open (or just closed); a message says why. */
        data object Refused : Shown
    }

    private val _shown = MutableStateFlow<Shown>(Shown.Shut)
    val shown: StateFlow<Shown> = _shown.asStateFlow()

    /** The entry (no [invite]) or an invite's link asks to open, under [access]. */
    fun request(invite: String?, access: GuestAccess) {
        _shown.value = when (access) {
            GuestAccess.Allowed -> Shown.Open(invite)
            GuestAccess.Limited -> Shown.Refused
            // the entry is hidden while unknown; a link waits for the answer
            GuestAccess.Unknown -> if (invite != null) Shown.Held(invite) else _shown.value
        }
    }

    /** The answer changed: a held link opens once allowed; whatever is open or held is refused under a limit. */
    fun update(access: GuestAccess) {
        val s = _shown.value
        _shown.value = when (access) {
            GuestAccess.Allowed -> if (s is Shown.Held) Shown.Open(s.invite) else s
            GuestAccess.Limited -> if (s is Shown.Open || s is Shown.Held) Shown.Refused else s
            // a sign-in whose limit is not known yet: an open guest space shuts (its invite was
            // already redeemed: opening it again would spend another of the invite's devices), a
            // link stays held
            GuestAccess.Unknown -> if (s is Shown.Open) Shown.Shut else s
        }
    }

    /** Close, Cancel or OK. */
    fun close() {
        _shown.value = Shown.Shut
    }
}
