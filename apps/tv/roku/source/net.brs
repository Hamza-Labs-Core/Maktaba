' Reaching a home, and the API client's rules (Epic 58 story 58.12).
'
' The pure parts live here so brs-engine tests them without SceneGraph; the Tasks
' (components/tasks/) give them a real transport (NetUrlTransport).
'
'   client = ApiClientNew(session, transport)
'   r = client.Request("GET", "/api/home", invalid)   ' {status, data, body}
'
' session is {base, access, refresh, plain}; it is changed in place when tokens are renewed
' (client.changed is then true, and the Task saves them). A 401 takes one refresh and one
' retry, as the Android core does: no refresh token, or a refresh the home refuses (400, 401,
' 403), signs out (client.signedOut); a 429 or 5xx on refresh keeps the tokens. A request
' that was sent with an older token than the session now holds is retried without another
' refresh. The channel runs every API call in one Task, so one refresh runs at a time.

function ApiClientNew(session as Object, transport as Object) as Object
    return {
        session: session
        transport: transport
        changed: false
        signedOut: false
        tooOld: false
        generation: 0
        Request: api_Request
        Refresh: api_Refresh
        Headers: api_Headers
    }
end function

function api_Headers(withAuth as Boolean) as Object
    h = {"Accept": "application/json", "X-Maktaba-Client": ClientHeader()}
    if withAuth and m.session.access <> invalid and m.session.access <> "" then
        h["Authorization"] = "Bearer " + m.session.access
    end if
    return h
end function

function api_Request(method as String, path as String, body as Dynamic) as Object
    gen = m.generation
    r = api_Send(m, method, path, body, true)
    if r.status <> 401 then return r
    if m.generation <> gen then return api_Send(m, method, path, body, true)
    if not m.Refresh() then return r
    r = api_Send(m, method, path, body, true)
    if r.status = 401 then
        m.session.access = ""
        m.session.refresh = ""
        m.changed = true
        m.signedOut = true
    end if
    return r
end function

' POST /api/auth/refresh with the refresh token: true when the session holds new tokens.
function api_Refresh() as Boolean
    if m.session.refresh = invalid or m.session.refresh = "" then
        m.signedOut = true
        return false
    end if
    r = api_Send(m, "POST", "/api/auth/refresh", {refresh_token: m.session.refresh}, false)
    if r.status = 200 and r.data <> invalid and r.data.access_token <> invalid then
        m.session.access = r.data.access_token
        if r.data.refresh_token <> invalid then m.session.refresh = r.data.refresh_token
        m.generation = m.generation + 1
        m.changed = true
        return true
    end if
    if r.status = 426 then m.tooOld = true
    if r.status = 400 or r.status = 401 or r.status = 403 then
        m.session.access = ""
        m.session.refresh = ""
        m.changed = true
        m.signedOut = true
    end if
    return false
end function

function api_Send(client as Object, method as String, path as String, body as Dynamic, withAuth as Boolean) as Object
    req = {method: method, url: NetJoin(client.session.base, path), headers: client.Headers(withAuth), body: ""}
    if body <> invalid then
        req.body = FormatJson(body)
        req.headers["Content-Type"] = "application/json"
    end if
    res = client.transport.Do(req)
    data = invalid
    ' only a JSON object or array is parsed: a WebVTT file (story 58.13) is read as text
    if res.body <> invalid and res.body <> "" then
        first = Left(res.body.Trim(), 1)
        if first = "{" or first = "[" then data = ParseJson(res.body)
    end if
    return {status: res.status, data: data, body: res.body}
end function

' base + path, with exactly one slash between them.
function NetJoin(base as String, path as String) as String
    if Left(path, 7) = "http://" or Left(path, 8) = "https://" then return path
    b = base
    while Right(b, 1) = "/"
        b = Left(b, Len(b) - 1)
    end while
    if Left(path, 1) <> "/" then path = "/" + path
    return b + path
end function

' A query string from an associative array (keys sorted, values escaped), "" when empty.
function NetQuery(params as Object) as String
    parts = []
    keys = params.Keys()
    keys.Sort()
    for each k in keys
        v = params[k]
        if v <> invalid then
            if type(v) <> "roString" and type(v) <> "String" then v = v.ToStr()
            parts.Push(NetEscape(k) + "=" + NetEscape(v))
        end if
    end for
    if parts.Count() = 0 then return ""
    return "?" + parts.Join("&")
end function

' Percent-encodes everything but RFC 3986's unreserved characters (UTF-8 bytes).
function NetEscape(s as String) as String
    ba = CreateObject("roByteArray")
    ba.FromAsciiString(s)
    out = ""
    for i = 0 to ba.Count() - 1
        b = ba[i]
        ok = (b >= 48 and b <= 57) or (b >= 65 and b <= 90) or (b >= 97 and b <= 122) or b = 45 or b = 46 or b = 95 or b = 126
        if ok then
            out = out + Chr(b)
        else
            h = UCase(StrI(b, 16))
            if Len(h) < 2 then h = "0" + h
            out = out + "%" + h
        end if
    end for
    return out
end function

' The host of a URL ("https://a.b:8443/x" is "a.b"), lower case; "" when there is none.
function NetHost(url as String) as String
    s = url
    i = Instr(1, s, "://")
    if i = 0 then return ""
    s = Mid(s, i + 3)
    for each sep in ["/", "?", "#"]
        j = Instr(1, s, sep)
        if j > 0 then s = Left(s, j - 1)
    end for
    j = Instr(1, s, ":")
    if j > 0 then s = Left(s, j - 1)
    return LCase(s)
end function

' Whether s is a dotted IPv4 address.
function NetIsIPv4(s as String) as Boolean
    parts = s.Split(".")
    if parts.Count() <> 4 then return false
    for each p in parts
        if Len(p) < 1 or Len(p) > 3 then return false
        for i = 1 to Len(p)
            ch = Mid(p, i, 1)
            if ch < "0" or ch > "9" then return false
        end for
        if p.ToInt() > 255 then return false
    end for
    return true
end function

' A base URL from what a person typed ("192.168.1.20", "home.local:8088", a full URL), or ""
' when it is not one. A bare host with no port is the home's web port, 8088 (story 58.09).
function NetNormalizeBase(input as String) as String
    s = input.Trim()
    if s = "" then return ""
    typedScheme = (LCase(Left(s, 7)) = "http://" or LCase(Left(s, 8)) = "https://")
    if not typedScheme then s = "http://" + s
    i = Instr(1, s, "://")
    scheme = LCase(Left(s, i - 1))
    rest = Mid(s, i + 3)
    for each sep in ["/", "?", "#"]
        j = Instr(1, rest, sep)
        if j > 0 then rest = Left(rest, j - 1)
    end for
    if rest = "" or Instr(1, rest, "@") > 0 or Instr(1, rest, " ") > 0 then return ""
    host = rest
    port = ""
    j = Instr(1, rest, ":")
    if j > 0 then
        host = Left(rest, j - 1)
        port = Mid(rest, j + 1)
        if port = "" or port.ToInt() <= 0 or port.ToInt() > 65535 or port.ToInt().ToStr() <> port then return ""
    end if
    if host = "" then return ""
    for i = 1 to Len(host)
        ch = LCase(Mid(host, i, 1))
        if not ((ch >= "a" and ch <= "z") or (ch >= "0" and ch <= "9") or ch = "." or ch = "-") then return ""
    end for
    if port = "" and scheme = "http" and not typedScheme then port = "8088"
    out = scheme + "://" + LCase(host)
    if port <> "" then out = out + ":" + port
    return out
end function

' Whether an answer from GET /api/discovery/home is a Maktaba home: an id and its urls.
function NetIsHomeAnswer(a as Dynamic) as Boolean
    if type(a) <> "roAssociativeArray" then return false
    if a.id = invalid or type(a.urls) <> "roArray" then return false
    return (type(a.id) = "roString" or type(a.id) = "String") and a.id <> ""
end function

' Whether an HTTPS URL a home named is its direct name for the address it was found at
' (story 50.15's 1-2-3-4.<home-id>.<domain>): the first label is the found address and the
' second the home's id, so an answer on the LAN can only name itself.
function NetDirectNameOf(url as String, foundAt as String, homeId as String) as Boolean
    if LCase(Left(url, 8)) <> "https://" or homeId = "" then return false
    found = NetHost(foundAt)
    if not NetIsIPv4(found) then return false
    labels = NetHost(url).Split(".")
    if labels.Count() < 3 then return false
    return labels[0] = found.Replace(".", "-") and labels[1] = LCase(homeId)
end function

' The HTTPS names to try, in order, for a home answering at foundAt.
function NetDirectNames(answer as Object, foundAt as String) as Object
    out = []
    for each u in answer.urls
        if type(u) = "roString" or type(u) = "String" then
            b = u
            while Right(b, 1) = "/"
                b = Left(b, Len(b) - 1)
            end while
            if NetDirectNameOf(b, foundAt, answer.id) then out.Push(b)
        end if
    end for
    return out
end function

' The cloud's connections (story 50.13's order) that a Roku can use, in that order. A local_ip
' entry whose certificate carries another name (tls_name) is left out: roUrlTransfer checks a
' certificate against the URL's own host and cannot pin another name, and verification is never
' turned off.
function NetCloudConnections(conns as Dynamic) as Object
    out = []
    if type(conns) <> "roArray" then return out
    for each c in conns
        if type(c) = "roAssociativeArray" and c.url <> invalid and c.url <> "" then
            pinned = (c.tls_name <> invalid and c.tls_name <> "" and LCase(c.tls_name) <> NetHost(c.url))
            if not (LCase(Left(c.url, 8)) = "https://" and pinned) then out.Push(c)
        end if
    end for
    return out
end function

' The addresses of the TV's /24 to probe (story 58.09): every host but the TV's own; [] when the
' TV's address is not an IPv4 address.
function NetProbeTargets(ip as String) as Object
    out = []
    if not NetIsIPv4(ip) then return out
    p = ip.Split(".")
    prefix = p[0] + "." + p[1] + "." + p[2] + "."
    own = p[3].ToInt()
    for n = 1 to 254
        if n <> own then out.Push(prefix + n.ToStr())
    end for
    return out
end function

' The TV's IPv4 address from roDeviceInfo.GetIPAddrs() ({eth1: "192.168.1.9"}), "" when none.
function NetDeviceIp(addrs as Dynamic) as String
    if type(addrs) <> "roAssociativeArray" then return ""
    keys = addrs.Keys()
    keys.Sort()
    for each k in keys
        v = addrs[k]
        if (type(v) = "roString" or type(v) = "String") and NetIsIPv4(v) then return v
    end for
    return ""
end function
