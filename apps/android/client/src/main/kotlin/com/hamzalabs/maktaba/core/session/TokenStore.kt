package com.hamzalabs.maktaba.core.session

import com.hamzalabs.maktaba.core.api.AuthTokens
import java.util.Base64

/** A home's access and refresh tokens. */
interface TokenStore {
    val access: String?
    val refresh: String?
    fun save(tokens: AuthTokens)
    fun clear()
}

/** Strings by key: SharedPreferences on Android, a file on a desktop, a map in tests. */
interface KeyValueStore {
    fun get(key: String): String?
    fun put(key: String, value: String?)
}

/** Seals and opens bytes: the Android Keystore's key on a phone, the system's key store on a desktop. */
interface SecretBox {
    fun seal(plain: ByteArray): ByteArray
    fun open(sealed: ByteArray): ByteArray
}

/**
 * A home's tokens, sealed, one entry per home (the key names the home), so signing out of one
 * home leaves the others. Only sealed bytes reach the store; a value that no longer opens (the
 * key was lost, as after a restore to another device) reads as signed out.
 */
class SealedTokenStore(
    private val store: KeyValueStore,
    private val box: SecretBox,
    home: String,
) : TokenStore {
    private val prefix = "$home#"
    private val lock = Any()

    override val access: String? get() = synchronized(lock) { read("access") }
    override val refresh: String? get() = synchronized(lock) { read("refresh") }

    override fun save(tokens: AuthTokens) = synchronized(lock) {
        write("access", tokens.accessToken)
        write("refresh", tokens.refreshToken)
    }

    override fun clear() = synchronized(lock) {
        store.put(prefix + "access", null)
        store.put(prefix + "refresh", null)
    }

    private fun write(kind: String, value: String) {
        store.put(prefix + kind, Base64.getEncoder().encodeToString(box.seal(value.toByteArray())))
    }

    private fun read(kind: String): String? {
        val sealed = store.get(prefix + kind) ?: return null
        return runCatching { String(box.open(Base64.getDecoder().decode(sealed))) }.getOrNull()
    }
}
