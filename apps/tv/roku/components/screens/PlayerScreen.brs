' The player (Epic 58 story 58.13). One video, or the server queue (Play all, Shuffle: Epic 56),
' opened as a stream session for Roku: client_profile roku with this Roku's device_caps (58.02),
' so the home sends what it plays (direct, remuxed into HLS or DASH, or transcoded). The media
' URLs are signed: the Video node needs only the scene's certificate bundle.
'
' Maktaba's overlay replaces the node's own trick-play bar: title, series and episode, position,
' chapter, scrub thumbnails from the home's trickplay sheet, the audio and subtitle pickers, and
' Up next 15 seconds before the end. An Arabic subtitle is drawn by the channel's own Label layer
' (Roku draws Arabic unjoined, left to right); other languages by Roku's renderer, which follows
' the system caption settings. Progress goes to the home every 10 seconds and on pause, stop and
' Back. A Video error retries once with force_transcode; a second shows the error and Back.
'
' Story 58.14 (parental controls on TVs) adds its answers to a refused session or progress here.

sub init()
    ui_Init()
    c = ui_Colors()
    m.video = m.top.FindNode("video")
    m.subs = m.top.FindNode("subs")
    m.overlay = m.top.FindNode("overlay")
    m.title = m.top.FindNode("title")
    m.meta = m.top.FindNode("meta")
    m.thumb = m.top.FindNode("thumb")
    m.sheet = m.top.FindNode("sheet")
    m.thumbClip = m.top.FindNode("thumbClip")
    m.thumbFrame = m.top.FindNode("thumbFrame")
    m.track = m.top.FindNode("track")
    m.fill = m.top.FindNode("fill")
    m.pos = m.top.FindNode("pos")
    m.dur = m.top.FindNode("dur")
    m.audioBtn = m.top.FindNode("audio")
    m.subsBtn = m.top.FindNode("subtitles")
    m.upNext = m.top.FindNode("upNext")
    m.upNextRing = m.top.FindNode("upNextRing")
    m.upNextPoster = m.top.FindNode("upNextPoster")
    m.upNextLabel = m.top.FindNode("upNextLabel")
    m.upNextTitle = m.top.FindNode("upNextTitle")
    m.status = m.top.FindNode("status")
    m.backBtn = m.top.FindNode("back")
    m.picker = m.top.FindNode("picker")
    m.keys = m.top.FindNode("keys")

    ui_Label(m.subs, 46, c.fg, true)
    m.subs.horizAlign = "center"
    ui_Label(m.title, 44, c.fg, true)
    ui_Label(m.meta, 30, c.muted, false)
    ui_Label(m.pos, 30, c.fg, false)
    ui_Label(m.dur, 30, c.fg, false)
    m.dur.horizAlign = m.layout.Align("right")
    ui_Label(m.upNextLabel, 28, c.accent, true)
    ui_Label(m.upNextTitle, 32, c.fg, true)
    ui_Label(m.status, 40, c.fg, false)
    m.status.horizAlign = "center"
    ' in Arabic the bar fills from the right and the buttons start at the right
    ui_Mirror(["title", "meta", "track", "pos", "dur", "audio", "subtitles", "upNext"])
    m.barX = m.track.translation[0]
    m.barW = m.track.width
    m.audioBtn.text = ui_Tr("roku.player.audio")
    m.subsBtn.text = ui_Tr("roku.player.subtitles")
    m.upNextLabel.text = ui_Tr("roku.player.upNext")
    m.backBtn.text = ui_Tr("launcher.back")

    ' the subtitle chosen in Maktaba lasts for the channel's run ("" none chosen, "off")
    if not m.global.HasField("subChoice") then m.global.AddField("subChoice", "string", false)
    if m.global.subChoice = invalid then m.global.subChoice = ""

    m.caps = CapsFromAnswers(CapsAnswers())
    print "[player] caps "; FormatJson(m.caps)
    m.captionsMode = CreateObject("roDeviceInfo").GetCaptionsMode()

    m.hideTimer = player_Timer(5, false, "onHide")
    m.progressTimer = player_Timer(10, true, "onProgressTick")
    m.seekTimer = player_Timer(0.8, false, "onSeekDone")

    m.queue = invalid
    m.qAt = -1
    m.sessionId = ""
    m.retried = false
    m.failed = false
    m.focus = "bar"
    m.cues = []
    m.ownSubs = false
    m.trick = invalid
    m.chapters = invalid
    m.duration = 0
    m.seekTarget = -1
    m.upNextShown = false
    m.lastSent = -1

    m.video.ObserveField("state", "onState")
    m.video.ObserveField("position", "onPosition")
    m.video.ObserveField("duration", "onDuration")
    m.picker.ObserveField("choice", "onPicked")
    m.top.ObserveField("params", "onParams")
    m.top.ObserveField("takeFocus", "onTakeFocus")
    m.top.ObserveField("visible", "onVisible")
end sub

function player_Timer(seconds as Float, repeat as Boolean, callback as String) as Object
    t = m.top.CreateChild("Timer")
    t.duration = seconds
    t.repeat = repeat
    t.ObserveField("fire", callback)
    return t
end function

sub onParams()
    p = m.top.params
    if p.queue <> invalid then
        ui_Api("POST", "/api/me/play-queues", {source: p.queue, shuffle: (p.shuffle = true)}, "queue", "onQueue")
    else
        open(p.videoId, p.start)
    end if
end sub

' --- the queue -----------------------------------------------------------------------------

sub onQueue(ev as Object)
    r = ev.GetData()
    if (r.status <> 200 and r.status <> 201) or r.data = invalid or type(r.data.items) <> "roArray" or r.data.items.Count() = 0 then
        fail(PlayerReason(r.data))
        return
    end if
    takeQueue(r.data)
    print "[player] queue "; m.queue.id; " at "; m.qAt; " of "; m.queue.items.Count()
    open(m.queue.items[m.qAt].id, 0)
end sub

sub takeQueue(q as Object)
    m.queue = q
    m.qAt = 0
    if q.current_pos <> invalid then
        for i = 0 to q.items.Count() - 1
            if q.items[i].pos = q.current_pos then m.qAt = i
        end for
    end if
end sub

function nextItem() as Dynamic
    if m.queue = invalid then return invalid
    n = PlayerQueueNext(m.queue.items, m.qAt)
    if n < 0 then return invalid
    return m.queue.items[n]
end function

' Moves the server queue to its next item (PUT …/current) and plays it.
sub playNext()
    it = nextItem()
    if it = invalid then
        leave()
        return
    end if
    sendProgress(false)
    closeSession()
    m.video.control = "stop"
    hideUpNext()
    print "[player] next "; it.id
    ui_Api("PUT", "/api/me/play-queues/" + m.queue.id + "/current", {pos: it.pos, version: m.queue.version}, "move", "onMoved")
end sub

sub onMoved(ev as Object)
    r = ev.GetData()
    if (r.status = 200 or r.status = 409) and r.data <> invalid and type(r.data.items) = "roArray" and r.data.items.Count() > 0 then
        was = m.queue.items[m.qAt].id
        takeQueue(r.data)
        ' a stale queue (409) answers with where it is now: play its next item from there
        if r.status = 409 and m.queue.items[m.qAt].id = was then
            n = PlayerQueueNext(m.queue.items, m.qAt)
            if n < 0 then
                leave()
                return
            end if
            m.qAt = n
        end if
        open(m.queue.items[m.qAt].id, 0)
    else
        fail(PlayerReason(r.data))
    end if
end sub

' --- opening a stream ----------------------------------------------------------------------

sub open(videoId as String, start as Dynamic)
    m.videoId = videoId
    m.start = 0
    if start <> invalid then m.start = start
    m.retried = false
    m.failed = false
    m.cues = []
    m.trick = invalid
    m.chapters = invalid
    m.tracks = []
    m.upNextShown = false
    m.lastSent = -1
    m.info = invalid
    m.status.visible = false
    m.backBtn.visible = false
    ui_Api("GET", "/api/videos/" + videoId, invalid, "video", "onVideo")
    ui_Api("GET", "/api/videos/" + videoId + "/subtitles", invalid, "subtitles", "onSubtitleList")
    ui_Api("GET", "/api/videos/" + videoId + "/chapters", invalid, "chapters", "onChapters")
    ui_Api("GET", "/api/videos/" + videoId + "/trickplay", invalid, "trickplay", "onTrickplay")
    openSession(false)
end sub

sub openSession(forceTranscode as Boolean)
    body = {video_id: m.videoId, client_profile: "roku", device_caps: m.caps}
    if m.start > 0 then body.start_sec = m.start
    if forceTranscode then body.force_transcode = true
    ui_Api("POST", "/api/stream/sessions", body, "session", "onSession")
end sub

sub onVideo(ev as Object)
    r = ev.GetData()
    if r.status <> 200 or r.data = invalid then return
    v = r.data
    m.info = v
    m.title.text = VisualText(CardTitle(v), 60)
    meta = []
    if type(v.series) = "roAssociativeArray" and v.series.name <> invalid then meta.Push(VisualText(v.series.name, 40))
    m.metaBase = joinParts(meta)
    m.meta.text = m.metaBase
    if m.video.content <> invalid then m.video.content.title = CardTitle(v)
end sub

sub onSession(ev as Object)
    r = ev.GetData()
    if (r.status <> 200 and r.status <> 201) or r.data = invalid or PlayerStreamUrl(r.data) = "" then
        print "[player] session refused "; r.status
        fail(PlayerReason(r.data))
        return
    end if
    d = r.data
    m.session = d
    m.sessionId = d.session_id
    if d.duration_sec <> invalid and d.duration_sec > 0 then m.duration = d.duration_sec
    filename = invalid
    if m.info <> invalid then filename = m.info.filename
    c = CreateObject("roSGNode", "ContentNode")
    c.url = NetJoin(m.global.session.base, PlayerStreamUrl(d))
    c.streamFormat = PlayerStreamFormat(d, filename)
    if m.info <> invalid then c.title = CardTitle(m.info)
    m.content = c
    applySubtitle()
    m.video.content = c
    ' a transcode opened at a position starts there; direct play and a remux seek
    if m.start > 0 and (d.start_sec = invalid or d.start_sec = 0) then m.video.seek = m.start
    m.video.control = "play"
    m.progressTimer.control = "start"
    print "[player] "; d.mode; " "; c.streamFormat
    print "[screen] PlayerScreen ready mode="; d.mode; " format="; c.streamFormat
    showOverlay()
    drawBar(m.start)
    checkUpNext(m.start)
end sub

sub onChapters(ev as Object)
    r = ev.GetData()
    if r.status = 200 and r.data <> invalid then m.chapters = r.data.items
end sub

sub onTrickplay(ev as Object)
    r = ev.GetData()
    if r.status <> 200 or r.data = invalid or r.data.sprite_url = invalid then return
    m.trick = r.data
    m.sheet.uri = NetJoin(m.global.session.base, r.data.sprite_url)
    m.sheet.width = r.data.sheet_width
    m.sheet.height = r.data.sheet_height
    w = r.data.tile_width
    h = r.data.tile_height
    m.thumbClip.clippingRect = [0, 0, w, h]
    m.thumbFrame.width = w + 8
    m.thumbFrame.height = h + 8
    m.thumb.translation = [m.barX, m.track.translation[1] - h - 30]
    print "[player] trickplay "; r.data.count; " tiles"
end sub

' --- subtitles -----------------------------------------------------------------------------

sub onSubtitleList(ev as Object)
    r = ev.GetData()
    m.tracks = []
    if r.status = 200 and r.data <> invalid and type(r.data.items) = "roArray" then m.tracks = r.data.items
    if m.content <> invalid then applySubtitle()
end sub

' Shows the subtitle chosen (or Roku's caption setting's, or the library's default): Arabic in
' the channel's own layer, Roku's caption renderer off; any other language through Roku's.
sub applySubtitle()
    if m.sessionId = "" then return
    t = PlayerChooseSubtitle(m.tracks, m.global.subChoice, m.captionsMode, m.i18n.lang)
    m.cues = []
    m.subs.text = ""
    m.subs.visible = false
    m.ownSubs = false
    if t = invalid then
        m.video.globalCaptionMode = "Off"
        m.subLang = ""
        print "[subs] off"
        return
    end if
    m.subLang = t.language
    path = PlayerSubtitleUrl(m.sessionId, t.language, m.session.stream_sig)
    if PlayerSubtitleMode(t.language) = "own" then
        m.ownSubs = true
        m.video.globalCaptionMode = "Off"
        if m.content <> invalid then m.content.SubtitleTracks = []
        req = CreateObject("roSGNode", "ApiRequest")
        req.method = "GET"
        req.path = path
        req.tag = "vtt"
        req.raw = true
        req.ObserveFieldScoped("response", "onVtt")
        m.global.api.request = req
        print "[subs] own "; t.language
    else
        if m.content <> invalid then m.content.SubtitleTracks = [{Language: t.language, TrackName: NetJoin(m.global.session.base, path), Description: t.language}]
        m.video.globalCaptionMode = "On"
        m.video.subtitleTrack = NetJoin(m.global.session.base, path)
        print "[subs] roku "; t.language
    end if
end sub

sub onVtt(ev as Object)
    r = ev.GetData()
    if r.status <> 200 or r.text = invalid then
        print "[subs] not loaded "; r.status
        return
    end if
    m.cues = VttParse(r.text)
    print "[subs] cues "; m.cues.Count()
    drawCue(m.video.position)
end sub

sub drawCue(t as Float)
    if not m.ownSubs then return
    ' a cue line is cut (with an ellipsis) only past what the layer holds at this size
    text = PlayerCueLines(VttCueAt(m.cues, t), 64)
    if text <> m.subs.text then
        m.subs.text = text
        if text <> "" then print "[subs] cue at "; Int(t)
    end if
    m.subs.visible = (text <> "")
end sub

' --- position, progress, Up next -----------------------------------------------------------

sub onDuration()
    if m.video.duration > 0 then m.duration = m.video.duration
    drawBar(m.video.position)
end sub

sub onPosition()
    t = m.video.position
    drawCue(t)
    if m.seekTarget < 0 then drawBar(t)
    checkUpNext(t)
end sub

sub checkUpNext(t as Float)
    if not m.upNextShown and PlayerUpNextDue(t, m.duration, nextItem() <> invalid) then showUpNext()
end sub

sub drawBar(t as Float)
    d = m.duration
    m.pos.text = FormatTime(Int(t))
    if d > 0 then m.dur.text = FormatTime(Int(d)) else m.dur.text = ""
    w = 0
    if d > 0 then w = Int(m.barW * t / d)
    if w > m.barW then w = m.barW
    if w < 0 then w = 0
    m.fill.width = w
    if m.rtl then
        m.fill.translation = [m.barX + m.barW - w, m.track.translation[1]]
    else
        m.fill.translation = [m.barX, m.track.translation[1]]
    end if
    chapter = PlayerChapterAt(m.chapters, t)
    if chapter <> "" then
        parts = []
        if m.metaBase <> invalid and m.metaBase <> "" then parts.Push(m.metaBase)
        parts.Push(VisualText(chapter, 40))
        m.meta.text = joinParts(parts)
    end if
end sub

' Parts of a line, each already visual, in reading order: Roku draws a line left to right, so in
' Arabic the first part goes at the right end.
function joinParts(parts as Object) as String
    if m.rtl then parts = m.layout.Order(parts)
    return parts.Join("  ·  ")
end function

sub onProgressTick()
    if m.video.state = "playing" then sendProgress(false)
end sub

' POST /api/stream/sessions/{id}/progress; completed at the end.
sub sendProgress(completed as Boolean, at = -1 as Integer)
    if m.sessionId = "" then return
    t = at
    if t < 0 then t = Int(m.video.position)
    if not completed and t = m.lastSent then return
    m.lastSent = t
    body = {position_sec: t}
    if completed then body.completed = true
    ui_Api("POST", "/api/stream/sessions/" + m.sessionId + "/progress", body, "progress", "onProgressSent")
    print "[progress] "; t; " completed="; completed
end sub

sub onProgressSent()
end sub

sub closeSession()
    if m.sessionId = "" then return
    ui_Api("DELETE", "/api/stream/sessions/" + m.sessionId, invalid, "close", "onProgressSent")
    m.sessionId = ""
    m.progressTimer.control = "stop"
end sub

sub showUpNext()
    it = nextItem()
    if it = invalid then return
    m.upNextShown = true
    m.upNextTitle.text = VisualText(CardTitle(it), 30)
    m.upNextPoster.uri = ui_Poster(it.id)
    m.upNext.visible = true
    m.upNextRing.visible = true
    print "[upnext] shown "; it.id
end sub

sub hideUpNext()
    m.upNext.visible = false
    m.upNextRing.visible = false
end sub

' --- the Video node's state ----------------------------------------------------------------

sub onState()
    s = m.video.state
    print "[player] state "; s
    if s = "paused" then
        sendProgress(false)
        showOverlay()
    else if s = "finished" then
        sendProgress(true)
        if nextItem() <> invalid then playNext() else leave()
    else if s = "error" then
        print "[player] video error "; m.video.errorCode; " "; m.video.errorMsg
        if PlayerAfterError(m.retried) = "retry" then
            m.retried = true
            m.status.text = ui_Tr("roku.player.retrying")
            m.status.visible = true
            m.start = Int(m.video.position)
            if m.start = 0 and m.top.params.start <> invalid then m.start = m.top.params.start
            closeSession()
            print "[player] retry force_transcode"
            openSession(true)
        else
            fail("")
        end if
    else if s = "playing" then
        m.status.visible = false
    end if
end sub

' The error with the home's reason (if it gave one) and Back.
sub fail(reason as String)
    m.failed = true
    m.video.control = "stop"
    m.progressTimer.control = "stop"
    m.overlay.visible = false
    hideUpNext()
    text = ui_Tr("roku.player.failed")
    if reason <> "" then text = text + Chr(10) + VisualText(reason, 70)
    m.status.text = text
    m.status.visible = true
    m.backBtn.visible = true
    m.backBtn.focused = true
    print "[player] failed reason="; reason
    print "[screen] PlayerScreen failed"
end sub

' --- the overlay and the remote --------------------------------------------------------------

sub showOverlay()
    if m.failed then return
    m.overlay.visible = true
    m.subs.translation = [m.subs.translation[0], 520]
    drawFocus()
    m.hideTimer.control = "stop"
    if m.video.state <> "paused" then m.hideTimer.control = "start"
end sub

sub onHide()
    if m.picker.visible or m.video.state = "paused" or m.focus <> "bar" then return
    m.overlay.visible = false
    m.thumb.visible = false
    m.title.visible = true
    m.meta.visible = true
    m.subs.translation = [m.subs.translation[0], 860]
    print "[player] overlay hidden"
end sub

sub drawFocus()
    m.audioBtn.focused = (m.focus = "audio")
    m.subsBtn.focused = (m.focus = "subtitles")
    print "[focus] player "; m.focus
end sub

' A seek of step seconds from where the bar is: the thumbnail and the time move at once, the
' Video node seeks once the keys stop.
sub seekBy(stepSec as Integer)
    from = m.video.position
    if m.seekTarget >= 0 then from = m.seekTarget
    m.seekTarget = PlayerSeekTo(from, stepSec, m.duration)
    drawBar(m.seekTarget)
    tile = PlayerTrickTile(m.trick, m.seekTarget)
    if tile <> invalid then
        m.sheet.translation = [-tile.x, -tile.y]
        x = 0
        if m.duration > 0 then x = Int(m.barW * m.seekTarget / m.duration)
        if m.rtl then x = m.barW - x
        x = m.barX + x - Int(tile.w / 2)
        if x < 40 then x = 40
        if x > 1880 - tile.w then x = 1880 - tile.w
        m.thumb.translation = [x, m.thumb.translation[1]]
        m.thumb.visible = true
        ' the thumbnail stands where the title is while it shows
        m.title.visible = false
        m.meta.visible = false
    end if
    m.seekTimer.control = "stop"
    m.seekTimer.control = "start"
    print "[seek] "; m.seekTarget
end sub

sub onSeekDone()
    if m.seekTarget < 0 then return
    m.video.seek = m.seekTarget
    print "[seek] to "; m.seekTarget
    t = m.seekTarget
    m.seekTarget = -1
    m.thumb.visible = false
    m.title.visible = true
    m.meta.visible = true
    sendProgress(false, t)
end sub

sub openPicker(kind as String)
    m.pickerKind = kind
    opts = []
    sel = 0
    m.pickerValues = []
    if kind = "audio" then
        m.picker.title = ui_Tr("roku.player.audio")
        tracks = m.video.availableAudioTracks
        if type(tracks) <> "roArray" then tracks = []
        i = 0
        for each a in tracks
            name = ""
            if a.Name <> invalid and a.Name <> "" then name = a.Name
            if name = "" and a.Language <> invalid then name = a.Language
            if name = "" then name = m.i18n.Format("roku.player.track", [(i + 1).ToStr()])
            opts.Push(VisualText(name, 30))
            m.pickerValues.Push(a.Track)
            if a.Track = m.video.currentAudioTrack then sel = i
            i = i + 1
        end for
        if opts.Count() = 0 then
            opts.Push(m.i18n.Format("roku.player.track", ["1"]))
            m.pickerValues.Push("")
        end if
    else
        m.picker.title = ui_Tr("roku.player.subtitles")
        opts.Push(ui_Tr("roku.player.off"))
        m.pickerValues.Push("off")
        i = 1
        for each t in m.tracks
            opts.Push(VisualText(PlayerLanguageName(t.language), 30))
            m.pickerValues.Push(t.language)
            if t.language = m.subLang then sel = i
            i = i + 1
        end for
    end if
    m.picker.selected = sel
    m.picker.options = opts
    m.picker.visible = true
    m.picker.SetFocus(true)
    m.hideTimer.control = "stop"
    print "[picker] "; kind; " "; opts.Count()
end sub

sub onPicked()
    i = m.picker.choice
    m.picker.visible = false
    m.keys.SetFocus(true)
    if i >= 0 and i < m.pickerValues.Count() then
        v = m.pickerValues[i]
        if m.pickerKind = "audio" then
            if v <> "" then m.video.audioTrack = v
            print "[picker] audio "; v
        else
            m.global.subChoice = v
            print "[picker] subtitles "; v
            applySubtitle()
        end if
    end if
    showOverlay()
end sub

sub onTakeFocus()
    m.keys.SetFocus(true)
end sub

sub onVisible()
    if not m.top.visible then
        m.video.control = "stop"
        m.progressTimer.control = "stop"
    end if
end sub

' Back: the position goes to the home, the session closes, the screen goes.
sub leave()
    sendProgress(false)
    closeSession()
    m.video.control = "stop"
    ui_Navigate({action: "back"})
end sub

function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    print "[key] player "; key; " focus="; m.focus; " overlay="; m.overlay.visible
    if m.failed then
        if key = "OK" then
            ui_Navigate({action: "back"})
            return true
        end if
        return false
    end if
    if key = "back" then
        if m.overlay.visible and m.focus <> "bar" then
            m.focus = "bar"
            drawFocus()
            return true
        end if
        sendProgress(false)
        closeSession()
        m.video.control = "stop"
        print "[player] back"
        return false
    end if
    k = m.layout.Key(key)
    if m.upNext.visible and (key = "OK" or key = "play") and not m.overlay.visible then
        playNext()
        return true
    end if
    if key = "play" then
        if m.video.state = "paused" then
            m.video.control = "resume"
        else
            m.video.control = "pause"
        end if
        showOverlay()
        return true
    end if
    if key = "fastforward" then
        showOverlay()
        seekBy(30)
        return true
    end if
    if key = "rewind" then
        showOverlay()
        seekBy(-30)
        return true
    end if
    if not m.overlay.visible then
        ' Left and Right seek at once, the bar shown; any other key only shows the overlay
        m.focus = "bar"
        showOverlay()
        if k = "right" then seekBy(10)
        if k = "left" then seekBy(-10)
        return true
    end if
    if m.focus = "bar" then
        if k = "right" then
            seekBy(10)
        else if k = "left" then
            seekBy(-10)
        else if key = "down" then
            m.focus = "audio"
        else if key = "OK" then
            if m.upNext.visible then
                playNext()
                return true
            end if
            if m.video.state = "paused" then m.video.control = "resume" else m.video.control = "pause"
        end if
    else
        if key = "up" then
            m.focus = "bar"
        else if k = "right" and m.focus = "audio" then
            m.focus = "subtitles"
        else if k = "left" and m.focus = "subtitles" then
            m.focus = "audio"
        else if key = "OK" then
            openPicker(m.focus)
            return true
        end if
    end if
    showOverlay()
    return true
end function
