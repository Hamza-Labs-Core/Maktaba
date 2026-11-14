sub init()
    ui_Init()
    m.title = m.top.FindNode("title")
    m.list = m.top.FindNode("list")
    ui_Label(m.title, 38, ui_Colors().fg, true)
    ui_List(m.list)
    ' the panel opens from the reading end: the right in English, the left in Arabic
    ui_Mirror(["panel", "title", "list"])
    m.list.ObserveField("itemSelected", "onSelected")
    m.top.ObserveField("focusedChild", "onFocus")
end sub

sub onFocus()
    if m.top.HasFocus() then m.list.SetFocus(true)
end sub

sub onItem()
    it = m.top.item
    m.title.text = it.title
    m.keys = []
    titles = []
    m.keys.Push("play")
    titles.Push(ui_Tr("action.play"))
    if it.resume <> invalid and it.resume > 0 then
        m.keys.Push("over")
        titles.Push(ui_Tr("video.startOver"))
    end if
    if it.seriesId <> invalid and it.seriesId <> "" and it.kind <> "series" then
        m.keys.Push("series")
        titles.Push(ui_Tr("action.goToSeries"))
    end if
    m.keys.Push("open")
    titles.Push(ui_Tr("action.open"))
    m.keys.Push("cancel")
    titles.Push(ui_Tr("action.cancel"))
    m.list.content = ui_ListContent(titles)
    print "[actions] "; m.keys.Join(",")
end sub

sub onSelected()
    m.top.choice = m.keys[m.list.itemSelected]
end sub

function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return true
    if key = "back" or key = "options" then
        m.top.choice = "cancel"
    end if
    ' nothing under the dialog moves while it is open
    return true
end function
