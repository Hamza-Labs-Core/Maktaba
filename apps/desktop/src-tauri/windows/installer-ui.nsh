; Where Maktaba's -setup.exe finds its art (Epic 13 story 13.14): the template
; (installer.nsi, bundle.windows.nsis.template) is rendered and compiled in tauri's target
; folder, so it learns this folder from the hooks file, the one file tauri includes from here.
; tauri.conf.json names this file as the hooks of a build without the server; the server
; build's hooks (installer-hooks.nsh, payload.sh's overlay) include it.

!ifndef MAKTABA_ART
  !define MAKTABA_ART "${__FILEDIR__}\installer"
!endif
