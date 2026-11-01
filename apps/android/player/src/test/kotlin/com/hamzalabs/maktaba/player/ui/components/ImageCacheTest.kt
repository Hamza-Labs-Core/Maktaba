package com.hamzalabs.maktaba.player.ui.components

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import coil3.ComponentRegistry
import coil3.ImageLoader
import coil3.decode.BitmapFactoryDecoder
import coil3.request.ErrorResult
import coil3.request.ImageRequest
import coil3.request.SuccessResult
import com.hamzalabs.maktaba.core.MemoryTokens
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.jwt
import kotlinx.coroutines.runBlocking
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.HttpUrl.Companion.toHttpUrl
import okio.Buffer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.annotation.GraphicsMode

/**
 * NFR-4 on Android: the image cache's limits, the token on the home's pictures only, the small
 * poster's URL, and a repeat visit that fetches nothing within the server's `max-age`. The home is
 * a MockWebServer serving generated PNGs; no real home or token is involved.
 */
/**
 * Robolectric has no ImageDecoder, which Coil uses on Android 9+: decode with BitmapFactory here.
 * The loader's own parts (caches, network, auth, cache strategy) are unchanged.
 */
val robolectricDecoder: ComponentRegistry.Builder.() -> Unit = { add(BitmapFactoryDecoder.Factory()) }

@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class ImageCacheTest {
    @get:Rule val folder = TemporaryFolder()

    private val context: Context = ApplicationProvider.getApplicationContext()
    private val home = MockWebServer()
    private val elsewhere = MockWebServer()
    private val homeRequests = mutableListOf<RecordedRequest>()
    private val otherRequests = mutableListOf<RecordedRequest>()
    private var cacheControl = "private, max-age=3600"
    private var clock = 1_000_000_000L
    private val access = jwt(System.currentTimeMillis() / 1000 + 86_400)

    private fun pngResponse() = MockResponse.Builder()
        .setHeader("Content-Type", "image/png")
        .setHeader("Cache-Control", cacheControl)
        .body(Buffer().write(FakePictures.png("poster")))
        .build()

    @Before
    fun start() {
        home.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                synchronized(homeRequests) { homeRequests += request }
                return if (request.headers["Authorization"] == "Bearer $access") pngResponse() else MockResponse.Builder().code(401).build()
            }
        }
        elsewhere.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                synchronized(otherRequests) { otherRequests += request }
                return pngResponse()
            }
        }
        home.start()
        elsewhere.start()
    }

    @After
    fun stop() {
        home.close()
        elsewhere.close()
    }

    private fun client() = ApiClient(home.url("/"), MemoryTokens(access, "refresh"))

    private fun loader(memoryOn: Boolean = true): ImageLoader {
        val disk = ImageCache.diskCache(folder.root)
        val memory = ImageCache.memoryCache(context)
        if (!memoryOn) memory.clear()
        return ImageCache.loader(context, client(), memory = memory, disk = disk, now = { clock }, online = true, decoders = robolectricDecoder)
    }

    private fun load(loader: ImageLoader, url: String) = runBlocking { loader.execute(ImageRequest.Builder(context).data(url).size(320, 180).build()) }

    @Test
    fun theCachesStayWithinNfr4() {
        assertEquals(50L * 1024 * 1024, ImageCache.MEMORY_MAX_BYTES)
        assertEquals(200L * 1024 * 1024, ImageCache.DISK_MAX_BYTES)
        // a big heap still caps at 50 MB; a small phone gets a quarter of its heap
        assertEquals(ImageCache.MEMORY_MAX_BYTES, ImageCache.memoryBytes(4096L * 1024 * 1024))
        assertEquals(32L * 1024 * 1024, ImageCache.memoryBytes(128L * 1024 * 1024))
        val l = ImageCache.loader(context, client(), online = true)
        assertTrue("memory ${l.memoryCache!!.maxSize}", l.memoryCache!!.maxSize in 1..ImageCache.MEMORY_MAX_BYTES)
        assertEquals(ImageCache.DISK_MAX_BYTES, l.diskCache!!.maxSize)
    }

    @Test
    fun everyHomeSharesOneMemoryAndOneDiskCache() {
        val a = ImageCache.loader(context, client(), online = true)
        val b = ImageCache.loader(context, ApiClient("http://other.test/".toHttpUrl(), MemoryTokens()), online = true)
        assertTrue(a.memoryCache === b.memoryCache)
        assertTrue(a.diskCache === b.diskCache)
    }

    @Test
    fun theSmallPosterIsAskedForInGridsAndTheFullOneOtherwise() {
        val c = client()
        assertEquals(home.url("/api/videos/v1/poster?size=small").toString(), posterUrl(c, "v1"))
        assertEquals(home.url("/api/videos/v1/poster").toString(), posterUrl(c, "v1", PosterSize.Full))
        // under a base path, and an id that needs encoding stays one segment
        val base = "https://home.test/maktaba/".toHttpUrl()
        assertEquals("https://home.test/maktaba/api/videos/a%20b/poster?size=small", posterUrl(base, "/maktaba", "a b"))
    }

    @Test
    fun theHomesPicturesCarryTheTokenAndNoOtherHostSeesIt() {
        val l = loader()
        val poster = load(l, posterUrl(client(), "v1"))
        assertTrue("$poster", poster is SuccessResult)
        val other = load(l, elsewhere.url("/logo.png").toString())
        assertTrue("$other", other is SuccessResult)

        assertEquals("Bearer $access", homeRequests.single().headers["Authorization"])
        assertEquals("small", homeRequests.single().url.queryParameter("size"))
        assertNull(otherRequests.single().headers["Authorization"])
    }

    @Test
    fun onlyTheHomesApiIsAuthorised() {
        val auth = HomeImageAuth(client())
        assertTrue(auth.isHomeApi(home.url("/api/channels/c1/logo")))
        assertTrue(!auth.isHomeApi(home.url("/stream/x/index.m3u8")))
        assertTrue(!auth.isHomeApi(elsewhere.url("/api/videos/v1/poster")))
    }

    @Test
    fun aRepeatVisitFetchesNothingWithinMaxAge() {
        val url = posterUrl(client(), "v1")
        assertTrue(load(loader(), url) is SuccessResult)
        // a new visit: a fresh loader with an empty memory cache, the same disk cache
        clock += 3_599_000
        assertTrue(load(loader(memoryOn = false), url) is SuccessResult)
        assertEquals("within max-age the disk cache answers", 1, homeRequests.size)
        // past max-age it asks again
        clock += 2_000
        assertTrue(load(loader(memoryOn = false), url) is SuccessResult)
        assertEquals(2, homeRequests.size)
    }

    @Test
    fun noStoreIsNeverKept() {
        cacheControl = "no-store"
        val url = posterUrl(client(), "v1")
        load(loader(), url)
        load(loader(memoryOn = false), url)
        assertEquals(2, homeRequests.size)
    }

    @Test
    fun aMissingPosterIsAnError() {
        home.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest) = MockResponse.Builder().code(404).build()
        }
        assertTrue(load(loader(), posterUrl(client(), "nope")) is ErrorResult)
    }

    @Test
    fun maxAgeIsReadFromCacheControl() {
        assertEquals(3600L, MaxAgeCacheStrategy.maxAgeSec("private, max-age=3600"))
        assertEquals(60L, MaxAgeCacheStrategy.maxAgeSec("Max-Age=60"))
        assertEquals(0L, MaxAgeCacheStrategy.maxAgeSec("no-cache"))
        assertEquals(0L, MaxAgeCacheStrategy.maxAgeSec("no-store, max-age=60"))
        assertNull(MaxAgeCacheStrategy.maxAgeSec(null))
        assertNull(MaxAgeCacheStrategy.maxAgeSec("private"))
    }
}
