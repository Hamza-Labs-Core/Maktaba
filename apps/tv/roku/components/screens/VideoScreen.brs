' A video (story 58.12): poster, title, description; Resume from (where it stopped) or Play,
' and Start over.

sub init()
    ui_Init()
    c = ui_Colors()
    m.poster = m.top.FindNode("poster")
    m.title = m.top.FindNode("title")
    m.meta = m.top.FindNode("meta")
    m.desc = m.top.FindNode("desc")
    m.resume = m.top.FindNode("resume")
    m.over = m.top.FindNode("over")
    ui_Label(m.title, 52, c.fg, true)
    ui_Label(m.meta, 30, c.muted, false)
    ui_Label(m.desc, 30, c.fg, false)
    ui_Mirror(["poster", "title", "meta", "desc", "resume", "over"])
    m.at = 0
    m.start = 0
    m.top.ObserveField("params", "onParams")
    m.top.ObserveField("takeFocus", "onTakeFocus")
end sub

sub onParams()
    id = m.top.params.id
    m.poster.uri = ui_Poster(id)
    ui_Api("GET", "/api/videos/" + id, invalid, "video", "onVideo")
end sub

sub onVideo(ev as Object)
    r = ev.GetData()
    if r.status <> 200 or r.data = invalid then
        m.title.text = ui_Tr("roku.notAvailable")
        return
    end if
    v = r.data
    m.video = v
    m.title.text = VisualLines(CardTitle(v), 40, 2, "auto").Join(Chr(10))
    if v.duration_sec <> invalid then m.meta.text = FormatTime(v.duration_sec)
    if v.description <> invalid then m.desc.text = VisualLines(v.description, 60, 8, "auto").Join(Chr(10))
    m.start = ResumePosition(v)
    if m.start > 0 then
        m.resume.text = m.i18n.Format("video.resumeFrom", [FormatTime(m.start)])
        m.over.text = ui_Tr("video.startOver")
        m.over.visible = true
    else
        m.resume.text = ui_Tr("action.play")
        m.over.visible = false
    end if
    show()
    print "[screen] VideoScreen ready resume="; m.start
end sub

sub onTakeFocus()
    show()
end sub

sub show()
    if m.at = 0 then
        m.resume.SetFocus(true)
    else
        m.over.SetFocus(true)
    end if
    m.resume.focused = (m.at = 0)
    m.over.focused = (m.at = 1)
end sub

function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    k = m.layout.Key(key)
    if k = "right" and m.over.visible then
        m.at = 1
    else if k = "left" then
        m.at = 0
    else if key = "OK" or key = "play" then
        if m.video = invalid then return true
        start = m.start
        if m.at = 1 then start = 0
        ui_Navigate({to: "PlayerScreen", params: {videoId: m.video.id, start: start}})
        return true
    else
        return false
    end if
    show()
    return true
end function
