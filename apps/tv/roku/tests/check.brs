' Assertions for the channel's BrightScript tests (Epic 58 story 58.12). A test's Main sets
' m.pass and m.fail to 0, calls these, and ends with ck_Result().

sub ck_Eq(name as String, got as Dynamic, want as Dynamic)
    same = false
    if type(got) = "roArray" or type(want) = "roArray" or type(got) = "roAssociativeArray" or type(want) = "roAssociativeArray" then
        same = (FormatJson(got) = FormatJson(want))
    else if got = invalid or want = invalid then
        same = (got = invalid and want = invalid)
    else
        same = (got = want)
    end if
    if same then
        m.pass = m.pass + 1
    else
        m.fail = m.fail + 1
        print "FAIL "; name; ": got "; ck_Show(got); ", want "; ck_Show(want)
    end if
end sub

sub ck_True(name as String, got as Boolean)
    ck_Eq(name, got, true)
end sub

function ck_Show(v as Dynamic) as String
    if v = invalid then return "invalid"
    t = type(v)
    if t = "roArray" or t = "roAssociativeArray" then return FormatJson(v)
    if t = "roString" or t = "String" then return Chr(34) + v + Chr(34)
    if t = "roBoolean" or t = "Boolean" then
        if v then return "true"
        return "false"
    end if
    return v.ToStr()
end function

sub ck_Result()
    print "RESULT pass="; m.pass; " fail="; m.fail
end sub

' A transport for ApiClientNew that answers from a script: each Do() takes the next answer
' ({status, body}) and records the request.
function ck_FakeTransport(answers as Object) as Object
    return {answers: answers, sent: [], Do: ck_FakeDo}
end function

function ck_FakeDo(req as Object) as Object
    m.sent.Push(req)
    if m.answers.Count() = 0 then return {status: 599, body: ""}
    return m.answers.Shift()
end function
