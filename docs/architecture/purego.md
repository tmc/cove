---
title: purego Bindings
---
# purego Bindings

cove uses [purego](https://github.com/ebitengine/purego) for cgo-free Objective-C interop with Apple's Virtualization.framework. This page covers the approach and known challenges.

## Why purego

Traditional Go bindings for Apple frameworks require cgo, which adds complexity:
- Cross-compilation becomes harder
- Build times increase
- C compiler toolchain required at build time
- CGo pointer passing rules create friction

purego uses `dlopen`/`dlsym` and the Objective-C runtime to call framework methods directly from Go, with no C compiler involved.

## How It Works

### Objective-C Message Sending

```go
import "github.com/nicktrav/objc"

// Equivalent to: [VZVirtualMachineConfiguration alloc]
config := objc.Send[objc.ID](
    objc.ID(objc.GetClass("VZVirtualMachineConfiguration")),
    objc.Sel("alloc"),
)

// Equivalent to: [config initWithConfiguration:cfg queue:q]
vm := objc.Send[objc.ID](
    instance,
    objc.Sel("initWithConfiguration:queue:"),
    config, queue.Handle(),
)
```

### Framework Loading

```go
import "github.com/ebitengine/purego"

// Load Virtualization.framework
lib, _ := purego.Dlopen("/System/Library/Frameworks/Virtualization.framework/Virtualization", purego.RTLD_LAZY)
```

### C Function Registration

```go
var CGEventCreateKeyboardEvent func(source uintptr, keycode uint16, keydown bool) uintptr
purego.RegisterLibFunc(&CGEventCreateKeyboardEvent, carbonLib, "CGEventCreateKeyboardEvent")
```

## Patterns from Code-Hex/vz

cove mirrors [Code-Hex/vz](https://github.com/Code-Hex/vz) patterns where possible. Key reference files:

- `virtualization_arm64.go` -- Go bindings for macOS installer
- `virtualization_12_arm64.m` -- Objective-C implementation
- `virtualization_11.m` -- VM creation, state observation

## Known ARM64 Challenges

### Parameter Corruption Beyond Position 8

On ARM64, `objc.Send` corrupts `uint16` parameters passed beyond argument position 8 (stack-passing region). This affects `NSEvent keyEventWithType:...keyCode:` where `keyCode` is at position 10.

**Symptom:** keycode always reads as 0 regardless of the value passed.

**Solution:** use `CGEventCreateKeyboardEvent` (a C function via `purego.RegisterLibFunc`) instead of NSEvent:

```go
// Create CGEvent
event := CGEventCreateKeyboardEvent(nil, keycode, keydown)
// Convert to NSEvent
nsEvent := objc.Send[objc.ID](
    objc.ID(objc.GetClass("NSEvent")),
    objc.Sel("eventWithCGEvent:"),
    event,
)
// Deliver to VM view
objc.Send[objc.Void](vmView, objc.Sel("keyDown:"), nsEvent)
```

### Object Retention

Virtualization.framework objects must be explicitly retained to prevent premature deallocation:

```go
objc.Send[objc.ID](config.ID, objc.Sel("retain"))
objc.Send[objc.ID](vm.ID, objc.Sel("retain"))
```

Do NOT use autorelease on framework objects.

### Struct Parameters

When passing Objective-C objects to `objc.Send`, extract the `.ID` field explicitly:

```go
// Correct
objc.Send[objc.ID](instance, sel, url.ID, hwModel.ID, options, &errPtr)

// Wrong -- passing Go struct wrappers
objc.Send[objc.ID](instance, sel, url, hwModel, options, &errPtr)
```

## CFRunLoop Modes

```go
// Correct: use kCFRunLoopDefaultMode for running the loop
CFRunLoopRunInMode(kCFRunLoopDefaultMode, seconds, returnAfterSourceHandled)

// Wrong: kCFRunLoopCommonModes is only for adding sources/observers
CFRunLoopRunInMode(kCFRunLoopCommonModes, seconds, returnAfterSourceHandled)
```

## Main Queue Dispatch

GCD main queue dispatch (`dispatch_get_main_queue()`) does NOT work from goroutines unless `NSApplication.run()` or `dispatch_main()` is actively draining it.

**Solutions:**

1. Use custom dispatch queues -- they work from any thread:
   ```go
   vmQueue := dispatch.QueueCreate("com.app.vm")
   DispatchAsyncQueue(vmQueue, func() { /* works */ })
   ```

2. Use thread-safe APIs -- many CoreGraphics APIs don't need the main queue:
   ```go
   cgImage := coregraphics.CGWindowListCreateImage(bounds, options, windowID, imageOptions)
   ```

3. Use CGEvent instead of NSEvent for input events:
   ```go
   event := coregraphics.CGEventCreateKeyboardEvent(nil, keycode, keydown)
   coregraphics.CGEventPost(kCGHIDEventTap, event)
   ```

## State Monitoring

Code-Hex/vz uses KVO (Key-Value Observing) via CGO with `ObservableVZVirtualMachine`. Since KVO callbacks are difficult to implement in pure Go, cove uses polling:

```go
func (vm *virtualMachine) monitorState() {
    ticker := time.NewTicker(100 * time.Millisecond)
    for range ticker.C {
        newState := vz.VZVirtualMachineState(vm.vm.State())
        if newState != oldState {
            // handle state change
        }
    }
}
```

## Pixel Format Conversion

CGImage uses BGRA byte order. Go's `image.RGBA` uses RGBA:

```go
for y := 0; y < height; y++ {
    for x := 0; x < width; x++ {
        b := srcData[pixelStart]
        g := srcData[pixelStart+1]
        r := srcData[pixelStart+2]
        a := srcData[pixelStart+3]
        rgba.SetRGBA(x, y, color.RGBA{r, g, b, a})
    }
}
```
