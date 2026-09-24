@echo off
title LANLink - firewall + network fix (Admin, one time)
net session >nul 2>&1
if %errorlevel% neq 0 (
  echo This fix needs Administrator rights - approving the popup...
  powershell -Command "Start-Process '%~f0' -Verb RunAs"
  exit /b
)
echo [1/2] Setting networks to Private (allows PCs to discover each other)...
powershell -Command "Get-NetConnectionProfile | Set-NetConnectionProfile -NetworkCategory Private"
echo [2/2] Opening LANLink ports in Windows Firewall...
netsh advfirewall firewall delete rule name="LANLink" >nul 2>&1
netsh advfirewall firewall delete rule name="LANLink discovery" >nul 2>&1
netsh advfirewall firewall delete rule name="LANLink mesh" >nul 2>&1
netsh advfirewall firewall add rule name="LANLink" dir=in action=allow program="%~dp0LANLink.exe" enable=yes profile=private
netsh advfirewall firewall add rule name="LANLink discovery" dir=in action=allow protocol=UDP localport=32442 enable=yes profile=private
netsh advfirewall firewall add rule name="LANLink mesh" dir=in action=allow protocol=UDP localport=32443-32460 enable=yes profile=private
echo.
echo Done! Close this window and restart LANLink on all PCs.
echo Still invisible? Log into your router and turn AP/Client Isolation OFF.
pause
