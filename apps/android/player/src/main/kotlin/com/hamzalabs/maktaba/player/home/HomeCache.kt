package com.hamzalabs.maktaba.player.home

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.HomeRails
import com.hamzalabs.maktaba.core.api.Recommendations
import com.hamzalabs.maktaba.core.api.VideoCard
import kotlinx.serialization.Serializable
import okhttp3.HttpUrl
import java.io.File
import java.security.MessageDigest

/** Home's last answer as kept on the device: the rows, the recommendations and the videos read for their cards. */
@Serializable
data class CachedHome(
    val rails: HomeRails,
    val recs: Recommendations? = null,
    val known: Map<String, VideoCard> = emptyMap(),
)

/**
 * Home's last answer on disk (FR-12: the next launch shows it at once and refreshes behind it),
 * one file per home and person under the app's cache folder (the system may clear it; a file
 * that cannot be read is no cache). Live TV on now is not kept: it is stale within minutes.
 */
class HomeCache(private val file: File) {
    fun read(): CachedHome? = runCatching { ApiClient.json.decodeFromString(CachedHome.serializer(), file.readText()) }.getOrNull()

    /**
     * Writes [home] whole or not at all (a temporary file, then a rename). Saves come from several
     * reads at once (Home, and each recommendation's video), so one at a time: otherwise one
     * save's rename races another's temporary file and the fallback deletes the kept answer.
     */
    fun write(home: CachedHome) {
        synchronized(this) {
            runCatching {
                file.parentFile?.mkdirs()
                val tmp = File(file.parentFile, file.name + ".tmp")
                tmp.writeText(ApiClient.json.encodeToString(CachedHome.serializer(), home))
                if (!tmp.renameTo(file)) {
                    file.delete()
                    tmp.renameTo(file)
                }
            }
        }
    }

    companion object {
        /** The folder under the app's cache folder. */
        const val FOLDER = "home-rows"

        /** The cache for [userId] on [home], in [cacheDir]: the file is named by a hash, so no address or id is readable in it. */
        fun of(cacheDir: File, home: HttpUrl, userId: String): HomeCache {
            val digest = MessageDigest.getInstance("SHA-256").digest("$home|$userId".toByteArray())
            val name = digest.take(16).joinToString("") { Integer.toHexString((it.toInt() and 0xff) or 0x100).substring(1) }
            return HomeCache(File(File(cacheDir, FOLDER), "$name.json"))
        }
    }
}
