package main

import "fmt"

func windowsCopyPublishScript(stage, dest string, overwrite bool) string {
	publish := "[IO.File]::Move($src,$dst)"
	if overwrite {
		publish = "if ([IO.File]::Exists($dst)) { [IO.File]::Replace($src,$dst,$null) } else { [IO.File]::Move($src,$dst) }"
	}
	return fmt.Sprintf("$ErrorActionPreference='Stop'; $src='%s'; $dst='%s'; %s", psSingleQuote(stage), psSingleQuote(dest), publish)
}
