sub init()
    ui_Init()
    m.msg = m.top.FindNode("msg")
    m.stay = m.top.FindNode("stay")
    m.leave = m.top.FindNode("leave")
    m.top.ObserveField("focusedChild", "onFocusChange")
    m.top.ObserveField("visible", "onShown")
end sub

sub onShown()
    if not m.top.visible then return
    ui_Init()
    ui_Label(m.msg, 40, ui_Colors().fg, true)
    m.stay.text = ui_Tr("roku.exit.stay")
    m.leave.text = ui_Tr("roku.exit.leave")
    m.layout.Place(m.stay, 600, 600, 340)
    m.layout.Place(m.leave, 980, 600, 340)
    m.at = 0
    show()
end sub

sub onMessage()
    m.msg.text = m.top.message
end sub

sub onFocusChange()
end sub

sub show()
    m.stay.focused = (m.at = 0)
    m.leave.focused = (m.at = 1)
    print "[focus] confirm "; m.at
end sub

function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return true
    k = m.layout.Key(key)
    if k = "right" then
        m.at = 1
        show()
    else if k = "left" then
        m.at = 0
        show()
    else if key = "OK" then
        if m.at = 1 then
            m.top.choice = "leave"
        else
            m.top.choice = "stay"
        end if
    else if key = "back" then
        m.top.choice = "stay"
    end if
    return true
end function
