' Story 58.13: the player's logic on brs-engine (source/player.brs): the stream's format, the
' subtitle choice and who draws it (T02), WebVTT cues, Up next, the one retry after an error, the
' queue, chapters, trickplay tiles and seeking; and the deep-link paths certification asks for,
' voice's input event included (T03). Arabic cues and the capability vectors (T01) are in
' tests/player.test.mjs, generated from the shared vector files.
sub Main()
    m.pass = 0
    m.fail = 0
    t_Format()
    t_Subtitles()
    t_Vtt()
    t_Playback()
    t_Voice()
    ck_Result()
end sub

sub t_Format()
    ck_Eq("hls manifest", PlayerStreamFormat({manifest_url: "/stream/s1/manifest.m3u8?sig=a.b"}, "x.mkv"), "hls")
    ck_Eq("dash manifest (AV1 on Roku)", PlayerStreamFormat({manifest_url: "/stream/s1/manifest.mpd?sig=a.b"}, "x.mkv"), "dash")
    ck_Eq("direct mkv by file name", PlayerStreamFormat({direct_url: "/stream/direct/v1?sig=a"}, "Film (2020).MKV"), "mkv")
    ck_Eq("direct mp4 by file name", PlayerStreamFormat({direct_url: "/stream/direct/v1?sig=a"}, "film.mp4"), "mp4")
    ck_Eq("direct by its own extension", PlayerStreamFormat({direct_url: "/stream/s1/direct.mkv?sig=a"}, "film.mp4"), "mkv")
    ck_Eq("a dot in the query is no extension", PlayerStreamFormat({direct_url: "/stream/direct/v1?sig=a.b"}, invalid), "mp4")
    ck_Eq("ts", PlayerStreamFormat({direct_url: "/stream/direct/v1"}, "rec.ts"), "ts")
    ck_Eq("manifest first", PlayerStreamUrl({manifest_url: "/m.m3u8", direct_url: "/d"}), "/m.m3u8")
    ck_Eq("direct", PlayerStreamUrl({direct_url: "/d"}), "/d")
end sub

sub t_Subtitles()
    ' T02: Arabic goes to the channel's own layer, English to Roku's renderer
    ck_Eq("ar: own layer", PlayerSubtitleMode("ar"), "own")
    ck_Eq("ar-EG: own layer", PlayerSubtitleMode("ar-EG"), "own")
    ck_Eq("en: Roku", PlayerSubtitleMode("en"), "roku")
    ck_Eq("fr: Roku", PlayerSubtitleMode("fr"), "roku")
    ck_Eq("none: Roku", PlayerSubtitleMode(invalid), "roku")
    tracks = [{language: "en", is_default: false}, {language: "ar", is_default: true}, {language: "fr", is_default: false}]
    ck_Eq("chosen in Maktaba", PlayerChooseSubtitle(tracks, "fr", "On", "en").language, "fr")
    ck_Eq("off in Maktaba wins over Roku's setting", PlayerChooseSubtitle(tracks, "off", "On", "en"), invalid)
    ck_Eq("chosen but missing", PlayerChooseSubtitle(tracks, "de", "Off", "en"), invalid)
    ' Roku's captions on and nothing chosen: the person's language
    ck_Eq("captions on: the person's language (en)", PlayerChooseSubtitle(tracks, invalid, "On", "en").language, "en")
    ck_Eq("captions on: the person's language (ar)", PlayerChooseSubtitle(tracks, "", "On", "ar").language, "ar")
    ck_Eq("captions on, no track in the language", PlayerChooseSubtitle([{language: "fr"}], invalid, "On", "en"), invalid)
    ' captions off and nothing chosen: the library's default
    ck_Eq("library default", PlayerChooseSubtitle(tracks, invalid, "Off", "en").language, "ar")
    ck_Eq("no default", PlayerChooseSubtitle([{language: "en"}], invalid, "Off", "en"), invalid)
    ck_Eq("no tracks", PlayerChooseSubtitle(invalid, "en", "On", "en"), invalid)
    ck_Eq("sidecar url", PlayerSubtitleUrl("s1", "ar", "abc.def"), "/stream/s1/subs/sidecar/ar.vtt?sig=abc.def")
    ck_Eq("language name", PlayerLanguageName("en"), "English")
    ck_Eq("language name with a region", PlayerLanguageName("fr-CA"), PlayerLanguageName("fr"))
    ck_Eq("unknown language", PlayerLanguageName("xx"), "XX")
    ck_Eq("sidecar url unsigned", PlayerSubtitleUrl("s1", "en", invalid), "/stream/s1/subs/sidecar/en.vtt")
end sub

sub t_Vtt()
    vtt = "WEBVTT" + Chr(13) + Chr(10) + Chr(13) + Chr(10) + "1" + Chr(10) + "00:00:01.000 --> 00:00:03.500 line:90%" + Chr(10) + "<i>Hello</i> &amp; welcome" + Chr(10) + "second line" + Chr(10) + Chr(10) + "01:02.250 --> 01:04.000" + Chr(10) + "<c.yellow>Later</c>" + Chr(10) + Chr(10) + "00:00:05.000 --> 00:00:04.000" + Chr(10) + "backwards" + Chr(10)
    cues = VttParse(vtt)
    ck_Eq("cues", cues.Count(), 2)
    ck_Eq("start", cues[0].start, 1)
    ck_Eq("end", cues[0].end, 3.5)
    ck_Eq("tags and entities", cues[0].text, "Hello & welcome" + Chr(10) + "second line")
    ck_Eq("mm:ss", cues[1].start, 62.25)
    ck_Eq("at 2s", VttCueAt(cues, 2), "Hello & welcome" + Chr(10) + "second line")
    ck_Eq("at 3.5s (end is open)", VttCueAt(cues, 3.5), "")
    ck_Eq("at 63s", VttCueAt(cues, 63), "Later")
    ck_Eq("none", VttCueAt(VttParse(""), 1), "")
    ck_Eq("bad time", VttTime("1:2:3:4"), -1)
    ck_Eq("comma millis", VttTime("00:00:02,500"), 2.5)
    ck_Eq("an English cue stays as it is", PlayerCueLines("Hello" + Chr(10) + "world", 50), "Hello" + Chr(10) + "world")
end sub

sub t_Playback()
    ' Up next: 15 seconds before the end, when there is a next video
    ck_Eq("up next at 15 s", PlayerUpNextDue(1785, 1800, true), true)
    ck_Eq("not at 16 s", PlayerUpNextDue(1784, 1800, true), false)
    ck_Eq("not without a next", PlayerUpNextDue(1790, 1800, false), false)
    ck_Eq("not without a duration", PlayerUpNextDue(10, 0, true), false)
    ' an error retries once with force_transcode, then fails
    ck_Eq("first error retries", PlayerAfterError(false), "retry")
    ck_Eq("second error fails", PlayerAfterError(true), "fail")
    ck_Eq("the home's reason", PlayerReason({title: "Forbidden", detail: "no transcode slot free"}), "no transcode slot free")
    ck_Eq("the title without a detail", PlayerReason({title: "Not found"}), "Not found")
    ck_Eq("no reason", PlayerReason(invalid), "")
    ' the queue
    ck_Eq("next", PlayerQueueNext([1, 2, 3], 0), 1)
    ck_Eq("end of queue", PlayerQueueNext([1, 2, 3], 2), -1)
    ck_Eq("no queue", PlayerQueueNext(invalid, 0), -1)
    ' chapters
    ch = [{start_sec: 0, end_sec: 60, title: "Opening"}, {start_sec: 60, title: "Main"}]
    ck_Eq("chapter 1", PlayerChapterAt(ch, 10), "Opening")
    ck_Eq("chapter 2 (no end)", PlayerChapterAt(ch, 600), "Main")
    ck_Eq("no chapters", PlayerChapterAt(invalid, 10), "")
    ' trickplay: tile i (row-major) shows i * interval
    idx = {columns: 10, rows: 10, count: 95, interval_sec: 10, tile_width: 240, tile_height: 135}
    ck_Eq("tile at 0", PlayerTrickTile(idx, 0), {x: 0, y: 0, w: 240, h: 135})
    ck_Eq("tile at 125 s", PlayerTrickTile(idx, 125), {x: 480, y: 135, w: 240, h: 135})
    ck_Eq("past the last tile", PlayerTrickTile(idx, 99999), {x: 4 * 240, y: 9 * 135, w: 240, h: 135})
    ck_Eq("no sheet", PlayerTrickTile(invalid, 5), invalid)
    ck_Eq("bad index", PlayerTrickTile({columns: 0}, 5), invalid)
    ' seeking stays inside the video
    ck_Eq("forward", PlayerSeekTo(100.4, 10, 1800), 110)
    ck_Eq("back past the start", PlayerSeekTo(4, -10, 1800), 0)
    ck_Eq("past the end", PlayerSeekTo(1795, 30, 1800), 1799)
end sub

' T03: what certification checks of deep links. Each media type plays, a blocked id (the home
' answers 404 for missing, another library and hidden alike) opens Home with the line, and voice's
' direct to play (an roInputEvent with a content id) takes the same path as a launch.
sub t_Voice()
    id = "0b5e6f8c-1d2e-4f3a-9b8c-7d6e5f4a3b2c"
    for each mt in ["movie", "episode", "series", "shortFormVideo"]
        ck_Eq("plays " + mt, DeepLinkAction(DeepLinkParse({contentId: id, mediaType: mt}), 200, {id: id}), "play")
    end for
    ck_Eq("a blocked id", DeepLinkAction(DeepLinkParse({contentId: id, mediaType: "movie"}), 404, invalid), "not_available")
    ' voice: Roku's input event carries more than the link (source, type); only the link is read
    info = {contentId: id, mediaType: "movie", source: "voice", type: "transport", extra: "x"}
    link = DeepLinkParse(info)
    ck_True("voice parsed", link <> invalid and link.valid)
    ck_Eq("voice plays", DeepLinkAction(link, 200, {id: id}), "play")
    ck_Eq("voice: a blocked id", DeepLinkAction(link, 404, invalid), "not_available")
end sub
