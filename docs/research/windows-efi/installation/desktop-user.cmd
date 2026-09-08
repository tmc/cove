@echo off
timeout /t 5 /nobreak >nul
taskkill /im screen.exe /f >nul 2>&1
taskkill /im screen-v3.exe /f >nul 2>&1
start "" C:\CoveDisplayV4\screen-v4.exe C:\CoveDisplayV4
