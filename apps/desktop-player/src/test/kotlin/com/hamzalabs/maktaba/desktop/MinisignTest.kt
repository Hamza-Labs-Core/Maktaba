package com.hamzalabs.maktaba.desktop

import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test
import java.util.Base64

/**
 * The update check against a signature made by Go's ed25519 and blake2b (the release side's
 * tools/release/updaterfeed uses the same), so the two can never disagree unnoticed.
 */
class MinisignTest {
    private val pub = "dW50cnVzdGVkIGNvbW1lbnQ6IG1pbmlzaWduIHB1YmxpYyBrZXkgMDgwNzA2MDUwNDAzMDIwMQpSV1FCQWdNRUJRWUhDRTBPdzZEUVArLy9rNm04emlva3RBYlViMFN1L3g5M2g5cnRzVHlxK2tBcwo="
    private val sig = "dW50cnVzdGVkIGNvbW1lbnQ6IHNpZ25hdHVyZSBmcm9tIHRhdXJpIHNlY3JldCBrZXkKUlVRQkFnTUVCUVlIQ0c0Nk5NU0dkOFVhcEdZRE5rbVRIZFFWYm1aeWFPYUxxS3VWRit0ZEcwRFQ1NW02WU1sTUZpQ3ZlalV0YStuQy9PU3d2dXo4TzQ1aHA0V0haL0lVTmdNPQp0cnVzdGVkIGNvbW1lbnQ6IHRpbWVzdGFtcDoxODAwMDAwMDAwCWZpbGU6bWFrdGFiYS1wbGF5ZXJfMC4zLjM1X2FtZDY0LmRlYgl2ZXJzaW9uOjAuMy4zNQpCN2Y1eTVJNzFxalEvRzg2RU42Nm5yUzB2QXhWc1NJdDNiQ0pZczJnbVhMWVUwVERLNG9oUXVTVnp5ZzBkcUFnOU4rQjBpNmZCUC9aa1d1bUlDampBQT09Cg=="
    private val file = "maktaba-player 0.3.35 test bundle\n".toByteArray()

    private fun hex(b: ByteArray) = b.joinToString("") { "%02x".format(it) }

    @Test
    fun blake2bMatchesTheReference() {
        // RFC 7693's "abc", and Go's x/crypto for the empty input, one exact block and several
        assertEquals(
            "ba80a53f981c4d0d6a2797b69f12f6e94c212f14685ac4b74b12bb6fdbffa2d17d87c5392aab792dc252d5de4533cc9518d38aa8dbf1925ab92386edd4009923",
            hex(Blake2b.hash512("abc".toByteArray())),
        )
        assertEquals(
            "786a02f742015903c6c6fd852552d272912f4740e15847618a86e217f71f5419d25e1031afee585313896444934eb04b903a685b1448b755d56f701afe9be2ce",
            hex(Blake2b.hash512(ByteArray(0))),
        )
        assertEquals(
            "865939e120e6805438478841afb739ae4250cf372653078a065cdcfffca4caf798e6d462b65d658fc165782640eded70963449ae1500fb0f24981d7727e22c41",
            hex(Blake2b.hash512(ByteArray(128))),
        )
        assertEquals(
            "d9cf5983dc6b34c0fa1f0226926855ad3eccd2bcdcd8f8053b9a80664d33b5afcc32fd21c70ea14f4ef50ca97c3203c4d1803159f0e01bb6cb1d1c83db52b63c",
            hex(Blake2b.hash512(ByteArray(300) { it.toByte() })),
        )
    }

    @Test
    fun acceptsTheReleasesSignature() {
        Minisign.verify(file, sig, pub, "0.3.35")
        Minisign.verify(file, sig, pub, "v0.3.35")
    }

    @Test
    fun refusesAnotherFileVersionOrKey() {
        assertThrows(Minisign.Refused::class.java) { Minisign.verify(file + 1, sig, pub, "0.3.35") }
        assertThrows(Minisign.Refused::class.java) { Minisign.verify(file, sig, pub, "0.3.36") }
        // the same key under another id is another key
        val otherId = String(Base64.getDecoder().decode(pub)).replace("RWQBAgME", "RWQBAgMF")
        assertThrows(Minisign.Refused::class.java) {
            Minisign.verify(file, sig, Base64.getEncoder().encodeToString(otherId.toByteArray()), "0.3.35")
        }
        assertThrows(Minisign.Refused::class.java) { Minisign.verify(file, "not base64!", pub, "0.3.35") }
    }

    @Test
    fun refusesAnEditedTrustedComment() {
        val text = String(Base64.getDecoder().decode(sig)).replace("version:0.3.35", "version:0.3.99")
        val edited = Base64.getEncoder().encodeToString(text.toByteArray())
        assertThrows(Minisign.Refused::class.java) { Minisign.verify(file, edited, pub, "0.3.99") }
    }
}
