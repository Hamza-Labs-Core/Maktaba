' Parental controls on Roku (Epic 58 story 58.14). The home filters, counts and refuses
' (Epic 57); the channel only says what it answered:
'
'   ParentalFrom(data)              GET /api/me/parental's answer as the session keeps it
'   ParentalLimited(p)              the sign-in is under a limit (its account's or its device's):
'                                   a 404 may then be the limit, and the blocked page shows
'   ParentalLimitLine(p, i18n)      "Your account is limited to 9+." (or the device's)
'   ParentalManagedLines(p, i18n)   Account's lines: a parent manages it, and its limit
'   ParentalRequestState(data, kind, id, nowSec)
'                                   what became of the account's latest request for kind:id in
'                                   GET /api/me/parental/requests' answer: none, pending,
'                                   approved, denied or expired; kind "time" is the latest
'                                   request for more time, whatever minutes it asked for. As on
'                                   the web and Android: a request still waiting past its own
'                                   expires_at is expired, and a no to more time is about its
'                                   day, so once its expires_at (the home's day end) has passed
'                                   it is none and asking is offered afresh
'
' The player's half (the server's answers to a stream, Epic 57 story 57.14):
'
'   ParentalStop(status, data)      what an answer to a session open or a progress post means:
'                                   "time" (403 parental_time: time's up or not watching time),
'                                   "lapse" (403 parental_time no_heartbeat: the stream's lease
'                                   ran out in a long pause), "unknown" (503
'                                   parental_time_unknown: the home could not read the time),
'                                   or "" (anything else, the player's own handling)
'   ParentalTimeUp(data)            the refusal's {reason, nextAt} (next_allowed_at, or "")
'   ParentalTimeTitleKey(reason)    the time's-up page's title
'   ParentalNextWords(iso, nowSec, offsetSec, i18n)
'                                   when watching opens again, in this Roku's time: "18:30"
'                                   today, "Saturday 07:00" on another day
'   ParentalAskMinutes()            the minutes a child may ask for at one press: 15, 30, 60
'
' The time left while a video plays (Epic 57 story 57.15, Android TV's TvTimeOverlay): the home
' is the authority, and the channel never counts; every number is the home's last answer to
' GET /api/me/parental/time.
'
'   ParentalTimePath(libraryId)     the time's path, for the video's library when it is known
'   ParentalTimeLeft(data)          its remaining_seconds when a rule covers now and watching is
'                                   allowed, else -1
'   ParentalShowsLeft(left)         the corner note shows: 15 minutes or fewer remain
'   ParentalPollSec(left)           when to ask again: every 30 s, every 10 s from 6 minutes left
'   ParentalWarningsNew()           the warnings' memory for one player
'   ParentalWarningCrossed(w, left) the warning line an answer newly crossed (300 or 60), or 0:
'                                   each once, and again only after the time went back above
'                                   it (a parent gave more)
'   ParentalWarningSeconds(line, left)
'                                   the seconds the warning names: the line itself within one
'                                   poll of it, else the time really left (a video opened with
'                                   two minutes left says two minutes, not five)
'   ParentalTimeWords(seconds, i18n)
'                                   "12 minutes", "1 hour and 5 minutes", "less than a minute",
'                                   whole minutes rounded down, Arabic in its plural forms; in
'                                   logical order, a value for parental.time.left or
'                                   parental.time.warning (I18n.Format shapes the line)
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

function ParentalRequestState(data as Dynamic, kind as String, id as String, nowSec = -1 as Integer) as String
    if type(data) <> "roAssociativeArray" or type(data.requests) <> "roArray" then return "none"
    if nowSec < 0 then nowSec = CreateObject("roDateTime").AsSeconds()
    ' newest first, as the home lists them
    for each r in data.requests
        if type(r) = "roAssociativeArray" and r.subject_kind = kind and (kind = "time" or r.subject_id = id) then
            ended = parental_Past(r.expires_at, nowSec)
            if r.status = "pending" and ended then return "expired"
            ' an earlier day's no to more time: the next day asks afresh
            if r.status = "denied" and kind = "time" and ended then return "none"
            if r.status = "pending" or r.status = "approved" or r.status = "denied" then return r.status
            return "expired"
        end if
    end for
    return "none"
end function

function ParentalTimePath(libraryId as Dynamic) as String
    path = "/api/me/parental/time"
    id = parental_Text(libraryId)
    if id <> "" then path = path + "?library_id=" + id
    return path
end function

function ParentalTimeLeft(data as Dynamic) as Integer
    if type(data) <> "roAssociativeArray" then return -1
    if data.allowed <> true or data.limited <> true then return -1
    left = parental_Int(data.remaining_seconds)
    if left = invalid then return -1
    if left < 0 then return 0
    return left
end function

function ParentalShowsLeft(left as Integer) as Boolean
    return left >= 0 and left <= 15 * 60
end function

function ParentalPollSec(left as Integer) as Integer
    if left >= 0 and left <= 6 * 60 then return 10
    return 30
end function

function ParentalWarningsNew() as Object
    return {last: 0}
end function

function ParentalWarningCrossed(w as Object, left as Integer) as Integer
    line = 0
    if left >= 0 and left <= 60 then
        line = 60
    else if left >= 0 and left <= 300 then
        line = 300
    end if
    if line = w.last then return 0
    was = w.last
    w.last = line
    ' a larger line after a smaller one is time given back, not a warning
    if line > 0 and (was = 0 or line < was) then return line
    return 0
end function

function ParentalWarningSeconds(line as Integer, left as Integer) as Integer
    if left < 0 or line - left <= 10 then return line
    return left
end function

function ParentalTimeWords(seconds as Integer, i18n as Object) as String
    if seconds < 0 then seconds = 0
    total = Int(seconds / 60)
    hours = Int(total / 60)
    minutes = total mod 60
    if hours = 0 and minutes = 0 then return i18n.Fill("parental.time.underMinute", [])
    if hours = 0 then return i18n.Plural("parental.time.minutes", minutes)
    h = i18n.Plural("parental.time.hours", hours)
    if minutes = 0 then return h
    return i18n.Fill("parental.time.hoursMinutes", [h, i18n.Plural("parental.time.minutes", minutes)])
end function

function ParentalStop(status as Integer, data as Dynamic) as String
    code = ""
    if type(data) = "roAssociativeArray" then code = parental_Text(data.code)
    if status = 403 and code = "parental_time" then
        if parental_Text(data.reason) = "no_heartbeat" then return "lapse"
        return "time"
    end if
    if status = 503 and code = "parental_time_unknown" then return "unknown"
    return ""
end function

function ParentalTimeUp(data as Dynamic) as Object
    t = {reason: "limit_reached", nextAt: ""}
    if type(data) <> "roAssociativeArray" then return t
    if parental_Text(data.reason) = "outside_slot" then t.reason = "outside_slot"
    t.nextAt = parental_Text(data.next_allowed_at)
    return t
end function

function ParentalTimeTitleKey(reason as String) as String
    if reason = "outside_slot" then return "parental.time.outside.title"
    return "parental.time.up.title"
end function

' iso: the home's instant (RFC 3339, UTC); nowSec: now in Unix seconds; offsetSec: this Roku's
' time zone (local minus UTC). The instant is the home's; only its display is local.
function ParentalNextWords(iso as String, nowSec as Integer, offsetSec as Integer, i18n as Object) as String
    if iso = "" then return ""
    at = parental_Seconds(iso)
    if at <= 0 then return ""
    atLocal = at + offsetSec
    day = Int(atLocal / 86400)
    sec = atLocal - day * 86400
    clock = parental_Two(Int(sec / 3600)) + ":" + parental_Two(Int((sec mod 3600) / 60))
    if day = Int((nowSec + offsetSec) / 86400) then return clock
    ' 1 January 1970 was a Thursday (Sunday is 0)
    return i18n.Tr("roku.weekday." + ((day + 4) mod 7).ToStr()) + " " + clock
end function

function ParentalAskMinutes() as Object
    return [15, 30, 60]
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

function parental_Two(n as Integer) as String
    if n < 10 then return "0" + n.ToStr()
    return n.ToStr()
end function

function parental_Text(v as Dynamic) as String
    t = type(v)
    if t = "roString" or t = "String" then return v
    return ""
end function

' Whether the home's instant (RFC 3339) is at or before nowSec; false when it gave none.
function parental_Past(iso as Dynamic, nowSec as Integer) as Boolean
    at = parental_Seconds(parental_Text(iso))
    if at <= 0 then return false
    return at <= nowSec
end function

' An RFC 3339 instant in Unix seconds (0 when it is not one), as Go writes it: fractional
' seconds dropped, "Z" or an offset (+03:00) taken into account.
function parental_Seconds(iso as String) as Integer
    if Len(iso) < 20 then return 0
    d = CreateObject("roDateTime")
    d.FromISO8601String(Left(iso, 19) + "Z")
    at = d.AsSeconds()
    if at <= 0 then return 0
    rest = Mid(iso, 20)
    ' the fraction, if any, then the zone
    while Len(rest) > 0 and (Left(rest, 1) = "." or (Asc(rest) >= 48 and Asc(rest) <= 57))
        rest = Mid(rest, 2)
    end while
    if rest = "Z" or rest = "z" then return at
    if Len(rest) <> 6 or Mid(rest, 4, 1) <> ":" then return 0
    offset = Mid(rest, 2, 2).ToInt() * 3600 + Mid(rest, 5, 2).ToInt() * 60
    if Left(rest, 1) = "+" then return at - offset
    if Left(rest, 1) = "-" then return at + offset
    return 0
end function
