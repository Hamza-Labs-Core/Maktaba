' What every screen of the channel shares (Epic 58 story 58.12): its strings and layout in the
' channel's language, Ink & Brass colours (Epic 48's tokens), the bundled font, and calls to the
' home through the ApiTask.

sub ui_Init()
    ' a child of the scene starts before the scene has set the language
    lang = m.global.lang
    if lang = invalid then lang = "en"
    m.i18n = I18nNew(lang)
    m.rtl = m.i18n.rtl
    m.layout = LayoutNew(m.rtl, 1920)
end sub

function ui_Colors() as Object
    return {
        bg: "0x0B111CFF"
        fg: "0xE8ECF3FF"
        muted: "0xA9B3C4FF"
        subtle: "0x131D2EFF"
        accent: "0xE3B252FF"
        accentFg: "0x1A1408FF"
        border: "0x22304AFF"
        error: "0xF0707FFF"
    }
end function

function ui_Font(size as Integer, bold as Boolean) as Object
    f = CreateObject("roSGNode", "Font")
    if bold then
        f.uri = "pkg:/fonts/IBMPlexSansArabic-SemiBold.ttf"
    else
        f.uri = "pkg:/fonts/IBMPlexSansArabic-Regular.ttf"
    end if
    f.size = size
    return f
end function

' A label in the channel's style: its font, colour and alignment (mirrored in Arabic).
sub ui_Label(lbl as Object, size as Integer, color as String, bold as Boolean)
    lbl.font = ui_Font(size, bold)
    lbl.color = color
    lbl.horizAlign = m.layout.Align("left")
end sub

function ui_Tr(key as String) as String
    return m.i18n.Tr(key)
end function

' Text from the home (titles, names), shaped for Roku: Arabic joined and in display order.
function ui_Vis(s as Dynamic, widthChars as Integer) as String
    if s = invalid then return ""
    return VisualText(s, widthChars)
end function

' Calls the home: the response arrives in callback (a function name of this component) as an
' roSGNodeEvent whose node is the request (its tag tells calls apart).
function ui_Api(method as String, path as String, body as Dynamic, tag as String, callback as String) as Object
    req = CreateObject("roSGNode", "ApiRequest")
    req.method = method
    req.path = path
    req.tag = tag
    if body <> invalid then
        req.body = body
        req.hasBody = true
    end if
    req.ObserveFieldScoped("response", callback)
    m.global.api.request = req
    return req
end function

' The poster of a video at the small size (the scene's HTTP agent carries the bearer).
function ui_Poster(videoId as Dynamic) as String
    if videoId = invalid or videoId = "" then return ""
    return NetJoin(m.global.session.base, "/api/videos/" + videoId + "/poster?size=small")
end function

' Asks the scene to open a screen, go back, or do something only it can.
sub ui_Navigate(nav as Object)
    m.top.navigate = nav
end sub

sub ui_Log(s as String)
    print "[ui] "; s
end sub

' A card's content node from a home's item (a video card, a queue item, a series, a
' collection): its title shaped once here, where the row is loaded (story 58.15: item
' components are recycled as a row scrolls), its poster, its progress and what it opens.
function ui_Item(parent as Object, item as Object, kind as String) as Object
    it = parent.CreateChild("ContentNode")
    it.AddFields({progress: 0.0, rtl: false, itemId: "", kind: kind, seriesId: "", resume: 0, subkind: "", library: ""})
    title = CardTitle(item)
    it.title = VisualText(title, 26)
    it.rtl = VisualIsRtl(title)
    id = item.id
    if id = invalid then id = item.video_id
    if id = invalid then id = ""
    it.itemId = id
    posterId = id
    if kind = "series" or kind = "collection" or kind = "playlist" then
        posterId = item.poster_video_id
        if posterId = invalid and type(item.poster_video_ids) = "roArray" and item.poster_video_ids.Count() > 0 then posterId = item.poster_video_ids[0]
    end if
    if posterId <> invalid then it.HDPosterUrl = ui_Poster(posterId)
    if type(item.series) = "roAssociativeArray" and item.series.id <> invalid then it.seriesId = item.series.id
    if item.series_id <> invalid then it.seriesId = item.series_id
    d = item.duration_sec
    p = item.position_sec
    if d <> invalid and p <> invalid and d > 0 and item.completed <> true then
        it.progress = p / d
        it.resume = Int(p)
    end if
    return it
end function

' The items of a list, in the layout's order (reversed in Arabic: a row reads from the right).
function ui_Ordered(list as Dynamic) as Object
    if type(list) <> "roArray" then return []
    return m.layout.Order(list)
end function

' A LabelList in the channel's style: the bundled font, a brass focus bar, text aligned to the
' reading start.
sub ui_List(list as Object)
    c = ui_Colors()
    list.font = ui_Font(34, false)
    list.focusedFont = ui_Font(34, true)
    list.color = c.fg
    list.focusedColor = c.accentFg
    list.focusBitmapBlendColor = c.accent
    list.textHorizAlign = m.layout.Align("left")
end sub

' A LabelList's content from strings (already visual).
function ui_ListContent(items as Object) as Object
    root = CreateObject("roSGNode", "ContentNode")
    for each t in items
        it = root.CreateChild("ContentNode")
        it.title = t
    end for
    return root
end function

' Places a screen's nodes by id at their XML x (as drawn in English), mirrored in Arabic.
sub ui_Mirror(ids as Object)
    for each id in ids
        n = m.top.FindNode(id)
        if n <> invalid then
            w = 0
            if n.HasField("width") then w = n.width
            ' A grid has no width field: its columns, item size and spacing give it.
            cols = 1
            if n.HasField("numColumns") and n.numColumns > 1 then cols = n.numColumns
            if w = 0 and n.HasField("basePosterSize") then w = cols * n.basePosterSize[0] + (cols - 1) * n.itemSpacing[0]
            if w = 0 and n.HasField("itemSize") then w = cols * n.itemSize[0] + (cols - 1) * n.itemSpacing[0]
            ' A RowList's focus is fixed at the row's left edge; in Arabic the first card sits at
            ' the right, so the focus floats to it.
            if m.layout.rtl and n.HasField("rowFocusAnimationStyle") then n.rowFocusAnimationStyle = "floatingFocus"
            if w = 0 then
                r = n.BoundingRect()
                w = r.width
            end if
            n.translation = [m.layout.X(n.translation[0], w), n.translation[1]]
        end if
    end for
end sub

' A row of cards for a RowList: its title (static, already visual) and items in the layout's
' order.
' In Arabic a row shorter than the screen is padded at its logical end (the left) with blank
' cards, so its cards start at the right as the reading does.
function ui_Row(root as Object, title as String, items as Object, kind as String) as Object
    row = root.CreateChild("ContentNode")
    row.title = title
    if m.layout.rtl and type(items) = "roArray" then
        for i = items.Count() to ui_RowCols() - 1
            b = row.CreateChild("ContentNode")
            b.AddFields({kind: "blank"})
        end for
    end if
    for each it in ui_Ordered(items)
        ui_Item(row, it, kind)
    end for
    return row
end function

' The cards a RowList row shows at once (rowItemSize 300 with spacing 28 in itemSize 1612).
function ui_RowCols() as Integer
    return 5
end function

' Sets up a RowList once its content is in place, or once rows from fromRow on are added. In
' Arabic each row title is moved to the row's right edge (the row label is drawn from the left,
' so its width is measured), and each new row starts on its first card, the right-most; the
' focus then goes back where it was (row 0 at first).
sub ui_RowsReady(rows as Object, fromRow as Integer)
    if rows.content = invalid then return
    count = rows.content.GetChildCount()
    if not m.layout.rtl or count = 0 or fromRow >= count then return
    rowWidth = rows.itemSize[0]
    measure = CreateObject("roSGNode", "Label")
    measure.font = rows.rowLabelFont
    offsets = []
    for r = 0 to count - 1
        measure.text = rows.content.GetChild(r).title
        offsets.Push([rowWidth - measure.BoundingRect().width, 16])
    end for
    rows.rowLabelOffset = offsets
    was = rows.rowItemFocused
    for r = count - 1 to fromRow step -1
        rows.jumpToRowItem = [r, m.layout.StartIndex(rows.content.GetChild(r).GetChildCount())]
    end for
    if fromRow > 0 and was <> invalid and was.Count() = 2 then rows.jumpToRowItem = was
    if m.rowGuards = invalid then m.rowGuards = {}
    if not m.rowGuards.DoesExist(rows.id) then
        m.rowGuards[rows.id] = true
        rows.ObserveField("rowItemFocused", "ui_OnRowFocus")
    end if
end sub

' Keeps the focus off the blanks that pad a short row in Arabic.
sub ui_OnRowFocus(event as Object)
    rows = event.GetRoSGNode()
    f = rows.rowItemFocused
    if f = invalid or f.Count() < 2 or rows.content = invalid then return
    row = rows.content.GetChild(f[0])
    if row = invalid then return
    card = row.GetChild(f[1])
    if card = invalid or card.kind <> "blank" then return
    first = f[1]
    while first < row.GetChildCount() - 1 and row.GetChild(first).kind = "blank"
        first = first + 1
    end while
    rows.jumpToRowItem = [f[0], first]
end sub

' The card focused in a RowList, or invalid (a blank is no card).
function ui_RowCard(rows as Object) as Dynamic
    f = rows.rowItemFocused
    if f = invalid or rows.content = invalid then return invalid
    row = rows.content.GetChild(f[0])
    if row = invalid then return invalid
    card = row.GetChild(f[1])
    if card = invalid or card.kind = "blank" then return invalid
    return card
end function

' The card focused in a MarkupGrid or PosterGrid, or invalid.
function ui_GridCard(grid as Object) as Dynamic
    if grid.content = invalid or grid.itemFocused < 0 then return invalid
    return grid.content.GetChild(grid.itemFocused)
end function

' What a card does: "play" (from where it stopped), "over" (from the start), "open" (its
' screen) or "series" (its series).
sub ui_CardAction(item as Object, action as String)
    if item = invalid then return
    print "[card] "; action; " "; item.kind
    if action = "play" or action = "over" then
        if item.kind = "series" or item.kind = "collection" or item.kind = "playlist" then
            ui_Navigate({to: "PlayerScreen", params: {queue: {kind: item.kind, id: item.itemId}, shuffle: false}})
        else
            start = 0
            if action = "play" then start = item.resume
            ui_Navigate({to: "PlayerScreen", params: {videoId: item.itemId, start: start}})
        end if
    else if action = "series" then
        ui_Navigate({to: "SeriesScreen", params: {id: item.seriesId}})
    else if action = "open" then
        if item.kind = "series" then
            ui_Navigate({to: "SeriesScreen", params: {id: item.itemId}})
        else if item.kind = "collection" or item.kind = "playlist" then
            ui_Navigate({to: "ItemsScreen", params: {kind: item.kind, id: item.itemId, sub: item.subkind, library: item.library, title: item.title}})
        else
            ui_Navigate({to: "VideoScreen", params: {id: item.itemId}})
        end if
    end if
end sub

' The * key on a card: its actions in a dialog; focus comes back to back.
sub ui_ShowActions(item as Dynamic, back as Object)
    if item = invalid then return
    d = CreateObject("roSGNode", "ActionsDialog")
    d.ObserveFieldScoped("choice", "ui_OnAction")
    m.actionsBack = back
    m.actionsItem = item
    m.actionsDialog = d
    m.top.AppendChild(d)
    d.item = item
    d.SetFocus(true)
end sub

sub ui_OnAction(ev as Object)
    choice = ev.GetData()
    m.top.RemoveChild(m.actionsDialog)
    m.actionsDialog = invalid
    m.actionsBack.SetFocus(true)
    if choice <> "" and choice <> "cancel" then ui_CardAction(m.actionsItem, choice)
end sub

' Starts the server queue (Epic 56): Play or Shuffle of a library, a series, a season, a
' collection or a playlist, then the player on its first item.
sub ui_PlayQueue(source as Object, shuffle as Boolean)
    ui_Navigate({to: "PlayerScreen", params: {queue: source, shuffle: shuffle}})
end sub

' A grid's items in reading order: each row of cols reversed in Arabic, the last row padded at
' its end (on the left) with empty cards when final, so a grid fills from the right.
sub ui_GridItems(parent as Object, items as Object, kind as String, cols as Integer, final as Boolean)
    n = items.Count()
    i = 0
    while i < n
        rowItems = []
        for j = i to i + cols - 1
            if j < n then rowItems.Push(items[j])
        end for
        if m.rtl then
            for k = 0 to cols - 1 - rowItems.Count()
                if not final then exit for
                blank = parent.CreateChild("ContentNode")
                blank.AddFields({itemId: "", kind: "blank", progress: 0.0, rtl: false, seriesId: "", resume: 0, subkind: "", library: ""})
            end for
            for k = rowItems.Count() - 1 to 0 step -1
                ui_GridItem(parent, rowItems[k], kind)
            end for
        else
            for each it in rowItems
                ui_GridItem(parent, it, kind)
            end for
        end if
        i = i + cols
    end while
end sub

function ui_GridItem(parent as Object, item as Object, kind as String) as Object
    it = ui_Item(parent, item, kind)
    it.shortDescriptionLine1 = it.title
    it.AddFields({poster: it.HDPosterUrl})
    return it
end function
