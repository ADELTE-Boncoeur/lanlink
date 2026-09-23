@echo off
title LANLink - meeting point (signaling server)
echo ============================================================
echo  LANLink meeting point - leave this window OPEN while you play.
echo  Internet players connect to:  http://YOUR-IP:32440
echo  (find YOUR-IP with:  ipconfig  ^| findstr IPv4)
echo ============================================================
if exist "%~dp0LANLink-Server.exe" (
  echo Using LANLink-Server.exe - no Python needed.
  "%~dp0LANLink-Server.exe" --port 32440
) else (
  echo LANLink-Server.exe not found - using Python instead.
  python py\signaling.py --port 32440
)
pause
