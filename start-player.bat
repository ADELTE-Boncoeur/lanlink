@echo off
title LANLink player
cd /d "%~dp0"
if exist "%~dp0LANLink.exe" (
  "%~dp0LANLink.exe" --version
  echo (If the line above shows no version, re-download LANLink.exe from GitHub - your copy is outdated.)
) else (
  echo LANLink.exe not found here - will use Python instead.
)
set /p NAME="Your name (e.g. Karim): "
set /p ROOM="Room code - same for all friends (e.g. HALO-42): "
set /p SIGNAL="Signaling server - Enter for same-WiFi, or http://HOST-IP:32440 : "
set /p SERVE="Hosting a game? Type its name so friends SEE it (e.g. CoD4 Shipment) or just press Enter: "
echo.
if exist "%~dp0LANLink.exe" (
  echo Starting LANLink.exe (no Python needed) - your page opens in 8 seconds...
  start "LANLink app - close me to quit" "%~dp0LANLink.exe" --name "%NAME%" --room "%ROOM%" --signal "%SIGNAL%" --serve "%SERVE%"
  timeout /t 8 /nobreak >nul
  start http://127.0.0.1:32441
  echo If the page says "can't be reached", read the black LANLink window for the error,
  echo then close it and tell us what it says.
) else (
  echo LANLink.exe not found - using Python instead.
  echo Join the page that opens:  http://127.0.0.1:32441
  start http://127.0.0.1:32441
  if "%SIGNAL%"=="" (
    python py\lanlink.py --name "%NAME%" --room "%ROOM%" --serve "%SERVE%"
  ) else (
    python py\lanlink.py --name "%NAME%" --room "%ROOM%" --signal "%SIGNAL%" --serve "%SERVE%"
  )
)
pause
