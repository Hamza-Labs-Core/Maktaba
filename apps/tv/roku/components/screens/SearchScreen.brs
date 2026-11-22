' Search (story 58.12): Roku's MiniKeyboard (voice from the remote's microphone types into it);
' the home is asked 600 ms after the last key (POST /api/search, the web's call).
'
' Story 49.11 (the API of 49.8; the logic in source/search.brs):
'   - suggestions under the keyboard 200 ms after a key (GET /api/search/suggest), each with its
'     kind's word: an earlier search, a word, a video's title, a speaker. OK on a title opens its
'     video; on any other it is searched. A choice hides the list until the next key.
'   - "Did you mean …?" above the results: OK searches the corrected words, keeping the order and
'     the filters.
'   - the order (Relevance, Newest, Most matches; the chosen one marked ✓) and Filters: a sheet
'     (the ChoiceDialog) of every facet's buckets with their counts, by library, language,
'     speaker, year and length. OK toggles a bucket and the sheet stays open, so several
'     combine; a chosen one is marked ✓ and OK again takes it off; Clear all filters heads the
'     sheet while any is on. Back closes it.
'   - new words drop the filters (they were the old results' counts) and keep the order.
'
' Focus: the keyboard, the suggestions under it, and the right column (Did you mean, the order
' and Filters, the results). Right from the keyboard or the suggestions goes to the column's
' first part; Left from the column's start, or Back, comes back to the keyboard. Each move is
' logged ("[focus] search <zone> <i>") for the screen tests.

sub init()
    ui_Init()
    c = ui_Colors()
    m.title = m.top.FindNode("title")
    m.keyboard = m.top.FindNode("keyboard")
    m.query = m.top.FindNode("query")
    m.suggest = m.top.FindNode("suggest")
    m.dym = m.top.FindNode("dym")
    m.tools = m.top.FindNode("tools")
    m.results = m.top.FindNode("results")
    m.empty = m.top.FindNode("empty")
    m.sheet = m.top.FindNode("sheet")
    m.keys = m.top.FindNode("keys")
    ui_Label(m.title, 48, c.fg, true)
    ui_Label(m.query, 34, c.accent, false)
    ui_Label(m.empty, 32, c.muted, false)
    ui_List(m.suggest)
    m.results.drawFocusFeedback = false
    m.title.text = ui_Tr("search.hint")
    ' the keyboard sits at the reading start; the results beside it
    m.layout.Place(m.keyboard, 120, 150, 640)
    ' the typed text and the suggestions go under the keyboard, however tall this Roku draws it
    kb = m.keyboard.BoundingRect()
    below = 780
    if kb.height > 0 then below = Int(kb.y + kb.height) + 12
    m.query.translation = [120, below]
    m.suggest.translation = [120, below + 52]
    rows = Int((1040 - (below + 52)) / 62)
    if rows < 1 then rows = 1
    if rows > 3 then rows = 3
    m.suggest.numRows = rows
    print "[search] keyboard ends at "; below; " suggestions "; rows
    ui_Mirror(["title", "query", "suggest", "results", "empty"])
    m.colX = m.layout.X(800, 1000)
    m.tool = []
    lay = m.layout.Child(1000)
    x = 0
    for each id in ["sort0", "sort1", "sort2", "filters"]
        b = m.top.FindNode(id)
        lay.Place(b, x, 0, b.width)
        x = x + b.width + 16
        m.tool.Push(b)
    end for
    m.timer = newTimer(0.6, "onTimer")
    m.suggestTimer = newTimer(0.2, "onSuggestTimer")
    m.keyboard.ObserveField("text", "onText")
    m.results.ObserveField("itemSelected", "onResult")
    m.suggest.ObserveField("itemSelected", "onSuggestion")
    m.suggest.ObserveField("itemFocused", "onSuggestFocus")
    m.sheet.ObserveField("choice", "onSheet")
    m.zone = "keyboard"
    m.toolIndex = 0
    m.q = ""
    m.sort = "relevance"
    m.sel = SearchNoSel()
    m.selFor = ""
    m.facets = invalid
    m.labels = {}
    m.dymText = ""
    m.seq = 0
    m.lastTag = ""
    m.suggestions = []
    m.suggestHidden = ""
    m.sheetRows = []
    m.sheetAt = 0
    m.sheetWant = invalid
    m.top.ObserveField("params", "onParams")
    m.top.ObserveField("takeFocus", "onTakeFocus")
    layoutColumn()
end sub

function newTimer(seconds as Float, callback as String) as Object
    t = CreateObject("roSGNode", "Timer")
    t.duration = seconds
    t.repeat = false
    t.ObserveField("fire", callback)
    return t
end function

sub onParams()
    print "[screen] SearchScreen ready"
end sub

sub onText()
    m.query.text = ui_Vis(m.keyboard.text, 30)
    m.timer.control = "stop"
    m.timer.control = "start"
    m.suggestTimer.control = "stop"
    m.suggestTimer.control = "start"
end sub

' Typing stopped: new words drop the filters and are searched.
sub onTimer()
    q = m.keyboard.text.Trim()
    ' the words on screen were asked already (Did you mean, or typed back to them)
    if q <> "" and q = m.q and m.lastTag <> "" then return
    m.q = q
    if q = "" then
        m.lastTag = ""
        m.results.content = CreateObject("roSGNode", "ContentNode")
        m.empty.visible = false
        m.dymText = ""
        m.facets = invalid
        ' an emptied box starts afresh: the words typed next are new, and drop the filters
        m.sel = SearchNoSel()
        m.selFor = ""
        layoutColumn()
        return
    end if
    if q <> m.selFor then
        m.sel = SearchNoSel()
        m.selFor = q
    end if
    runSearch()
end sub

' Asks the home for m.q with the order and filters on screen.
sub runSearch()
    if m.q = "" then return
    body = SearchBody(m.q, m.sort, m.sel, m.facets)
    m.seq = m.seq + 1
    m.lastTag = "q" + m.seq.ToStr()
    print "[search] ask "; FormatJson(body)
    ui_Api("POST", "/api/search", body, m.lastTag, "onResults")
end sub

sub onResults(ev as Object)
    if ev.GetRoSGNode().tag <> m.lastTag then return
    r = ev.GetData()
    data = invalid
    if r.status = 200 then data = r.data
    m.facets = SearchFacets(data)
    SearchKeepLabels(m.facets, m.labels)
    m.dymText = SearchDidYouMean(data, m.q)
    items = SearchResults(data)
    root = CreateObject("roSGNode", "ContentNode")
    ui_GridItems(root, items, "video", 3, true)
    m.results.content = root
    m.empty.visible = (items.Count() = 0)
    m.empty.text = ui_Tr("search.noResults")
    layoutColumn()
    rows = SearchFacetRows(m.facets, m.sel, m.labels)
    print "[search] results "; items.Count(); " dym="; m.dymText; " facets="; rows.Count(); " filters="; SearchSelCount(m.sel)
    ' the sheet, open while the answer came, keeps the row the remote is on
    if m.sheet.visible then
        at = m.sheet.at
        want = invalid
        if at >= 0 and at < m.sheetRows.Count() and not m.sheetRows[at].DoesExist("clear") then want = {facet: m.sheetRows[at].facet, value: m.sheetRows[at].value}
        showSheet(at, want)
    end if
    ' a part that went away takes the focus with it: the column's first part, or the keyboard
    if (m.zone = "dym" and not m.dym.visible) or (m.zone = "tools" and not m.tools.visible) or (m.zone = "results" and items.Count() = 0) then
        first = columnFirst()
        if first = "" then first = "keyboard"
        focusZone(first)
    end if
end sub

' Places the right column's parts (Did you mean, the order and Filters, the results) one under
' the other, leaving out those with nothing to show.
sub layoutColumn()
    y = 150
    m.dym.visible = (m.dymText <> "")
    if m.dym.visible then
        m.dym.text = m.i18n.Format("search.didYouMean", [m.dymText])
        m.dym.translation = [m.colX, y]
        y = y + 96
    end if
    hasResults = m.results.content <> invalid and m.results.content.GetChildCount() > 0
    m.tools.visible = m.q <> "" and m.lastTag <> "" and (hasResults or SearchSelCount(m.sel) > 0 or m.facets <> invalid)
    if m.tools.visible then
        m.tools.translation = [m.colX, y]
        y = y + 100
        updateTools()
    end if
    m.results.translation = [m.results.translation[0], y]
    m.empty.translation = [m.empty.translation[0], y + 40]
    if y > 250 then m.results.numRows = 2 else m.results.numRows = 3
end sub

' The order's buttons, the chosen one marked ✓ at the reading start, and Filters with the count
' of those in use (left out when the home sent no facets and none is chosen).
sub updateTools()
    sorts = SearchSorts()
    for i = 0 to 2
        label = ui_Tr("search.sort." + sorts[i])
        b = m.tool[i]
        if sorts[i] = m.sort then
            if m.rtl then label = label + " ✓" else label = "✓ " + label
            b.opacity = 1.0
        else
            b.opacity = 0.7
        end if
        b.text = label
    end for
    n = SearchSelCount(m.sel)
    f = m.tool[3]
    f.visible = (n > 0 or SearchFacetRows(m.facets, m.sel, m.labels).Count() > 0)
    if n > 0 then f.text = m.i18n.Format("search.filters.on", [n.ToStr()]) else f.text = ui_Tr("search.filters")
    if m.toolIndex > lastTool() then m.toolIndex = lastTool()
    showToolFocus()
end sub

function lastTool() as Integer
    if m.tool[3].visible then return 3
    return 2
end function

sub showToolFocus()
    for i = 0 to 3
        m.tool[i].focused = (m.zone = "tools" and i = m.toolIndex)
    end for
end sub

' The right column's first part with something in it, or "".
function columnFirst() as String
    if m.dym.visible then return "dym"
    if m.tools.visible then return "tools"
    if m.results.content <> invalid and m.results.content.GetChildCount() > 0 then return "results"
    return ""
end function

sub focusZone(z as String)
    m.zone = z
    m.dym.focused = (z = "dym")
    showToolFocus()
    if z = "keyboard" then
        m.keyboard.SetFocus(true)
    else if z = "suggest" then
        m.suggest.SetFocus(true)
    else if z = "results" then
        m.results.SetFocus(true)
    else
        m.keys.SetFocus(true)
    end if
    print "[focus] search "; z; " "; m.toolIndex
end sub

sub onSuggestTimer()
    t = m.keyboard.text.Trim()
    if t = "" or t = m.suggestHidden then
        showSuggestions([])
        return
    end if
    m.suggestHidden = ""
    ui_Api("GET", "/api/search/suggest" + NetQuery({q: t}), invalid, "s:" + t, "onSuggest")
end sub

sub onSuggest(ev as Object)
    t = m.keyboard.text.Trim()
    if ev.GetRoSGNode().tag <> "s:" + t or t = m.suggestHidden then return
    r = ev.GetData()
    items = []
    if r.status = 200 then items = SearchSuggestions(r.data, SearchSuggestMax())
    showSuggestions(items)
end sub

sub showSuggestions(items as Object)
    m.suggestions = []
    texts = []
    kinds = []
    for each it in items
        m.suggestions.Push(it)
        texts.Push(SearchSuggestionText(m.i18n, it))
        kinds.Push(it.kind)
    end for
    m.suggest.content = ui_ListContent(texts)
    m.suggest.visible = (texts.Count() > 0)
    if texts.Count() > 0 then print "[search] suggestions "; kinds.Join(",")
    if m.zone = "suggest" and not m.suggest.visible then focusZone("keyboard")
end sub

sub onSuggestFocus()
    if m.zone = "suggest" then print "[focus] search suggest item "; m.suggest.itemFocused
end sub

' A suggestion: a title opens its video; any other is searched (new words: no filters).
sub onSuggestion()
    i = m.suggest.itemSelected
    if i < 0 or i >= m.suggestions.Count() then return
    it = m.suggestions[i]
    print "[search] chose "; it.kind
    if it.kind = "title" and it.videoId <> "" then
        ui_Navigate({to: "VideoScreen", params: {id: it.videoId}})
        return
    end if
    m.suggestHidden = it.text.Trim()
    showSuggestions([])
    focusZone("keyboard")
    m.keyboard.text = it.text
end sub

' Did you mean: the corrected words, with the same order and filters.
sub runCorrected()
    d = m.dymText
    if d = "" then return
    print "[search] did you mean "; d
    m.selFor = d
    m.suggestHidden = d
    m.q = d
    runSearch()
    m.keyboard.text = d
end sub

sub onTool()
    if m.toolIndex = 3 then
        m.sheetWant = invalid
        m.sheetAt = 0
        showSheet(0, invalid)
        return
    end if
    m.sort = SearchSorts()[m.toolIndex]
    updateTools()
    runSearch()
end sub

' The filter sheet: Clear all filters while any is on, then each facet's buckets. The focus
' stays on the row of the bucket last pressed (want: its facet and value), wherever Clear all
' filters coming or going moved it; else on the row at selected.
sub showSheet(selected as Integer, want as Dynamic)
    rows = SearchFacetRows(m.facets, m.sel, m.labels)
    opts = []
    m.sheetRows = []
    if SearchSelCount(m.sel) > 0 then
        opts.Push(ui_Tr("search.filters.clear"))
        m.sheetRows.Push({clear: true})
    end if
    for each r in rows
        if want <> invalid and r.facet = want.facet and r.value = want.value then selected = opts.Count()
        opts.Push(SearchFacetOption(m.i18n, r))
        m.sheetRows.Push(r)
    end for
    if selected >= opts.Count() then selected = opts.Count() - 1
    if selected < 0 then selected = 0
    m.sheet.title = ui_Tr("search.filters.title")
    m.sheet.selected = selected
    m.sheet.options = opts
    if not m.sheet.visible then
        m.sheet.visible = true
        m.sheet.SetFocus(true)
    end if
    print "[search] sheet "; opts.Count(); " at "; selected
end sub

sub onSheet()
    i = m.sheet.choice
    if i < 0 or i >= m.sheetRows.Count() then
        m.sheet.visible = false
        focusZone("tools")
        return
    end if
    r = m.sheetRows[i]
    m.sheetAt = i
    if r.DoesExist("clear") then
        m.sel = SearchNoSel()
        m.sheetWant = invalid
    else
        m.sel = SearchToggle(m.sel, r.facet, r.value)
        m.sheetWant = {facet: r.facet, value: r.value}
    end if
    print "[search] filter "; SearchSelCount(m.sel)
    updateTools()
    showSheet(m.sheetAt, m.sheetWant)
    runSearch()
end sub

sub onResult()
    it = ui_GridCard(m.results)
    if it <> invalid and it.kind <> "blank" then ui_CardAction(it, "open")
end sub

sub onTakeFocus()
    if m.sheet.visible then
        m.sheet.SetFocus(true)
    else
        focusZone(m.zone)
    end if
end sub

function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press or m.sheet.visible then return false
    k = m.layout.Key(key)
    z = m.zone
    if z = "keyboard" or z = "suggest" then
        if k = "right" then
            first = columnFirst()
            if first <> "" then
                focusZone(first)
                return true
            end if
        else if z = "keyboard" and key = "down" and m.suggest.visible then
            focusZone("suggest")
            return true
        else if z = "suggest" and (key = "up" or key = "back") then
            focusZone("keyboard")
            return true
        end if
        return false
    end if
    if key = "back" then
        focusZone("keyboard")
        return true
    end if
    if z = "dym" then
        if key = "OK" then
            runCorrected()
        else if k = "left" then
            focusZone("keyboard")
        else if key = "down" then
            if m.tools.visible then
                focusZone("tools")
            else if m.results.content <> invalid and m.results.content.GetChildCount() > 0 then
                focusZone("results")
            end if
        end if
        return true
    end if
    if z = "tools" then
        if k = "left" then
            if m.toolIndex > 0 then
                m.toolIndex = m.toolIndex - 1
                focusZone("tools")
            else
                focusZone("keyboard")
            end if
        else if k = "right" then
            if m.toolIndex < lastTool() then
                m.toolIndex = m.toolIndex + 1
                focusZone("tools")
            end if
        else if key = "up" then
            if m.dym.visible then focusZone("dym")
        else if key = "down" then
            if m.results.content <> invalid and m.results.content.GetChildCount() > 0 then focusZone("results")
        else if key = "OK" then
            onTool()
        end if
        return true
    end if
    ' the results: Up from the first row reaches the order, Left from the edge the keyboard
    if k = "left" then
        focusZone("keyboard")
        return true
    end if
    if key = "up" then
        if m.tools.visible then
            focusZone("tools")
        else if m.dym.visible then
            focusZone("dym")
        end if
        return true
    end if
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
    return false
end function
