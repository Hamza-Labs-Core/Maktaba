' The compatibility contract on Roku (Epic 58 story 58.01): before signing in, the channel reads
' api_level, api_level_oldest and client_floor from GET /api/discovery/home and decides, as every
' app does (shared/compat/vectors.json holds the cases each copy must agree on):
'
'   CompatDecide(home, CompatApp())   ' "ok", "update_home" or "update_app"
'
' A home from before the contract sends none of the three: it reads as level 0, oldest 0 and no
' floor.

' The channel's own identity in the contract. min_api_level 0: it works with a home from before
' the contract; built_for_api_level: the level its API calls were written against.
function CompatApp() as Object
    return {name: "tv", platform: "roku", version: AppVersion(), min_api_level: 0, built_for_api_level: 1}
end function

function CompatDecide(home as Object, app as Object) as String
    level = compat_Int(home.api_level)
    oldest = compat_Int(home.api_level_oldest)
    if level < compat_Int(app.min_api_level) then return "update_home"
    if compat_Int(app.built_for_api_level) < oldest then return "update_app"
    if CompatBelowFloor(home.client_floor, app) then return "update_app"
    return "ok"
end function

' Whether the app's version is below its entry ("<name>/<platform>") in the floor. A version
' with no number to read is never below it.
function CompatBelowFloor(floor as Dynamic, app as Object) as Boolean
    if type(floor) <> "roAssociativeArray" then return false
    floorVersion = floor[app.name + "/" + app.platform]
    if floorVersion = invalid then return false
    c = CompatCompareVersions(app.version, floorVersion)
    if c = invalid then return false
    return c < 0
end function

' Compares two dotted versions by number: -1, 0 or 1, invalid when either has no number to read.
' A missing part is 0 (1.4 is 1.4.0), a pre-release suffix ends the number (1.3.9-beta is
' 1.3.9) and a leading v is dropped.
function CompatCompareVersions(a as String, b as String) as Dynamic
    pa = compat_Parts(a)
    pb = compat_Parts(b)
    if pa = invalid or pb = invalid then return invalid
    n = pa.Count()
    if pb.Count() > n then n = pb.Count()
    for i = 0 to n - 1
        x = 0
        y = 0
        if i < pa.Count() then x = pa[i]
        if i < pb.Count() then y = pb[i]
        if x < y then return -1
        if x > y then return 1
    end for
    return 0
end function

function compat_Parts(v as String) as Dynamic
    s = v.Trim()
    if Left(s, 1) = "v" then s = Mid(s, 2)
    out = []
    for each p in s.Split(".")
        digits = ""
        for i = 1 to Len(p)
            ch = Mid(p, i, 1)
            if ch < "0" or ch > "9" then exit for
            digits = digits + ch
        end for
        if digits = "" then exit for
        out.Push(digits.ToInt())
        if Len(digits) < Len(p) then exit for
    end for
    if out.Count() = 0 then return invalid
    return out
end function

function compat_Int(v as Dynamic) as Integer
    if v = invalid then return 0
    t = type(v)
    if t = "roInt" or t = "Integer" or t = "roInteger" or t = "LongInteger" or t = "roLongInteger" then return v
    if t = "Float" or t = "roFloat" or t = "Double" or t = "roDouble" then return Int(v)
    return 0
end function
