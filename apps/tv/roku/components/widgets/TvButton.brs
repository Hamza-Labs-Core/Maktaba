sub init()
    m.label = m.top.FindNode("label")
    m.box = m.top.FindNode("box")
    m.ring = m.top.FindNode("ring")
    m.label.font = ui_Font(32, true)
    onFocused()
end sub

sub onText()
    m.label.text = m.top.text
end sub

sub onLayout()
    w = m.top.width
    m.box.width = w
    m.label.width = w
    m.ring.width = w + 8
end sub

sub onFocused()
    c = ui_Colors()
    m.ring.visible = m.top.focused
    if m.top.focused then
        m.box.color = c.accent
        m.label.color = c.accentFg
    else
        m.box.color = c.subtle
        m.label.color = c.fg
    end if
end sub
