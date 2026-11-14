' A series (story 58.12): Play and Shuffle (the server queue), the season pills, the episodes.
' Focus moves buttons → seasons → episodes; Back goes to the episodes' top, then out.

sub init()
    ui_Init()
    c = ui_Colors()
    m.backdrop = m.top.FindNode("backdrop")
    m.title = m.top.FindNode("title")
    m.desc = m.top.FindNode("desc")
    m.play = m.top.FindNode("play")
    m.shuffle = m.top.FindNode("shuffle")
    m.seasons = m.top.FindNode("seasons")
    m.episodes = m.top.FindNode("episodes")
    ui_Label(m.title, 56, c.fg, true)
    ui_Label(m.desc, 30, c.muted, false)
    m.play.text = ui_Tr("action.play")
    m.shuffle.text = ui_Tr("action.shuffle")
    m.episodes.drawFocusFeedback = false
    ui_Mirror(["title", "desc", "play", "shuffle", "episodes"])
    m.zone = "buttons"
    m.button = 0
    m.data = []
    m.seasons.ObserveField("selected", "onSeason")
    m.episodes.ObserveField("itemSelected", "onEpisode")
    m.episodes.ObserveField("itemFocused", "onEpisodeFocus")
    m.top.ObserveField("params", "onParams")
    m.top.ObserveField("takeFocus", "onTakeFocus")
end sub

sub onParams()
    id = m.top.params.id
    ui_Api("GET", "/api/series/" + id, invalid, "series", "onSeries")
    ui_Api("GET", "/api/series/" + id + "/episodes" + NetQuery({sort: "episode"}), invalid, "episodes", "onEpisodes")
end sub

sub onSeries(ev as Object)
    r = ev.GetData()
    if r.status <> 200 or r.data = invalid then return
    m.title.text = ui_Vis(r.data.name, 50)
    if r.data.description <> invalid then m.desc.text = VisualLines(r.data.description, 70, 2, "auto").Join(Chr(10))
end sub

sub onEpisodes(ev as Object)
    r = ev.GetData()
    m.data = []
    if r.status = 200 and r.data <> invalid and type(r.data.seasons) = "roArray" then m.data = r.data.seasons
    names = []
    for each s in m.data
        names.Push(m.i18n.Format("series.season", [s.season]))
    end for
    m.seasons.items = names
    if m.data.Count() > 0 and type(m.data[0].episodes) = "roArray" and m.data[0].episodes.Count() > 0 then
        m.backdrop.uri = ui_Poster(m.data[0].episodes[0].video_id)
    end if
    showSeason()
    print "[screen] SeriesScreen ready seasons="; m.data.Count()
end sub

sub onSeason()
    showSeason()
end sub

sub showSeason()
    root = CreateObject("roSGNode", "ContentNode")
    i = m.seasons.selected
    if i >= 0 and i < m.data.Count() then
        list = []
        for each e in m.data[i].episodes
            n = ""
            if e.episode <> invalid then n = e.episode.ToStr() + ". "
            ep = {video_id: e.video_id, title: n + CardTitle(e), duration_sec: e.duration_sec, series_id: m.top.params.id}
            if e.progress_pct <> invalid and e.duration_sec <> invalid and e.watched <> true then ep.position_sec = e.duration_sec * e.progress_pct / 100
            list.Push(ep)
        end for
        ui_GridItems(root, list, "video", 4, true)
    end if
    m.episodes.content = root
    m.episodes.jumpToItem = m.layout.StartIndex(m_Min(4, root.GetChildCount()))
    print "[series] season "; i; " episodes "; root.GetChildCount()
end sub

function m_Min(a as Integer, b as Integer) as Integer
    if a < b then return a
    return b
end function

sub onEpisode()
    it = ui_GridCard(m.episodes)
    if it <> invalid and it.kind <> "blank" then ui_CardAction(it, "open")
end sub

sub onEpisodeFocus()
    print "[focus] series episode "; m.episodes.itemFocused
end sub

sub onTakeFocus()
    focusZone()
end sub

sub focusZone()
    m.play.focused = (m.zone = "buttons" and m.button = 0)
    m.shuffle.focused = (m.zone = "buttons" and m.button = 1)
    m.seasons.focused = (m.zone = "seasons")
    if m.zone = "seasons" then
        m.seasons.SetFocus(true)
    else if m.zone = "episodes" then
        m.episodes.SetFocus(true)
    else if m.button = 0 then
        m.play.SetFocus(true)
    else
        m.shuffle.SetFocus(true)
    end if
    print "[focus] series "; m.zone
end sub

function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    k = m.layout.Key(key)
    if key = "back" then
        onTop = (m.zone <> "episodes" or m.episodes.itemFocused < 4)
        go = NavBack("series", false, onTop)
        print "[back] series "; go
        if go = "top" then
            m.episodes.jumpToItem = m.layout.StartIndex(m_Min(4, m.episodes.content.GetChildCount()))
            return true
        end if
        return false
    end if
    if m.zone = "buttons" then
        if k = "right" then
            m.button = 1
        else if k = "left" then
            m.button = 0
        else if key = "down" then
            m.zone = "seasons"
        else if key = "OK" then
            ui_PlayQueue({kind: "series", id: m.top.params.id}, (m.button = 1))
            return true
        else
            return true
        end if
        focusZone()
        return true
    end if
    if m.zone = "seasons" then
        if key = "up" then
            m.zone = "buttons"
        else if key = "down" then
            m.zone = "episodes"
        else
            return false
        end if
        focusZone()
        return true
    end if
    if key = "up" then
        m.zone = "seasons"
        focusZone()
        return true
    end if
    action = NavCardKey(key)
    card = ui_GridCard(m.episodes)
    if card <> invalid and card.kind <> "blank" then
        if action = "play" then
            ui_CardAction(card, "play")
            return true
        else if action = "actions" then
            ui_ShowActions(card, m.episodes)
            return true
        end if
    end if
    return false
end function
