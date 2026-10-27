package com.hamzalabs.maktaba.core.session

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.DeviceCode
import com.hamzalabs.maktaba.core.api.DeviceSignIn
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
 * there. One per app ([app] names it: its tokens and homes are its own).
 */
class AppSession(
    private val store: KeyValueStore,
    private val scope: CoroutineScope,
    private val tokensFor: (home: String) -> TokenStore,
    private val clientFor: (HttpUrl, TokenStore) -> ApiClient = { url, t -> ApiClient(url, t) },
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

    private val _restoreProblem = MutableStateFlow<String?>(null)
    /** Why the kept sign-in could not be checked (the home did not answer): try again, or sign in. */
    val restoreProblem: StateFlow<String?> = _restoreProblem.asStateFlow()

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
        val r = probe.first(link.urls, link.id)
        val name = r.info.name?.trim()?.takeIf { it.isNotEmpty() } ?: link.name ?: r.url.substringAfter("://")
        // its other addresses as the home that answered says them (a link's that did not answer
        // are unchecked: never kept)
        val others = r.info.urls.mapNotNull { Home.normalizeWithPath(it) }
        use(Home(r.url, name, r.info.id ?: link.id, addresses = others))
        if (_link.value == link) _link.value = null
        return _current.value!!
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
        if (tokens.access != null) restore(c)
    }

    /** Signs in again with the kept tokens, if the home answers. */
    fun retryRestore() {
        client?.let(::restore)
    }

    private fun restore(c: ApiClient) {
        scope.launch {
            try {
                signedIn(c, c.whoami())
                _restoreProblem.value = null
            } catch (e: ApiException.SignedOut) {
                _restoreProblem.value = null
            } catch (e: CancellationException) {
                throw e
            } catch (e: ApiException.Transport) {
                // this address does not answer (the local one, away from home): another might
                if (c === client && !reachElsewhere()) _restoreProblem.value = e.message ?: e.toString()
            } catch (e: Exception) {
                if (c === client) _restoreProblem.value = e.message ?: e.toString()
            }
        }
    }

    /** Checks an address before the app moves to it (replaceable in tests). */
    internal var probe: HomeProbe = HomeProbe()

    /**
     * The home in use did not answer at its address: tries its other addresses, and moves to
     * the first where the same home answers. There the app uses that address's own tokens, or
     * signs in through Maktaba Cloud (no password) when it can, or asks the person to sign in.
     * False when no other address answers.
     */
    suspend fun reachElsewhere(): Boolean {
        val home = _current.value ?: return false
        for (other in home.addresses) {
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
        val r = probe.first(home.addresses)
        val name = home.name.ifBlank { r.info.name ?: r.url.substringAfter("://") }
        use(
            Home(
                r.url, name, r.info.id,
                addresses = home.addresses + r.info.urls.mapNotNull { Home.normalizeWithPath(it) },
                cloudServer = home.serverId,
                cloudAddresses = home.addresses,
            ),
        )
        if (phase.value !is Phase.SignedIn) signInWithCloud()
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
        _phase.value = Phase.ChoosingHome
        store.put(KEY_HOME, null)
    }

    suspend fun signIn(username: String, password: String) {
        val c = client ?: return
        c.signIn(username, password)
        signedIn(c, c.whoami())
    }

    /**
     * Signs in with a code (story 42.6): shows each code the home gives ([onCode]) until a
     * person signed in elsewhere approves it, then keeps this device's tokens.
     * [DeviceSignIn.Unsupported] when the home has no sign-in by code.
     */
    suspend fun signInWithCode(label: String, onCode: (DeviceCode) -> Unit, sleep: suspend (Long) -> Unit = { delay(it) }) {
        val c = client ?: return
        val tokens = DeviceSignIn({ path, body -> c.publicPost(path, body) }, label, sleep = sleep).run(onCode)
        if (c !== client) return
        c.keep(tokens)
        signedIn(c, c.whoami())
    }

    fun signOut() {
        _current.value?.let { tokensFor(it.url).clear() }
        _phase.value = Phase.SigningIn
    }

    /** A screen saw `ApiException.SignedOut`: back to signing in. */
    fun signedOut() {
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

    private companion object {
        const val KEY_HOMES = "homes"
        const val KEY_HOME = "home"
    }
}
