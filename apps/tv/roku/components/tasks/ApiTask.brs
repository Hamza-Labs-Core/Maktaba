sub init()
    m.top.functionName = "runTask"
end sub

sub runTask()
    port = CreateObject("roMessagePort")
    m.top.ObserveField("request", port)
    m.client = invalid
    m.homeId = ""
    m.last = invalid
    ' a Task started for its first call (AI mode's, story 49.14) may be given it before it
    ' observes the field: that call is sent now, and its event, if one comes too, is not
    first = m.top.request
    if first <> invalid then send(first)
    while true
        msg = Wait(0, port)
        if type(msg) = "roSGNodeEvent" and msg.GetField() = "request" then
            req = msg.GetData()
            if req <> invalid then send(req)
        end if
    end while
end sub

sub send(req as Object)
    if m.last <> invalid and m.last.IsSameNode(req) then return
    m.last = req
    s = m.global.session
    if s = invalid or s.base = invalid then
        req.response = {status: 401, data: invalid}
        return
    end if
    client = m.client
    ' a following Task takes the tokens the scene holds now: the main Task may have renewed them
    ' since this one's last call
    stale = m.top.follow and client <> invalid and client.session.access <> s.access
    if client = invalid or m.homeId <> s.id or client.session.base <> s.base or stale then
        t = NetUrlTransport(m.top.timeoutMs)
        t.node = m.top
        client = ApiClientNew({base: s.base, access: s.access, refresh: s.refresh, plain: s.plain}, t)
        m.client = client
        m.homeId = s.id
    end if
    client.transport.tag = req.tag
    client.noRefresh = m.top.follow
    body = invalid
    if req.hasBody then body = req.body
    r = client.Request(req.method, req.path, body)
    ' a following Task's 401: the main Task renews the tokens; when it has since this call
    ' was sent, the call is sent once more with them
    if r.status = 401 and m.top.follow then
        s = m.global.session
        if s <> invalid and s.access <> invalid and s.access <> "" and s.access <> client.session.access then
            client.session.access = s.access
            client.session.refresh = s.refresh
            r = client.Request(req.method, req.path, body)
        end if
    end if
    print "[api] "; req.method; " "; HttpRedact(req.path); " "; r.status
    if client.changed then
        client.changed = false
        StoreSaveTokens(m.homeId, client.session.access, client.session.refresh)
        m.top.tokens = {id: m.homeId, access: client.session.access, refresh: client.session.refresh}
    end if
    resp = {status: r.status, data: r.data}
    if req.raw then resp.text = r.body
    req.response = resp
    if client.tooOld then
        client.tooOld = false
        m.top.tooOld = true
    end if
    if client.signedOut then
        m.client = invalid
        m.top.signedOut = true
    end if
end sub
