@echo off
set phase=before-wpeinit
call :receipt
wpeinit
set phase=after-wpeinit
call :receipt
X:\sources\setup.exe
exit /b
:receipt
for %%D in (C D E F G H I J K L M N O P Q R S T U V W Y Z) do if exist %%D:\COVEWINPE.TAG (
  echo COVE_WINPE_USERMODE_STARTED_20260907>>%%D:\WINPE-RECEIPT.TXT
  echo phase=%phase%>>%%D:\WINPE-RECEIPT.TXT
  ver>>%%D:\WINPE-RECEIPT.TXT
  echo systemroot=%SYSTEMROOT% architecture=%PROCESSOR_ARCHITECTURE%>>%%D:\WINPE-RECEIPT.TXT
  echo time=%DATE% %TIME%>>%%D:\WINPE-RECEIPT.TXT
  reg query HKLM\SYSTEM\CurrentControlSet\Control\MiniNT>>%%D:\WINPE-RECEIPT.TXT 2>&1
)
exit /b
