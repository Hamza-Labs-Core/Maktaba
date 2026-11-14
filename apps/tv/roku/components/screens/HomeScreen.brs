sub init()
    ui_Init()
    c = ui_Colors()
    m.brand = m.top.FindNode("brand")
    m.menu = m.top.FindNode("menu")
    m.notice = m.top.FindNode("notice")
    m.rows = m.top.FindNode("rows")
    m.hint = m.top.FindNode("hint")
    ui_Label(m.brand, 44, c.accent, true)
    ui_Label(m.notice, 34, c.error, true)
    ui_Label(m.hint, 28, c.muted, false)
    ui_Mirror(["brand", "notice", "rows", "hint"])
    m.rows.rowLabelFont = ui_Font(34, true)
    m.rows.rowLabelColor = c.fg
    m.rows.focusBitmapUri = ""
    m.rows.drawFocusFeedback = false
    m.brand.text = ui_Tr("launcher.title")
    m.hint.text = ui_Tr("roku.options")
    m.menu.items = [ui_Tr("nav.home"), ui_Tr("nav.libraries"), ui_Tr("nav.search"), ui_Tr("nav.account")]
    m.menu.ObserveField("activated", "onMenu")
    m.rows.ObserveField("rowItemSelected", "onCard")
    m.rows.ObserveField("rowItemFocused", "onCardFocus")
    m.inMenu = false
    m.menuChosen = false
    m.top.ObserveField("params", "onParams")
    m.top.ObserveField("takeFocus", "onTakeFocus")
end sub

sub onParams()
    p = m.top.params
    if p.notice <> invalid and p.notice <> "" then
        m.notice.text = ui_Tr(p.notice)
        m.notice.visible = true
        print "[home] notice "; p.notice
    end if
    s = m.global.session
    if s.name <> invalid then m.brand.text = ui_Tr("launcher.title") + "  ·  " + ui_Vis(s.name, 30)
    if m.rtl then m.brand.text = ui_Vis(s.name, 30) + "  ·  " + ui_Tr("launcher.title")
    ui_Api("GET", "/api/home", invalid, "home", "onHome")
end sub

' Continue watching and Recently added come in one answer; For you is loaded after them (the
' RowList loads its rows lazily: Home is drawn before the third row is asked for).
sub onHome(ev as Object)
    r = ev.GetData()
    root = CreateObject("roSGNode", "ContentNode")
    d = r.data
    if r.status = 200 and d <> invalid then
        if type(d.continue_watching) = "roArray" and d.continue_watching.Count() > 0 then ui_Row(root, ui_Tr("home.continueWatching"), d.continue_watching, "video")
        if type(d.recently_added) = "roArray" and d.recently_added.Count() > 0 then ui_Row(root, ui_Tr("home.recentlyAdded"), d.recently_added, "video")
    end if
    m.rows.content = root
    startFocus()
    ' the rows arrive after the screen took focus: the first card gets it, unless the menu was
    ' chosen meanwhile
    if not m.menuChosen then
        m.inMenu = false
        if m.top.visible then onTakeFocus()
    end if
    print "[screen] HomeScreen ready rows="; root.GetChildCount()
    ui_Navigate({action: "drawn", screen: "HomeScreen"})
    ui_Api("GET", "/api/recommendations", invalid, "rec", "onRecommendations")
end sub

sub onRecommendations(ev as Object)
    r = ev.GetData()
    if r.status <> 200 or r.data = invalid or type(r.data.rails) <> "roArray" then return
    m.forYou = []
    for each rail in r.data.rails
        if rail.id = "for-you" and type(rail.items) = "roArray" then
            for each it in rail.items
                if m.forYou.Count() < 12 then m.forYou.Push(it)
            end for
        end if
    end for
    if m.forYou.Count() = 0 then return
    ' the rail names videos; their titles come from each video (12 at most)
    m.forYouDone = []
    m.forYouWaiting = m.forYou.Count()
    for each it in m.forYou
        ui_Api("GET", "/api/videos/" + it.video_id, invalid, it.video_id, "onForYouVideo")
    end for
end sub

sub onForYouVideo(ev as Object)
    r = ev.GetData()
    if r.status = 200 and r.data <> invalid then m.forYouDone.Push(r.data)
    m.forYouWaiting = m.forYouWaiting - 1
    if m.forYouWaiting > 0 then return
    if m.forYouDone.Count() = 0 then return
    ' keep the rail's order
    byId = {}
    for each v in m.forYouDone
        byId[v.id] = v
    end for
    items = []
    for each it in m.forYou
        if byId.DoesExist(it.video_id) then items.Push(byId[it.video_id])
    end for
    ui_Row(m.rows.content, ui_Tr("home.forYou"), items, "video")
    ui_RowsReady(m.rows, m.rows.content.GetChildCount() - 1)
    print "[home] for you "; items.Count()
end sub

sub startFocus()
    if m.rows.content <> invalid and m.rows.content.GetChildCount() > 0 then
        n = m.rows.content.GetChild(0).GetChildCount()
        m.rows.jumpToRowItem = [0, m.layout.StartIndex(n)]
        ui_RowsReady(m.rows, 0)
    end if
end sub

sub onTakeFocus()
    if m.inMenu or m.rows.content = invalid or m.rows.content.GetChildCount() = 0 then
        focusMenu()
    else
        m.menu.focused = false
        m.rows.SetFocus(true)
    end if
end sub

sub focusMenu()
    m.inMenu = true
    m.menu.focused = true
    m.menu.SetFocus(true)
    print "[focus] home menu "; m.menu.selected
end sub

sub onCardFocus()
    f = m.rows.rowItemFocused
    if f <> invalid then print "[focus] home row "; f[0]; " item "; f[1]
end sub

sub onMenu()
    i = m.menu.activated
    print "[home] menu "; i
    if i = 0 then
        m.inMenu = false
        onTakeFocus()
    else if i = 1 then
        ui_Navigate({to: "LibrariesScreen", params: {}})
    else if i = 2 then
        ui_Navigate({to: "SearchScreen", params: {}})
    else if i = 3 then
        ui_Navigate({to: "AccountScreen", params: {}})
    end if
end sub

sub onCard()
    ui_CardAction(ui_RowCard(m.rows), "open")
end sub

function atTop() as Boolean
    f = m.rows.rowItemFocused
    if f = invalid or m.rows.content = invalid or m.rows.content.GetChildCount() = 0 then return true
    n = m.rows.content.GetChild(0).GetChildCount()
    return f[0] = 0 and f[1] = m.layout.StartIndex(n)
end function

function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    if key = "back" then
        go = NavBack("home", m.inMenu, atTop())
        print "[back] home "; go
        if go = "top" then
            startFocus()
            return true
        end if
        return false
    end if
    if m.inMenu then
        if key = "down" and m.rows.content <> invalid and m.rows.content.GetChildCount() > 0 then
            m.inMenu = false
            onTakeFocus()
            return true
        end if
        return false
    end if
    if key = "up" then
        m.menuChosen = true
        focusMenu()
        return true
    end if
    action = NavCardKey(key)
    if action = "play" then
        ui_CardAction(ui_RowCard(m.rows), "play")
        return true
    else if action = "actions" then
        ui_ShowActions(ui_RowCard(m.rows), m.rows)
        return true
    end if
    return false
end function
