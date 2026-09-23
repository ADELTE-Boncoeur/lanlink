@echo off
title LANLink player
cd /d "%~dp0"
set /p NAME="Your name (e.g. Karim): "
set /p ROOM="Room code - same for all friends (e.g. HALO-42): "
set /p SIGNAL="Signaling server - Enter for same-WiFi, or http://HOST-IP:32440 : "
echo.
echo Join the page that opens:  http://127.0.0.1:32441
start http://127.0.0.1:32441
if exist "%~dp0LANLink.exe" (
  echo Using LANLink.exe - no Python needed.
  if "%SIGNAL%"=="" (
    "%~dp0LANLink.exe" --name "%NAME%" --room "%ROOM%"
  ) else (
    "%~dp0LANLink.exe" --name "%NAME%" --room "%ROOM%" --signal "%SIGNAL%"
  )
) else (
  echo LANLink.exe not found - using Python instead.
  if "%SIGNAL%"=="" (
    python py\lanlink.py --name "%NAME%" --room "%ROOM%"
  ) else (
    python py\lanlink.py --name "%NAME%" --room "%ROOM%" --signal "%SIGNAL%"
  )
)
pause
