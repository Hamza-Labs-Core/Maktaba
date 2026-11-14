' A collection or a playlist (story 58.12): its title, Play and Shuffle (the server queue) and
' its items.

sub init()
    ui_Init()
    c = ui_Colors()
    m.title = m.top.FindNode("title")
    m.play = m.top.FindNode("play")
    m.shuffle = m.top.FindNode("shuffle")
    m.items = m.top.FindNode("items")
    m.empty = m.top.FindNode("empty")
    ui_Label(m.title, 56, c.fg, true)
    ui_Label(m.empty, 34, c.muted, false)
    m.play.text = ui_Tr("action.play")
    m.shuffle.text = ui_Tr("action.shuffle")
    m.items.drawFocusFeedback = false
    ui_Mirror(["title", "play", "shuffle", "items", "empty"])
    m.zone = "buttons"
    m.button = 0
    m.items.ObserveField("itemSelected", "onItem")
    m.top.ObserveField("params", "onParams")
    m.top.ObserveField("takeFocus", "onTakeFocus")
end sub

' The queue source and the route of the items: a playlist; a topic or clip collection (by its
' library); any other collection.
sub onParams()
    p = m.top.params
    m.title.text = p.title
    if p.kind = "playlist" then
        m.source = {kind: "playlist", id: p.id}
        path = "/api/me/playlists/" + p.id
    else if p.sub = "topic" or p.sub = "clip" then
        m.source = {kind: "collection", id: p.sub + ":" + p.id}
        path = "/api/libraries/" + p.library + "/collections/" + p.sub + "/" + p.id
    else
        m.source = {kind: "collection", id: p.id}
        path = "/api/collections/" + p.id + "/items" + NetQuery({limit: 200})
    end if
    ui_Api("GET", path, invalid, "items", "onItems")
end sub

sub onItems(ev as Object)
    r = ev.GetData()
    items = []
    if r.status = 200 and r.data <> invalid and type(r.data.items) = "roArray" then items = r.data.items
    if r.data <> invalid and type(r.data.playlist) = "roAssociativeArray" and r.data.playlist.name <> invalid then m.title.text = ui_Vis(r.data.playlist.name, 50)
    root = CreateObject("roSGNode", "ContentNode")
    ui_GridItems(root, items, "video", 5, true)
    m.items.content = root
    m.empty.visible = (items.Count() = 0)
    m.empty.text = ui_Tr("library.empty")
    if items.Count() > 0 then m.items.jumpToItem = m.layout.StartIndex(m_Min(5, root.GetChildCount()))
    print "[screen] ItemsScreen ready items="; items.Count()
end sub

function m_Min(a as Integer, b as Integer) as Integer
    if a < b then return a
    return b
end function

sub onItem()
    it = ui_GridCard(m.items)
    if it <> invalid and it.kind <> "blank" then ui_CardAction(it, "open")
end sub

sub onTakeFocus()
    focusZone()
end sub

sub focusZone()
    m.play.focused = (m.zone = "buttons" and m.button = 0)
    m.shuffle.focused = (m.zone = "buttons" and m.button = 1)
    if m.zone = "items" then
        m.items.SetFocus(true)
    else if m.button = 0 then
        m.play.SetFocus(true)
    else
        m.shuffle.SetFocus(true)
    end if
end sub

function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    k = m.layout.Key(key)
    if key = "back" then
        if m.zone = "items" and m.items.itemFocused >= 5 then
            m.items.jumpToItem = m.layout.StartIndex(m_Min(5, m.items.content.GetChildCount()))
            return true
        end if
        return false
    end if
    if m.zone = "buttons" then
        if k = "right" then
            m.button = 1
        else if k = "left" then
            m.button = 0
        else if key = "down" and m.items.content <> invalid and m.items.content.GetChildCount() > 0 then
            m.zone = "items"
        else if key = "OK" then
            ui_PlayQueue(m.source, (m.button = 1))
            return true
        else
            return true
        end if
        focusZone()
        return true
    end if
    if key = "up" then
        m.zone = "buttons"
        focusZone()
        return true
    end if
    action = NavCardKey(key)
    card = ui_GridCard(m.items)
    if card <> invalid and card.kind <> "blank" then
        if action = "play" then
            ui_CardAction(card, "play")
            return true
        else if action = "actions" then
            ui_ShowActions(card, m.items)
            return true
        end if
    end if
    return false
end function
