' What every screen of the channel shares (Epic 58 story 58.12): its strings and layout in the
' channel's language, Ink & Brass colours (Epic 48's tokens), the bundled font, and calls to the
' home through the ApiTask.

sub ui_Init()
    m.i18n = I18nNew(m.global.lang)
    m.rtl = m.i18n.rtl
    m.layout = LayoutNew(m.rtl, 1920)
end sub

function ui_Colors() as Object
    return {
        bg: "0x0B111CFF"
        fg: "0xE8ECF3FF"
        muted: "0xA9B3C4FF"
        subtle: "0x131D2EFF"
        accent: "0xE3B252FF"
        accentFg: "0x1A1408FF"
        border: "0x22304AFF"
        error: "0xF0707FFF"
    }
end function

function ui_Font(size as Integer, bold as Boolean) as Object
    f = CreateObject("roSGNode", "Font")
    if bold then
        f.uri = "pkg:/fonts/IBMPlexSansArabic-SemiBold.ttf"
    else
        f.uri = "pkg:/fonts/IBMPlexSansArabic-Regular.ttf"
    end if
    f.size = size
    return f
end function

' A label in the channel's style: its font, colour and alignment (mirrored in Arabic).
sub ui_Label(lbl as Object, size as Integer, color as String, bold as Boolean)
    lbl.font = ui_Font(size, bold)
    lbl.color = color
    lbl.horizAlign = m.layout.Align("left")
end sub

function ui_Tr(key as String) as String
    return m.i18n.Tr(key)
end function

' Text from the home (titles, names), shaped for Roku: Arabic joined and in display order.
function ui_Vis(s as Dynamic, widthChars as Integer) as String
    if s = invalid then return ""
    return VisualText(s, widthChars)
end function

' Calls the home: the response arrives in callback (a function name of this component) as an
' roSGNodeEvent whose node is the request (its tag tells calls apart).
function ui_Api(method as String, path as String, body as Dynamic, tag as String, callback as String) as Object
    req = CreateObject("roSGNode", "ApiRequest")
    req.method = method
    req.path = path
    req.tag = tag
    if body <> invalid then
        req.body = body
        req.hasBody = true
    end if
    req.ObserveFieldScoped("response", callback)
    m.global.api.request = req
    return req
end function

' The poster of a video at the small size (the scene's HTTP agent carries the bearer).
function ui_Poster(videoId as Dynamic) as String
    if videoId = invalid or videoId = "" then return ""
    return NetJoin(m.global.session.base, "/api/videos/" + videoId + "/poster?size=small")
end function

' Asks the scene to open a screen, go back, or do something only it can.
sub ui_Navigate(nav as Object)
    m.top.navigate = nav
end sub

sub ui_Log(s as String)
    print "[ui] "; s
end sub

' A card's content node from a home's item (a video card, a queue item, a series, a
' collection): its title shaped once here, where the row is loaded (story 58.15: item
' components are recycled as a row scrolls), its poster, its progress and what it opens.
function ui_Item(parent as Object, item as Object, kind as String) as Object
    it = parent.CreateChild("ContentNode")
    it.AddFields({progress: 0.0, rtl: false, itemId: "", kind: kind, seriesId: "", resume: 0})
    title = CardTitle(item)
    it.title = VisualText(title, 26)
    it.rtl = VisualIsRtl(title)
    id = item.id
    if id = invalid then id = item.video_id
    if id = invalid then id = ""
    it.itemId = id
    posterId = id
    if kind = "series" or kind = "collection" or kind = "playlist" then
        posterId = item.poster_video_id
        if posterId = invalid and type(item.poster_video_ids) = "roArray" and item.poster_video_ids.Count() > 0 then posterId = item.poster_video_ids[0]
    end if
    if posterId <> invalid then it.HDPosterUrl = ui_Poster(posterId)
    if type(item.series) = "roAssociativeArray" and item.series.id <> invalid then it.seriesId = item.series.id
    if item.series_id <> invalid then it.seriesId = item.series_id
    d = item.duration_sec
    p = item.position_sec
    if d <> invalid and p <> invalid and d > 0 and item.completed <> true then
        it.progress = p / d
        it.resume = Int(p)
    end if
    return it
end function

' The items of a list, in the layout's order (reversed in Arabic: a row reads from the right).
function ui_Ordered(list as Dynamic) as Object
    if type(list) <> "roArray" then return []
    return m.layout.Order(list)
end function
