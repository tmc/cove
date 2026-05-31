package runs

import (
	"fmt"
	"io"
	"strings"

	"github.com/tmc/cove/internal/metrics"
)

const networkPolicyEvent = "network_policy"

// NetworkSummary is a derived view over network_policy events.
type NetworkSummary struct {
	Policy       string   `json:"policy,omitempty"`
	Mode         string   `json:"mode,omitempty"`
	Enforcement  string   `json:"enforcement,omitempty"`
	AuditLog     bool     `json:"audit_log,omitempty"`
	AllowDomains []string `json:"allow_domains,omitempty"`
	AllowCIDRs   []string `json:"allow_cidrs,omitempty"`
	Limitation   string   `json:"limitation,omitempty"`
}

func summarizeNetwork(events []metrics.Event) *NetworkSummary {
	var s *NetworkSummary
	for _, e := range events {
		if e.EventType != networkPolicyEvent {
			continue
		}
		next := NetworkSummary{
			Policy:       extraString(e.Extra, "policy"),
			Mode:         extraString(e.Extra, "mode"),
			Enforcement:  extraString(e.Extra, "enforcement"),
			AuditLog:     extraBool(e.Extra, "audit_log"),
			AllowDomains: extraStringSlice(e.Extra, "allow_domains"),
			AllowCIDRs:   extraStringSlice(e.Extra, "allow_cidrs"),
			Limitation:   extraString(e.Extra, "limitation"),
		}
		s = &next
	}
	if s == nil || s.empty() {
		return nil
	}
	return s
}

func (s *NetworkSummary) empty() bool {
	return s.Policy == "" && s.Mode == "" && s.Enforcement == "" && !s.AuditLog && len(s.AllowDomains) == 0 && len(s.AllowCIDRs) == 0 && s.Limitation == ""
}

func renderNetworkSummary(w io.Writer, s *NetworkSummary) error {
	if s == nil {
		return nil
	}
	if _, err := fmt.Fprintln(w, "Network:"); err != nil {
		return err
	}
	for _, row := range networkSummaryRows(s) {
		if _, err := fmt.Fprintf(w, "  %s: %s\n", row.Name, row.Value); err != nil {
			return err
		}
	}
	return nil
}

type networkSummaryRow struct {
	Name  string
	Value string
}

func networkSummaryRows(s *NetworkSummary) []networkSummaryRow {
	if s == nil {
		return nil
	}
	var rows []networkSummaryRow
	if s.Policy != "" || s.Mode != "" {
		value := networkEmptyDash(s.Policy)
		if s.Mode != "" {
			value += " mode=" + s.Mode
		}
		rows = append(rows, networkSummaryRow{Name: "policy", Value: value})
	}
	if s.Enforcement != "" {
		rows = append(rows, networkSummaryRow{Name: "enforcement", Value: s.Enforcement})
	}
	rows = append(rows, networkSummaryRow{Name: "audit_log", Value: fmt.Sprint(s.AuditLog)})
	if len(s.AllowDomains) > 0 {
		rows = append(rows, networkSummaryRow{Name: "allow_domains", Value: strings.Join(s.AllowDomains, ", ")})
	}
	if len(s.AllowCIDRs) > 0 {
		rows = append(rows, networkSummaryRow{Name: "allow_cidrs", Value: strings.Join(s.AllowCIDRs, ", ")})
	}
	if s.Limitation != "" {
		rows = append(rows, networkSummaryRow{Name: "limitation", Value: s.Limitation})
	}
	return rows
}

func extraBool(extra map[string]any, key string) bool {
	if extra == nil {
		return false
	}
	v, ok := extra[key]
	if !ok {
		return false
	}
	switch v := v.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}

func networkEmptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func extraStringSlice(extra map[string]any, key string) []string {
	if extra == nil {
		return nil
	}
	switch values := extra[key].(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			s, ok := value.(string)
			if ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
