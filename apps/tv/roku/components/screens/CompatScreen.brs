sub init()
    ui_Init()
    c = ui_Colors()
    m.title = m.top.FindNode("title")
    m.body = m.top.FindNode("body")
    m.again = m.top.FindNode("again")
    m.other = m.top.FindNode("other")
    ui_Label(m.title, 56, c.fg, true)
    ui_Label(m.body, 34, c.fg, false)
    m.again.text = ui_Tr("launcher.tryAgain")
    m.other.text = ui_Tr("launcher.chooseAnother")
    ui_Mirror(["title", "body", "again", "other"])
    m.at = 0
    m.top.ObserveField("params", "onParams")
    m.top.ObserveField("takeFocus", "onTakeFocus")
end sub

sub onParams()
    p = m.top.params
    name = ""
    if p.home <> invalid and p.home.name <> invalid then name = ui_Vis(p.home.name, 30)
    if p.verdict = "update_home" then
        m.title.text = ui_Tr("compat.updateHome.title")
        m.body.text = m.i18n.Format("compat.updateHome.viewer", [name])
    else
        m.title.text = ui_Tr("compat.updateApp.title")
        m.body.text = m.i18n.Format("compat.updateApp.why", [name]) + Chr(10) + ui_Tr("compat.updateApp.roku")
    end if
    print "[screen] CompatScreen ready "; p.verdict
    ui_Navigate({action: "drawn", screen: "CompatScreen"})
end sub

sub onTakeFocus()
    show()
end sub

sub show()
    if m.at = 0 then
        m.again.SetFocus(true)
    else
        m.other.SetFocus(true)
    end if
    m.again.focused = (m.at = 0)
    m.other.focused = (m.at = 1)
end sub

function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    k = m.layout.Key(key)
    if k = "right" then
        m.at = 1
    else if k = "left" then
        m.at = 0
    else if key = "OK" then
        p = m.top.params
        if m.at = 0 and p.home <> invalid and p.home.refresh <> invalid and p.home.refresh <> "" then
            ui_Navigate({action: "connect", home: p.home})
        else
            ui_Navigate({action: "start"})
        end if
        return true
    else
        return false
    end if
    show()
    return true
end function
