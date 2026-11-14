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
' A restricted account's limits (Epic 57; on Roku, story 58.14): the home is the authority, and
' the player only shows what it answered. While a video plays it asks GET /api/me/parental/time
' (every 30 s, every 10 s from 6 minutes left) and shows its remaining_seconds as Android TV
' does: "12 minutes left today" in the end corner once 15 minutes or fewer remain, and "Watching
' ends in 5 minutes." for 10 s as the time crosses five minutes and one minute. A session open or a progress post refused with 403
' parental_time stops the video and shows the time's-up page (why, when watching opens again,
' where it stopped, Ask for more time with 15, 30 or 60 minutes, and Check again); a parent's yes,
' or Check again, opens the session again, and the home decides. no_heartbeat (the lease ran
' out in a long pause) opens the stream again once where it is. A 404 for the stream while it
' plays, or for an open under a limit, hands the screen to "This isn't available" (BlockedScreen),
' whose yes plays the video again from there. 503 parental_time_unknown: never free time on an
' error, the open says the home can't check the time; a progress post so refused keeps playing
' and the next one asks again.

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
    m.timesUp = m.top.FindNode("timesUp")
    m.warnBox = m.top.FindNode("warnBox")
    m.warn = m.top.FindNode("warn")
    m.leftBox = m.top.FindNode("leftBox")
    m.leftNote = m.top.FindNode("leftNote")
    for each id in ["tuTitle", "tuNext", "tuSaved", "tuState", "tuAsk", "tuHint", "tuAsk0", "tuAsk1", "tuAsk2", "tuCheck"]
        m[id] = m.top.FindNode(id)
    end for

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
    ui_Label(m.warn, 34, c.accent, true)
    ui_Label(m.leftNote, 30, c.fg, false)
    m.warn.horizAlign = "center"
    m.leftNote.horizAlign = "center"
    ' in Arabic the bar fills from the right and the buttons start at the right
    ui_Mirror(["title", "meta", "track", "pos", "dur", "audio", "subtitles", "upNext"])
    m.barX = m.track.translation[0]
    m.barW = m.track.width
    m.audioBtn.text = ui_Tr("roku.player.audio")
    m.subsBtn.text = ui_Tr("roku.player.subtitles")
    m.upNextLabel.text = ui_Tr("roku.player.upNext")
    m.backBtn.text = ui_Tr("launcher.back")
    ui_Label(m.tuTitle, 52, c.fg, true)
    ui_Label(m.tuNext, 34, c.fg, false)
    ui_Label(m.tuSaved, 28, c.muted, false)
    ui_Label(m.tuState, 32, c.fg, false)
    ui_Label(m.tuAsk, 32, c.muted, false)
    ui_Label(m.tuHint, 28, c.muted, false)
    ' the page is centred: only the button row mirrors
    for each lbl in [m.tuTitle, m.tuNext, m.tuSaved, m.tuState, m.tuAsk, m.tuHint]
        lbl.horizAlign = "center"
    end for
    m.tuAsk0.text = ui_Tr("roku.parental.ask15")
    m.tuAsk1.text = ui_Tr("roku.parental.ask30")
    m.tuAsk2.text = ui_Tr("roku.parental.ask60")
    m.tuCheck.text = ui_Tr("parental.time.check")

    ' the subtitle chosen in Maktaba lasts for the channel's run ("" none chosen, "off")
    if not m.global.HasField("subChoice") then m.global.AddField("subChoice", "string", false)
    if m.global.subChoice = invalid then m.global.subChoice = ""

    m.caps = CapsFromAnswers(CapsAnswers())
    print "[player] caps "; FormatJson(m.caps)
    m.captionsMode = CreateObject("roDeviceInfo").GetCaptionsMode()

    m.hideTimer = player_Timer(5, false, "onHide")
    m.progressTimer = player_Timer(10, true, "onProgressTick")
    m.seekTimer = player_Timer(0.8, false, "onSeekDone")
    ' a request for more time is followed while the time's-up page shows and it waits
    m.tuPoll = player_Timer(15, true, "onTimePoll")
    ' the time left: asked again when this fires; a warning shows for 10 s
    m.timeTimer = player_Timer(30, false, "onTimeTick")
    m.warnTimer = player_Timer(10, false, "onWarnDone")
    m.clock = CreateObject("roTimespan")

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
    ' time's up: {reason, nextAt, at} while the page shows; the request for more time
    m.timeUp = invalid
    m.tuAskState = "none"
    m.tuWaited = false
    m.tuError = invalid
    m.tuButtons = []
    m.tuFocus = 0
    m.tuCanAsk = false
    m.lapsedAt = -1
    ' the home's last remaining_seconds (-1: no rule now) and the warnings already shown, kept
    ' across a queue's videos
    m.timeLeft = -1
    m.warnings = ParentalWarningsNew()

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
        if refusedByParent(r) then return
        fail(PlayerReason(r.data))
        return
    end if
    if m.timeUp <> invalid then hideTimesUp()
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
    if player_Restricted() then askTimeLeft()
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
    ui_Api("POST", "/api/stream/sessions/" + m.sessionId + "/progress", body, "progress:" + m.sessionId, "onProgressAnswer")
    print "[progress] "; t; " completed="; completed
end sub

sub onProgressSent()
end sub

' The progress post is also the stream's heartbeat for an account with time rules: its answer
' says whether the time is over, the lease lapsed, or the stream is gone.
sub onProgressAnswer(ev as Object)
    r = ev.GetData()
    sid = Mid(ev.GetRoSGNode().tag, Len("progress:") + 1)
    ' a post of a session since closed or replaced says nothing about what plays now
    if m.sessionId = "" or sid <> m.sessionId then return
    answer = ParentalStop(r.status, r.data)
    if answer = "time" then
        timesUp(ParentalTimeUp(r.data), m.lastSent)
    else if answer = "lapse" then
        lapsed(ParentalTimeUp(r.data))
    else if answer = "unknown" then
        ' never free time on an error: the home refuses the stream itself; the next post asks again
        print "[parental] time not checked"
    else if r.status = 404 then
        print "[parental] the home ended the stream"
        blocked(true, m.lastSent)
    end if
end sub

sub closeSession()
    if m.sessionId = "" then return
    ui_Api("DELETE", "/api/stream/sessions/" + m.sessionId, invalid, "close", "onProgressSent")
    m.sessionId = ""
    m.progressTimer.control = "stop"
    m.timeTimer.control = "stop"
end sub

sub showUpNext()
    it = nextItem()
    if it = invalid then return
    m.upNextShown = true
    m.upNextTitle.text = VisualText(CardTitle(it), 30)
    m.upNextPoster.uri = ui_Poster(it.id)
    m.upNext.visible = true
    m.upNextRing.visible = true
    placeTime()
    print "[upnext] shown "; it.id
end sub

sub hideUpNext()
    m.upNext.visible = false
    m.upNextRing.visible = false
    placeTime()
end sub

' --- the Video node's state ----------------------------------------------------------------

sub onState()
    s = m.video.state
    print "[player] state "; s
    ' stopped by the home: the page that says why stays
    if m.timeUp <> invalid then return
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
    text = ui_Tr("roku.player.failed")
    if reason <> "" then text = text + Chr(10) + VisualText(reason, 70)
    showFailure(text, reason)
end sub

sub showFailure(text as String, reason as String)
    m.failed = true
    m.video.control = "stop"
    m.progressTimer.control = "stop"
    m.overlay.visible = false
    hideUpNext()
    hideTime()
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
        m.tuPoll.control = "stop"
        m.timeTimer.control = "stop"
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
    if m.timeUp <> invalid then return timesUpKey(key)
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

' --- parental controls: the home's answers (Epic 57 on Roku, story 58.14) -------------------

function player_Restricted() as Boolean
    p = m.global.session.parental
    return type(p) = "roAssociativeArray" and p.restricted = true
end function

' The home's parental answers to a session open: true when one of them took the screen.
function refusedByParent(r as Object) as Boolean
    answer = ParentalStop(r.status, r.data)
    if answer = "time" or answer = "lapse" then
        timesUp(ParentalTimeUp(r.data), m.start)
        return true
    end if
    if answer = "unknown" then
        print "[parental] time not checked"
        showFailure(ui_Tr("parental.time.cannotCheck"), "parental_time_unknown")
        return true
    end if
    ' a 404 under a limit may be the limit: the blocked page, with a parent to ask
    if r.status = 404 and ParentalLimited(m.global.session.parental) then
        blocked(false, m.start)
        return true
    end if
    return false
end function

' The video, its session and everything over it stop; nothing asks the home again by itself.
sub stopPlayback()
    closeSession()
    m.video.control = "stop"
    m.progressTimer.control = "stop"
    m.hideTimer.control = "stop"
    m.seekTimer.control = "stop"
    m.seekTarget = -1
    m.overlay.visible = false
    m.cues = []
    m.subs.visible = false
    m.picker.visible = false
    m.status.visible = false
    m.backBtn.visible = false
    hideUpNext()
    hideTime()
    m.keys.SetFocus(true)
end sub

' "This isn't available" in this screen's place (Back skips it): stopped when the home ended
' the stream while it played. A parent's yes plays the video again from at.
sub blocked(stopped as Boolean, at as Integer)
    stopPlayback()
    if at < 0 then at = 0
    print "[parental] blocked stopped="; stopped; " at="; at
    ui_Navigate({to: "BlockedScreen", replace: true, params: {kind: "video", id: m.videoId, stopped: stopped, start: at}})
end sub

' The lease lapsed (no progress for a while, a long pause): opened again once, where it is. A
' lapse again soon after that stops the video for time.
sub lapsed(t as Object)
    now = m.clock.TotalMilliseconds()
    if m.lapsedAt >= 0 and now - m.lapsedAt < 30000 then
        timesUp(t, m.lastSent)
        return
    end if
    m.lapsedAt = now
    print "[parental] lease lapsed; opening again"
    m.start = Int(m.video.position)
    closeSession()
    openSession(false)
end sub

' Time's up: the video stops at at, and the page says why. The home is asked again only by
' Check again or a parent's yes.
sub timesUp(t as Object, at as Integer)
    if at < 0 then at = 0
    first = (m.timeUp = invalid)
    stopPlayback()
    m.failed = false
    m.timeUp = {reason: t.reason, nextAt: t.nextAt, at: at}
    if first then
        m.tuAskState = "none"
        m.tuWaited = false
        m.tuError = invalid
    end if
    print "[parental] times up reason="; t.reason; " at="; at
    drawTimesUp()
    m.timesUp.visible = true
    ' a request made earlier (here or on another device) is followed as it stands
    if first and player_Restricted() then readTimeRequests()
    print "[screen] PlayerScreen timesup reason="; t.reason
end sub

sub hideTimesUp()
    m.timeUp = invalid
    m.timesUp.visible = false
    m.tuPoll.control = "stop"
    m.keys.SetFocus(true)
    print "[parental] times up closed"
end sub

sub drawTimesUp()
    t = m.timeUp
    if t = invalid then return
    m.tuTitle.text = ui_Tr(ParentalTimeTitleKey(t.reason))
    d = CreateObject("roDateTime")
    nowSec = d.AsSeconds()
    d.ToLocalTime()
    words = ParentalNextWords(t.nextAt, nowSec, d.AsSeconds() - nowSec, m.i18n)
    m.tuNext.text = ""
    if words <> "" then m.tuNext.text = m.i18n.Format("parental.time.next", [words])
    m.tuSaved.text = ""
    if t.at >= 1 then m.tuSaved.text = m.i18n.Format("parental.time.saved", [FormatTime(t.at)])

    restricted = player_Restricted()
    canAsk = restricted and m.tuAskState <> "pending" and m.tuAskState <> "approved"
    m.tuState.text = ""
    if m.tuAskState = "pending" then m.tuState.text = ui_Tr("parental.ask.sentTime")
    if m.tuAskState = "approved" then m.tuState.text = ui_Tr("parental.ask.approved")
    if m.tuAskState = "denied" then m.tuState.text = ui_Tr("parental.ask.denied")
    if m.tuError <> invalid then m.tuState.text = m.tuError
    m.tuAsk.text = ""
    if canAsk and m.tuAskState = "denied" then
        m.tuAsk.text = ui_Tr("parental.ask.again")
    else if canAsk then
        m.tuAsk.text = ui_Tr("parental.time.askMore")
    end if
    m.tuHint.text = ""
    if restricted then m.tuHint.text = ui_Tr("tv.parental.askOnPhone")

    ' the buttons in reading order: the first at the right in Arabic
    if canAsk then
        m.tuButtons = [m.tuAsk0, m.tuAsk1, m.tuAsk2, m.tuCheck]
    else
        m.tuButtons = [m.tuCheck]
    end if
    ' the main action takes the focus: the first minutes to ask for, or Check again once asked
    if canAsk <> m.tuCanAsk or m.tuFocus >= m.tuButtons.Count() then m.tuFocus = 0
    m.tuCanAsk = canAsk
    for each b in [m.tuAsk0, m.tuAsk1, m.tuAsk2, m.tuCheck]
        b.visible = false
    end for
    n = m.tuButtons.Count()
    w = 300
    gap = 30
    x0 = Int((1920 - (n * w + (n - 1) * gap)) / 2)
    for i = 0 to n - 1
        b = m.tuButtons[i]
        b.translation = [m.layout.X(x0 + i * (w + gap), w), 690]
        b.visible = true
    end for
    drawTimesUpFocus()
    if m.tuAskState = "pending" and m.top.visible then m.tuPoll.control = "start" else m.tuPoll.control = "stop"
end sub

sub drawTimesUpFocus()
    for i = 0 to m.tuButtons.Count() - 1
        m.tuButtons[i].focused = (i = m.tuFocus)
    end for
    print "[focus] timesup "; m.tuButtons[m.tuFocus].id
end sub

function timesUpKey(key as String) as Boolean
    ' Back leaves the player: the session is already closed
    if key = "back" then
        print "[player] back"
        return false
    end if
    k = m.layout.Key(key)
    if k = "right" and m.tuFocus < m.tuButtons.Count() - 1 then
        m.tuFocus = m.tuFocus + 1
        drawTimesUpFocus()
    else if k = "left" and m.tuFocus > 0 then
        m.tuFocus = m.tuFocus - 1
        drawTimesUpFocus()
    else if key = "OK" or key = "play" then
        b = m.tuButtons[m.tuFocus]
        if b.id = "tuCheck" then
            checkAgain()
        else
            askTime(ParentalAskMinutes()[m.tuFocus])
        end if
    end if
    return true
end function

' The same video again, from where it stopped: the home answers as it does any open.
sub checkAgain()
    if m.timeUp = invalid then return
    m.tuError = invalid
    m.start = m.timeUp.at
    m.retried = false
    m.lapsedAt = -1
    print "[parental] check again at "; m.start
    openSession(false)
end sub

sub askTime(minutes as Integer)
    m.tuError = invalid
    print "[parental] ask time "; minutes
    ui_Api("POST", "/api/me/parental/requests", {subject_kind: "time", subject_id: minutes.ToStr()}, "ask", "onTimeAsked")
end sub

sub onTimeAsked(ev as Object)
    if m.timeUp = invalid then return
    r = ev.GetData()
    if r.status = 200 or r.status = 201 then
        m.tuAskState = "pending"
        m.tuWaited = true
        print "[parental] asked time"
    else if r.status = 429 then
        m.tuError = ui_Tr("parental.ask.tooMany")
    else
        m.tuError = ui_Tr("parental.ask.failed")
    end if
    drawTimesUp()
end sub

sub readTimeRequests()
    ui_Api("GET", "/api/me/parental/requests", invalid, "requests", "onTimeRequests")
end sub

sub onTimePoll()
    readTimeRequests()
end sub

sub onTimeRequests(ev as Object)
    if m.timeUp = invalid then return
    r = ev.GetData()
    if r.status <> 200 then return
    state = ParentalRequestState(r.data, "time", "")
    if state = "pending" then m.tuWaited = true
    ' a yes opens the session again, once, when this page saw the request wait: the home decides
    if state = "approved" and m.tuWaited then
        m.tuAskState = "approved"
        m.tuWaited = false
        print "[parental] approved time"
        drawTimesUp()
        checkAgain()
        return
    end if
    if state = "approved" or state = "expired" then state = "none"
    m.tuAskState = state
    print "[parental] times up ask="; state
    drawTimesUp()
end sub

' --- the time left (Epic 57 story 57.15 on Roku, Android TV's TvTimeOverlay) -----------------

sub askTimeLeft()
    lib = invalid
    if m.info <> invalid then lib = m.info.library_id
    ui_Api("GET", ParentalTimePath(lib), invalid, "time:" + m.sessionId, "onTimeLeft")
end sub

sub onTimeTick()
    if m.sessionId <> "" and m.timeUp = invalid then askTimeLeft()
end sub

' The home's answer: the note, a warning newly crossed, and when to ask next. An answer that
' does not come keeps the last one (the stream itself is refused when the time is over).
sub onTimeLeft(ev as Object)
    sid = Mid(ev.GetRoSGNode().tag, Len("time:") + 1)
    if m.sessionId = "" or sid <> m.sessionId or m.timeUp <> invalid or m.failed then return
    r = ev.GetData()
    if r.status = 200 then
        m.timeLeft = ParentalTimeLeft(r.data)
        line = ParentalWarningCrossed(m.warnings, m.timeLeft)
        if line > 0 then showWarning(ParentalWarningSeconds(line, m.timeLeft))
    else
        print "[parental] time not read "; r.status
    end if
    drawTimeLeft()
    m.timeTimer.duration = ParentalPollSec(m.timeLeft)
    m.timeTimer.control = "start"
    print "[parental] time left="; m.timeLeft; " poll="; m.timeTimer.duration
end sub

sub drawTimeLeft()
    shown = ParentalShowsLeft(m.timeLeft)
    if shown then m.leftNote.text = m.i18n.Format("parental.time.left", [ParentalTimeWords(m.timeLeft, m.i18n)])
    m.leftNote.visible = shown
    m.leftBox.visible = shown
    placeTime()
end sub

sub showWarning(seconds as Integer)
    m.warn.text = m.i18n.Format("parental.time.warning", [ParentalTimeWords(seconds, m.i18n)])
    m.warn.visible = true
    m.warnBox.visible = true
    m.warnTimer.control = "stop"
    m.warnTimer.control = "start"
    placeTime()
    print "[parental] warning "; seconds
end sub

sub onWarnDone()
    m.warn.visible = false
    m.warnBox.visible = false
    placeTime()
end sub

sub hideTime()
    m.timeTimer.control = "stop"
    m.warnTimer.control = "stop"
    for each n in [m.warn, m.warnBox, m.leftNote, m.leftBox]
        n.visible = false
    end for
end sub

' The end corner (the right in English, the left in Arabic): the warning over the note, both
' below Up next while it shows.
sub placeTime()
    w = 640
    x = m.layout.X(1920 - 80 - w, w)
    y = 48
    if m.upNext.visible then y = 440
    if m.warn.visible then
        m.warnBox.translation = [x, y]
        m.warn.translation = [x, y]
        y = y + 76
    end if
    m.leftBox.translation = [x, y]
    m.leftNote.translation = [x, y]
end sub
