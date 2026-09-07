package restore

import (
	"fmt"
	"slices"

	"github.com/tmc/apple/x/img4"
)

// Personalize assembles a component using the selected build identity's Info,
// current AP parameters and TSS response. It does not authenticate the response
// or check its hardware, nonce or image assertions; use MatchTicket for those
// consistency checks, and establish ticket provenance separately.
func Personalize(name string, payload []byte, tss, info, parameters map[string]any) ([]byte, error) {
	if name == "" {
		return nil, fmt.Errorf("component name is required")
	}
	ticket, ok := tss["ApImg4Ticket"].([]byte)
	if !ok || len(ticket) == 0 {
		return nil, fmt.Errorf("ApImg4Ticket is missing or not data")
	}
	options, err := componentOptions(name, tss, info, parameters)
	if err != nil {
		return nil, err
	}
	image, err := img4.Personalize(payload, ticket, options)
	if err != nil {
		return nil, fmt.Errorf("personalize %s: %w", name, err)
	}
	return image, nil
}

func componentOptions(name string, tss, info, parameters map[string]any) (img4.Options, error) {
	options := img4.Options{FourCC: componentFourCC[name]}
	var required bool
	if value, ok := info["RequiresNonceSlot"]; ok {
		var valid bool
		required, valid = value.(bool)
		if !valid {
			return options, fmt.Errorf("RequiresNonceSlot is not a boolean")
		}
	}
	if required && (name == "SEP" || name == "SepStage1" || name == "LLB") {
		property, key, fallback := "snid", "SepNonceSlotID", uint64(2)
		if name == "LLB" {
			property, key, fallback = "anid", "ApNonceSlotID", 0
		}
		value, ok := parameters[key]
		if !ok {
			value, ok = info[key]
		}
		slot := fallback
		if ok {
			var err error
			slot, err = nonceSlot(value)
			if err != nil {
				return options, fmt.Errorf("%s: %w", key, err)
			}
		}
		options.RestoreProperties = map[string]any{property: slot}
	}
	if value, ok := tss[name+"-TBM"]; ok {
		properties, ok := value.(map[string]any)
		if !ok {
			return options, fmt.Errorf("%s-TBM is not a dictionary", name)
		}
		if options.RestoreProperties == nil {
			options.RestoreProperties = make(map[string]any)
		}
		for key, value := range properties {
			if _, ok := options.RestoreProperties[key]; ok {
				return options, fmt.Errorf("duplicate restore property %s", key)
			}
			if key == "BNCN" {
				nonce, ok := value.([]byte)
				if !ok || len(nonce) != 8 {
					return options, fmt.Errorf("BNCN must contain eight bytes")
				}
				nonce = slices.Clone(nonce)
				slices.Reverse(nonce)
				value = nonce
			}
			options.RestoreProperties[key] = value
		}
	}
	return options, nil
}

func nonceSlot(value any) (uint64, error) {
	switch n := value.(type) {
	case uint64:
		return n, nil
	case int64:
		if n >= 0 {
			return uint64(n), nil
		}
	case int:
		if n >= 0 {
			return uint64(n), nil
		}
	}
	return 0, fmt.Errorf("nonce slot must be a nonnegative integer")
}
