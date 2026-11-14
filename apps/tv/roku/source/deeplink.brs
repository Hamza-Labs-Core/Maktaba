' Deep links (Epic 58 story 58.12; Roku requires them of channels with video). A launch, or an
' roInputEvent while the channel runs, carries contentId (a video's id) and mediaType:
'
'   link = DeepLinkParse(args)           ' {contentId, mediaType}, or invalid when it is none
'   DeepLinkAction(link, status, video)  ' "play" or "not_available", from GET /api/videos/{id}
'
' A link the signed-in person cannot open (missing, another library, hidden by parental
' controls: the home answers 404 for all of them, story 58.14) opens Home with the
' not-available line, never a hint that the video exists.

function DeepLinkMediaTypes() as Object
    return {movie: true, episode: true, series: true, shortFormVideo: true}
end function

function DeepLinkParse(args as Dynamic) as Dynamic
    if type(args) <> "roAssociativeArray" then return invalid
    id = args.contentId
    mt = args.mediaType
    if id = invalid or mt = invalid then return invalid
    if type(id) <> "roString" and type(id) <> "String" then return invalid
    if type(mt) <> "roString" and type(mt) <> "String" then return invalid
    if id = "" and mt = "" then return invalid
    return {contentId: id.Trim(), mediaType: mt.Trim(), valid: DeepLinkValid(id.Trim(), mt.Trim())}
end function

' Whether a link names a video id (a UUID) and one of the media types the channel publishes.
function DeepLinkValid(id as String, mediaType as String) as Boolean
    if not DeepLinkMediaTypes().DoesExist(mediaType) then return false
    return IsUuid(id)
end function

function DeepLinkAction(link as Dynamic, status as Integer, video as Dynamic) as String
    if link = invalid or not link.valid then return "not_available"
    if status <> 200 or type(video) <> "roAssociativeArray" then return "not_available"
    if video.playable <> invalid and video.playable = false then return "not_available"
    return "play"
end function

function IsUuid(s as String) as Boolean
    if Len(s) <> 36 then return false
    for i = 1 to 36
        ch = LCase(Mid(s, i, 1))
        if i = 9 or i = 14 or i = 19 or i = 24 then
            if ch <> "-" then return false
        else if not ((ch >= "0" and ch <= "9") or (ch >= "a" and ch <= "f")) then
            return false
        end if
    end for
    return true
end function
