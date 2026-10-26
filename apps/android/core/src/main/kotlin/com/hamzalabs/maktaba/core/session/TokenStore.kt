package com.hamzalabs.maktaba.core.session

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import androidx.core.content.edit
import com.hamzalabs.maktaba.core.api.AuthTokens
import java.security.KeyStore
import java.util.Base64
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** A home's access and refresh tokens. */
interface TokenStore {
    val access: String?
    val refresh: String?
    fun save(tokens: AuthTokens)
    fun clear()
}

/** Strings by key: SharedPreferences on a device, a map in tests. */
interface KeyValueStore {
    fun get(key: String): String?
    fun put(key: String, value: String?)
}

/** Seals and opens bytes: the Android Keystore's key on a device. */
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

/** A SharedPreferences file as a [KeyValueStore]. */
class PreferencesStore(context: Context, name: String) : KeyValueStore {
    private val prefs = context.getSharedPreferences(name, Context.MODE_PRIVATE)
    override fun get(key: String): String? = prefs.getString(key, null)
    override fun put(key: String, value: String?) {
        prefs.edit { if (value == null) remove(key) else putString(key, value) }
    }
}

/**
 * AES-GCM with a key that lives in the Android Keystore and never leaves it: the app holds
 * only the sealed tokens. Sealed bytes are the 12-byte IV, then the ciphertext and its tag.
 */
class KeystoreSecretBox(private val alias: String) : SecretBox {
    private fun key(): SecretKey {
        val ks = KeyStore.getInstance(KEYSTORE).apply { load(null) }
        (ks.getKey(alias, null) as? SecretKey)?.let { return it }
        val gen = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KEYSTORE)
        gen.init(
            KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build(),
        )
        return gen.generateKey()
    }

    override fun seal(plain: ByteArray): ByteArray {
        val c = Cipher.getInstance(TRANSFORMATION)
        c.init(Cipher.ENCRYPT_MODE, key())
        return c.iv + c.doFinal(plain)
    }

    override fun open(sealed: ByteArray): ByteArray {
        val c = Cipher.getInstance(TRANSFORMATION)
        c.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, sealed, 0, IV_BYTES))
        return c.doFinal(sealed, IV_BYTES, sealed.size - IV_BYTES)
    }

    private companion object {
        const val KEYSTORE = "AndroidKeyStore"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val IV_BYTES = 12
    }
}

/** The device's token store for one app and home. */
fun keystoreTokens(context: Context, app: String, home: String): TokenStore =
    SealedTokenStore(PreferencesStore(context, "$app.tokens"), KeystoreSecretBox("$app.tokens"), home)
