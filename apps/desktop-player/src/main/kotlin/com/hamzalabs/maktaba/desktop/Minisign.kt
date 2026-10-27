package com.hamzalabs.maktaba.desktop

import java.math.BigInteger
import java.security.KeyFactory
import java.security.Signature
import java.security.spec.EdECPoint
import java.security.spec.EdECPublicKeySpec
import java.security.spec.NamedParameterSpec
import java.util.Base64

/**
 * The update's signature, checked as the server apps' updater checks theirs
 * (tools/release/updaterfeed/verify.go, tauri-plugin-updater): a minisign signature by the
 * apps' key over the file (prehashed with BLAKE2b-512 for "ED"), its trusted comment signed
 * too, naming the version. The release signs the players with the same key.
 */
object Minisign {
    class Refused(why: String) : Exception(why)

    /** Throws [Refused] unless [sig] (the `.sig`: base64 of a minisign signature) signs [file] for [version]. */
    fun verify(file: ByteArray, sig: String, pubkey: String, version: String) {
        val (keyId, key) = parsePubKey(pubkey)
        val lines = String(decode(sig.trim(), "the signature is not base64")).replace("\r", "")
            .split("\n").filter { it.isNotEmpty() }
        if (lines.size != 4 || !lines[0].startsWith("untrusted comment:") || !lines[2].startsWith("trusted comment: ")) {
            throw Refused("not a minisign signature")
        }
        val raw = decode(lines[1], "the signature line is not base64")
        if (raw.size != 2 + 8 + 64) throw Refused("the signature line is not an algorithm, a key id and a signature")
        val alg = String(raw, 0, 2)
        if (!raw.copyOfRange(2, 10).contentEquals(keyId)) throw Refused("signed by another key")
        val s = raw.copyOfRange(10, 74)
        val msg = when (alg) {
            "ED" -> Blake2b.hash512(file)
            "Ed" -> file
            else -> throw Refused("unknown signature algorithm $alg")
        }
        if (!ed25519(key, msg, s)) throw Refused("the signature does not match the file")
        val trusted = lines[2].removePrefix("trusted comment: ")
        val global = decode(lines[3], "the global signature is not base64")
        if (global.size != 64 || !ed25519(key, s + trusted.toByteArray(), global)) {
            throw Refused("the trusted comment's signature does not match")
        }
        val signed = trusted.split('\t').lastOrNull { it.startsWith("version:") }?.removePrefix("version:")
            ?: throw Refused("the signature names no version")
        if (signed.removePrefix("v") != version.removePrefix("v")) throw Refused("signed for version $signed, not $version")
    }

    /** The key id and Ed25519 key of a minisign public key (the key file in base64, or its key line). */
    private fun parsePubKey(text: String): Pair<ByteArray, ByteArray> {
        var t = text.trim()
        runCatching { String(Base64.getDecoder().decode(t)) }.getOrNull()
            ?.takeIf { it.trim().startsWith("untrusted comment:") }?.let { t = it }
        val line = t.replace("\r", "").split("\n").map { it.trim() }
            .lastOrNull { it.isNotEmpty() && !it.startsWith("untrusted comment:") } ?: ""
        val raw = decode(line, "not a minisign public key")
        if (raw.size != 42 || String(raw, 0, 2) != "Ed") throw Refused("not a minisign public key")
        return raw.copyOfRange(2, 10) to raw.copyOfRange(10, 42)
    }

    private fun decode(s: String, why: String): ByteArray =
        runCatching { Base64.getDecoder().decode(s.trim()) }.getOrElse { throw Refused(why) }

    /** Ed25519 with the JDK's own (Java 15 and later); the raw key is little-endian y with x's sign in the top bit. */
    private fun ed25519(rawKey: ByteArray, msg: ByteArray, sig: ByteArray): Boolean {
        val le = rawKey.copyOf()
        val xOdd = (le[31].toInt() and 0x80) != 0
        le[31] = (le[31].toInt() and 0x7f).toByte()
        val y = BigInteger(1, le.reversedArray())
        val key = KeyFactory.getInstance("Ed25519")
            .generatePublic(EdECPublicKeySpec(NamedParameterSpec.ED25519, EdECPoint(xOdd, y)))
        return Signature.getInstance("Ed25519").run {
            initVerify(key)
            update(msg)
            runCatching { verify(sig) }.getOrDefault(false)
        }
    }
}

/** BLAKE2b-512 (RFC 7693), unkeyed: minisign's prehash. */
object Blake2b {
    private val IV = longArrayOf(
        0x6a09e667f3bcc908, -0x4498517a7b3558c5, 0x3c6ef372fe94f82b, -0x5ab00ac5a0e2c90f,
        0x510e527fade682d1, -0x64fa9773d4c193e1, 0x1f83d9abfb41bd6b, 0x5be0cd19137e2179,
    )
    private val SIGMA = arrayOf(
        intArrayOf(0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15),
        intArrayOf(14, 10, 4, 8, 9, 15, 13, 6, 1, 12, 0, 2, 11, 7, 5, 3),
        intArrayOf(11, 8, 12, 0, 5, 2, 15, 13, 10, 14, 3, 6, 7, 1, 9, 4),
        intArrayOf(7, 9, 3, 1, 13, 12, 11, 14, 2, 6, 5, 10, 4, 0, 15, 8),
        intArrayOf(9, 0, 5, 7, 2, 4, 10, 15, 14, 1, 11, 12, 6, 8, 3, 13),
        intArrayOf(2, 12, 6, 10, 0, 11, 8, 3, 4, 13, 7, 5, 15, 14, 1, 9),
        intArrayOf(12, 5, 1, 15, 14, 13, 4, 10, 0, 7, 6, 3, 9, 2, 8, 11),
        intArrayOf(13, 11, 7, 14, 12, 1, 3, 9, 5, 0, 15, 4, 8, 6, 2, 10),
        intArrayOf(6, 15, 14, 9, 11, 3, 0, 8, 12, 2, 13, 7, 1, 4, 10, 5),
        intArrayOf(10, 2, 8, 4, 7, 6, 1, 5, 15, 11, 9, 14, 3, 12, 13, 0),
    )

    fun hash512(data: ByteArray): ByteArray {
        val h = IV.copyOf()
        h[0] = h[0] xor 0x01010040L // digest length 64, no key, fanout 1, depth 1
        val block = ByteArray(128)
        var off = 0
        var counter = 0L
        // every full block but the last is compressed as it comes; the last (maybe partial, maybe empty) is final
        while (data.size - off > 128) {
            counter += 128
            compress(h, data, off, counter, last = false)
            off += 128
        }
        val rest = data.size - off
        block.fill(0)
        System.arraycopy(data, off, block, 0, rest)
        counter += rest
        compress(h, block, 0, counter, last = true)
        val out = ByteArray(64)
        for (i in 0 until 8) for (b in 0 until 8) out[i * 8 + b] = (h[i] ushr (8 * b)).toByte()
        return out
    }

    private fun compress(h: LongArray, buf: ByteArray, off: Int, counter: Long, last: Boolean) {
        val m = LongArray(16) { i ->
            var w = 0L
            for (b in 7 downTo 0) w = (w shl 8) or (buf[off + i * 8 + b].toLong() and 0xff)
            w
        }
        val v = LongArray(16)
        System.arraycopy(h, 0, v, 0, 8)
        System.arraycopy(IV, 0, v, 8, 8)
        v[12] = v[12] xor counter // the counter's high word stays 0: no file here reaches 2^64 bytes
        if (last) v[14] = v[14].inv()
        for (r in 0 until 12) {
            val s = SIGMA[r % 10]
            g(v, 0, 4, 8, 12, m[s[0]], m[s[1]])
            g(v, 1, 5, 9, 13, m[s[2]], m[s[3]])
            g(v, 2, 6, 10, 14, m[s[4]], m[s[5]])
            g(v, 3, 7, 11, 15, m[s[6]], m[s[7]])
            g(v, 0, 5, 10, 15, m[s[8]], m[s[9]])
            g(v, 1, 6, 11, 12, m[s[10]], m[s[11]])
            g(v, 2, 7, 8, 13, m[s[12]], m[s[13]])
            g(v, 3, 4, 9, 14, m[s[14]], m[s[15]])
        }
        for (i in 0 until 8) h[i] = h[i] xor v[i] xor v[i + 8]
    }

    private fun g(v: LongArray, a: Int, b: Int, c: Int, d: Int, x: Long, y: Long) {
        v[a] += v[b] + x
        v[d] = (v[d] xor v[a]).rotateRight(32)
        v[c] += v[d]
        v[b] = (v[b] xor v[c]).rotateRight(24)
        v[a] += v[b] + y
        v[d] = (v[d] xor v[a]).rotateRight(16)
        v[c] += v[d]
        v[b] = (v[b] xor v[c]).rotateRight(63)
    }
}
