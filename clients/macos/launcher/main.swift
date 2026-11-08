import AppKit

struct Profile: Codable {
    let id: String
    let name: String
    let target: String
    let arguments: [String]
}

let home = FileManager.default.homeDirectoryForCurrentUser
let config = home.appendingPathComponent(".config/vm200-launcher/profiles.json")
let logs = home.appendingPathComponent("Library/Logs/VM200 Launcher", isDirectory: true)

func profiles() throws -> [Profile] {
    let list = try JSONDecoder().decode([Profile].self, from: Data(contentsOf: config))
    var ids = Set<String>()
    for p in list {
        guard !p.id.isEmpty, p.id.allSatisfy({ $0.isASCII && ($0.isLetter || $0.isNumber || $0 == "-") }),
              ids.insert(p.id).inserted, !p.name.isEmpty, !p.target.isEmpty,
              !p.target.hasPrefix("-"), !p.arguments.isEmpty else {
            throw NSError(domain: "Invalid or duplicate application profile", code: 1)
        }
    }
    return list
}

class Launcher: NSObject, NSApplicationDelegate {
    var status: NSStatusItem!
    var window: NSWindow!
    var running: [String: Process] = [:]
    var pending = Set<String>()
    var deferred = Set<String>()
    let queue = DispatchQueue(label: "vm200.launch")
    var probeError = ""

    func alert(_ title: String, _ detail: String) {
        DispatchQueue.main.async {
            NSApp.activate(ignoringOtherApps: true)
            let a = NSAlert()
            a.messageText = title
            a.informativeText = detail
            a.runModal()
        }
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 400, height: 300),
                          styleMask: [.titled, .closable, .miniaturizable], backing: .buffered, defer: false)
        window.title = "Linux Apps"
        window.isReleasedWhenClosed = false
        window.center()
        status = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        status.button?.image = NSImage(systemSymbolName: "display", accessibilityDescription: "Linux Apps")
        rebuild()
        for id in deferred { launch(id) }
        deferred.removeAll()
        window.makeKeyAndOrderFront(nil)
        if let id = Bundle.main.object(forInfoDictionaryKey: "VM200Profile") as? String {
            launch(id)
        }
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        if let id = Bundle.main.object(forInfoDictionaryKey: "VM200Profile") as? String { launch(id) }
        else { window?.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true) }
        return false
    }

    func application(_ application: NSApplication, open urls: [URL]) {
        for url in urls where url.scheme == "vm200-apps" {
            if let id = url.host { launch(id) }
        }
    }

    func rebuild() {
        let menu = NSMenu()
        menu.addItem(withTitle: "Linux Apps · VM 200", action: nil, keyEquivalent: "")
        do {
            for p in try profiles() {
                let suffix = pending.contains(p.id) ? " — connecting…" : (running[p.id] != nil ? " — running" : "")
                let item = menu.addItem(withTitle: p.name + suffix, action: #selector(selected(_:)), keyEquivalent: "")
                item.representedObject = p.id
                item.target = self
            }
        } catch {
            menu.addItem(withTitle: "Invalid configuration — edit profiles", action: nil, keyEquivalent: "")
        }
        menu.addItem(.separator())
        for (title, action) in [("Edit applications…", #selector(edit)), ("Reload applications", #selector(reload)),
                                 ("Open logs", #selector(openLogs)), ("Quit launcher", #selector(quit))] {
            menu.addItem(withTitle: title, action: action, keyEquivalent: "").target = self
        }
        status.menu = menu
        if let window = window {
            let stack = NSStackView()
            stack.orientation = .vertical
            stack.alignment = .leading
            stack.spacing = 12
            stack.edgeInsets = NSEdgeInsets(top: 24, left: 24, bottom: 24, right: 24)
            stack.addArrangedSubview(NSTextField(labelWithString: "Linux applications · VM 200"))
            for p in (try? profiles()) ?? [] {
                let suffix = pending.contains(p.id) ? " — connecting…" : (running[p.id] != nil ? " — running" : "")
                let button = NSButton(title: p.name + suffix, target: self, action: #selector(selectedButton(_:)))
                button.identifier = NSUserInterfaceItemIdentifier(p.id)
                stack.addArrangedSubview(button)
            }
            stack.addArrangedSubview(NSButton(title: "Edit applications…", target: self, action: #selector(edit)))
            stack.addArrangedSubview(NSButton(title: "Reload applications", target: self, action: #selector(reload)))
            stack.addArrangedSubview(NSButton(title: "Open logs", target: self, action: #selector(openLogs)))
            stack.addArrangedSubview(NSTextField(wrappingLabelWithString: "Click an application to connect. Close its Linux window when finished."))
            window.contentView = stack
        }
    }

    @objc func selected(_ item: NSMenuItem) { if let id = item.representedObject as? String { launch(id) } }
    @objc func selectedButton(_ button: NSButton) { if let id = button.identifier?.rawValue { launch(id) } }
    @objc func edit() { NSWorkspace.shared.open(config) }
    @objc func reload() { rebuild() }
    @objc func openLogs() { NSWorkspace.shared.open(logs) }
    @objc func quit() {
        guard running.isEmpty && pending.isEmpty else {
            alert("Applications are still connected", "Close their Linux windows first, then quit the launcher. Cocoa-Way and Sunshine will remain running.")
            return
        }
        NSApp.terminate(nil)
    }

    // All probes have bounded timeouts; GUI work remains on the main queue.
    func probe(_ path: String, _ args: [String], timeout: TimeInterval = 10) -> Bool {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: path)
        p.arguments = args
        p.standardOutput = FileHandle.nullDevice
        let errors = Pipe()
        p.standardError = errors
        do { try p.run() } catch { return false }
        let deadline = Date().addingTimeInterval(timeout)
        while p.isRunning && Date() < deadline { Thread.sleep(forTimeInterval: 0.1) }
        if p.isRunning { p.terminate(); return false }
        probeError = String(data: errors.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
        return p.terminationStatus == 0
    }

    func launch(_ id: String) {
        guard status != nil else { deferred.insert(id); return }
        if pending.contains(id) { return }
        if running[id] != nil {
            NSWorkspace.shared.openApplication(at: home.appendingPathComponent("Applications/Cocoa-Way.app"),
                                              configuration: NSWorkspace.OpenConfiguration(), completionHandler: nil)
            return
        }
        let profile: Profile
        do {
            guard let p = try profiles().first(where: { $0.id == id }) else {
                alert("Application not found", "Reload applications after editing profiles."); return
            }
            profile = p
        } catch { alert("Cannot read applications", error.localizedDescription); return }
        pending.insert(id)
        rebuild()
        queue.async {
            let ctl = "/opt/homebrew/bin/cocoa-wayctl"
            if !self.probe(ctl, ["--json", "status"], timeout: 2) {
                _ = self.probe("/usr/bin/open", [home.appendingPathComponent("Applications/Cocoa-Way.app").path])
            }
            var ready = false
            for _ in 0..<30 {
                if self.probe(ctl, ["--json", "status"], timeout: 1) { ready = true; break }
                Thread.sleep(forTimeInterval: 0.2)
            }
            guard ready else { self.failed(id, "Cocoa-Way did not become ready. Check that Cocoa-Way.app is installed in ~/Applications."); return }
            guard self.probe("/usr/bin/ssh", ["-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "ConnectionAttempts=1", profile.target, "true"], timeout: 8) else {
                self.failed(id, "Cannot connect to \(profile.target). \(self.probeError)\nCheck the VM/network and authenticate once in Terminal with: ssh \(profile.target)"); return
            }
            do {
                let fm = FileManager.default
                try fm.createDirectory(at: logs, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
                let log = logs.appendingPathComponent("\(id)-\(UUID().uuidString).log")
                guard fm.createFile(atPath: log.path, contents: nil, attributes: [.posixPermissions: 0o600]) else {
                    throw NSError(domain: "Cannot create application log", code: 1)
                }
                let handle = try FileHandle(forWritingTo: log)
                let p = Process()
                p.executableURL = Bundle.main.resourceURL!.appendingPathComponent("vm200-waypipe")
                p.arguments = profile.arguments
                var env = ProcessInfo.processInfo.environment
                env["PATH"] = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
                env["VM200_SSH_TARGET"] = profile.target
                env["VM200_SSH_BATCH"] = "1"
                env.removeValue(forKey: "WAYLAND_DISPLAY")
                env.removeValue(forKey: "COCOA_WAY_RUNTIME_DIR")
                p.environment = env
                p.standardInput = FileHandle.nullDevice
                p.standardOutput = handle
                p.standardError = handle
                p.terminationHandler = { process in
                    try? handle.close()
                    DispatchQueue.main.async {
                        self.running.removeValue(forKey: id)
                        self.rebuild()
                        if process.terminationStatus != 0 {
                            self.alert("\(profile.name) disconnected", "The application or connection exited with code \(process.terminationStatus). Open logs for details, then click the application to start again. Unsaved remote state cannot be restored automatically.")
                        }
                    }
                }
                // Register before starting so a fast exit cannot leave a stale running entry.
                DispatchQueue.main.sync { self.pending.remove(id); self.running[id] = p; self.rebuild() }
                do { try p.run() } catch {
                    try? handle.close()
                    DispatchQueue.main.async { self.running.removeValue(forKey: id) }
                    throw error
                }
            } catch { self.failed(id, error.localizedDescription) }
        }
    }

    func failed(_ id: String, _ message: String) {
        DispatchQueue.main.async {
            self.pending.remove(id)
            self.rebuild()
            self.alert("Cannot launch Linux application", message)
        }
    }
}

if CommandLine.arguments.contains("--check-config") {
    do { print("Validated \(try profiles().count) application profiles"); exit(0) }
    catch { fputs("\(error)\n", stderr); exit(1) }
}
let app = NSApplication.shared
app.setActivationPolicy(.regular)
let delegate = Launcher()
app.delegate = delegate
app.run()
