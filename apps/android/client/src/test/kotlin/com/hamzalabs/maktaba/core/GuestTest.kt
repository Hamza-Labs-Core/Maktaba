package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.guest.GuestClient
import com.hamzalabs.maktaba.core.guest.GuestError
import com.hamzalabs.maktaba.core.guest.GuestInput
import com.hamzalabs.maktaba.core.guest.GuestLive
import com.hamzalabs.maktaba.core.guest.GuestPairPoll
import com.hamzalabs.maktaba.core.guest.GuestPairing
import com.hamzalabs.maktaba.core.guest.GuestPairingRun
import com.hamzalabs.maktaba.core.guest.GuestPass
import com.hamzalabs.maktaba.core.guest.GuestPassStore
import com.hamzalabs.maktaba.core.guest.GuestSpace
import com.hamzalabs.maktaba.core.guest.GuestViewerState
import com.hamzalabs.maktaba.core.guest.GuestWatch
import com.hamzalabs.maktaba.core.session.SecretBox
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.io.File
import java.util.Base64
import java.util.Collections

/** Story 50.27 T04: the guest pass store, the edge client, the pairing poll and the token in the path. */
@OptIn(ExperimentalCoroutinesApi::class)
class GuestTest {
    /** Seals by xor: enough to show the store never writes the plain list. */
    private class XorBox(private val k: Int = 0x5a) : SecretBox {
        override fun seal(plain: ByteArray) = byteArrayOf(1) + plain.map { (it.toInt() xor k).toByte() }
        override fun open(sealed: ByteArray): ByteArray {
            require(sealed.firstOrNull() == 1.toByte()) { "not sealed by this box" }
            return sealed.drop(1).map { (it.toInt() xor k).toByte() }.toByteArray()
        }
    }

    private fun cred(exp: Long, prefix: String = "mkbg1"): String {
        val enc = Base64.getUrlEncoder().withoutPadding()
        return "$prefix." + enc.encodeToString("""{"pub":"p","exp":$exp}""".toByteArray()) + ".sig"
    }

    // region the states, shared with the edge and the Apple apps

    private fun repoFile(path: String): File {
        var dir: File? = File("").absoluteFile
        while (dir != null) {
            val f = File(dir, path)
            if (f.isFile) return f
            dir = dir.parentFile
        }
        error("$path not found above ${File("").absolutePath}")
    }

    @Test
    fun everySharedStateVectorMapsAsTheEdgeDoes() {
        val cases = Json.parseToJsonElement(repoFile("shared/broadcast/viewer-states.json").readText()).jsonObject["cases"] as JsonArray
        assertTrue(cases.size >= 20)
        for (c in cases) {
            val o = c.jsonObject
            val body = o["body"]?.takeIf { it !is JsonNull }?.jsonObject
            val s = GuestViewerState.of(
                o.getValue("status").jsonPrimitive.int,
                channelState = body?.get("channel_state")?.jsonPrimitive?.content,
                error = body?.get("error")?.jsonPrimitive?.content,
            )
            val what = o.toString()
            assertEquals(what, o.getValue("state").jsonPrimitive.content, s.state.name)
            assertEquals(what, o.getValue("key").jsonPrimitive.content, s.key)
            assertEquals(what, o.getValue("plays").jsonPrimitive.boolean, s.plays)
            assertEquals(what, o.getValue("retry").jsonPrimitive.boolean, s.retry)
        }
    }

    @Test
    fun aRefusalTheStatesDoNotNameReadsAsTheInvitesOwn() {
        assertEquals("broadcast.invite.invalid", GuestError(404, "code_unknown").messageKey)
        assertEquals("broadcast.invite.invalid", GuestError(400, "bad_token").messageKey)
        assertEquals("guest.network", GuestError.Offline.messageKey)
        assertEquals("broadcast.invite.used", GuestError(410, "invite_used").messageKey)
        assertEquals("site.error", GuestError(500, "").messageKey)
    }

    // endregion

    // region what is typed or opened

    private val token = "mkbn1.eyJwIjoxfQ.c2ln-_x"

    @Test
    fun anInviteLinkIsReadStrictly() {
        assertEquals(token, GuestInput.inviteInLink("https://watch.maktaba.tv/i/$token"))
        assertEquals(token, GuestInput.inviteInLink("maktaba://watch/i/$token"))
        assertEquals(token, GuestInput.inviteInLink("HTTPS://WATCH.maktaba.tv/i/$token"))
        val refused = listOf(
            "http://watch.maktaba.tv/i/$token", // not https
            "https://evil.example/i/$token", // another host
            "https://watch.maktaba.tv.evil.example/i/$token",
            "https://user@watch.maktaba.tv/i/$token", // user info
            "https://user:pw@watch.maktaba.tv/i/$token",
            "https://watch.maktaba.tv:443/i/$token", // a port, even the usual one
            "https://watch.maktaba.tv:8443/i/$token",
            "https://watch.maktaba.tv/en/i/$token", // the path is exactly /i/<token>
            "https://watch.maktaba.tv/i/$token/",
            "https://watch.maktaba.tv//i/$token",
            "https://watch.maktaba.tv/i/",
            "https://watch.maktaba.tv/x/$token",
            "maktaba://connect/i/$token", // another app link
            "maktaba://watch/i/a%2Fb.$token",
            "maktaba://watch/i/mkbn2.abc.def", // the token's shape
            "maktaba://watch/i/mkbn1.abc", "maktaba://watch/i/mkbn1.ab+c.def", "maktaba://watch/i/mkbn1..def",
            "maktaba://watch/i/mkbn1.abc.de=f", "javascript://watch/i/$token", "not a link", "",
        )
        for (l in refused) assertNull(l, GuestInput.inviteInLink(l))
        assertNull(GuestInput.inviteInLink(null))
    }

    @Test
    fun anInviteIsAtMostFourKilobytes() {
        val body = "a".repeat(4096 - "mkbn1..sig".length)
        assertEquals("mkbn1.$body.sig", GuestInput.token("mkbn1.$body.sig"))
        assertNull(GuestInput.token("mkbn1.${body}a.sig"))
        assertNull(GuestInput.inviteInLink("maktaba://watch/i/mkbn1.${body}a.sig"))
    }

    @Test
    fun aPastedInviteIsALinkOrABareToken() {
        assertEquals(token, GuestInput.invite("  https://watch.maktaba.tv/i/$token \n"))
        assertEquals(token, GuestInput.invite(token))
        assertNull(GuestInput.invite("https://watch.maktaba.tv/ar/al-noor"))
        assertNull(GuestInput.invite("mkbg1.a.b"))
    }

    @Test
    fun aShortCodeAndAHandleAreReadAsTyped() {
        assertEquals("MK-7Q2X", GuestInput.code("mk 7q2x"))
        assertEquals("MK-7Q2X", GuestInput.code("7Q2X"))
        assertEquals("MK-7Q2X", GuestInput.code("MK-7Q2X"))
        assertNull(GuestInput.code("MK-7Q2"))
        assertNull(GuestInput.code("MK-7Q2XY"))
        assertNull(GuestInput.code("٧٨٩٠"))
        assertEquals("al-noor", GuestInput.publisher(" al-noor "))
        assertEquals("al-noor", GuestInput.publisher("https://watch.maktaba.tv/ar/al-noor/ch1"))
        assertEquals("al-noor", GuestInput.publisher("https://watch.maktaba.tv/al-noor"))
        assertNull(GuestInput.publisher(""))
        assertNull(GuestInput.publisher("al noor"))
        assertNull(GuestInput.publisher("a".repeat(65)))
    }

    // endregion

    // region the store

    private var clock = 1_000_000L

    private fun store(map: MapStore = MapStore(), box: SecretBox = XorBox()) = GuestPassStore(map, box) { clock }

    private fun pass(pub: String, exp: Long = clock + 3600, name: String = "") = GuestPass(pub, name, cred(exp), channels = listOf("ch1"), expiresAt = exp)

    @Test
    fun savesListsNewestFirstAndRemoves() {
        val map = MapStore()
        val s = store(map)
        s.save(pass("a"))
        clock += 1
        s.save(pass("b"))
        assertEquals(listOf("b", "a"), s.list().map { it.publisher })
        assertEquals(clock, s.pass("b")!!.savedAt)
        // the same publisher again: replaced, at the top
        clock += 1
        s.save(pass("a"))
        assertEquals(listOf("a", "b"), s.list().map { it.publisher })
        s.remove("a")
        assertEquals(listOf("b"), s.list().map { it.publisher })
        s.remove("b")
        assertTrue(s.list().isEmpty())
        assertTrue("an empty list leaves nothing behind: ${map.map}", map.map.isEmpty())
    }

    @Test
    fun onlySealedBytesAreKept() {
        val map = MapStore()
        val p = pass("al-noor")
        store(map).save(p)
        val kept = map.map.getValue(GuestPassStore.KEY)
        assertFalse(p.credential in kept)
        assertFalse("al-noor" in kept)
        assertFalse(p.credential in String(Base64.getDecoder().decode(kept)))
        // the same file opened with another device's key: nothing (never another device's pass)
        assertTrue(store(map, XorBox(0x11).let { k -> object : SecretBox by k { override fun open(sealed: ByteArray) = error("another key") } }).list().isEmpty())
        assertEquals(listOf(p.credential), store(map).list().map { it.credential })
    }

    @Test
    fun anExpiredPassIsDroppedWhenTheListIsRead() {
        val map = MapStore()
        val s = store(map)
        s.save(pass("old", exp = clock + 10))
        s.save(pass("new", exp = clock + 1000))
        clock += 11
        assertEquals(listOf("new"), s.list().map { it.publisher })
        clock += 1000
        assertTrue(s.list().isEmpty())
        assertTrue(map.map.isEmpty())
    }

    @Test
    fun aRenewedPassAndTheLineupsNameAreKept() {
        val s = store()
        s.save(pass("a", name = "Al-Noor"))
        // a new pass with no name keeps the old name
        s.save(pass("a"))
        assertEquals("Al-Noor", s.pass("a")!!.name)
        val renewed = cred(clock + 30 * 86_400)
        s.renew("a", renewed)
        assertEquals(renewed, s.pass("a")!!.credential)
        assertEquals(clock + 30 * 86_400, s.pass("a")!!.expiresAt)
        s.name("a", "النور")
        assertEquals("النور", s.pass("a")!!.label)
        s.renew("nobody", renewed)
        assertEquals(1, s.list().size)
    }

    @Test
    fun aPassNeverPrintsItsCredential() {
        val p = pass("a")
        assertFalse(p.credential in p.toString())
        assertEquals(clock + 3600, GuestPass.claimedExpiry(p.credential))
        assertNull(GuestPass.claimedExpiry("mkbg1.%%%.sig"))
        assertNull(GuestPass.claimedExpiry("nope"))
    }

    // endregion

    // region the client against a mock edge

    private val server = MockWebServer()
    private val requests: MutableList<RecordedRequest> = Collections.synchronizedList(mutableListOf())
    private var route: (RecordedRequest) -> MockResponse = { json(404, """{"error":"nope"}""") }

    private fun json(code: Int, body: String, vararg headers: Pair<String, String>) =
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").apply { headers.forEach { setHeader(it.first, it.second) } }.body(body).build()

    @Before
    fun start() {
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                requests += request
                return route(request)
            }
        }
        server.start()
    }

    @After
    fun close() = server.close()

    private val lines: MutableList<String> = Collections.synchronizedList(mutableListOf())
    private fun client() = GuestClient(server.url("/"), language = { "ar" }, log = { lines += it })

    @Test
    fun redeemsAnInviteAndAShortCode() = runTest {
        route = { r ->
            val body = Json.parseToJsonElement(r.body!!.utf8()).jsonObject
            when {
                body["invite"] != null -> json(200, """{"pass":"${cred(2_000_000_000)}","publisher":"al-noor","channels":["ch1","ch2"],"expires_at":"2033-05-18T03:33:20Z"}""")
                body["code"]?.jsonPrimitive?.content == "MK-USED" -> json(410, """{"error":"invite_used"}""")
                else -> json(404, """{"error":"code_unknown"}""")
            }
        }
        val p = client().redeem(token)
        assertEquals("al-noor", p.publisher)
        assertEquals(listOf("ch1", "ch2"), p.channels)
        assertEquals(2_000_000_000L, p.expiresAt)
        val r = requests.single()
        assertEquals("POST", r.method)
        assertEquals("/api/v1/redeem", r.url.encodedPath)
        assertEquals("ar", r.headers["Accept-Language"])
        assertNull(r.headers["Authorization"])
        try {
            client().redeem("al-noor", "MK-USED")
            fail()
        } catch (e: GuestError) {
            assertEquals(GuestError(410, "invite_used"), e)
            assertEquals(GuestViewerState.Name.inviteUsed, e.state.state)
        }
        assertTrue("the log names the refusal, never the code: $lines", lines.any { "410 invite_used" in it } && lines.none { "MK-USED" in it })
    }

    @Test
    fun theSpaceSavesWhatItRedeemsAndSaysWhyNot() = runTest {
        route = { r ->
            if ("MK-AB12" in r.body!!.utf8()) json(200, """{"pass":"${cred(clock + 3600)}","publisher":"al-noor","channels":["ch1"]}""")
            else json(404, """{"error":"code_unknown"}""")
        }
        val space = GuestSpace(store(), client())
        assertNull(space.redeemCode("al-noor", "MK-AB1"))
        assertEquals("guest.badCode", space.problem.value)
        assertTrue("nothing was sent for a code that cannot be one", requests.isEmpty())
        assertNull(space.redeemInvite("https://evil.example/i/$token"))
        assertEquals("guest.badInvite", space.problem.value)
        assertNull(space.redeemCode("al-noor", "mk zz99"))
        assertEquals("broadcast.invite.invalid", space.problem.value)
        val p = space.redeemCode("al-noor", "mk ab12")
        assertEquals("al-noor", p!!.publisher)
        assertNull(space.problem.value)
        assertEquals(listOf("al-noor"), space.saved.value.map { it.publisher })
        space.remove(p)
        assertTrue(space.saved.value.isEmpty())
    }

    @Test
    fun noAnswerIsOffline() = runTest {
        server.close()
        try {
            client().redeem(token)
            fail()
        } catch (e: GuestError) {
            assertEquals(GuestError.Offline, e)
        }
    }

    @Test
    fun readsTheLineupAndPlaysWithThePassAsTheBearer() = runTest {
        val p = pass("al-noor")
        val renewed = cred(clock + 99_999)
        route = { r ->
            when (r.url.encodedPath) {
                "/api/v1/lineup" -> json(
                    200,
                    """{"publisher":{"id":"al-noor","name":"Al-Noor"},"channels":[{"id":"ch1","name":"Quran","number":1,"state":"live","now":{"start":"2026-11-15T10:00:00Z","end":"2026-11-15T11:00:00Z","title":"Morning","attribution":{"title":"Recitation","by":"A Reciter","licence":"CC BY 4.0"}},"next":null,"extra":1}]}""",
                )
                "/api/v1/play" -> json(200, """{"session_id":"s1","beat_key":"k1","master_url":"https://live.maktaba.tv/vt1/ch1/master.m3u8","refresh_after":300,"channel_state":"offline_slate"}""", "X-Maktaba-Pass" to renewed)
                else -> json(404, "{}")
            }
        }
        val l = client().lineup(p)
        assertEquals("Al-Noor", l.publisher.name)
        val ch = l.channels.single()
        assertEquals("Morning", ch.now!!.title)
        assertEquals(java.time.Instant.parse("2026-11-15T10:00:00Z"), ch.now!!.start)
        assertEquals("CC BY 4.0", ch.now!!.attribution!!.licence)
        assertEquals("publisher=al-noor", requests[0].url.encodedQuery)
        assertEquals("Bearer ${p.credential}", requests[0].headers["Authorization"])

        val play = client().play(p, "ch1")
        assertEquals("s1", play.session)
        assertEquals(GuestViewerState.Name.offline, play.state.state)
        assertTrue(play.state.plays)
        assertEquals(renewed, play.renewedPass)
        assertEquals("Bearer ${p.credential}", requests[1].headers["Authorization"])
    }

    @Test
    fun aPairingWaitsThenBringsThePassAndEndsWhenGone() = runTest {
        val pairing = GuestPairing("n1", "pk1", "Q7XZ", "https://watch.maktaba.tv/pair?tv=n1")
        var answer = 0
        route = { r ->
            assertEquals("pk1", r.headers["X-Maktaba-Pair-Key"])
            when (answer++) {
                0 -> MockResponse.Builder().code(204).build()
                1 -> json(200, """{"publisher":"al-noor","credential":"${cred(clock + 600, "mkbo1")}","channels":["ch1"],"expires_at":"2030-01-01T00:00:00Z"}""")
                else -> json(410, """{"error":"pair_gone"}""")
            }
        }
        val c = client()
        assertEquals(GuestPairPoll.Waiting, c.poll(pairing, wait = 1))
        val paired = c.poll(pairing, wait = 1) as GuestPairPoll.Paired
        assertEquals("al-noor", paired.pass.publisher)
        assertEquals(GuestPass.Kind.Open, paired.pass.kind)
        assertEquals(java.time.Instant.parse("2030-01-01T00:00:00Z").epochSecond, paired.pass.expiresAt)
        assertEquals(GuestPairPoll.Gone, c.poll(pairing, wait = 1))
        assertEquals("/api/v1/pair/n1", requests[0].url.encodedPath)
        assertEquals("1", requests[0].url.queryParameter("wait"))
        assertFalse("the QR address never holds the poll key", "pk1" in pairing.url)
    }

    @Test
    fun thePairingRunTakesANewCodeWhenOneEnds() = runTest {
        var pairs = 0
        var polls = 0
        route = { r ->
            when (r.url.encodedPath) {
                "/api/v1/pair" -> {
                    pairs++
                    json(201, """{"nonce":"n$pairs","poll_key":"pk$pairs","code":"C00$pairs","url":"https://watch.maktaba.tv/pair?tv=n$pairs"}""")
                }
                else -> when (polls++) {
                    0 -> json(410, """{"error":"pair_gone"}""")
                    1 -> json(503, "")
                    2 -> MockResponse.Builder().code(204).build()
                    else -> json(200, """{"publisher":"al-noor","credential":"${cred(clock + 600)}","kind":"pass"}""")
                }
            }
        }
        val pauses = mutableListOf<Long>()
        val run = GuestPairingRun(client()) { pauses += it }
        val pass = run.run()
        assertEquals("al-noor", pass.publisher)
        assertEquals(2, pairs)
        assertEquals("C002", run.pairing.value!!.code)
        assertEquals(listOf(5_000L), pauses)
        assertNull(run.problem.value)
        run.renew()
        assertNull(run.pairing.value)
    }

    // endregion

    // region the token in the path

    @Test
    fun theNewestTokenGoesInEachRequestsPath() {
        val live = GuestLive.of("https://live.maktaba.tv/vt-1/ch1/master.m3u8")!!
        assertEquals("ch1", live.channel)
        assertEquals("https://live.maktaba.tv/vt-1/ch1/480p/index.m3u8", live.rewrite("https://live.maktaba.tv/vt-1/ch1/480p/index.m3u8"))
        assertTrue(live.update("https://live.maktaba.tv/vt-2/ch1/master.m3u8"))
        assertEquals("vt-2", live.currentToken)
        assertEquals("https://live.maktaba.tv/vt-2/ch1/master.m3u8", live.master)
        // an address resolved against the old token's playlist goes out with the new one
        assertEquals("https://live.maktaba.tv/vt-2/ch1/480p/seg7.ts?x=1", live.rewrite("https://live.maktaba.tv/vt-1/ch1/480p/seg7.ts?x=1"))
        // another channel, edge or path is refused, never sent with the token
        assertNull(live.rewrite("https://live.maktaba.tv/vt-1/ch2/480p/seg7.ts"))
        assertNull(live.rewrite("https://evil.example/vt-1/ch1/480p/seg7.ts"))
        assertNull(live.rewrite("http://live.maktaba.tv/vt-1/ch1/480p/seg7.ts"))
        assertNull(live.rewrite("https://live.maktaba.tv/vt-1/ch1/../ch2/seg7.ts"))
        assertNull(live.rewrite("https://live.maktaba.tv/vt-1/ch1"))
        assertFalse(live.update("https://live.maktaba.tv/vt-3/ch2/master.m3u8"))
        assertFalse(live.update("https://other.example/vt-3/ch1/master.m3u8"))
        assertEquals("vt-2", live.currentToken)
        assertNull(GuestLive.of("https://live.maktaba.tv/vt-1/ch1/index.m3u8"))
        assertNull(GuestLive.of("http://live.maktaba.tv/vt-1/ch1/master.m3u8"))
        assertNotNull(GuestLive.of("http://127.0.0.1:8080/vt-1/ch1/master.m3u8"))
    }

    @Test
    fun theHeartbeatRefreshesTheTokenWithoutANewPlay() {
        val scope = TestScope(StandardTestDispatcher())
        var beats = 0
        route = { r ->
            when {
                r.url.encodedPath == "/api/v1/play" -> json(200, """{"session_id":"s1","beat_key":"k1","master_url":"https://live.maktaba.tv/vt-1/ch1/master.m3u8"}""")
                r.url.encodedPath.endsWith("/beat") -> when (++beats) {
                    1 -> json(200, """{"ok":true,"channel_state":"live","master_url":"https://live.maktaba.tv/vt-2/ch1/master.m3u8"}""")
                    2 -> json(200, """{"ok":true,"channel_state":"offline_slate"}""")
                    else -> json(410, """{"error":"invite_expired"}""")
                }
                else -> json(404, "{}")
            }
        }
        val w = GuestWatch(pass("al-noor"), client(), store(), scope, beatEveryMs = 60_000)
        w.play("ch1")
        waitUntil(scope) { w.shown.value.live != null }
        val first = w.shown.value
        assertTrue(first.plays)
        assertEquals("vt-1", first.live!!.currentToken)
        scope.advanceTimeBy(60_001)
        waitUntil(scope) { beats >= 1 && first.live!!.currentToken == "vt-2" }
        assertEquals("the same play: the player keeps going", first.play, w.shown.value.play)
        scope.advanceTimeBy(60_001)
        waitUntil(scope) { w.shown.value.state?.state == GuestViewerState.Name.offline }
        assertTrue(w.shown.value.plays)
        assertEquals("broadcast.homeOffline", w.shown.value.messageKey)
        scope.advanceTimeBy(60_001)
        waitUntil(scope) { w.shown.value.problem != null }
        assertEquals("broadcast.invite.expired", w.shown.value.messageKey)
        assertFalse(w.shown.value.plays)
        assertEquals("k1", Json.parseToJsonElement(requests.last().body!!.utf8()).jsonObject.getValue("beat_key").jsonPrimitive.content)
        w.stop()
    }

    @Test
    fun aRefusedPlayShowsItsStateAndTryAgain() {
        val scope = TestScope(StandardTestDispatcher())
        route = { json(429, """{"error":"broadcast_full"}""") }
        val w = GuestWatch(pass("al-noor"), client(), store(), scope)
        w.play("ch1")
        waitUntil(scope) { w.shown.value.problem != null }
        assertEquals("broadcast.full", w.shown.value.messageKey)
        assertTrue(w.shown.value.canRetry)
        assertFalse(w.shown.value.plays)
        route = { json(200, """{"session_id":"s1","beat_key":"k1","master_url":"https://live.maktaba.tv/vt-1/ch1/master.m3u8","channel_state":"off_air"}""") }
        w.retry()
        waitUntil(scope) { w.shown.value.state != null }
        assertEquals("broadcast.off", w.shown.value.messageKey)
        assertNull(w.shown.value.live)
        w.stop()
    }

    /** Runs [scope]'s due work until [check] holds; the HTTP answers come on OkHttp's threads. */
    private fun waitUntil(scope: TestScope, check: () -> Boolean) {
        val until = System.currentTimeMillis() + 10_000
        while (!check()) {
            scope.runCurrent()
            if (System.currentTimeMillis() > until) fail("timed out")
            Thread.sleep(10)
        }
    }

    // endregion
}
