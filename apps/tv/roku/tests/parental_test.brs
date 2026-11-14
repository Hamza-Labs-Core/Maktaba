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
    ck_Result()
end sub
