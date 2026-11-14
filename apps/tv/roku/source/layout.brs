' The channel's layout direction (Epic 58 story 58.15).
'
' Roku has no right-to-left layout: in Arabic every screen is mirrored by hand. Screens place
' their components through a layout made from the channel's language, never with raw x
' positions, so one code path draws both directions:
'
'   layout = LayoutNew(rtl, 1920)        ' the screen (or a parent Group) and its width
'   layout.X(x, w)                       ' the x of a box at x, w wide, as drawn
'   layout.Translation(x, y, w)          ' [x, y] for a node's translation field
'   layout.Place(node, x, y, w)          ' sets node.translation
'   layout.Align("left")                 ' a Label's horizAlign: "left" and "right" swap
'   layout.Order(items)                  ' a row's items as drawn: reversed in Arabic, so
'                                        ' RowList and PosterGrid get reversed content
'   layout.StartIndex(count)             ' where focus starts in a row: the right-most card
'   layout.Index(i, count)               ' item i of the logical order, in the drawn order
'                                        ' (and back: the mapping is its own inverse)
'   layout.Key(key)                      ' a remote key read in reading order: "left" and
'                                        ' "right" swap, so Left moves forward in Arabic
'   layout.FocusMap(map)                 ' {id: {left: id, right: id, up: id, down: id}}
'                                        ' with left and right swapped
'   layout.Child(w)                      ' the layout inside a Group w wide (the Group
'                                        ' itself is placed with Place)
'
' Positions are in the layout's own coordinates (0 at the logical start, the left in English).
' Text inside labels goes through VisualText (visualtext.brs); its alignment comes from Align.

function LayoutNew(rtl as Boolean, width as Integer) as Object
    return {
        rtl: rtl
        width: width
        X: layout_X
        Translation: layout_Translation
        Place: layout_Place
        Align: layout_Align
        Order: layout_Order
        StartIndex: layout_StartIndex
        Index: layout_Index
        Key: layout_Key
        FocusMap: layout_FocusMap
        Child: layout_Child
    }
end function

function layout_X(x as Integer, w as Integer) as Integer
    if m.rtl then return m.width - x - w
    return x
end function

function layout_Translation(x as Integer, y as Integer, w as Integer) as Object
    return [m.X(x, w), y]
end function

sub layout_Place(node as Object, x as Integer, y as Integer, w as Integer)
    node.translation = m.Translation(x, y, w)
end sub

function layout_Align(horiz as String) as String
    if not m.rtl then return horiz
    if horiz = "left" then return "right"
    if horiz = "right" then return "left"
    return horiz
end function

function layout_Order(items as Object) as Object
    out = []
    if m.rtl then
        for i = items.Count() - 1 to 0 step -1
            out.Push(items[i])
        end for
    else
        for each item in items
            out.Push(item)
        end for
    end if
    return out
end function

function layout_StartIndex(count as Integer) as Integer
    if m.rtl and count > 0 then return count - 1
    return 0
end function

function layout_Index(i as Integer, count as Integer) as Integer
    if m.rtl then return count - 1 - i
    return i
end function

function layout_Key(key as String) as String
    if not m.rtl then return key
    if key = "left" then return "right"
    if key = "right" then return "left"
    return key
end function

function layout_FocusMap(focusMap as Object) as Object
    out = {}
    for each id in focusMap
        entry = focusMap[id]
        moved = {}
        for each k in entry
            moved[m.Key(k)] = entry[k]
        end for
        out[id] = moved
    end for
    return out
end function

function layout_Child(w as Integer) as Object
    return LayoutNew(m.rtl, w)
end function
