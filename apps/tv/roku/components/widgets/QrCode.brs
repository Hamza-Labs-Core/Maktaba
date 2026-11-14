sub init()
    m.paper = m.top.FindNode("paper")
    m.dark = m.top.FindNode("dark")
end sub

sub onModules()
    m.dark.RemoveChildrenIndex(m.dark.GetChildCount(), 0)
    rows = m.top.modules
    if rows = invalid or rows.Count() = 0 then
        m.top.visible = false
        return
    end if
    n = rows.Count()
    ' whole pixels per module, the quiet zone four modules on each side; the square is centred
    ' in the width it was given
    px = Int(m.top.width / (n + 8))
    if px < 1 then px = 1
    side = px * (n + 8)
    pad = Int((m.top.width - side) / 2)
    m.paper.width = side
    m.paper.height = side
    m.paper.translation = [pad, pad]
    rects = 0
    for y = 0 to n - 1
        x = 0
        while x < n
            if rows[y][x] then
                start = x
                while x < n and rows[y][x]
                    x = x + 1
                end while
                r = m.dark.CreateChild("Rectangle")
                r.color = "0x000000FF"
                r.width = (x - start) * px
                r.height = px
                r.translation = [pad + (start + 4) * px, pad + (y + 4) * px]
                rects = rects + 1
            else
                x = x + 1
            end if
        end while
    end for
    m.top.visible = true
    print "[qr] drawn "; n; " modules at "; px; " px, "; rects; " runs"
end sub
