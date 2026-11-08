@echo off
rem The mediaintel control centre on its own: stand-alone, outside Maktaba and any editor
rem session, started again after a crash. Runs are started from its page (Start a run).
rem   set MEDIAINTEL_DB=E:\tv\mediaintel\mediaintel.db   (required; settings.json sits beside it)
rem   set MEDIAINTEL_PLEX_URL=http://localhost:32400      (optional: Plex, for runs started here)
rem   set MEDIAINTEL_PLEX_TOKEN=...                       (optional: with MEDIAINTEL_PLEX_URL)
rem   set MEDIAINTEL_PORT=8770                            (optional)
rem   control-centre.cmd
setlocal
if "%MEDIAINTEL_DB%"=="" (echo Set MEDIAINTEL_DB to the cache database first. & exit /b 2)
if "%MEDIAINTEL_PORT%"=="" set MEDIAINTEL_PORT=8770
for %%d in ("%MEDIAINTEL_DB%") do set HOME_DIR=%%~dpd
set LOG=%HOME_DIR%run-plex.log
set REPORTS=%HOME_DIR%reports
set PLEX=
if not "%MEDIAINTEL_PLEX_URL%"=="" set PLEX=--plex-url "%MEDIAINTEL_PLEX_URL%" --plex-token "%MEDIAINTEL_PLEX_TOKEN%"
set PYTHONIOENCODING=utf-8
cd /d "%~dp0..\.."
:run
uv run --extra mediaintel python -m scripts.mediaintel --db "%MEDIAINTEL_DB%" serve --log "%LOG%" --port %MEDIAINTEL_PORT% %PLEX% --reports "%REPORTS%" > "%HOME_DIR%control-centre.log" 2>&1
echo %date% %time% control centre stopped (exit %errorlevel%), starting again >> "%HOME_DIR%control-centre-restarts.log"
timeout /t 5 /nobreak >nul
goto run
