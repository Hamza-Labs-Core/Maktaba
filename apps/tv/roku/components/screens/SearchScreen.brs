' Search (story 58.12): Roku's MiniKeyboard (voice from the remote's microphone types into it);
' the home is asked 600 ms after the last key.

sub init()
    ui_Init()
    c = ui_Colors()
    m.title = m.top.FindNode("title")
    m.keyboard = m.top.FindNode("keyboard")
    m.query = m.top.FindNode("query")
    m.results = m.top.FindNode("results")
    m.empty = m.top.FindNode("empty")
    ui_Label(m.title, 48, c.fg, true)
    ui_Label(m.query, 34, c.accent, false)
    ui_Label(m.empty, 32, c.muted, false)
    m.results.drawFocusFeedback = false
    m.title.text = ui_Tr("search.hint")
    ' the keyboard sits at the reading start; the results beside it
    m.layout.Place(m.keyboard, 120, 150, 640)
    ui_Mirror(["title", "query", "results", "empty"])
    m.timer = CreateObject("roSGNode", "Timer")
    m.timer.duration = 0.6
    m.timer.repeat = false
    m.timer.ObserveField("fire", "onTimer")
    m.keyboard.ObserveField("text", "onText")
    m.results.ObserveField("itemSelected", "onResult")
    m.inResults = false
    m.top.ObserveField("params", "onParams")
    m.top.ObserveField("takeFocus", "onTakeFocus")
end sub

sub onParams()
    print "[screen] SearchScreen ready"
end sub

sub onText()
    m.query.text = ui_Vis(m.keyboard.text, 30)
    m.timer.control = "stop"
    m.timer.control = "start"
end sub

sub onTimer()
    q = m.keyboard.text.Trim()
    if q = "" then
        m.results.content = CreateObject("roSGNode", "ContentNode")
        return
    end if
    m.lastQuery = q
    ui_Api("GET", "/api/videos" + NetQuery({q: q, limit: 60}), invalid, q, "onResults")
end sub

sub onResults(ev as Object)
    if ev.GetRoSGNode().tag <> m.lastQuery then return
    r = ev.GetData()
    items = []
    if r.status = 200 and r.data <> invalid and type(r.data.items) = "roArray" then items = r.data.items
    root = CreateObject("roSGNode", "ContentNode")
    ui_GridItems(root, items, "video", 3, true)
    m.results.content = root
    m.empty.visible = (items.Count() = 0)
    m.empty.text = ui_Tr("search.noResults")
    print "[search] results "; items.Count()
end sub

sub onResult()
    it = ui_GridCard(m.results)
    if it <> invalid and it.kind <> "blank" then ui_CardAction(it, "open")
end sub

sub onTakeFocus()
    if m.inResults then
        m.results.SetFocus(true)
    else
        m.keyboard.SetFocus(true)
    end if
end sub

function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    k = m.layout.Key(key)
    if not m.inResults and k = "right" and m.results.content <> invalid and m.results.content.GetChildCount() > 0 then
        m.inResults = true
        onTakeFocus()
        return true
    end if
    if m.inResults and (k = "left" or key = "back") then
        m.inResults = false
        onTakeFocus()
        return true
    end if
    if m.inResults then
        action = NavCardKey(key)
        card = ui_GridCard(m.results)
        if card <> invalid and card.kind <> "blank" then
            if action = "play" then
                ui_CardAction(card, "play")
                return true
            else if action = "actions" then
                ui_ShowActions(card, m.results)
                return true
            end if
        end if
    end if
    return false
end function
