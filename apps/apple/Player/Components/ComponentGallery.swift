#if DEBUG
import MaktabaKit
import SwiftUI

/// The components on one page each, with made-up content in the app's language: their Xcode
/// previews, and the screen the UI tests photograph (`PlayerComponentsUITests`). A Debug build
/// shows it instead of the app when launched with `-maktaba-gallery <piece>`; pictures come
/// from `-maktaba-gallery-home <url>` (the tests' MockHome), or none.
struct ComponentGallery: View {
    enum Piece: String, CaseIterable, Identifiable {
        case poster, rail, hero, progress, logo, shimmer
        var id: String { rawValue }
    }

    let piece: Piece
    var home: URL?

    init(piece: Piece, home: URL? = nil) {
        self.piece = piece
        self.home = home
    }

    /// The gallery a launch asks for, if any.
    init?(arguments: [String]) {
        guard let name = ShellLaunch.value(of: "-maktaba-gallery", in: arguments), let piece = Piece(rawValue: name) else { return nil }
        self.piece = piece
        home = ShellLaunch.value(of: "-maktaba-gallery-home", in: arguments).flatMap(URL.init(string:))
    }

    private var arabic: Bool { Bundle.main.preferredLocalizations.first == "ar" }

    private func say(_ en: String, _ ar: String) -> String { arabic ? ar : en }

    /// `/api/videos/{id}/poster?size=small` on the gallery's home.
    private func poster(_ id: String) -> URL? {
        home?.appending(path: "api/videos/\(id)/poster").appending(queryItems: [URLQueryItem(name: "size", value: "small")])
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                content
                    .padding(.vertical, MaktabaTokens.Spacing.s4)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .maktabaSurface()
            .navigationTitle(Text(verbatim: "Gallery: \(piece.rawValue)"))
            #if os(iOS)
            .navigationBarTitleDisplayMode(.inline)
            #endif
        }
        .accessibilityIdentifier("gallery.\(piece.rawValue)")
    }

    @ViewBuilder private var content: some View {
        switch piece {
        case .poster: posters
        case .rail: rail
        case .hero: hero
        case .progress: progress
        case .logo: logos
        case .shimmer: shimmer
        }
    }

    private var posters: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s6) {
            PosterCard(title: say("The Seerah, lesson 4", "السيرة النبوية، الدرس الرابع"),
                       detail: say("The Seerah · 48 min", "السيرة النبوية · ٤٨ دقيقة"),
                       poster: poster("v1"), progress: 0.4)
            PosterCard(title: say("Tafsir of Surat al-Kahf", "تفسير سورة الكهف"),
                       detail: say("12 lessons", "١٢ درسًا"), poster: poster("v2"), shape: .tall)
            // the tests answer this poster 404: the title on the brand colour
            PosterCard(title: say("A long title that wraps onto a second line and more",
                                  "عنوان طويل يلتف إلى سطر ثانٍ وأكثر من ذلك أيضًا"),
                       poster: poster("missing"), progress: 1)
        }
        .padding(.horizontal, MaktabaTokens.Spacing.s4)
    }

    private struct Sample: Identifiable {
        let id: String
        let title: String
        let progress: Double?
    }

    private var samples: [Sample] {
        (1 ... 6).map { i in
            Sample(id: "v\(i)", title: say("Lesson \(i)", "الدرس \(i.formatted(.number.locale(Locale(identifier: "ar"))))"),
                   progress: i == 1 ? 0.6 : i == 2 ? 0.2 : nil)
        }
    }

    private var rail: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s6) {
            Rail(Text(verbatim: say("Continue watching", "تابع المشاهدة")), items: samples, seeAll: {}) { s in
                PosterCard(title: s.title, poster: poster(s.id), progress: s.progress)
            }
            Rail(Text(verbatim: say("Series", "السلاسل")), items: samples.prefix(4)) { s in
                PosterCard(title: s.title, poster: poster(s.id), shape: .tall)
            }
        }
    }

    private var hero: some View {
        Hero(title: say("The Seerah, lesson 4", "السيرة النبوية، الدرس الرابع"),
             eyebrow: Text(verbatim: say("Featured", "مميّز")),
             detail: say("The Seerah · 48 min", "السيرة النبوية · ٤٨ دقيقة"),
             backdrop: poster("v1"), progress: 0.4) {}
    }

    private var progress: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s5) {
            ForEach([0.1, 0.4, 0.75, 1.0], id: \.self) { v in
                VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s2) {
                    Text(verbatim: "\(Int(v * 100))%").font(.maktabaBody(13, relativeTo: .caption)).foregroundStyle(Color.mktMuted)
                    ProgressStrip(value: v)
                }
            }
            PosterArt(url: poster("v1"), title: "", progress: 0.4)
                .aspectRatio(16 / 9, contentMode: .fit)
                .frame(maxWidth: 240)
        }
        .padding(.horizontal, MaktabaTokens.Spacing.s4)
    }

    private var logos: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s4) {
            HStack(spacing: MaktabaTokens.Spacing.s4) {
                ChannelLogo(name: say("Quran", "القرآن"), number: 2, logo: home?.appending(path: "api/channels/ch-2/logo"))
                Text(verbatim: say("Quran", "القرآن")).font(.maktabaBody(17, relativeTo: .body)).foregroundStyle(Color.mktText)
            }
            HStack(spacing: MaktabaTokens.Spacing.s4) {
                ChannelLogo(name: say("Lectures", "المحاضرات"), number: 14, logo: nil)
                Text(verbatim: say("Lectures", "المحاضرات")).font(.maktabaBody(17, relativeTo: .body)).foregroundStyle(Color.mktText)
            }
        }
        .padding(.horizontal, MaktabaTokens.Spacing.s4)
    }

    /// A row loading: shimmering blocks in its shape, and a card whose poster never comes
    /// (the tests hold `/api/videos/slow/poster`).
    private var shimmer: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s4) {
            Shimmer(cornerRadius: MaktabaTokens.Radius.sm).frame(width: 160, height: 22)
            HStack(spacing: MaktabaTokens.Spacing.s3) {
                ForEach(0 ..< 2, id: \.self) { _ in
                    Shimmer().aspectRatio(16 / 9, contentMode: .fit).frame(width: 150)
                }
            }
            PosterCard(title: say("Still loading", "ما زال يُحمَّل"), poster: poster("slow"))
        }
        .padding(.horizontal, MaktabaTokens.Spacing.s4)
    }
}

extension View {
    /// A preview in Arabic (right to left) and/or at the third accessibility size, dark.
    func galleryPreview(arabic: Bool, large: Bool) -> some View {
        environment(\.locale, Locale(identifier: arabic ? "ar" : "en"))
            .environment(\.layoutDirection, arabic ? .rightToLeft : .leftToRight)
            .dynamicTypeSize(large ? .accessibility3 : .large)
            .preferredColorScheme(.dark)
    }
}
#endif
