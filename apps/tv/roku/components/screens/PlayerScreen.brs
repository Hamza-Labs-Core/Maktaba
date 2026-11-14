' Playback (story 58.12): the server queue's first item or one video, opened as a stream
' session for Roku (client_profile roku) in a Video node. The media URLs are signed, so the
' node needs only the scene's certificate bundle. Story 58.13 builds the player on this:
' overlay, device_caps, subtitles, progress reports, Up next.

sub init()
    ui_Init()
    m.video = m.top.FindNode("video")
    m.error = m.top.FindNode("error")
    ui_Label(m.error, 36, ui_Colors().fg, false)
    m.video.ObserveField("state", "onState")
    m.top.ObserveField("params", "onParams")
    m.top.ObserveField("takeFocus", "onTakeFocus")
    m.top.ObserveField("visible", "onVisible")
end sub

sub onParams()
    p = m.top.params
    if p.queue <> invalid then
        ui_Api("POST", "/api/me/play-queues", {source: p.queue, shuffle: (p.shuffle = true)}, "queue", "onQueue")
    else
        open(p.videoId, p.start)
    end if
end sub

sub onQueue(ev as Object)
    r = ev.GetData()
    if (r.status <> 200 and r.status <> 201) or r.data = invalid or type(r.data.items) <> "roArray" or r.data.items.Count() = 0 then
        fail()
        return
    end if
    at = 0
    if r.data.current_pos <> invalid then
        for i = 0 to r.data.items.Count() - 1
            if r.data.items[i].pos = r.data.current_pos then at = i
        end for
    end if
    it = r.data.items[at]
    print "[player] queue "; r.data.id; " at "; at
    open(it.id, 0)
end sub

sub open(videoId as String, start as Dynamic)
    m.start = 0
    if start <> invalid then m.start = start
    body = {video_id: videoId, client_profile: "roku"}
    if m.start > 0 then body.start_sec = m.start
    ui_Api("POST", "/api/stream/sessions", body, "session", "onSession")
end sub

sub onSession(ev as Object)
    r = ev.GetData()
    if (r.status <> 200 and r.status <> 201) or r.data = invalid then
        fail()
        return
    end if
    d = r.data
    c = CreateObject("roSGNode", "ContentNode")
    if d.manifest_url <> invalid and d.manifest_url <> "" then
        c.url = NetJoin(m.global.session.base, d.manifest_url)
        c.streamFormat = "hls"
        if Instr(1, LCase(d.manifest_url), ".mpd") > 0 then c.streamFormat = "dash"
    else if d.direct_url <> invalid and d.direct_url <> "" then
        c.url = NetJoin(m.global.session.base, d.direct_url)
        c.streamFormat = "mp4"
    else
        fail()
        return
    end if
    m.video.content = c
    ' a transcode opened at a position starts there; direct play seeks
    if m.start > 0 and (d.start_sec = invalid or d.start_sec = 0) then m.video.seek = m.start
    m.video.control = "play"
    print "[player] "; d.mode; " "; c.streamFormat
    print "[screen] PlayerScreen ready"
end sub

sub onState()
    s = m.video.state
    print "[player] state "; s
    if s = "error" then fail()
end sub

sub fail()
    m.error.text = ui_Tr("player.error")
    m.error.visible = true
    print "[player] error"
end sub

sub onTakeFocus()
    m.video.SetFocus(true)
end sub

sub onVisible()
    if not m.top.visible then m.video.control = "stop"
end sub

function onKeyEvent(key as String, press as Boolean) as Boolean
    if press and key = "back" then m.video.control = "stop"
    return false
end function
