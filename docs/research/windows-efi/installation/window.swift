import AppKit

final class ScreenView: NSView {
    var frameImage: NSImage?
    var send: ((String) -> Void)?
    override var acceptsFirstResponder: Bool { true }
    var imageRect: NSRect {
        guard let image = frameImage else { return bounds }
        let scale = min(bounds.width / image.size.width, bounds.height / image.size.height)
        let size = NSSize(width: image.size.width * scale, height: image.size.height * scale)
        return NSRect(x: (bounds.width-size.width)/2, y: (bounds.height-size.height)/2, width: size.width, height: size.height)
    }
    override func draw(_ dirtyRect: NSRect) {
        NSColor.black.setFill()
        bounds.fill()
        frameImage?.draw(in: imageRect)
    }
    var pressed = Set<Int>()
    var lastMove = Date.distantPast
    var lastPoint = (0, 0)
    var buttons = Set<Int>()
    var tracking: NSTrackingArea?
    var caps = false
    let keys: [UInt16:Int] = [0:65,1:83,2:68,3:70,4:72,5:71,6:90,7:88,8:67,9:86,11:66,
        12:81,13:87,14:69,15:82,16:89,17:84,18:49,19:50,20:51,21:52,22:54,23:53,
        24:187,25:57,26:55,27:189,28:56,29:48,30:221,31:79,32:85,33:219,34:73,35:80,
        36:13,37:76,38:74,39:222,40:75,41:186,42:220,43:188,44:191,45:78,46:77,47:190,
        48:9,49:32,50:192,51:8,53:27,65:110,67:106,69:107,75:111,76:13,78:109,
        82:96,83:97,84:98,85:99,86:100,87:101,88:102,89:103,91:104,92:105,
        96:116,97:117,98:118,99:114,100:119,101:120,103:122,109:121,111:123,
        115:36,116:33,117:46,118:115,119:35,120:113,121:34,122:112,123:37,124:39,125:40,126:38]
    func pointer(_ event: NSEvent, _ flags: Int) {
        guard let image = frameImage else { return }
        let point = convert(event.locationInWindow, from: nil)
        let rect = imageRect
        let x = max(0, min(Int(image.size.width)-1, Int((point.x-rect.minX)*image.size.width/rect.width)))
        let y = max(0, min(Int(image.size.height)-1, Int((rect.maxY-point.y)*image.size.height/rect.height)))
        lastPoint = (x, y)
        if flags == 2 || flags == 8 { buttons.insert(flags) }
        if flags == 4 { buttons.remove(2) }
        if flags == 16 { buttons.remove(8) }
        send?("pointer:\(x),\(y),\(flags)")
    }
    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        if let tracking = tracking { removeTrackingArea(tracking) }
        tracking = NSTrackingArea(rect: .zero, options: [.mouseMoved, .activeInKeyWindow, .inVisibleRect], owner: self)
        addTrackingArea(tracking!)
    }
    override func mouseMoved(with event: NSEvent) { mouseDragged(with: event) }
    override func mouseDown(with event: NSEvent) { window?.makeFirstResponder(self); pointer(event, 2) }
    override func mouseUp(with event: NSEvent) { pointer(event, 4) }
    override func rightMouseDown(with event: NSEvent) { pointer(event, 8) }
    override func rightMouseUp(with event: NSEvent) { pointer(event, 16) }
    override func mouseDragged(with event: NSEvent) {
        if Date().timeIntervalSince(lastMove) >= 0.03 { lastMove = Date(); pointer(event, 1) }
    }
    override func rightMouseDragged(with event: NSEvent) { mouseDragged(with: event) }
    override func scrollWheel(with event: NSEvent) {
        let value = Int(event.scrollingDeltaY * (event.hasPreciseScrollingDeltas ? 8 : 120))
        if value != 0 { send?("wheel:\(value)") }
    }
    func key(_ code: Int, _ down: Bool) {
        if down { pressed.insert(code) } else { pressed.remove(code) }
        send?("kbd:\(code),\(down ? 1 : 0)")
    }
    override func flagsChanged(with event: NSEvent) {
        if event.modifierFlags.contains(.capsLock) != caps {
            caps = event.modifierFlags.contains(.capsLock)
            key(20, true)
            key(20, false)
        }
        let flags: [(NSEvent.ModifierFlags, Int)] = [(.shift,16),(.control,17),(.option,18),(.command,91)]
        for (flag, code) in flags {
            let down = event.modifierFlags.contains(flag)
            if down != pressed.contains(code) { key(code, down) }
        }
    }
    override func keyDown(with event: NSEvent) {
        flagsChanged(with: event)
        if let code = keys[event.keyCode] { key(code, true) }
        else if let text = event.characters { send?("text:"+text) }
    }
    override func keyUp(with event: NSEvent) {
        if let code = keys[event.keyCode] { key(code, false) }
    }
    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        if event.modifierFlags.intersection([.command, .control, .option]).isEmpty { return false }
        keyDown(with: event)
        return true
    }
    func releaseKeys() {
        for code in pressed.sorted() { key(code, false) }
        for button in buttons.sorted() {
            send?("pointer:\(lastPoint.0),\(lastPoint.1),\(button == 2 ? 4 : 16)")
        }
        buttons.removeAll()
    }

}

final class App: NSObject, NSApplicationDelegate, NSWindowDelegate {
    var window: NSWindow!
    let screen = ScreenView()
    let endpoint: URL
    var loading = false
    var commands: [String] = []
    var sending = false
    var lastFrame = Date.distantPast
    var guestSession = ""
    var timer: Timer?
    init(endpoint: URL) { self.endpoint = endpoint }
    func applicationDidFinishLaunching(_ notification: Notification) {
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1200, height: 750), styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
        window.delegate = self
        window.acceptsMouseMovedEvents = true
        window.title = "Windows on VZ — connecting"
        window.contentView = screen
        window.center()
        window.makeKeyAndOrderFront(nil)
        window.makeFirstResponder(screen)
        NSApp.activate(ignoringOtherApps: true)
        screen.send = { [weak self] command in self?.send(command) }
        timer = Timer.scheduledTimer(withTimeInterval: 0.25, repeats: true) { [weak self] _ in self?.refresh() }
        refresh()
    }
    func windowDidResignKey(_ notification: Notification) { screen.releaseKeys() }
    func send(_ command: String) {
        guard Date().timeIntervalSince(lastFrame) < 5 else { return }
        commands.append(command)
        drain()
    }
    func drain() {
        guard !sending, !commands.isEmpty else { return }
        sending = true
        let command = commands.removeFirst()
        var request = URLRequest(url: endpoint.appendingPathComponent("input"))
        request.httpMethod = "POST"
        request.setValue(guestSession, forHTTPHeaderField: "X-Session")
        request.httpBody = command.data(using: .utf8)
        request.timeoutInterval = 3
        URLSession.shared.dataTask(with: request) { _, response, error in
            DispatchQueue.main.async {
                self.sending = false
                if error != nil || (response as? HTTPURLResponse)?.statusCode != 200 {
                    self.window.title = "Windows on VZ — input delivery failed"
                    self.commands.removeAll()
                }
                self.drain()
            }
        }.resume()
    }
    func refresh() {
        guard !loading else { return }
        loading = true
        var request = URLRequest(url: endpoint.appendingPathComponent("frame"))
        request.cachePolicy = .reloadIgnoringLocalCacheData
        request.timeoutInterval = 3
        URLSession.shared.dataTask(with: request) { data, response, _ in
            DispatchQueue.main.async {
                self.loading = false
                guard let data = data, let image = NSImage(data: data) else {
                    self.window.title = "Windows on VZ — waiting for display"
                    return
                }
                if let stamp = (response as? HTTPURLResponse)?.value(forHTTPHeaderField: "X-Frame-Time"), let seconds = Double(stamp) {
                    self.lastFrame = Date(timeIntervalSince1970: seconds)
                } else { self.lastFrame = Date() }
                let session = (response as? HTTPURLResponse)?.value(forHTTPHeaderField: "X-Agent-Session") ?? ""
                if session != self.guestSession {
                    self.commands.removeAll()
                    self.screen.pressed.removeAll()
                    self.screen.buttons.removeAll()
                    self.guestSession = session
                }
                self.screen.frameImage = image
                self.screen.needsDisplay = true
                self.window.title = Date().timeIntervalSince(self.lastFrame) < 5 ? "Windows on VZ" : "Windows on VZ — waiting for guest"
                if Date().timeIntervalSince(self.lastFrame) >= 5 { self.commands.removeAll() }
            }
        }.resume()
    }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }
}

guard CommandLine.arguments.count == 2,
      let text = try? String(contentsOfFile: CommandLine.arguments[1], encoding: .utf8),
      let endpoint = URL(string: text.trimmingCharacters(in: .whitespacesAndNewlines)),
      endpoint.host == "127.0.0.1", endpoint.scheme == "http" else {
    fputs("usage: windows-vz-window viewer.url-file\n", stderr)
    exit(2)
}
let app = NSApplication.shared
app.setActivationPolicy(.regular)
let delegate = App(endpoint: endpoint)
app.delegate = delegate
app.run()
