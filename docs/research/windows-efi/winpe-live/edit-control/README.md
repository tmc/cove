# Visible text input control

An own-thread Win32 EDIT window became foreground and pumped messages. After
frame0, the host requested type-cove. The guest used SendInput Unicode events;
the edit control subsequently read COVEINPUT: and the uploaded image displayed
that text. Six frames arrived during the bounded VZ run. The first immediate
read caught partial text CINPUT:, while later reads consistently had COVEINPUT:,
which is consistent with asynchronous event delivery.

This proves visible Unicode text input through the live host/guest channel.
It does not establish Setup shortcuts, pointer input, or an interactive viewer.
Raw images: /tmp/cove-winpe-edit-20260907/frame-{0..5}.png. Source and text
receipts are adjacent; all binaries and media remain external.
