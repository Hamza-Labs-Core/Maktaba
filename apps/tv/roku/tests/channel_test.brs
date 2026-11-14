' Story 58.12 T01: the channel's logic on brs-engine (the API client and its token refresh,
' reaching a home and the subnet probe, the deep-link router, the remote's rules, the grid's
' memory window, what is kept in the registry).
sub Main()
    m.pass = 0
    m.fail = 0
    t_Client()
    t_Net()
    t_DeepLink()
    t_Nav()
    t_Store()
    ck_Result()
end sub

function t_Session() as Object
    return {base: "https://1-2-3-4.home.example:8443/", access: "A1", refresh: "R1", plain: false}
end function

sub t_Client()
    ' a plain request carries the bearer, the client header and JSON
    tr = ck_FakeTransport([{status: 200, body: "{""rows"":1}"}])
    c = ApiClientNew(t_Session(), tr)
    r = c.Request("GET", "/api/home", invalid)
    ck_Eq("200 status", r.status, 200)
    ck_Eq("200 data", r.data.rows, 1)
    ck_Eq("url joined", tr.sent[0].url, "https://1-2-3-4.home.example:8443/api/home")
    ck_Eq("bearer", tr.sent[0].headers["Authorization"], "Bearer A1")
    ck_True("client header", Left(tr.sent[0].headers["X-Maktaba-Client"], 3) = "tv/")
    ck_True("client header names roku", Right(tr.sent[0].headers["X-Maktaba-Client"], 11) = " (roku; tv)")
    ck_Eq("accept", tr.sent[0].headers["Accept"], "application/json")
    ck_Eq("no refresh", c.changed, false)

    ' a 401 takes one refresh and one retry with the new token
    tr = ck_FakeTransport([{status: 401, body: ""}, {status: 200, body: "{""access_token"":""A2"",""refresh_token"":""R2""}"}, {status: 200, body: "{}"}])
    c = ApiClientNew(t_Session(), tr)
    r = c.Request("POST", "/api/me/play-queues", {shuffle: true})
    ck_Eq("401 then ok", r.status, 200)
    ck_Eq("three requests", tr.sent.Count(), 3)
    ck_Eq("refresh path", tr.sent[1].url, "https://1-2-3-4.home.example:8443/api/auth/refresh")
    ck_Eq("refresh body", ParseJson(tr.sent[1].body).refresh_token, "R1")
    ck_True("refresh sends no bearer", tr.sent[1].headers["Authorization"] = invalid)
    ck_Eq("retry carries the new token", tr.sent[2].headers["Authorization"], "Bearer A2")
    ck_Eq("retry keeps its body", ParseJson(tr.sent[2].body).shuffle, true)
    ck_Eq("session renewed", c.session.refresh, "R2")
    ck_Eq("changed", c.changed, true)
    ck_Eq("still signed in", c.signedOut, false)

    ' a second 401 after the refresh signs out
    tr = ck_FakeTransport([{status: 401, body: ""}, {status: 200, body: "{""access_token"":""A2"",""refresh_token"":""R2""}"}, {status: 401, body: ""}])
    c = ApiClientNew(t_Session(), tr)
    r = c.Request("GET", "/api/home", invalid)
    ck_Eq("second 401", r.status, 401)
    ck_Eq("signed out after a second 401", c.signedOut, true)
    ck_Eq("tokens cleared", c.session.access, "")

    ' a refresh the home refuses signs out, and nothing is retried
    tr = ck_FakeTransport([{status: 401, body: ""}, {status: 401, body: ""}])
    c = ApiClientNew(t_Session(), tr)
    r = c.Request("GET", "/api/home", invalid)
    ck_Eq("refused refresh: two requests", tr.sent.Count(), 2)
    ck_Eq("refused refresh signs out", c.signedOut, true)
    ck_Eq("refused refresh clears the refresh token", c.session.refresh, "")

    ' a refresh that hits a 503 keeps the tokens (the home may be restarting)
    tr = ck_FakeTransport([{status: 401, body: ""}, {status: 503, body: ""}])
    c = ApiClientNew(t_Session(), tr)
    r = c.Request("GET", "/api/home", invalid)
    ck_Eq("503 on refresh: still signed in", c.signedOut, false)
    ck_Eq("503 on refresh: tokens kept", c.session.refresh, "R1")

    ' 426 on refresh: this app is below the home's floor
    tr = ck_FakeTransport([{status: 401, body: ""}, {status: 426, body: "{}"}])
    c = ApiClientNew(t_Session(), tr)
    c.Request("GET", "/api/home", invalid)
    ck_Eq("426 on refresh", c.tooOld, true)

    ' no refresh token: a 401 signs out at once
    s = t_Session()
    s.refresh = ""
    tr = ck_FakeTransport([{status: 401, body: ""}])
    c = ApiClientNew(s, tr)
    c.Request("GET", "/api/home", invalid)
    ck_Eq("no refresh token: one request", tr.sent.Count(), 1)
    ck_Eq("no refresh token: signed out", c.signedOut, true)

    ' one refresh at a time: two calls that 401 in turn refresh once each time the token is stale
    tr = ck_FakeTransport([{status: 401, body: ""}, {status: 200, body: "{""access_token"":""A2"",""refresh_token"":""R2""}"}, {status: 200, body: "{}"}, {status: 200, body: "{}"}])
    c = ApiClientNew(t_Session(), tr)
    c.Request("GET", "/api/home", invalid)
    c.Request("GET", "/api/libraries", invalid)
    ck_Eq("the second call uses the renewed token", tr.sent[3].headers["Authorization"], "Bearer A2")
    ck_Eq("one refresh", tr.sent.Count(), 4)
end sub

sub t_Net()
    ck_Eq("join", NetJoin("http://a:8088", "api/x"), "http://a:8088/api/x")
    ck_Eq("join slashes", NetJoin("http://a:8088//", "/api/x"), "http://a:8088/api/x")
    ck_Eq("join absolute", NetJoin("http://a", "https://b/x"), "https://b/x")
    ck_Eq("query", NetQuery({library: "L 1", limit: 60, q: "سلام"}), "?library=L%201&limit=60&q=%D8%B3%D9%84%D8%A7%D9%85")
    ck_Eq("query empty", NetQuery({}), "")
    ck_Eq("host", NetHost("https://A.b.example:8443/x?y"), "a.b.example")
    ck_Eq("host none", NetHost("nothing"), "")
    ck_True("ipv4", NetIsIPv4("192.168.1.20"))
    ck_Eq("not ipv4", NetIsIPv4("192.168.1.256"), false)
    ck_Eq("not ipv4 letters", NetIsIPv4("192.168.a.1"), false)

    ' typed addresses
    ck_Eq("typed bare ip", NetNormalizeBase(" 192.168.1.20 "), "http://192.168.1.20:8088")
    ck_Eq("typed host and port", NetNormalizeBase("Home.local:8443"), "http://home.local:8443")
    ck_Eq("typed https", NetNormalizeBase("https://home.example"), "https://home.example")
    ck_Eq("typed path dropped", NetNormalizeBase("http://10.0.0.2:8088/web"), "http://10.0.0.2:8088")
    ck_Eq("typed nothing", NetNormalizeBase("  "), "")
    ck_Eq("typed credentials refused", NetNormalizeBase("http://u:p@10.0.0.2"), "")
    ck_Eq("typed bad port", NetNormalizeBase("10.0.0.2:99999"), "")
    ck_Eq("typed bad char", NetNormalizeBase("10.0.0.2;rm"), "")

    ' discovery answers
    ck_True("home answer", NetIsHomeAnswer({id: "h1", urls: []}))
    ck_Eq("no id", NetIsHomeAnswer({urls: []}), false)
    ck_Eq("empty id", NetIsHomeAnswer({id: "", urls: []}), false)
    ck_Eq("no urls", NetIsHomeAnswer({id: "h1"}), false)
    ck_Eq("not an object", NetIsHomeAnswer("x"), false)

    ' a home's direct name for the address it answered from, and nothing else
    ck_True("direct name", NetDirectNameOf("https://192-168-1-20.h1.direct.example:8443", "http://192.168.1.20:8088", "H1"))
    ck_Eq("another address", NetDirectNameOf("https://192-168-1-21.h1.direct.example", "http://192.168.1.20:8088", "h1"), false)
    ck_Eq("another home", NetDirectNameOf("https://192-168-1-20.h2.direct.example", "http://192.168.1.20:8088", "h1"), false)
    ck_Eq("plain http name", NetDirectNameOf("http://192-168-1-20.h1.direct.example", "http://192.168.1.20:8088", "h1"), false)
    ck_Eq("found by name", NetDirectNameOf("https://192-168-1-20.h1.direct.example", "http://home.local:8088", "h1"), false)
    ans = {id: "h1", urls: ["https://evil.example", "https://192-168-1-20.h1.d.example:8443/", "https://relay.example/h1"]}
    ck_Eq("direct names", NetDirectNames(ans, "http://192.168.1.20:8088"), ["https://192-168-1-20.h1.d.example:8443"])

    ' the cloud's connections a Roku can use
    conns = [
        {kind: "local", url: "https://192-168-1-20.h1.d.example:8443"}
        {kind: "local_ip", url: "https://192.168.1.20:8443", tls_name: "192-168-1-20.h1.d.example"}
        {kind: "direct", url: "https://h1.d.example:443"}
        {kind: "relay", url: "https://relay.maktaba.tv/h/h1"}
    ]
    got = NetCloudConnections(conns)
    ck_Eq("pinned local_ip left out", got.Count(), 3)
    ck_Eq("order kept", got[0].kind, "local")
    ck_Eq("relay last", got[2].kind, "relay")
    ck_Eq("no connections", NetCloudConnections(invalid).Count(), 0)

    ' the subnet probe
    targets = NetProbeTargets("192.168.1.9")
    ck_Eq("253 addresses", targets.Count(), 253)
    ck_Eq("first", targets[0], "192.168.1.1")
    ck_Eq("skips its own", targets[8], "192.168.1.10")
    ck_Eq("last", targets[252], "192.168.1.254")
    ck_Eq("no address, no probe", NetProbeTargets("").Count(), 0)
    ck_Eq("device ip", NetDeviceIp({eth1: "fe80::1", wlan0: "10.0.0.7"}), "10.0.0.7")
    ck_Eq("no device ip", NetDeviceIp({}), "")
end sub

sub t_DeepLink()
    id = "0b5e6f8c-1d2e-4f3a-9b8c-7d6e5f4a3b2c"
    link = DeepLinkParse({contentId: id, mediaType: "movie"})
    ck_True("parsed", link <> invalid)
    ck_True("valid", link.valid)
    ck_Eq("plays", DeepLinkAction(link, 200, {id: id}), "play")
    ck_Eq("404 is not available", DeepLinkAction(link, 404, invalid), "not_available")
    ck_Eq("403 is not available", DeepLinkAction(link, 403, {}), "not_available")
    ck_Eq("not playable", DeepLinkAction(link, 200, {id: id, playable: false}), "not_available")
    for each mt in ["episode", "series", "shortFormVideo"]
        ck_True("media type " + mt, DeepLinkParse({contentId: id, mediaType: mt}).valid)
    end for
    ck_Eq("unknown media type", DeepLinkParse({contentId: id, mediaType: "live"}).valid, false)
    ck_Eq("not an id", DeepLinkParse({contentId: "../../admin", mediaType: "movie"}).valid, false)
    ck_Eq("an invalid link is not available", DeepLinkAction(DeepLinkParse({contentId: "x", mediaType: "movie"}), 200, {}), "not_available")
    ck_Eq("no link", DeepLinkParse({}), invalid)
    ck_Eq("not a link", DeepLinkParse(invalid), invalid)
    ck_True("uuid", IsUuid("0B5E6F8C-1D2E-4F3A-9B8C-7D6E5F4A3B2C"))
    ck_Eq("not uuid", IsUuid("0b5e6f8c-1d2e-4f3a-9b8c-7d6e5f4a3b2"), false)
end sub

sub t_Nav()
    ' Back's three steps on a library, one confirm on Home
    ck_Eq("library deep in a tab", NavBack("library", false, false), "top")
    ck_Eq("library at the top", NavBack("library", false, true), "tabs")
    ck_Eq("library on its tabs", NavBack("library", true, true), "leave")
    ck_Eq("home deep in a row", NavBack("home", false, false), "top")
    ck_Eq("home at the top", NavBack("home", false, true), "confirm_exit")
    ck_Eq("home on its nav", NavBack("home", true, true), "confirm_exit")
    ck_Eq("series deep", NavBack("series", false, false), "top")
    ck_Eq("series at the top", NavBack("series", false, true), "leave")
    ck_Eq("play on a card", NavCardKey("play"), "play")
    ck_Eq("star on a card", NavCardKey("options"), "actions")
    ck_Eq("OK on a card", NavCardKey("OK"), "open")
    ck_Eq("other keys", NavCardKey("rewind"), "")

    ltr = LayoutNew(false, 1920)
    rtl = LayoutNew(true, 1920)
    ck_Eq("tab right", NavTabStep(1, 5, "right", ltr), 2)
    ck_Eq("tab left at start", NavTabStep(0, 5, "left", ltr), 0)
    ck_Eq("tab right at end", NavTabStep(4, 5, "right", ltr), 4)
    ck_Eq("arabic: left moves forward", NavTabStep(1, 5, "left", rtl), 2)
    ck_Eq("arabic: right moves back", NavTabStep(1, 5, "right", rtl), 0)

    ' at most three pages of posters loaded, whatever the grid's length
    w = GridWindow(0, 30, 1000, [])
    ck_Eq("first page and the next", w.keep, [0, 1])
    ck_Eq("loads them", w.load, [0, 1])
    w = GridWindow(95, 30, 1000, [0, 1, 2])
    ck_Eq("window moves", w.keep, [2, 3, 4])
    ck_Eq("drops behind", w.drop, [0, 1])
    ck_Eq("loads ahead", w.load, [3, 4])
    maxKept = 0
    loaded = []
    for f = 0 to 999 step 7
        w = GridWindow(f, 30, 1000, loaded)
        loaded = w.keep
        if loaded.Count() > maxKept then maxKept = loaded.Count()
    end for
    ck_Eq("never more than three pages", maxKept, 3)
    ck_Eq("empty grid", GridWindow(0, 30, 0, []).keep, [])

    ck_Eq("time", FormatTime(3723), "1:02:03")
    ck_Eq("time short", FormatTime(125.7), "2:05")
    ck_Eq("time none", FormatTime(invalid), "0:00")
    ck_Eq("card title", CardTitle({title: "A film"}), "A film")
    ck_Eq("card filename", CardTitle({title: "", filename: "trip.2019.mkv"}), "trip.2019")
    ck_Eq("resume", ResumePosition({playback_state: {position_sec: 754.2}}), 754)
    ck_Eq("resume completed", ResumePosition({playback_state: {position_sec: 754, completed: true}}), 0)
    ck_Eq("resume none", ResumePosition({}), 0)
    ck_Eq("resume card", ResumePosition({position_sec: 30}), 30)
end sub

sub t_Store()
    StoreForget("h1")
    StoreForget("h2")
    StoreSaveHome({id: "h1", name: "One", base: "http://10.0.0.2:8088", plain: true, access: "a", refresh: "r"})
    StoreSaveHome({id: "h2", name: "Two", base: "https://x", plain: false, access: "b", refresh: "s"})
    homes = StoreHomes()
    ck_Eq("two homes", homes.Count(), 2)
    ck_Eq("last used first", homes[0].id, "h2")
    StoreSaveHome({id: "h1", name: "One", base: "http://10.0.0.2:8088", plain: true, access: "a", refresh: "r"})
    ck_Eq("saved again moves first", StoreHomes()[0].id, "h1")
    ck_Eq("no duplicate", StoreHomes().Count(), 2)
    StoreSaveTokens("h2", "b2", "s2")
    ck_Eq("tokens renewed in place", StoreHomes()[1].refresh, "s2")
    ck_Eq("order kept", StoreHomes()[0].id, "h1")
    StoreForget("h1")
    ck_Eq("forgotten", StoreHomes().Count(), 1)
    StoreSetLang("ar")
    ck_Eq("language kept", StoreLang(), "ar")
    StoreSetLang("en")
    ck_Eq("language back", StoreLang(), "en")
    StoreForget("h2")
end sub
