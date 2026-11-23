sub init()
    m.top.functionName = "runTask"
end sub

sub runTask()
    port = CreateObject("roMessagePort")
    m.top.ObserveField("request", port)
    client = invalid
    homeId = ""
    while true
        msg = Wait(0, port)
        if type(msg) = "roSGNodeEvent" and msg.GetField() = "request" then
            req = msg.GetData()
            if req <> invalid then
                s = m.global.session
                if s = invalid or s.base = invalid then
                    req.response = {status: 401, data: invalid}
                else
                    ' a following Task takes the tokens the scene holds now: the main Task may
                    ' have renewed them since this one's last call
                    stale = m.top.follow and client <> invalid and client.session.access <> s.access
                    if client = invalid or homeId <> s.id or client.session.base <> s.base or stale then
                        t = NetUrlTransport(m.top.timeoutMs)
                        t.node = m.top
                        client = ApiClientNew({base: s.base, access: s.access, refresh: s.refresh, plain: s.plain}, t)
                        homeId = s.id
                    end if
                    client.transport.tag = req.tag
                    body = invalid
                    if req.hasBody then body = req.body
                    r = client.Request(req.method, req.path, body)
                    print "[api] "; req.method; " "; HttpRedact(req.path); " "; r.status
                    if client.changed then
                        client.changed = false
                        StoreSaveTokens(homeId, client.session.access, client.session.refresh)
                        m.top.tokens = {id: homeId, access: client.session.access, refresh: client.session.refresh}
                    end if
                    resp = {status: r.status, data: r.data}
                    if req.raw then resp.text = r.body
                    req.response = resp
                    if client.tooOld then
                        client.tooOld = false
                        m.top.tooOld = true
                    end if
                    if client.signedOut then
                        client = invalid
                        m.top.signedOut = true
                    end if
                end if
            end if
        end if
    end while
end sub
