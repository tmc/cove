//go:build darwin

package ios

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os/exec"
	"runtime"
)

// HostReport distinguishes signature and binding checks from VM qualification.
type HostReport struct {
	Architecture         string   `json:"architecture"`
	SignatureValid       bool     `json:"signatureValid"`
	MissingEntitlements  []string `json:"missingEntitlements"`
	InactiveEntitlements []string `json:"inactiveEntitlements"`
	DescriptorABI        bool     `json:"descriptorABI"`
	Errors               []string `json:"errors,omitempty"`
}

// InspectHost inspects the specified executable and the current host's descriptor
// ABI without creating a model or VM. The caller should supply a bounded context.
func InspectHost(ctx context.Context, executable string) HostReport {
	report := HostReport{Architecture: runtime.GOARCH, MissingEntitlements: []string{}, InactiveEntitlements: []string{}}
	if err := probeHardwareModel(); err != nil {
		report.Errors = append(report.Errors, err.Error())
	} else {
		report.DescriptorABI = true
	}
	if out, err := exec.CommandContext(ctx, "codesign", "--verify", "--strict", executable).CombinedOutput(); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("verify signature: %v: %s", err, bytes.TrimSpace(out)))
	} else {
		report.SignatureValid = true
	}
	out, err := exec.CommandContext(ctx, "codesign", "-d", "--entitlements", ":-", executable).Output()
	var entitlements map[string]bool
	if err == nil {
		entitlements, err = parseEntitlements(out)
	}
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("read entitlements: %v", err))
	}
	_, required := SigningProfile()
	for _, key := range required {
		if active, present := entitlements[key]; !present {
			report.MissingEntitlements = append(report.MissingEntitlements, key)
		} else if !active {
			report.InactiveEntitlements = append(report.InactiveEntitlements, key)
		}
	}
	return report
}

func parseEntitlements(data []byte) (map[string]bool, error) {
	var plist struct {
		XMLName xml.Name `xml:"plist"`
		Dict    *struct {
			Inner string `xml:",innerxml"`
		} `xml:"dict"`
	}
	if err := xml.Unmarshal(data, &plist); err != nil {
		return nil, err
	}
	if plist.Dict == nil {
		return nil, fmt.Errorf("entitlements dictionary missing")
	}
	d := xml.NewDecoder(bytes.NewBufferString(plist.Dict.Inner))
	next := func() (xml.Token, error) {
		for {
			token, err := d.Token()
			if err != nil {
				return nil, err
			}
			switch token.(type) {
			case xml.Comment, xml.ProcInst:
				continue
			}
			if chars, ok := token.(xml.CharData); ok && len(bytes.TrimSpace(chars)) == 0 {
				continue
			}
			return token, nil
		}
	}
	result := make(map[string]bool)
	for {
		token, err := next()
		if err == io.EOF {
			return result, nil
		}
		if err != nil {
			return nil, err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "key" {
			return nil, fmt.Errorf("expected entitlement key")
		}
		var key string
		if err := d.DecodeElement(&key, &start); err != nil {
			return nil, err
		}
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("duplicate entitlement %q", key)
		}
		token, err = next()
		if err != nil {
			return nil, fmt.Errorf("read entitlement %q: %w", key, err)
		}
		value, ok := token.(xml.StartElement)
		if !ok {
			return nil, fmt.Errorf("missing entitlement value for %q", key)
		}
		result[key] = value.Name.Local == "true"
		if err := d.Skip(); err != nil {
			return nil, err
		}
	}
}
