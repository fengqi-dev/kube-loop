import AppKit

let application = NSApplication.shared
application.setActivationPolicy(.accessory)
let window = NSWindow(
    contentRect: NSRect(x: 0, y: 0, width: 320, height: 120),
    styleMask: [.titled],
    backing: .buffered,
    defer: false
)
window.title = "KubeLoop UI Test Host"
window.makeKeyAndOrderFront(nil)
application.run()
