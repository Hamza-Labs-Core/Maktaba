; The Windows app's NSIS installer hooks (Epic 38, story 38.11; contract-agent.md, "Installer
; hooks"). tools/release/win-app/payload.sh points bundle.windows.nsis.installerHooks at this
; file in the overlay the server app is built with; the client-only build has no hooks.
;
; Each hook asks the installed app (Maktaba.exe, the same program the sign-in task runs as
; --server-agent) to act on the server, and waits for it. Their exit codes are not checked:
; a server that cannot be stopped is reported by the app (server.log), and the install goes
; on (the restart manager check that follows closes Maktaba.exe itself).
;
; The per-user state shares $INSTDIR with the program (%LOCALAPPDATA%\Maktaba: server.toml,
; keys\, data\, postgres\, venv\, ...): nothing here deletes it, and the uninstaller removes
; only the files the installer wrote.

!macro NSIS_HOOK_PREINSTALL
  ; Every file of the payload is in use while the server runs (server\bin, postgres, python,
  ; ffmpeg): stop it before they are replaced. A first install has no Maktaba.exe yet.
  ${If} ${FileExists} "$INSTDIR\${MAINBINARYNAME}.exe"
    ExecWait '"$INSTDIR\${MAINBINARYNAME}.exe" --server-stop'
  ${EndIf}
!macroend

!macro NSIS_HOOK_POSTINSTALL
  ; Start the server from the new payload: runs the sign-in task when it is registered and
  ; the server is not paused (a first install does nothing: setup registers the task).
  ExecWait '"$INSTDIR\${MAINBINARYNAME}.exe" --server-start'
!macroend

!macro NSIS_HOOK_PREUNINSTALL
  ; Not when an update runs the old uninstaller (/UPDATE): the new installer's hooks stop and
  ; start the server around it. A real uninstall stops the server and removes the sign-in
  ; task, so no task is left pointing at a removed program.
  ${If} $UpdateMode <> 1
  ${AndIf} ${FileExists} "$INSTDIR\${MAINBINARYNAME}.exe"
    ExecWait '"$INSTDIR\${MAINBINARYNAME}.exe" --server-stop'
    ExecWait '"$INSTDIR\${MAINBINARYNAME}.exe" --server-remove'
  ${EndIf}
!macroend
