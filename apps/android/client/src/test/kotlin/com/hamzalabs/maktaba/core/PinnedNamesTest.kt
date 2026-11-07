package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.PinnedNames
import com.hamzalabs.maktaba.core.session.CloudConnection
import com.hamzalabs.maktaba.core.session.CloudHome
import com.hamzalabs.maktaba.core.session.CloudUser
import com.hamzalabs.maktaba.core.session.KeyValueStore
import com.hamzalabs.maktaba.core.session.SavedCloudHomes
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.Request
import okhttp3.tls.HandshakeCertificates
import okhttp3.tls.HeldCertificate
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import javax.net.ssl.SSLPeerUnverifiedException

/**
 * Story 50.15 §3 (DNS rebinding): a home reached by its LAN address is checked against its own
 * name. Over real TLS: a certificate naming only the home's LAN name, served on 127.0.0.1, is
 * refused by IP and accepted once the cloud's `local_ip` entry pins that address to the name;
 * a pin to another home's name is still refused.
 */
class PinnedNamesTest {
    private val hid = "0123456789abcdef0123456789abcdef"
    private val name = "127-0-0-1.$hid.direct-a.example"
    private lateinit var server: MockWebServer
    private lateinit var client: okhttp3.OkHttpClient

    @Before
    fun setUp() {
        PinnedNames.clear()
        val ca = HeldCertificate.Builder().certificateAuthority(0).build()
        val leaf = HeldCertificate.Builder().addSubjectAlternativeName(name).signedBy(ca).build()
        server = MockWebServer()
        server.useHttps(HandshakeCertificates.Builder().heldCertificate(leaf, ca.certificate).build().sslSocketFactory())
        server.start()
        val trust = HandshakeCertificates.Builder().addTrustedCertificate(ca.certificate).build()
        // the app's own client: its hostname check is the pinned one
        client = ApiClient.defaultHttp.newBuilder().sslSocketFactory(trust.sslSocketFactory(), trust.trustManager).build()
    }

    @After
    fun tearDown() {
        server.close()
        PinnedNames.clear()
    }

    private fun get(): Int {
        server.enqueue(MockResponse.Builder().body("ok").build())
        val url = "https://127.0.0.1:${server.port}/api/health"
        return client.newCall(Request.Builder().url(url).build()).execute().use { it.code }
    }

    private fun refused(): Boolean = try {
        get(); false
    } catch (_: SSLPeerUnverifiedException) {
        true
    }

    @Test
    fun anAddressIsCheckedAgainstItsPinnedName() {
        assertTrue("by IP, unpinned: refused", refused())
        PinnedNames.pin("https://127.0.0.1:${server.port}", name)
        assertEquals(200, get())
    }

    @Test
    fun aPinToAnotherHomeIsRefused() {
        PinnedNames.pin("https://127.0.0.1:${server.port}", "127-0-0-1.ffffffffffffffffffffffffffffffff.direct-a.example")
        assertTrue(refused())
    }

    @Test
    fun onlyHttpsAddressesArePinned() {
        PinnedNames.pin("http://192.168.1.20:8444", name)
        PinnedNames.pin("https://home.example:8444", name)
        PinnedNames.pin("https://192.168.1.20:8444", "")
        assertNull(PinnedNames.nameFor("192.168.1.20", 8444))
        assertNull(PinnedNames.nameFor("home.example", 8444))
        PinnedNames.pin("https://[fd00::7]:8444", "FD00--7.$hid.direct-a.example.")
        assertEquals("fd00--7.$hid.direct-a.example", PinnedNames.nameFor("fd00::7", 8444))
        assertNull(PinnedNames.nameFor("fd00::7", 8445))
    }

    @Test
    fun theCloudListPinsItsLocalIpEntriesAndTheyComeAfterTheLanName() {
        val home = ApiClient.json.decodeFromString(
            CloudHome.serializer(),
            """{"server_id":"s","relay_url":"https://x.relay.test","connections":[
              {"url":"https://x.relay.test","kind":"relay"},
              {"url":"https://192.168.1.20:8444","kind":"local_ip","tls_name":"192-168-1-20.$hid.direct-a.example"},
              {"url":"https://192-168-1-20.$hid.direct-a.example:8444","kind":"local"},
              {"url":"https://x","kind":"future_kind"}]}""",
        )
        assertEquals(listOf("local", "local_ip", "relay"), home.ordered.map(CloudConnection::kind))
        // a saved list pins at start, before the cloud answers
        val kv = object : KeyValueStore {
            val m = mutableMapOf<String, String>()
            override fun get(key: String) = m[key]
            override fun put(key: String, value: String?) { if (value == null) m.remove(key) else m[key] = value }
        }
        SavedCloudHomes.save(kv, CloudUser("u", "a@example.test"), listOf(home), 0)
        PinnedNames.clear()
        SavedCloudHomes.pinAll(kv)
        assertEquals("192-168-1-20.$hid.direct-a.example", PinnedNames.nameFor("192.168.1.20", 8444))
    }
}
