@echo off
title LANLink - meeting point (signaling server)
echo ============================================================
echo  LANLink meeting point - leave this window OPEN while you play.
echo  Internet players connect to:  http://YOUR-IP:32440
echo  (find YOUR-IP with:  ipconfig  ^| findstr IPv4)
echo ============================================================
python py\signaling.py --port 32440
pause
