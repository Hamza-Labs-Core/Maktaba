sub init()
    m.top.functionName = "run"
end sub

sub run()
    c = m.top.command
    if c.kind = "connect" then
        m.top.result = home_Connect(c.home)
    else if c.kind = "probe" then
        m.top.result = home_Probe()
    else if c.kind = "read" then
        m.top.result = home_Read(c.base)
    else if c.kind = "choose" then
        m.top.result = home_Choose(c.foundAt, c.answer)
    end if
end sub

' GET <base>/api/discovery/home within timeoutMs: the answer when it is a Maktaba home.
function home_Discover(base as String, timeoutMs as Integer) as Dynamic
    t = NetUrlTransport(timeoutMs)
    r = t.Do({method: "GET", url: NetJoin(base, "/api/discovery/home"), headers: {"Accept": "application/json", "X-Maktaba-Client": ClientHeader()}, body: ""})
    if r.status <> 200 then return invalid
    a = ParseJson(r.body)
    if not NetIsHomeAnswer(a) then return invalid
    return a
end function

' A saved home at launch: it must answer as itself, at a level this channel can use.
function home_Connect(home as Object) as Object
    a = home_Discover(home.base, 5000)
    if a = invalid or a.id <> home.id then
        print "[home] "; home.id; " did not answer at "; home.base
        return {ok: false, home: home}
    end if
    verdict = CompatDecide(a, CompatApp())
    print "[home] "; home.id; " level "; a.api_level; " "; verdict
    if a.name <> invalid and a.name <> "" then home.name = a.name
    return {ok: (verdict = "ok"), verdict: verdict, home: home, answer: a}
end function

' A typed address: read it, then reach it as Find on this network does.
function home_Read(base as String) as Object
    a = home_Discover(base, 5000)
    if a = invalid then return {ok: false, base: base}
    return home_Choose(base, a)
end function

' The connection for a home found at foundAt: its direct HTTPS name for that address when it
' answers as the same home, else foundAt itself (plain HTTP on the LAN: sign-in by code only).
function home_Choose(foundAt as String, answer as Object) as Object
    verdict = CompatDecide(answer, CompatApp())
    if LCase(Left(foundAt, 8)) = "https://" then
        return {ok: true, verdict: verdict, base: foundAt, plain: false, answer: answer}
    end if
    for each name in NetDirectNames(answer, foundAt)
        again = home_Discover(name, 2500)
        if again <> invalid and again.id = answer.id then
            print "[home] "; answer.id; ": its HTTPS name answers"
            return {ok: true, verdict: verdict, base: name, plain: false, answer: again}
        end if
        print "[home] "; answer.id; ": "; NetHost(name); " did not answer"
    end for
    print "[home] "; answer.id; ": plain HTTP on the LAN at "; foundAt
    return {ok: true, verdict: verdict, base: foundAt, plain: true, answer: answer}
end function

' Find on this network (story 58.09): GET http://<ip>:8088/api/discovery/home on every other
' address of the TV's /24, 32 at a time, 700 ms each; homes listed once each.
function home_Probe() as Object
    ip = ""
    di = CreateObject("roDeviceInfo")
    if di <> invalid then ip = NetDeviceIp(di.GetIPAddrs())
    over = m.top.command.ip
    if over <> invalid and over <> "" then ip = over
    targets = NetProbeTargets(ip)
    if targets.Count() = 0 then return {ok: false, noIp: true, homes: []}
    port = m.top.command.port
    if port = invalid or port = 0 then port = 8088
    found = []
    seen = {}
    i = 0
    while i < targets.Count()
        batch = []
        mp = CreateObject("roMessagePort")
        for j = i to i + 31
            if j >= targets.Count() then exit for
            url = "http://" + targets[j] + ":" + port.ToStr()
            ut = HttpTransfer(NetJoin(url, "/api/discovery/home"), {"Accept": "application/json", "X-Maktaba-Client": ClientHeader()})
            ut.SetMessagePort(mp)
            if ut.AsyncGetToString() then batch.Push({ut: ut, base: url, id: ut.GetIdentity()})
        end for
        deadline = CreateObject("roTimespan")
        pending = batch.Count()
        while pending > 0
            remain = 700 - deadline.TotalMilliseconds()
            if remain <= 0 then exit while
            msg = Wait(remain, mp)
            if type(msg) = "roUrlEvent" then
                pending = pending - 1
                if msg.GetResponseCode() = 200 then
                    a = ParseJson(msg.GetString())
                    if NetIsHomeAnswer(a) and not seen.DoesExist(a.id) then
                        for each b in batch
                            if b.id = msg.GetSourceIdentity() then
                                seen[a.id] = true
                                found.Push({id: a.id, name: a.name, foundAt: b.base, answer: a})
                                print "[probe] home "; a.id; " at "; b.base
                            end if
                        end for
                    end if
                end if
            end if
        end while
        for each b in batch
            b.ut.AsyncCancel()
        end for
        i = i + 32
        m.top.progress = {done: i, total: targets.Count()}
    end while
    return {ok: true, homes: found}
end function
