sub init()
    ui_Init()
    c = ui_Colors()
    for each id in ["title", "code", "where", "expires", "state", "plain"]
        m[id] = m.top.FindNode(id)
    end for
    m.qr = m.top.FindNode("qr")
    ui_Label(m.title, 56, c.fg, true)
    ui_Label(m.code, 120, c.accent, true)
    ui_Label(m.where, 34, c.fg, false)
    ui_Label(m.expires, 30, c.muted, false)
    ui_Label(m.state, 32, c.muted, false)
    ui_Label(m.plain, 30, c.muted, false)
    ui_Mirror(["title", "code", "where", "expires", "state", "plain", "qr"])
    m.title.text = ui_Tr("code.title")
    m.top.ObserveField("params", "onParams")
    m.top.ObserveField("takeFocus", "onTakeFocus")
    m.top.ObserveField("visible", "onVisible")
end sub

sub onParams()
    p = m.top.params
    m.mode = "cloud"
    if p.mode = "home" then m.mode = "home"
    if m.mode = "home" then
        m.where.text = ui_Tr("code.whereHome")
        if p.plain = true then m.plain.text = ui_Tr("launcher.plainHttpCode")
    else
        m.where.text = ui_Tr("code.where")
    end if
    m.state.text = ""
    startTask()
end sub

sub startTask()
    p = m.top.params
    t = CreateObject("roSGNode", "CodeTask")
    t.mode = m.mode
    if m.mode = "home" then
        t.base = p.base
        t.plain = (p.plain = true)
    end if
    t.ObserveField("code", "onCode")
    t.ObserveField("state", "onState")
    t.ObserveField("result", "onResult")
    m.task = t
    t.control = "RUN"
end sub

sub onCode(ev as Object)
    c = ev.GetData()
    m.code.text = c.code
    if c.qr <> invalid and c.qr <> "" then
        m.qr.uri = c.qr
        m.qr.visible = true
    else
        m.qr.visible = false
    end if
    minutes = 10
    if c.expiresIn <> invalid and c.expiresIn > 0 then minutes = Int(c.expiresIn / 60)
    m.expires.text = m.i18n.Format("code.expires", [minutes])
    m.state.text = ui_Tr("code.waiting")
    print "[screen] CodeScreen ready mode="; m.mode
end sub

sub onState(ev as Object)
    s = ev.GetData()
    if s = "signing_in" then
        m.state.text = ui_Tr("code.signingIn")
    else if s = "cloud_down" then
        m.code.text = ""
        m.state.text = ui_Tr("code.cloudDown")
    else if s = "no_code" then
        m.state.text = ui_Tr("code.failed")
    end if
end sub

sub onResult(ev as Object)
    r = ev.GetData()
    if r.verdict <> invalid and r.verdict <> "ok" then
        ui_Navigate({action: "compat", verdict: r.verdict, home: r.home})
        return
    end if
    if not r.ok then
        m.state.text = ui_Tr("code.failed")
        return
    end if
    home = r.home
    p = m.top.params
    if m.mode = "home" then
        home.id = p.id
        home.name = p.name
    end if
    print "[code] signed in to "; home.id
    ui_Navigate({action: "signedIn", home: home})
end sub

sub onVisible()
    ' a screen pushed over this one, or this one gone: the Task stops polling
    if not m.top.visible and m.task <> invalid then m.task.stop = true
end sub

sub onTakeFocus()
    m.top.SetFocus(true)
    if m.task <> invalid and m.task.stop then
        m.task.stop = false
        startTask()
    end if
end sub

function onKeyEvent(key as String, press as Boolean) as Boolean
    if press and key = "back" and m.task <> invalid then m.task.stop = true
    return false
end function
