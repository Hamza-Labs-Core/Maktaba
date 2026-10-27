package com.hamzalabs.maktaba.desktop

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.session.KeyValueStore
import com.hamzalabs.maktaba.core.session.SealedTokenStore
import com.hamzalabs.maktaba.core.session.SecretBox
import com.hamzalabs.maktaba.core.session.TokenStore
import com.sun.jna.platform.win32.Crypt32Util
import kotlinx.serialization.builtins.MapSerializer
import kotlinx.serialization.builtins.serializer
import java.io.File
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.nio.file.attribute.PosixFilePermissions
import java.security.SecureRandom
import javax.crypto.Cipher
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

/** Where the player keeps its homes, sealed tokens and choices: this user's own. */
object AppDirs {
    private val os = System.getProperty("os.name").lowercase()
    val isWindows = os.startsWith("windows")

    val config: File by lazy {
        val home = System.getProperty("user.home")
        val dir = when {
            isWindows -> File(System.getenv("APPDATA") ?: "$home\\AppData\\Roaming", "Maktaba Player")
            os.contains("mac") -> File(home, "Library/Application Support/Maktaba Player")
            else -> File(System.getenv("XDG_CONFIG_HOME")?.takeIf { it.isNotBlank() } ?: "$home/.config", "maktaba-player")
        }
        dir.mkdirs()
        ownerOnly(dir.toPath(), directory = true)
        dir
    }

    /** Readable by this user alone where the system has POSIX permissions (Linux; Windows' profile already is). */
    fun ownerOnly(path: java.nio.file.Path, directory: Boolean = false) {
        runCatching {
            Files.setPosixFilePermissions(path, PosixFilePermissions.fromString(if (directory) "rwx------" else "rw-------"))
        }
    }
}

/**
 * Strings by key in one JSON file, written whole to a temporary file and moved over the old
 * one, so a crash mid-write never leaves half a file.
 */
class FileStore(private val file: File) : KeyValueStore {
    private val lock = Any()
    private val map: MutableMap<String, String> = runCatching {
        ApiClient.json.decodeFromString(SERIALIZER, file.readText()).toMutableMap()
    }.getOrElse { mutableMapOf() }

    override fun get(key: String): String? = synchronized(lock) { map[key] }

    override fun put(key: String, value: String?) = synchronized(lock) {
        if (value == null) map.remove(key) else map[key] = value
        file.parentFile?.mkdirs()
        val tmp = File(file.parentFile, file.name + ".tmp")
        tmp.writeText(ApiClient.json.encodeToString(SERIALIZER, map))
        AppDirs.ownerOnly(tmp.toPath())
        Files.move(tmp.toPath(), file.toPath(), StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE)
        Unit
    }

    private companion object {
        val SERIALIZER = MapSerializer(String.serializer(), String.serializer())
    }
}

/**
 * Windows' DPAPI: sealed with a key the system keeps for this Windows user, so the sealed
 * tokens open only for them on this machine (as the Android Keystore on a phone).
 */
class DpapiSecretBox : SecretBox {
    override fun seal(plain: ByteArray): ByteArray = Crypt32Util.cryptProtectData(plain)
    override fun open(sealed: ByteArray): ByteArray = Crypt32Util.cryptUnprotectData(sealed)
}

/**
 * AES-GCM with a key in a file only this user can read (Linux has no key store every desktop
 * shares). Sealed bytes are the 12-byte IV, then the ciphertext and its tag. A key file that
 * is lost or replaced makes the tokens unreadable: the person signs in again.
 */
class KeyFileSecretBox(private val keyFile: File) : SecretBox {
    private val key: SecretKeySpec by lazy {
        val bytes = keyFile.takeIf { it.isFile }?.readBytes()?.takeIf { it.size == KEY_BYTES }
            ?: ByteArray(KEY_BYTES).also { k ->
                SecureRandom().nextBytes(k)
                keyFile.parentFile?.mkdirs()
                keyFile.writeBytes(k)
                AppDirs.ownerOnly(keyFile.toPath())
            }
        SecretKeySpec(bytes, "AES")
    }

    override fun seal(plain: ByteArray): ByteArray {
        val iv = ByteArray(IV_BYTES).also(SecureRandom()::nextBytes)
        val c = Cipher.getInstance(TRANSFORMATION)
        c.init(Cipher.ENCRYPT_MODE, key, GCMParameterSpec(128, iv))
        return iv + c.doFinal(plain)
    }

    override fun open(sealed: ByteArray): ByteArray {
        val c = Cipher.getInstance(TRANSFORMATION)
        c.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(128, sealed, 0, IV_BYTES))
        return c.doFinal(sealed, IV_BYTES, sealed.size - IV_BYTES)
    }

    private companion object {
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val KEY_BYTES = 32
        const val IV_BYTES = 12
    }
}

/** The player's stores: its homes and choices, and each home's sealed tokens. */
class Stores(dir: File = AppDirs.config) {
    val settings: KeyValueStore = FileStore(File(dir, "player.json"))
    private val tokenFile: KeyValueStore = FileStore(File(dir, "tokens.json"))
    private val box: SecretBox = if (AppDirs.isWindows) DpapiSecretBox() else KeyFileSecretBox(File(dir, "tokens.key"))

    /** One home's tokens, sealed, kept apart from every other home's. */
    fun tokens(home: String): TokenStore = SealedTokenStore(tokenFile, box, home)
}
