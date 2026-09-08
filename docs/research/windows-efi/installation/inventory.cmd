@echo off
for %%D in (C D E F G H I J K L M N O P Q R S T U V W Y Z) do if exist %%D:\COVEWINPE.TAG set media=%%D:
if not defined media exit /b 1
echo COVE_WINPE_INVENTORY>%media%\INVENTORY.TXT
ver>>%media%\INVENTORY.TXT
wpeinit
echo wpeinit=%errorlevel% >>%media%\INVENTORY.TXT
drvload %media%\netkvm\netkvm.inf >>%media%\INVENTORY.TXT 2>&1
echo drvload=%errorlevel% >>%media%\INVENTORY.TXT
wpeutil InitializeNetwork >>%media%\INVENTORY.TXT 2>&1
echo network=%errorlevel% >>%media%\INVENTORY.TXT
ipconfig /all >>%media%\INVENTORY.TXT 2>&1
diskpart /s %media%\disks.txt >>%media%\INVENTORY.TXT 2>&1
pnputil /enum-devices /class DiskDrive >>%media%\INVENTORY.TXT 2>&1
start "" %media%\screen.exe %media%\
if exist %media%\INSTALL.TAG call %media%\install.cmd
cmd.exe
