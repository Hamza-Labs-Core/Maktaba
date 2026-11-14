' Account (story 58.12): who is signed in, to which home (and a plain-HTTP line when the home
' is reached without encryption on the LAN); Switch home, Sign out; the language.

sub init()
    ui_Init()
    c = ui_Colors()
    for each id in ["title", "who", "home", "plain", "version"]
        m[id] = m.top.FindNode(id)
    end for
    m.list = m.top.FindNode("list")
    ui_Label(m.title, 56, c.fg, true)
    ui_Label(m.who, 34, c.fg, false)
    ui_Label(m.home, 34, c.fg, false)
    ui_Label(m.plain, 30, c.muted, false)
    ui_Label(m.version, 26, c.muted, false)
    ui_List(m.list)
    ui_Mirror(["title", "who", "home", "plain", "list", "version"])
    m.title.text = ui_Tr("nav.account")
    m.list.ObserveField("itemSelected", "onSelected")
    m.top.ObserveField("params", "onParams")
    m.top.ObserveField("takeFocus", "onTakeFocus")
end sub

sub onParams()
    s = m.global.session
    user = s.user
    if user = invalid then user = ""
    m.who.text = m.i18n.Format("account.signedInAs", [ui_Vis(user, 30)])
    m.home.text = m.i18n.Format("account.home", [ui_Vis(s.name, 30)])
    if s.plain = true then m.plain.text = ui_Tr("launcher.plainHttp")
    m.version.text = m.i18n.Format("launcher.version", [AppVersion()])
    m.keys = ["switch", "signOut", "en", "ar"]
    en = ui_Tr("account.language") + ": " + ui_Tr("account.language.en")
    ar = ui_Tr("account.language") + ": " + ui_Tr("account.language.ar")
    if m.rtl then
        en = ui_Tr("account.language.en") + " :" + ui_Tr("account.language")
        ar = ui_Tr("account.language.ar") + " :" + ui_Tr("account.language")
    end if
    m.list.content = ui_ListContent([ui_Tr("account.switchHome"), ui_Tr("account.signOut"), en, ar])
    print "[screen] AccountScreen ready lang="; m.global.lang
end sub

sub onTakeFocus()
    m.list.SetFocus(true)
end sub

sub onSelected()
    k = m.keys[m.list.itemSelected]
    print "[account] "; k
    if k = "switch" then
        ui_Navigate({action: "start"})
    else if k = "signOut" then
        ui_Navigate({action: "signOut"})
    else if k <> m.global.lang then
        ui_Navigate({action: "setLang", lang: k})
    end if
end sub
