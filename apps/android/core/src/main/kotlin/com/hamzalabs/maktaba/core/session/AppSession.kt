package com.hamzalabs.maktaba.core.session

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.Me
import com.hamzalabs.maktaba.core.api.User
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/** A home this app knows: its address and a name to show (its address until it says its name). */
@Serializable
data class Home(val url: String, val name: String) {
    val httpUrl: HttpUrl get() = url.toHttpUrl()

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

    /** The client for the current home (null while choosing one). */
    var client: ApiClient? = null
        private set

    init {
        store.get(KEY_HOME)?.let { last -> _homes.value.firstOrNull { it.url == last }?.let(::use) }
    }

    /** Picks a home: its client, and signed in at once when its tokens are still kept. */
    fun use(home: Home) {
        if (_homes.value.none { it.url == home.url }) {
            _homes.value = _homes.value + home
            saveHomes()
        }
        _current.value = home
        store.put(KEY_HOME, home.url)
        val tokens = tokensFor(home.url)
        val c = clientFor(home.httpUrl, tokens)
        client = c
        _phase.value = Phase.SigningIn
        if (tokens.access != null) {
            scope.launch { runCatching { c.whoami() }.getOrNull()?.let { signedIn(c, it) } }
        }
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
