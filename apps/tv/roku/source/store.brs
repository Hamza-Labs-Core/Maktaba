' What the channel keeps on the Roku (Epic 58 story 58.12), in its roRegistrySection: per
' channel, encrypted at rest by Roku, and removed with the channel.
'
'   StoreHomes()            ' the saved homes, the last used first
'   StoreSaveHome(home)     ' {id, name, base, plain, access, refresh, user, admin}
'   StoreForget(id)
'   StoreLang() / StoreSetLang("ar")
'
' A home's tokens are the access and refresh tokens the home gave at sign-in (by code). No
' password is ever kept, or sent.

function store_Section() as Object
    return CreateObject("roRegistrySection", "maktaba")
end function

function StoreHomes() as Object
    raw = store_Section().Read("homes")
    if raw = invalid or raw = "" then return []
    list = ParseJson(raw)
    if type(list) <> "roArray" then return []
    return list
end function

sub store_Write(list as Object)
    sec = store_Section()
    sec.Write("homes", FormatJson(list))
    sec.Flush()
end sub

' Saves a home first in the list (the last used), replacing its earlier entry.
sub StoreSaveHome(home as Object)
    list = [home]
    for each h in StoreHomes()
        if h.id <> home.id then list.Push(h)
    end for
    store_Write(list)
end sub

' Updates a saved home's tokens in place (a refresh), keeping its place in the list.
sub StoreSaveTokens(id as String, access as String, refresh as String)
    list = StoreHomes()
    for each h in list
        if h.id = id then
            h.access = access
            h.refresh = refresh
        end if
    end for
    store_Write(list)
end sub

sub StoreForget(id as String)
    list = []
    for each h in StoreHomes()
        if h.id <> id then list.Push(h)
    end for
    store_Write(list)
end sub

function StoreLang() as String
    v = store_Section().Read("lang")
    if v = "ar" then return "ar"
    if v = "en" then return "en"
    ' first launch: the Roku's own language
    di = CreateObject("roDeviceInfo")
    if di <> invalid then
        loc = di.GetCurrentLocale()
        if loc <> invalid and LCase(Left(loc, 2)) = "ar" then return "ar"
    end if
    return "en"
end function

sub StoreSetLang(lang as String)
    sec = store_Section()
    sec.Write("lang", lang)
    sec.Flush()
end sub
