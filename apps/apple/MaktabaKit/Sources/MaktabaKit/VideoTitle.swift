import Foundation

/// The title a video shows when it has no metadata title: its file name without
/// the final extension. The API applies the same rule (Go package videotitle)
/// wherever it picks the title itself; this is for the places the apps fall
/// back to `filename` on their own.
public enum VideoTitle {
    /// `filename` without its final extension: a final "." and one or more ASCII
    /// letters or digits, after a non-empty stem. "a.b.mkv" -> "a.b"; "notes",
    /// ".mp4" and "x." stay as they are.
    public static func stripExt(_ filename: String) -> String {
        let scalars = filename.unicodeScalars
        var end = scalars.endIndex
        while end > scalars.startIndex {
            let prev = scalars.index(before: end)
            guard scalars[prev].isASCII, CharacterSet.alphanumerics.contains(scalars[prev]) else { break }
            end = prev
        }
        // `end` is just past the dot: the extension is not empty, and a stem
        // comes before the dot
        guard end < scalars.endIndex, end > scalars.startIndex else { return filename }
        let dot = scalars.index(before: end)
        guard scalars[dot] == ".", dot > scalars.startIndex else { return filename }
        return String(scalars[scalars.startIndex..<dot])
    }

    /// `title` when it is not blank, else `filename` without its extension.
    public static func of(_ title: String?, _ filename: String) -> String {
        title?.nilIfBlank ?? stripExt(filename)
    }
}
