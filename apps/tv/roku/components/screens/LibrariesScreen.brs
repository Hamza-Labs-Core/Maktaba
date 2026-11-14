sub init()
    ui_Init()
    m.title = m.top.FindNode("title")
    m.list = m.top.FindNode("list")
    ui_Label(m.title, 56, ui_Colors().fg, true)
    ui_List(m.list)
    ui_Mirror(["title", "list"])
    m.title.text = ui_Tr("nav.libraries")
    m.list.ObserveField("itemSelected", "onSelected")
    m.top.ObserveField("params", "onParams")
    m.top.ObserveField("takeFocus", "onTakeFocus")
    m.libs = []
end sub

sub onParams()
    ui_Api("GET", "/api/libraries", invalid, "libs", "onLibs")
end sub

sub onLibs(ev as Object)
    r = ev.GetData()
    m.libs = []
    titles = []
    if r.status = 200 and r.data <> invalid and type(r.data.items) = "roArray" then
        for each l in r.data.items
            m.libs.Push(l)
            titles.Push(ui_Vis(l.name, 40))
        end for
    end if
    m.list.content = ui_ListContent(titles)
    if m.top.visible then m.list.SetFocus(true)
    print "[screen] LibrariesScreen ready libraries="; m.libs.Count()
end sub

sub onSelected()
    l = m.libs[m.list.itemSelected]
    ui_Navigate({to: "LibraryScreen", params: {id: l.id, name: l.name}})
end sub

sub onTakeFocus()
    m.list.SetFocus(true)
end sub
