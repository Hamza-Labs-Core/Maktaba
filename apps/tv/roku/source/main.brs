' The Maktaba channel's entry point (Epic 58 story 58.12). It shows the scene, hands it the
' launch's deep link, and passes on each later deep link (roInputEvent: voice, Roku search).

sub Main(args as Dynamic)
    screen = CreateObject("roSGScreen")
    port = CreateObject("roMessagePort")
    screen.SetMessagePort(port)
    scene = screen.CreateScene("MainScene")
    screen.Show()
    input = CreateObject("roInput")
    input.SetMessagePort(port)
    scene.ObserveField("exitChannel", port)
    print "[main] launch "; FormatJson(main_LaunchArgs(args))
    scene.launchArgs = main_LaunchArgs(args)
    while true
        msg = Wait(0, port)
        t = type(msg)
        if t = "roSGScreenEvent" then
            if msg.IsScreenClosed() then return
        else if t = "roInputEvent" then
            info = msg.GetInfo()
            if type(info) = "roAssociativeArray" then
                print "[main] input "; FormatJson(main_LaunchArgs(info))
                scene.inputArgs = main_LaunchArgs(info)
            end if
        else if t = "roSGNodeEvent" then
            if msg.GetField() = "exitChannel" and msg.GetData() = true then
                print "[main] exit"
                screen.Close()
                return
            end if
        end if
    end while
end sub

' The parts of a launch or input event the channel reads.
function main_LaunchArgs(args as Dynamic) as Object
    out = {}
    if type(args) <> "roAssociativeArray" then return out
    for each k in ["contentId", "mediaType", "source"]
        if args[k] <> invalid then out[k] = args[k]
    end for
    return out
end function
