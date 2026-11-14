' The player's logic (Epic 58 story 58.13), apart from its SceneGraph screen so brs-engine can
' test it (brs-engine has no real Video node): the stream's format, which subtitle shows and who
' draws it, WebVTT cues, Up next, the retry after an error, trickplay tiles, chapters, the queue.

' The Video node's streamFormat for a session: its manifest (HLS, or DASH when the home sends
' DASH, as it does for AV1 on Roku), or its direct-play file's container, read from the direct
' URL or else the video's file name.
function PlayerStreamFormat(session as Object, filename as Dynamic) as String
    mu = player_Str(session.manifest_url)
    if mu <> "" then
        if Instr(1, LCase(player_Path(mu)), ".mpd") > 0 then return "dash"
        return "hls"
    end if
    ext = player_Ext(player_Path(player_Str(session.direct_url)))
    if ext = "" then ext = player_Ext(player_Str(filename))
    if ext = "mkv" or ext = "webm" then return "mkv"
    if ext = "ts" or ext = "m2ts" then return "ts"
    return "mp4"
end function

' The stream's URL: the session's manifest or direct-play file, already signed by the home, so the
' Video node sends no token with media.
function PlayerStreamUrl(session as Object) as String
    mu = player_Str(session.manifest_url)
    if mu <> "" then return mu
    return player_Str(session.direct_url)
end function

' Who draws a subtitle in this language: "own" (the channel's Label layer through VisualText:
' Roku draws Arabic letters unjoined and left to right, research.md §6.4) or "roku" (Roku's own
' renderer, which follows the system caption settings).
function PlayerSubtitleMode(lang as Dynamic) as String
    l = LCase(player_Str(lang))
    if l = "ar" or Left(l, 3) = "ar-" or Left(l, 3) = "ar_" then return "own"
    return "roku"
end function

' The subtitle to show at the start: the one chosen in Maktaba ("off" for none); with nothing
' chosen, Roku's caption setting on picks the track in the person's language if there is one,
' and otherwise the library's default track shows. Returns the track (an item of
' GET /api/videos/{id}/subtitles) or invalid.
function PlayerChooseSubtitle(tracks as Dynamic, chosen as Dynamic, captionsMode as Dynamic, uiLang as String) as Dynamic
    if type(tracks) <> "roArray" then return invalid
    c = player_Str(chosen)
    if c = "off" then return invalid
    if c <> "" then return player_TrackOf(tracks, c)
    if LCase(player_Str(captionsMode)) = "on" then return player_TrackOf(tracks, uiLang)
    for each t in tracks
        if t.is_default = true then return t
    end for
    return invalid
end function

function player_TrackOf(tracks as Object, lang as String) as Dynamic
    for each t in tracks
        if LCase(player_Str(t.language)) = LCase(lang) then return t
    end for
    return invalid
end function

' A track's sidecar WebVTT under the session (/stream/{session}/subs/sidecar/{lang}.vtt), signed
' with the session's stream_sig: Roku's node sends no header (58.02).
function PlayerSubtitleUrl(sessionId as String, lang as String, sig as Dynamic) as String
    u = "/stream/" + sessionId + "/subs/sidecar/" + lang + ".vtt"
    s = player_Str(sig)
    if s <> "" then u = u + "?sig=" + s
    return u
end function

' WebVTT's cues: [{start, end, text}] in seconds, tags and the header left out, a cue's lines
' joined by line feeds.
function VttParse(text as Dynamic) as Object
    cues = []
    if player_Str(text) = "" then return cues
    lines = text.Replace(Chr(13) + Chr(10), Chr(10)).Replace(Chr(13), Chr(10)).Split(Chr(10))
    i = 0
    n = lines.Count()
    while i < n
        ln = lines[i]
        arrow = Instr(1, ln, "-->")
        if arrow > 0 then
            st = VttTime(Left(ln, arrow - 1))
            rest = Mid(ln, arrow + 3).Trim()
            sp = Instr(1, rest, " ")
            if sp > 0 then rest = Left(rest, sp - 1)
            en = VttTime(rest)
            body = []
            i = i + 1
            while i < n and lines[i].Trim() <> ""
                body.Push(player_StripTags(lines[i]))
                i = i + 1
            end while
            if st >= 0 and en > st then cues.Push({start: st, end: en, text: body.Join(Chr(10))})
        end if
        i = i + 1
    end while
    return cues
end function

' A WebVTT time (hh:mm:ss.ttt or mm:ss.ttt) in seconds; -1 when it is none.
function VttTime(s as String) as Float
    s = s.Trim().Replace(",", ".")
    parts = s.Split(":")
    if parts.Count() < 2 or parts.Count() > 3 then return -1
    total = 0.0
    for each p in parts
        if p = "" then return -1
        total = total * 60 + Val(p)
    end for
    return total
end function

' The text showing at t seconds ("" for none); cues that overlap are stacked in order.
function VttCueAt(cues as Object, t as Float) as String
    out = []
    for each c in cues
        if t >= c.start and t < c.end then out.Push(c.text)
    end for
    return out.Join(Chr(10))
end function

' A cue's text for the channel's own layer: each line shaped and put in display order (joined
' letters, right to left), the cue's first line still on top.
function PlayerCueLines(text as String, widthChars as Integer) as String
    if text = "" then return ""
    out = []
    for each ln in text.Split(Chr(10))
        out.Push(VisualText(ln, widthChars))
    end for
    return out.Join(Chr(10))
end function

' Whether the Up next card shows: 15 seconds before the end, when the queue has a next video.
function PlayerUpNextDue(position as Float, duration as Float, hasNext as Boolean) as Boolean
    if not hasNext or duration <= 0 then return false
    return duration - position <= 15
end function

' What the player does when the Video node fails: retry once with force_transcode, then show
' the error with the home's reason and Back.
function PlayerAfterError(retried as Boolean) as String
    if retried then return "fail"
    return "retry"
end function

' The home's reason for a refused session (its problem's detail or title), or "".
function PlayerReason(data as Dynamic) as String
    if type(data) <> "roAssociativeArray" then return ""
    d = player_Str(data.detail)
    if d <> "" then return d
    return player_Str(data.title)
end function

' The queue's next item after index at (-1 at its end).
function PlayerQueueNext(items as Dynamic, at as Integer) as Integer
    if type(items) <> "roArray" then return -1
    if at + 1 < items.Count() then return at + 1
    return -1
end function

' The chapter at t seconds (its title, "" for none) from GET /api/videos/{id}/chapters.
function PlayerChapterAt(chapters as Dynamic, t as Float) as String
    if type(chapters) <> "roArray" then return ""
    title = ""
    for each c in chapters
        if c.start_sec <> invalid and t >= c.start_sec then
            if c.end_sec = invalid or t < c.end_sec then title = player_Str(c.title)
        end if
    end for
    return title
end function

' The trickplay tile for t seconds in GET /api/videos/{id}/trickplay's sheet: {x, y, w, h}, the
' tile's place in the sheet (tile i, row-major, shows the video at i * interval_sec).
function PlayerTrickTile(index as Dynamic, t as Float) as Dynamic
    if type(index) <> "roAssociativeArray" then return invalid
    if index.columns = invalid or index.columns < 1 or index.interval_sec = invalid or index.interval_sec <= 0 then return invalid
    count = index.count
    if count = invalid or count < 1 then count = index.columns * index.rows
    i = Int(t / index.interval_sec)
    if i < 0 then i = 0
    if i > count - 1 then i = count - 1
    col = i mod index.columns
    row = Int(i / index.columns)
    return {x: col * index.tile_width, y: row * index.tile_height, w: index.tile_width, h: index.tile_height}
end function

' A seek from position by step seconds, kept inside the video.
function PlayerSeekTo(position as Float, stepSec as Integer, duration as Float) as Integer
    t = Int(position) + stepSec
    if t < 0 then t = 0
    if duration > 0 and t > duration - 1 then t = Int(duration) - 1
    if t < 0 then t = 0
    return t
end function

' A subtitle language's name in that language (the picker lists each in its own language, as
' Roku's settings do), or its code in capitals for one the table does not name.
function PlayerLanguageName(code as Dynamic) as String
    c = LCase(player_Str(code))
    names = {en: "English", ar: "العربية", fr: "Français", es: "Español", de: "Deutsch", tr: "Türkçe", ur: "اردو", fa: "فارسی", id: "Bahasa Indonesia", ms: "Bahasa Melayu", it: "Italiano", pt: "Português", ru: "Русский", zh: "中文", ja: "日本語", hi: "हिन्दी", bn: "বাংলা", so: "Soomaali", sw: "Kiswahili"}
    base = c
    dash = Instr(1, c, "-")
    if dash > 1 then base = Left(c, dash - 1)
    if names.DoesExist(base) then return names[base]
    return UCase(player_Str(code))
end function

function player_Path(u as String) as String
    q = Instr(1, u, "?")
    if q > 0 then return Left(u, q - 1)
    return u
end function

function player_Ext(path as String) as String
    slash = 0
    for i = Len(path) to 1 step -1
        if Mid(path, i, 1) = "/" then
            slash = i
            exit for
        end if
    end for
    name = Mid(path, slash + 1)
    dot = 0
    for i = Len(name) to 1 step -1
        if Mid(name, i, 1) = "." then
            dot = i
            exit for
        end if
    end for
    if dot = 0 then return ""
    return LCase(Mid(name, dot + 1))
end function

function player_StripTags(s as String) as String
    out = ""
    inTag = false
    for i = 1 to Len(s)
        ch = Mid(s, i, 1)
        if ch = "<" then
            inTag = true
        else if ch = ">" and inTag then
            inTag = false
        else if not inTag then
            out = out + ch
        end if
    end for
    return out.Replace("&lt;", "<").Replace("&gt;", ">").Replace("&nbsp;", " ").Replace("&amp;", "&")
end function

function player_Str(v as Dynamic) as String
    if v = invalid then return ""
    if type(v) = "roString" or type(v) = "String" then return v
    return ""
end function
