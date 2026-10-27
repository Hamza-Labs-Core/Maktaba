package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.session.KeyValueStore
import java.util.Base64

// What the client's, the Android core's and the desktop player's tests share
// (`testFixtures(project(":client"))`).

/** A JWT the client can read the expiry of (its signature is not checked on the device). */
fun jwt(expSec: Long, tag: String = "t"): String {
    val enc = Base64.getUrlEncoder().withoutPadding()
    val head = enc.encodeToString("""{"alg":"RS256"}""".toByteArray())
    val body = enc.encodeToString("""{"sub":"u1","exp":$expSec,"tag":"$tag"}""".toByteArray())
    return "$head.$body.sig"
}

/** The tokens in memory. */
class MemoryTokens(var accessToken: String? = null, var refreshToken: String? = null) :
    com.hamzalabs.maktaba.core.session.TokenStore {
    override val access get() = accessToken
    override val refresh get() = refreshToken
    override fun save(tokens: AuthTokens) {
        accessToken = tokens.accessToken
        refreshToken = tokens.refreshToken
    }
    override fun clear() {
        accessToken = null
        refreshToken = null
    }
}

/** A [KeyValueStore] in memory. */
class MapStore : KeyValueStore {
    val map = mutableMapOf<String, String>()
    override fun get(key: String) = map[key]
    override fun put(key: String, value: String?) {
        if (value == null) map.remove(key) else map[key] = value
    }
}
