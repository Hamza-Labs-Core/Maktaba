' The capability report (Epic 58 story 58.13; 58.02's device_caps). The player sends it with
' each stream session, and the home decides from it what this Roku plays: direct, remuxed into
' HLS or DASH, or transcoded.
'
'   answers = CapsAnswers()          ' roDeviceInfo's answers on this Roku, in the shape that
'                                    ' shared/playback/caps-vectors.json records for three models
'   caps = CapsFromAnswers(answers)  ' the device_caps the home reads (58.02's DeviceCaps)
'
' The two halves are apart so the report can be tested on brs-engine (T01): the vectors hold each
' model's recorded answers and the report its channel must build from them.

' roDeviceInfo's answers: CanDecodeVideo for H.264, HEVC Main10 at 2160p and AV1 (by DASH and by
' HLS: Roku plays AV1 only inside DASH), GetDisplayProperties for HDR10, HLG and Dolby Vision,
' GetVideoMode, CanDecodeAudio for AAC and the Dolby and DTS passthroughs, GetAudioOutputChannel.
function CapsAnswers() as Object
    di = CreateObject("roDeviceInfo")
    out = {can_decode_video: [], can_decode_audio: [], display_properties: {}, video_mode: "", audio_output_channel: ""}
    if di = invalid then return out
    ' H.264 at level 5.1 (4K), else 4.2 (a 1080p Roku Express): every Roku plays H.264
    asks = [{codec: "mpeg4 avc", profile: "high", level: "5.1"}, {codec: "mpeg4 avc", profile: "high", level: "4.2"}, {codec: "hevc", profile: "main10", level: "5.1"}, {codec: "av1", container: "dash"}, {codec: "av1", container: "hls"}, {codec: "vp9", profile: "profile 2"}]
    for each q in asks
        ask = {Codec: q.codec}
        if q.profile <> invalid then ask.Profile = q.profile
        if q.level <> invalid then ask.Level = q.level
        if q.container <> invalid then ask.Container = q.container
        r = di.CanDecodeVideo(ask)
        a = {}
        a.Append(q)
        a.result = (type(r) = "roAssociativeArray" and r.Result = true)
        out.can_decode_video.Push(a)
    end for
    dp = di.GetDisplayProperties()
    if type(dp) = "roAssociativeArray" then
        out.display_properties = {Hdr10: (dp.Hdr10 = true), Hlg: (dp.Hlg = true), DolbyVision: (dp.DolbyVision = true)}
        ' Roku plays Dolby Vision as profile 5 only (research.md §6.2)
        if dp.DolbyVision = true then out.display_properties.dolby_vision_profiles = ["dvhe.05"]
    end if
    mode = di.GetVideoMode()
    if mode <> invalid then out.video_mode = mode
    for each q in [{codec: "aac"}, {codec: "ac3", passthru: true}, {codec: "eac3", passthru: true}, {codec: "dts", passthru: true}, {codec: "truehd"}]
        ask = {Codec: q.codec}
        if q.passthru = true then ask.PassThru = 1
        r = di.CanDecodeAudio(ask)
        a = {}
        a.Append(q)
        a.result = (type(r) = "roAssociativeArray" and r.Result = true)
        out.can_decode_audio.Push(a)
    end for
    ch = di.GetAudioOutputChannel()
    if ch <> invalid then out.audio_output_channel = ch
    return out
end function

' The device_caps for roDeviceInfo's answers. Each video codec the Roku decodes is listed at the
' video mode's height and frame rate (a 1080p Roku Express says 1080p H.264 only, so the home
' transcodes a 4K source); HEVC and AV1 are 10-bit. AV1 counts only when it decodes inside DASH.
' Dolby Vision is listed as profile 5. Audio is what the Roku decodes itself; a codec it can
' only pass to the receiver is a passthrough.
function CapsFromAnswers(a as Object) as Object
    height = 1080
    fps = 30
    mode = ""
    if a.video_mode <> invalid then mode = LCase(a.video_mode)
    p = Instr(1, mode, "p")
    if p > 1 then
        h = Val(Left(mode, p - 1))
        if h > 0 then height = h
        rest = Mid(mode, p + 1)
        b = Instr(1, rest, "b")
        if b > 0 then rest = Left(rest, b - 1)
        f = Val(rest)
        if f > 0 then fps = f
    end if
    video = []
    for each q in caps_List(a.can_decode_video)
        if q.result = true then
            codec = caps_VideoCodec(q.codec)
            if codec = "av1" and LCase(caps_Str(q.container)) <> "dash" then codec = ""
            if codec <> "" and not caps_HasCodec(video, codec) then
                v = {codec: codec, max_height: height, max_fps: fps}
                if codec = "hevc" or codec = "av1" or codec = "vp9" then v.bit_depth = 10
                video.Push(v)
            end if
        end if
    end for
    hdr = []
    dp = a.display_properties
    if type(dp) = "roAssociativeArray" then
        if dp.Hdr10 = true then hdr.Push("hdr10")
        if dp.Hlg = true then hdr.Push("hlg")
        if dp.DolbyVision = true then
            for each prof in caps_List(dp.dolby_vision_profiles)
                if LCase(prof) = "dvhe.05" then hdr.Push("dv-p5")
            end for
        end if
    end if
    audio = []
    pass = []
    for each q in caps_List(a.can_decode_audio)
        if q.result = true then
            if q.passthru = true then
                pass.Push(LCase(q.codec))
            else
                audio.Push(LCase(q.codec))
            end if
        end if
    end for
    return {
        containers: ["mp4", "mkv", "ts"]
        video: video
        hdr: hdr
        audio: audio
        passthrough: pass
        max_audio_channels: caps_Channels(caps_Str(a.audio_output_channel))
        hls_segments: ["ts", "fmp4"]
        dash: true
    }
end function

' The API's codec name for roDeviceInfo's ("mpeg4 avc" is h264); "" for one the API has none for.
function caps_VideoCodec(c as Dynamic) as String
    s = LCase(caps_Str(c))
    if s = "mpeg4 avc" or s = "h264" or s = "avc" then return "h264"
    if s = "hevc" or s = "h265" then return "hevc"
    if s = "av1" then return "av1"
    if s = "vp9" then return "vp9"
    return ""
end function

' The channels GetAudioOutputChannel names: "Stereo", "5.1 surround", "7.1 surround".
function caps_Channels(s as String) as Integer
    s = LCase(s)
    if Instr(1, s, "7.1") > 0 then return 8
    if Instr(1, s, "5.1") > 0 then return 6
    return 2
end function

function caps_HasCodec(list as Object, codec as String) as Boolean
    for each v in list
        if v.codec = codec then return true
    end for
    return false
end function

function caps_List(v as Dynamic) as Object
    if type(v) = "roArray" then return v
    return []
end function

function caps_Str(v as Dynamic) as String
    if v = invalid then return ""
    if type(v) = "roString" or type(v) = "String" then return v
    return ""
end function
