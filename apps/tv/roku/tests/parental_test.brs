' Story 58.14 T04 (the logic half; screens.test.mjs drives the screens): the parental limit as
' the channel keeps it (source/parental.brs), its lines in both languages, and what became of
' the account's own request.
sub Main()
    m.pass = 0
    m.fail = 0
    en = I18nNew("en")
    ar = I18nNew("ar")

    ' GET /api/me/parental's answers
    child = ParentalFrom(ParseJson("{""restricted"":true,""max_tier"":9,""allow_only"":false,""version"":3}"))
    ck_Eq("child restricted", child.restricted, true)
    ck_Eq("child tier", child.maxTier, 9)
    ck_True("child limited", ParentalLimited(child))
    device = ParentalFrom(ParseJson("{""restricted"":false,""max_tier"":13,""device_limit"":13}"))
    ck_True("a device limit is a limit", ParentalLimited(device))
    ck_Eq("no limit", ParentalLimited(ParentalFrom(ParseJson("{""restricted"":false}"))), false)
    ck_Eq("an older home (no answer)", ParentalLimited(ParentalFrom(invalid)), false)
    ck_Eq("no session field", ParentalLimited(invalid), false)

    ' the blocked page's line
    ck_Eq("en account line", ParentalLimitLine(child, en), "Your account is limited to 9+.")
    ck_Eq("en device line", ParentalLimitLine(device, en), "This device is limited to 13+.")
    ck_Eq("en all ages", ParentalLimitLine(ParentalFrom({restricted: true, max_tier: 0}), en), "Your account is limited to All ages.")
    ck_Eq("en nothing with a tier", ParentalLimitLine(ParentalFrom({restricted: true, max_tier: -1}), en), "Your account is limited to what a parent allowed.")
    ck_Eq("no line without a limit", ParentalLimitLine(ParentalFrom(invalid), en), "")
    got = ar.Format("parental.blocked.account", ["9"])
    ck_Eq("ar account line is the shaped template", ParentalLimitLine(child, ar), got)

    ' Account's lines
    lines = ParentalManagedLines(child, en)
    ck_Eq("en managed lines", lines, ["A parent manages this account.", "Content limit: 9+", "A parent can answer from Maktaba on their phone."])
    ck_Eq("device managed lines", ParentalManagedLines(device, en), ["This device is limited to 13+."])
    ck_Eq("no managed lines", ParentalManagedLines(ParentalFrom(invalid), en), [])
    ck_Eq("ar managed lines count", ParentalManagedLines(child, ar).Count(), 3)
    ck_Eq("ar managed first line", ParentalManagedLines(child, ar)[0], ar.Tr("parental.profile.managed"))

    ' the account's own requests, newest first
    list = ParseJson("{""requests"":[{""id"":""r2"",""subject_kind"":""video"",""subject_id"":""v9"",""status"":""approved""},{""id"":""r1"",""subject_kind"":""video"",""subject_id"":""v9"",""status"":""denied""},{""id"":""r0"",""subject_kind"":""series"",""subject_id"":""s1"",""status"":""pending""}]}")
    ck_Eq("the latest for v9", ParentalRequestState(list, "video", "v9"), "approved")
    ck_Eq("a series pending", ParentalRequestState(list, "series", "s1"), "pending")
    ck_Eq("none for another", ParentalRequestState(list, "video", "v1"), "none")
    ck_Eq("an ended request", ParentalRequestState(ParseJson("{""requests"":[{""subject_kind"":""video"",""subject_id"":""v9"",""status"":""expired""}]}"), "video", "v9"), "expired")
    ck_Eq("an older home", ParentalRequestState(invalid, "video", "v9"), "none")
    ' a request for more time: the latest, whatever minutes it asked for
    timeList = ParseJson("{""requests"":[{""id"":""r5"",""subject_kind"":""time"",""subject_id"":""30"",""status"":""pending""},{""id"":""r4"",""subject_kind"":""time"",""subject_id"":""15"",""status"":""denied""}]}")
    ck_Eq("time pending", ParentalRequestState(timeList, "time", ""), "pending")
    ck_Eq("no time request", ParentalRequestState(list, "time", ""), "none")

    ' the player: what the home's answer to a session open or a progress post means
    over = ParseJson("{""type"":""parental-time"",""status"":403,""code"":""parental_time"",""reason"":""limit_reached"",""next_allowed_at"":""2026-11-15T07:00:00Z""}")
    ck_Eq("time's up", ParentalStop(403, over), "time")
    slot = ParseJson("{""status"":403,""code"":""parental_time"",""reason"":""outside_slot""}")
    ck_Eq("outside watching hours", ParentalStop(403, slot), "time")
    ck_Eq("a lapsed lease", ParentalStop(403, ParseJson("{""status"":403,""code"":""parental_time"",""reason"":""no_heartbeat""}")), "lapse")
    ck_Eq("time unknown", ParentalStop(503, ParseJson("{""status"":503,""code"":""parental_time_unknown""}")), "unknown")
    ck_Eq("a busy transcoder is not parental", ParentalStop(503, ParseJson("{""status"":503,""detail"":""Every transcode slot is taken.""}")), "")
    ck_Eq("a restricted 403 is not time", ParentalStop(403, ParseJson("{""status"":403,""code"":""restricted""}")), "")
    ck_Eq("a 404 is the player's", ParentalStop(404, invalid), "")
    ck_Eq("no body", ParentalStop(403, invalid), "")
    t = ParentalTimeUp(over)
    ck_Eq("refusal reason", t.reason, "limit_reached")
    ck_Eq("refusal next", t.nextAt, "2026-11-15T07:00:00Z")
    ck_Eq("slot reason", ParentalTimeUp(slot).reason, "outside_slot")
    ck_Eq("no next", ParentalTimeUp(slot).nextAt, "")
    ck_Eq("title for the limit", en.Tr(ParentalTimeTitleKey("limit_reached")), "Time's up for today")
    ck_Eq("title outside hours", en.Tr(ParentalTimeTitleKey("outside_slot")), "Not watching time yet")

    ' when watching opens again, in this Roku's time
    d = CreateObject("roDateTime")
    d.FromISO8601String("2026-11-14T12:00:00Z")
    noon = d.AsSeconds()
    ck_Eq("today, UTC", ParentalNextWords("2026-11-14T18:30:00Z", noon, 0, en), "18:30")
    ck_Eq("today, UTC+3", ParentalNextWords("2026-11-14T18:30:00Z", noon, 3 * 3600, en), "21:30")
    ck_Eq("tomorrow", ParentalNextWords("2026-11-15T07:00:00Z", noon, 0, en), "Sunday 07:00")
    ck_Eq("tomorrow already, UTC+3", ParentalNextWords("2026-11-14T22:00:00Z", noon, 3 * 3600, en), "Sunday 01:00")
    ck_Eq("tomorrow in Arabic", ParentalNextWords("2026-11-15T07:00:00Z", noon, 0, ar), ar.Tr("roku.weekday.0") + " 07:00")
    ck_Eq("no instant", ParentalNextWords("", noon, 0, en), "")
    ck_Eq("en next line", en.Format("parental.time.next", ["18:30"]), "You can watch again at 18:30")
    ck_Eq("ask minutes", ParentalAskMinutes(), [15, 30, 60])
    ck_Eq("next with a fraction and an offset", ParentalNextWords("2026-11-14T21:30:00.123456+03:00", noon, 0, en), "18:30")

    ' an earlier day's no to more time is none: asking is offered afresh (the web and Android)
    ago = "2026-11-13T21:00:00Z"
    ahead = "2026-11-14T21:00:00.5+00:00"
    deniedTime = {requests: [{subject_kind: "time", subject_id: "30", status: "denied", expires_at: ago}]}
    ck_Eq("an earlier day's no to more time", ParentalRequestState(deniedTime, "time", "", noon), "none")
    deniedTime.requests[0].expires_at = ahead
    ck_Eq("today's no to more time", ParentalRequestState(deniedTime, "time", "", noon), "denied")
    deniedTime.requests[0].expires_at = invalid
    ck_Eq("a no with no end stays a no", ParentalRequestState(deniedTime, "time", "", noon), "denied")
    ' a no to a video is about the video, whatever its expires_at
    deniedVideo = {requests: [{subject_kind: "video", subject_id: "v9", status: "denied", expires_at: ago}]}
    ck_Eq("a no to a video stays", ParentalRequestState(deniedVideo, "video", "v9", noon), "denied")
    ' a request still listed as waiting past its own end has expired
    late = {requests: [{subject_kind: "time", subject_id: "15", status: "pending", expires_at: ago}]}
    ck_Eq("waiting past its end", ParentalRequestState(late, "time", "", noon), "expired")
    late.requests[0].expires_at = ahead
    ck_Eq("waiting within its end", ParentalRequestState(late, "time", "", noon), "pending")
    ck_Eq("the device clock when no time is given", ParentalRequestState(deniedVideo, "video", "v9"), "denied")
    ck_Eq("seconds of an offset instant", parental_Seconds("2026-11-14T15:00:00+03:00"), noon)
    ck_Eq("seconds of a negative offset", parental_Seconds("2026-11-14T07:00:00-05:00"), noon)
    ck_Eq("seconds of a fraction", parental_Seconds("2026-11-14T12:00:00.999Z"), noon)
    ck_Eq("not an instant", parental_Seconds("soon"), 0)

    ' the time left while a video plays: the home's remaining_seconds, never counted here
    ck_Eq("left", ParentalTimeLeft(ParseJson("{""allowed"":true,""limited"":true,""remaining_seconds"":720}")), 720)
    ck_Eq("no rule now", ParentalTimeLeft(ParseJson("{""allowed"":true,""limited"":false,""remaining_seconds"":null}")), -1)
    ck_Eq("over is not a time left", ParentalTimeLeft(ParseJson("{""allowed"":false,""limited"":true,""remaining_seconds"":0}")), -1)
    ck_Eq("a negative answer is none left", ParentalTimeLeft(ParseJson("{""allowed"":true,""limited"":true,""remaining_seconds"":-4}")), 0)
    ck_Eq("an older home", ParentalTimeLeft(invalid), -1)
    ck_Eq("the note at 15 minutes", ParentalShowsLeft(900), true)
    ck_Eq("no note above 15 minutes", ParentalShowsLeft(901), false)
    ck_Eq("no note with no rule", ParentalShowsLeft(-1), false)
    ck_Eq("poll far", ParentalPollSec(720), 30)
    ck_Eq("poll near", ParentalPollSec(360), 10)
    ck_Eq("poll with no rule", ParentalPollSec(-1), 30)
    ck_Eq("time path", ParentalTimePath("lib1"), "/api/me/parental/time?library_id=lib1")
    ck_Eq("time path, no library", ParentalTimePath(invalid), "/api/me/parental/time")

    ' the warnings: each line once as it is crossed, again only after time was given back
    w = ParentalWarningsNew()
    ck_Eq("12 minutes: none", ParentalWarningCrossed(w, 720), 0)
    ck_Eq("5 minutes crossed", ParentalWarningCrossed(w, 298), 300)
    ck_Eq("5 minutes once", ParentalWarningCrossed(w, 280), 0)
    ck_Eq("1 minute crossed", ParentalWarningCrossed(w, 59), 60)
    ck_Eq("1 minute once", ParentalWarningCrossed(w, 30), 0)
    ck_Eq("a parent gave 4 minutes: no warning", ParentalWarningCrossed(w, 240), 0)
    ck_Eq("1 minute again", ParentalWarningCrossed(w, 55), 60)
    ck_Eq("a parent gave 30 minutes", ParentalWarningCrossed(w, 1800), 0)
    ck_Eq("5 minutes again", ParentalWarningCrossed(w, 300), 300)
    ck_Eq("no rule now", ParentalWarningCrossed(w, -1), 0)
    opened = ParentalWarningsNew()
    ck_Eq("opened with 2 minutes left: the 5-minute line", ParentalWarningCrossed(opened, 120), 300)
    ck_Eq("it names the time really left", ParentalWarningSeconds(300, 120), 120)
    ck_Eq("within a poll it names the line", ParentalWarningSeconds(300, 292), 300)
    ck_Eq("the 1-minute line", ParentalWarningSeconds(60, 58), 60)

    ' the time in words, rounded down to whole minutes; Arabic in its plural forms
    ck_Eq("en 12 minutes", ParentalTimeWords(745, en), "12 minutes")
    ck_Eq("en 1 minute", ParentalTimeWords(60, en), "1 minute")
    ck_Eq("en under a minute", ParentalTimeWords(59, en), "less than a minute")
    ck_Eq("en hours", ParentalTimeWords(7200, en), "2 hours")
    ck_Eq("en hours and minutes", ParentalTimeWords(3900, en), "1 hour and 5 minutes")
    ck_Eq("en note", en.Format("parental.time.left", [ParentalTimeWords(720, en)]), "12 minutes left today")
    ck_Eq("en warning", en.Format("parental.time.warning", [ParentalTimeWords(300, en)]), "Watching ends in 5 minutes.")
    ck_Eq("ar one", ParentalTimeWords(60, ar), "دقيقة واحدة")
    ck_Eq("ar two", ParentalTimeWords(120, ar), "دقيقتان")
    ck_Eq("ar few", ParentalTimeWords(300, ar), "5 دقائق")
    ck_Eq("ar many", ParentalTimeWords(720, ar), "12 دقيقة")
    ck_Eq("ar under a minute", ParentalTimeWords(30, ar), "أقل من دقيقة")
    ck_Eq("ar hours and minutes", ParentalTimeWords(3900, ar), "ساعة واحدة و5 دقائق")
    ck_Eq("ar two hours", ParentalTimeWords(7200, ar), "ساعتان")
    ck_Eq("ar note shaped once", ar.Format("parental.time.left", [ParentalTimeWords(720, ar)]), VisualLines("بقي 12 دقيقة اليوم", 40, 1, "rtl")[0])
    ck_Eq("ar warning shaped once", ar.Format("parental.time.warning", [ParentalTimeWords(60, ar)]), VisualLines("تنتهي المشاهدة بعد دقيقة واحدة.", 48, 1, "rtl")[0])
    ck_Result()
end sub
