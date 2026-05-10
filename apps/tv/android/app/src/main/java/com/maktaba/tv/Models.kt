package com.maktaba.tv

data class VideoSummary(
    val id: String,
    val title: String,
    val durationSec: Double,
    val positionSec: Double?,
    val posterUrl: String?,
) {
    val progressFraction: Double
        get() = positionSec?.let { p ->
            if (durationSec > 0) minOf(1.0, p / durationSec) else 0.0
        } ?: 0.0
}

data class PairingCode(val code: String, val expiresAtEpochSec: Long)

interface LibraryService {
    suspend fun continueWatching(): List<VideoSummary>
    suspend fun recommendations(): List<VideoSummary>
    suspend fun search(q: String): List<VideoSummary>
}

class StubLibraryService : LibraryService {
    override suspend fun continueWatching() = emptyList<VideoSummary>()
    override suspend fun recommendations() = emptyList<VideoSummary>()
    override suspend fun search(q: String) = emptyList<VideoSummary>()
}

interface PairingService {
    suspend fun requestCode(): PairingCode
}

class StubPairingService : PairingService {
    override suspend fun requestCode(): PairingCode =
        PairingCode(code = "ABCD-1234", expiresAtEpochSec = System.currentTimeMillis() / 1000 + 300)
}
