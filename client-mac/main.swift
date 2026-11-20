import Cocoa
import SwiftUI
import Foundation
import ServiceManagement

// MARK: - 配置

// 个人版：默认连本机，可用 -hub 参数或 config.json 里的 hub 改成自己的地址
var defaultHub = "http://127.0.0.1:7100"

struct Config: Codable {
    var hub = defaultHub {
        didSet { if hub != defaultHub { defaultHub = hub } }
    }
    var token = ""
    var deviceId = ""
    var deviceName = Host.current().localizedName ?? "Mac"
    var dshURL = ""
    var autoConnect = true
}

func configPath() -> URL {
    let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        .appendingPathComponent("RemoteHarness", isDirectory: true)
    try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    return dir.appendingPathComponent("config.json")
}

func loadConfig() -> Config {
    if let d = try? Data(contentsOf: configPath()),
       let c = try? JSONDecoder().decode(Config.self, from: d) { return c }
    return Config()
}

func saveConfig(_ c: Config) {
    if let d = try? JSONEncoder().encode(c) {
        try? d.write(to: configPath(), options: .atomic)
    }
}

func logPath() -> URL {
    let dir = FileManager.default.urls(for: .libraryDirectory, in: .userDomainMask)[0]
        .appendingPathComponent("Logs/RemoteHarness", isDirectory: true)
    try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    return dir.appendingPathComponent("client.log")
}

func log(_ s: String) {
    let line = ISO8601DateFormatter().string(from: Date()) + " " + s + "\n"
    if let h = try? FileHandle(forWritingTo: logPath()) {
        h.seekToEndOfFile()
        h.write(line.data(using: .utf8)!)
        try? h.close()
    } else {
        try? line.write(to: logPath(), atomically: true, encoding: .utf8)
    }
    NSLog("RemoteHarness: %@", s)
}

// MARK: - 工具

func b64(_ d: Data) -> String { d.base64EncodedString() }
func unb64(_ s: String) -> Data { Data(base64Encoded: s) ?? Data() }

// MARK: - 不跟随跳转（让浏览器处理，才能带上 DSH 下发的 cookie）
final class NoRedirect: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask,
                    willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest,
                    completionHandler: @escaping (URLRequest?) -> Void) {
        completionHandler(nil)
    }
}

func sysctlString(_ name: String) -> String {
    var size = 0
    sysctlbyname(name, nil, &size, nil, 0)
    guard size > 0 else { return "" }
    var buf = [CChar](repeating: 0, count: size)
    sysctlbyname(name, &buf, &size, nil, 0)
    return String(cString: buf)
}

func deviceMeta() -> (name: String, model: String, chip: String, osv: String) {
    let model = sysctlString("hw.model")
    let chip = sysctlString("machdep.cpu.brand_string")
    let v = ProcessInfo.processInfo.operatingSystemVersion
    let osv = "macOS \(v.majorVersion).\(v.minorVersion)"
    let name = Host.current().localizedName ?? "我的 Mac"
    return (name, model, chip, osv)
}


// MARK: - 面板 UI（SwiftUI）

struct DevVM: Identifiable {
    let id: String
    let name: String
    let meta: String
    let online: Bool
}

final class PanelModel: ObservableObject {
    @Published var name = ""
    @Published var online = false
    @Published var ready = false
    @Published var harnessRunning = false
    @Published var serverOK = false
    @Published var devices: [DevVM] = []
    @Published var member = ""
    @Published var memberActive = false
    @Published var memberDays = 0
    @Published var memberUntil = ""
}

struct PanelView: View {
    @ObservedObject var model: PanelModel
    var onTakeover: () -> Void
    var onLogout: () -> Void
    var onRenew: () -> Void

    private var harnessColor: Color {
        if !model.serverOK { return .red }
        return model.ready ? .green : .orange
    }

    private var harnessText: String {
        if !model.serverOK { return "未连接服务器" }
        if model.ready { return "就绪" }
        return model.harnessRunning ? "运行中，未取得凭证" : "未运行"
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider().padding(.vertical, 14)
            rows
            Divider().padding(.vertical, 14)
            devices
            Spacer(minLength: 10)
            member
            Divider().padding(.vertical, 12)
            footer
        }
        .padding(20)
        .frame(width: 460, height: 640, alignment: .top)
        .background(Color(nsColor: .windowBackgroundColor))
    }

    private var header: some View {
        HStack(spacing: 11) {
            if let img = NSImage(named: "RemoteHarness") {
                Image(nsImage: img).resizable()
                    .frame(width: 36, height: 36)
                    .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
            }
            VStack(alignment: .leading, spacing: 1) {
                Text("远程 Harness 服务端").font(.system(size: 14, weight: .semibold))
                Text(model.name.isEmpty ? "本机" : model.name)
                    .font(.system(size: 11)).foregroundStyle(.secondary)
            }
            Spacer()
            HStack(spacing: 5) {
                Circle().fill(model.serverOK ? Color.green : Color.red).frame(width: 7, height: 7)
                Text(model.serverOK ? "在线" : "离线")
                    .font(.system(size: 11)).foregroundStyle(.secondary)
            }
        }
    }

    private var rows: some View {
        VStack(spacing: 10) {
            HStack {
                Text("服务器").font(.system(size: 12.5))
                Spacer()
                Text(model.serverOK ? "已连接" : "未连接")
                    .font(.system(size: 12.5))
                    .foregroundStyle(model.serverOK ? Color.secondary : Color.red)
            }
            HStack {
                Text("Harness").font(.system(size: 12.5))
                Spacer()
                Circle().fill(harnessColor).frame(width: 7, height: 7)
                Text(harnessText).font(.system(size: 12.5)).foregroundStyle(.secondary)
            }
            if !model.ready && model.serverOK {
                Button(action: onTakeover) {
                    Text(model.harnessRunning ? "重启并接管 Harness" : "运行 Harness")
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 3)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .padding(.top, 2)
            }
        }
    }

    private var devices: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("设备").font(.system(size: 11, weight: .semibold)).foregroundStyle(.secondary)
            if model.devices.isEmpty {
                Text("还没有设备登录").font(.system(size: 12)).foregroundStyle(.secondary)
            } else {
                ForEach(model.devices) { d in
                    HStack(spacing: 9) {
                        Image(systemName: d.meta.lowercased().contains("iphone") ? "iphone" : "desktopcomputer")
                            .font(.system(size: 12)).foregroundStyle(.secondary)
                            .frame(width: 16)
                        Text(d.name).font(.system(size: 12.5)).lineLimit(1)
                        Text(d.meta).font(.system(size: 10.5)).foregroundStyle(.secondary).lineLimit(1)
                        Spacer()
                        Circle().fill(d.online ? Color.green : Color(nsColor: .tertiaryLabelColor))
                            .frame(width: 6, height: 6)
                        Text(d.online ? "在线" : "离线")
                            .font(.system(size: 10.5)).foregroundStyle(.secondary)
                    }
                }
            }
        }
    }

    private var member: some View {
        HStack(spacing: 8) {
            Image(systemName: "crown.fill").font(.system(size: 10)).foregroundStyle(.orange)
            if model.memberActive {
                Text("会员 · 剩余 \(model.memberDays) 天").font(.system(size: 12))
                Text(model.memberUntil).font(.system(size: 11)).foregroundStyle(.secondary)
            } else {
                Text("未开通会员").font(.system(size: 12)).foregroundStyle(.secondary)
            }
            Spacer()
            Button("管理") { onRenew() }.buttonStyle(.link).font(.system(size: 11.5))
        }
    }

    private var footer: some View {
        HStack {
            Text("v1.0.0").font(.system(size: 10)).foregroundStyle(.tertiary)
            Spacer()
            Button("退出登录") { onLogout() }.buttonStyle(.link).font(.system(size: 11.5))
        }
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate, NSMenuDelegate {
    let statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
    var cfg = loadConfig()
    var state: String = "未登录"

    var hub: URLSessionWebSocketTask?
    var generation = 0
    var hubSession: URLSession!
    var localSession: URLSession!
    var wsTasks: [String: URLSessionWebSocketTask] = [:]
    var wsIDs: [ObjectIdentifier: String] = [:]

    var loginWindow: NSWindow?
    var userField: NSTextField!
    var passField: NSSecureTextField!
    var loginStatus: NSTextField!

    // MARK: 生命周期

    func applicationDidFinishLaunching(_ n: Notification) {
        // 只允许一个实例：多开会让同一台设备在服务器上互相顶掉
        let others = NSRunningApplication.runningApplications(withBundleIdentifier: "local.z.remoteharness")
            .filter { $0.processIdentifier != getpid() }
        if !others.isEmpty {
            log("已有实例在运行，本实例退出")
            NSApp.terminate(nil)
            return
        }
        NSApp.setActivationPolicy(.accessory)
        unsetenv("ELECTRON_RUN_AS_NODE")
        log("启动")
        cfg.hub = defaultHub
        cfg.deviceName = deviceMeta().name
        saveConfig(cfg)
        localSession = URLSession(configuration: .ephemeral, delegate: NoRedirect(), delegateQueue: nil)
        let sc = URLSessionConfiguration.default
        sc.timeoutIntervalForRequest = 30
        sc.waitsForConnectivity = true
        hubSession = URLSession(configuration: sc)

        updateStatusIcon()
        statusItem.button?.image?.isTemplate = true
        rebuildMenu()
        migrateLoginItem()
        if #available(macOS 13.0, *) {
            log("登录项状态=\(SMAppService.mainApp.status.rawValue)")
        }

        if cfg.token.isEmpty {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.4) { self.openLogin() }
        } else if cfg.autoConnect {
            if cfg.dshURL.isEmpty { detectDSH() }
            connect()
        }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ s: NSApplication) -> Bool { false }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        showPanel()
        return true
    }

    func applicationWillTerminate(_ n: Notification) {
        log("退出（应用终止）")
    }

    // MARK: 菜单

    func rebuildMenu() {
        let m = NSMenu()
        let head = NSMenuItem(title: "远程 Harness 服务端 · \(state)", action: nil, keyEquivalent: "")
        head.isEnabled = false
        m.addItem(head)
        m.addItem(.separator())
        m.addItem(NSMenuItem(title: "启动 Harness", action: #selector(startHarnessMenu), keyEquivalent: "r"))
        m.addItem(NSMenuItem(title: "打开面板", action: #selector(showPanel), keyEquivalent: "o"))
        let auto = NSMenuItem(title: "开机自动启动", action: #selector(toggleLaunchAtLogin), keyEquivalent: "")
        auto.state = launchAtLoginEnabled() ? .on : .off
        m.addItem(auto)
        m.addItem(.separator())
        m.addItem(NSMenuItem(title: "退出", action: #selector(quit), keyEquivalent: "q"))
        statusItem.menu = m
    }

    func loginAgentPath() -> URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/LaunchAgents", isDirectory: true)
            .appendingPathComponent("local.z.remoteharness.login.plist")
    }

    func launchAtLoginEnabled() -> Bool {
        if #available(macOS 13.0, *), SMAppService.mainApp.status == .enabled {
            return true
        }
        return FileManager.default.fileExists(atPath: loginAgentPath().path)
    }

    @objc func toggleLaunchAtLogin() {
        setLaunchAtLogin(!launchAtLoginEnabled())
    }

    func writeLoginAgent() throws {
        let path = loginAgentPath()
        try FileManager.default.createDirectory(at: path.deletingLastPathComponent(),
                                                withIntermediateDirectories: true)
        let app = Bundle.main.bundlePath
        let plist = """
        <?xml version="1.0" encoding="UTF-8"?>
        <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
        <plist version="1.0"><dict>
          <key>Label</key><string>local.z.remoteharness.login</string>
          <key>ProgramArguments</key><array>
            <string>/usr/bin/open</string>
            <string>-a</string>
            <string>\(app)</string>
          </array>
          <key>RunAtLoad</key><true/>
          <key>LimitLoadToSessionType</key><string>Aqua</string>
        </dict></plist>
        """
        try plist.write(to: path, atomically: true, encoding: .utf8)
    }

    func removeLoginAgent() {
        try? FileManager.default.removeItem(at: loginAgentPath())
    }

    func migrateLoginItem() {
        guard #available(macOS 13.0, *) else { return }
        guard FileManager.default.fileExists(atPath: loginAgentPath().path) else { return }
        do {
            if SMAppService.mainApp.status != .enabled {
                try SMAppService.mainApp.register()
            }
            removeLoginAgent()
            log("开机启动已登记到系统登录项")
        } catch {
            log("系统登录项不可用，继续用 LaunchAgent：\(error.localizedDescription)")
        }
    }

    func setLaunchAtLogin(_ on: Bool) {
        if #available(macOS 13.0, *) {
            do {
                if on {
                    if SMAppService.mainApp.status != .enabled {
                        try SMAppService.mainApp.register()
                    }
                    removeLoginAgent()
                } else {
                    try SMAppService.mainApp.unregister()
                    removeLoginAgent()
                }
                log("开机自动启动：\(on ? "开" : "关")（系统登录项）")
                rebuildMenu()
                return
            } catch {
                log("系统登录项不可用，改用 LaunchAgent：\(error.localizedDescription)")
            }
        }
        do {
            if on {
                try writeLoginAgent()
                log("开机自动启动：开（LaunchAgent）")
            } else {
                removeLoginAgent()
                log("开机自动启动：关（LaunchAgent）")
            }
        } catch {
            log("开机自动启动写入失败：\(error.localizedDescription)")
        }
        rebuildMenu()
    }

    func updateStatusIcon() {
        let size = NSSize(width: 22, height: 18)
        let img = NSImage(size: size)
        img.lockFocus()

        if let base = NSImage(systemSymbolName: "server.rack", accessibilityDescription: "远程 Harness 服务端") {
            let cfg = NSImage.SymbolConfiguration(pointSize: 14, weight: .regular)
                .applying(.init(paletteColors: [.labelColor]))
            if let sym = base.withSymbolConfiguration(cfg) {
                let r = NSRect(x: 3, y: 2, width: 17, height: 16)
                sym.draw(in: r, from: .zero, operation: .sourceOver, fraction: 1)
            }
        }

        // 右下角指示灯：绿=全通，黄=到服务器通但 Harness 没拿到 token，红=掉线
        var color = NSColor.systemRed
        if hub != nil {
            color = cfg.dshURL.isEmpty ? .systemYellow : .systemGreen
        }
        let dotRect = NSRect(x: 13.5, y: 2.0, width: 7.5, height: 7.5)
        let path = NSBezierPath(ovalIn: dotRect)
        NSGradient(starting: color.blended(withFraction: 0.55, of: .white) ?? color, ending: color)?
            .draw(in: path, angle: -70)
        color.setStroke()
        path.lineWidth = 0.7
        path.stroke()
        // 高光
        NSColor.white.withAlphaComponent(0.9).setFill()
        NSBezierPath(ovalIn: NSRect(x: dotRect.minX + 1.8, y: dotRect.maxY - 2.8, width: 2.2, height: 2.2)).fill()

        img.unlockFocus()
        img.isTemplate = false
        statusItem.button?.image = img
        statusItem.button?.toolTip = "远程 Harness 服务端 · \(state)"
    }

    func setState(_ s: String) {
        if state == s { return }
        state = s
        DispatchQueue.main.async {
            self.rebuildMenu()
            self.updateStatusIcon()
        }
        log("state=\(s)")
    }

    // MARK: 登录

    @objc func openLogin() {
        if let w = loginWindow { w.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true); return }
        let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 380, height: 230),
                         styleMask: [.titled, .closable], backing: .buffered, defer: false)
        w.title = "登录 Remote Harness"
        w.isReleasedWhenClosed = false
        w.delegate = self
        let v = NSView(frame: w.contentView!.bounds)

        func label(_ s: String, _ y: CGFloat) -> NSTextField {
            let t = NSTextField(labelWithString: s)
            t.frame = NSRect(x: 24, y: y, width: 330, height: 18)
            t.textColor = .secondaryLabelColor
            t.font = .systemFont(ofSize: 12)
            return t
        }

        v.addSubview(label("用户名", 176))
        userField = NSTextField(frame: NSRect(x: 24, y: 148, width: 332, height: 24))
        userField.placeholderString = "手机和电脑用同一个账号"
        v.addSubview(userField)

        v.addSubview(label("密码", 112))
        passField = NSSecureTextField(frame: NSRect(x: 24, y: 84, width: 332, height: 24))
        passField.placeholderString = "至少 4 位"
        v.addSubview(passField)

        let login = NSButton(title: "登录", target: self, action: #selector(doLogin))
        login.frame = NSRect(x: 24, y: 38, width: 332, height: 30)
        login.bezelStyle = .rounded
        login.keyEquivalent = "\r"
        v.addSubview(login)

        loginStatus = NSTextField(labelWithString: "首次登录会自动创建这个账号")
        loginStatus.frame = NSRect(x: 24, y: 12, width: 332, height: 18)
        loginStatus.textColor = .secondaryLabelColor
        loginStatus.font = .systemFont(ofSize: 11.5)
        v.addSubview(loginStatus)

        w.contentView = v
        w.center()
        loginWindow = w
        w.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    @objc func doLogin() {
        let user = userField.stringValue.trimmingCharacters(in: .whitespaces)
        let pass = passField.stringValue
        guard user.count > 0, pass.count >= 4 else { loginStatus.stringValue = "用户名和密码都要填"; return }
        loginStatus.stringValue = "登录中…"
        hubAPI("/_hub/api/login/password", body: ["user": user, "pass": pass]) { obj, err in
            if let err = err {
                DispatchQueue.main.async { self.loginStatus.stringValue = "失败：\(err.localizedDescription)" }
                return
            }
            guard let token = obj?["token"] as? String else { return }
            self.cfg.token = token
            if self.cfg.deviceName.isEmpty || self.cfg.deviceName == "我的 Mac" {
                self.cfg.deviceName = deviceMeta().name
            }
            log("登录成功")
            self.ensureDevice { ok in
                DispatchQueue.main.async {
                    self.loginStatus.stringValue = ok ? "登录成功" : "登录成功，但设备创建失败"
                    if ok {
                        self.loginWindow?.close()
                        self.connect()
                    }
                }
            }
        }
    }

    func hubGET(_ path: String, token: String, done: @escaping ([String: Any]?, Error?) -> Void) {
        guard let url = URL(string: cfg.hub + path) else { return }
        var req = URLRequest(url: url)
        req.setValue("Bearer " + token, forHTTPHeaderField: "Authorization")
        hubSession.dataTask(with: req) { data, resp, err in
            if let err = err { done(nil, err); return }
            guard let data = data,
                  let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
                done(nil, NSError(domain: "hub", code: 0, userInfo: [NSLocalizedDescriptionKey: "返回不是 JSON"]))
                return
            }
            if let code = (resp as? HTTPURLResponse)?.statusCode, code >= 400 {
                done(nil, NSError(domain: "hub", code: code, userInfo: [NSLocalizedDescriptionKey: (obj["error"] as? String) ?? "HTTP \(code)"]))
                return
            }
            done(obj, nil)
        }.resume()
    }

    func hubAPI(_ path: String, body: [String: Any], token: String? = nil, done: @escaping ([String: Any]?, Error?) -> Void) {
        guard let url = URL(string: cfg.hub + path) else { return }
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        if let t = token { req.setValue("Bearer " + t, forHTTPHeaderField: "Authorization") }
        req.httpBody = try? JSONSerialization.data(withJSONObject: body)
        hubSession.dataTask(with: req) { data, resp, err in
            if let err = err { done(nil, err); return }
            guard let data = data,
                  let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
                done(nil, NSError(domain: "hub", code: 0, userInfo: [NSLocalizedDescriptionKey: "返回不是 JSON"]))
                return
            }
            let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
            if code >= 400 {
                done(nil, NSError(domain: "hub", code: code,
                                  userInfo: [NSLocalizedDescriptionKey: (obj["error"] as? String) ?? "HTTP \(code)"]))
                return
            }
            done(obj, nil)
        }.resume()
    }

    func ensureDevice(done: @escaping (Bool) -> Void) {
        let m = deviceMeta()
        hubAPI("/_hub/api/devices", body: [
            "name": cfg.deviceName.isEmpty ? m.name : cfg.deviceName,
            "os": "darwin", "kind": "mac", "model": m.model, "chip": m.chip, "osVersion": m.osv,
        ], token: cfg.token) { obj, err in
            if let err = err {
                log("建设备失败 \(err.localizedDescription)")
                done(false); return
            }
            if let d = obj?["device"] as? [String: Any], let id = d["id"] as? String {
                self.cfg.deviceId = id
                saveConfig(self.cfg)
                log("设备 \(id)")
                self.pushDeviceMeta(id)
                done(true)
            } else { done(false) }
        }
    }

    @objc func logout() {
        disconnect()
        cfg.token = ""; cfg.deviceId = ""
        saveConfig(cfg)
        setState("未登录")
        rebuildMenu()
    }

    // MARK: 连接

    @objc func connect() {
        guard !cfg.token.isEmpty else { openLogin(); return }
        guard !cfg.deviceId.isEmpty else { ensureDevice { _ in self.connect() }; return }
        guard let url = URL(string: cfg.hub.replacingOccurrences(of: "https://", with: "wss://")
                            .replacingOccurrences(of: "http://", with: "ws://")
                            + "/agent?token=" + (cfg.token.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? "")) else { return }

        generation += 1
        hub?.cancel(with: .goingAway, reason: nil)
        let t = hubSession.webSocketTask(with: url)
        hub = t
        t.resume()
        send(["t": "hello", "deviceId": cfg.deviceId])
        setState("在线")
        DispatchQueue.global().asyncAfter(deadline: .now() + 0.8) {
            if !self.cfg.dshURL.isEmpty, !self.verify(self.cfg.dshURL) {
                log("旧入口已失效，自动清除")
                self.cfg.dshURL = ""
                saveConfig(self.cfg)
                self.dshCookies = [:]
            }
            self.reportState()
        }
        if !cfg.deviceId.isEmpty { pushDeviceMeta(cfg.deviceId) }
        receiveHub(t)
    }

    @objc func disconnect() {
        hub?.cancel(with: .goingAway, reason: nil)
        hub = nil
        setState("已断开")
    }

    func reportState() {
        DispatchQueue.global().async {
            let ok = !self.cfg.dshURL.isEmpty && self.verify(self.cfg.dshURL)
            self.send(["t": "state", "dsh": ok])
        }
    }

    func send(_ obj: [String: Any]) {
        guard let hub = hub else { return }
        guard let data = try? JSONSerialization.data(withJSONObject: obj),
              let s = String(data: data, encoding: .utf8) else { return }
        hub.send(.string(s)) { err in
            if let err = err { log("send 失败 \(err.localizedDescription)") }
        }
    }

    func receiveHub(_ t: URLSessionWebSocketTask) {
        let gen = generation
        t.receive { result in
            switch result {
            case .failure(let e):
                log("hub 断开 \(e.localizedDescription)")
                if self.hub === t {
                    self.hub = nil
                    self.setState("已断开")
                    DispatchQueue.main.asyncAfter(deadline: .now() + 3) {
                        if gen == self.generation { self.connect() }
                    }
                }
            case .success(let msg):
                self.setState("在线")
                let data: Data
                switch msg {
                case .string(let s): data = s.data(using: .utf8)!
                case .data(let d): data = d
                @unknown default: data = Data()
                }
                if let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
                    self.dispatch(obj)
                }
                self.receiveHub(t)
            }
        }
    }

    func dispatch(_ f: [String: Any]) {
        let t = f["t"] as? String ?? ""
        switch t {
        case "req": forwardHTTP(f)
        case "ws-open": openLocalWS(f)
        case "ws-msg": feedLocalWS(f)
        case "ws-close": closeLocalWS(f)
        case "takeover":
            handleTakeover(f)
        case "wake":
            // 手机点进来一次 = 允许尝试启动一次
            launchBudget = true
            ensureDSH { _, _ in }
        case "error": log("hub 错误：\(f["error"] ?? "")")
        case "agent": break
        default: log("未知帧 \(t)")
        }
    }

    // MARK: 本机 DSH

    struct DSHBase { var scheme = "http"; var host = "127.0.0.1"; var port = 0; var token = "" }

    func dshBase() -> DSHBase? {
        guard let c = URLComponents(string: cfg.dshURL), let h = c.host, let p = c.port else { return nil }
        var b = DSHBase(); b.scheme = c.scheme ?? "http"; b.host = h; b.port = p
        b.token = c.queryItems?.first(where: { $0.name == "token" })?.value ?? ""
        return b
    }

    func localURL(_ path: String, _ b: DSHBase, withToken: Bool = true) -> URL? {
        var p = path.hasPrefix("/") ? path : "/" + path
        if withToken && !b.token.isEmpty && !p.contains("token=") {
            p += (p.contains("?") ? "&" : "?") + "token=" + b.token
        }
        return URL(string: "\(b.scheme)://\(b.host):\(b.port)\(p)")
    }

    func forwardHTTP(_ f: [String: Any]) {
        ensureDSH { ok, code in
            if !ok {
                let id = f["id"] as? String ?? ""
                let msg = code == "dsh_unreadable"
                    ? "电脑上的 Harness 正在运行，但客户端读不到它的链接，请点「接管 DSH」。"
                    : "电脑上的 Harness 没有运行，客户端已尝试启动但没等到它。"
                self.send(["t": "res", "id": id, "status": 503, "code": code,
                           "headers": ["content-type": "application/json"],
                           "body": b64(try! JSONSerialization.data(withJSONObject: ["ok": false, "error": code, "message": msg]))])
                return
            }
            self.forwardHTTPNow(f)
        }
    }

    func cookieHeaderValue() -> String {
        dshCookies.map { "\($0.key)=\($0.value)" }.joined(separator: "; ")
    }

    func absorbSetCookie(_ headers: [AnyHashable: Any]) {
        for (k, v) in headers {
            guard let name = k as? String, name.lowercased() == "set-cookie" else { continue }
            let values: [String]
            if let arr = v as? [String] { values = arr } else if let s = v as? String { values = [s] } else { continue }
            for raw in values {
                let first = raw.split(separator: ";").first.map(String.init) ?? ""
                let parts = first.split(separator: "=", maxSplits: 1).map(String.init)
                guard parts.count == 2 else { continue }
                let key = parts[0].trimmingCharacters(in: .whitespaces)
                if key.hasPrefix("dsh-") {
                    dshCookies[key] = parts[1]
                }
            }
        }
    }

    func stripHubCookie(_ raw: String) -> String {
        raw.split(separator: ";")
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.hasPrefix("rh_sess=") && !$0.hasPrefix("dsh-") }
            .joined(separator: "; ")
    }

    func forwardHTTPNow(_ f: [String: Any], forceToken: Bool = false) {
        guard let b = dshBase(), let id = f["id"] as? String,
              let method = f["method"] as? String, let path = f["path"] as? String else {
            return
        }
        let rawHeaders = f["headers"] as? [String: String] ?? [:]
        var cookie = ""
        for (k, v) in rawHeaders where k.lowercased() == "cookie" { cookie = stripHubCookie(v) }
        let mine = forceToken ? "" : cookieHeaderValue()
        let needToken = mine.isEmpty
        guard let url = localURL(path, b, withToken: needToken) else {
            send(["t": "res", "id": f["id"] ?? "", "status": 502, "body": b64("bad local url".data(using: .utf8)!)])
            return
        }
        var req = URLRequest(url: url)
        req.httpMethod = method
        for (k, v) in rawHeaders {
            let lk = k.lowercased()
            if ["host", "content-length", "connection", "accept-encoding", "cookie"].contains(lk) { continue }
            req.setValue(v, forHTTPHeaderField: k)
        }
        if !mine.isEmpty {
            req.setValue(mine, forHTTPHeaderField: "Cookie")
        } else if !cookie.isEmpty {
            req.setValue(cookie, forHTTPHeaderField: "Cookie")
        }
        // 始终改写成与 Host 一致的本机 Origin，否则 DSH 的写操作会 403
        let localOrigin = "\(b.scheme)://\(b.host):\(b.port)"
        req.setValue(localOrigin, forHTTPHeaderField: "Origin")
        if req.value(forHTTPHeaderField: "Referer") != nil {
            req.setValue(localOrigin + "/", forHTTPHeaderField: "Referer")
        }
        if let body = f["body"] as? String, !body.isEmpty { req.httpBody = unb64(body) }
        localSession.dataTask(with: req) { data, resp, err in
            if let err = err {
                self.send(["t": "res", "id": id, "status": 502, "body": b64("local error: \(err.localizedDescription)".data(using: .utf8)!)])
                return
            }
            let http = resp as? HTTPURLResponse
            var headers: [String: String] = [:]
            for (k, v) in (http?.allHeaderFields as? [String: Any]) ?? [:] {
                if let s = v as? String { headers[k] = s }
            }
            self.absorbSetCookie((http?.allHeaderFields as? [AnyHashable: Any]) ?? [:])
            // 302/303 且还带着自己的 cookie：说明这个 cookie 不被当前实例接受，
            // 清掉重试一次（用 token 换新 cookie），否则浏览器会陷入跳转循环
            if let code = http?.statusCode, code == 302 || code == 303,
               !forceToken, !self.cookieHeaderValue().isEmpty {
                self.dshCookies = [:]
                self.forwardHTTPNow(f, forceToken: true)
                return
            }
            headers.removeValue(forKey: "Content-Encoding")
            headers.removeValue(forKey: "Transfer-Encoding")
            self.send(["t": "res", "id": id, "status": http?.statusCode ?? 200,
                       "headers": headers, "body": b64(data ?? Data())])
        }.resume()
    }

    var dshCookies: [String: String] = [:]
    var launching = false
    var launchBudget = true

    func dshAppRunning() -> Bool {
        !NSRunningApplication.runningApplications(withBundleIdentifier: "com.deepseek.dsh").isEmpty
    }

    func launchDSH() {
        // 客户端可能是从 Electron/终端环境里启动的，会带 ELECTRON_RUN_AS_NODE=1；
        // 传给 Harness 会让它以 Node 模式跑，直接报 "bad option"。
        unsetenv("ELECTRON_RUN_AS_NODE")
        let exe = "/Applications/DeepSeek Harness.app/Contents/MacOS/DeepSeek Harness"
        guard FileManager.default.fileExists(atPath: exe) else { return }
        try? FileManager.default.removeItem(at: dshLogPath())
        FileManager.default.createFile(atPath: dshLogPath().path, contents: nil)
        let logFD = open(dshLogPath().path, O_WRONLY | O_CREAT | O_TRUNC, 0o644)

        // 直接带 --remote-debugging-port 启动，保证能读到带 token 的地址。
        var attr: posix_spawnattr_t?
        posix_spawnattr_init(&attr)
        var fileActions: posix_spawn_file_actions_t?
        posix_spawn_file_actions_init(&fileActions)
        if logFD >= 0 {
            posix_spawn_file_actions_adddup2(&fileActions, logFD, 1)
            posix_spawn_file_actions_adddup2(&fileActions, logFD, 2)
        }

        var argv: [UnsafeMutablePointer<CChar>?] = [
            strdup(exe), strdup("--remote-debugging-port=9222"), nil
        ]
        var pid: pid_t = 0
        let rc = posix_spawn(&pid, exe, &fileActions, &attr, &argv, environ)
        posix_spawn_file_actions_destroy(&fileActions)
        posix_spawnattr_destroy(&attr)
        if logFD >= 0 { close(logFD) }
        if rc == 0 {
            log("已启动 DSH（pid=\(pid)）")
        } else {
            log("posix_spawn 失败 rc=\(rc)，回退到 Process")
            let p = Process()
            p.executableURL = URL(fileURLWithPath: exe)
            p.arguments = ["--remote-debugging-port=9222"]
            var env = ProcessInfo.processInfo.environment
            env.removeValue(forKey: "ELECTRON_RUN_AS_NODE")
            p.environment = env
            if let h = try? FileHandle(forWritingTo: dshLogPath()) {
                p.standardOutput = h; p.standardError = h
            }
            try? p.run()
        }
    }

    /// 彻底等到 Harness 退出（最多 timeout 秒），必要时强杀
    @discardableResult
    func waitDSHExit(timeout: TimeInterval = 15) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if NSRunningApplication.runningApplications(withBundleIdentifier: "com.deepseek.dsh").isEmpty {
                return true
            }
            usleep(100_000)
        }
        for app in NSRunningApplication.runningApplications(withBundleIdentifier: "com.deepseek.dsh") {
            app.forceTerminate()
        }
        let hard = Date().addingTimeInterval(5)
        while Date() < hard {
            if NSRunningApplication.runningApplications(withBundleIdentifier: "com.deepseek.dsh").isEmpty {
                return true
            }
            usleep(200_000)
        }
        return false
    }

    // 只在有请求时调用：本机可用就直接放行；否则只启动一次，最多等 20 秒
    func ensureDSH(_ done: @escaping (Bool, String) -> Void) {
        if !cfg.dshURL.isEmpty, verify(cfg.dshURL) { done(true, ""); return }
        if let u = urlFromLog() ?? cdpURL(), verify(u) {
            cfg.dshURL = u; saveConfig(cfg); done(true, ""); return
        }
        if launching { done(false, "dsh_starting"); return }
        if dshAppRunning() { done(false, "dsh_unreadable"); return }
        if !launchBudget { done(false, "dsh_not_running"); return }
        launchBudget = false

        launching = true
        launchDSH()
        DispatchQueue.global().async {
            for _ in 0..<40 {
                usleep(500_000)
                if let u = self.urlFromLog() ?? self.cdpURL(), self.verify(u) {
                    self.cfg.dshURL = u
                    saveConfig(self.cfg)
                    self.launching = false
                    DispatchQueue.main.async { done(true, "") }
                    return
                }
            }
            self.launching = false
            DispatchQueue.main.async { done(false, "dsh_not_running") }
        }
    }

    func openLocalWS(_ f: [String: Any]) {
        ensureDSH { ok, code in
            if !ok {
                self.send(["t": "ws-open-err", "id": f["id"] ?? "", "code": code])
                return
            }
            self.openLocalWSNow(f)
        }
    }

    func openLocalWSNow(_ f: [String: Any]) {
        guard let b = dshBase(), let id = f["id"] as? String, let path = f["path"] as? String else { return }
        var p = path.hasPrefix("/") ? path : "/" + path
        if !b.token.isEmpty && !p.contains("token=") { p += (p.contains("?") ? "&" : "?") + "token=" + b.token }
        guard let url = URL(string: "ws://\(b.host):\(b.port)\(p)") else { return }
        var req = URLRequest(url: url)
        if let ck = f["cookie"] as? String, !ck.isEmpty {
            req.setValue(ck, forHTTPHeaderField: "Cookie")
        }
        let t = localSession.webSocketTask(with: req)
        wsTasks[id] = t
        t.resume()
        send(["t": "ws-open-ok", "id": id])
        receiveLocalWS(t, id: id)
    }

    func receiveLocalWS(_ t: URLSessionWebSocketTask, id: String) {
        t.receive { result in
            switch result {
            case .failure(let e):
                log("本地 ws 断开 \(id): \(e.localizedDescription)")
                self.wsTasks.removeValue(forKey: id)
                self.send(["t": "ws-close", "id": id])
            case .success(let msg):
                let data: Data
                var kind = "binary"
                switch msg {
                case .string(let s): data = s.data(using: .utf8)!; kind = "text"
                case .data(let d): data = d
                @unknown default: data = Data()
                }
                self.send(["t": "ws-msg", "id": id, "kind": kind, "data": b64(data)])
                self.receiveLocalWS(t, id: id)
            }
        }
    }

    func feedLocalWS(_ f: [String: Any]) {
        guard let id = f["id"] as? String, let t = wsTasks[id], let s = f["data"] as? String else { return }
        let kind = f["kind"] as? String ?? "binary"
        let raw = unb64(s)
        let msg: URLSessionWebSocketTask.Message = (kind == "text")
            ? .string(String(data: raw, encoding: .utf8) ?? "")
            : .data(raw)
        t.send(msg) { err in
            if let err = err { log("本地 ws 发送失败 \(id): \(err.localizedDescription)") }
        }
    }

    func closeLocalWS(_ f: [String: Any]) {
        guard let id = f["id"] as? String, let t = wsTasks.removeValue(forKey: id) else { return }
        t.cancel(with: .normalClosure, reason: nil)
    }

    // MARK: 检测 / 设置

    @objc func detectDSH() {
        if let u = urlFromLog() ?? cdpURL(), verify(u) {
            cfg.dshURL = u
            saveConfig(cfg)
            setState("已读到 DSH 链接")
            return
        }
        let candidates = [
            NSHomeDirectory() + "/Library/Logs/dsh-desktop-app.log",
            NSHomeDirectory() + "/Library/Logs/RemoteHarness/dsh.log",
        ]
        for path in candidates {
            guard let text = try? String(contentsOfFile: path, encoding: .utf8) else { continue }
            let lines = text.components(separatedBy: "\n").filter { $0.contains("dsh web: http") }
            guard let last = lines.last,
                  let range = last.range(of: "http://") else { continue }
            var url = String(last[range.lowerBound...]).trimmingCharacters(in: .whitespaces)
            if let q = url.range(of: " ") { url = String(url[..<q.lowerBound]) }
            if verify(url) {
                cfg.dshURL = url
                saveConfig(cfg)
                setState("已检测到 DSH")
                return
            }
        }
        setState("没找到 DSH（可手动设置地址）")
    }

    // 从 Chromium 调试端口读当前页面地址（含 token），不需要重启
    func cdpURL() -> String? {
        guard let u = URL(string: "http://127.0.0.1:9222/json") else { return nil }
        let sem = DispatchSemaphore(value: 0)
        var found: String?
        var req = URLRequest(url: u)
        req.timeoutInterval = 2
        localSession.dataTask(with: req) { data, _, _ in
            defer { sem.signal() }
            guard let data = data,
                  let arr = try? JSONSerialization.jsonObject(with: data) as? [[String: Any]] else { return }
            for item in arr {
                guard let t = item["url"] as? String,
                      t.contains("127.0.0.1"), t.contains("token=") else { continue }
                found = t
                break
            }
        }.resume()
        _ = sem.wait(timeout: .now() + 3)
        return found
    }

    func urlFromLog() -> String? {
        guard let text = try? String(contentsOf: dshLogPath(), encoding: .utf8) else { return nil }
        let lines = text.components(separatedBy: "\n").filter { $0.contains("dsh web: http") }
        guard let last = lines.last, let r = last.range(of: "http") else { return nil }
        var u = String(last[r.lowerBound...]).trimmingCharacters(in: .whitespaces)
        if let sp = u.range(of: " ") { u = String(u[..<sp.lowerBound]) }
        return u
    }

    func verify(_ urlString: String) -> Bool {
        guard let u = URL(string: urlString) else { return false }
        let sem = DispatchSemaphore(value: 0)
        var ok = false
        var req = URLRequest(url: u)
        req.timeoutInterval = 3
        localSession.dataTask(with: req) { _, resp, _ in
            if let code = (resp as? HTTPURLResponse)?.statusCode, code < 500 { ok = true }
            sem.signal()
        }.resume()
        _ = sem.wait(timeout: .now() + 4)
        return ok
    }

    @objc func editDSH() {
        let a = NSAlert()
        a.messageText = "本机 DSH 地址"
        a.informativeText = "带 token 的完整地址，例如\nhttp://127.0.0.1:57073/?token=…"
        let f = NSTextField(frame: NSRect(x: 0, y: 0, width: 420, height: 24))
        f.stringValue = cfg.dshURL
        a.accessoryView = f
        a.addButton(withTitle: "保存")
        a.addButton(withTitle: "取消")
        if a.runModal() == .alertFirstButtonReturn {
            cfg.dshURL = f.stringValue.trimmingCharacters(in: .whitespaces)
            saveConfig(cfg)
            setState("DSH 地址已保存")
        }
    }

    func dshLogPath() -> URL {
        logPath().deletingLastPathComponent().appendingPathComponent("dsh.log")
    }

    @objc func takeoverDSH() {
        let a = NSAlert()
        a.messageText = "接管 DSH？"
        a.informativeText = "会先退出正在运行的 DeepSeek Harness，再重新启动它并把输出写到本地日志，以便拿到带 token 的地址。当前窗口里的对话会被中断。"
        a.addButton(withTitle: "接管")
        a.addButton(withTitle: "取消")
        guard a.runModal() == .alertFirstButtonReturn else { return }

        for app in NSRunningApplication.runningApplications(withBundleIdentifier: "com.deepseek.dsh") {
            _ = app.terminate()
        }
        let deadline = Date().addingTimeInterval(5)
        while Date() < deadline,
              !NSRunningApplication.runningApplications(withBundleIdentifier: "com.deepseek.dsh").isEmpty {
            usleep(200_000)
        }

        launchDSH()
        setState("等待 DSH 启动…")

        DispatchQueue.global().async {
            let until = Date().addingTimeInterval(30)
            while Date() < until {
                if let url = self.urlFromLog() ?? self.cdpURL(), self.verify(url) {
                    self.cfg.dshURL = url
                    saveConfig(self.cfg)
                    DispatchQueue.main.async {
                        self.setState("已接管 DSH")
                        self.connect()
                    }
                    return
                }
                usleep(400_000)
            }
            DispatchQueue.main.async { self.setState("30 秒内没等到 DSH 链接") }
        }
    }

    func handleTakeover(_ f: [String: Any]) {
        let id = f["id"] as? String ?? ""
        log("收到接管请求")
        // 已经有可用链接：直接成功
        if !cfg.dshURL.isEmpty, verify(cfg.dshURL) {
            send(["t": "takeover-ok", "id": id])
            return
        }
        setState("接管中…")
        DispatchQueue.global().async {
            for app in NSRunningApplication.runningApplications(withBundleIdentifier: "com.deepseek.dsh") {
                app.terminate()
            }
            guard self.waitDSHExit() else {
                log("接管失败：Harness 没退出")
                self.send(["t": "takeover-fail", "id": id])
                DispatchQueue.main.async { self.setState("接管失败：旧进程没退出") }
                return
            }
            self.launchDSH()
            for _ in 0..<80 {   // 最多 40 秒
                usleep(500_000)
                if let u = self.urlFromLog() ?? self.cdpURL(), self.verify(u) {
                    self.cfg.dshURL = u
                    saveConfig(self.cfg)
                    self.dshCookies = [:]
                    self.launchBudget = true
                    self.send(["t": "takeover-ok", "id": id])
                    self.reportState()
                    DispatchQueue.main.async { self.setState("已接管 DSH") }
                    return
                }
            }
            log("接管失败：40 秒内没读到链接")
            self.send(["t": "takeover-fail", "id": id])
            DispatchQueue.main.async { self.setState("接管失败") }
        }
    }

    var panel: NSWindow?
    var panelTimer: Timer?
    let panelModel = PanelModel()

    @objc func showPanel() {
        if let w = panel {
            w.makeKeyAndOrderFront(nil)
            NSApp.activate(ignoringOtherApps: true)
            refreshPanel()
            return
        }
        let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 460, height: 640),
                         styleMask: [.titled, .closable, .miniaturizable],
                         backing: .buffered, defer: false)
        w.title = "远程 Harness 服务端"
        w.isMovableByWindowBackground = true
        w.isReleasedWhenClosed = false

        let view = PanelView(model: panelModel,
                             onTakeover: { self.takeoverNow() },
                             onLogout: { self.logout() },
                             onRenew: {
                                 if let u = URL(string: defaultHub + "/_hub/member") {
                                     NSWorkspace.shared.open(u)
                                 }
                             },
                             )
        w.contentView = NSHostingView(rootView: view)
        w.center()
        panel = w
        w.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)

        refreshPanel()
        panelTimer?.invalidate()
        panelTimer = Timer.scheduledTimer(withTimeInterval: 5, repeats: true) { [weak self] _ in
            self?.refreshPanel()
        }
    }

    func refreshPanel() {
        guard panel != nil else { return }
        if Thread.isMainThread {
            self.applyPanelModel()
        } else {
            DispatchQueue.main.async { self.applyPanelModel() }
        }
    }

    func applyPanelModel() {
        panelModel.name = cfg.token.isEmpty ? "未登录" : cfg.deviceName
        panelModel.online = hub != nil
        panelModel.ready = !cfg.dshURL.isEmpty
        updateStatusIcon()
        panelModel.serverOK = hub != nil
        panelModel.harnessRunning = !NSRunningApplication
            .runningApplications(withBundleIdentifier: "com.deepseek.dsh").isEmpty
        guard !cfg.token.isEmpty else { return }
        hubGET("/_hub/api/membership", token: cfg.token) { obj, err in
            guard err == nil, let obj = obj else { return }
            let active = (obj["active"] as? Bool) ?? false
            let days = (obj["days_left"] as? Int) ?? 0
            let until = obj["until"] as? String ?? ""
            DispatchQueue.main.async {
                self.panelModel.memberActive = active
                self.panelModel.memberDays = days
                self.panelModel.memberUntil = until
            }
        }
        hubGET("/_hub/api/devices", token: cfg.token) { obj, err in
            guard err == nil, let list = obj?["devices"] as? [[String: Any]] else { return }
            var out: [DevVM] = []
            for d in list {
                let name = d["name"] as? String ?? "设备"
                var meta = [d["model"] as? String ?? "", d["chip"] as? String ?? "",
                            d["os_version"] as? String ?? ""].filter { !$0.isEmpty }.joined(separator: " · ")
                if (d["kind"] as? String) == "web" {
                    meta = "网页登录"
                    if let ls = d["last_seen"] as? String, ls.count >= 16 {
                        let t = ls.dropFirst(11).prefix(5)
                        meta += " · 最后在线 \(t)"
                    }
                }
                out.append(DevVM(id: d["id"] as? String ?? name, name: name, meta: meta,
                                 online: (d["online"] as? Bool) ?? false))
            }
            DispatchQueue.main.async { self.panelModel.devices = out }
        }
    }

    func pushDeviceMeta(_ id: String) {
        let m = deviceMeta()
        guard let url = URL(string: cfg.hub + "/_hub/api/devices/" + id) else { return }
        var req = URLRequest(url: url)
        req.httpMethod = "PUT"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer " + cfg.token, forHTTPHeaderField: "Authorization")
        req.httpBody = try? JSONSerialization.data(withJSONObject: [
            "name": cfg.deviceName, "kind": "mac", "model": m.model, "chip": m.chip, "osVersion": m.osv,
        ])
        hubSession.dataTask(with: req) { _, _, _ in }.resume()
    }

    @objc func reconnect() {
        disconnect()
        connect()
    }

    @objc func startHarnessMenu() {
        takeoverNow()
    }

    @objc func takeoverNow() {
        let running = !NSRunningApplication
            .runningApplications(withBundleIdentifier: "com.deepseek.dsh").isEmpty
        if !running {
            startHarnessFresh()
        } else {
            handleTakeover(["id": "manual"])
        }
    }

    /// 直接启动 Harness（不重启），启动后等它打印出口令地址
    func startHarnessFresh() {
        setState("正在启动 Harness…")
        DispatchQueue.global().async {
            self.launchDSH()
            for _ in 0..<60 {           // 最多 30 秒
                usleep(500_000)
                if let u = self.urlFromLog() ?? self.cdpURL(), self.verify(u) {
                    self.cfg.dshURL = u
                    saveConfig(self.cfg)
                    self.reportState()
                    DispatchQueue.main.async {
                        self.setState("Harness 已启动")
                    }
                    return
                }
            }
            DispatchQueue.main.async { self.setState("启动失败：没读到链接") }
        }
    }

    @objc func quit() { NSApp.terminate(nil) }
}

// MARK: 入口

let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let delegate = AppDelegate()
app.delegate = delegate
app.run()
