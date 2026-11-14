' Story 58.15: the layout direction helper (source/layout.brs) in English and Arabic.
sub Main()
    m.pass = 0
    m.fail = 0
    ltr = LayoutNew(false, 1920)
    rtl = LayoutNew(true, 1920)

    ' x positions: a 300-wide card at x=100 sits 100 from the right edge in Arabic
    t_Eq("ltr X", ltr.X(100, 300), 100)
    t_Eq("rtl X", rtl.X(100, 300), 1520)
    t_Eq("rtl X full width", rtl.X(0, 1920), 0)
    t_Eq("rtl translation", rtl.Translation(100, 50, 300)[0], 1520)
    t_Eq("rtl translation y", rtl.Translation(100, 50, 300)[1], 50)
    node = {translation: [0, 0]}
    rtl.Place(node, 60, 200, 400)
    t_Eq("Place sets translation x", node.translation[0], 1460)
    t_Eq("Place sets translation y", node.translation[1], 200)
    ' mirrored twice is where it started
    t_Eq("X is its own inverse", rtl.X(rtl.X(100, 300), 300), 100)

    ' a Group's children use the Group's width
    child = rtl.Child(600)
    t_Eq("child X", child.X(20, 100), 480)
    t_Eq("child keeps the direction", child.rtl, true)

    ' label alignment
    t_Str("rtl align left", rtl.Align("left"), "right")
    t_Str("rtl align right", rtl.Align("right"), "left")
    t_Str("rtl align center", rtl.Align("center"), "center")
    t_Str("ltr align left", ltr.Align("left"), "left")

    ' row order: rows start at the right in Arabic
    items = ["a", "b", "c", "d"]
    t_Str("ltr order", ltr.Order(items).Join(""), "abcd")
    t_Str("rtl order", rtl.Order(items).Join(""), "dcba")
    t_Str("Order leaves its input alone", items.Join(""), "abcd")
    t_Eq("rtl order empty", rtl.Order([]).Count(), 0)

    ' focus starts at the right-most card, which is the first logical item
    t_Eq("ltr start", ltr.StartIndex(4), 0)
    t_Eq("rtl start", rtl.StartIndex(4), 3)
    t_Eq("rtl start empty", rtl.StartIndex(0), 0)
    t_Str("rtl start is the first item", rtl.Order(items)[rtl.StartIndex(4)], "a")
    t_Eq("rtl index", rtl.Index(0, 4), 3)
    t_Eq("rtl index back", rtl.Index(rtl.Index(1, 4), 4), 1)
    t_Eq("ltr index", ltr.Index(2, 4), 2)

    ' keys: Left moves forward in Arabic
    t_Str("rtl left", rtl.Key("left"), "right")
    t_Str("rtl right", rtl.Key("right"), "left")
    t_Str("rtl up", rtl.Key("up"), "up")
    t_Str("rtl OK", rtl.Key("OK"), "OK")
    t_Str("ltr left", ltr.Key("left"), "left")
    ' the next logical item of a reversed row is drawn to the left of the first one, and Left
    ' (read through Key) is the key that moves there
    drawn = rtl.Order(items)
    t_Str("the second item, drawn", drawn[rtl.Index(1, 4)], "b")
    t_Eq("drawn to the left", rtl.Index(1, 4) < rtl.Index(0, 4), true)
    t_Str("Left is forward", rtl.Key("left"), "right")

    ' focus maps
    fm = {
        tabs: {down: "grid", right: "search"}
        search: {left: "tabs", down: "grid"}
    }
    mirrored = rtl.FocusMap(fm)
    t_Str("focus map swaps right", mirrored.tabs.left, "search")
    t_Eq("focus map drops the old right", mirrored.tabs.right = invalid, true)
    t_Str("focus map keeps down", mirrored.tabs.down, "grid")
    t_Str("focus map swaps left", mirrored.search.right, "tabs")
    t_Str("ltr focus map unchanged", ltr.FocusMap(fm).tabs.right, "search")

    print "RESULT pass="; m.pass; " fail="; m.fail
end sub

sub t_Eq(name as String, got as Dynamic, want as Dynamic)
    if got = want then
        m.pass = m.pass + 1
    else
        m.fail = m.fail + 1
        print "FAIL "; name; ": got "; got; ", want "; want
    end if
end sub

sub t_Str(name as String, got as String, want as String)
    t_Eq(name, got, want)
end sub
