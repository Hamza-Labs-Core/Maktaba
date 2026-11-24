; Maktaba's -setup.exe (Epic 13 story 13.14, decision D3): tauri's NSIS template with
; Maktaba's two pages in place of its stock ones. bundle.windows.nsis.template points here.
;
; Taken from tauri-bundler 2.10.3 (the one @tauri-apps/cli 2.12.3 runs; its installer.nsi's
; SHA-256 8ae02c66f8adb4a0d15d97f984fbacd2b44262d8a4d03fefaf6177f67f7008e5). Everything that
; installs, updates, uninstalls and reads the command line (/S, /P, /UPDATE, /NS, /R, /ARGS)
; is tauri's, unchanged; tools/ci/tests/test_installers.py keeps its sections and functions
; here and fails when the CLI moves to another bundler, so a bump re-takes the template.
; Maktaba's parts are marked "Maktaba (13.14)":
;   - the pages: welcome (the brand pane, the title, where it installs, the Start menu and
;     desktop shortcuts) and installing (the brand pane, the progress, the steps, "Open
;     Maktaba when it's done"); the reinstall page (only over an earlier install) and the
;     uninstaller's pages stay MUI's, with the header in the chrome colour. No finish page:
;     the window closes itself when the install is done and opens Maktaba when asked.
;   - the art: installer/*.bmp, drawn by docs/design/desktop/installer-art.mjs (the brand
;     pane and the titles at 100, 150 and 200%, picked by the window's DPI); the hooks file
;     (installer-ui.nsh, or installer-hooks.nsh which includes it) says where it is.
;   - the colours: the desktop tokens (web/design-system/tokens: desktop.window, pane, chrome,
;     subtle, line; semantic.fg, muted, success; brand.500), the rest in Segoe UI.
;   - English and Arabic (bundle.windows.nsis.languages), Arabic right to left.

Unicode true
ManifestDPIAware true
; Add in `dpiAwareness` `PerMonitorV2` to manifest for Windows 10 1607+ (note this should not affect lower versions since they should be able to ignore this and pick up `dpiAware` `true` set by `ManifestDPIAware true`)
; Currently undocumented on NSIS's website but is in the Docs folder of source tree, see
; https://github.com/kichik/nsis/blob/5fc0b87b819a9eec006df4967d08e522ddd651c9/Docs/src/attributes.but#L286-L300
; https://github.com/tauri-apps/tauri/pull/10106
ManifestDPIAwareness PerMonitorV2

!if "{{compression}}" == "none"
  SetCompress off
!else
  ; Set the compression algorithm. We default to LZMA.
  SetCompressor /SOLID "{{compression}}"
!endif

; Keep above !include to stay ahead of any plugin command
; see https://github.com/tauri-apps/tauri/pull/15422#discussion_r3289239624
{{#if signed_plugins_path}}
!addplugindir "{{signed_plugins_path}}"
{{/if}}

!include MUI2.nsh
!include FileFunc.nsh
!include x64.nsh
!include WordFunc.nsh
!include "utils.nsh"
!include "FileAssociation.nsh"
!include "Win\COM.nsh"
!include "Win\Propkey.nsh"
!include "Win\RestartManager.nsh"
!include "StrFunc.nsh"
${StrCase}
${StrLoc}

{{#if installer_hooks}}
!include "{{installer_hooks}}"
{{/if}}

!define WEBVIEW2APPGUID "{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"

!define MANUFACTURER "{{manufacturer}}"
!define PRODUCTNAME "{{product_name}}"
!define VERSION "{{version}}"
!define VERSIONWITHBUILD "{{version_with_build}}"
!define HOMEPAGE "{{homepage}}"
!define INSTALLMODE "{{install_mode}}"
!define LICENSE "{{license}}"
!define INSTALLERICON "{{installer_icon}}"
!define SIDEBARIMAGE "{{sidebar_image}}"
!define HEADERIMAGE "{{header_image}}"
!define UNINSTALLERICON "{{uninstaller_icon}}"
!define UNINSTALLERHEADERIMAGE "{{uninstaller_header_image}}"
!define MAINBINARYNAME "{{main_binary_name}}"
!define MAINBINARYSRCPATH "{{main_binary_path}}"
!define BUNDLEID "{{bundle_id}}"
!define COPYRIGHT "{{copyright}}"
!define OUTFILE "{{out_file}}"
!define ARCH "{{arch}}"
!define ADDITIONALPLUGINSPATH "{{additional_plugins_path}}"
!define ALLOWDOWNGRADES "{{allow_downgrades}}"
!define DISPLAYLANGUAGESELECTOR "{{display_language_selector}}"
!define INSTALLWEBVIEW2MODE "{{install_webview2_mode}}"
!define WEBVIEW2INSTALLERARGS "{{webview2_installer_args}}"
!define WEBVIEW2BOOTSTRAPPERPATH "{{webview2_bootstrapper_path}}"
!define WEBVIEW2INSTALLERPATH "{{webview2_installer_path}}"
!define MINIMUMWEBVIEW2VERSION "{{minimum_webview2_version}}"
!define UNINSTKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCTNAME}"
!define MANUKEY "Software\${MANUFACTURER}"
!define MANUPRODUCTKEY "${MANUKEY}\${PRODUCTNAME}"
!define UNINSTALLERSIGNCOMMAND "{{uninstaller_sign_cmd}}"
!define ESTIMATEDSIZE "{{estimated_size}}"
!define STARTMENUFOLDER "{{start_menu_folder}}"

Var PassiveMode
Var UpdateMode
Var NoShortcutMode
Var WixMode
Var OldMainBinaryName

; ---- Maktaba (13.14): the colours, the art, the pages' state -------------------------------
; the desktop tokens, as RRGGBB (SetCtlColors, MUI) and as COLORREF 0x00BBGGRR (messages)
!define MK_WINDOW "0B111C"     ; desktop.window
!define MK_PANE "0F1725"       ; desktop.pane
!define MK_CHROME "0E1624"     ; desktop.chrome
!define MK_SUBTLE "131D2E"     ; desktop.subtle
!define MK_FG "E8ECF3"         ; semantic.fg
!define MK_MUTED "A9B3C4"      ; semantic.muted
!define MK_BRASS "E3B252"      ; brand.500
!define MK_OK "4CC38A"         ; semantic.success
!define MK_CONTROL "6B7A94"    ; desktop.control
!define MK_BRASS_REF 0x0052B2E3
!define MK_SUBTLE_REF 0x002E1D13
!ifndef MAKTABA_ART
  !error "MAKTABA_ART is not defined: bundle.windows.nsis.installerHooks must be installer-ui.nsh or installer-hooks.nsh"
!endif
; the MUI pages kept (reinstall, the uninstaller's) get the chrome colour in their header
!define MUI_BGCOLOR "${MK_CHROME}"
!define MUI_TEXTCOLOR "${MK_FG}"
; the server build carries the hooks (installer-hooks.nsh) and the payload (server\ in the
; install folder)
!ifmacrodef NSIS_HOOK_PREINSTALL
  !define MK_SERVER
!endif
Var MkStartMenu    ; 1: a Start menu shortcut (the default, and in silent installs)
Var MkDesktop      ; 1: a desktop shortcut, from the welcome page
Var MkOpenAfter    ; 1: open Maktaba when the install is done
Var MkScale        ; the art's scale for this window's DPI: 100, 150 or 200
Var MkDpi
Var MkFont
Var MkFontLead
Var MkFontGlyph
Var MkPage         ; the page's dialog
Var MkBrand        ; the brand pane's bitmap control, and its image
Var MkBrandImage
Var MkTitle
Var MkTitleImage
Var MkDir          ; the welcome page's folder field
Var MkStartMenuBox ; the welcome page's two check boxes
Var MkDesktopBox
Var MkStep1        ; the installing page's steps: a glyph and a label each
Var MkStep1Text
!ifdef MK_SERVER
  Var MkStep2
  Var MkStep2Text
!endif
Var MkStep3
Var MkStep3Text
Var MkNow          ; what is being done now
Var MkOpen         ; the "open when done" check box
Var MkReinstallShown ; 1: the reinstall page came before the welcome page

Name "${PRODUCTNAME}"
BrandingText "${COPYRIGHT}"
OutFile "${OUTFILE}"

; We don't actually use this value as default install path,
; it's just for nsis to append the product name folder in the directory selector
; https://nsis.sourceforge.io/Reference/InstallDir
!define PLACEHOLDER_INSTALL_DIR "placeholder\${PRODUCTNAME}"
InstallDir "${PLACEHOLDER_INSTALL_DIR}"

VIProductVersion "${VERSIONWITHBUILD}"
VIAddVersionKey "ProductName" "${PRODUCTNAME}"
VIAddVersionKey "FileDescription" "${PRODUCTNAME}"
VIAddVersionKey "LegalCopyright" "${COPYRIGHT}"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"

# additional plugins
!addplugindir "${ADDITIONALPLUGINSPATH}"

; Uninstaller signing command
!if "${UNINSTALLERSIGNCOMMAND}" != ""
  !uninstfinalize '${UNINSTALLERSIGNCOMMAND}'
!endif

; Handle install mode, `perUser`, `perMachine` or `both`
!if "${INSTALLMODE}" == "perMachine"
  RequestExecutionLevel admin
!endif

!if "${INSTALLMODE}" == "currentUser"
  RequestExecutionLevel user
!endif

!if "${INSTALLMODE}" == "both"
  !define MULTIUSER_MUI
  !define MULTIUSER_INSTALLMODE_INSTDIR "${PRODUCTNAME}"
  !define MULTIUSER_INSTALLMODE_COMMANDLINE
  !if "${ARCH}" == "x64"
    !define MULTIUSER_USE_PROGRAMFILES64
  !else if "${ARCH}" == "arm64"
    !define MULTIUSER_USE_PROGRAMFILES64
  !endif
  !define MULTIUSER_INSTALLMODE_DEFAULT_REGISTRY_KEY "${UNINSTKEY}"
  !define MULTIUSER_INSTALLMODE_DEFAULT_REGISTRY_VALUENAME "CurrentUser"
  !define MULTIUSER_INSTALLMODEPAGE_SHOWUSERNAME
  !define MULTIUSER_INSTALLMODE_FUNCTION RestorePreviousInstallLocation
  !define MULTIUSER_EXECUTIONLEVEL Highest
  !include MultiUser.nsh
!endif

; Installer icon
!if "${INSTALLERICON}" != ""
  !define MUI_ICON "${INSTALLERICON}"
!endif

; Installer sidebar image
!if "${SIDEBARIMAGE}" != ""
  !define MUI_WELCOMEFINISHPAGE_BITMAP "${SIDEBARIMAGE}"
!endif

; Enable header images for installer and uninstaller pages when either image is configured.
!if "${HEADERIMAGE}" != ""
  !define MUI_HEADERIMAGE
!else if "${UNINSTALLERHEADERIMAGE}" != ""
  !define MUI_HEADERIMAGE
!endif

; Installer header image
!if "${HEADERIMAGE}" != ""
  !define MUI_HEADERIMAGE_BITMAP "${HEADERIMAGE}"
!endif

; Uninstaller header image
!if "${UNINSTALLERHEADERIMAGE}" != ""
  !define MUI_HEADERIMAGE_UNBITMAP "${UNINSTALLERHEADERIMAGE}"
!endif

; Uninstaller icon
!if "${UNINSTALLERICON}" != ""
  !define MUI_UNICON "${UNINSTALLERICON}"
!endif

; Define registry key to store installer language
!define MUI_LANGDLL_REGISTRY_ROOT "HKCU"
!define MUI_LANGDLL_REGISTRY_KEY "${MANUPRODUCTKEY}"
!define MUI_LANGDLL_REGISTRY_VALUENAME "Installer Language"

; Installer pages, must be ordered as they appear
; 1. Welcome Page
; Maktaba (13.14): Maktaba's welcome page stands where tauri's directory page did (5, below):
; it holds the folder, so it comes after the reinstall page (an earlier install's uninstall)
; and the install mode page, as the folder did

; 2. License Page (if defined)
!if "${LICENSE}" != ""
  !define MUI_PAGE_CUSTOMFUNCTION_PRE SkipIfPassive
  !insertmacro MUI_PAGE_LICENSE "${LICENSE}"
!endif

; 3. Install mode (if it is set to `both`)
!if "${INSTALLMODE}" == "both"
  !define MUI_PAGE_CUSTOMFUNCTION_PRE SkipIfPassive
  !insertmacro MULTIUSER_PAGE_INSTALLMODE
!endif

; 4. Custom page to ask user if he wants to reinstall/uninstall
;    only if a previous installation was detected
Var ReinstallPageCheck
Page custom PageReinstall PageLeaveReinstall
Function PageReinstall
  ; Maktaba (13.14): back from the welcome page, MUI's header again
  Call MaktabaNormalWindow

  ; Uninstall previous WiX installation if exists.
  ;
  ; A WiX installer stores the installation info in registry
  ; using a UUID and so we have to loop through all keys under
  ; `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`
  ; and check if `DisplayName` and `Publisher` keys match ${PRODUCTNAME} and ${MANUFACTURER}
  ;
  ; This has a potential issue that there maybe another installation that matches
  ; our ${PRODUCTNAME} and ${MANUFACTURER} but wasn't installed by our WiX installer,
  ; however, this should be fine since the user will have to confirm the uninstallation
  ; and they can chose to abort it if doesn't make sense.
  StrCpy $0 0
  wix_loop:
    EnumRegKey $1 HKLM "SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall" $0
    StrCmp $1 "" wix_loop_done ; Exit loop if there is no more keys to loop on
    IntOp $0 $0 + 1
    ReadRegStr $R0 HKLM "SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\$1" "DisplayName"
    ReadRegStr $R1 HKLM "SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\$1" "Publisher"
    StrCmp "$R0$R1" "${PRODUCTNAME}${MANUFACTURER}" 0 wix_loop
    ReadRegStr $R0 HKLM "SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\$1" "UninstallString"
    ${StrCase} $R1 $R0 "L"
    ${StrLoc} $R0 $R1 "msiexec" ">"
    StrCmp $R0 0 0 wix_loop_done
    StrCpy $WixMode 1
    StrCpy $R6 "SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\$1"
    Goto compare_version
  wix_loop_done:

  ; Check if there is an existing installation, if not, abort the reinstall page
  ReadRegStr $R0 SHCTX "${UNINSTKEY}" ""
  ReadRegStr $R1 SHCTX "${UNINSTKEY}" "UninstallString"
  ${IfThen} "$R0$R1" == "" ${|} Abort ${|}

  ; Compare this installar version with the existing installation
  ; and modify the messages presented to the user accordingly
  compare_version:
  ; Maktaba (13.14): this page is shown, so the welcome page can go back to it
  StrCpy $MkReinstallShown 1
  StrCpy $R4 "$(older)"
  ${If} $WixMode = 1
    ReadRegStr $R0 HKLM "$R6" "DisplayVersion"
  ${Else}
    ReadRegStr $R0 SHCTX "${UNINSTKEY}" "DisplayVersion"
  ${EndIf}
  ${IfThen} $R0 == "" ${|} StrCpy $R4 "$(unknown)" ${|}

  nsis_tauri_utils::SemverCompare "${VERSION}" $R0
  Pop $R0
  ; Reinstalling the same version
  ${If} $R0 = 0
    StrCpy $R1 "$(alreadyInstalledLong)"
    StrCpy $R2 "$(addOrReinstall)"
    StrCpy $R3 "$(uninstallApp)"
    !insertmacro MUI_HEADER_TEXT "$(alreadyInstalled)" "$(chooseMaintenanceOption)"
  ; Upgrading
  ${ElseIf} $R0 = 1
    StrCpy $R1 "$(olderOrUnknownVersionInstalled)"
    StrCpy $R2 "$(uninstallBeforeInstalling)"
    StrCpy $R3 "$(dontUninstall)"
    !insertmacro MUI_HEADER_TEXT "$(alreadyInstalled)" "$(choowHowToInstall)"
  ; Downgrading
  ${ElseIf} $R0 = -1
    StrCpy $R1 "$(newerVersionInstalled)"
    StrCpy $R2 "$(uninstallBeforeInstalling)"
    !if "${ALLOWDOWNGRADES}" == "true"
      StrCpy $R3 "$(dontUninstall)"
    !else
      StrCpy $R3 "$(dontUninstallDowngrade)"
    !endif
    !insertmacro MUI_HEADER_TEXT "$(alreadyInstalled)" "$(choowHowToInstall)"
  ${Else}
    Abort
  ${EndIf}

  ; Skip showing the page if passive
  ;
  ; Note that we don't call this earlier at the beginning
  ; of this function because we need to populate some variables
  ; related to current installed version if detected and whether
  ; we are downgrading or not.
  ${If} $PassiveMode = 1
    Call PageLeaveReinstall
  ${Else}
    nsDialogs::Create 1018
    Pop $R4
    ${IfThen} $(^RTL) = 1 ${|} nsDialogs::SetRTL $(^RTL) ${|}

    ${NSD_CreateLabel} 0 0 100% 24u $R1
    Pop $R1

    ${NSD_CreateRadioButton} 30u 50u -30u 8u $R2
    Pop $R2
    ${NSD_OnClick} $R2 PageReinstallUpdateSelection

    ${NSD_CreateRadioButton} 30u 70u -30u 8u $R3
    Pop $R3
    ; Disable this radio button if downgrading and downgrades are disabled
    !if "${ALLOWDOWNGRADES}" == "false"
      ${IfThen} $R0 = -1 ${|} EnableWindow $R3 0 ${|}
    !endif
    ${NSD_OnClick} $R3 PageReinstallUpdateSelection

    ; Check the first radio button if this the first time
    ; we enter this page or if the second button wasn't
    ; selected the last time we were on this page
    ${If} $ReinstallPageCheck <> 2
      SendMessage $R2 ${BM_SETCHECK} ${BST_CHECKED} 0
    ${Else}
      SendMessage $R3 ${BM_SETCHECK} ${BST_CHECKED} 0
    ${EndIf}

    ${NSD_SetFocus} $R2
    nsDialogs::Show
  ${EndIf}
FunctionEnd
Function PageReinstallUpdateSelection
  ${NSD_GetState} $R2 $R1
  ${If} $R1 == ${BST_CHECKED}
    StrCpy $ReinstallPageCheck 1
  ${Else}
    StrCpy $ReinstallPageCheck 2
  ${EndIf}
FunctionEnd
Function PageLeaveReinstall
  ${NSD_GetState} $R2 $R1

  ; If migrating from Wix, always uninstall
  ${If} $WixMode = 1
    Goto reinst_uninstall
  ${EndIf}

  ; In update mode, always proceeds without uninstalling
  ${If} $UpdateMode = 1
    Goto reinst_done
  ${EndIf}

  ; $R0 holds whether same(0)/upgrading(1)/downgrading(-1) version
  ; $R1 holds the radio buttons state:
  ;   1 => first choice was selected
  ;   0 => second choice was selected
  ${If} $R0 = 0 ; Same version, proceed
    ${If} $R1 = 1              ; User chose to add/reinstall
      Goto reinst_done
    ${Else}                    ; User chose to uninstall
      Goto reinst_uninstall
    ${EndIf}
  ${ElseIf} $R0 = 1 ; Upgrading
    ${If} $R1 = 1              ; User chose to uninstall
      Goto reinst_uninstall
    ${Else}
      Goto reinst_done         ; User chose NOT to uninstall
    ${EndIf}
  ${ElseIf} $R0 = -1 ; Downgrading
    ${If} $R1 = 1              ; User chose to uninstall
      Goto reinst_uninstall
    ${Else}
      Goto reinst_done         ; User chose NOT to uninstall
    ${EndIf}
  ${EndIf}

  reinst_uninstall:
    HideWindow
    ClearErrors

    ${If} $WixMode = 1
      ReadRegStr $R1 HKLM "$R6" "UninstallString"
      ExecWait '$R1' $0
    ${Else}
      ReadRegStr $4 SHCTX "${MANUPRODUCTKEY}" ""
      ReadRegStr $R1 SHCTX "${UNINSTKEY}" "UninstallString"
      ${IfThen} $UpdateMode = 1 ${|} StrCpy $R1 "$R1 /UPDATE" ${|} ; append /UPDATE
      ${IfThen} $PassiveMode = 1 ${|} StrCpy $R1 "$R1 /P" ${|} ; append /P
      StrCpy $R1 "$R1 _?=$4" ; append uninstall directory
      ExecWait '$R1' $0
    ${EndIf}

    BringToFront

    ${IfThen} ${Errors} ${|} StrCpy $0 2 ${|} ; ExecWait failed, set fake exit code

    ${If} $0 <> 0
    ${OrIf} ${FileExists} "$INSTDIR\${MAINBINARYNAME}.exe"
      ; User cancelled wix uninstaller? return to select un/reinstall page
      ${If} $WixMode = 1
      ${AndIf} $0 = 1602
        Abort
      ${EndIf}

      ; User cancelled NSIS uninstaller? return to select un/reinstall page
      ${If} $0 = 1
        Abort
      ${EndIf}

      ; Other errors? show generic error message and return to select un/reinstall page
      MessageBox MB_ICONEXCLAMATION "$(unableToUninstall)"
      Abort
    ${EndIf}

    ; The NSIS uninstaller was run in place (`_?=`), so it cannot delete itself
    ; and the install directory, clean them up here once the uninstall succeeded
    ; https://nsis.sourceforge.io/Docs/Chapter3.html#:~:text=path%20contains%20spaces.-,3.2.3%20Examples,-installer.exe%20/NCRC
    ${If} $WixMode = 0
    ${AndIf} $4 != ""
      Delete "$4\uninstall.exe"
      RMDir "$4"
    ${EndIf}
  reinst_done:
FunctionEnd

; 5. Choose install directory page
; Maktaba (13.14): Maktaba's welcome page (where it installs, the Start menu and desktop
; shortcuts) in place of MUI's welcome, directory and finish pages; skipped when passive.
; On a first install it is the first page.
Page custom MaktabaWelcome MaktabaWelcomeLeave

; 6. Start menu shortcut page
Var AppStartMenuFolder
!if "${STARTMENUFOLDER}" != ""
  !define MUI_PAGE_CUSTOMFUNCTION_PRE SkipIfPassive
  !define MUI_STARTMENUPAGE_DEFAULTFOLDER "${STARTMENUFOLDER}"
!else
  !define MUI_PAGE_CUSTOMFUNCTION_PRE Skip
!endif
!insertmacro MUI_PAGE_STARTMENU Application $AppStartMenuFolder

; 7. Installation page
; Maktaba (13.14): drawn as Maktaba's installing page (MaktabaInstallingShow); when the
; install fails the window stays open with the details shown (MaktabaInstallingLeave, which
; MUI calls as the install ends)
!define MUI_PAGE_CUSTOMFUNCTION_SHOW MaktabaInstallingShow
!define MUI_PAGE_CUSTOMFUNCTION_LEAVE MaktabaInstallingLeave
!insertmacro MUI_PAGE_INSTFILES

; 8. Finish page
; Maktaba (13.14): none. The desktop shortcut is the welcome page's choice, "Open Maktaba
; when it's done" the installing page's; the window closes when the install is done
; (Section Install) and .onInstSuccess opens Maktaba when asked.

Function RunMainBinary
  nsis_tauri_utils::RunAsUser "$INSTDIR\${MAINBINARYNAME}.exe" ""
FunctionEnd

; Uninstaller Pages
; 1. Confirm uninstall page
Var DeleteAppDataCheckbox
Var DeleteAppDataCheckboxState
!define /ifndef WS_EX_LAYOUTRTL         0x00400000
!define MUI_PAGE_CUSTOMFUNCTION_SHOW un.ConfirmShow
Function un.ConfirmShow ; Add add a `Delete app data` check box
  ; $1 inner dialog HWND
  ; $2 window DPI
  ; $3 style
  ; $4 x
  ; $5 y
  ; $6 width
  ; $7 height
  FindWindow $1 "#32770" "" $HWNDPARENT ; Find inner dialog
  System::Call "user32::GetDpiForWindow(p r1) i .r2"
  ${If} $(^RTL) = 1
    StrCpy $3 "${__NSD_CheckBox_EXSTYLE} | ${WS_EX_LAYOUTRTL}"
    IntOp $4 50 * $2
  ${Else}
    StrCpy $3 "${__NSD_CheckBox_EXSTYLE}"
    IntOp $4 0 * $2
  ${EndIf}
  IntOp $5 100 * $2
  IntOp $6 400 * $2
  IntOp $7 25 * $2
  IntOp $4 $4 / 96
  IntOp $5 $5 / 96
  IntOp $6 $6 / 96
  IntOp $7 $7 / 96
  System::Call 'user32::CreateWindowEx(i r3, w "${__NSD_CheckBox_CLASS}", w "$(deleteAppData)", i ${__NSD_CheckBox_STYLE}, i r4, i r5, i r6, i r7, p r1, i0, i0, i0) i .s'
  Pop $DeleteAppDataCheckbox
  SendMessage $HWNDPARENT ${WM_GETFONT} 0 0 $1
  SendMessage $DeleteAppDataCheckbox ${WM_SETFONT} $1 1
FunctionEnd
!define MUI_PAGE_CUSTOMFUNCTION_LEAVE un.ConfirmLeave
Function un.ConfirmLeave
  SendMessage $DeleteAppDataCheckbox ${BM_GETCHECK} 0 0 $DeleteAppDataCheckboxState
FunctionEnd
!define MUI_PAGE_CUSTOMFUNCTION_PRE un.SkipIfPassive
!insertmacro MUI_UNPAGE_CONFIRM

; 2. Uninstalling Page
!insertmacro MUI_UNPAGE_INSTFILES

;Languages
{{#each languages}}
!insertmacro MUI_LANGUAGE "{{this}}"
{{/each}}
!insertmacro MUI_RESERVEFILE_LANGDLL
{{#each language_files}}
  !include "{{this}}"
{{/each}}

; ---- Maktaba (13.14): the pages' words, English and Arabic --------------------------------
; (the mockups' copy: docs/design/desktop/mockups/installer-welcome.html, installer-progress.html)
!ifdef MK_SERVER
  LangString mkLead ${LANG_ENGLISH} "The server, the web app and Live TV, in one app. It installs for you only and needs no administrator."
!else
  LangString mkLead ${LANG_ENGLISH} "Maktaba on this PC. It installs for you only and needs no administrator."
!endif
LangString mkInstallsIn ${LANG_ENGLISH} "Installs in"
LangString mkChange ${LANG_ENGLISH} "Change"
LangString mkChooseFolder ${LANG_ENGLISH} "Choose where to install Maktaba"
LangString mkStartMenu ${LANG_ENGLISH} "Add Maktaba to the Start menu"
LangString mkDesktop ${LANG_ENGLISH} "Put a shortcut on the desktop"
LangString mkFine ${LANG_ENGLISH} "Free and open source (MIT licence)"
LangString mkVersion ${LANG_ENGLISH} "Version $R1 · $R2"
LangString mkGB ${LANG_ENGLISH} "$R2 GB"
LangString mkMB ${LANG_ENGLISH} "$R2 MB"
LangString mkInstallingLead ${LANG_ENGLISH} "This takes about a minute. Your library and settings stay where they are."
LangString mkStepApp ${LANG_ENGLISH} "Maktaba app"
LangString mkStepServer ${LANG_ENGLISH} "The server and its tools"
LangString mkStepFinish ${LANG_ENGLISH} "Start menu and shortcuts"
LangString mkNowApp ${LANG_ENGLISH} "Copying Maktaba"
LangString mkNowServer ${LANG_ENGLISH} "Copying the server: the database, ffmpeg and Python"
LangString mkNowFinish ${LANG_ENGLISH} "Adding the shortcuts"
LangString mkNowDone ${LANG_ENGLISH} "Done"
LangString mkFailed ${LANG_ENGLISH} "The install stopped. The details below say where."
LangString mkOpenAfter ${LANG_ENGLISH} "Open Maktaba when it's done"
!ifdef LANG_ARABIC
  !ifdef MK_SERVER
    LangString mkLead ${LANG_ARABIC} "الخادم وتطبيق الويب والبث المباشر في تطبيق واحد. يُثبَّت لك وحدك ولا يحتاج إلى مسؤول."
  !else
    LangString mkLead ${LANG_ARABIC} "مكتبة على هذا الحاسوب. يُثبَّت لك وحدك ولا يحتاج إلى مسؤول."
  !endif
  LangString mkInstallsIn ${LANG_ARABIC} "يُثبَّت في"
  LangString mkChange ${LANG_ARABIC} "تغيير"
  LangString mkChooseFolder ${LANG_ARABIC} "اختر أين تُثبَّت مكتبة"
  LangString mkStartMenu ${LANG_ARABIC} "أضف مكتبة إلى قائمة ابدأ"
  LangString mkDesktop ${LANG_ARABIC} "ضع اختصارًا على سطح المكتب"
  LangString mkFine ${LANG_ARABIC} "مجاني ومفتوح المصدر (ترخيص MIT)"
  LangString mkVersion ${LANG_ARABIC} "الإصدار $R1 · $R2"
  LangString mkGB ${LANG_ARABIC} "$R2 غيغابايت"
  LangString mkMB ${LANG_ARABIC} "$R2 ميغابايت"
  LangString mkInstallingLead ${LANG_ARABIC} "يستغرق هذا دقيقة تقريبًا. تبقى مكتبتك وإعداداتك في مكانها."
  LangString mkStepApp ${LANG_ARABIC} "تطبيق مكتبة"
  LangString mkStepServer ${LANG_ARABIC} "الخادم وأدواته"
  LangString mkStepFinish ${LANG_ARABIC} "قائمة ابدأ والاختصارات"
  LangString mkNowApp ${LANG_ARABIC} "نسخ مكتبة"
  LangString mkNowServer ${LANG_ARABIC} "نسخ الخادم: قاعدة البيانات وffmpeg وPython"
  LangString mkNowFinish ${LANG_ARABIC} "إضافة الاختصارات"
  LangString mkNowDone ${LANG_ARABIC} "تم"
  LangString mkFailed ${LANG_ARABIC} "توقّف التثبيت. التفاصيل أدناه تبيّن أين."
  LangString mkOpenAfter ${LANG_ARABIC} "افتح مكتبة عند الانتهاء"
!endif

; ---- Maktaba (13.14): the pages ---------------------------------------------------------------
!define MK_LICENSE_URL "https://github.com/Hamza-Labs-Core/Maktaba/blob/main/LICENSE"
!define /ifndef MK_WS_CLIPSIBLINGS 0x04000000
!define /ifndef MK_SWP_TOP 0x13        ; SWP_NOSIZE | SWP_NOMOVE | SWP_NOACTIVATE
!define /ifndef MK_PBM_SETBARCOLOR 0x0409
!define /ifndef MK_PBM_SETBKCOLOR 0x2001
!define /ifndef MK_WS_EX_RTL 0x00403000 ; WS_EX_LAYOUTRTL | WS_EX_RTLREADING | WS_EX_RIGHT
ReserveFile "${MAKTABA_ART}\brand-en-100.bmp"
ReserveFile "${MAKTABA_ART}\title-welcome-en-100.bmp"
ReserveFile "${MAKTABA_ART}\title-progress-en-100.bmp"

; the art's scale for this window's DPI (GetDpiForWindow: Windows 10 1607 and later; 96 before)
Function MaktabaScale
  StrCpy $0 0
  System::Call 'user32::GetDpiForWindow(p $HWNDPARENT) i .r0'
  ${If} $0 < 1
    StrCpy $0 96
  ${EndIf}
  StrCpy $MkDpi $0
  ${If} $0 < 120
    StrCpy $MkScale 100
  ${ElseIf} $0 < 168
    StrCpy $MkScale 150
  ${Else}
    StrCpy $MkScale 200
  ${EndIf}
FunctionEnd

!macro MK_ART_ONE NAME LANG SCALE
  File "/oname=$PLUGINSDIR\mk-${NAME}.bmp" "${MAKTABA_ART}\${NAME}-${LANG}-${SCALE}.bmp"
!macroend
!macro MK_ART_LANG NAME LANG
  ${If} $MkScale = 200
    !insertmacro MK_ART_ONE ${NAME} ${LANG} 200
  ${ElseIf} $MkScale = 150
    !insertmacro MK_ART_ONE ${NAME} ${LANG} 150
  ${Else}
    !insertmacro MK_ART_ONE ${NAME} ${LANG} 100
  ${EndIf}
!macroend
; MK_ART NAME: $PLUGINSDIR\mk-NAME.bmp, in the page's language (Arabic is the one right to left
; language) at the window's scale
!macro MK_ART NAME
  InitPluginsDir
  ${If} $(^RTL) = 1
    !insertmacro MK_ART_LANG ${NAME} ar
  ${Else}
    !insertmacro MK_ART_LANG ${NAME} en
  ${EndIf}
!macroend

Function MaktabaFonts
  ${If} $MkFont == ""
    CreateFont $MkFont "Segoe UI" 9 400
    CreateFont $MkFontLead "Segoe UI" 10 400
    CreateFont $MkFontGlyph "Segoe UI Symbol" 9 400
  ${EndIf}
FunctionEnd

; the page over the whole window, MUI's header and branding hidden (as MUI's welcome page does)
Function MaktabaFullWindow
  LockWindow on
  ShowWindow $mui.Branding.Background ${SW_HIDE}
  ShowWindow $mui.Branding.Text ${SW_HIDE}
  ShowWindow $mui.Header.Text ${SW_HIDE}
  ShowWindow $mui.Header.SubText ${SW_HIDE}
  ShowWindow $mui.Header.Image ${SW_HIDE}
  ShowWindow $mui.Header.Background ${SW_HIDE}
  ShowWindow $mui.Line.Standard ${SW_HIDE}
  ShowWindow $mui.Line.FullWindow ${SW_NORMAL}
  SetCtlColors $HWNDPARENT "${MK_FG}" "${MK_WINDOW}"
  LockWindow off
FunctionEnd
Function MaktabaNormalWindow
  LockWindow on
  ShowWindow $mui.Branding.Background ${SW_NORMAL}
  ShowWindow $mui.Branding.Text ${SW_NORMAL}
  ShowWindow $mui.Header.Text ${SW_NORMAL}
  ShowWindow $mui.Header.SubText ${SW_NORMAL}
  ShowWindow $mui.Header.Image ${SW_NORMAL}
  ShowWindow $mui.Header.Background ${SW_NORMAL}
  ShowWindow $mui.Line.Standard ${SW_NORMAL}
  ShowWindow $mui.Line.FullWindow ${SW_HIDE}
  LockWindow off
FunctionEnd

; $R0 = "Version <v> · <size>": the version, and what the install takes (tauri's estimate, in
; KB) in MB, or GB with one decimal; in Arabic the size's digits are Arabic-Indic
Function MaktabaVersionText
  StrCpy $R1 "${VERSION}"
  IntOp $0 ${ESTIMATEDSIZE} + 0
  ${If} $0 >= 1048576
    IntOp $0 $0 * 10
    IntOp $0 $0 / 1048576
    IntOp $1 $0 / 10
    IntOp $2 $0 % 10
    StrCpy $R2 "$1.$2"
    Call MaktabaDigits
    StrCpy $R2 "$(mkGB)"
  ${Else}
    IntOp $0 $0 / 1024
    ${If} $0 < 1
      StrCpy $0 1
    ${EndIf}
    StrCpy $R2 $0
    Call MaktabaDigits
    StrCpy $R2 "$(mkMB)"
  ${EndIf}
  StrCpy $R0 "$(mkVersion)"
FunctionEnd

; $R2: its digits and decimal point in Arabic-Indic, when the page is Arabic
Function MaktabaDigits
  ${If} $(^RTL) <> 1
    Return
  ${EndIf}
  StrCpy $3 ""
  StrCpy $4 0
  ${Do}
    StrCpy $5 $R2 1 $4
    ${If} $5 == ""
      ${Break}
    ${ElseIf} $5 == "."
      StrCpy $5 "٫"
    ${Else}
      StrCpy $5 "٠١٢٣٤٥٦٧٨٩" 1 $5
    ${EndIf}
    StrCpy $3 "$3$5"
    IntOp $4 $4 + 1
  ${Loop}
  StrCpy $R2 $3
FunctionEnd

; $R0 (a control) above the brand pane's bitmap, which no longer paints over it
Function MaktabaAboveBrand
  System::Call 'user32::GetWindowLong(p $MkBrand, i -16) i .r0'
  IntOp $0 $0 | ${MK_WS_CLIPSIBLINGS}
  System::Call 'user32::SetWindowLong(p $MkBrand, i -16, i r0)'
  System::Call 'user32::SetWindowPos(p $R0, p 0, i 0, i 0, i 0, i 0, i ${MK_SWP_TOP})'
FunctionEnd

; the welcome page: the brand pane, the title, where it installs, the two shortcuts
Function MaktabaWelcome
  Call SkipIfPassive
  Call MaktabaScale
  !insertmacro MK_ART brand
  !insertmacro MK_ART title-welcome
  nsDialogs::Create 1044
  Pop $MkPage
  ${If} $MkPage == error
    Abort
  ${EndIf}
  nsDialogs::SetRTL $(^RTL)
  SetCtlColors $MkPage "${MK_FG}" "${MK_WINDOW}"
  Call MaktabaFonts
  Call MaktabaFullWindow

  ${NSD_CreateBitmap} 0u 0u 109u 193u ""
  Pop $MkBrand
  ${NSD_SetStretchedImage} $MkBrand "$PLUGINSDIR\mk-brand.bmp" $MkBrandImage
  Call MaktabaVersionText
  ${NSD_CreateLabel} 11u 33u 92u 10u "$R0"
  Pop $R0
  SetCtlColors $R0 "${MK_MUTED}" "${MK_PANE}"
  SendMessage $R0 ${WM_SETFONT} $MkFont 1
  Call MaktabaAboveBrand

  ${NSD_CreateBitmap} 121u 8u 188u 25u ""
  Pop $MkTitle
  ${NSD_SetStretchedImage} $MkTitle "$PLUGINSDIR\mk-title-welcome.bmp" $MkTitleImage

  ${NSD_CreateLabel} 121u 36u 186u 40u "$(mkLead)"
  Pop $0
  SetCtlColors $0 "${MK_MUTED}" "${MK_WINDOW}"
  SendMessage $0 ${WM_SETFONT} $MkFontLead 1

  ${NSD_CreateLabel} 121u 80u 186u 10u "$(mkInstallsIn)"
  Pop $0
  SetCtlColors $0 "${MK_MUTED}" "${MK_WINDOW}"
  SendMessage $0 ${WM_SETFONT} $MkFont 1
  ${NSD_CreateText} 121u 92u 138u 14u "$INSTDIR"
  Pop $MkDir
  SendMessage $MkDir ${EM_SETREADONLY} 1 0
  SetCtlColors $MkDir "${MK_FG}" "${MK_SUBTLE}"
  SendMessage $MkDir ${WM_SETFONT} $MkFont 1
  ; a path reads left to right in Arabic too
  ${If} $(^RTL) = 1
    System::Call 'user32::GetWindowLong(p $MkDir, i -20) i .r0'
    IntOp $0 $0 & 0xFFFF8FFF ; no WS_EX_RIGHT, WS_EX_RTLREADING, WS_EX_LEFTSCROLLBAR
    System::Call 'user32::SetWindowLong(p $MkDir, i -20, i r0)'
    System::Call 'user32::GetWindowLong(p $MkDir, i -16) i .r0'
    IntOp $0 $0 & 0xFFFFFFFD ; no ES_RIGHT
    System::Call 'user32::SetWindowLong(p $MkDir, i -16, i r0)'
  ${EndIf}
  ${NSD_CreateButton} 263u 91u 46u 16u "$(mkChange)"
  Pop $0
  SendMessage $0 ${WM_SETFONT} $MkFont 1
  ${NSD_OnClick} $0 MaktabaChange

  ; check boxes drawn classic, so their words take the page's colours
  ${NSD_CreateCheckBox} 121u 114u 186u 12u "$(mkStartMenu)"
  Pop $MkStartMenuBox
  System::Call 'UxTheme::SetWindowTheme(p $MkStartMenuBox, w " ", w " ")'
  SetCtlColors $MkStartMenuBox "${MK_FG}" "${MK_WINDOW}"
  SendMessage $MkStartMenuBox ${WM_SETFONT} $MkFont 1
  ${If} $MkStartMenu = 1
    ${NSD_Check} $MkStartMenuBox
  ${EndIf}
  ${NSD_CreateCheckBox} 121u 128u 186u 12u "$(mkDesktop)"
  Pop $MkDesktopBox
  System::Call 'UxTheme::SetWindowTheme(p $MkDesktopBox, w " ", w " ")'
  SetCtlColors $MkDesktopBox "${MK_FG}" "${MK_WINDOW}"
  SendMessage $MkDesktopBox ${WM_SETFONT} $MkFont 1
  ${If} $MkDesktop = 1
    ${NSD_Check} $MkDesktopBox
  ${EndIf}

  ${NSD_CreateLink} 121u 178u 186u 10u "$(mkFine)"
  Pop $0
  SetCtlColors $0 "${MK_BRASS}" "${MK_WINDOW}"
  SendMessage $0 ${WM_SETFONT} $MkFont 1
  ${NSD_OnClick} $0 MaktabaLicence

  GetDlgItem $0 $HWNDPARENT 1
  SendMessage $0 ${WM_SETTEXT} 0 "STR:$(^InstallBtn)"
  ; Back only to the reinstall page, when it was shown
  ${If} $MkReinstallShown <> 1
    GetDlgItem $0 $HWNDPARENT 3
    EnableWindow $0 0
  ${EndIf}
  nsDialogs::Show
  ${NSD_FreeImage} $MkBrandImage
  ${NSD_FreeImage} $MkTitleImage
FunctionEnd

Function MaktabaWelcomeLeave
  ${NSD_GetState} $MkStartMenuBox $MkStartMenu
  ${NSD_GetState} $MkDesktopBox $MkDesktop
  GetInstDirError $0
  ${If} $0 <> 0
    MessageBox MB_ICONEXCLAMATION "$(^DirSubText)$\r$\n$INSTDIR"
    Abort
  ${EndIf}
  Call MaktabaNormalWindow
FunctionEnd

; the folder: a chosen folder gets Maktaba's own folder inside it, as MUI's directory page does
Function MaktabaChange
  Pop $0
  nsDialogs::SelectFolderDialog "$(mkChooseFolder)" "$INSTDIR"
  Pop $0
  ${If} $0 == error
  ${OrIf} $0 == ""
    Return
  ${EndIf}
  StrCpy $1 $0 1 -1
  ${If} $1 == "\"
    StrCpy $0 $0 -1
  ${EndIf}
  ${GetFileName} $0 $1
  ${If} $1 != "${PRODUCTNAME}"
    StrCpy $0 "$0\${PRODUCTNAME}"
  ${EndIf}
  StrCpy $INSTDIR $0
  ${NSD_SetText} $MkDir $INSTDIR
FunctionEnd

Function MaktabaLicence
  Pop $0
  ExecShell "open" "${MK_LICENSE_URL}"
FunctionEnd

; MaktabaPlace: the dialog-unit box $R5 $R6 $R7 $R8 (x y w h) of $MkPage in pixels, mirrored
; when the page is right to left
Function MaktabaPlace
  IntOp $R7 $R5 + $R7
  IntOp $R8 $R6 + $R8
  System::Call '*(i R5, i R6, i R7, i R8) p .r9'
  System::Call 'user32::MapDialogRect(p $MkPage, p r9)'
  System::Call '*$9(i .R5, i .R6, i .R7, i .R8)'
  System::Free $9
  IntOp $R7 $R7 - $R5
  IntOp $R8 $R8 - $R6
  ${If} $(^RTL) = 1
    System::Call '*(i, i, i, i) p .r9'
    System::Call 'user32::GetClientRect(p $MkPage, p r9)'
    System::Call '*$9(i, i, i .r8, i)'
    System::Free $9
    IntOp $R5 $8 - $R5
    IntOp $R5 $R5 - $R7
  ${EndIf}
FunctionEnd

; MaktabaChild: $R0 = a new control of $MkPage: class $R1, words $R2, style $R3, extended
; style $R4, at the dialog-unit box $R5 $R6 $R7 $R8, in the page's font
Function MaktabaChild
  Call MaktabaPlace
  ${If} $(^RTL) = 1
  ${AndIf} $R1 != "msctls_progress32"
    IntOp $R4 $R4 | 0x3000 ; WS_EX_RTLREADING | WS_EX_RIGHT
  ${EndIf}
  IntOp $R3 $R3 | 0x50000000 ; WS_CHILD | WS_VISIBLE
  System::Call 'user32::CreateWindowEx(i R4, w R1, w R2, i R3, i R5, i R6, i R7, i R8, p $MkPage, p 0, p 0, p 0) p .R0'
  SendMessage $R0 ${WM_SETFONT} $MkFont 1
FunctionEnd

; MK_STEP_ROW GLYPH TEXT WORDS: one step's two controls at the row $R6 (then the next row)
!macro MK_STEP_ROW GLYPH TEXT WORDS
  StrCpy $R1 "STATIC"
  StrCpy $R2 "○"
  StrCpy $R3 0
  StrCpy $R4 0
  StrCpy $R5 121
  StrCpy $R7 10
  StrCpy $R8 10
  Push $R6
  Call MaktabaChild
  Pop $R6
  StrCpy ${GLYPH} $R0
  SendMessage ${GLYPH} ${WM_SETFONT} $MkFontGlyph 1
  SetCtlColors ${GLYPH} "${MK_CONTROL}" "${MK_WINDOW}"
  StrCpy $R2 "${WORDS}"
  StrCpy $R5 133
  StrCpy $R7 174
  Push $R6
  Call MaktabaChild
  Pop $R6
  StrCpy ${TEXT} $R0
  SetCtlColors ${TEXT} "${MK_MUTED}" "${MK_WINDOW}"
  IntOp $R6 $R6 + 14
!macroend

; the installing page: tauri's progress, drawn as Maktaba's page
Function MaktabaInstallingShow
  FindWindow $MkPage "#32770" "" $HWNDPARENT
  Call MaktabaScale
  !insertmacro MK_ART brand
  !insertmacro MK_ART title-progress
  Call MaktabaFonts
  Call MaktabaFullWindow
  ; over the whole window, where MUI's welcome page sits (the outer dialog's control 1044)
  GetDlgItem $0 $HWNDPARENT 1044
  System::Call '*(i, i, i, i) p .r1'
  System::Call 'user32::GetWindowRect(p r0, p r1)'
  System::Call 'user32::MapWindowPoints(p 0, p $HWNDPARENT, p r1, i 2)'
  System::Call '*$1(i .r2, i .r3, i .r4, i .r5)'
  System::Free $1
  ${If} $4 < $2
    StrCpy $6 $2
    StrCpy $2 $4
    StrCpy $4 $6
  ${EndIf}
  IntOp $4 $4 - $2
  IntOp $5 $5 - $3
  System::Call 'user32::SetWindowPos(p $MkPage, p 0, i r2, i r3, i r4, i r5, i 0x14)'
  SetCtlColors $MkPage "${MK_FG}" "${MK_WINDOW}"

  ; tauri's own controls: the progress bar moves into the page, brass on the field colour; the
  ; status line and the details give way to the steps (the details come back on a failure)
  GetDlgItem $0 $MkPage 1006
  ShowWindow $0 ${SW_HIDE}
  GetDlgItem $0 $MkPage 1016
  ShowWindow $0 ${SW_HIDE}
  GetDlgItem $0 $MkPage 1027
  ShowWindow $0 ${SW_HIDE}

  StrCpy $R1 "STATIC"
  StrCpy $R2 ""
  StrCpy $R4 0
  StrCpy $R3 0x4E ; SS_BITMAP | SS_REALSIZECONTROL
  StrCpy $R5 0
  StrCpy $R6 0
  StrCpy $R7 109
  StrCpy $R8 193
  Call MaktabaChild
  StrCpy $MkBrand $R0
  LoadAndSetImage /STRINGID /RESIZETOFIT $MkBrand ${IMAGE_BITMAP} ${LR_LOADFROMFILE} "$PLUGINSDIR\mk-brand.bmp" $MkBrandImage
  Call MaktabaVersionText
  StrCpy $R2 $R0
  StrCpy $R3 0
  StrCpy $R5 11
  StrCpy $R6 33
  StrCpy $R7 92
  StrCpy $R8 10
  Call MaktabaChild
  SetCtlColors $R0 "${MK_MUTED}" "${MK_PANE}"
  Call MaktabaAboveBrand

  StrCpy $R2 ""
  StrCpy $R3 0x4E
  StrCpy $R5 121
  StrCpy $R6 8
  StrCpy $R7 188
  StrCpy $R8 25
  Call MaktabaChild
  StrCpy $MkTitle $R0
  LoadAndSetImage /STRINGID /RESIZETOFIT $MkTitle ${IMAGE_BITMAP} ${LR_LOADFROMFILE} "$PLUGINSDIR\mk-title-progress.bmp" $MkTitleImage

  StrCpy $R2 "$(mkInstallingLead)"
  StrCpy $R3 0
  StrCpy $R5 121
  StrCpy $R6 36
  StrCpy $R7 186
  StrCpy $R8 30
  Call MaktabaChild
  SetCtlColors $R0 "${MK_MUTED}" "${MK_WINDOW}"
  SendMessage $R0 ${WM_SETFONT} $MkFontLead 1

  GetDlgItem $0 $MkPage 1004
  StrCpy $R5 121
  StrCpy $R6 72
  StrCpy $R7 186
  StrCpy $R8 6
  Call MaktabaPlace
  System::Call 'user32::SetWindowPos(p r0, p 0, i R5, i R6, i R7, i R8, i 0x14)'
  System::Call 'UxTheme::SetWindowTheme(p r0, w " ", w " ")'
  SendMessage $0 ${MK_PBM_SETBARCOLOR} 0 ${MK_BRASS_REF}
  SendMessage $0 ${MK_PBM_SETBKCOLOR} 0 ${MK_SUBTLE_REF}

  StrCpy $R2 "$(mkNowApp)"
  StrCpy $R5 121
  StrCpy $R6 82
  StrCpy $R7 186
  StrCpy $R8 10
  Call MaktabaChild
  StrCpy $MkNow $R0
  SetCtlColors $MkNow "${MK_MUTED}" "${MK_WINDOW}"

  ; the steps: a glyph (○ waiting, ● now, ✓ done) and its words
  StrCpy $R6 100
  !insertmacro MK_STEP_ROW $MkStep1 $MkStep1Text "$(mkStepApp)"
  !ifdef MK_SERVER
    !insertmacro MK_STEP_ROW $MkStep2 $MkStep2Text "$(mkStepServer)"
  !endif
  !insertmacro MK_STEP_ROW $MkStep3 $MkStep3Text "$(mkStepFinish)"
  StrCpy $R0 $MkStep1
  StrCpy $R1 $MkStep1Text
  StrCpy $R2 1
  StrCpy $R3 "$(mkNowApp)"
  Call MaktabaStep

  ; "Open Maktaba when it's done": a choice for a person at the window, not the updater's
  ${If} $PassiveMode <> 1
    StrCpy $R1 "BUTTON"
    StrCpy $R2 "$(mkOpenAfter)"
    StrCpy $R3 0x00010003 ; WS_TABSTOP | BS_AUTOCHECKBOX
    StrCpy $R4 0
    ${If} $(^RTL) = 1
      StrCpy $R4 0x00400000 ; WS_EX_LAYOUTRTL: the box at the right
    ${EndIf}
    StrCpy $R5 121
    StrCpy $R6 176
    StrCpy $R7 186
    StrCpy $R8 12
    Call MaktabaChild
    StrCpy $MkOpen $R0
    System::Call 'UxTheme::SetWindowTheme(p $MkOpen, w " ", w " ")'
    SetCtlColors $MkOpen "${MK_FG}" "${MK_WINDOW}"
    SendMessage $MkOpen ${BM_SETCHECK} ${BST_CHECKED} 0
  ${EndIf}

  ; the ground: one static over the page in the window colour, made last (a new control goes
  ; below the others), kept at the bottom and clipped to what they leave uncovered
  StrCpy $R1 "STATIC"
  StrCpy $R2 ""
  StrCpy $R3 ${MK_WS_CLIPSIBLINGS}
  StrCpy $R4 0
  StrCpy $R5 0
  StrCpy $R6 0
  StrCpy $R7 400
  StrCpy $R8 300
  Call MaktabaChild
  SetCtlColors $R0 "${MK_FG}" "${MK_WINDOW}"
  System::Call 'user32::SetWindowPos(p R0, p 1, i 0, i 0, i 0, i 0, i ${MK_SWP_TOP})' ; HWND_BOTTOM
FunctionEnd

; MaktabaStep: the step whose glyph is $R0 and words $R1 is now $R2 (1 running, 2 done); the
; line under the progress says $R3. Nothing when there is no page (a silent install).
Function MaktabaStep
  ${If} $R0 == ""
  ${OrIf} $R0 == 0
    Return
  ${EndIf}
  ${If} $R2 = 2
    SendMessage $R0 ${WM_SETTEXT} 0 "STR:✓"
    SetCtlColors $R0 "${MK_OK}" "${MK_WINDOW}"
    SetCtlColors $R1 "${MK_FG}" "${MK_WINDOW}"
  ${Else}
    SendMessage $R0 ${WM_SETTEXT} 0 "STR:●"
    SetCtlColors $R0 "${MK_BRASS}" "${MK_WINDOW}"
    SetCtlColors $R1 "${MK_FG}" "${MK_WINDOW}"
  ${EndIf}
  System::Call 'user32::InvalidateRect(p R0, p 0, i 1)'
  System::Call 'user32::InvalidateRect(p R1, p 0, i 1)'
  ${If} $MkNow != ""
    SendMessage $MkNow ${WM_SETTEXT} 0 "STR:$R3"
    System::Call 'user32::InvalidateRect(p $MkNow, p 0, i 1)'
  ${EndIf}
FunctionEnd

; MK_STEP GLYPH TEXT STATE NOW: from a section
!macro MK_STEP GLYPH TEXT STATE NOW
  Push $R0
  Push $R1
  Push $R2
  Push $R3
  StrCpy $R0 ${GLYPH}
  StrCpy $R1 ${TEXT}
  StrCpy $R2 ${STATE}
  StrCpy $R3 "${NOW}"
  Call MaktabaStep
  Pop $R3
  Pop $R2
  Pop $R1
  Pop $R0
!macroend

; as the install ends: when it failed, the details come back where the steps were (with
; "Show details" and the status line), and the window stays open (no auto close on a failure)
Function MaktabaInstallingLeave
  IfAbort +2
  Return
  ${If} $MkPage == ""
    Return
  ${EndIf}
  SetDetailsView show
  GetDlgItem $0 $MkPage 1027
  ShowWindow $0 ${SW_SHOW}
  GetDlgItem $0 $MkPage 1006
  ShowWindow $0 ${SW_SHOW}
  GetDlgItem $0 $MkPage 1016
  StrCpy $R5 121
  StrCpy $R6 96
  StrCpy $R7 186
  StrCpy $R8 76
  Call MaktabaPlace
  System::Call 'user32::SetWindowPos(p r0, p 0, i R5, i R6, i R7, i R8, i 0x50)' ; SWP_SHOWWINDOW | SWP_NOACTIVATE, on top
  ${If} $MkNow != ""
    SendMessage $MkNow ${WM_SETTEXT} 0 "STR:$(mkFailed)"
  ${EndIf}
FunctionEnd

; MaktabaPriorDesktop: the welcome page's desktop box starts ticked when this PC has the
; shortcut already (an upgrade's uninstall, the reinstall page's default, takes it away and this
; install makes it again), or has the WiX .msi this installer replaces (its uninstall takes the
; shortcut away too; the reinstall page finds it the same way)
Function MaktabaPriorDesktop
  ${If} ${FileExists} "$DESKTOP\${PRODUCTNAME}.lnk"
    StrCpy $MkDesktop 1
    Return
  ${EndIf}
  StrCpy $0 0
  ${Do}
    EnumRegKey $1 HKLM "SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall" $0
    ${If} $1 == ""
      ${Break}
    ${EndIf}
    IntOp $0 $0 + 1
    ReadRegStr $2 HKLM "SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\$1" "DisplayName"
    ReadRegStr $3 HKLM "SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\$1" "Publisher"
    ${If} "$2$3" == "${PRODUCTNAME}${MANUFACTURER}"
      ReadRegStr $2 HKLM "SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\$1" "UninstallString"
      ${StrCase} $3 $2 "L"
      ${StrLoc} $2 $3 "msiexec" ">"
      ${If} $2 == 0
        StrCpy $MkDesktop 1
        ${Break}
      ${EndIf}
    ${EndIf}
  ${Loop}
FunctionEnd

Function .onInit
  ${GetOptions} $CMDLINE "/P" $PassiveMode
  ${IfNot} ${Errors}
    StrCpy $PassiveMode 1
  ${EndIf}

  ${GetOptions} $CMDLINE "/NS" $NoShortcutMode
  ${IfNot} ${Errors}
    StrCpy $NoShortcutMode 1
  ${EndIf}

  ${GetOptions} $CMDLINE "/UPDATE" $UpdateMode
  ${IfNot} ${Errors}
    StrCpy $UpdateMode 1
  ${EndIf}

  ; Maktaba (13.14): the welcome page's choices, before anyone makes them (and for /S, /P)
  StrCpy $MkStartMenu 1
  StrCpy $MkDesktop 0
  StrCpy $MkOpenAfter 0

  !if "${DISPLAYLANGUAGESELECTOR}" == "true"
    !insertmacro MUI_LANGDLL_DISPLAY
  !endif

  !insertmacro SetContext

  ; Maktaba (13.14): an earlier install's desktop shortcut is kept
  Call MaktabaPriorDesktop

  ${If} $INSTDIR == "${PLACEHOLDER_INSTALL_DIR}"
    ; Set default install location
    !if "${INSTALLMODE}" == "perMachine"
      ${If} ${RunningX64}
        !if "${ARCH}" == "x64"
          StrCpy $INSTDIR "$PROGRAMFILES64\${PRODUCTNAME}"
        !else if "${ARCH}" == "arm64"
          StrCpy $INSTDIR "$PROGRAMFILES64\${PRODUCTNAME}"
        !else
          StrCpy $INSTDIR "$PROGRAMFILES\${PRODUCTNAME}"
        !endif
      ${Else}
        StrCpy $INSTDIR "$PROGRAMFILES\${PRODUCTNAME}"
      ${EndIf}
    !else if "${INSTALLMODE}" == "currentUser"
      StrCpy $INSTDIR "$LOCALAPPDATA\${PRODUCTNAME}"
    !endif

    Call RestorePreviousInstallLocation
  ${EndIf}


  !if "${INSTALLMODE}" == "both"
    !insertmacro MULTIUSER_INIT
  !endif

  ; Abort downgrades if they are disabled, in the modes that skip the reinstall page choice:
  ; silent, passive and update
  !if "${ALLOWDOWNGRADES}" == "false"
  ${If} ${Silent}
  ${OrIf} $PassiveMode = 1
  ${OrIf} $UpdateMode = 1
    ReadRegStr $R0 SHCTX "${UNINSTKEY}" "DisplayVersion"
    nsis_tauri_utils::SemverCompare "${VERSION}" $R0
    Pop $R0
    ; If downgrading
    ${If} $R0 = -1
      ${If} ${Silent}
        System::Call 'kernel32::AttachConsole(i -1)i.r0'
        ${If} $0 <> 0
          System::Call 'kernel32::GetStdHandle(i -11)i.r0'
          System::call 'kernel32::SetConsoleTextAttribute(i r0, i 0x0004)' ; set red color
          FileWrite $0 "$(silentDowngrades)"
        ${EndIf}
      ${Else}
        MessageBox MB_ICONSTOP "$(silentDowngrades)"
      ${EndIf}
      Abort
    ${EndIf}
  ${EndIf}
  !endif
FunctionEnd

Section WebView2
  ; Maktaba (13.14): the first step, the app (WebView2 when it is missing, then Maktaba.exe)
  !insertmacro MK_STEP $MkStep1 $MkStep1Text 1 "$(mkNowApp)"

  ; Check if Webview2 is already installed and skip this section
  ${If} ${RunningX64}
    ReadRegStr $4 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\${WEBVIEW2APPGUID}" "pv"
  ${Else}
    ReadRegStr $4 HKLM "SOFTWARE\Microsoft\EdgeUpdate\Clients\${WEBVIEW2APPGUID}" "pv"
  ${EndIf}
  ${If} $4 == ""
    ReadRegStr $4 HKCU "SOFTWARE\Microsoft\EdgeUpdate\Clients\${WEBVIEW2APPGUID}" "pv"
  ${EndIf}

  ${If} $4 == ""
    ; Webview2 installation
    ;
    ; Skip if updating
    ${If} $UpdateMode <> 1
      !if "${INSTALLWEBVIEW2MODE}" == "downloadBootstrapper"
        Delete "$TEMP\MicrosoftEdgeWebview2Setup.exe"
        DetailPrint "$(webview2Downloading)"
        NSISdl::download "https://go.microsoft.com/fwlink/p/?LinkId=2124703" "$TEMP\MicrosoftEdgeWebview2Setup.exe"
        Pop $0
        ${If} $0 == "success"
          DetailPrint "$(webview2DownloadSuccess)"
        ${Else}
          DetailPrint "$(webview2DownloadError)"
          Abort "$(webview2AbortError)"
        ${EndIf}
        StrCpy $6 "$TEMP\MicrosoftEdgeWebview2Setup.exe"
        Goto install_webview2
      !endif

      !if "${INSTALLWEBVIEW2MODE}" == "embedBootstrapper"
        Delete "$TEMP\MicrosoftEdgeWebview2Setup.exe"
        File "/oname=$TEMP\MicrosoftEdgeWebview2Setup.exe" "${WEBVIEW2BOOTSTRAPPERPATH}"
        DetailPrint "$(installingWebview2)"
        StrCpy $6 "$TEMP\MicrosoftEdgeWebview2Setup.exe"
        Goto install_webview2
      !endif

      !if "${INSTALLWEBVIEW2MODE}" == "offlineInstaller"
        Delete "$TEMP\MicrosoftEdgeWebView2RuntimeInstaller.exe"
        File "/oname=$TEMP\MicrosoftEdgeWebView2RuntimeInstaller.exe" "${WEBVIEW2INSTALLERPATH}"
        DetailPrint "$(installingWebview2)"
        StrCpy $6 "$TEMP\MicrosoftEdgeWebView2RuntimeInstaller.exe"
        Goto install_webview2
      !endif

      Goto webview2_done

      install_webview2:
        DetailPrint "$(installingWebview2)"
        ; $6 holds the path to the webview2 installer
        ExecWait "$6 ${WEBVIEW2INSTALLERARGS} /install" $1
        ${If} $1 = 0
          DetailPrint "$(webview2InstallSuccess)"
        ${Else}
          DetailPrint "$(webview2InstallError)"
          Abort "$(webview2AbortError)"
        ${EndIf}
      webview2_done:
    ${EndIf}
  ${Else}
    !if "${MINIMUMWEBVIEW2VERSION}" != ""
      ${VersionCompare} "${MINIMUMWEBVIEW2VERSION}" "$4" $R0
      ${If} $R0 = 1
        update_webview:
          DetailPrint "$(installingWebview2)"
          ${If} ${RunningX64}
            ReadRegStr $R1 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate" "path"
          ${Else}
            ReadRegStr $R1 HKLM "SOFTWARE\Microsoft\EdgeUpdate" "path"
          ${EndIf}
          ${If} $R1 == ""
            ReadRegStr $R1 HKCU "SOFTWARE\Microsoft\EdgeUpdate" "path"
          ${EndIf}
          ${If} $R1 != ""
            ; Chromium updater docs: https://source.chromium.org/chromium/chromium/src/+/main:docs/updater/user_manual.md
            ; Modified from "HKEY_LOCAL_MACHINE\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\Microsoft EdgeWebView\ModifyPath"
            ExecWait `"$R1" /install appguid=${WEBVIEW2APPGUID}&needsadmin=true` $1
            ${If} $1 = 0
              DetailPrint "$(webview2InstallSuccess)"
            ${Else}
              MessageBox MB_ICONEXCLAMATION|MB_ABORTRETRYIGNORE "$(webview2InstallError)" IDIGNORE ignore IDRETRY update_webview
              Quit
              ignore:
            ${EndIf}
          ${EndIf}
      ${EndIf}
    !endif
  ${EndIf}
SectionEnd

Section Install
  SetOutPath $INSTDIR

  !ifmacrodef NSIS_HOOK_PREINSTALL
    !insertmacro NSIS_HOOK_PREINSTALL
  !endif

  !insertmacro CheckIfAppIsRunning "$INSTDIR\${MAINBINARYNAME}.exe" "${PRODUCTNAME}"

  ; Copy main executable
  File "${MAINBINARYSRCPATH}"

  ; Maktaba (13.14): the app is in; the server's payload next (the resources), when it has one
  !insertmacro MK_STEP $MkStep1 $MkStep1Text 2 "$(mkNowApp)"
  !ifdef MK_SERVER
    !insertmacro MK_STEP $MkStep2 $MkStep2Text 1 "$(mkNowServer)"
  !endif

  ; Copy resources
  {{#each resources_dirs}}
    CreateDirectory "$INSTDIR\\{{this}}"
  {{/each}}
  {{#each resources}}
    File /a "/oname={{this.[1]}}" "{{no-escape @key}}"
  {{/each}}

  ; Copy external binaries
  {{#each binaries}}
    File /a "/oname={{this}}" "{{no-escape @key}}"
  {{/each}}

  ; Create file associations
  {{#each file_associations as |association| ~}}
    {{#each association.ext as |ext| ~}}
       !insertmacro APP_ASSOCIATE "{{ext}}" "{{or association.name ext}}" "{{association-description association.description ext}}" "$INSTDIR\${MAINBINARYNAME}.exe,0" "Open with ${PRODUCTNAME}" "$INSTDIR\${MAINBINARYNAME}.exe $\"%1$\""
    {{/each}}
  {{/each}}

  ; Register deep links
  {{#each deep_link_protocols as |protocol| ~}}
    WriteRegStr SHCTX "Software\Classes\\{{protocol}}" "URL Protocol" ""
    WriteRegStr SHCTX "Software\Classes\\{{protocol}}" "" "URL:${BUNDLEID} protocol"
    WriteRegStr SHCTX "Software\Classes\\{{protocol}}\DefaultIcon" "" "$\"$INSTDIR\${MAINBINARYNAME}.exe$\",0"
    WriteRegStr SHCTX "Software\Classes\\{{protocol}}\shell\open\command" "" "$\"$INSTDIR\${MAINBINARYNAME}.exe$\" $\"%1$\""
  {{/each}}

  ; Maktaba (13.14): the payload is in; the uninstaller, the registration and the shortcuts
  !ifdef MK_SERVER
    !insertmacro MK_STEP $MkStep2 $MkStep2Text 2 "$(mkNowServer)"
  !endif
  !insertmacro MK_STEP $MkStep3 $MkStep3Text 1 "$(mkNowFinish)"

  ; Create uninstaller
  WriteUninstaller "$INSTDIR\uninstall.exe"

  ; Save $INSTDIR in registry for future installations
  WriteRegStr SHCTX "${MANUPRODUCTKEY}" "" $INSTDIR

  !if "${INSTALLMODE}" == "both"
    ; Save install mode to be selected by default for the next installation such as updating
    ; or when uninstalling
    WriteRegStr SHCTX "${UNINSTKEY}" $MultiUser.InstallMode 1
  !endif

  ; Remove old main binary if it doesn't match new main binary name
  ReadRegStr $OldMainBinaryName SHCTX "${UNINSTKEY}" "MainBinaryName"
  ${If} $OldMainBinaryName != ""
  ${AndIf} $OldMainBinaryName != "${MAINBINARYNAME}.exe"
    Delete "$INSTDIR\$OldMainBinaryName"
  ${EndIf}

  ; Save current MAINBINARYNAME for future updates
  WriteRegStr SHCTX "${UNINSTKEY}" "MainBinaryName" "${MAINBINARYNAME}.exe"

  ; Registry information for add/remove programs
  WriteRegStr SHCTX "${UNINSTKEY}" "DisplayName" "${PRODUCTNAME}"
  WriteRegStr SHCTX "${UNINSTKEY}" "DisplayIcon" "$\"$INSTDIR\${MAINBINARYNAME}.exe$\""
  WriteRegStr SHCTX "${UNINSTKEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr SHCTX "${UNINSTKEY}" "Publisher" "${MANUFACTURER}"
  WriteRegStr SHCTX "${UNINSTKEY}" "InstallLocation" "$\"$INSTDIR$\""
  WriteRegStr SHCTX "${UNINSTKEY}" "UninstallString" "$\"$INSTDIR\uninstall.exe$\""
  WriteRegDWORD SHCTX "${UNINSTKEY}" "NoModify" "1"
  WriteRegDWORD SHCTX "${UNINSTKEY}" "NoRepair" "1"

  ${GetSize} "$INSTDIR" "/M=uninstall.exe /S=0K /G=0" $0 $1 $2
  IntOp $0 $0 + ${ESTIMATEDSIZE}
  IntFmt $0 "0x%08X" $0
  WriteRegDWORD SHCTX "${UNINSTKEY}" "EstimatedSize" "$0"

  !if "${HOMEPAGE}" != ""
    WriteRegStr SHCTX "${UNINSTKEY}" "URLInfoAbout" "${HOMEPAGE}"
    WriteRegStr SHCTX "${UNINSTKEY}" "URLUpdateInfo" "${HOMEPAGE}"
    WriteRegStr SHCTX "${UNINSTKEY}" "HelpLink" "${HOMEPAGE}"
  !endif

  ; Create start menu shortcut
  !insertmacro MUI_STARTMENU_WRITE_BEGIN Application
    Call CreateOrUpdateStartMenuShortcut
  !insertmacro MUI_STARTMENU_WRITE_END

  ; Create desktop shortcut for silent and passive installers
  ; because finish page will be skipped
  ${If} $PassiveMode = 1
  ${OrIf} ${Silent}
    Call CreateOrUpdateDesktopShortcut
  ; Maktaba (13.14): a person at the window asked for one on the welcome page
  ${ElseIf} $MkDesktop = ${BST_CHECKED}
    Call CreateOrUpdateDesktopShortcut
  ${EndIf}

  !ifmacrodef NSIS_HOOK_POSTINSTALL
    !insertmacro NSIS_HOOK_POSTINSTALL
  !endif

  ; Maktaba (13.14): done; whether to open Maktaba (read here: the window closes next)
  !insertmacro MK_STEP $MkStep3 $MkStep3Text 2 "$(mkNowDone)"
  ${If} $MkOpen != ""
    SendMessage $MkOpen ${BM_GETCHECK} 0 0 $MkOpenAfter
  ${EndIf}

  ; Auto close this page for passive mode
  ${If} $PassiveMode = 1
    SetAutoClose true
  ${EndIf}
  ; Maktaba (13.14): and for a person, there being no finish page (a failure keeps it open)
  SetAutoClose true
SectionEnd

Function .onInstSuccess
  ; Check for `/R` flag only in silent and passive installers because
  ; GUI installer has a toggle for the user to (re)start the app
  ${If} $PassiveMode = 1
  ${OrIf} ${Silent}
    ${GetOptions} $CMDLINE "/R" $R0
    ${IfNot} ${Errors}
      ${GetOptions} $CMDLINE "/ARGS" $R0
      nsis_tauri_utils::RunAsUser "$INSTDIR\${MAINBINARYNAME}.exe" "$R0"
    ${EndIf}
  ; Maktaba (13.14): the installing page's "Open Maktaba when it's done"
  ${ElseIf} $MkOpenAfter = ${BST_CHECKED}
    Call RunMainBinary
  ${EndIf}
FunctionEnd

Function un.onInit
  !insertmacro SetContext

  !if "${INSTALLMODE}" == "both"
    !insertmacro MULTIUSER_UNINIT
  !endif

  !insertmacro MUI_UNGETLANGUAGE

  ${GetOptions} $CMDLINE "/P" $PassiveMode
  ${IfNot} ${Errors}
    StrCpy $PassiveMode 1
  ${EndIf}

  ${GetOptions} $CMDLINE "/UPDATE" $UpdateMode
  ${IfNot} ${Errors}
    StrCpy $UpdateMode 1
  ${EndIf}
FunctionEnd

Section Uninstall

  !ifmacrodef NSIS_HOOK_PREUNINSTALL
    !insertmacro NSIS_HOOK_PREUNINSTALL
  !endif

  !insertmacro CheckIfAppIsRunning "$INSTDIR\${MAINBINARYNAME}.exe" "${PRODUCTNAME}"

  ; Delete the app directory and its content from disk
  ; Copy main executable
  Delete "$INSTDIR\${MAINBINARYNAME}.exe"

  ; Delete resources
  {{#each resources}}
    Delete "$INSTDIR\\{{this.[1]}}"
  {{/each}}

  ; Delete external binaries
  {{#each binaries}}
    Delete "$INSTDIR\\{{this}}"
  {{/each}}

  ; Delete app associations
  {{#each file_associations as |association| ~}}
    {{#each association.ext as |ext| ~}}
      !insertmacro APP_UNASSOCIATE "{{ext}}" "{{or association.name ext}}"
    {{/each}}
  {{/each}}

  ; Delete deep links
  {{#each deep_link_protocols as |protocol| ~}}
    ReadRegStr $R7 SHCTX "Software\Classes\\{{protocol}}\shell\open\command" ""
    ${If} $R7 == "$\"$INSTDIR\${MAINBINARYNAME}.exe$\" $\"%1$\""
      DeleteRegKey SHCTX "Software\Classes\\{{protocol}}"
    ${EndIf}
  {{/each}}


  ; Delete uninstaller
  Delete "$INSTDIR\uninstall.exe"

  {{#each resources_ancestors}}
  RMDir /REBOOTOK "$INSTDIR\\{{this}}"
  {{/each}}
  RMDir "$INSTDIR"

  ; Remove shortcuts if not updating
  ${If} $UpdateMode <> 1
    !insertmacro DeleteAppUserModelId

    ; Remove start menu shortcut
    !insertmacro MUI_STARTMENU_GETFOLDER Application $AppStartMenuFolder
    !insertmacro IsShortcutTarget "$SMPROGRAMS\$AppStartMenuFolder\${PRODUCTNAME}.lnk" "$INSTDIR\${MAINBINARYNAME}.exe"
    Pop $0
    ${If} $0 = 1
      !insertmacro UnpinShortcut "$SMPROGRAMS\$AppStartMenuFolder\${PRODUCTNAME}.lnk"
      Delete "$SMPROGRAMS\$AppStartMenuFolder\${PRODUCTNAME}.lnk"
      RMDir "$SMPROGRAMS\$AppStartMenuFolder"
    ${EndIf}
    !insertmacro IsShortcutTarget "$SMPROGRAMS\${PRODUCTNAME}.lnk" "$INSTDIR\${MAINBINARYNAME}.exe"
    Pop $0
    ${If} $0 = 1
      !insertmacro UnpinShortcut "$SMPROGRAMS\${PRODUCTNAME}.lnk"
      Delete "$SMPROGRAMS\${PRODUCTNAME}.lnk"
    ${EndIf}

    ; Remove desktop shortcuts
    !insertmacro IsShortcutTarget "$DESKTOP\${PRODUCTNAME}.lnk" "$INSTDIR\${MAINBINARYNAME}.exe"
    Pop $0
    ${If} $0 = 1
      !insertmacro UnpinShortcut "$DESKTOP\${PRODUCTNAME}.lnk"
      Delete "$DESKTOP\${PRODUCTNAME}.lnk"
    ${EndIf}
  ${EndIf}

  ; Remove registry information for add/remove programs
  !if "${INSTALLMODE}" == "both"
    DeleteRegKey SHCTX "${UNINSTKEY}"
  !else if "${INSTALLMODE}" == "perMachine"
    DeleteRegKey HKLM "${UNINSTKEY}"
  !else
    DeleteRegKey HKCU "${UNINSTKEY}"
  !endif

  ; Removes the Autostart entry for ${PRODUCTNAME} from the HKCU Run key if it exists.
  ; This ensures the program does not launch automatically after uninstallation if it exists.
  ; If it doesn't exist, it does nothing.
  ; We do this when not updating (to preserve the registry value on updates)
  ${If} $UpdateMode <> 1
    DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "${PRODUCTNAME}"
  ${EndIf}

  ; Delete app data if the checkbox is selected
  ; and if not updating
  ${If} $DeleteAppDataCheckboxState = 1
  ${AndIf} $UpdateMode <> 1
    ; Clear the install location $INSTDIR from registry
    DeleteRegKey SHCTX "${MANUPRODUCTKEY}"
    DeleteRegKey /ifempty SHCTX "${MANUKEY}"

    ; Clear the install language from registry
    DeleteRegValue HKCU "${MANUPRODUCTKEY}" "Installer Language"
    DeleteRegKey /ifempty HKCU "${MANUPRODUCTKEY}"
    DeleteRegKey /ifempty HKCU "${MANUKEY}"

    SetShellVarContext current
    RmDir /r "$APPDATA\${BUNDLEID}"
    RmDir /r "$LOCALAPPDATA\${BUNDLEID}"
  ${EndIf}

  !ifmacrodef NSIS_HOOK_POSTUNINSTALL
    !insertmacro NSIS_HOOK_POSTUNINSTALL
  !endif

  ; Auto close if passive mode or updating
  ${If} $PassiveMode = 1
  ${OrIf} $UpdateMode = 1
    SetAutoClose true
  ${EndIf}
SectionEnd

Function RestorePreviousInstallLocation
  ReadRegStr $4 SHCTX "${MANUPRODUCTKEY}" ""
  StrCmp $4 "" +2 0
    StrCpy $INSTDIR $4
FunctionEnd

Function Skip
  Abort
FunctionEnd

Function SkipIfPassive
  ${IfThen} $PassiveMode = 1  ${|} Abort ${|}
FunctionEnd
Function un.SkipIfPassive
  ${IfThen} $PassiveMode = 1  ${|} Abort ${|}
FunctionEnd

Function CreateOrUpdateStartMenuShortcut
  ; We used to use product name as MAINBINARYNAME
  ; migrate old shortcuts to target the new MAINBINARYNAME
  StrCpy $R0 0

  !insertmacro IsShortcutTarget "$SMPROGRAMS\$AppStartMenuFolder\${PRODUCTNAME}.lnk" "$INSTDIR\$OldMainBinaryName"
  Pop $0
  ${If} $0 = 1
    !insertmacro SetShortcutTarget "$SMPROGRAMS\$AppStartMenuFolder\${PRODUCTNAME}.lnk" "$INSTDIR\${MAINBINARYNAME}.exe"
    StrCpy $R0 1
  ${EndIf}

  !insertmacro IsShortcutTarget "$SMPROGRAMS\${PRODUCTNAME}.lnk" "$INSTDIR\$OldMainBinaryName"
  Pop $0
  ${If} $0 = 1
    !insertmacro SetShortcutTarget "$SMPROGRAMS\${PRODUCTNAME}.lnk" "$INSTDIR\${MAINBINARYNAME}.exe"
    StrCpy $R0 1
  ${EndIf}

  ${If} $R0 = 1
    Return
  ${EndIf}

  ; Maktaba (13.14): the welcome page's Start menu box was cleared (in every mode)
  ${If} $MkStartMenu <> ${BST_CHECKED}
    Return
  ${EndIf}

  ; Skip creating shortcut if in update mode or no shortcut mode
  ; but always create if migrating from wix
  ${If} $WixMode = 0
    ${If} $UpdateMode = 1
    ${OrIf} $NoShortcutMode = 1
      Return
    ${EndIf}
  ${EndIf}

  !if "${STARTMENUFOLDER}" != ""
    CreateDirectory "$SMPROGRAMS\$AppStartMenuFolder"
    CreateShortcut "$SMPROGRAMS\$AppStartMenuFolder\${PRODUCTNAME}.lnk" "$INSTDIR\${MAINBINARYNAME}.exe"
    !insertmacro SetLnkAppUserModelId "$SMPROGRAMS\$AppStartMenuFolder\${PRODUCTNAME}.lnk"
  !else
    CreateShortcut "$SMPROGRAMS\${PRODUCTNAME}.lnk" "$INSTDIR\${MAINBINARYNAME}.exe"
    !insertmacro SetLnkAppUserModelId "$SMPROGRAMS\${PRODUCTNAME}.lnk"
  !endif
FunctionEnd

Function CreateOrUpdateDesktopShortcut
  ; We used to use product name as MAINBINARYNAME
  ; migrate old shortcuts to target the new MAINBINARYNAME
  !insertmacro IsShortcutTarget "$DESKTOP\${PRODUCTNAME}.lnk" "$INSTDIR\$OldMainBinaryName"
  Pop $0
  ${If} $0 = 1
    !insertmacro SetShortcutTarget "$DESKTOP\${PRODUCTNAME}.lnk" "$INSTDIR\${MAINBINARYNAME}.exe"
    Return
  ${EndIf}

  ; Skip creating shortcut if in update mode or no shortcut mode
  ; but always create if migrating from wix
  ${If} $WixMode = 0
    ${If} $UpdateMode = 1
    ${OrIf} $NoShortcutMode = 1
      Return
    ${EndIf}
  ${EndIf}

  CreateShortcut "$DESKTOP\${PRODUCTNAME}.lnk" "$INSTDIR\${MAINBINARYNAME}.exe"
  !insertmacro SetLnkAppUserModelId "$DESKTOP\${PRODUCTNAME}.lnk"
FunctionEnd
