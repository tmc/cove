# Virtualization research probes

These tools investigate private Apple Virtualization interfaces. Run them
explicitly from the repository root on Apple Silicon macOS.

```sh
go run ./_experiments/lfbprobe -h
go run ./_experiments/lfbshot -h
go run ./_experiments/winbootgui -h
go run ./_experiments/winbootprobe -h
```

The directory starts with an underscore so Go skips it in recursive package
patterns. Explicit builds still work for research.
