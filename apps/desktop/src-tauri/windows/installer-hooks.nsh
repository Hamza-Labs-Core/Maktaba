; The Windows app's NSIS installer hooks (Epic 38, story 38.11; contract-agent.md, "Installer
; hooks"). tools/release/win-app/payload.sh points bundle.windows.nsis.installerHooks at this
; file in the overlay the server app is built with; the client-only build has no hooks.
;
; Each hook asks the installed app (Maktaba.exe, the same program the sign-in task runs as
; --server-agent) to act on the server, and waits for it. Their exit codes are not checked:
; a server that cannot be stopped is reported by the app (server.log), and the install goes
; on (the restart manager check that follows closes Maktaba.exe itself).
;
; Only a Maktaba.exe that takes the --server-* flags (story 38.3) is asked. One from before
; them (the client-only app, which installs to the same %LOCALAPPDATA%\Maktaba under the same
; name) opens its window for any argument and never exits, so the install would wait until
; someone closed it. Which Maktaba.exe takes them:
;   - the one this installer packs: decided when the installer is compiled, from the shell's
;     source in this tree (knows-server-flags.ps1: the flags' literals in apps\desktop\
;     src-tauri\src), which the same `tauri build` compiled into that Maktaba.exe. Not from
;     the program itself: a release build need not hold the flags' text (LLVM compiles the
;     comparison with "--server-remove" into a length and integer compares).
;     tools/release/win-app/check.sh runs the installed Maktaba.exe with a flag and fails
;     when what it does disagrees with this;
;   - the one already installed (before an install, an update, an uninstall): the installer
;     that put a flag-taking Maktaba.exe there recorded its size and time in the uninstall
;     key (MaktabaServerFlags). A client-only install never has that value; one whose
;     Maktaba.exe was replaced since (by a client-only installer) no longer matches it. The
;     uninstaller deletes the key, so the record goes with the install.
;
; The per-user state shares $INSTDIR with the program (%LOCALAPPDATA%\Maktaba: server.toml,
; keys\, data\, postgres\, venv\, ...): nothing here deletes it, and the uninstaller removes
; only the files the installer wrote.

!define MAKTABA_HOOKS_DIR "${__FILEDIR__}"
; the shell's source the decision reads (test-hooks.sh compiles the hooks with its own)
!ifndef MAKTABA_SHELL_SRC
  !define MAKTABA_SHELL_SRC "${MAKTABA_HOOKS_DIR}\..\src"
!endif
!define MAKTABA_FLAGS_VALUE "MaktabaServerFlags"

; MAKTABA_STAMP: $R9 = "<size> <time>" of the installed Maktaba.exe, "" when there is none
!macro MAKTABA_STAMP
  Push $0
  Push $1
  Push $2
  StrCpy $R9 ""
  ClearErrors
  FileOpen $0 "$INSTDIR\${MAINBINARYNAME}.exe" r
  ${IfNot} ${Errors}
    FileSeek $0 0 END $1
    FileClose $0
    GetFileTime "$INSTDIR\${MAINBINARYNAME}.exe" $0 $2
    ${IfNot} ${Errors}
      StrCpy $R9 "$1 $0.$2"
    ${EndIf}
  ${EndIf}
  Pop $2
  Pop $1
  Pop $0
!macroend

; MAKTABA_INSTALLED_TAKES_FLAGS: $R8 = 1 when the installed Maktaba.exe is the flag-taking
; one an installer recorded, else 0 ($R9 is used too)
!macro MAKTABA_INSTALLED_TAKES_FLAGS
  !insertmacro MAKTABA_STAMP
  ReadRegStr $R8 SHCTX "${UNINSTKEY}" "${MAKTABA_FLAGS_VALUE}"
  ${If} $R9 != ""
  ${AndIf} $R8 == $R9
    StrCpy $R8 1
  ${Else}
    StrCpy $R8 0
  ${EndIf}
!macroend

!macro NSIS_HOOK_PREINSTALL
  ; Every file of the payload is in use while the server runs (server\bin, postgres, python,
  ; ffmpeg): stop it before they are replaced. A first install has no Maktaba.exe yet.
  Push $R8
  Push $R9
  !insertmacro MAKTABA_INSTALLED_TAKES_FLAGS
  ${If} $R8 = 1
    ExecWait '"$INSTDIR\${MAINBINARYNAME}.exe" --server-stop'
    ; server\ is wholly the payload (the state is beside it: server.toml, keys\, data\,
    ; postgres\, venv\, logs\), and an update (/UPDATE: the app's own updater) installs over
    ; the old files without uninstalling them: a file the new payload renamed or dropped (a
    ; migration, an Ollama or Python library) would stay and be found. The whole old payload
    ; goes, once its server stopped; the new one is copied in after this. Only a payload of
    ; ours (its payload.json there).
    ${If} ${FileExists} "$INSTDIR\server\payload.json"
      RMDir /r "$INSTDIR\server"
    ${EndIf}
  ${EndIf}
  Pop $R9
  Pop $R8
  ; The pipeline wheel's name carries its version (Epic 35 review round 2, B-H1): a payload
  ; the removal above left (no payload.json, a server not stopped) loses at least its old
  ; wheel, as the server builds the pipeline's venv from exactly one.
  Delete "$INSTDIR\server\pipeline\maktaba_pipeline-*.whl"
!macroend

!macro NSIS_HOOK_POSTINSTALL
  ; Does the Maktaba.exe this installer packs take the flags? (once, when compiling: from the
  ; shell's source it was built from)
  !system 'powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "${MAKTABA_HOOKS_DIR}\knows-server-flags.ps1" -Source "${MAKTABA_SHELL_SRC}"' MAKTABA_FLAGS_RC
  !if ${MAKTABA_FLAGS_RC} = 0
    ; Record it for the next installer and the uninstaller, then start the server from the
    ; new payload: runs the sign-in task when it is registered and the server is not paused
    ; (a first install does nothing: setup registers the task).
    Push $R9
    !insertmacro MAKTABA_STAMP
    WriteRegStr SHCTX "${UNINSTKEY}" "${MAKTABA_FLAGS_VALUE}" $R9
    Pop $R9
    ExecWait '"$INSTDIR\${MAINBINARYNAME}.exe" --server-start'
  !else if ${MAKTABA_FLAGS_RC} = 1
    ; a Maktaba.exe from before the flags: nothing to ask, and no record (an earlier one
    ; described another program)
    DeleteRegValue SHCTX "${UNINSTKEY}" "${MAKTABA_FLAGS_VALUE}"
  !else
    !error "installer-hooks.nsh: cannot tell whether the shell in ${MAKTABA_SHELL_SRC} takes the --server-* flags (knows-server-flags.ps1 exit ${MAKTABA_FLAGS_RC})"
  !endif
!macroend

!macro NSIS_HOOK_PREUNINSTALL
  ; Not when an update runs the old uninstaller (/UPDATE): the new installer's hooks stop and
  ; start the server around it. A real uninstall stops the server and removes the sign-in
  ; task, so no task is left pointing at a removed program.
  ${If} $UpdateMode <> 1
    Push $R8
    Push $R9
    !insertmacro MAKTABA_INSTALLED_TAKES_FLAGS
    ${If} $R8 = 1
      ExecWait '"$INSTDIR\${MAINBINARYNAME}.exe" --server-stop'
      ExecWait '"$INSTDIR\${MAINBINARYNAME}.exe" --server-remove'
    ${EndIf}
    Pop $R9
    Pop $R8
  ${EndIf}
!macroend
