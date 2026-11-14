' Parental controls on Roku (Epic 58 story 58.14). The home filters, counts and refuses
' (Epic 57); the channel only says what it answered:
'
'   ParentalFrom(data)              GET /api/me/parental's answer as the session keeps it
'   ParentalLimited(p)              the sign-in is under a limit (its account's or its device's):
'                                   a 404 may then be the limit, and the blocked page shows
'   ParentalLimitLine(p, i18n)      "Your account is limited to 9+." (or the device's)
'   ParentalManagedLines(p, i18n)   Account's lines: a parent manages it, and its limit
'   ParentalRequestState(data, kind, id)
'                                   what became of the account's latest request for kind:id in
'                                   GET /api/me/parental/requests' answer: none, pending,
'                                   approved, denied or expired
'
' No parent PIN on a TV remote: a guardian answers from Maktaba on their phone.
'
' The tier goes into the catalog's templates as digits (ASCII, the same in both languages):
' the catalog's own words for All ages are whole strings of their own, since Arabic is
' shaped once, when a template is filled.

function ParentalFrom(data as Dynamic) as Object
    p = {restricted: false, maxTier: invalid, deviceLimit: invalid}
    if type(data) <> "roAssociativeArray" then return p
    p.restricted = (data.restricted = true)
    p.maxTier = parental_Int(data.max_tier)
    p.deviceLimit = parental_Int(data.device_limit)
    return p
end function

function ParentalLimited(p as Dynamic) as Boolean
    if type(p) <> "roAssociativeArray" then return false
    return p.restricted = true or p.maxTier <> invalid
end function

function ParentalLimitLine(p as Dynamic, i18n as Object) as String
    if not ParentalLimited(p) or p.maxTier = invalid then return ""
    byDevice = p.deviceLimit <> invalid and (p.restricted <> true or p.deviceLimit = p.maxTier)
    if byDevice then return parental_Tiered(i18n, "parental.blocked.device", p.maxTier, "parental.blocked.deviceAll", "parental.blocked.deviceAll")
    return parental_Tiered(i18n, "parental.blocked.account", p.maxTier, "parental.blocked.accountAll", "parental.blocked.accountNone")
end function

function ParentalManagedLines(p as Dynamic, i18n as Object) as Object
    lines = []
    if type(p) <> "roAssociativeArray" then return lines
    if p.restricted = true then
        lines.Push(i18n.Tr("parental.profile.managed"))
        if p.maxTier <> invalid then lines.Push(parental_Tiered(i18n, "parental.profile.limit", p.maxTier, "parental.profile.limitAll", "parental.profile.limitNone"))
    end if
    if p.deviceLimit <> invalid and (p.restricted <> true or p.deviceLimit <> p.maxTier) then
        lines.Push(parental_Tiered(i18n, "parental.blocked.device", p.deviceLimit, "parental.blocked.deviceAll", "parental.blocked.deviceAll"))
    end if
    if p.restricted = true then lines.Push(i18n.Tr("tv.parental.askOnPhone"))
    return lines
end function

function ParentalRequestState(data as Dynamic, kind as String, id as String) as String
    if type(data) <> "roAssociativeArray" or type(data.requests) <> "roArray" then return "none"
    ' newest first, as the home lists them
    for each r in data.requests
        if type(r) = "roAssociativeArray" and r.subject_kind = kind and r.subject_id = id then
            if r.status = "pending" or r.status = "approved" or r.status = "denied" then return r.status
            return "expired"
        end if
    end for
    return "none"
end function

' The template for a tier of 9 and over; the whole strings for All ages (0) and for nothing
' that has a tier (-1).
function parental_Tiered(i18n as Object, key as String, tier as Integer, allKey as String, noneKey as String) as String
    if tier < 0 then return i18n.Tr(noneKey)
    if tier = 0 then return i18n.Tr(allKey)
    return i18n.Format(key, [tier.ToStr()])
end function

function parental_Int(v as Dynamic) as Dynamic
    if v = invalid then return invalid
    t = type(v)
    if t = "roInt" or t = "Integer" or t = "roInteger" or t = "LongInteger" or t = "roLongInteger" then return Int(v)
    if t = "roFloat" or t = "Float" or t = "Double" or t = "roDouble" then return Int(v)
    return invalid
end function
