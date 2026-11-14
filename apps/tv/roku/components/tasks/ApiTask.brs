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
                    if client = invalid or homeId <> s.id or client.session.base <> s.base then
                        client = ApiClientNew({base: s.base, access: s.access, refresh: s.refresh, plain: s.plain}, NetUrlTransport(15000))
                        homeId = s.id
                    end if
                    body = invalid
                    if req.hasBody then body = req.body
                    r = client.Request(req.method, req.path, body)
                    print "[api] "; req.method; " "; HttpRedact(req.path); " "; r.status
                    if client.changed then
                        client.changed = false
                        StoreSaveTokens(homeId, client.session.access, client.session.refresh)
                        m.top.tokens = {id: homeId, access: client.session.access, refresh: client.session.refresh}
                    end if
                    req.response = {status: r.status, data: r.data}
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
