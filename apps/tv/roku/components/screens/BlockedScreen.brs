' This isn't available (Epic 58 story 58.14), for a sign-in under a limit: never the title or
' picture of what was asked for (the home sends none). Ask a parent has the focus; the request
' is then followed (every REQUEST_POLL_SEC while this screen shows), and a parent's yes opens the
' item by itself, without a restart. Check again asks the home at once. No parent PIN: a
' guardian answers from Maktaba on their phone, as the line under the button says.
'
'   POST /api/me/parental/requests   {subject_kind, subject_id}
'   GET  /api/me/parental/requests   what became of the account's own requests

sub init()
    ui_Init()
    c = ui_Colors()
    for each id in ["title", "reason", "state", "hint", "action"]
        m[id] = m.top.FindNode(id)
    end for
    ui_Label(m.title, 52, c.fg, true)
    ui_Label(m.reason, 32, c.muted, false)
    ui_Label(m.state, 32, c.fg, false)
    ui_Label(m.hint, 28, c.muted, false)
    ' everything is centred: nothing to mirror
    for each lbl in [m.title, m.reason, m.state, m.hint]
        lbl.horizAlign = "center"
    end for
    m.title.text = ui_Tr("parental.blocked.title")
    m.ask = "none"
    m.waited = false
    m.poll = CreateObject("roSGNode", "Timer")
    m.poll.duration = REQUEST_POLL_SEC()
    m.poll.repeat = true
    m.poll.ObserveField("fire", "onPoll")
    m.top.ObserveField("params", "onParams")
    m.top.ObserveField("takeFocus", "onTakeFocus")
    m.top.ObserveField("visible", "onVisible")
end sub

function REQUEST_POLL_SEC() as Integer
    return 15
end function

sub onParams()
    p = m.global.session.parental
    m.limited = ParentalLimited(p)
    m.restricted = (type(p) = "roAssociativeArray" and p.restricted = true)
    m.reason.text = ParentalLimitLine(p, m.i18n)
    if m.restricted then
        m.hint.text = ui_Tr("tv.parental.askOnPhone")
        m.action.visible = true
        ' a request made earlier (here or on another device) is followed as it stands
        readRequests()
    else
        m.action.visible = false
    end if
    show()
    print "[screen] BlockedScreen ready kind="; m.top.params.kind; " restricted="; m.restricted
end sub

sub onTakeFocus()
    show()
end sub

sub onVisible()
    if not m.top.visible then m.poll.control = "stop"
end sub

sub show()
    if m.ask = "pending" or m.ask = "approved" then
        m.action.text = ui_Tr("parental.time.check")
    else if m.ask = "denied" then
        m.action.text = ui_Tr("parental.ask.again")
    else
        m.action.text = ui_Tr("parental.blocked.ask")
    end if
    m.state.text = ""
    if m.ask = "pending" then m.state.text = ui_Tr("parental.ask.sent")
    if m.ask = "approved" then m.state.text = ui_Tr("parental.ask.approved")
    if m.ask = "denied" then m.state.text = ui_Tr("parental.ask.denied")
    if m.error <> invalid then m.state.text = m.error
    if m.action.visible then
        m.action.SetFocus(true)
        m.action.focused = true
        print "[focus] blocked "; m.ask
    else
        m.top.SetFocus(true)
    end if
    if m.ask = "pending" and m.top.visible then m.poll.control = "start" else m.poll.control = "stop"
end sub

sub readRequests()
    ui_Api("GET", "/api/me/parental/requests", invalid, "requests", "onRequests")
end sub

sub onPoll()
    readRequests()
end sub

sub onRequests(ev as Object)
    r = ev.GetData()
    if r.status <> 200 then return
    p = m.top.params
    state = ParentalRequestState(r.data, p.kind, p.id)
    if state = "pending" then m.waited = true
    ' a yes opens the item, once, when this screen saw the request wait
    if state = "approved" and m.waited then
        m.ask = "approved"
        m.poll.control = "stop"
        print "[parental] approved "; p.kind
        show()
        if p.kind = "series" then
            ui_Navigate({to: "SeriesScreen", replace: true, params: {id: p.id}})
        else
            ui_Navigate({to: "VideoScreen", replace: true, params: {id: p.id}})
        end if
        return
    end if
    if state = "approved" then state = "none"
    if state = "expired" then state = "none"
    m.ask = state
    show()
end sub

sub sendAsk()
    p = m.top.params
    m.error = invalid
    ui_Api("POST", "/api/me/parental/requests", {subject_kind: p.kind, subject_id: p.id}, "ask", "onAsked")
end sub

sub onAsked(ev as Object)
    r = ev.GetData()
    if r.status = 200 or r.status = 201 then
        m.ask = "pending"
        m.waited = true
        print "[parental] asked"
    else if r.status = 429 then
        m.error = ui_Tr("parental.ask.tooMany")
    else if r.status = 404 then
        ' nothing to ask for: a missing item is answered as one
        m.action.visible = false
        m.hint.text = ""
    else
        m.error = ui_Tr("parental.ask.failed")
    end if
    show()
end sub

function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    if (key = "OK" or key = "play") and m.action.visible then
        if m.ask = "pending" or m.ask = "approved" then
            readRequests()
        else
            sendAsk()
        end if
        return true
    end if
    return false
end function
