' The real transport for ApiClientNew and the Tasks (Epic 58 story 58.12): roUrlTransfer,
' asynchronous with a timeout, trusting Roku's stock certificate bundle.
'
'   t = NetUrlTransport(15000)
'   t.Do({method: "GET", url: "...", headers: {...}, body: ""})   ' {status, body}
'
' status 0 means no answer (timeout, refused, a certificate that did not verify).
'
' A transport with a node (t.node, a Task) and a tag (t.tag, the request being sent) stops the
' transfer when the node's cancel field is set to that tag: AI mode's answer (story 49.14) is
' given up when the person asks again or leaves, so the home gives the turn to the next.

function NetUrlTransport(timeoutMs as Integer) as Object
    return {timeoutMs: timeoutMs, node: invalid, tag: "", Do: http_Do}
end function

function http_Do(req as Object) as Object
    ut = HttpTransfer(req.url, req.headers)
    port = CreateObject("roMessagePort")
    ut.SetMessagePort(port)
    ut.SetRequest(req.method)
    if req.method = "GET" then
        ok = ut.AsyncGetToString()
    else
        ok = ut.AsyncPostFromString(req.body)
    end if
    if not ok then return {status: 0, body: ""}
    if m.node <> invalid then m.node.ObserveField("cancel", port)
    msg = invalid
    while true
        msg = Wait(m.timeoutMs, port)
        if type(msg) <> "roSGNodeEvent" then exit while
        ' "*" drops whatever call is being read (the scene was reset: Search is gone)
        if (msg.GetData() = m.tag or msg.GetData() = "*") and m.tag <> "" then
            m.node.UnobserveField("cancel")
            ut.AsyncCancel()
            print "[http] "; req.method; " "; HttpRedact(req.url); " cancelled"
            return {status: 0, body: "", cancelled: true}
        end if
    end while
    if m.node <> invalid then m.node.UnobserveField("cancel")
    if type(msg) <> "roUrlEvent" then
        ut.AsyncCancel()
        print "[http] "; req.method; " "; HttpRedact(req.url); " timed out"
        return {status: 0, body: ""}
    end if
    code = msg.GetResponseCode()
    if code < 0 then print "[http] "; req.method; " "; HttpRedact(req.url); " failed: "; msg.GetFailureReason()
    return {status: code, body: msg.GetString()}
end function

' An roUrlTransfer for url with the stock certificate bundle and the given headers.
function HttpTransfer(url as String, headers as Dynamic) as Object
    ut = CreateObject("roUrlTransfer")
    ut.SetUrl(url)
    ut.SetCertificatesFile(CertificatesFile())
    ut.InitClientCertificates()
    ut.RetainBodyOnError(true)
    ut.EnableEncodings(true)
    if type(headers) = "roAssociativeArray" then
        for each k in headers
            ut.AddHeader(k, headers[k])
        end for
    end if
    return ut
end function

' A URL for the log: no query string (a signed URL's sig, a code's secret never reach it).
function HttpRedact(url as String) as String
    i = Instr(1, url, "?")
    if i > 0 then return Left(url, i - 1) + "?…"
    return url
end function
