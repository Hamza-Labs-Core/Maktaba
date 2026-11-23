' Epic 49 story 49.14: AI mode's help on brs-engine (source/ai.brs): the Server-Sent Events
' reader (events split anywhere, comments, CRLF, an event left without its blank line), each
' event of POST /api/search/ai into the answer's state, the request bodies of a question and a
' follow-up, the refusals, and the lines the screen shows in English and Arabic.
sub Main()
    m.pass = 0
    m.fail = 0
    t_Parser()
    t_Split()
    t_Events()
    t_Ends()
    t_Bodies()
    t_Refusals()
    t_Lines()
    ck_Result()
end sub

function t_Lf() as String
    return Chr(10)
end function

' A whole answer, as the home writes it: a ping, the line, the sources, two sentences, done.
function t_Stream() as String
    lf = t_Lf()
    q = Chr(34)
    s = ": ping" + lf + lf
    s = s + "event: queue" + lf + "data: {" + q + "position" + q + ":1}" + lf + lf
    s = s + "event: sources" + lf + "data: {" + q + "q" + q + ":" + q + "kinds of patience" + q + "," + q + "passages" + q + ":["
    s = s + "{" + q + "n" + q + ":1," + q + "video_id" + q + ":" + q + "V1" + q + "," + q + "title" + q + ":" + q + "Patience" + q + "," + q + "library_name" + q + ":" + q + "Lessons" + q + "," + q + "start_sec" + q + ":75.5," + q + "end_sec" + q + ":80," + q + "text" + q + ":" + q + "Patience is of three kinds." + q + "},"
    s = s + "{" + q + "n" + q + ":2," + q + "video_id" + q + ":" + q + "V2" + q + "," + q + "title" + q + ":" + q + "Gratitude" + q + "," + q + "start_sec" + q + ":3725," + q + "text" + q + ":" + q + "Gratitude and patience." + q + "},"
    s = s + "{" + q + "n" + q + ":3," + q + "video_id" + q + ":" + q + "V3" + q + "," + q + "title" + q + ":" + q + "Hope" + q + "," + q + "start_sec" + q + ":10," + q + "text" + q + ":" + q + "Hope." + q + "}]}" + lf + lf
    s = s + "event: text" + lf + "data: {" + q + "text" + q + ":" + q + "Patience is of three kinds." + q + "," + q + "citations" + q + ":[{" + q + "n" + q + ":2," + q + "video_id" + q + ":" + q + "V2" + q + "," + q + "start_sec" + q + ":3725," + q + "quote" + q + ":" + q + "Gratitude and patience." + q + "}]}" + lf + lf
    s = s + "event: text" + lf + "data: {" + q + "text" + q + ":" + q + "It is held in hardship." + q + "," + q + "citations" + q + ":[{" + q + "n" + q + ":1," + q + "video_id" + q + ":" + q + "V1" + q + "," + q + "start_sec" + q + ":75.5," + q + "quote" + q + ":" + q + "Patience is of three kinds." + q + "},{" + q + "n" + q + ":2," + q + "video_id" + q + ":" + q + "V2" + q + "," + q + "start_sec" + q + ":3725," + q + "quote" + q + ":" + q + "" + q + "}]}" + lf + lf
    s = s + "event: done" + lf + "data: {" + q + "answered" + q + ":true," + q + "sentences" + q + ":2," + q + "dropped" + q + ":1," + q + "took_ms" + q + ":900}" + lf + lf
    return s
end function

function t_Names(evs as Object) as String
    out = []
    for each e in evs
        out.Push(e.event)
    end for
    return out.Join(",")
end function

sub t_Parser()
    evs = AiEvents(t_Stream())
    ck_Eq("every event, the ping left out", t_Names(evs), "queue,sources,text,text,done")
    ck_Eq("queue data", evs[0].data.position, 1)
    ck_Eq("sources data", evs[1].data.passages.Count(), 3)
    ck_Eq("text data", evs[2].data.text, "Patience is of three kinds.")

    lf = t_Lf()
    cr = Chr(13)
    ' CRLF lines, a field with no space after its colon, data on two lines, an unknown field
    evs = AiEvents("event:text" + cr + lf + "id: 7" + cr + lf + "data: {" + Chr(34) + "text" + Chr(34) + ":" + cr + lf + "data: " + Chr(34) + "a" + Chr(34) + "}" + cr + lf + cr + lf)
    ck_Eq("CRLF event", t_Names(evs), "text")
    ck_Eq("two data lines joined by a line break", evs[0].data.text, "a")
    ' no event name: message; a data line that is not JSON keeps its text
    evs = AiEvents("data: hello" + lf + lf)
    ck_Eq("default event name", evs[0].event, "message")
    ck_Eq("not JSON", evs[0].data, invalid)
    ck_Eq("its text", evs[0].raw, "hello")
    ' a blank line with no data dispatches nothing; comments only: nothing
    ck_Eq("event without data", AiEvents("event: queue" + lf + lf).Count(), 0)
    ck_Eq("comments only", AiEvents(": ping" + lf + lf + ": ping" + lf + lf).Count(), 0)
    ' the last event of a stream cut before its blank line, and its last line without its end
    evs = AiEvents("event: done" + lf + "data: {" + Chr(34) + "answered" + Chr(34) + ":false}")
    ck_Eq("an event at the end without its blank line", t_Names(evs), "done")
    ck_Eq("its data", evs[0].data.answered, false)
end sub

' The same events however the stream is cut: one character at a time, at every split point.
sub t_Split()
    s = t_Stream()
    whole = FormatJson(AiEvents(s))
    p = AiParserNew()
    evs = []
    for i = 1 to Len(s)
        evs.Append(p.Feed(Mid(s, i, 1)))
    end for
    evs.Append(p.Finish())
    ck_Eq("one character at a time", FormatJson(evs), whole)
    bad = 0
    for cut = 1 to Len(s) - 1 step 7
        p = AiParserNew()
        evs = p.Feed(Left(s, cut))
        evs.Append(p.Feed(Mid(s, cut + 1)))
        evs.Append(p.Finish())
        if FormatJson(evs) <> whole then bad = bad + 1
    end for
    ck_Eq("cut in two anywhere", bad, 0)
    ' a piece holds nothing until its event ends
    p = AiParserNew()
    ck_Eq("half an event", p.Feed("event: queue" + t_Lf() + "data: {").Count(), 0)
    ck_Eq("its end", t_Names(p.Feed(Chr(34) + "position" + Chr(34) + ":2}" + t_Lf() + t_Lf())), "queue")
end sub

sub t_Events()
    st = AiNew("kinds of patience")
    ck_Eq("asking", st.phase, "asking")
    ck_Eq("asking line", AiStatusKey(st), "search.ai.asking")
    evs = AiEvents(t_Stream())
    AiApply(st, evs[0])
    ck_Eq("in line", st.phase, "waiting")
    ck_Eq("its place", st.position, 1)
    ck_Eq("queue line", AiStatusKey(st), "search.ai.queue")
    AiApply(st, evs[1])
    ck_Eq("sources first: writing", st.phase, "writing")
    ck_Eq("three passages", st.sources.Count(), 3)
    ck_Eq("a passage", st.sources[0], {n: 1, videoId: "V1", title: "Patience", library: "Lessons", start: 75.5, text: "Patience is of three kinds."})
    ck_Eq("writing line", AiStatusKey(st), "search.ai.writing")
    ' the sources alone are listed, in the home's order, none cited yet
    rows = AiRows(st)
    ck_Eq("rows before the answer", [rows[0].n, rows[1].n, rows[2].n, rows[0].cited], [1, 2, 3, false])
    AiApply(st, evs[2])
    AiApply(st, evs[3])
    ck_Eq("two sentences", st.sentences.Count(), 2)
    ck_Eq("citations", st.sentences[1].citations.Count(), 2)
    ck_Eq("answer with markers", AiAnswer(st), "Patience is of three kinds. [2] It is held in hardship. [1][2]")
    ck_Eq("plain answer", AiPlain(st), "Patience is of three kinds. It is held in hardship.")
    ' cited passages first, in the order first cited, at the cited moment; then the others
    rows = AiRows(st)
    ck_Eq("rows' order", [rows[0].n, rows[1].n, rows[2].n], [2, 1, 3])
    ck_Eq("cited", [rows[0].cited, rows[1].cited, rows[2].cited], [true, true, false])
    ck_Eq("cited moment", rows[0].start, 3725)
    ck_Eq("cited video", rows[1].videoId, "V1")
    ck_Eq("row title from its passage", rows[0].title, "Gratitude")
    ck_Eq("the quote", rows[0].text, "Gratitude and patience.")
    AiApply(st, evs[4])
    ck_Eq("done", st.phase, "done")
    ck_True("answered", st.answered)
    ck_Eq("an answer written in full has no line", AiStatusKey(st), "")
    ' events the client does not know, or with no object, change nothing
    AiApply(st, {event: "message", data: {x: 1}})
    AiApply(st, {event: "text", data: invalid})
    ck_Eq("unknown events left out", st.sentences.Count(), 2)
    ' a sentence of spaces is not shown; a citation with no video is dropped
    AiApply(st, {event: "text", data: {text: "  ", citations: []}})
    ck_Eq("an empty sentence", st.sentences.Count(), 2)
    AiApply(st, {event: "text", data: {text: "x.", citations: [{n: 9}]}})
    ck_Eq("a citation without its video", st.sentences[2].citations.Count(), 0)
end sub

sub t_Ends()
    ' not in the passages: the sources stay
    st = AiNew("q")
    AiApply(st, {event: "sources", data: {passages: [{n: 1, video_id: "V1", start_sec: 1}]}})
    AiApply(st, {event: "done", data: {answered: false, reason: "not-in-passages"}})
    ck_Eq("no answer line", AiStatusKey(st), "search.ai.noAnswer")
    ck_Eq("sources kept", AiRows(st).Count(), 1)
    st = AiNew("q")
    AiApply(st, {event: "sources", data: {passages: []}})
    AiApply(st, {event: "done", data: {answered: false, reason: "no-passages"}})
    ck_Eq("no passages", AiStatusKey(st), "search.ai.noPassages")
    st = AiNew("q")
    AiApply(st, {event: "text", data: {text: "One.", citations: [{n: 1, video_id: "V1", start_sec: 2}]}})
    AiApply(st, {event: "done", data: {answered: true, reason: "timeout"}})
    ck_Eq("timeout, the sentences standing", AiStatusKey(st), "search.ai.timeout")
    ' an error: the answer stopped, the sentences sent stand
    st = AiNew("q")
    AiApply(st, {event: "text", data: {text: "One.", citations: [{n: 1, video_id: "V1", start_sec: 2}]}})
    AiApply(st, {event: "error", data: {code: "model-failed"}})
    ck_Eq("stopped", [st.phase, st.error, AiStatusKey(st)], ["stopped", "model-failed", "search.ai.stopped"])
    ck_Eq("its sentence stands", AiAnswer(st), "One. [1]")
    AiEnd(st)
    ck_Eq("an error stays itself at the end", st.error, "model-failed")
    ' a stream that ended with no done: cut short
    st = AiNew("q")
    AiApply(st, {event: "sources", data: {passages: []}})
    AiEnd(st)
    ck_Eq("cut", [st.phase, st.error], ["stopped", "cut"])
    st = AiNew("q")
    AiApply(st, {event: "done", data: {answered: true}})
    AiEnd(st)
    ck_Eq("done stays done", st.phase, "done")
end sub

sub t_Bodies()
    ck_Eq("a question", AiBody("What is patience?", []), {q: "What is patience?"})
    ck_Eq("no turns", AiBody("q", invalid), {q: "q"})
    turns = AiKeepTurn([], "What is patience?", "Holding back.")
    b = AiBody("and its kinds?", turns)
    ck_Eq("a follow-up", b, {q: "and its kinds?", history: [{q: "What is patience?", answer: "Holding back."}]})
    ck_Eq("FormatJson of the body", ParseJson(FormatJson(b)).history[0].answer, "Holding back.")
    ' no answer: not a turn
    ck_Eq("an empty answer is no turn", AiKeepTurn(turns, "q2", " ").Count(), 1)
    ' the last three turns, oldest first
    for i = 2 to 5
        turns = AiKeepTurn(turns, "q" + i.ToStr(), "a" + i.ToStr())
    end for
    ck_Eq("three kept", turns.Count(), 3)
    b = AiBody("next", turns)
    ck_Eq("the last three, oldest first", [b.history[0].q, b.history[1].q, b.history[2].q], ["q3", "q4", "q5"])
    ' a list longer than three (kept by hand) still sends the last three
    long = [{q: "1", answer: "a"}, {q: "2", answer: "b"}, {q: "3", answer: "c"}, {q: "4", answer: "d"}]
    ck_Eq("the body's own limit", AiBody("x", long).history.Count(), 3)
    ck_Eq("from the fourth back", AiBody("x", long).history[0].q, "2")
end sub

sub t_Refusals()
    ck_Eq("no model", AiRefusal(409, {type: "ai-unavailable", code: "ai-unavailable", help: {settings: "/settings"}}), "unavailable")
    ck_Eq("off", AiRefusal(409, {code: "ai-off"}), "off")
    ck_Eq("busy", AiRefusal(429, {code: "ai-busy", position: 9}), "busy")
    ck_Eq("busy by status", AiRefusal(429, invalid), "busy")
    ck_Eq("type as a URL", AiRefusal(409, {type: "https://maktaba.dev/problems/ai-off"}), "off")
    ck_Eq("a home before 49.12", AiRefusal(404, {type: "not-found"}), "old")
    ck_Eq("no such method", AiRefusal(405, invalid), "old")
    ck_Eq("409 with no code", AiRefusal(409, invalid), "unavailable")
    ck_Eq("no answer", AiRefusal(0, invalid), "failed")
    ck_Eq("a server error", AiRefusal(500, {type: "internal"}), "failed")
end sub

sub t_Lines()
    en = I18nNew("en")
    row = {n: 2, videoId: "V2", title: "Gratitude", start: 3725, text: "Gratitude and patience.", cited: true}
    ck_Eq("a passage's line", AiRowText(en, row), "[2]  1:02:05 · Gratitude · Gratitude and patience.")
    long = {n: 1, videoId: "V1", title: "T", start: 5, text: String(100, "a"), cited: false}
    ck_Eq("long words cut", Len(AiRowText(en, long)), Len("[1]  0:05 · T · ") + 80)
    ck_Eq("queue line", en.Format("search.ai.queue", [2]), "Other questions are ahead of this one: it is number 2 in line.")
    for each k in ["search.tab.results", "search.tab.ai", "search.ai.ask", "search.ai.newTopic", "search.ai.sources", "search.ai.hint", "search.ai.followUp", "search.ai.asking", "search.ai.writing", "search.ai.noAnswer", "search.ai.noPassages", "search.ai.timeout", "search.ai.stopped", "search.ai.unavailable", "search.ai.off", "search.ai.busy", "search.ai.old", "search.ai.failed"]
        ck_True("en " + k, en.Tr(k) <> k and en.Tr(k) <> "")
    end for
    ar = I18nNew("ar")
    for each k in ["search.tab.results", "search.tab.ai", "search.ai.ask", "search.ai.newTopic", "search.ai.sources", "search.ai.hint", "search.ai.followUp", "search.ai.asking", "search.ai.writing", "search.ai.noAnswer", "search.ai.noPassages", "search.ai.timeout", "search.ai.stopped", "search.ai.unavailable", "search.ai.off", "search.ai.busy", "search.ai.old", "search.ai.failed"]
        s = ar.Tr(k)
        ck_True("ar " + k, s <> k and s <> en.Tr(k))
    end for
    ' Arabic is shaped (presentation forms) and drawn right to left: the passage's number,
    ' which starts the line, is at its right end
    arRow = {n: 1, videoId: "V1", title: "الصبر", start: 75, text: "الصبر ثلاثة أنواع", cited: true}
    line = AiRowText(ar, arRow)
    ck_True("Arabic line shaped", Instr(1, line, "الصبر") = 0)
    ck_Eq("its number at the right", Right(line, 3), "[1]")
    ck_True("its time kept left to right", Instr(1, line, "1:15") > 0)
    ck_True("the tab shaped", Instr(1, ar.Tr("search.tab.ai"), "الذكاء") = 0)
end sub
