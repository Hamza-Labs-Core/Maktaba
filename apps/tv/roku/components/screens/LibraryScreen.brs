' A library (Epic 56 section 6.5, on Roku; story 58.12). Focus is in one of four places: the tab
' bar, Play and Shuffle, the tab's content, or the Library tab's letter rail. Each tab keeps
' its focus when the person comes back to it. Back: the tab's top, then the tabs, then out.

function tab_Keys() as Object
    return ["recommended", "library", "collections", "playlists", "categories"]
end function

sub init()
    ui_Init()
    c = ui_Colors()
    m.title = m.top.FindNode("title")
    m.tabs = m.top.FindNode("tabs")
    m.play = m.top.FindNode("play")
    m.shuffle = m.top.FindNode("shuffle")
    m.grid = m.top.FindNode("grid")
    m.rail = m.top.FindNode("rail")
    m.list = m.top.FindNode("list")
    m.rows = m.top.FindNode("rows")
    m.empty = m.top.FindNode("empty")
    ui_Label(m.title, 48, c.fg, true)
    ui_Label(m.empty, 34, c.muted, false)
    ui_List(m.list)
    ui_List(m.rail)
    m.rail.textHorizAlign = "center"
    ' the row titles in the bundled font: the Roku system font has no Arabic
    m.rows.rowLabelFont = ui_Font(34, true)
    m.rows.rowLabelColor = c.fg
    m.rows.drawFocusFeedback = false
    m.grid.caption1Font = ui_Font(24, false)
    m.grid.caption1Color = c.fg
    m.grid.focusBitmapBlendColor = c.accent
    m.play.text = ui_Tr("action.play")
    m.shuffle.text = ui_Tr("action.shuffle")
    ui_Mirror(["title", "play", "shuffle", "grid", "rail", "list", "rows", "empty"])
    names = []
    for each k in tab_Keys()
        names.Push(ui_Tr("library.tab." + k))
    end for
    m.tabs.items = names
    m.tab = 0
    m.zone = "tabs"
    m.button = 0
    m.memory = {}
    m.filters = {}
    m.letter = ""
    m.pages = []
    m.pageSize = 36
    m.next = invalid
    m.loadingMore = false
    m.tabs.ObserveField("selected", "onTab")
    m.grid.ObserveField("itemFocused", "onGridFocus")
    m.grid.ObserveField("itemSelected", "onGridSelected")
    m.rows.ObserveField("rowItemSelected", "onRowSelected")
    m.list.ObserveField("itemSelected", "onListSelected")
    m.rail.ObserveField("itemSelected", "onLetter")
    m.top.ObserveField("params", "onParams")
    m.top.ObserveField("takeFocus", "onTakeFocus")
end sub

sub onParams()
    p = m.top.params
    m.libId = p.id
    m.title.text = ui_Vis(p.name, 50)
    m.tabs.selected = m.tab
    showTab()
end sub

sub onTab()
    if m.tabs.selected = m.tab then return
    remember()
    m.tab = m.tabs.selected
    showTab()
end sub

' Shows the current tab: its content loaded from the home (each tab once a visit).
sub showTab()
    key = tab_Keys()[m.tab]
    print "[library] tab "; key
    m.grid.visible = false
    m.rail.visible = false
    m.list.visible = false
    m.rows.visible = false
    m.empty.visible = false
    m.play.visible = (key = "library")
    m.shuffle.visible = (key = "library")
    lib = m.libId
    if key = "recommended" then
        ui_Api("GET", "/api/libraries/" + lib + "/hub", invalid, "hub", "onHub")
    else if key = "library" then
        m.letter = ""
        if m.filters.letter <> invalid then m.letter = m.filters.letter
        loadGrid()
        ui_Api("GET", "/api/videos/facets" + NetQuery({library: lib}), invalid, "facets", "onFacets")
    else if key = "collections" then
        ui_Api("GET", "/api/libraries/" + lib + "/collections" + NetQuery({limit: 200}), invalid, "collections", "onCollections")
    else if key = "playlists" then
        ui_Api("GET", "/api/me/playlists" + NetQuery({library: lib}), invalid, "playlists", "onPlaylists")
    else if key = "categories" then
        ui_Api("GET", "/api/libraries/" + lib + "/categories", invalid, "categories", "onCategories")
    end if
end sub

sub remember()
    key = tab_Keys()[m.tab]
    if key = "recommended" then
        m.memory[key] = m.rows.rowItemFocused
    else if key = "categories" then
        m.memory[key] = m.list.itemFocused
    else
        m.memory[key] = m.grid.itemFocused
    end if
end sub

' The Library tab's query: the view's filters and the letter, sorted by title.
function gridQuery(cursor as Dynamic) as Object
    q = {library: m.libId, limit: m.pageSize, sort: "title", order: "asc"}
    for each k in m.filters
        if k <> "letter" then q[k] = m.filters[k]
    end for
    if m.letter <> "" then q.letter = m.letter
    if cursor <> invalid then q.cursor = cursor
    return q
end function

sub loadGrid()
    m.pages = []
    m.next = invalid
    m.gridItems = 0
    m.grid.content = CreateObject("roSGNode", "ContentNode")
    ui_Api("GET", "/api/videos" + NetQuery(gridQuery(invalid)), invalid, "grid", "onGridPage")
end sub

sub onGridPage(ev as Object)
    r = ev.GetData()
    m.loadingMore = false
    if r.status <> 200 or r.data = invalid then
        showEmpty()
        return
    end if
    items = []
    if type(r.data.items) = "roArray" then items = r.data.items
    m.next = r.data.next
    if m.next = "" then m.next = invalid
    page = m.pages.Count()
    m.pages.Push(page)
    ui_GridItems(m.grid.content, items, "video", 6, (m.next = invalid))
    m.gridItems = m.grid.content.GetChildCount()
    if m.gridItems = 0 then
        showEmpty()
        return
    end if
    m.grid.visible = true
    m.rail.visible = true
    if page = 0 then
        restoreGrid()
        print "[screen] LibraryScreen ready tab=library items="; m.gridItems
    end if
    windowPosters()
end sub

' At most three pages of posters loaded (a low-end Roku's channel memory): the focused page and
' one each side keep their posters; the rest are emptied and filled again on the way back.
sub windowPosters()
    focus = m.grid.itemFocused
    if focus < 0 then focus = 0
    w = GridWindow(focus, m.pageSize, m.gridItems, m.pages)
    keep = {}
    for each p in w.keep
        keep[p.ToStr()] = true
    end for
    loaded = 0
    for i = 0 to m.gridItems - 1
        it = m.grid.content.GetChild(i)
        p = Int(i / m.pageSize)
        if keep.DoesExist(p.ToStr()) then
            if it.HDPosterUrl <> it.poster then it.HDPosterUrl = it.poster
            if it.poster <> "" and it.poster <> invalid then loaded = loaded + 1
        else if it.HDPosterUrl <> "" then
            it.HDPosterUrl = ""
        end if
    end for
    print "[grid] posters loaded "; loaded; " of "; m.gridItems
end sub

sub onGridFocus()
    f = m.grid.itemFocused
    print "[focus] library grid "; f
    if tab_Keys()[m.tab] <> "library" then return
    windowPosters()
    ' the next page when focus nears the end
    if m.next <> invalid and not m.loadingMore and f >= m.gridItems - 12 then
        m.loadingMore = true
        ui_Api("GET", "/api/videos" + NetQuery(gridQuery(m.next)), invalid, "grid", "onGridPage")
    end if
end sub

sub onFacets(ev as Object)
    r = ev.GetData()
    m.letters = [""]
    titles = [ui_Tr("library.allLetters")]
    if r.status = 200 and r.data <> invalid and type(r.data.letters) = "roArray" then
        for each l in r.data.letters
            if l.letter <> invalid and l.letter <> "" then
                m.letters.Push(l.letter)
                titles.Push(VisualText(l.letter, 3))
            end if
        end for
    end if
    m.rail.content = ui_ListContent(titles)
end sub

sub onLetter()
    m.letter = m.letters[m.rail.itemSelected]
    print "[library] letter "; m.letter
    m.memory.library = 0
    loadGrid()
end sub

sub onHub(ev as Object)
    r = ev.GetData()
    root = CreateObject("roSGNode", "ContentNode")
    if r.status = 200 and r.data <> invalid and r.data.rows <> invalid then
        rows = r.data.rows
        for each pair in [["continue_watching", "home.continueWatching"], ["recently_added", "home.recentlyAdded"], ["on_deck", "library.onDeck"], ["recently_watched", "library.recentlyWatched"]]
            list = rows[pair[0]]
            if type(list) = "roArray" and list.Count() > 0 then ui_Row(root, ui_Tr(pair[1]), list, "video")
        end for
    end if
    if root.GetChildCount() = 0 then
        showEmpty()
        return
    end if
    m.rows.content = root
    m.rows.visible = true
    ui_RowsReady(m.rows, 0)
    f = m.memory.recommended
    if f <> invalid then
        m.rows.jumpToRowItem = f
    else
        m.rows.jumpToRowItem = [0, m.layout.StartIndex(root.GetChild(0).GetChildCount())]
    end if
    print "[screen] LibraryScreen ready tab=recommended rows="; root.GetChildCount()
    ui_Navigate({action: "drawn", screen: "LibraryScreen"})
end sub

sub onCollections(ev as Object)
    r = ev.GetData()
    showGridOf(r, "collection")
end sub

sub onPlaylists(ev as Object)
    r = ev.GetData()
    showGridOf(r, "playlist")
end sub

sub showGridOf(r as Object, kind as String)
    items = []
    if r.status = 200 and r.data <> invalid and type(r.data.items) = "roArray" then items = r.data.items
    if items.Count() = 0 then
        showEmpty()
        return
    end if
    root = CreateObject("roSGNode", "ContentNode")
    ui_GridItems(root, items, kind, 6, true)
    for i = 0 to root.GetChildCount() - 1
        it = root.GetChild(i)
        it.library = m.libId
    end for
    ' a collection's own kind (topic, clip, or a plain collection) picks its items' route
    kinds = {}
    for each src in items
        if src.id <> invalid and src.kind <> invalid then kinds[src.id] = src.kind
    end for
    for i = 0 to root.GetChildCount() - 1
        it = root.GetChild(i)
        if it.kind <> "blank" and kinds.DoesExist(it.itemId) then it.subkind = kinds[it.itemId]
    end for
    m.grid.content = root
    m.gridItems = root.GetChildCount()
    m.grid.visible = true
    restoreGrid()
    print "[screen] LibraryScreen ready tab="; kind; " items="; m.gridItems
end sub

sub restoreGrid()
    f = m.memory[tab_Keys()[m.tab]]
    if f = invalid or f < 0 or f >= m.gridItems then f = m.layout.StartIndex(m_Min(6, m.gridItems))
    m.grid.jumpToItem = f
end sub

sub onCategories(ev as Object)
    r = ev.GetData()
    m.categories = []
    titles = []
    if r.status = 200 and r.data <> invalid then
        for each group in [["speakers", "speaker"], ["topics", "tag"], ["languages", "language"], ["years", "year"]]
            list = r.data[group[0]]
            if type(list) = "roArray" then
                for each c in list
                    label = c.label
                    if label = invalid or label = "" then label = c.value
                    if type(label) <> "roString" and type(label) <> "String" then label = label.ToStr()
                    value = c.value
                    if type(value) <> "roString" and type(value) <> "String" then value = value.ToStr()
                    titles.Push(ui_Vis(label, 40))
                    m.categories.Push({key: group[1], value: value})
                end for
            end if
        end for
    end if
    if titles.Count() = 0 then
        showEmpty()
        return
    end if
    m.list.content = ui_ListContent(titles)
    m.list.visible = true
    f = m.memory.categories
    if f <> invalid then m.list.jumpToItem = f
    print "[screen] LibraryScreen ready tab=categories items="; titles.Count()
end sub

' A category opens the Library tab with that filter.
sub onListSelected()
    c = m.categories[m.list.itemSelected]
    print "[library] category "; c.key; "="; c.value
    m.filters = {}
    m.filters[c.key] = c.value
    m.memory.library = 0
    m.tab = 1
    m.tabs.selected = 1
    showTab()
    m.zone = "content"
    focusZone()
end sub

sub showEmpty()
    m.empty.text = ui_Tr("library.empty")
    m.empty.visible = true
    print "[screen] LibraryScreen ready tab="; tab_Keys()[m.tab]; " empty"
end sub

function m_Min(a as Integer, b as Integer) as Integer
    if a < b then return a
    return b
end function

sub onGridSelected()
    it = ui_GridCard(m.grid)
    if it = invalid or it.kind = "blank" then return
    ui_CardAction(it, "open")
end sub

sub onRowSelected()
    ui_CardAction(ui_RowCard(m.rows), "open")
end sub

sub onTakeFocus()
    focusZone()
end sub

' The content node of the current tab.
function contentNode() as Object
    key = tab_Keys()[m.tab]
    if key = "recommended" then return m.rows
    if key = "categories" then return m.list
    return m.grid
end function

function contentReady() as Boolean
    n = contentNode()
    return n.visible
end function

sub focusZone()
    m.tabs.focused = (m.zone = "tabs")
    m.play.focused = (m.zone = "buttons" and m.button = 0)
    m.shuffle.focused = (m.zone = "buttons" and m.button = 1)
    if m.zone = "tabs" then
        m.tabs.SetFocus(true)
    else if m.zone = "buttons" then
        ' the button itself takes focus (its keys come up to this screen)
        if m.button = 0 then
            m.play.SetFocus(true)
        else
            m.shuffle.SetFocus(true)
        end if
    else if m.zone = "rail" then
        m.rail.SetFocus(true)
    else
        contentNode().SetFocus(true)
    end if
    print "[focus] library "; m.zone; " tab="; tab_Keys()[m.tab]
end sub

function atTop() as Boolean
    key = tab_Keys()[m.tab]
    if key = "recommended" then
        f = m.rows.rowItemFocused
        if f = invalid or m.rows.content = invalid then return true
        return f[0] = 0 and f[1] = m.layout.StartIndex(m.rows.content.GetChild(0).GetChildCount())
    end if
    if key = "categories" then return m.list.itemFocused <= 0
    return m.grid.itemFocused < 6
end function

sub toTop()
    key = tab_Keys()[m.tab]
    if key = "recommended" then
        m.rows.jumpToRowItem = [0, m.layout.StartIndex(m.rows.content.GetChild(0).GetChildCount())]
    else if key = "categories" then
        m.list.jumpToItem = 0
    else
        m.grid.jumpToItem = m.layout.StartIndex(m_Min(6, m.gridItems))
    end if
end sub

' Whether the grid's focus is on the column next to the rail (the right in English).
function gridAtRailEdge() as Boolean
    f = m.grid.itemFocused
    if f < 0 then return false
    col = f mod 6
    if m.rtl then return col = 0
    return col = 5 or f = m.gridItems - 1
end function

function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    if key = "back" then
        go = NavBack("library", m.zone = "tabs", m.zone = "buttons" or m.zone = "rail" or atTop())
        print "[back] library "; go
        if go = "top" then
            toTop()
            return true
        else if go = "tabs" then
            remember()
            m.zone = "tabs"
            focusZone()
            return true
        end if
        return false
    end if
    k = m.layout.Key(key)
    if m.zone = "tabs" then
        if key = "down" then
            if m.play.visible then
                m.zone = "buttons"
                m.button = 0
            else if contentReady() then
                m.zone = "content"
            end if
            focusZone()
            return true
        end if
        return false
    end if
    if m.zone = "buttons" then
        if k = "right" and m.button = 0 then
            m.button = 1
        else if k = "left" and m.button = 1 then
            m.button = 0
        else if key = "up" then
            m.zone = "tabs"
        else if key = "down" and contentReady() then
            m.zone = "content"
        else if key = "OK" then
            ui_PlayQueue({kind: "library", id: m.libId, filters: gridQuery(invalid)}, (m.button = 1))
            return true
        else
            return true
        end if
        focusZone()
        return true
    end if
    if m.zone = "rail" then
        if k = "left" then
            m.zone = "content"
            focusZone()
        end if
        if key = "up" then return true
        return k = "left"
    end if
    ' the content
    if key = "up" then
        if m.play.visible then
            m.zone = "buttons"
        else
            m.zone = "tabs"
        end if
        focusZone()
        return true
    end if
    if k = "right" and tab_Keys()[m.tab] = "library" and m.rail.visible and gridAtRailEdge() then
        m.zone = "rail"
        focusZone()
        return true
    end if
    action = NavCardKey(key)
    if action = "play" or action = "actions" then
        card = invalid
        if tab_Keys()[m.tab] = "recommended" then
            card = ui_RowCard(m.rows)
        else if m.grid.visible then
            card = ui_GridCard(m.grid)
        end if
        if card = invalid or card.kind = "blank" then return true
        if action = "play" then
            ui_CardAction(card, "play")
        else
            ui_ShowActions(card, contentNode())
        end if
        return true
    end if
    return false
end function
