sub init()
    m.top.functionName = "run"
end sub

function code_Headers() as Object
    return {"Accept": "application/json", "Content-Type": "application/json", "X-Maktaba-Client": ClientHeader()}
end function

function code_Post(url as String, body as Object, timeoutMs as Integer) as Object
    t = NetUrlTransport(timeoutMs)
    r = t.Do({method: "POST", url: url, headers: code_Headers(), body: FormatJson(body)})
    data = invalid
    if r.body <> invalid and r.body <> "" then data = ParseJson(r.body)
    return {status: r.status, data: data}
end function

function code_Label() as String
    model = "Roku"
    di = CreateObject("roDeviceInfo")
    if di <> invalid then
        name = di.GetFriendlyName()
        if name <> invalid and name <> "" then model = name
    end if
    return model
end function

sub run()
    if m.top.mode = "home" then
        code_Home()
    else
        code_Cloud()
    end if
end sub

' Waits seconds, or less when the screen stops the Task; false when stopped.
function code_Sleep(seconds as Integer) as Boolean
    clock = CreateObject("roTimespan")
    while clock.TotalMilliseconds() < seconds * 1000
        if m.top.stop then return false
        Sleep(200)
    end while
    return not m.top.stop
end function

' The cloud code: POST /v1/tv/codes, then poll until a phone approves it (a new code when one
' expires), then the grant.
sub code_Cloud()
    cloud = CloudBase()
    while not m.top.stop
        r = code_Post(NetJoin(cloud, "/v1/tv/codes"), {platform: "roku", app_version: AppVersion()}, 10000)
        if r.status <> 201 or r.data = invalid or r.data.code = invalid then
            print "[code] the cloud did not give a code: "; r.status
            m.top.state = "cloud_down"
            if not code_Sleep(10) then return
        else
            c = r.data
            interval = 5
            if c.interval <> invalid and c.interval > 0 then interval = c.interval
            m.top.code = {code: c.code, qr: NetJoin(cloud, "/v1/tv/codes/" + c.code.Replace("-", "") + "/qr.png"), expiresIn: c.expires_in, verifyUrl: c.verify_url}
            m.top.state = "waiting"
            print "[code] cloud code shown"
            renew = false
            while not m.top.stop and not renew
                if not code_Sleep(interval) then return
                p = code_Post(NetJoin(cloud, "/v1/tv/codes/poll"), {code: c.code, poll_secret: c.poll_secret}, 10000)
                if p.status = 200 and p.data <> invalid and p.data.status = "approved" then
                    print "[code] approved by "; p.data.home_id
                    code_Redeem(p.data)
                    return
                else if p.status = 429 then
                    interval = interval + 5
                else if p.status = 410 or p.status = 404 then
                    print "[code] expired: a new code"
                    renew = true
                end if
            end while
        end if
    end while
end sub

' The grant goes to the first of the cloud's connections that answers as the approving home
' (LAN first, the relay last; story 50.13's order), at a level this channel can use.
sub code_Redeem(p as Object)
    m.top.state = "signing_in"
    for each conn in NetCloudConnections(p.connections)
        base = conn.url
        while Right(base, 1) = "/"
            base = Left(base, Len(base) - 1)
        end while
        a = code_Discover(base)
        if a <> invalid and a.id = p.home_id then
            verdict = CompatDecide(a, CompatApp())
            if verdict <> "ok" then
                m.top.result = {ok: false, verdict: verdict, home: {id: a.id, name: a.name, base: base}}
                return
            end if
            plain = (LCase(Left(base, 7)) = "http://")
            res = code_Token(base, p.grant)
            if res.ok then
                res.home.id = a.id
                res.home.name = a.name
                if a.name = invalid or a.name = "" then res.home.name = p.home_name
                res.home.base = base
                res.home.plain = plain
                print "[code] signed in through "; conn.kind
                m.top.result = res
                return
            end if
            m.top.result = {ok: false, error: "redeem"}
            return
        end if
        print "[code] "; conn.kind; " did not answer as the home"
    end for
    m.top.result = {ok: false, error: "unreachable"}
end sub

function code_Discover(base as String) as Dynamic
    t = NetUrlTransport(4000)
    r = t.Do({method: "GET", url: NetJoin(base, "/api/discovery/home"), headers: code_Headers(), body: ""})
    if r.status <> 200 then return invalid
    a = ParseJson(r.body)
    if not NetIsHomeAnswer(a) then return invalid
    return a
end function

' POST /api/pairing/device/token: 200 gives the tokens; then /api/auth/me says who signed in.
function code_Token(base as String, deviceCode as String) as Object
    r = code_Post(NetJoin(base, "/api/pairing/device/token"), {device_code: deviceCode, device_label: code_Label()}, 10000)
    if r.status <> 200 or r.data = invalid or r.data.access_token = invalid then return {ok: false, status: r.status}
    home = {access: r.data.access_token, refresh: r.data.refresh_token, user: "", admin: false}
    t = NetUrlTransport(10000)
    h = code_Headers()
    h["Authorization"] = "Bearer " + home.access
    me = t.Do({method: "GET", url: NetJoin(base, "/api/auth/me"), headers: h, body: ""})
    if me.status = 200 then
        d = ParseJson(me.body)
        if d <> invalid then
            if d.username <> invalid then home.user = d.username
            home.admin = (d.is_admin = true)
        end if
    end if
    return {ok: true, home: home}
end function

' The home's own code (POST /api/pairing/device), then its token poll.
sub code_Home()
    base = m.top.base
    while not m.top.stop
        r = code_Post(NetJoin(base, "/api/pairing/device"), {device_label: code_Label()}, 10000)
        if (r.status <> 200 and r.status <> 201) or r.data = invalid or r.data.code = invalid then
            print "[code] the home gave no code: "; r.status
            m.top.state = "no_code"
            m.top.result = {ok: false, error: "no_code"}
            return
        end if
        c = r.data
        interval = 2
        if c.interval <> invalid and c.interval > 0 then interval = c.interval
        m.top.code = {code: c.code, qr: "", expiresIn: 600, verifyUrl: c.verify_url}
        m.top.state = "waiting"
        renew = false
        while not m.top.stop and not renew
            if not code_Sleep(interval) then return
            res = code_Token(base, c.device_code)
            if res.ok then
                res.home.base = base
                res.home.plain = m.top.plain
                m.top.result = res
                return
            else if res.status = 404 or res.status = 409 or res.status = 410 then
                renew = true
            end if
        end while
    end while
end sub
