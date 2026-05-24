package covecli

import (
	"fmt"
	"io"
)

func PrintVersion(w io.Writer, info string) {
	fmt.Fprintln(w, info)
}
