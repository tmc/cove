# Browser viewer prototype

The local viewer's input endpoint queued VIEWER. The guest received it in a
frame-upload response, injected Unicode events, and its EDIT control reported
VIEWERINPUT:. Later uploaded PNGs visibly show that text. Six live frames
arrived. This validates the viewer backend's queue and text round trip.

The browser UI consists of a polled image and a Send text form. The form's
actual DOM interaction and visual layout have not been validated: headless
Brave timed out after12 seconds with Crashpad permission errors and produced
no browser screenshot. Do not call this a verified interactive browser viewer.
The HTTP endpoint was exercised directly by the harness, not by clicking the
button. Raw guest frames are at /tmp/cove-winpe-viewer-20260907/frame-{0..5}.png.

viewer.py is the exact bounded experiment, with fixed scratch paths and an
automatic browser check. It stops the VM and listeners after the run, including
when browser validation fails. The guest HTTP listener uses a per-run random
URL; the UI binds loopback with a separate random URL and a bounded text queue.
Tokens, executable, disks, browser profile and images stay outside git.

Mouse, general keyboard shortcuts, full installation, production cove
integration and a persistent usable desktop remain unfinished. The demonstrated
control window is instrumentation, not the Windows desktop objective.
