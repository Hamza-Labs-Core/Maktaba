package com.hamzalabs.maktaba.core.session

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.DeviceCode
import com.hamzalabs.maktaba.core.api.DeviceSignIn
import com.hamzalabs.maktaba.core.api.ErrorKind
import com.hamzalabs.maktaba.core.api.errorKind
import com.hamzalabs.maktaba.core.api.Me
import com.hamzalabs.maktaba.core.api.User
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/**
 * A home this app knows: its address in use (under the web's base path when it has one), a
 * name to show (its address until it says its name), its instance id when it said it, and the
 * name the person gave it here ([nickname], shown before the home's own).
 *
 * [addresses] are its other addresses (story 42.7: the local one and the relay's through
 * Maktaba Cloud, from `/api/discovery/home`, a link or the cloud), tried when the one in use
 * does not answer. Tokens stay with the address that issued them: another address signs in
 * afresh (through the cloud without a password, when [cloudServer] and a cloud sign-in allow).
 */
@Serializable
data class Home(
    val url: String,
    val name: String,
    val id: String? = null,
    val nickname: String? = null,
    val addresses: List<String> = emptyList(),
    /** The home's id in Maktaba Cloud, when the person chose it there. */
    val cloudServer: String? = null,
    /**
     * The addresses Maktaba Cloud gave for it (its relay, its own): the only ones a cloud
     * sign-in is sent to. An address the home announced about itself may be another machine
     * on another network, and a cloud assertion caught there would work at the real home
     * for its minute.
     */
    val cloudAddresses: List<String> = emptyList(),
) {
    val httpUrl: HttpUrl get() = url.toHttpUrl()

    /** What the app shows: the person's name for it, else the home's. */
    val label: String get() = nickname?.takeIf { it.isNotBlank() } ?: name

    /** The address without its scheme, as a row shows it under the name. */
    val address: String get() = url.substringAfter("://")

    companion object {
        /**
         * The home's address from what a person types: a scheme added when missing (http on a
         * local address, else https), no path, no query, no trailing slash.
         */
        fun normalize(text: String): String? {
            var s = text.trim()
            if (s.isEmpty()) return null
            if (!s.contains("://")) {
                val host = s.substringBefore('/')
                s = (if (isLocal(host)) "http://" else "https://") + s
            }
            val u = s.toHttpUrlOrNull() ?: return null
            if (u.host.isEmpty()) return null
            return u.newBuilder().encodedPath("/").query(null).fragment(null).build().toString().trimEnd('/')
        }

        /**
         * As [normalize], keeping the path: a home served under a base path
         * (`https://example.com/maktaba`), as a home's link or `path=` gives it.
         */
        fun normalizeWithPath(text: String): String? {
            val root = normalize(text) ?: return null
            var s = text.trim()
            if (!s.contains("://")) s = root.substringBefore("://") + "://" + s
            val u = s.toHttpUrlOrNull() ?: return root
            return u.newBuilder().query(null).fragment(null).build().toString().trimEnd('/')
        }

        fun isLocal(hostPort: String): Boolean {
            val host = hostPort.substringBefore(':')
            return host == "localhost" || host.endsWith(".local") || host.startsWith("192.168.") ||
                host.startsWith("10.") || host.startsWith("127.") ||
                Regex("""^172\.(1[6-9]|2\d|3[01])\.""").containsMatchIn(host)
        }

        private fun String.toHttpUrl(): HttpUrl = toHttpUrlOrNull() ?: error("not a home address: $this")
    }
}

/**
 * The app's state the screens follow: the homes it knows, the one in use, who is signed in
 * there, and what connecting is doing right now ([progress]). One per app ([app] names it: its
 * tokens and homes are its own).
 *
 * [now] and [sleep] are the clock and the wait between tries (a busy home's Retry-After, a
 * code's polling): replaced in tests. [progressState] is where [progress] is kept: a test or a
 * screenshot may hold a step there to show it.
 */
class AppSession(
    private val store: KeyValueStore,
    private val scope: CoroutineScope,
    private val tokensFor: (home: String) -> TokenStore,
    private val clientFor: (HttpUrl, TokenStore) -> ApiClient = { url, t -> ApiClient(url, t) },
    val now: () -> Long = System::currentTimeMillis,
    private val sleep: suspend (Long) -> Unit = { delay(it) },
    progressState: MutableStateFlow<ConnectProgress?> = MutableStateFlow(null),
) {
    sealed interface Phase {
        data object ChoosingHome : Phase
        data object SigningIn : Phase
        data class SignedIn(val user: User, val client: ApiClient) : Phase
    }

    private val _homes = MutableStateFlow(loadHomes())
    val homes: StateFlow<List<Home>> = _homes.asStateFlow()

    private val _current = MutableStateFlow<Home?>(null)
    val current: StateFlow<Home?> = _current.asStateFlow()

    private val _phase = MutableStateFlow<Phase>(Phase.ChoosingHome)
    val phase: StateFlow<Phase> = _phase.asStateFlow()

    private val _restoreProblem = MutableStateFlow<ErrorKind?>(null)
    /** Why the kept sign-in could not be checked (the home did not answer): try again, or sign in. */
    val restoreProblem: StateFlow<ErrorKind?> = _restoreProblem.asStateFlow()

    private val _progress = progressState

    /**
     * What connecting is doing now (story 48.37): finding the home, each address tried, found,
     * signing in, waiting for a code's approval, reconnecting on launch, the home busy, or why
     * it failed. Null when nothing is under way. Every connect, sign-in and restore screen
     * shows it.
     */
    val progress: StateFlow<ConnectProgress?> = _progress.asStateFlow()

    /** What is being tried, and how many times in a row (Retry counts up). */
    private var trying: Any? = null
    private var tries = 0

    /** The attempt number for [what]: one more when it is what was tried last, else 1. */
    private fun attemptFor(what: Any): Int {
        tries = if (what == trying) tries + 1 else 1
        trying = what
        return tries
    }

    /** It worked: the next try of anything starts at attempt 1. */
    private fun succeeded() {
        trying = null
        tries = 0
    }

    /** Clears [progress] when it still shows [mine] (a cancelled step leaves no spinner behind). */
    private fun clearIf(mine: ConnectProgress) {
        _progress.compareAndSet(mine, null)
    }

    /** What Retry does for the last failure of a connect or restore ([retry]). */
    private var again: (suspend () -> Unit)? = null

    /**
     * Tries the last failed connect or restore again (its attempt number one more). Its
     * failure is in [progress].
     */
    fun retry() {
        val a = again ?: return
        scope.launch {
            try {
                a()
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                // said in progress (Failed)
            }
        }
    }

    /** The client for the current home (null while choosing one). */
    var client: ApiClient? = null
        private set

    private val _link = MutableStateFlow<ConnectLink?>(null)
    /** A home's link the app was opened with (`maktaba://connect`), waiting for the person to confirm. */
    val link: StateFlow<ConnectLink?> = _link.asStateFlow()

    /** Shows [link] for the person to confirm (opening it never switches homes by itself). */
    fun offer(link: ConnectLink) {
        _link.value = link
    }

    /** The link an activity was opened with (`Intent.dataString`), when it is a `maktaba:` home link: offered. */
    fun openLink(data: String?): Boolean {
        if (data == null || !data.trim().startsWith(ConnectLink.SCHEME + ":", ignoreCase = true)) return false
        val link = ConnectLink.parse(data) ?: return false
        offer(link)
        return true
    }

    fun dismissLink() {
        _link.value = null
    }

    /**
     * Connects to the home [link] names (a typed address, a scanned QR, a `maktaba://` link):
     * tries its addresses in order, keeps the first where a Maktaba home answers (the home
     * the link names, when it names one), and uses it. [NotAHome] when none answers.
     */
    suspend fun connect(link: ConnectLink, probe: HomeProbe = HomeProbe()): Home {
        val attempt = attemptFor(Tried(ConnectStage.Connect, link.urls + listOfNotNull(link.id)))
        again = { connect(link, probe) }
        val r = find(link.urls, link.id, probe, ConnectStage.Connect, attempt)
        val name = r.info.name?.trim()?.takeIf { it.isNotEmpty() } ?: link.name ?: r.url.substringAfter("://")
        // its other addresses as the home that answered says them (a link's that did not answer
        // are unchecked: never kept)
        val others = r.info.urls.mapNotNull { Home.normalizeWithPath(it) }
        _progress.value = ConnectProgress.Found(name, attempt)
        succeeded()
        use(Home(r.url, name, r.info.id ?: link.id, addresses = others))
        if (_link.value == link) _link.value = null
        return _current.value!!
    }

    /**
     * Finds the home at [urls] (the first that answers as it), saying each step in [progress]:
     * finding, each address tried, and why none did ([NotAHome], rethrown).
     */
    private suspend fun find(urls: List<String>, expectId: String?, probe: HomeProbe, stage: ConnectStage, attempt: Int): Reached {
        _progress.value = ConnectProgress.Finding(attempt)
        try {
            return probe.first(urls, expectId) { u, i, n -> _progress.value = ConnectProgress.Trying(shown(u), i, n, attempt) }
        } catch (e: NotAHome) {
            _progress.value = ConnectProgress.Failed(e.reason, urls.map(::shown), stage, attempt)
            throw e
        } catch (e: CancellationException) {
            if (_progress.value.let { it is ConnectProgress.Finding || it is ConnectProgress.Trying }) _progress.value = null
            throw e
        }
    }

    init {
        store.get(KEY_HOME)?.let { last -> _homes.value.firstOrNull { it.url == last }?.let(::use) }
    }

    /**
     * Picks a home: its client, and signed in at once when its tokens are still kept. Homes
     * are known by their exact address: a home's id (mDNS, `/api/discovery/home`, a link) is
     * what it says of itself, unauthenticated, so the same id at another address is another
     * entry and signs in afresh; tokens never move to another address.
     */
    fun use(home: Home) {
        val known = _homes.value
        // the same entry: at this address, or one of its other addresses (then it moves there)
        val previous = known.firstOrNull { it.url == home.url } ?: known.firstOrNull { home.url in it.addresses }
        val chosen = (previous?.let {
            it.copy(
                url = home.url,
                id = it.id ?: home.id,
                addresses = it.addresses + it.url + home.addresses,
                cloudServer = home.cloudServer ?: it.cloudServer,
                cloudAddresses = (it.cloudAddresses + home.cloudAddresses).distinct(),
            )
        } ?: home).let { h -> h.copy(addresses = h.addresses.filter { it != h.url }.distinct()) }
        _homes.value = if (previous == null) known + chosen else known.map { if (it === previous) chosen else it }
        if (previous != chosen) saveHomes()
        _current.value = chosen
        store.put(KEY_HOME, chosen.url)
        val tokens = tokensFor(chosen.url)
        val c = clientFor(chosen.httpUrl, tokens)
        client = c
        _phase.value = Phase.SigningIn
        _restoreProblem.value = null
        // the home just found stays said ("Found …") over the sign-in; anything else is over
        _progress.value.let { if (it != null && it !is ConnectProgress.Found) _progress.value = null }
        if (tokens.access != null) restore(c)
    }

    /** Signs in again with the kept tokens, if the home answers (its attempt number one more). */
    fun retryRestore() {
        client?.let(::restore)
    }

    private fun restore(c: ApiClient) {
        val home = _current.value ?: return
        val what = Tried(ConnectStage.Restore, listOf(home.url))
        val attempt = attemptFor(what)
        again = { restore(c) }
        _progress.value = ConnectProgress.Reconnecting(home.label, attempt)
        scope.launch { restoring(c, home, what, attempt) }
    }

    /**
     * Checks the kept sign-in at [home]: "Reconnecting to …"; a busy or starting home
     * (429/503) is tried again after its Retry-After, counted down in [progress]; an address
     * that does not answer tries the home's others; else why it failed.
     */
    private suspend fun restoring(c: ApiClient, home: Home, what: Tried, first: Int) {
        var attempt = first
        var waits = 0
        while (true) {
            if (c !== client) return
            _progress.value = ConnectProgress.Reconnecting(home.label, attempt)
            try {
                signedIn(c, c.whoami())
                _restoreProblem.value = null
                succeeded()
                _progress.value = null
                return
            } catch (e: ApiException.SignedOut) {
                // the kept sign-in ended: the sign-in form, nothing failed
                _restoreProblem.value = null
                succeeded()
                _progress.value = null
                return
            } catch (e: CancellationException) {
                clearIf(ConnectProgress.Reconnecting(home.label, attempt))
                throw e
            } catch (e: ApiException.Status) {
                if ((e.code == 429 || e.code == 503) && waits < BUSY_TRIES && c === client) {
                    waits++
                    val wait = (e.retryAfterSec ?: BUSY_WAIT_SEC).coerceIn(1, MAX_WAIT_SEC) * 1000
                    _progress.value = ConnectProgress.HomeBusy(home.label, now() + wait, attempt)
                    sleep(wait)
                    if (trying != what) return // something else started meanwhile
                    attempt = attemptFor(what)
                    continue
                }
                if (c === client) failRestore(home, e, attempt)
                return
            } catch (e: ApiException.Transport) {
                // this address does not answer (the local one, away from home): another might
                if (c === client && !reachElsewhere(attempt)) {
                    _restoreProblem.value = e.errorKind()
                    _progress.value = ConnectProgress.Failed(
                        FailReason.NoAnswer, (listOf(home.url) + home.addresses).map(::shown), ConnectStage.Restore, attempt,
                    )
                }
                return
            } catch (e: Exception) {
                if (c === client) failRestore(home, e, attempt)
                return
            }
        }
    }

    private fun failRestore(home: Home, e: Exception, attempt: Int) {
        _restoreProblem.value = e.errorKind()
        _progress.value = ConnectProgress.Failed(reasonFor(e), listOf(shown(home.url)), ConnectStage.Restore, attempt)
    }

    /** Checks an address before the app moves to it (replaceable in tests). */
    internal var probe: HomeProbe = HomeProbe()

    /**
     * The home in use did not answer at its address: tries its other addresses, and moves to
     * the first where the same home answers. There the app uses that address's own tokens, or
     * signs in through Maktaba Cloud (no password) when it can, or asks the person to sign in.
     * False when no other address answers.
     */
    suspend fun reachElsewhere(): Boolean = reachElsewhere(_progress.value?.attempt ?: 1)

    private suspend fun reachElsewhere(attempt: Int): Boolean {
        val home = _current.value ?: return false
        for ((i, other) in home.addresses.withIndex()) {
            if (_current.value?.url == home.url) {
                _progress.value = ConnectProgress.Trying(shown(other), i + 1, home.addresses.size, attempt)
            }
            val info = try {
                probe.probe(other)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                continue
            }
            // the same home only: an address this home gave may belong to another machine on
            // another network (a local address away from home), and an unknown id proves nothing
            if (home.id == null || info.id == null || !sameHome(home.id, info.id)) continue
            if (_current.value?.url != home.url) return true
            use(home.copy(url = other))
            val cloud = this.cloud
            if (tokensFor(other).access == null && cloud?.signedIn == true && other in home.cloudAddresses) {
                runCatching { signInWithCloud() }
            }
            return true
        }
        return false
    }

    /** Signed in to Maktaba Cloud (in memory only: the app asks again after it restarts). */
    var cloud: CloudSession? = null

    /** The cloud's API address: the hosted one, or one the person chose. */
    var cloudApi: String
        get() = store.get(CloudSession.KEY_API) ?: CloudSession.DEFAULT_API
        set(value) = store.put(CloudSession.KEY_API, value.takeIf { it != CloudSession.DEFAULT_API })

    /**
     * Uses a home chosen in Maktaba Cloud: its own address when it answers (on its network),
     * else the relay's, both kept; then signs in there through the cloud.
     */
    suspend fun useCloudHome(home: CloudHome, probe: HomeProbe = this.probe) {
        val attempt = attemptFor(Tried(ConnectStage.Cloud, listOf(home.serverId)))
        again = { useCloudHome(home, probe) }
        val r = find(home.addresses, null, probe, ConnectStage.Cloud, attempt)
        val name = home.name.ifBlank { r.info.name ?: r.url.substringAfter("://") }
        use(
            Home(
                r.url, name, r.info.id,
                addresses = home.addresses + r.info.urls.mapNotNull { Home.normalizeWithPath(it) },
                cloudServer = home.serverId,
                cloudAddresses = home.addresses,
            ),
        )
        _progress.value = ConnectProgress.Found(name, attempt)
        succeeded()
        if (phase.value !is Phase.SignedIn) {
            val step = ConnectProgress.SigningIn(_current.value?.label ?: name, ConnectStage.Cloud, attempt)
            _progress.value = step
            try {
                signInWithCloud()
                clearIf(step)
            } catch (e: CancellationException) {
                clearIf(step)
                throw e
            } catch (e: Exception) {
                _progress.value = ConnectProgress.Failed(reasonFor(e), listOf(shown(r.url)), ConnectStage.Cloud, attempt)
                throw e
            }
        }
    }

    /**
     * Signs in to the home in use through Maktaba Cloud: a one-use assertion for it, which the
     * home turns into its own tokens for this address. The home decides who the person is
     * there and what they may do.
     */
    suspend fun signInWithCloud() {
        val c = client ?: return
        val cloud = this.cloud ?: throw CloudException(401, "signed_out", "Sign in to Maktaba Cloud.")
        val home = _current.value
        val server = home?.cloudServer ?: throw CloudException(0, "not_in_cloud", "This home was not chosen in Maktaba Cloud.")
        // only to an address the cloud gave for this home (Home.cloudAddresses)
        if (home.url !in home.cloudAddresses) throw CloudException(0, "not_a_cloud_address", "Sign in at this address with your password or a code.")
        val assertion = cloud.assertion(server)
        if (c !== client) return
        c.signInWithAssertion(assertion)
        signedIn(c, c.whoami())
    }

    /** Names a known home for this app ([nickname] blank: back to the home's own name). */
    fun rename(home: Home, nickname: String) {
        val n = nickname.trim().takeIf { it.isNotEmpty() }
        val renamed = { h: Home -> if (h.url == home.url) h.copy(nickname = n) else h }
        _homes.value = _homes.value.map(renamed)
        _current.value = _current.value?.let(renamed)
        saveHomes()
    }

    fun forget(home: Home) {
        tokensFor(home.url).clear()
        _homes.value = _homes.value.filterNot { it.url == home.url }
        saveHomes()
        if (_current.value?.url == home.url) switchHome()
    }

    fun switchHome() {
        _current.value = null
        client = null
        _progress.value = null
        succeeded()
        again = null
        _phase.value = Phase.ChoosingHome
        store.put(KEY_HOME, null)
    }

    /**
     * Signs in with a username and password: "Signing in to …" in [progress], then nothing
     * (signed in) or why it failed (a wrong password, no answer, …), and the failure rethrown.
     */
    suspend fun signIn(username: String, password: String) {
        val c = client ?: return
        val home = _current.value ?: return
        val attempt = attemptFor(Tried(ConnectStage.Password, listOf(home.url)))
        val step = ConnectProgress.SigningIn(home.label, ConnectStage.Password, attempt)
        _progress.value = step
        try {
            c.signIn(username, password)
            signedIn(c, c.whoami())
            succeeded()
            clearIf(step)
        } catch (e: CancellationException) {
            clearIf(step)
            throw e
        } catch (e: Exception) {
            if (c === client) {
                _progress.value = ConnectProgress.Failed(reasonFor(e, password = true), listOf(shown(home.url)), ConnectStage.Password, attempt)
            }
            throw e
        }
    }

    /**
     * Signs in with a code (story 42.6): shows each code the home gives ([onCode]) until a
     * person signed in elsewhere approves it, then keeps this device's tokens. [progress] says
     * "Signing in", then the code waiting for approval and when it runs out.
     * [DeviceSignIn.Unsupported] when the home has no sign-in by code.
     *
     * A code that runs out is replaced by a new one when [renew]; else (story 48.37) the wait
     * ends: [progress] says the approval expired and [DeviceSignIn.Expired] is thrown (Retry
     * asks for a new code, attempt one more).
     */
    suspend fun signInWithCode(
        label: String,
        onCode: (DeviceCode) -> Unit,
        sleep: suspend (Long) -> Unit = this.sleep,
        renew: Boolean = true,
    ) {
        val c = client ?: return
        val home = _current.value ?: return
        val attempt = attemptFor(Tried(ConnectStage.Code, listOf(home.url)))
        val device = DeviceSignIn({ path, body -> c.publicPost(path, body) }, label, now, sleep)
        _progress.value = ConnectProgress.SigningIn(home.label, ConnectStage.Code, attempt)
        try {
            while (true) {
                val code = device.start()
                if (c !== client) return
                onCode(code)
                _progress.value = ConnectProgress.AwaitingApproval(code.code, device.deadline(code), attempt)
                val tokens = device.poll(code)
                if (c !== client) return
                if (tokens != null) {
                    c.keep(tokens)
                    signedIn(c, c.whoami())
                    succeeded()
                    _progress.value = null
                    return
                }
                if (!renew) {
                    _progress.value = ConnectProgress.Failed(FailReason.ApprovalExpired, listOf(shown(home.url)), ConnectStage.Code, attempt)
                    throw DeviceSignIn.Expired()
                }
            }
        } catch (e: CancellationException) {
            val p = _progress.value
            if ((p is ConnectProgress.SigningIn && p.stage == ConnectStage.Code) || p is ConnectProgress.AwaitingApproval) _progress.value = null
            throw e
        } catch (e: DeviceSignIn.Expired) {
            throw e
        } catch (e: DeviceSignIn.Unsupported) {
            // the screen offers the password instead: nothing failed
            if (c === client) _progress.value = null
            throw e
        } catch (e: Exception) {
            if (c === client) _progress.value = ConnectProgress.Failed(reasonFor(e), listOf(shown(home.url)), ConnectStage.Code, attempt)
            throw e
        }
    }

    fun signOut() {
        _current.value?.let { tokensFor(it.url).clear() }
        _progress.value = null
        _phase.value = Phase.SigningIn
    }

    /** A screen saw `ApiException.SignedOut`: back to signing in. */
    fun signedOut() {
        _progress.value = null
        _phase.value = Phase.SigningIn
    }

    private fun signedIn(c: ApiClient, me: Me) {
        if (c !== client) return
        named(me.homeName)
        _phase.value = Phase.SignedIn(me.user, c)
    }

    /** The current home's name as the home says it (null: it did not say, so the name stays). */
    internal fun named(name: String?) {
        val home = _current.value ?: return
        if (name == null || home.name == name) return
        val renamed = home.copy(name = name)
        _current.value = renamed
        _homes.value = _homes.value.map { if (it.url == home.url) renamed else it }
        saveHomes()
    }

    private fun loadHomes(): List<Home> = store.get(KEY_HOMES)
        ?.let { runCatching { ApiClient.json.decodeFromString(ListSerializer(Home.serializer()), it) }.getOrNull() }
        ?: emptyList()

    private fun saveHomes() {
        store.put(KEY_HOMES, ApiClient.json.encodeToString(ListSerializer(Home.serializer()), _homes.value))
    }

    /** What a try is of: its flow and what it is tried at (an attempt counts up while it stays the same). */
    private data class Tried(val stage: ConnectStage, val at: List<String>)

    private companion object {
        const val KEY_HOMES = "homes"
        const val KEY_HOME = "home"

        /** How many times a busy or starting home is waited for on launch before it counts as failed. */
        const val BUSY_TRIES = 5

        /** The wait when a busy home does not say (no Retry-After), and the longest one kept. */
        const val BUSY_WAIT_SEC = 5L
        const val MAX_WAIT_SEC = 120L

        /** Why [e] failed, as a person reads it ([password]: a 400-403 is the username and password). */
        fun reasonFor(e: Throwable, password: Boolean = false): FailReason {
            if (e is NotAHome) return e.reason
            if (password && e is ApiException.Status && e.code in 400..403) return FailReason.WrongPassword
            return when (e.errorKind()) {
                ErrorKind.Unreachable -> FailReason.NoAnswer
                ErrorKind.Busy -> FailReason.Busy
                else -> FailReason.Other
            }
        }
    }
}
