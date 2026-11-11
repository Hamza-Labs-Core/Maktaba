import Foundation
import Testing
@testable import MaktabaKit

/// A video with no metadata title shows its file name without the extension (the API's rule).
struct VideoTitleTests {
    // the same cases as the API's Go rule (api/internal/videotitle)
    @Test(arguments: [
        ("My Lecture 03.mp4", "My Lecture 03"),
        ("a.b.mkv", "a.b"),
        ("clip.MP4", "clip"),
        ("محاضرة الفقه.mkv", "محاضرة الفقه"),
        ("notes", "notes"),
        (".mp4", ".mp4"),
        ("x.", "x."),
        ("", ""),
        ("Ch. 3 intro", "Ch. 3 intro"),
        ("..mp4", "."),
        (".hidden.mkv", ".hidden"),
        ("صوت.٣", "صوت.٣"), // a non-ASCII digit is not an extension
    ])
    func theFinalExtensionGoesAndNothingElse(name: String, want: String) {
        #expect(VideoTitle.stripExt(name) == want)
    }

    @Test func aMetadataTitleIsKeptAsItIs() {
        #expect(VideoTitle.of("Title.mp4", "f.mp4") == "Title.mp4")
        #expect(VideoTitle.of(nil, "My Lecture 03.mp4") == "My Lecture 03")
        #expect(VideoTitle.of("  ", "a.b.mkv") == "a.b")
    }

    @Test func browseCardsUseTheRule() throws {
        let json = #"{"id":"v1","filename":"My Lecture 03.mp4","title":null}"#
        #expect(try JSONDecoder().decode(BrowseVideo.self, from: Data(json.utf8)).displayTitle == "My Lecture 03")
        let info = #"{"id":"v2","filename":"a.b.mkv"}"#
        #expect(try JSONDecoder().decode(VideoInfo.self, from: Data(info.utf8)).displayTitle == "a.b")
    }
}
