//go:build darwin

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/objc"
	"github.com/tmc/vz-macos/internal/vmconfig"
)

const (
	coveVMBundleExtension = ".covevm"
	coveVMBundleType      = "com.tmc.cove.vm"
)

var coveVMDocumentOpenDelegate objc.ID
var coveVMDocumentDelegateCount atomic.Uint64

func coveVMBundlePathArg(args []string) (string, bool) {
	if len(args) != 1 {
		return "", false
	}
	return cleanCoveVMBundlePath(args[0])
}

func cleanCoveVMBundlePath(path string) (string, bool) {
	if !strings.EqualFold(filepath.Ext(path), coveVMBundleExtension) {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", false
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", false
	}
	return path, true
}

func coveVMNameForPath(path string) string {
	name := filepath.Base(path)
	if strings.EqualFold(filepath.Ext(name), coveVMBundleExtension) {
		name = name[:len(name)-len(filepath.Ext(name))]
	}
	return name
}

func configureOpenedCoveVM(path string) error {
	path, ok := cleanCoveVMBundlePath(path)
	if !ok {
		return fmt.Errorf("open covevm: invalid bundle %q", path)
	}
	if !vmconfig.Validate(path) {
		return fmt.Errorf("open covevm: invalid VM bundle %q", path)
	}
	vmDir = path
	vmName = coveVMNameForPath(path)
	guiMode = true
	headlessMode = false
	switch vmconfig.DetectOSType(path) {
	case "Linux":
		linuxMode = true
		windowsMode = false
	case "Windows":
		windowsMode = true
		linuxMode = false
	default:
		linuxMode = false
		windowsMode = false
	}
	applyVMConfig(vmDir)
	return nil
}

func showCoveVMLaunchError(title string, err error) {
	if err == nil {
		return
	}
	app := ensureAppReady(appkit.NSApplicationActivationPolicyRegular)
	setAppIcon(&app)
	app.Activate()

	alert := appkit.NewNSAlert()
	alert.SetAlertStyle(appkit.NSAlertStyleCritical)
	alert.SetMessageText(title)
	alert.SetInformativeText(err.Error())
	alert.AddButtonWithTitle("OK")
	alert.RunModal()
}

func installCoveVMDocumentTypes(bundlePath string) error {
	plistPath := filepath.Join(bundlePath, "Contents", "Info.plist")
	data, err := os.ReadFile(plistPath)
	if err != nil {
		return fmt.Errorf("read info plist: %w", err)
	}
	insert := coveVMDocumentPlistXML()
	text := string(data)
	marker := "</dict>"
	idx := strings.LastIndex(text, marker)
	if idx < 0 {
		return fmt.Errorf("info plist missing closing dict")
	}
	text = text[:idx] + insert + text[idx:]
	if err := os.WriteFile(plistPath, []byte(text), 0644); err != nil {
		return fmt.Errorf("write info plist: %w", err)
	}
	return nil
}

func coveVMDocumentPlistXML() string {
	return `
	<key>CFBundleDocumentTypes</key>
	<array>
		<dict>
			<key>CFBundleTypeName</key>
			<string>Cove VM</string>
			<key>CFBundleTypeRole</key>
			<string>Editor</string>
			<key>CFBundleTypeExtensions</key>
			<array>
				<string>covevm</string>
			</array>
			<key>LSItemContentTypes</key>
			<array>
				<string>com.tmc.cove.vm</string>
			</array>
			<key>LSHandlerRank</key>
			<string>Owner</string>
			<key>LSTypeIsPackage</key>
			<true/>
		</dict>
	</array>
	<key>UTExportedTypeDeclarations</key>
	<array>
		<dict>
			<key>UTTypeIdentifier</key>
			<string>com.tmc.cove.vm</string>
			<key>UTTypeDescription</key>
			<string>Cove VM</string>
			<key>UTTypeConformsTo</key>
			<array>
				<string>com.apple.package</string>
				<string>public.directory</string>
			</array>
			<key>UTTypeTagSpecification</key>
			<dict>
				<key>public.filename-extension</key>
				<array>
					<string>covevm</string>
				</array>
			</dict>
		</dict>
	</array>
`
}

func waitForCoveVMDocumentOpen(timeout time.Duration) string {
	if timeout <= 0 {
		return ""
	}
	ch := make(chan string, 1)
	app := ensureAppReady(appkit.NSApplicationActivationPolicyRegular)
	setCoveVMDocumentOpenDelegate(app, ch)
	deadline := time.Now().Add(timeout)
	var opened string
	runAppEventLoopUntil(app, func() bool {
		select {
		case path := <-ch:
			if clean, ok := cleanCoveVMBundlePath(path); ok {
				opened = clean
				return true
			}
		default:
		}
		return time.Now().After(deadline)
	})
	return opened
}

func setCoveVMDocumentOpenDelegate(app appkit.NSApplication, ch chan<- string) {
	className := fmt.Sprintf("CoveVMDocumentOpenDelegate_%d", coveVMDocumentDelegateCount.Add(1))
	cls, err := objc.RegisterClass(
		className,
		objc.GetClass("NSObject"),
		nil,
		nil,
		[]objc.MethodDef{
			{
				Cmd: objc.RegisterName("application:openFile:"),
				Fn: func(self objc.ID, _cmd objc.SEL, sender objc.ID, filename objc.ID) bool {
					path := objc.IDToString(filename)
					select {
					case ch <- path:
					default:
					}
					return true
				},
			},
			{
				Cmd: objc.RegisterName("application:openFiles:"),
				Fn: func(self objc.ID, _cmd objc.SEL, sender objc.ID, filenames objc.ID) {
					for _, id := range objc.NSArrayToSlice(filenames) {
						path := objc.IDToString(id)
						select {
						case ch <- path:
						default:
						}
					}
				},
			},
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not register covevm document delegate: %v\n", err)
		return
	}
	delegate := objc.Send[objc.ID](objc.ID(cls), objc.Sel("alloc"))
	delegate = objc.Send[objc.ID](delegate, objc.Sel("init"))
	coveVMDocumentOpenDelegate = delegate
	app.SetDelegate(appkit.NSApplicationDelegateObjectFromID(delegate))
}
