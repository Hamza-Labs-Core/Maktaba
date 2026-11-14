sub init()
    ui_Init()
end sub

sub onItems()
    g = m.top.FindNode("pills")
    while g.GetChildCount() > 0
        g.RemoveChildIndex(0)
    end while
    lay = m.layout.Child(m.top.rowWidth)
    w = m.top.pillWidth
    x = 0
    for each text in m.top.items
        b = CreateObject("roSGNode", "TvButton")
        b.width = w
        b.text = text
        lay.Place(b, x, 0, w)
        g.AppendChild(b)
        x = x + w + 16
    end for
    onSelected()
end sub

sub onSelected()
    g = m.top.FindNode("pills")
    c = ui_Colors()
    for i = 0 to g.GetChildCount() - 1
        b = g.GetChild(i)
        b.focused = (m.top.focused and i = m.top.selected)
        ' the selected pill stays marked while focus is elsewhere
        b.opacity = 1.0
        if not m.top.focused and i <> m.top.selected then b.opacity = 0.6
    end for
end sub

function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    if key = "left" or key = "right" then
        n = NavTabStep(m.top.selected, m.top.items.Count(), key, m.layout)
        if n <> m.top.selected then m.top.selected = n
        return true
    end if
    if key = "OK" then
        m.top.activated = m.top.selected
        return true
    end if
    return false
end function
