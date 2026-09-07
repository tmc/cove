package restore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/tmc/apple/x/plist"
	"github.com/tmc/cove/internal/ios/firmware"
)

// BundleObjects resolves restored requests against a verified patched bundle.
// Keep the bundle open until the session and all returned readers finish.
// The zero value is not usable; construct it with NewBundleObjects.
type BundleObjects struct {
	bundle            *firmware.Bundle
	primary, recovery map[string]any
	response          map[string]any
	behavior          string
}

// NewBundleObjects selects deviceClass and behavior ("Erase" or "Update") from
// the bundle's BuildManifest. response is the retained AP TSS response used by
// SessionData, including any component path overrides. It is copied. Missing or
// ambiguous primary identities fail; an absent recovery identity fails only when
// requested. Manifest signing digests are retained independently of patched files.
func NewBundleObjects(ctx context.Context, bundle *firmware.Bundle, deviceClass, behavior string, response map[string]any) (*BundleObjects, error) {
	if bundle == nil || deviceClass == "" || behavior != "Erase" && behavior != "Update" {
		return nil, fmt.Errorf("bundle, device class and Erase or Update behavior are required")
	}
	f, err := bundle.Open(ctx, "iphone_Restore/BuildManifest.plist")
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, 16<<20+1))
	err = errors.Join(err, f.Close())
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) > 16<<20 {
		return nil, fmt.Errorf("build manifest exceeds 16 MiB")
	}
	var manifest map[string]any
	if _, err := plist.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode build manifest: %w", err)
	}
	identities, ok := manifest["BuildIdentities"].([]any)
	if !ok || len(identities) == 0 {
		return nil, fmt.Errorf("build manifest has no identities")
	}
	variant := "Erase Install (IPSW)"
	if behavior == "Update" {
		variant = "Upgrade Install (IPSW)"
	}
	primary, err := selectBundleIdentity(identities, deviceClass, behavior, variant)
	if err != nil {
		return nil, err
	}
	if primary == nil && behavior == "Update" {
		primary, err = selectBundleIdentity(identities, deviceClass, behavior, "")
		if err != nil {
			return nil, err
		}
	}
	if primary == nil {
		return nil, fmt.Errorf("bundle has no %s identity for %s", behavior, deviceClass)
	}
	recovery, err := selectBundleIdentity(identities, deviceClass, "", "macOS Customer")
	if err != nil {
		return nil, err
	}
	var copied map[string]any
	if response != nil {
		copied, err = copyBundleDictionary(response)
		if err != nil {
			return nil, fmt.Errorf("copy bundle TSS response: %w", err)
		}
	}
	return &BundleObjects{bundle: bundle, primary: primary, recovery: recovery, response: copied, behavior: behavior}, nil
}

func selectBundleIdentity(identities []any, deviceClass, behavior, variant string) (map[string]any, error) {
	var selected map[string]any
	for _, value := range identities {
		identity, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("build identity is not a dictionary")
		}
		info, ok := identity["Info"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("build identity has no Info dictionary")
		}
		model, _ := info["DeviceClass"].(string)
		v, _ := info["Variant"].(string)
		b, _ := info["RestoreBehavior"].(string)
		if model != deviceClass || behavior != "" && b != behavior || variant != "" && !strings.Contains(v, variant) {
			continue
		}
		if _, ok := identity["Manifest"].(map[string]any); !ok {
			return nil, fmt.Errorf("selected build identity has no Manifest dictionary")
		}
		if selected != nil {
			return nil, fmt.Errorf("ambiguous bundle identity for %s, %s, %s", deviceClass, behavior, variant)
		}
		selected = identity
	}
	return selected, nil
}

// Identity selects a build for a SessionData request and returns an independent
// dictionary. V3 and legacy objects use the primary identity. V4 and build-identity
// requests honor IsRecoveryOS. Erase volume policy requires a recovery identity.
func (b *BundleObjects) Identity(ctx context.Context, message map[string]any) (map[string]any, error) {
	identity, err := b.identity(ctx, message)
	if err != nil {
		return nil, err
	}
	return copyBundleDictionary(identity)
}

func (b *BundleObjects) identity(ctx context.Context, message map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil || b.bundle == nil {
		return nil, fmt.Errorf("bundle object provider is not initialized")
	}
	recovery, requireVariant := false, false
	switch message["DataType"] {
	case "BuildIdentityDict", "SourceBootObjectV4":
		requireVariant = true
		arguments, ok := message["Arguments"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("build selection Arguments is not a dictionary")
		}
		if value, present := arguments["IsRecoveryOS"]; present {
			recovery, ok = value.(bool)
			if !ok {
				return nil, fmt.Errorf("IsRecoveryOS is not a boolean")
			}
		}
	case "RecoveryOSLocalPolicy":
		recovery = b.behavior == "Erase"
		requireVariant = true
	case "PersonalizedBootObjectV3", "KernelCache", "DeviceTree", "SystemImageRootHash", "SystemImageCanonicalMetadata":
	default:
		return nil, fmt.Errorf("unsupported build selection data type %v", message["DataType"])
	}
	if recovery {
		if b.recovery == nil {
			return nil, fmt.Errorf("bundle has no recovery OS identity")
		}
		return b.recovery, nil
	}
	if requireVariant && b.behavior == "Update" {
		variant, _ := b.primary["Info"].(map[string]any)["Variant"].(string)
		if !strings.Contains(variant, "Upgrade Install (IPSW)") {
			return nil, fmt.Errorf("bundle has no upgrade install identity for service request")
		}
	}
	return b.primary, nil
}

// Object opens a named component or special metadata file for SessionData.Object.
// TSS component paths take precedence over manifest paths. Both must be canonical
// paths inside iphone_Restore and present in the verified output catalog.
// The caller closes the returned reader.
func (b *BundleObjects) Object(ctx context.Context, message map[string]any, name string) (io.ReadCloser, error) {
	if b == nil || b.bundle == nil {
		return nil, fmt.Errorf("bundle object provider is not initialized")
	}
	var filename string
	switch name {
	case "__RestoreVersion__":
		filename = "RestoreVersion.plist"
	case "__SystemVersion__":
		filename = "SystemVersion.plist"
	case "__GlobalManifest__":
		info := b.primary["Info"].(map[string]any)
		variant, _ := info["MacOSVariant"].(string)
		model, _ := info["DeviceClass"].(string)
		if !bundleSegment(variant) || !bundleSegment(model) {
			return nil, fmt.Errorf("global manifest requires local MacOSVariant and DeviceClass names")
		}
		filename = "Firmware/Manifests/restore/" + variant + "/apticket." + model + ".im4m"
	default:
		identity, err := b.identity(ctx, message)
		if err != nil {
			return nil, err
		}
		manifest := identity["Manifest"].(map[string]any)
		component, ok := manifest[name].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("build identity has no component %q", name)
		}
		if value, present := b.response[name]; present {
			entry, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("TSS component %s is not a dictionary", name)
			}
			if value, present := entry["Path"]; present {
				filename, ok = value.(string)
				if !ok || filename == "" {
					return nil, fmt.Errorf("TSS component %s Path is not a nonempty string", name)
				}
			}
		}
		if filename == "" {
			info, _ := component["Info"].(map[string]any)
			filename, _ = info["Path"].(string)
		}
	}
	if !bundlePath(filename) {
		return nil, fmt.Errorf("invalid bundle component path %q", filename)
	}
	return b.bundle.Open(ctx, "iphone_Restore/"+filename)
}

func bundlePath(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.HasPrefix(name, "/") && !strings.HasPrefix(name, "../") && path.Clean(name) == name && !strings.ContainsAny(name, "\\\x00")
}

func bundleSegment(name string) bool {
	return bundlePath(name) && !strings.Contains(name, "/")
}

func copyBundleDictionary(value map[string]any) (map[string]any, error) {
	data, err := plist.Marshal(value, plist.FormatXML)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	_, err = plist.Unmarshal(data, &result)
	return result, err
}
