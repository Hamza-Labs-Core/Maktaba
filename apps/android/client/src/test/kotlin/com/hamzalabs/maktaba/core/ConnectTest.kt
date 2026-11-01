package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.StreamSession
import okhttp3.HttpUrl.Companion.toHttpUrl
import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.ConnectLink
import com.hamzalabs.maktaba.core.session.FoundHome
import com.hamzalabs.maktaba.core.session.Home
import com.hamzalabs.maktaba.core.session.HomeProbe
import com.hamzalabs.maktaba.core.session.NotAHome
import com.hamzalabs.maktaba.core.session.addressCandidates
import com.hamzalabs.maktaba.core.session.dedupeHomes
import com.hamzalabs.maktaba.core.session.foundHome
import com.hamzalabs.maktaba.core.session.homeIdKey
import com.hamzalabs.maktaba.core.session.sameHome
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import okhttp3.HttpUrl
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import java.net.InetAddress

class ConnectTest {
    private val mac = InetAddress.getByName("192.168.50.177")

    // ---------- mDNS TXT ----------

    @Test
    fun theTxtRecordGivesTheSchemeAndBasePath() {
        val txt = mapOf("role" to "home", "id" to "28e21b45", "name" to "Darwish", "web" to "8443", "tls" to "1", "path" to "/maktaba/")
        assertEquals(
            FoundHome("Darwish", "https://192.168.50.177:8443/maktaba", "28e21b45", null),
            foundHome("Maktaba (studio)", mac, 8080, txt),
        )
        assertEquals("tls=0 and path=/: http at the root", "http://192.168.50.177:8443", foundHome("x", mac, 8080, txt + ("tls" to "0") + ("path" to "/"))!!.url)
        assertEquals("a path with a space is not a path", "http://192.168.50.177:8443", foundHome("x", mac, 8080, txt - "tls" + ("path" to "/a b"))!!.url)
    }

    @Test
    fun theHomesIdIsPreferredOverTheMachinesId() {
        val h = foundHome("x", mac, 8080, mapOf("id" to "aaaa1111", "home" to "bbbb2222"))!!
        assertEquals("bbbb2222", h.id)
        assertNull("a blank id is none", foundHome("x", mac, 8080, mapOf("id" to " "))!!.id)
    }

    @Test
    fun aHomeAnnouncedTwiceIsOneRowByIdElseByHostAndPort() {
        val a = FoundHome("Studio", "http://192.168.1.5:8088", "28e21b45", null)
        val sameIdOtherAddress = FoundHome("Studio", "http://[fe80:0:0:0:0:0:0:1]:8088", "28E21B45", null)
        val fullId = FoundHome("Studio", "http://10.0.0.5:8088", "28e21b45-1111-2222-3333-444455556666", null)
        val noId = FoundHome("Attic", "http://192.168.1.9:8080", null, null)
        val noIdAgain = FoundHome("Attic (renamed)", "http://192.168.1.9:8080/maktaba", null, null)
        val other = FoundHome("Other", "http://192.168.1.9:9090", null, null)
        assertEquals(listOf(a, noId, other), dedupeHomes(listOf(a, sameIdOtherAddress, noId, fullId, noIdAgain, other)))
    }

    @Test
    fun onlyTheHomeIsListedNotTheMachinesThatWorkForIt() {
        val home = "28e21b45-1111-2222-3333-444455556666"
        val txt = mapOf("protocol" to "1", "home" to home, "name" to "Studio", "web" to "8088", "version" to "v0.4.0")
        val announced = listOfNotNull(
            foundHome("Maktaba (studio)", mac, 8080, txt + ("role" to "home") + ("id" to home)),
            // a full node of the same home, and a machine of a kind this app does not know
            foundHome("Maktaba (gpu-box)", InetAddress.getByName("192.168.50.20"), 8780, txt + ("role" to "node") + ("id" to "9f9f9f9f-0000")),
            foundHome("Maktaba (later)", InetAddress.getByName("192.168.50.21"), 8780, txt + ("role" to "relay") + ("id" to "abababab-0000")),
            // the same home again on another interface
            foundHome("Maktaba (studio)", InetAddress.getByName("fe80::1"), 8080, txt + ("role" to "home") + ("id" to home)),
            // an older home: no role, and no home= (its id= stands in)
            foundHome("Maktaba (attic)", InetAddress.getByName("192.168.50.9"), 8080, mapOf("id" to "77777777", "version" to "v0.2.0")),
        )
        val homes = dedupeHomes(announced)
        assertEquals(listOf("http://192.168.50.177:8088", "http://192.168.50.9:8080"), homes.map { it.url })
        assertEquals("the home's id from home=, not the machine's", home, homes.first().id)
    }

    @Test
    fun idsCompareByTheirShortForm() {
        assertEquals("28e21b45", homeIdKey("28E21B45-aaaa-bbbb"))
        assertTrue(sameHome("28e21b45", "28E21B45-1111-2222-3333-444455556666"))
        assertFalse(sameHome("28e21b45", null))
        assertFalse(sameHome("", ""))
        assertFalse(sameHome("28e21b45", "28e21b46"))
    }

    // ---------- links ----------

    @Test
    fun aConnectLinkKeepsItsAddressesInOrder() {
        val link = ConnectLink.parse(
            "maktaba://connect?id=28e21b45&n=Darwish%20home&u=http%3A%2F%2F192.168.1.5%3A8088&u=https%3A%2F%2Fdarwish.maktaba.tv%2F&u=http%3A%2F%2F192.168.1.5%3A8088",
        )!!
        assertEquals("28e21b45", link.id)
        assertEquals("Darwish home", link.name)
        assertEquals("local first, then the others, once each", listOf("http://192.168.1.5:8088", "https://darwish.maktaba.tv"), link.urls)
    }

    @Test
    fun aConnectLinkKeepsABasePathAndDropsWhatIsNotAnAddress() {
        val link = ConnectLink.parse("maktaba://connect?u=https://example.com/maktaba/&u=ftp://x&u=&n=+Shared+")!!
        assertEquals(listOf("https://example.com/maktaba"), link.urls)
        assertNull(link.id)
        assertEquals("Shared", link.name)
        assertEquals("a scanner's maktaba:connect form", listOf("http://10.0.0.2:8088"), ConnectLink.parse("maktaba:connect?u=10.0.0.2:8088")!!.urls)
    }

    @Test
    fun whatIsNotAHomeLinkIsNull() {
        assertNull(ConnectLink.parse(""))
        assertNull("no address", ConnectLink.parse("maktaba://connect?id=1&n=x"))
        assertNull("another action", ConnectLink.parse("maktaba://pair?u=http://a"))
        assertNull("another scheme", ConnectLink.parse("mailto:a@b.c"))
        assertNull("plain words", ConnectLink.parse("hello there"))
    }

    @Test
    fun aWebAddressScannedIsTriedWithItsPathThenAtItsRoot() {
        assertEquals(listOf("http://192.168.1.5:8088/library", "http://192.168.1.5:8088"), ConnectLink.parse("http://192.168.1.5:8088/library?x=1")!!.urls)
        assertEquals(listOf("http://192.168.1.5:8088"), addressCandidates("192.168.1.5:8088"))
        assertEquals(listOf("https://example.com/maktaba", "https://example.com"), addressCandidates("example.com/maktaba/"))
        assertEquals(emptyList<String>(), addressCandidates("  "))
        assertEquals("http://maktaba.local:8088/m", Home.normalizeWithPath("maktaba.local:8088/m/#x"))
    }

    // ---------- checking a home ----------

    /** Answers by URL: path → (status, body). Records what was asked. */
    private class FakeHomes(val routes: Map<String, Pair<Int, String>>) {
        val asked = mutableListOf<String>()
        suspend fun get(url: HttpUrl): Pair<Int, String> {
            asked += url.toString()
            val key = url.toString()
            if (key.startsWith("http://down")) throw ApiException.Transport("connect timed out")
            return routes[key] ?: (404 to "404 page not found")
        }
    }

    private val discovery = """{"id":"28e21b45-1111","name":"Darwish home","version":"v0.4.0","urls":["http://192.168.1.5:8088"],"sign_in":{"password":true,"code":true}}"""

    @Test
    fun aHomeSaysWhoItIs() = runBlocking {
        val homes = FakeHomes(mapOf("http://h:8088/api/discovery/home" to (200 to discovery)))
        val info = HomeProbe(homes::get).probe("http://h:8088")
        assertEquals("28e21b45-1111", info.id)
        assertEquals("Darwish home", info.name)
        assertEquals(true, info.signIn?.code)
    }

    @Test
    fun aHomeUnderABasePathIsAskedThere() = runBlocking {
        val homes = FakeHomes(mapOf("https://example.com/maktaba/api/discovery/home" to (200 to discovery)))
        assertEquals("Darwish home", HomeProbe(homes::get).probe("https://example.com/maktaba").name)
    }

    @Test
    fun anOlderHomeIsKnownByItsVersion() = runBlocking {
        val homes = FakeHomes(mapOf("http://h:8088/api/system/version" to (200 to """{"version":"v0.3.20","role":"home","home_instance_id":"28e21b45-aaaa"}""")))
        val info = HomeProbe(homes::get).probe("http://h:8088")
        assertEquals("v0.3.20", info.version)
        assertEquals("28e21b45-aaaa", info.id)
        assertNull("an older home does not say its name", info.name)
        assertNull(info.signIn)
        assertEquals(listOf("http://h:8088/api/discovery/home", "http://h:8088/api/system/version"), homes.asked)
    }

    @Test
    fun whatIsNotAHomeIsRefused() = runBlocking {
        val probe = HomeProbe(
            FakeHomes(
                mapOf(
                    "http://router/api/discovery/home" to (200 to "<html>login</html>"),
                    "http://node:8780/api/system/version" to (200 to """{"version":"v0.4.0","role":"node"}"""),
                    "http://other/api/system/version" to (200 to """{"status":"ok"}"""),
                    "http://busy/api/discovery/home" to (503 to ""),
                ),
            )::get,
        )
        for (u in listOf("http://router", "http://node:8780", "http://other", "http://busy", "http://nothing")) {
            try {
                probe.probe(u)
                fail("$u is not a home")
            } catch (e: NotAHome) {
                // as expected
            }
        }
    }

    @Test
    fun aHomesAddressesAreTriedInOrderAndTheFirstThatAnswersIsKept() = runBlocking {
        val homes = FakeHomes(
            mapOf(
                "https://relay.example/api/discovery/home" to (200 to discovery),
                "https://second.example/api/discovery/home" to (200 to discovery),
            ),
        )
        val r = HomeProbe(homes::get).first(listOf("http://down:8088", "http://gone:8088", "https://relay.example", "https://second.example"))
        assertEquals("https://relay.example", r.url)
        assertEquals(
            "each in turn, none after the one that answered",
            listOf(
                "http://down:8088/api/discovery/home",
                "http://gone:8088/api/discovery/home", "http://gone:8088/api/system/version",
                "https://relay.example/api/discovery/home",
            ),
            homes.asked,
        )
    }

    @Test
    fun anAddressWhereAnotherHomeAnswersIsPassedOver() = runBlocking {
        val homes = FakeHomes(
            mapOf(
                "http://192.168.1.5:8088/api/discovery/home" to (200 to """{"id":"ffff0000","name":"Neighbour"}"""),
                "https://relay.example/api/discovery/home" to (200 to discovery),
            ),
        )
        val r = HomeProbe(homes::get).first(listOf("http://192.168.1.5:8088", "https://relay.example"), expectId = "28e21b45")
        assertEquals("https://relay.example", r.url)
        try {
            HomeProbe(homes::get).first(listOf("http://192.168.1.5:8088"), expectId = "28e21b45")
            fail("only another home answered")
        } catch (e: NotAHome) {
            assertEquals(listOf("http://192.168.1.5:8088"), e.tried)
        }
    }

    @Test
    fun aHomeUnderABasePathIsTalkedToThere() {
        val c = ApiClient("https://example.com/maktaba".toHttpUrl(), MemoryTokens())
        assertEquals("https://example.com/maktaba/api/videos", c.request("GET", "/api/videos").url.toString())
        assertTrue(c.isStream("https://example.com/maktaba/stream/s/master.m3u8".toHttpUrl()))
        assertFalse("the token goes only under the base path", c.isStream("https://example.com/stream/s/master.m3u8".toHttpUrl()))
        assertEquals(
            "https://example.com/maktaba/stream/s/master.m3u8?sig=1",
            StreamSession("s", manifestUrl = "/stream/s/master.m3u8?sig=1").playbackUrl(c.home).toString(),
        )
        assertEquals(
            "a path already under it is kept",
            "https://example.com/maktaba/stream/s/a.mp4",
            StreamSession("s", directUrl = "/maktaba/stream/s/a.mp4").playbackUrl(c.home).toString(),
        )
        val root = ApiClient("http://h:8088".toHttpUrl(), MemoryTokens())
        assertEquals("http://h:8088/api/videos", root.request("GET", "/api/videos").url.toString())
        assertEquals("http://h:8088/stream/s/master.m3u8", StreamSession("s", manifestUrl = "/stream/s/master.m3u8").playbackUrl(root.home).toString())
    }

    // ---------- the session ----------

    private fun session(store: MapStore = MapStore(), tokens: MutableMap<String, MemoryTokens> = mutableMapOf()) =
        AppSession(store, CoroutineScope(Dispatchers.Unconfined), { tokens.getOrPut(it) { MemoryTokens() } })

    @Test
    fun connectingUsesTheAddressThatAnsweredWithTheHomesName() = runBlocking {
        val homes = FakeHomes(mapOf("https://relay.example/api/discovery/home" to (200 to discovery)))
        val s = session()
        val link = ConnectLink("28e21b45", "From the link", listOf("http://down:8088", "https://relay.example"))
        s.offer(link)
        val home = s.connect(link, HomeProbe(homes::get))
        // its other addresses as the home said them; the link's that did not answer are not kept
        assertEquals(Home("https://relay.example", "Darwish home", "28e21b45-1111", addresses = listOf("http://192.168.1.5:8088")), home)
        assertEquals(AppSession.Phase.SigningIn, s.phase.value)
        assertNull("the link was answered", s.link.value)
    }

    @Test
    fun anOlderHomeIsNamedByTheLinkElseItsAddress() = runBlocking {
        val homes = FakeHomes(mapOf("http://h:8088/api/system/version" to (200 to """{"version":"v0.3.20"}""")))
        assertEquals("Attic", session().connect(ConnectLink(null, "Attic", listOf("http://h:8088")), HomeProbe(homes::get)).name)
        assertEquals("h:8088", session().connect(ConnectLink(null, null, listOf("http://h:8088")), HomeProbe(homes::get)).name)
    }

    @Test
    fun theSameIdAtANewAddressGetsNoTokens() {
        val tokens = mutableMapOf<String, MemoryTokens>()
        val s = session(tokens = tokens)
        s.use(Home("http://192.168.1.5:8088", "Studio", "28e21b45"))
        s.rename(s.current.value!!, "Living room")
        tokens.getValue("http://192.168.1.5:8088").save(AuthTokens("a1", "r1"))
        s.switchHome()

        // an id is what a home says of itself: anyone on the network can claim it
        s.use(Home("http://192.168.1.77:8088", "Studio", "28E21B45-1111"))
        assertEquals("another entry, not the known home moved", 2, s.homes.value.size)
        val claimed = s.current.value!!
        assertEquals("http://192.168.1.77:8088", claimed.url)
        assertEquals("nothing of the known home carries over", "Studio", claimed.label)
        assertNull("no tokens at the new address: a fresh sign-in", tokens.getValue("http://192.168.1.77:8088").access)
        assertEquals(AppSession.Phase.SigningIn, s.phase.value)
        assertEquals("the known home keeps its own", "a1", tokens.getValue("http://192.168.1.5:8088").access)
    }

    @Test
    fun aLinkNamingAKnownHomeAtAnotherAddressStillSignsInAfresh() = runBlocking {
        val tokens = mutableMapOf<String, MemoryTokens>()
        val s = session(tokens = tokens)
        s.use(Home("http://192.168.1.5:8088", "Studio", "28e21b45"))
        tokens.getValue("http://192.168.1.5:8088").save(AuthTokens("a1", "r1"))
        val homes = FakeHomes(mapOf("https://evil.example/api/discovery/home" to (200 to """{"id":"28e21b45","name":"Studio"}""")))
        s.connect(ConnectLink("28e21b45", "Studio", listOf("https://evil.example")), HomeProbe(homes::get))
        assertEquals("https://evil.example", s.current.value?.url)
        assertNull(tokens.getValue("https://evil.example").access)
        assertEquals(AppSession.Phase.SigningIn, s.phase.value)
    }

    @Test
    fun aHomeIsRenamedOnThisDeviceAndBackToItsOwnName() {
        val store = MapStore()
        val s = session(store)
        s.use(Home("http://a:8088", "Studio"))
        s.use(Home("http://b:8088", "Attic"))
        s.rename(s.homes.value.first(), "  Living room ")
        assertEquals("Living room", s.homes.value.first().label)
        assertEquals("Studio", s.homes.value.first().name)
        assertEquals("the others keep theirs", "Attic", s.homes.value.last().label)
        assertEquals("kept", "Living room", session(store).homes.value.first().label)
        s.rename(s.homes.value.first(), " ")
        assertEquals("Studio", s.homes.value.first().label)
    }

    @Test
    fun homesSavedBeforeIdsAndNamesStillLoad() {
        val store = MapStore()
        store.put("homes", """[{"url":"http://a:8088","name":"Studio"}]""")
        assertEquals(listOf(Home("http://a:8088", "Studio")), session(store).homes.value)
    }

    @Test
    fun aLinkTheAppOpensWithIsOfferedNotUsed() {
        val s = session()
        s.use(Home("http://10.0.0.2:8088", "Current"))
        assertTrue(s.openLink("maktaba://connect?u=http://192.168.1.5:8088&n=Studio"))
        assertEquals("Studio", s.link.value?.name)
        assertEquals("nothing switches until the person confirms", "http://10.0.0.2:8088", s.current.value?.url)
        assertEquals("nothing is added either", 1, s.homes.value.size)
        assertFalse(s.openLink(null))
        assertFalse(s.openLink("https://maktaba.tv/help"))
        s.dismissLink()
        assertNull(s.link.value)
    }
}
