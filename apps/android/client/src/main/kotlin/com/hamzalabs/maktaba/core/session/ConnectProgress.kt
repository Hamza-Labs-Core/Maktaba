package com.hamzalabs.maktaba.core.session

/**
 * What connecting to a home is doing right now (story 48.37): every wait on a connect, sign-in or
 * reconnect screen says this instead of a bare spinner. [AppSession.progress] publishes it as
 * the connect code goes; null when nothing is under way.
 *
 * [attempt] counts tries of the same thing: 1 the first time, 2 after Retry (or after the
 * home said it was busy and the app tried again), and so on.
 */
sealed interface ConnectProgress {
    val attempt: Int

    /** Looking for the home: before the first address is tried. */
    data class Finding(override val attempt: Int = 1) : ConnectProgress

    /**
     * None of the home's addresses answered: it is looked for among the homes announced on
     * this network (mDNS), to be asked where it is announced.
     */
    data class LookingNearby(override val attempt: Int = 1) : ConnectProgress

    /** Trying [address] (without its scheme), the [index]th (from 1) of [count]. */
    data class Trying(val address: String, val index: Int, val count: Int, override val attempt: Int = 1) : ConnectProgress

    /** A Maktaba home answered: [name]. */
    data class Found(val name: String, override val attempt: Int = 1) : ConnectProgress

    /** Signing in to [name] ([stage]: with a password, a code or Maktaba Cloud). */
    data class SigningIn(val name: String, val stage: ConnectStage, override val attempt: Int = 1) : ConnectProgress

    /**
     * Sign-in by code: [code] waits for a person signed in elsewhere to approve it, until
     * [expiresAt] (epoch millis on this device's clock).
     */
    data class AwaitingApproval(val code: String, val expiresAt: Long, override val attempt: Int = 1) : ConnectProgress

    /** On launch: signing in again to [name] with the kept sign-in. */
    data class Reconnecting(val name: String, override val attempt: Int = 1) : ConnectProgress

    /** [name] said it is busy or starting (429/503, Retry-After): the next try is at [retryAt] (epoch millis). */
    data class HomeBusy(val name: String, val retryAt: Long, override val attempt: Int = 1) : ConnectProgress

    /** It did not work: [reason] in plain words, and the addresses [tried] (without their scheme). */
    data class Failed(
        val reason: FailReason,
        val tried: List<String>,
        val stage: ConnectStage,
        override val attempt: Int = 1,
    ) : ConnectProgress
}

/** Which flow a step belongs to: what Retry does, and which screen shows it. */
enum class ConnectStage {
    /** Adding a home: a typed address, a QR code, a link. */
    Connect,

    /** On launch: the kept sign-in checked again. */
    Restore,

    /** Signing in with a username and password. */
    Password,

    /** Signing in with a code another device approves. */
    Code,

    /** Opening a home through Maktaba Cloud. */
    Cloud,
}

/** Why connecting failed, as a person reads it. */
enum class FailReason {
    /** No address answered (the network, the home off, a local address seen from elsewhere). */
    NoAnswer,

    /** Something answered, but not a Maktaba home. */
    NotAHome,

    /** A Maktaba home answered, but not the one asked for. */
    OtherHome,

    /** The home refused the username and password. */
    WrongPassword,

    /** The sign-in code ran out before anyone approved it. */
    ApprovalExpired,

    /** The home stayed busy or starting through every try. */
    Busy,

    /** Anything else (the home failed, an answer this app cannot read). */
    Other,
}

/** An address as a person reads it: without its scheme. */
internal fun shown(url: String): String = url.substringAfter("://")
