package windows

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ProvisionConfig configures Windows unattended setup.
type ProvisionConfig struct {
	Username   string
	Password   string
	Hostname   string
	Locale     string
	TimeZone   string
	ProductKey string
	// AgentExecutable, when set, is copied to the autounattend ISO and
	// started by a Windows scheduled task on first logon.
	AgentExecutable string
	AgentTCPPort    int
	OOBEBypass      bool
	AutoLogon       bool
	LocalAdmin      bool
	MarkerPath      string
}

// Config is an alias for ProvisionConfig.
type Config = ProvisionConfig

// DefaultProvisionConfig returns conservative Windows 11 ARM64 setup defaults.
func DefaultProvisionConfig() ProvisionConfig {
	return ProvisionConfig{
		Username:   "cove",
		Password:   "Cove123!",
		Hostname:   "COVE-WIN11",
		Locale:     "en-US",
		TimeZone:   "UTC",
		ProductKey: "YTMG3-N6DKC-DKB77-7M9GH-8HVX7",
		OOBEBypass: true,
		AutoLogon:  true,
		LocalAdmin: true,
		MarkerPath: `C:\ProgramData\cove\provisioned`,
	}
}

// GenerateAutounattendXML returns an autounattend.xml document.
func GenerateAutounattendXML(config ProvisionConfig) string {
	config = config.withDefaults()

	accountGroup := "Users"
	if config.LocalAdmin {
		accountGroup = "Administrators"
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
  <settings pass="offlineServicing">
    <component name="Microsoft-Windows-PnpCustomizationsNonWinPE" processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <DriverPaths>
%[12]s
      </DriverPaths>
    </component>
  </settings>
  <settings pass="windowsPE">
    <component name="Microsoft-Windows-International-Core-WinPE" processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <SetupUILanguage>
        <UILanguage>%[1]s</UILanguage>
      </SetupUILanguage>
      <InputLocale>%[1]s</InputLocale>
      <SystemLocale>%[1]s</SystemLocale>
      <UILanguage>%[1]s</UILanguage>
      <UserLocale>%[1]s</UserLocale>
    </component>
    <component name="Microsoft-Windows-Setup" processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
%[11]s
      <DriverPaths>
        <PathAndCredentials wcm:action="add" wcm:keyValue="1">
          <Path>D:\</Path>
        </PathAndCredentials>
        <PathAndCredentials wcm:action="add" wcm:keyValue="2">
          <Path>E:\</Path>
        </PathAndCredentials>
        <PathAndCredentials wcm:action="add" wcm:keyValue="3">
          <Path>F:\</Path>
        </PathAndCredentials>
        <PathAndCredentials wcm:action="add" wcm:keyValue="4">
          <Path>D:\NetKVM\w11\ARM64</Path>
        </PathAndCredentials>
        <PathAndCredentials wcm:action="add" wcm:keyValue="5">
          <Path>E:\NetKVM\w11\ARM64</Path>
        </PathAndCredentials>
        <PathAndCredentials wcm:action="add" wcm:keyValue="6">
          <Path>F:\NetKVM\w11\ARM64</Path>
        </PathAndCredentials>
        <PathAndCredentials wcm:action="add" wcm:keyValue="7">
          <Path>G:\NetKVM\w11\ARM64</Path>
        </PathAndCredentials>
      </DriverPaths>
      <DiskConfiguration>
        <Disk wcm:action="add">
          <DiskID>0</DiskID>
          <WillWipeDisk>true</WillWipeDisk>
          <CreatePartitions>
            <CreatePartition wcm:action="add">
              <Order>1</Order>
              <Size>260</Size>
              <Type>EFI</Type>
            </CreatePartition>
            <CreatePartition wcm:action="add">
              <Order>2</Order>
              <Size>16</Size>
              <Type>MSR</Type>
            </CreatePartition>
            <CreatePartition wcm:action="add">
              <Order>3</Order>
              <Extend>true</Extend>
              <Type>Primary</Type>
            </CreatePartition>
          </CreatePartitions>
          <ModifyPartitions>
            <ModifyPartition wcm:action="add">
              <Order>1</Order>
              <PartitionID>1</PartitionID>
              <Format>FAT32</Format>
              <Label>EFI</Label>
            </ModifyPartition>
            <ModifyPartition wcm:action="add">
              <Order>2</Order>
              <PartitionID>3</PartitionID>
              <Format>NTFS</Format>
              <Label>Windows</Label>
            </ModifyPartition>
          </ModifyPartitions>
        </Disk>
      </DiskConfiguration>
      <ImageInstall>
        <OSImage>
          <InstallTo>
            <DiskID>0</DiskID>
            <PartitionID>3</PartitionID>
          </InstallTo>
        </OSImage>
      </ImageInstall>
      <UserData>
        <AcceptEula>true</AcceptEula>
        <FullName>%[2]s</FullName>
        <Organization>cove</Organization>
        <ProductKey>
          <Key>%[10]s</Key>
          <WillShowUI>Never</WillShowUI>
        </ProductKey>
      </UserData>
    </component>
  </settings>
  <settings pass="specialize">
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <ComputerName>%[4]s</ComputerName>
      <TimeZone>%[5]s</TimeZone>
    </component>
    <component name="Microsoft-Windows-Deployment" processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
%[13]s
    </component>
  </settings>
  <settings pass="oobeSystem">
    <component name="Microsoft-Windows-International-Core" processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <InputLocale>%[1]s</InputLocale>
      <SystemLocale>%[1]s</SystemLocale>
      <UILanguage>%[1]s</UILanguage>
      <UserLocale>%[1]s</UserLocale>
    </component>
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
%[6]s
      <UserAccounts>
        <LocalAccounts>
          <LocalAccount wcm:action="add">
            <Name>%[2]s</Name>
            <DisplayName>%[2]s</DisplayName>
            <Password>
              <Value>%[3]s</Value>
              <PlainText>true</PlainText>
            </Password>
            <Group>%[7]s</Group>
          </LocalAccount>
        </LocalAccounts>
      </UserAccounts>
%[8]s
%[9]s
    </component>
  </settings>
</unattend>
`, xmlText(config.Locale), xmlText(config.Username), xmlText(config.Password), xmlText(config.Hostname), xmlText(config.TimeZone), oobeXML(config), xmlText(accountGroup), autoLogonXML(config), firstLogonCommandsXML(config), xmlText(config.ProductKey), labConfigRunSynchronousXML(), offlineServicingDriverPathsXML(), specializeRunSynchronousXML())
}

// CreateAutounattendISO writes autounattend.xml and packages it as OEMDRV.
func CreateAutounattendISO(vmDir string, config ProvisionConfig) (string, error) {
	if vmDir == "" {
		return "", fmt.Errorf("vm dir is empty")
	}
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return "", fmt.Errorf("create vm dir: %w", err)
	}

	tmp, err := os.MkdirTemp(vmDir, "autounattend-")
	if err != nil {
		return "", fmt.Errorf("create autounattend dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	xmlPath := filepath.Join(tmp, "autounattend.xml")
	if err := os.WriteFile(xmlPath, []byte(GenerateAutounattendXML(config)), 0644); err != nil {
		return "", fmt.Errorf("write autounattend.xml: %w", err)
	}
	if config.AgentExecutable != "" {
		agentData, err := os.ReadFile(config.AgentExecutable)
		if err != nil {
			return "", fmt.Errorf("read vz-agent executable: %w", err)
		}
		if err := os.WriteFile(filepath.Join(tmp, "vz-agent.exe"), agentData, 0644); err != nil {
			return "", fmt.Errorf("write vz-agent.exe: %w", err)
		}
		if err := os.WriteFile(filepath.Join(tmp, "install-vz-agent.ps1"), []byte(windowsAgentInstallScript(config.AgentTCPPort)), 0644); err != nil {
			return "", fmt.Errorf("write install-vz-agent.ps1: %w", err)
		}
	}

	isoPath := filepath.Join(vmDir, "autounattend.iso")
	if err := os.Remove(isoPath); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("remove existing autounattend iso: %w", err)
	}

	cmd := exec.Command("hdiutil", "makehybrid",
		"-o", isoPath,
		"-hfs",
		"-joliet",
		"-iso",
		"-default-volume-name", "OEMDRV",
		"-iso-volume-name", "OEMDRV",
		"-joliet-volume-name", "OEMDRV",
		tmp,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("create autounattend iso: %w: %s", err, bytes.TrimSpace(output))
	}
	return isoPath, nil
}

func (c ProvisionConfig) withDefaults() ProvisionConfig {
	d := DefaultProvisionConfig()
	if c.Username == "" {
		c.Username = d.Username
	}
	if c.Password == "" {
		c.Password = d.Password
	}
	if c.Hostname == "" {
		c.Hostname = d.Hostname
	}
	if c.Locale == "" {
		c.Locale = d.Locale
	}
	if c.TimeZone == "" {
		c.TimeZone = d.TimeZone
	}
	if c.ProductKey == "" {
		c.ProductKey = d.ProductKey
	}
	if c.AgentTCPPort == 0 {
		c.AgentTCPPort = 1024
	}
	if c.MarkerPath == "" {
		c.MarkerPath = d.MarkerPath
	}
	if !c.OOBEBypass && !c.AutoLogon && !c.LocalAdmin {
		c.OOBEBypass = true
		c.AutoLogon = true
		c.LocalAdmin = true
	}
	return c
}

func oobeXML(config ProvisionConfig) string {
	if !config.OOBEBypass {
		return ""
	}
	return `      <OOBE>
        <HideEULAPage>true</HideEULAPage>
        <HideOnlineAccountScreens>true</HideOnlineAccountScreens>
        <HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE>
        <ProtectYourPC>3</ProtectYourPC>
      </OOBE>`
}

func labConfigRunSynchronousXML() string {
	commands := []string{
		`reg add HKLM\SYSTEM\Setup\LabConfig /v BypassTPMCheck /t REG_DWORD /d 1 /f`,
		`reg add HKLM\SYSTEM\Setup\LabConfig /v BypassSecureBootCheck /t REG_DWORD /d 1 /f`,
		`reg add HKLM\SYSTEM\Setup\LabConfig /v BypassRAMCheck /t REG_DWORD /d 1 /f`,
		`reg add HKLM\SYSTEM\Setup\LabConfig /v BypassCPUCheck /t REG_DWORD /d 1 /f`,
	}
	var b strings.Builder
	b.WriteString(`      <RunSynchronous>`)
	for i, command := range commands {
		fmt.Fprintf(&b, `
        <RunSynchronousCommand wcm:action="add">
          <Order>%d</Order>
          <Path>%s</Path>
        </RunSynchronousCommand>`, i+1, xmlText(command))
	}
	b.WriteString(`
      </RunSynchronous>`)
	return b.String()
}

func offlineServicingDriverPathsXML() string {
	paths := []string{
		`D:\NetKVM\w11\ARM64`,
		`E:\NetKVM\w11\ARM64`,
		`F:\NetKVM\w11\ARM64`,
		`G:\NetKVM\w11\ARM64`,
	}
	var b strings.Builder
	for i, path := range paths {
		fmt.Fprintf(&b, `        <PathAndCredentials wcm:action="add" wcm:keyValue="%d">
          <Path>%s</Path>
        </PathAndCredentials>
`, i+1, xmlText(path))
	}
	return b.String()
}

func specializeRunSynchronousXML() string {
	commands := []string{
		`cmd.exe /c netsh advfirewall firewall add rule name="Cove vz-agent" dir=in action=allow protocol=TCP localport=1024 profile=any & exit /b 0`,
	}
	var b strings.Builder
	b.WriteString(`      <RunSynchronous>`)
	for i, command := range commands {
		fmt.Fprintf(&b, `
        <RunSynchronousCommand wcm:action="add">
          <Order>%d</Order>
          <Path>%s</Path>
        </RunSynchronousCommand>`, i+1, xmlText(command))
	}
	b.WriteString(`
      </RunSynchronous>`)
	return b.String()
}

func autoLogonXML(config ProvisionConfig) string {
	if !config.AutoLogon {
		return ""
	}
	return fmt.Sprintf(`      <AutoLogon>
        <Username>%s</Username>
        <Password>
          <Value>%s</Value>
          <PlainText>true</PlainText>
        </Password>
        <Enabled>true</Enabled>
        <LogonCount>3</LogonCount>
      </AutoLogon>`, xmlText(config.Username), xmlText(config.Password))
}

func firstLogonCommandsXML(config ProvisionConfig) string {
	commands := []struct {
		description string
		line        string
	}{
		{
			description: "Enable OpenSSH Server",
			line:        `powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "$ErrorActionPreference='SilentlyContinue'; Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0; Set-Service sshd -StartupType Automatic; Start-Service sshd"`,
		},
		{
			description: "Enable WinRM",
			line:        `powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "$ErrorActionPreference='SilentlyContinue'; Enable-PSRemoting -Force; Set-Service WinRM -StartupType Automatic; Start-Service WinRM"`,
		},
	}
	if config.AgentExecutable != "" {
		commands = append(commands, struct {
			description string
			line        string
		}{
			description: "Install cove vz-agent",
			line:        `powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "$p = Get-PSDrive -PSProvider FileSystem | ForEach-Object { Join-Path $_.Root 'install-vz-agent.ps1' } | Where-Object { Test-Path $_ } | Select-Object -First 1; if (-not $p) { throw 'install-vz-agent.ps1 not found' }; & $p"`,
		})
	}
	commands = append(commands, struct {
		description string
		line        string
	}{
		description: "Write cove provision marker",
		line:        fmt.Sprintf(`powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "New-Item -ItemType Directory -Force '%s'; New-Item -ItemType File -Force '%s'"`, psParent(config.MarkerPath), psSingleQuote(config.MarkerPath)),
	})

	var b strings.Builder
	b.WriteString("      <FirstLogonCommands>\n")
	for i, cmd := range commands {
		fmt.Fprintf(&b, `        <SynchronousCommand wcm:action="add">
          <Order>%d</Order>
          <Description>%s</Description>
          <CommandLine>%s</CommandLine>
        </SynchronousCommand>
`, i+1, xmlText(cmd.description), xmlText(cmd.line))
	}
	b.WriteString("      </FirstLogonCommands>")
	return b.String()
}

func windowsAgentInstallScript(port int) string {
	if port == 0 {
		port = 1024
	}
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$Root = 'C:\ProgramData\cove'
New-Item -ItemType Directory -Force $Root | Out-Null
try { Start-Transcript -Path (Join-Path $Root 'install-vz-agent.log') -Force | Out-Null } catch {}
$Source = Get-PSDrive -PSProvider FileSystem | ForEach-Object { Join-Path $_.Root 'vz-agent.exe' } | Where-Object { Test-Path $_ } | Select-Object -First 1
if (-not $Source) { throw 'vz-agent.exe not found on attached media' }
$Agent = Join-Path $Root 'vz-agent.exe'
Copy-Item -Force $Source $Agent
try {
  New-NetFirewallRule -DisplayName 'Cove vz-agent' -Direction Inbound -Action Allow -Protocol TCP -LocalPort %d -Profile Any -ErrorAction SilentlyContinue | Out-Null
} catch {}
try { Unregister-ScheduledTask -TaskName 'cove-vz-agent' -Confirm:$false -ErrorAction SilentlyContinue | Out-Null } catch {}
& sc.exe stop cove-vz-agent | Out-Null
& sc.exe delete cove-vz-agent | Out-Null
$Action = New-ScheduledTaskAction -Execute $Agent -Argument '-mode daemon -tcp-listen :%d'
$Trigger = New-ScheduledTaskTrigger -AtStartup
$Principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -RunLevel Highest
Register-ScheduledTask -TaskName 'cove-vz-agent' -Action $Action -Trigger $Trigger -Principal $Principal -Force | Out-Null
Start-ScheduledTask -TaskName 'cove-vz-agent'
Start-Sleep -Seconds 3
$Process = Get-CimInstance Win32_Process -Filter "Name = 'vz-agent.exe'" | Where-Object { $_.CommandLine -like '*-tcp-listen*:%d*' } | Select-Object -First 1
if (-not $Process) { throw 'vz-agent scheduled task did not start vz-agent.exe' }
try { Stop-Transcript | Out-Null } catch {}
`, port, port, port)
}

func psParent(path string) string {
	i := strings.LastIndex(path, `\`)
	if i < 0 {
		return "."
	}
	return psSingleQuote(path[:i])
}

func psSingleQuote(s string) string {
	return strings.ReplaceAll(s, `'`, `''`)
}

func xmlText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
