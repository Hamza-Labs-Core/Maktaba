' AI mode (Epic 49 story 49.14, on the API of story 49.12): what SearchScreen's AI mode tab asks
' the home (POST /api/search/ai) and reads from its Server-Sent Events, kept apart from the
' screen so brs-engine tests it (tests/ai_test.brs). The guide for clients is
' docs/developers/contributing/ai-mode.md.
'
'   p = AiParserNew()                  ' the stream's reader: Feed(chunk) gives the events whole
'   evs = p.Feed(chunk)                ' in each piece read ([{event, data}], data parsed JSON),
'   evs = p.Finish()                   ' split anywhere; Finish gives one left without its blank
'                                      ' line (the stream ended)
'   AiBody(q, turns)                   ' the request: {q} and, for a follow-up, history (the
'                                      ' last three turns)
'   turns = AiKeepTurn(turns, q, a)    ' a finished answer joins the conversation
'   st = AiNew(q)                      ' one answer's state; AiApply(st, ev) takes each event
'   AiAnswer(st)                       ' the answer's sentences, each with its markers ([1][2])
'   AiPlain(st)                        ' the same without markers: the turn sent as history
'   AiRows(st)                         ' the passages to list: those cited first, each with the
'                                      ' moment the player opens at
'   AiRefusal(status, data)            ' a refused question: unavailable, off, busy, old, failed
'   AiStatusKey(st)                    ' the catalog key of the line above the answer
'
' Roku reads the answer when its stream ends: roUrlTransfer gives a response body only whole,
' AsyncGetSSEvents (Roku OS 16) sends no request body, and roStreamSocket has no TLS for a home
' at an https name. The events are read the same way they would be as they arrive.

' The turns a follow-up sends (the API reads the last three).
function AiMaxTurns() as Integer
    return 3
end function

function AiParserNew() as Object
    return {buf: "", event: "", data: [], hasData: false, Feed: ai_Feed, Finish: ai_Finish}
end function

' The events complete in what has been read so far; a line cut in two waits for its end.
function ai_Feed(chunk as String) as Object
    m.buf = m.buf + chunk
    out = []
    lf = Chr(10)
    while true
        i = Instr(1, m.buf, lf)
        if i = 0 then exit while
        line = Left(m.buf, i - 1)
        m.buf = Mid(m.buf, i + 1)
        if Right(line, 1) = Chr(13) then line = Left(line, Len(line) - 1)
        ai_Line(m, line, out)
    end while
    return out
end function

' The stream ended: the last line, and an event that had no blank line after it.
function ai_Finish() as Object
    out = []
    if m.buf <> "" then
        line = m.buf
        m.buf = ""
        if Right(line, 1) = Chr(13) then line = Left(line, Len(line) - 1)
        ai_Line(m, line, out)
    end if
    ai_Line(m, "", out)
    return out
end function

' One line of the stream (the SSE rules): a blank line ends an event, a colon starts a comment
' (": ping"), "field: value" with one space after the colon left out.
sub ai_Line(p as Object, line as String, out as Object)
    if line = "" then
        if p.hasData then
            name = p.event
            if name = "" then name = "message"
            text = p.data.Join(Chr(10))
            data = invalid
            first = Left(text.Trim(), 1)
            if first = "{" or first = "[" then data = ParseJson(text)
            out.Push({event: name, data: data, raw: text})
        end if
        p.event = ""
        p.data = []
        p.hasData = false
        return
    end if
    if Left(line, 1) = ":" then return
    c = Instr(1, line, ":")
    if c = 0 then
        field = line
        value = ""
    else
        field = Left(line, c - 1)
        value = Mid(line, c + 1)
        if Left(value, 1) = " " then value = Mid(value, 2)
    end if
    if field = "event" then
        p.event = value
    else if field = "data" then
        p.data.Push(value)
        p.hasData = true
    end if
end sub

' Every event of a whole stream, as the screen reads it once the answer has ended.
function AiEvents(text as String) as Object
    p = AiParserNew()
    out = p.Feed(text)
    out.Append(p.Finish())
    return out
end function

function AiBody(q as String, turns as Dynamic) as Object
    body = {q: q}
    if type(turns) = "roArray" and turns.Count() > 0 then
        h = []
        first = turns.Count() - AiMaxTurns()
        if first < 0 then first = 0
        for i = first to turns.Count() - 1
            h.Push({q: turns[i].q, answer: turns[i].answer})
        end for
        body.history = h
    end if
    return body
end function

' The conversation with one more turn: only an answer with words in it, the last three kept.
function AiKeepTurn(turns as Dynamic, q as String, answer as String) as Object
    out = []
    if type(turns) = "roArray" then
        for each t in turns
            out.Push(t)
        end for
    end if
    if q.Trim() = "" or answer.Trim() = "" then return out
    out.Push({q: q, answer: answer})
    while out.Count() > AiMaxTurns()
        out.Shift()
    end while
    return out
end function

' One answer's state: phase is asking (sent, nothing read yet), waiting (in line: position),
' writing (the sources are in), done or stopped (an error event, or the stream cut short).
function AiNew(q as String) as Object
    return {q: q, phase: "asking", position: 0, sources: [], sentences: [], answered: false, reason: "", error: ""}
end function

function ai_Num(v as Dynamic) as Float
    t = type(v)
    if t = "roInt" or t = "roInteger" or t = "Integer" or t = "roFloat" or t = "Float" or t = "roDouble" or t = "Double" or t = "LongInteger" or t = "roLongInteger" then return v
    return 0
end function

function ai_Text(v as Dynamic) as String
    if type(v) = "roString" or type(v) = "String" then return v
    return ""
end function

' Takes one event into st (an unknown event, or one whose data is not an object, is left out).
sub AiApply(st as Object, ev as Object)
    d = ev.data
    if type(d) <> "roAssociativeArray" then return
    if ev.event = "queue" then
        st.position = Int(ai_Num(d.position))
        if st.phase = "asking" or st.phase = "waiting" then st.phase = "waiting"
    else if ev.event = "sources" then
        st.sources = []
        if type(d.passages) = "roArray" then
            for each p in d.passages
                if type(p) = "roAssociativeArray" and ai_Text(p.video_id) <> "" then
                    st.sources.Push({n: Int(ai_Num(p.n)), videoId: p.video_id, title: ai_Text(p.title), library: ai_Text(p.library_name), start: ai_Num(p.start_sec), text: ai_Text(p.text)})
                end if
            end for
        end if
        st.position = 0
        st.phase = "writing"
    else if ev.event = "text" then
        cites = []
        if type(d.citations) = "roArray" then
            for each c in d.citations
                if type(c) = "roAssociativeArray" and ai_Text(c.video_id) <> "" then
                    cites.Push({n: Int(ai_Num(c.n)), videoId: c.video_id, start: ai_Num(c.start_sec), quote: ai_Text(c.quote)})
                end if
            end for
        end if
        t = ai_Text(d.text).Trim()
        if t <> "" then st.sentences.Push({text: t, citations: cites})
        st.phase = "writing"
    else if ev.event = "done" then
        st.answered = (d.answered = true)
        st.reason = ai_Text(d.reason)
        st.phase = "done"
    else if ev.event = "error" then
        st.error = ai_Text(d.code)
        if st.error = "" then st.error = "failed"
        st.phase = "stopped"
    end if
end sub

' The stream ended: an answer with no done or error event was cut short.
sub AiEnd(st as Object)
    if st.phase <> "done" and st.phase <> "stopped" then
        st.phase = "stopped"
        st.error = "cut"
    end if
end sub

' The answer's sentences, each followed by its passages' numbers ("… patience. [1][3]").
function AiAnswer(st as Object) as String
    parts = []
    for each s in st.sentences
        marks = ""
        for each c in s.citations
            marks = marks + "[" + c.n.ToStr() + "]"
        end for
        if marks = "" then parts.Push(s.text) else parts.Push(s.text + " " + marks)
    end for
    return parts.Join(" ")
end function

' The answer as a turn of the conversation: its sentences, without the markers.
function AiPlain(st as Object) as String
    parts = []
    for each s in st.sentences
        parts.Push(s.text)
    end for
    return parts.Join(" ")
end function

' The passages to list under the answer: those the sentences cite first, in the order they are
' first cited, at the cited moment; then the others, in the home's order. Each row:
' {n, videoId, title, start, text, cited}.
function AiRows(st as Object) as Object
    byN = {}
    for each s in st.sources
        byN[s.n.ToStr()] = s
    end for
    out = []
    seen = {}
    for each sen in st.sentences
        for each c in sen.citations
            k = c.n.ToStr()
            if not seen.DoesExist(k) then
                seen[k] = true
                src = byN[k]
                title = ""
                text = c.quote
                if src <> invalid then
                    title = src.title
                    if text = "" then text = src.text
                end if
                out.Push({n: c.n, videoId: c.videoId, title: title, start: c.start, text: text, cited: true})
            end if
        end for
    end for
    for each s in st.sources
        k = s.n.ToStr()
        if not seen.DoesExist(k) then
            seen[k] = true
            out.Push({n: s.n, videoId: s.videoId, title: s.title, start: s.start, text: s.text, cited: false})
        end if
    end for
    return out
end function

' What a refused question means: "unavailable" (the home's language model is not set up, 409),
' "off" (an admin turned AI mode off, 409), "busy" (eight questions wait already, 429), "old"
' (a home before story 49.12: no such route), "failed" (no answer, or any other refusal).
function AiRefusal(status as Integer, data as Dynamic) as String
    code = ""
    if type(data) = "roAssociativeArray" then
        code = ai_Text(data.code)
        if code = "" then
            t = ai_Text(data.type)
            i = Len(t)
            while i > 0 and Mid(t, i, 1) <> "/"
                i = i - 1
            end while
            code = Mid(t, i + 1)
        end if
    end if
    if code = "ai-unavailable" then return "unavailable"
    if code = "ai-off" then return "off"
    if code = "ai-busy" or status = 429 then return "busy"
    if status = 404 or status = 405 then return "old"
    if status = 409 then return "unavailable"
    return "failed"
end function

' The catalog key of the line above the answer, or "" for none (an answer written in full).
function AiStatusKey(st as Object) as String
    if st.phase = "asking" then return "search.ai.asking"
    if st.phase = "waiting" then return "search.ai.queue"
    if st.phase = "writing" then return "search.ai.writing"
    if st.phase = "stopped" then return "search.ai.stopped"
    if st.reason = "no-passages" then return "search.ai.noPassages"
    ' a timeout says so whether or not a sentence came before it (answered:false with no
    ' sentence is no proof the passages do not answer)
    if st.reason = "timeout" then return "search.ai.timeout"
    if not st.answered then return "search.ai.noAnswer"
    return ""
end function

' A passage as the list under the answer shows it (visual): "[2]  3:12 · Title · its words"
' (the moment before the title, so an Arabic title does not carry it to its other side).
function AiRowText(i18n as Object, row as Object) as String
    words = row.text.Trim()
    if Len(words) > 80 then words = Left(words, 79) + "…"
    return i18n.Format("search.ai.source", ["[" + row.n.ToStr() + "]", FormatTime(row.start), row.title, words])
end function
