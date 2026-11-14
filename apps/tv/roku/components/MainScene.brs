sub init()
    m.top.backgroundColor = "0x0B111CFF"
    m.top.backgroundURI = ""
    m.global.AddFields({lang: StoreLang(), session: {}})
    m.global.AddField("api", "node", false)
    ui_Init()
    m.clock = CreateObject("roTimespan")
    m.stack = m.top.FindNode("stack")
    m.busy = m.top.FindNode("busy")
    ui_Label(m.busy, 36, ui_Colors().muted, false)
    m.busy.horizAlign = "center"
    m.confirm = m.top.FindNode("confirm")
    m.confirm.ObserveField("choice", "onConfirm")
    m.pendingLink = invalid
    m.notice = ""
    m.beaconSent = false
    m.deferTimer = CreateObject("roSGNode", "Timer")
    m.deferTimer.duration = 0.05
    m.deferTimer.repeat = false
    m.deferTimer.ObserveField("fire", "onDeferred")
    api = CreateObject("roSGNode", "ApiTask")
    api.ObserveField("signedOut", "onSignedOut")
    api.ObserveField("tooOld", "onTooOld")
    api.ObserveField("tokens", "onTokens")
    m.global.api = api
    api.control = "RUN"
end sub

' The launch: a deep link waits for a home; the last home used opens at once when it still
' answers as itself, else the start screen.
sub onLaunchArgs()
    m.pendingLink = DeepLinkParse(m.top.launchArgs)
    homes = StoreHomes()
    if homes.Count() > 0 and homes[0].refresh <> invalid and homes[0].refresh <> "" then
        scene_Connect(homes[0])
    else
        scene_Reset("StartScreen", {})
    end if
end sub

' A deep link while the channel runs (voice, Roku search).
sub onInputArgs()
    link = DeepLinkParse(m.top.inputArgs)
    if link = invalid then return
    if m.global.session.base = invalid then
        m.pendingLink = link
        return
    end if
    scene_OpenLink(link)
end sub

' Opens a saved home: its discovery answer first (the same home, and a level this channel can
' use), then Home.
sub scene_Connect(home as Object)
    scene_Busy(m.i18n.Format("launcher.connecting", [ui_Vis(home.name, 30)]))
    t = CreateObject("roSGNode", "HomeTask")
    t.command = {kind: "connect", home: home}
    t.ObserveField("result", "onConnected")
    m.homeTask = t
    t.control = "RUN"
end sub

sub onConnected(ev as Object)
    r = ev.GetData()
    scene_Busy("")
    if r.verdict <> invalid and r.verdict <> "ok" then
        scene_Reset("CompatScreen", {verdict: r.verdict, home: r.home, answer: r.answer})
        return
    end if
    if not r.ok then
        scene_Reset("StartScreen", {unreachable: r.home})
        return
    end if
    scene_SignedIn(r.home)
end sub

sub scene_SignedIn(home as Object)
    StoreSaveHome(home)
    m.global.session = {id: home.id, name: home.name, base: home.base, plain: (home.plain = true), access: home.access, refresh: home.refresh, user: home.user, admin: (home.admin = true)}
    scene_SetAgent()
    link = m.pendingLink
    m.pendingLink = invalid
    if link <> invalid then
        scene_Reset("HomeScreen", {})
        scene_OpenLink(link)
    else
        scene_Reset("HomeScreen", {notice: m.notice})
        m.notice = ""
    end if
end sub

' The scene's HTTP agent: every Poster (and the player's Video node) inherits it, so posters
' carry the bearer and every load trusts the stock certificate bundle.
sub scene_SetAgent()
    agent = CreateObject("roHttpAgent")
    agent.SetCertificatesFile(CertificatesFile())
    agent.InitClientCertificates()
    agent.AddHeader("X-Maktaba-Client", ClientHeader())
    s = m.global.session
    if s.access <> invalid and s.access <> "" then agent.AddHeader("Authorization", "Bearer " + s.access)
    m.top.SetHttpAgent(agent)
end sub

sub onTokens(ev as Object)
    t = ev.GetData()
    s = m.global.session
    if s.id <> t.id then return
    s.access = t.access
    s.refresh = t.refresh
    m.global.session = s
    scene_SetAgent()
end sub

sub onSignedOut()
    s = m.global.session
    if s.id <> invalid then StoreSaveTokens(s.id, "", "")
    m.global.session = {}
    scene_Reset("StartScreen", {})
end sub

sub onTooOld()
    s = m.global.session
    scene_Reset("CompatScreen", {verdict: "update_app", home: s})
end sub

' A deep link: the video is read through the home's own filtered routes; one the person
' cannot open (404 for missing, another library or hidden by parental controls) opens Home
' with the not-available line.
sub scene_OpenLink(link as Object)
    print "[link] "; link.mediaType; " "; link.contentId
    if not link.valid then
        scene_NotAvailable()
        return
    end if
    m.linkReq = ui_Api("GET", "/api/videos/" + link.contentId, invalid, "link", "onLinkVideo")
    m.link = link
end sub

sub onLinkVideo(ev as Object)
    r = ev.GetData()
    action = DeepLinkAction(m.link, r.status, r.data)
    print "[link] "; action
    if action = "play" then
        scene_Reset("HomeScreen", {})
        scene_Push("PlayerScreen", {videoId: r.data.id, start: ResumePosition(r.data)})
    else
        scene_NotAvailable()
    end if
end sub

sub scene_NotAvailable()
    scene_Reset("HomeScreen", {notice: "roku.notAvailable"})
end sub

' The stack: Reset leaves one screen, Push adds one, Pop goes back.
sub scene_Reset(name as String, params as Object)
    m.confirm.visible = false
    while m.stack.GetChildCount() > 0
        m.stack.RemoveChildIndex(m.stack.GetChildCount() - 1)
    end while
    scene_Push(name, params)
end sub

sub scene_Push(name as String, params as Object)
    count = m.stack.GetChildCount()
    if count > 0 then m.stack.GetChild(count - 1).visible = false
    s = CreateObject("roSGNode", name)
    s.ObserveField("navigate", "onNavigate")
    m.stack.AppendChild(s)
    s.params = params
    s.takeFocus = true
    print "[nav] open "; name; " depth="; m.stack.GetChildCount(); " t="; m.clock.TotalMilliseconds()
end sub

sub scene_Pop()
    count = m.stack.GetChildCount()
    if count <= 1 then return
    m.stack.RemoveChildIndex(count - 1)
    top = m.stack.GetChild(count - 2)
    top.visible = true
    top.takeFocus = true
    print "[nav] back to "; top.subtype(); " depth="; m.stack.GetChildCount()
end sub

sub onNavigate(ev as Object)
    nav = ev.GetData()
    ' a screen asking for the stack to be reset is still inside its own callback: the reset
    ' runs on the next tick, so the screen is never removed while its handler runs
    if nav.action = "setLang" or nav.action = "signOut" or nav.action = "start" then
        m.deferred = nav
        m.deferTimer.control = "start"
        return
    end if
    scene_Do(nav)
end sub

sub onDeferred()
    nav = m.deferred
    m.deferred = invalid
    if nav <> invalid then scene_Do(nav)
end sub

sub scene_Do(nav as Object)
    if nav.to <> invalid then
        scene_Push(nav.to, nav.params)
    else if nav.action = "back" then
        scene_Pop()
    else if nav.action = "signedIn" then
        scene_SignedIn(nav.home)
    else if nav.action = "connect" then
        scene_Connect(nav.home)
    else if nav.action = "compat" then
        scene_Reset("CompatScreen", {verdict: nav.verdict, home: nav.home})
    else if nav.action = "start" then
        scene_Reset("StartScreen", {})
    else if nav.action = "signOut" then
        s = m.global.session
        if s.id <> invalid then StoreSaveTokens(s.id, "", "")
        m.global.session = {}
        scene_Reset("StartScreen", {})
    else if nav.action = "setLang" then
        StoreSetLang(nav.lang)
        m.global.lang = nav.lang
        ui_Init()
        scene_Reset("HomeScreen", {})
        scene_Push("AccountScreen", {})
    else if nav.action = "drawn" then
        ' Roku's launch beacon, once: the first screen a person can use is drawn
        if not m.beaconSent then
            m.beaconSent = true
            m.top.signalBeacon("AppLaunchComplete")
            print "[perf] "; nav.screen; " drawn "; m.clock.TotalMilliseconds(); " ms after launch"
        end if
    end if
end sub

sub scene_Busy(text as String)
    m.busy.text = text
    m.busy.visible = (text <> "")
end sub

function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    if key = "back" then
        if m.confirm.visible then return false
        if m.stack.GetChildCount() > 1 then
            scene_Pop()
        else
            ' Back on the first screen leaves the channel, asking once (Roku's certification
            ' allows one confirmation)
            m.confirm.message = ui_Tr("roku.exitConfirm")
            m.confirm.visible = true
            m.confirm.setFocus(true)
            print "[nav] confirm exit"
        end if
        return true
    end if
    return false
end function

sub onConfirm(ev as Object)
    choice = ev.GetData()
    m.confirm.visible = false
    if choice = "leave" then
        m.top.exitChannel = true
    else
        count = m.stack.GetChildCount()
        if count > 0 then m.stack.GetChild(count - 1).takeFocus = true
    end if
end sub
