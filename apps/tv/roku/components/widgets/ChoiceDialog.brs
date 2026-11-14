' A list to choose from (story 58.13). options are visual strings; choice is the index chosen,
' or -1 for Back.

sub init()
    ui_Init()
    m.title = m.top.FindNode("title")
    m.list = m.top.FindNode("list")
    ui_Label(m.title, 38, ui_Colors().fg, true)
    ui_List(m.list)
    ui_Mirror(["panel", "title", "list"])
    m.list.ObserveField("itemSelected", "onSelected")
    m.top.ObserveField("focusedChild", "onFocus")
end sub

sub onFocus()
    if m.top.HasFocus() then m.list.SetFocus(true)
end sub

sub onTitle()
    m.title.text = m.top.title
end sub

sub onOptions()
    m.list.content = ui_ListContent(m.top.options)
    if m.top.selected >= 0 and m.top.selected < m.top.options.Count() then m.list.jumpToItem = m.top.selected
end sub

sub onSelected()
    m.top.choice = m.list.itemSelected
end sub

function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return true
    if key = "back" then m.top.choice = -1
    ' nothing under the dialog moves while it is open
    return true
end function
