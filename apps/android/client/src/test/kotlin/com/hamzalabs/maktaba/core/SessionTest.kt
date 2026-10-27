package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.Home
import com.hamzalabs.maktaba.core.session.SealedTokenStore
import com.hamzalabs.maktaba.core.session.SecretBox
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Reverses the bytes and adds a mark: enough to see that only sealed bytes are stored. */
class FakeBox : SecretBox {
    var broken = false
    override fun seal(plain: ByteArray) = byteArrayOf(42) + plain.reversedArray()
    override fun open(sealed: ByteArray): ByteArray {
        check(!broken && sealed[0] == 42.toByte())
        return sealed.copyOfRange(1, sealed.size).reversedArray()
    }
}

class SessionTest {
    @Test
    fun normalizesAHomesAddress() {
        assertEquals("http://192.168.50.177:8088", Home.normalize(" 192.168.50.177:8088/library?x=1 "))
        assertEquals("http://maktaba.local:8088", Home.normalize("maktaba.local:8088"))
        assertEquals("http://10.0.0.2", Home.normalize("10.0.0.2/"))
        assertEquals("http://172.20.1.1:8088", Home.normalize("172.20.1.1:8088"))
        assertEquals("https://home.example.com", Home.normalize("home.example.com"))
        assertEquals("https://172.40.1.1", Home.normalize("172.40.1.1"))
        assertEquals("http://home.example.com:8088", Home.normalize("http://home.example.com:8088/"))
        assertNull(Home.normalize("   "))
        assertNull(Home.normalize("ftp://home"))
    }

    @Test
    fun tokensAreSealedAndKeptPerHome() {
        val store = MapStore()
        val box = FakeBox()
        val a = SealedTokenStore(store, box, "http://a:8088")
        val b = SealedTokenStore(store, box, "http://b:8088")
        a.save(AuthTokens("access-a", "refresh-a"))
        b.save(AuthTokens("access-b", "refresh-b"))
        assertEquals("access-a", a.access)
        assertEquals("refresh-b", b.refresh)
        assertFalse("only sealed bytes are stored", store.map.values.any { it.contains("access") || it.contains("refresh") })
        a.clear()
        assertNull(a.access)
        assertEquals("signing out of one home leaves the others", "access-b", b.access)
        box.broken = true
        assertNull("a value that no longer opens reads as signed out", b.access)
    }

    @Test
    fun remembersHomesAndTheOneInUse() {
        val store = MapStore()
        val tokens = mutableMapOf<String, MemoryTokens>()
        fun session() = AppSession(store, CoroutineScope(Dispatchers.Unconfined), { tokens.getOrPut(it) { MemoryTokens() } })

        val s = session()
        assertEquals(AppSession.Phase.ChoosingHome, s.phase.value)
        s.use(Home("http://192.168.50.177:8088", "192.168.50.177:8088"))
        assertEquals(AppSession.Phase.SigningIn, s.phase.value)
        s.named("Darwish home")
        assertEquals("Darwish home", s.current.value?.name)

        val again = session()
        assertEquals("the last home is used again", "Darwish home", again.current.value?.name)
        assertEquals(1, again.homes.value.size)
        again.named(null)
        assertEquals("a home that does not say its name keeps it", "Darwish home", again.current.value?.name)

        tokens.getValue("http://192.168.50.177:8088").save(AuthTokens("a", "r"))
        again.forget(again.current.value!!)
        assertTrue(again.homes.value.isEmpty())
        assertNull("forgetting a home drops its tokens", tokens.getValue("http://192.168.50.177:8088").access)
        assertEquals(AppSession.Phase.ChoosingHome, again.phase.value)
    }
}
