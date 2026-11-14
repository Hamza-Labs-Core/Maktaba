sub init()
    ui_Init()
    c = ui_Colors()
    m.title = m.top.FindNode("title")
    m.state = m.top.FindNode("state")
    m.list = m.top.FindNode("list")
    ui_Label(m.title, 56, c.fg, true)
    ui_Label(m.state, 34, c.muted, false)
    ui_List(m.list)
    ui_Mirror(["title", "state", "list"])
    m.title.text = ui_Tr("launcher.find.title")
    m.list.ObserveField("itemSelected", "onSelected")
    m.top.ObserveField("params", "onParams")
    m.top.ObserveField("takeFocus", "onTakeFocus")
end sub

sub onParams()
    probe()
end sub

sub probe()
    m.state.text = ui_Tr("launcher.find.searching")
    m.list.content = ui_ListContent([])
    m.homes = []
    t = CreateObject("roSGNode", "HomeTask")
    cmd = {kind: "probe"}
    ' a test build names the network to probe (the manifest's maktaba_probe_*); a store build
    ' has neither, and probes the TV's own /24 on the home's web port
    info = CreateObject("roAppInfo")
    ip = info.GetValue("maktaba_probe_ip")
    if ip <> invalid and ip <> "" then cmd.ip = ip
    port = info.GetValue("maktaba_probe_port")
    if port <> invalid and port <> "" then cmd.port = port.ToInt()
    t.command = cmd
    t.ObserveField("result", "onProbed")
    m.task = t
    t.control = "RUN"
end sub

sub onProbed(ev as Object)
    r = ev.GetData()
    titles = []
    m.homes = []
    if r.noIp = true then
        m.state.text = ui_Tr("launcher.find.noIp")
    else if r.homes.Count() = 0 then
        m.state.text = ui_Tr("launcher.find.none")
    else
        m.state.text = ui_Tr("launcher.find.found")
        for each h in r.homes
            titles.Push(ui_Vis(h.name, 40))
            m.homes.Push(h)
        end for
    end if
    titles.Push(ui_Tr("launcher.find.again"))
    m.list.content = ui_ListContent(titles)
    if m.top.visible then m.list.SetFocus(true)
    print "[screen] FindScreen ready found="; m.homes.Count()
end sub

sub onSelected()
    i = m.list.itemSelected
    if i >= m.homes.Count() then
        probe()
        return
    end if
    h = m.homes[i]
    m.state.text = m.i18n.Format("launcher.connecting", [ui_Vis(h.name, 30)])
    t = CreateObject("roSGNode", "HomeTask")
    t.command = {kind: "choose", foundAt: h.foundAt, answer: h.answer}
    t.ObserveField("result", "onChosen")
    m.task = t
    t.control = "RUN"
end sub

sub onChosen(ev as Object)
    r = ev.GetData()
    if r.verdict <> "ok" then
        ui_Navigate({action: "compat", verdict: r.verdict, home: {id: r.answer.id, name: r.answer.name, base: r.base}})
        return
    end if
    ui_Navigate({to: "CodeScreen", params: {mode: "home", base: r.base, plain: r.plain, id: r.answer.id, name: r.answer.name}})
end sub

sub onTakeFocus()
    m.list.SetFocus(true)
end sub
