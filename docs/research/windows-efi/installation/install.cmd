@echo off
if not defined media exit /b 1
call :install >%media%\INSTALL.LOG 2>&1
echo install=%errorlevel% >>%media%\INSTALL.LOG
exit /b
:install
echo INSTALL_BEGIN %DATE% %TIME%
%media%\diskcheck-test.exe -test.v
if errorlevel 1 exit /b 1
%media%\diskcheck.exe
if errorlevel 1 exit /b 1
diskpart /s %media%\partition.txt
if errorlevel 1 exit /b 1
if not exist W:\ exit /b 1
if not exist S:\ exit /b 1
dism /Apply-Image /ImageFile:%media%\sources\install.swm /SWMFile:%media%\sources\install*.swm /Index:1 /ApplyDir:W:\ /CheckIntegrity
if errorlevel 1 exit /b 1
dism /Image:W:\ /Add-Driver /Driver:%media%\netkvm\netkvm.inf
if errorlevel 1 exit /b 1
W:\Windows\System32\bcdboot W:\Windows /s S: /f UEFI
if errorlevel 1 exit /b 1
copy /y %media%\EFI\BOOT\BOOTAA64.EFI S:\EFI\BOOT\BOOTAA64.EFI
if errorlevel 1 exit /b 1
if not exist S:\EFI\Microsoft\Boot\bootmgfw.efi exit /b 1
mkdir W:\Cove
copy /y %media%\screen.exe W:\Cove\screen.exe
if errorlevel 1 exit /b 1
copy /y %media%\PUSH.URL W:\Cove\PUSH.URL
if errorlevel 1 exit /b 1
mkdir W:\Windows\Panther
copy /y %media%\unattend.xml W:\Windows\Panther\unattend.xml
if errorlevel 1 exit /b 1
copy /y %media%\desktop.cmd "W:\ProgramData\Microsoft\Windows\Start Menu\Programs\Startup\cove.cmd"
if errorlevel 1 exit /b 1
mkdir R:\Recovery\WindowsRE
copy /y W:\Windows\System32\Recovery\winre.wim R:\Recovery\WindowsRE\winre.wim
W:\Windows\System32\reagentc /setreimage /path R:\Recovery\WindowsRE /target W:\Windows
echo recovery=%errorlevel%
echo INSTALL_APPLIED %DATE% %TIME%
echo INSTALL_APPLIED>%media%\APPLIED.TAG
wpeutil shutdown
exit /b 0
