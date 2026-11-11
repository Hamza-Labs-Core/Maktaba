package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.Video
import com.hamzalabs.maktaba.core.api.VideoCard
import com.hamzalabs.maktaba.core.api.VideoTitle
import org.junit.Assert.assertEquals
import org.junit.Test

/** A video with no metadata title shows its file name without the extension (the API's rule). */
class VideoTitleTest {
    @Test
    fun theFinalExtensionGoesAndNothingElse() {
        // the same cases as the API's Go rule (api/internal/videotitle)
        val cases = listOf(
            "My Lecture 03.mp4" to "My Lecture 03",
            "a.b.mkv" to "a.b",
            "clip.MP4" to "clip",
            "محاضرة الفقه.mkv" to "محاضرة الفقه",
            "notes" to "notes",
            ".mp4" to ".mp4",
            "x." to "x.",
            "" to "",
            "Ch. 3 intro" to "Ch. 3 intro",
            "..mp4" to ".",
            ".hidden.mkv" to ".hidden",
        )
        for ((name, want) in cases) assertEquals(name, want, VideoTitle.stripExt(name))
    }

    @Test
    fun aMetadataTitleIsKeptAsItIs() {
        assertEquals("Title.mp4", VideoTitle.of("Title.mp4", "f.mp4"))
        assertEquals("My Lecture 03", VideoTitle.of(null, "My Lecture 03.mp4"))
        assertEquals("a.b", VideoTitle.of("  ", "a.b.mkv"))
    }

    @Test
    fun modelsUseTheRule() {
        assertEquals("My Lecture 03", Video(id = "v1", filename = "My Lecture 03.mp4").displayTitle)
        assertEquals("Seerah", Video(id = "v2", filename = "s.mp4", title = "Seerah").displayTitle)
        assertEquals("lesson", VideoCard(id = "v3", filename = "lesson.mkv").displayTitle)
    }
}
