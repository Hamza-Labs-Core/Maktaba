sub init()
    ui_Init()
    c = ui_Colors()
    m.title = m.top.FindNode("title")
    m.hint = m.top.FindNode("hint")
    m.state = m.top.FindNode("state")
    m.keyboard = m.top.FindNode("keyboard")
    m.connect = m.top.FindNode("connect")
    ui_Label(m.title, 56, c.fg, true)
    ui_Label(m.hint, 32, c.muted, false)
    ui_Label(m.state, 32, c.error, false)
    m.title.text = ui_Tr("launcher.typed.title")
    m.hint.text = ui_Tr("launcher.typed.label")
    m.connect.text = ui_Tr("launcher.typed.connect")
    ' the keyboard types an address, left to right in both languages; the rest mirrors
    ui_Mirror(["title", "hint", "state", "connect"])
    m.top.ObserveField("takeFocus", "onTakeFocus")
    m.top.ObserveField("params", "onParams")
end sub

sub onParams()
    print "[screen] AddressScreen ready"
end sub

sub onTakeFocus()
    m.keyboard.SetFocus(true)
    m.connect.focused = false
end sub

function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    if key = "down" and m.keyboard.IsInFocusChain() then
        m.connect.focused = true
        m.connect.SetFocus(true)
        return true
    end if
    if key = "up" and m.connect.focused then
        onTakeFocus()
        return true
    end if
    if key = "OK" and m.connect.focused then
        go()
        return true
    end if
    return false
end function

sub go()
    base = NetNormalizeBase(m.keyboard.text)
    if base = "" then
        m.state.text = ui_Tr("launcher.typed.invalid")
        return
    end if
    m.state.text = ""
    print "[typed] "; base
    t = CreateObject("roSGNode", "HomeTask")
    t.command = {kind: "read", base: base}
    t.ObserveField("result", "onRead")
    m.task = t
    t.control = "RUN"
end sub

sub onRead(ev as Object)
    r = ev.GetData()
    if not r.ok then
        m.state.text = m.i18n.Format("launcher.notHome", [r.base])
        return
    end if
    if r.verdict <> "ok" then
        ui_Navigate({action: "compat", verdict: r.verdict, home: {id: r.answer.id, name: r.answer.name, base: r.base}})
        return
    end if
    ui_Navigate({to: "CodeScreen", params: {mode: "home", base: r.base, plain: r.plain, id: r.answer.id, name: r.answer.name}})
end sub
