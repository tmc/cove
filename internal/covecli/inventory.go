package covecli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

func Inventory(registry []Spec) []Info {
	out := make([]Info, 0, len(registry))
	for _, spec := range registry {
		out = append(out, Info{
			Name:              spec.Name,
			Aliases:           append([]string(nil), spec.Aliases...),
			Summary:           spec.Summary,
			Dispatch:          DispatchName(spec.Dispatch),
			Outputs:           OutputHints(spec.Name),
			SafeForDiscovery:  SafeForDiscovery(spec.Name),
			MutatesState:      MutatesState(spec.Name),
			RequiresRunningVM: RequiresRunningVM(spec.Name),
			MayBootVM:         MayBootVM(spec.Name),
		})
	}
	return out
}

func PrintCommandsTable(w io.Writer, inventory []Info) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "COMMAND\tALIASES\tDISPATCH\tOUTPUTS\tSUMMARY"); err != nil {
		return err
	}
	for _, info := range inventory {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", info.Name, strings.Join(info.Aliases, ","), info.Dispatch, strings.Join(info.Outputs, ","), info.Summary); err != nil {
			return err
		}
	}
	return tw.Flush()
}
