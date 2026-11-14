sub init()
    ui_Init()
    c = ui_Colors()
    m.title = m.top.FindNode("title")
    m.note = m.top.FindNode("note")
    m.list = m.top.FindNode("list")
    ui_Label(m.title, 56, c.fg, true)
    ui_Label(m.note, 32, c.muted, false)
    ui_List(m.list)
    ui_Mirror(["title", "note", "list"])
    m.title.text = ui_Tr("launcher.start.title")
    m.list.ObserveField("itemSelected", "onSelected")
    m.top.ObserveField("params", "onParams")
    m.top.ObserveField("takeFocus", "onTakeFocus")
end sub

sub onParams()
    p = m.top.params
    if p.unreachable <> invalid then
        m.note.text = m.i18n.Format("launcher.unreachable.title", [ui_Vis(p.unreachable.name, 30)])
    end if
    ' the saved homes, then the ways to add one: a code first (the default for a new TV)
    m.homes = StoreHomes()
    m.actions = []
    titles = []
    for each h in m.homes
        titles.Push(ui_Vis(h.name, 40))
        m.actions.Push({kind: "home", home: h})
    end for
    for each a in [["code", "code.title"], ["find", "launcher.find.title"], ["typed", "launcher.typed.title"]]
        titles.Push(ui_Tr(a[1]))
        m.actions.Push({kind: a[0]})
    end for
    m.list.content = ui_ListContent(titles)
    ' a new TV starts on Sign in with a code
    first = 0
    if m.homes.Count() = 0 then first = 0
    m.list.jumpToItem = first
    print "[screen] StartScreen ready homes="; m.homes.Count()
    ui_Navigate({action: "drawn", screen: "StartScreen"})
end sub

sub onTakeFocus()
    m.list.SetFocus(true)
end sub

sub onSelected()
    a = m.actions[m.list.itemSelected]
    print "[start] "; a.kind
    if a.kind = "home" then
        if a.home.refresh <> invalid and a.home.refresh <> "" then
            ui_Navigate({action: "connect", home: a.home})
        else
            ' signed out of a saved home: its own code again, at its saved address
            ui_Navigate({to: "CodeScreen", params: {mode: "home", base: a.home.base, plain: (a.home.plain = true), id: a.home.id, name: a.home.name}})
        end if
    else if a.kind = "code" then
        ui_Navigate({to: "CodeScreen", params: {mode: "cloud"}})
    else if a.kind = "find" then
        ui_Navigate({to: "FindScreen", params: {}})
    else if a.kind = "typed" then
        ui_Navigate({to: "AddressScreen", params: {}})
    end if
end sub
