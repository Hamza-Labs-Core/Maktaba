' The real transport for ApiClientNew and the Tasks (Epic 58 story 58.12): roUrlTransfer,
' asynchronous with a timeout, trusting Roku's stock certificate bundle.
'
'   t = NetUrlTransport(15000)
'   t.Do({method: "GET", url: "...", headers: {...}, body: ""})   ' {status, body}
'
' status 0 means no answer (timeout, refused, a certificate that did not verify).

function NetUrlTransport(timeoutMs as Integer) as Object
    return {timeoutMs: timeoutMs, Do: http_Do}
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
    msg = Wait(m.timeoutMs, port)
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
