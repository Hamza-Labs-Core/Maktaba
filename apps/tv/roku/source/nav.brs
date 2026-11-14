' The remote's rules (Epic 56 §6.5, mapped to Roku's keys; Epic 58 story 58.12).
'
' Back takes three steps on a screen with tabs: to the top of the tab, then to its tabs, then
' out. On Home, Back asks once whether to leave the channel (Roku's certification allows one
' confirmation). Roku's remote has no menu key, so * (Roku's "options") opens a card's actions,
' and Play/Pause on a focused card plays it.

' What Back does: "top" (focus the tab's first item), "tabs" (focus the tab bar), "leave" (close
' the screen) or "confirm_exit" (Home: ask once).
function NavBack(screen as String, inTabs as Boolean, onTop as Boolean) as String
    if screen = "home" then
        if inTabs or onTop then return "confirm_exit"
        return "top"
    end if
    if inTabs then return "leave"
    if not onTop then return "top"
    if screen = "library" then return "tabs"
    return "leave"
end function

' What a key does on a focused card: "play", "actions", "open" or "" (not handled).
function NavCardKey(key as String) as String
    if key = "play" then return "play"
    if key = "options" then return "actions"
    if key = "OK" then return "open"
    return ""
end function

' The tab to its side in reading order, wrapping never: tabs is the logical list, i the current
' one, key "left" or "right" as pressed (mirrored in Arabic through layout.Key).
function NavTabStep(i as Integer, count as Integer, key as String, layout as Object) as Integer
    k = layout.Key(key)
    if k = "right" and i < count - 1 then return i + 1
    if k = "left" and i > 0 then return i - 1
    return i
end function

' The window of grid pages to keep loaded: the focused page and one each side, so at most three
' pages of posters are in memory on a low-end Roku (its channel memory limit). Returns
' {keep: [pages], load: [pages not yet loaded], drop: [pages to unload]}.
function GridWindow(focusIndex as Integer, pageSize as Integer, total as Integer, loaded as Object) as Object
    pages = 0
    if total > 0 then pages = Int((total - 1) / pageSize) + 1
    focusPage = Int(focusIndex / pageSize)
    keep = {}
    for p = focusPage - 1 to focusPage + 1
        if p >= 0 and p < pages then keep[p.ToStr()] = true
    end for
    out = {keep: [], load: [], drop: []}
    for each k in keep
        out.keep.Push(k.ToInt())
    end for
    out.keep.Sort()
    have = {}
    for each p in loaded
        have[p.ToStr()] = true
        if not keep.DoesExist(p.ToStr()) then out.drop.Push(p)
    end for
    for each p in out.keep
        if not have.DoesExist(p.ToStr()) then out.load.Push(p)
    end for
    return out
end function

' Seconds as "1:02:03" or "2:03" (a time on a label is left to right in both languages).
function FormatTime(sec as Dynamic) as String
    if sec = invalid then return "0:00"
    s = Int(sec)
    if s < 0 then s = 0
    h = Int(s / 3600)
    mm = Int((s mod 3600) / 60)
    ss = s mod 60
    if h > 0 then return h.ToStr() + ":" + nav_Two(mm) + ":" + nav_Two(ss)
    return mm.ToStr() + ":" + nav_Two(ss)
end function

function nav_Two(n as Integer) as String
    if n < 10 then return "0" + n.ToStr()
    return n.ToStr()
end function

' A card's title: its title, else the file name without its extension.
function CardTitle(item as Object) as String
    for each k in ["display_title", "title", "name"]
        v = item[k]
        if v <> invalid and (type(v) = "roString" or type(v) = "String") and v <> "" then return v
    end for
    f = item.filename
    if f = invalid or f = "" then return ""
    dot = 0
    for i = Len(f) to 1 step -1
        if Mid(f, i, 1) = "." then
            dot = i
            exit for
        end if
    end for
    if dot > 1 then return Left(f, dot - 1)
    return f
end function

' Where playback resumes: playback_state.position_sec (or position_sec) when the video is not
' finished and has one, else 0.
function ResumePosition(video as Object) as Integer
    st = video.playback_state
    where = invalid
    completed = false
    if type(st) = "roAssociativeArray" then
        where = st.position_sec
        if st.completed = true then completed = true
    end if
    if where = invalid then where = video.position_sec
    if video.completed = true then completed = true
    if completed or where = invalid then return 0
    if where <= 0 then return 0
    return Int(where)
end function
