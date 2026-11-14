sub init()
    m.card = m.top.FindNode("card")
    m.ring = m.top.FindNode("ring")
    m.frame = m.top.FindNode("frame")
    m.poster = m.top.FindNode("poster")
    m.title = m.top.FindNode("title")
    m.track = m.top.FindNode("track")
    m.bar = m.top.FindNode("bar")
    f = CreateObject("roSGNode", "Font")
    f.uri = "pkg:/fonts/IBMPlexSansArabic-Regular.ttf"
    f.size = 26
    m.title.font = f
    m.title.wrap = false
end sub

sub onSize()
    w = m.top.width
    h = m.top.height
    if w <= 0 or h <= 0 then return
    ph = h - 48
    m.frame.width = w
    m.frame.height = ph
    m.ring.width = w + 12
    m.ring.height = ph + 12
    m.ring.translation = [-6, -6]
    m.poster.width = w
    m.poster.height = ph
    m.track.width = w
    m.track.height = 6
    m.track.translation = [0, ph - 6]
    m.bar.height = 6
    m.bar.translation = [0, ph - 6]
    m.title.width = w
    m.title.translation = [0, ph + 8]
    m.card.scaleRotateCenter = [w / 2, ph / 2]
    onContent()
end sub

sub onContent()
    c = m.top.itemContent
    if c = invalid then return
    m.poster.uri = c.HDPosterUrl
    m.title.text = c.title
    if c.rtl = true then
        m.title.horizAlign = "right"
    else
        m.title.horizAlign = "left"
    end if
    p = c.progress
    if p <> invalid and p > 0 and m.top.width > 0 then
        if p > 1 then p = 1
        m.track.visible = true
        m.bar.visible = true
        m.bar.width = m.top.width * p
        if c.rtl = true then m.bar.translation = [m.top.width - m.bar.width, m.bar.translation[1]]
    else
        m.track.visible = false
        m.bar.visible = false
    end if
end sub

sub onFocus()
    f = m.top.focusPercent
    focusedArea = (m.top.rowHasFocus or m.top.gridHasFocus)
    if not focusedArea then f = 0
    m.ring.visible = (f > 0.5)
    s = 1.0 + 0.08 * f
    m.card.scale = [s, s]
end sub
