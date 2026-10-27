package com.hamzalabs.maktaba.desktop

import com.hamzalabs.maktaba.core.api.ApiClient
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.File
import java.io.IOException
import java.util.concurrent.TimeUnit

/**
 * The players' update feed, `latest-player.json` (Epic 42.4), written by
 * `tools/release/cmd/updater-feed --family player` beside the server apps' `latest.json`:
 * the same shape, its own platform keys (`windows-x86_64-player`, `linux-x86_64-player-deb`,
 * …), so a player never takes a server bundle and the other way round.
 */
@Serializable
data class PlayerFeed(
    val version: String,
    val notes: String = "",
    val platforms: Map<String, FeedEntry> = emptyMap(),
)

@Serializable
data class FeedEntry(val signature: String, val url: String)

object Updates {
    const val FEED = "https://github.com/Hamza-Labs-Core/Maktaba/releases/latest/download/latest-player.json"

    private val http = OkHttpClient.Builder()
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(60, TimeUnit.SECONDS)
        .build()

    /**
     * This computer's keys in the feed, the installer it uses first: the .msi on Windows; on
     * Linux the package its system installs (.deb, else .rpm), then the bare key (the .deb).
     */
    fun platformKeys(
        os: String = System.getProperty("os.name"),
        arch: String = System.getProperty("os.arch"),
        hasRpmOnly: Boolean = File("/usr/bin/rpm").exists() && !File("/usr/bin/dpkg").exists(),
    ): List<String> {
        val a = when (arch.lowercase()) {
            "amd64", "x86_64" -> "x86_64"
            "aarch64", "arm64" -> "aarch64"
            else -> return emptyList()
        }
        val o = os.lowercase()
        return when {
            o.startsWith("windows") -> listOf("windows-$a-player-msi", "windows-$a-player")
            o.startsWith("linux") ->
                if (hasRpmOnly) listOf("linux-$a-player-rpm") else listOf("linux-$a-player-deb", "linux-$a-player")
            else -> emptyList()
        }
    }

    /** The entry this computer installs from [feed], or null when it has none. */
    fun entryFor(feed: PlayerFeed, keys: List<String> = platformKeys()): FeedEntry? =
        keys.firstNotNullOfOrNull { feed.platforms[it] }

    /** Whether [latest] is newer than [current] (x.y.z, a pre-release older than its release). */
    fun isNewer(latest: String, current: String): Boolean {
        fun parse(v: String): Pair<List<Int>, String?>? {
            val s = v.trim().removePrefix("v")
            val core = s.substringBefore('-').substringBefore('+')
            val nums = core.split('.').map { it.toIntOrNull() ?: return null }
            if (nums.size != 3) return null
            return nums to s.substringAfter('-', "").substringBefore('+').ifEmpty { null }
        }
        val (l, lp) = parse(latest) ?: return false
        val (c, cp) = parse(current) ?: return true // a development build: anything released is newer
        for (i in 0 until 3) if (l[i] != c[i]) return l[i] > c[i]
        return when {
            lp == null && cp != null -> true
            lp != null && cp != null -> lp > cp
            else -> false
        }
    }

    suspend fun fetch(url: String = FEED): PlayerFeed = withContext(Dispatchers.IO) {
        http.newCall(Request.Builder().url(url).build()).execute().use { r ->
            if (!r.isSuccessful) throw IOException("the update feed answered ${r.code}")
            ApiClient.json.decodeFromString(PlayerFeed.serializer(), r.body.string())
        }
    }

    /**
     * Downloads [entry] into [dir] and checks its signature against the apps' key for
     * [version] ([Minisign.Refused] when it does not check: the file is deleted, never run).
     */
    suspend fun download(entry: FeedEntry, version: String, pubkey: String, dir: File): File = withContext(Dispatchers.IO) {
        require(entry.url.startsWith("https://")) { "not an https download" }
        dir.mkdirs()
        val name = entry.url.substringAfterLast('/').substringBefore('?')
            .takeIf { it.matches(Regex("""[A-Za-z0-9._+-]+""")) } ?: "maktaba-player-update"
        val file = File(dir, name)
        http.newCall(Request.Builder().url(entry.url).build()).execute().use { r ->
            if (!r.isSuccessful) throw IOException("the download answered ${r.code}")
            file.outputStream().use { out -> r.body.byteStream().copyTo(out) }
        }
        try {
            Minisign.verify(file.readBytes(), entry.signature, pubkey, version)
        } catch (e: Exception) {
            file.delete()
            throw e
        }
        file
    }

    /**
     * Starts installing [file]: on Windows the .msi (a current-user install: no administrator),
     * after which the player quits; elsewhere null (the person installs the package).
     */
    fun install(file: File): Process? =
        if (AppDirs.isWindows) ProcessBuilder("msiexec", "/i", file.absolutePath, "/passive").start() else null
}
