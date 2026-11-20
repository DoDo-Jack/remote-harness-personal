import SwiftUI
import Photos
import AVFoundation
import WebKit
import LocalAuthentication
import Security

// MARK: - 钥匙串

// MARK: - 配置

struct AppConfig: Codable {
    var root: String = "http://127.0.0.1:7100"
}

extension AppConfig {
    var startURL: URL? {
        var s = root
        if s.hasSuffix("/") { s.removeLast() }
        return URL(string: s + "/")
    }
}

@MainActor
final class ConfigStore: ObservableObject {
    @Published var config: AppConfig
    private let key = "dsh.web.config"

    init() {
        var cfg = AppConfig()
        if let data = UserDefaults.standard.data(forKey: key),
           let decoded = try? JSONDecoder().decode(AppConfig.self, from: data) {
            cfg = decoded
        }
        // 安装时可用启动参数指定服务器
        let args = ProcessInfo.processInfo.arguments
        if let i = args.firstIndex(of: "-serverURL"), i + 1 < args.count {
            cfg.root = args[i + 1]
        }
        config = cfg
        persist()
    }

    private func persist() {
        if let data = try? JSONEncoder().encode(config) {
            UserDefaults.standard.set(data, forKey: key)
        }
    }

    func apply(root: String) {
        config = AppConfig(root: root)
        persist()
    }

    var isConfigured: Bool {
        !config.root.isEmpty && URL(string: config.root) != nil
    }
}

// MARK: - 权限（只申请真的会用到的三个）

enum Permissions {
    static let key = "dsh.permissions.asked"

    static func requestAll() {
        UserDefaults.standard.set(true, forKey: key)
        PHPhotoLibrary.requestAuthorization(for: .readWrite) { _ in }
        AVCaptureDevice.requestAccess(for: .video) { _ in }
        AVCaptureDevice.requestAccess(for: .audio) { _ in }
    }
}

// MARK: - 生物识别闸门

@MainActor
final class Lock: ObservableObject {
    @Published var unlocked = false
    @Published var authError: String?
    @Published var biometryName: String = "面容 ID"

    init() {
        let ctx = LAContext()
        var err: NSError?
        if ctx.canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: &err) {
            switch ctx.biometryType {
            case .faceID: biometryName = "面容 ID"
            case .touchID: biometryName = "触控 ID"
            case .opticID: biometryName = "光学 ID"
            default: biometryName = "生物识别"
            }
        }
    }

    /// 允许生物识别，失败退设备密码
    func unlock() {
        authError = nil
        let ctx = LAContext()
        ctx.localizedFallbackTitle = "输入设备密码"
        var err: NSError?
        guard ctx.canEvaluatePolicy(.deviceOwnerAuthentication, error: &err) else {
            authError = "这台设备没有设置面容 ID 或密码"
            return
        }
        ctx.evaluatePolicy(.deviceOwnerAuthentication,
                           localizedReason: "解锁 DeepSeek Harness") { ok, error in
            Task { @MainActor in
                if ok {
                    self.unlocked = true
                } else {
                    let code = (error as NSError?)?.code
                    self.authError = (code == LAError.userCancel.rawValue || code == LAError.userFallback.rawValue)
                        ? nil : "解锁失败，请重试"
                }
            }
        }
    }

    func lock() { unlocked = false; awaySince = nil }

    /// 离开 App 的时刻；切 App 不立刻上锁，超过宽限期才锁
    private var awaySince: Date?

    /// 熄屏/设备上锁：立刻上锁（不享受宽限期）
    func deviceLocked() { lock() }

    func enterBackground() { awaySince = Date() }

    func enterForeground() {
        guard let since = awaySince else { return }
        awaySince = nil
        if Date().timeIntervalSince(since) > Lock.graceSeconds { unlocked = false }
    }

    /// 切 App 后多久才重新要求验证
    static let graceSeconds: TimeInterval = 300
}

// MARK: - 入口

@main
struct DSHApp: App {
    @StateObject private var store = ConfigStore()
    @StateObject private var lock = Lock()
    @Environment(\.scenePhase) private var scenePhase

    var body: some Scene {
        WindowGroup {
            RootView()
                .preferredColorScheme(.dark)
                .environmentObject(store)
                .environmentObject(lock)
                .onChange(of: scenePhase) { _, phase in
                    guard store.isConfigured else { return }
                    // 切 App：不立刻上锁，只记时间；超过 5 分钟回来才要求验证
                    if phase == .background { lock.enterBackground() }
                    if phase == .active && !UserDefaults.standard.bool(forKey: Permissions.key) {
                        Permissions.requestAll()
                    }
                    if phase == .active { lock.enterForeground() }
                }
                .onReceive(NotificationCenter.default.publisher(
                    for: Notification.Name("UIApplicationProtectedDataDidBecomeUnavailable"))) { _ in
                    // 熄屏 / 设备上锁 —— 只有真正锁屏才会触发，切 App 不会
                    if store.isConfigured { lock.deviceLocked() }
                }
        }
    }
}

struct RootView: View {
    @EnvironmentObject var store: ConfigStore
    @EnvironmentObject var lock: Lock
    @State private var showSettings = false

    var body: some View {
        Group {
            if !store.isConfigured {
                SettingsScreen()
            } else if !lock.unlocked {
                LockScreen(showSettings: $showSettings)
            } else {
                WebScreen(showSettings: $showSettings)
            }
        }
        .animation(.easeInOut(duration: 0.18), value: lock.unlocked)
        .sheet(isPresented: $showSettings) { SettingsScreen() }
    }
}

extension Notification.Name {
    static let dshReload = Notification.Name("dsh.reload")
    static let dshLogout = Notification.Name("dsh.logout")
    static let dshShowDevices = Notification.Name("dsh.devices")
}

struct LockScreen: View {
    @EnvironmentObject var lock: Lock
    @EnvironmentObject var store: ConfigStore
    @Binding var showSettings: Bool
    @State private var tried = false

    var body: some View {
        VStack(spacing: 22) {
            Spacer()
            Image(systemName: "lock.shield.fill")
                .font(.system(size: 56))
                .foregroundStyle(.tint)
            Text("已锁定")
                .font(.title2.weight(.semibold))
            Text("用\(lock.biometryName)解锁后继续")
                .font(.callout)
                .foregroundStyle(.secondary)
            if let e = lock.authError {
                Text(e).font(.footnote).foregroundStyle(.red)
            }
            Spacer()
            Button {
                lock.unlock()
            } label: {
                Label("用\(lock.biometryName)解锁", systemImage: "faceid")
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 6)
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            .padding(.horizontal, 28)

            Button("打开设置") { showSettings = true }
                .font(.footnote)
                .foregroundStyle(.secondary)

            Text("远程 Harness")
                .font(.caption)
                .foregroundStyle(.tertiary)
                .padding(.top, 2)
                .padding(.bottom, 26)
        }
        .task {
            guard !tried else { return }
            tried = true
            try? await Task.sleep(nanoseconds: 250_000_000)
            lock.unlock()
        }
    }
}

// MARK: - 设置

struct SettingsScreen: View {
    @EnvironmentObject var store: ConfigStore
    @Environment(\.dismiss) private var dismiss
    @State private var confirmLogout = false
    @State private var serverText = ""

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("服务器地址", text: $serverText)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .onSubmit { store.apply(root: serverText) }
                    LabeledContent("登录方式", value: "在网页里用账号登录")
                } header: {
                    Text("连接")
                } footer: {
                    Text("电脑和手机用同一个账号。登录状态保存在本机，30 天内不用重登。")
                }

                Section {
                    Button("切换设备") {
                        NotificationCenter.default.post(name: .dshShowDevices, object: nil)
                        dismiss()
                    }
                    Button("重新加载页面") {
                        NotificationCenter.default.post(name: .dshReload, object: nil)
                        dismiss()
                    }
                    Button("退出登录", role: .destructive) {
                        confirmLogout = true
                    }
                }
            }
            .navigationTitle("设置")
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("完成") { dismiss() }
                }
            }
            .confirmationDialog("退出后需要重新登录", isPresented: $confirmLogout, titleVisibility: .visible) {
                Button("退出登录", role: .destructive) {
                    NotificationCenter.default.post(name: .dshLogout, object: nil)
                    dismiss()
                }
                Button("取消", role: .cancel) {}
            }
        }
    }
}

// MARK: - WebView 壳

struct WebScreen: View {
    @EnvironmentObject var store: ConfigStore
    @Binding var showSettings: Bool
    @StateObject private var model = WebModel()
    @State private var progress: Double = 0

    var body: some View {
        ZStack(alignment: .top) {
            WebView(model: model, config: store.config)
                .ignoresSafeArea()

            if model.state == nil && progress > 0 && progress < 1 {
                ProgressView(value: progress)
                    .progressViewStyle(.linear)
                    .frame(height: 2)
                    .padding(.top, 2)
            }

            if model.showControls {
                HStack(spacing: 10) {
                    if model.canGoBack {
                        circleButton("chevron.left") { model.goBack() }
                    }
                    Spacer()
                    circleButton("arrow.clockwise") { model.reload(store.config) }
                    circleButton("gearshape") { showSettings = true }
                }
                .padding(.horizontal, 16)
                .padding(.top, 8)
                .transition(.opacity.combined(with: .move(edge: .top)))
            }

            if let st = model.state {
                stateOverlay(st)
            }

            if let err = model.errorText {
                VStack(spacing: 12) {
                    Image(systemName: "wifi.exclamationmark").font(.system(size: 40)).foregroundStyle(.secondary)
                    Text(err).font(.callout).foregroundStyle(.secondary).multilineTextAlignment(.center)
                    Button("重试") { model.reload(store.config) }.buttonStyle(.borderedProminent)
                }
                .padding(24)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .background(.background)
            }
        }
        .environment(\.colorScheme, model.webTheme == "light" ? .light : .dark)
        .task { model.load(store.config) }
        .onReceive(NotificationCenter.default.publisher(for: .dshReload)) { _ in
            model.reload(store.config)
        }
        .onReceive(NotificationCenter.default.publisher(for: .dshLogout)) { _ in
            model.logout(store.config)
        }
        .onReceive(NotificationCenter.default.publisher(for: .dshShowDevices)) { _ in
            model.state = nil
            model.openDevices(store.config)
        }
        .onChange(of: model.estimatedProgress) { _, v in progress = v }
    }

    @ViewBuilder
    func stateOverlay(_ st: String) -> some View {
        let offline = st == "offline"
        let title = offline ? "电脑客户端没有运行" : (st == "dsh_unreadable" ? "Harness在运行，但读不到入口" : "Harness没有运行")
        let desc = offline
            ? "那台电脑上的「远程 Harness」没开，或者退出了。打开它之后这里会自动继续。"
            : "点下面的按钮，让电脑把Harness启动起来，通常十几秒。"
        VStack(spacing: 14) {
            Spacer()
            Image(systemName: offline ? "desktopcomputer.trianglebadge.exclamationmark" : "bolt.horizontal.circle")
                .font(.system(size: 44, weight: .light))
                .foregroundStyle(.secondary)
            Text(title).font(.system(size: 19, weight: .semibold)).multilineTextAlignment(.center)
            Text(desc).font(.system(size: 13.5)).foregroundStyle(.secondary)
                .multilineTextAlignment(.center).padding(.horizontal, 28)
            if !offline {
                Button {
                    takeoverNative()
                } label: {
                    Text("接管电脑上的Harness")
                        .font(.system(size: 15, weight: .semibold))
                        .frame(maxWidth: 260).padding(.vertical, 12)
                }
                .buttonStyle(.borderedProminent)
                .tint(.green)
                .padding(.top, 4)
            }
            Button("重新检查") { model.reload(store.config) }
                .font(.system(size: 13))
            Spacer()
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color(red: 0.04, green: 0.05, blue: 0.07).ignoresSafeArea())
    }

    func takeoverNative() {
        guard let base = URL(string: store.config.root) else { return }
        var req = URLRequest(url: base.appendingPathComponent("_hub/api/takeover"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "content-type")
        req.httpBody = Data("{}".utf8)
        URLSession.shared.dataTask(with: req) { _, _, _ in
            DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
                pollReady(20)
            }
        }.resume()
        // 立刻显示等待文案
    }

    func pollReady(_ left: Int) {
        guard left > 0, let base = URL(string: store.config.root) else { return }
        var req = URLRequest(url: base.appendingPathComponent("_hub/api/state"))
        req.setValue("application/json", forHTTPHeaderField: "accept")
        URLSession.shared.dataTask(with: req) { data, _, _ in
            var ready = false
            if let d = data, let obj = try? JSONSerialization.jsonObject(with: d) as? [String: Any] {
                ready = (obj["dsh_ready"] as? Bool) ?? false
            }
            DispatchQueue.main.async {
                if ready { model.reload(store.config); model.state = nil }
                else { DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { pollReady(left - 1) } }
            }
        }.resume()
    }

    func circleButton(_ symbol: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(size: 15, weight: .semibold))
                .foregroundStyle(.primary)
                .frame(width: 38, height: 38)
                .background(.ultraThinMaterial, in: Circle())
                .overlay(Circle().stroke(.white.opacity(0.12), lineWidth: 0.5))
        }
        .buttonStyle(.plain)
    }
}

@MainActor
final class WebModel: ObservableObject {
    @Published var canGoBack = false
    @Published var estimatedProgress: Double = 0
    @Published var errorText: String?
    weak var webView: WKWebView?
    private var loaded = false
    @Published var showControls = false
    @Published var webTheme = "dark"
    @Published var state: String? = nil   // offline / dsh_not_running / dsh_unreadable
    private var hideTask: DispatchWorkItem?

    /// 页面下拉到顶时把工具条请出来，4 秒后自动收起
    func scrollChanged(_ y: CGFloat) {
        if y < -30 {
            revealControls()
        } else if y > 60 && showControls {
            hideControls()
        }
    }

    func revealControls() {
        hideTask?.cancel()
        withAnimation(.easeOut(duration: 0.18)) { showControls = true }
        let t = DispatchWorkItem { [weak self] in
            withAnimation(.easeIn(duration: 0.2)) { self?.showControls = false }
        }
        hideTask = t
        DispatchQueue.main.asyncAfter(deadline: .now() + 4, execute: t)
    }

    func hideControls() {
        hideTask?.cancel()
        withAnimation(.easeIn(duration: 0.2)) { showControls = false }
    }

    func load(_ cfg: AppConfig) {
        NSLog("RH: load called loaded=\(loaded)")
        guard !loaded, let url = cfg.startURL else { return }
        loaded = true
        guard let webView else { return }
        errorText = nil
        webView.load(URLRequest(url: url))
    }

    func reload(_ cfg: AppConfig) {
        NSLog("RH: reload called")
        guard let webView, let url = cfg.startURL else { return }
        errorText = nil
        webView.load(URLRequest(url: url))
    }

    func goBack() { webView?.goBack() }

    func openDevices(_ cfg: AppConfig) {
        guard let base = URL(string: cfg.root) else { return }
        webView?.load(URLRequest(url: base.appendingPathComponent("_hub/devices")))
    }

    func logout(_ cfg: AppConfig) {
        loaded = false
        guard let webView, let url = cfg.startURL else { return }
        let store = webView.configuration.websiteDataStore
        store.removeData(ofTypes: WKWebsiteDataStore.allWebsiteDataTypes(), modifiedSince: .distantPast) {
            DispatchQueue.main.async {
                webView.load(URLRequest(url: url))
            }
        }
    }
}

struct WebView: UIViewRepresentable {
    @ObservedObject var model: WebModel
    let config: AppConfig

    func makeCoordinator() -> Coordinator { Coordinator(model: model, config: config) }

    func makeUIView(context: Context) -> WKWebView {
        NSLog("RH: makeUIView")
        let conf = WKWebViewConfiguration()
        conf.websiteDataStore = .default()
        conf.allowsInlineMediaPlayback = true

        let web = WKWebView(frame: .zero, configuration: conf)
        web.navigationDelegate = context.coordinator
        web.uiDelegate = context.coordinator
        web.allowsBackForwardNavigationGestures = true
        context.coordinator.observe(web)
        web.scrollView.delegate = context.coordinator
        let swipe = UISwipeGestureRecognizer(target: context.coordinator, action: #selector(Coordinator.twoFingerDown))
        swipe.numberOfTouchesRequired = 2
        swipe.direction = .down
        web.addGestureRecognizer(swipe)
        model.webView = web
        return web
    }

    func updateUIView(_ uiView: WKWebView, context: Context) {}

    final class Coordinator: NSObject, WKNavigationDelegate, WKUIDelegate, UIScrollViewDelegate {
        let model: WebModel
        let config: AppConfig
        private var obs: [NSKeyValueObservation] = []

        init(model: WebModel, config: AppConfig) {
            self.model = model
            self.config = config
        }

        func scrollViewDidScroll(_ scrollView: UIScrollView) {
            model.scrollChanged(scrollView.contentOffset.y + scrollView.adjustedContentInset.top)
        }

        @objc func twoFingerDown() {
            Task { @MainActor in model.revealControls() }
        }

        func readTheme(_ web: WKWebView) {
            web.evaluateJavaScript("localStorage.getItem('rh-theme')||'dark'") { value, _ in
                Task { @MainActor in
                    if let v = value as? String { self.model.webTheme = v }
                }
            }
        }

        func observe(_ web: WKWebView) {
            obs.append(web.observe(\.estimatedProgress, options: [.new]) { [weak self] w, _ in
                Task { @MainActor in self?.model.estimatedProgress = w.estimatedProgress }
            })
            obs.append(web.observe(\.canGoBack, options: [.new]) { [weak self] w, _ in
                Task { @MainActor in self?.model.canGoBack = w.canGoBack }
            })
        }

        func webView(_ webView: WKWebView,
                     didReceive challenge: URLAuthenticationChallenge,
                     completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) {
            completionHandler(.performDefaultHandling, nil)
        }

        func webView(_ webView: WKWebView,
                     decidePolicyFor action: WKNavigationAction,
                     decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
            guard let url = action.request.url else { return decisionHandler(.allow) }
            let host = URL(string: config.root)?.host ?? ""
            if let h = url.host, h != host, url.scheme == "http" || url.scheme == "https" {
                UIApplication.shared.open(url)
                return decisionHandler(.cancel)
            }
            decisionHandler(.allow)
        }

        func webView(_ webView: WKWebView,
                     decidePolicyFor navigationResponse: WKNavigationResponse,
                     decisionHandler: @escaping (WKNavigationResponsePolicy) -> Void) {
            if let http = navigationResponse.response as? HTTPURLResponse,
               let st = http.value(forHTTPHeaderField: "X-DSH-State"), st != "ok" {
                webView.stopLoading()
                Task { @MainActor in
                    self.model.state = st
                    self.model.estimatedProgress = 0
                }
                decisionHandler(.cancel)
                return
            }
            Task { @MainActor in self.model.state = nil }
            decisionHandler(.allow)
        }

        func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
            let e = error as NSError
            // 我们自己取消的导航（状态页拦截）会以 102 / -999 回来，不是真的失败
            if e.code == NSURLErrorCancelled { return }
            if e.domain == "WebKitErrorDomain" && e.code == 102 { return }
            Task { @MainActor in
                if self.model.state != nil { return }
                self.model.errorText = "连不上服务器：\(error.localizedDescription)"
            }
        }

        func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
            let e = error as NSError
            if e.code == NSURLErrorCancelled { return }
            if e.domain == "WebKitErrorDomain" && e.code == 102 { return }
            Task { @MainActor in
                if self.model.state != nil { return }
                self.model.errorText = "加载失败：\(error.localizedDescription)"
            }
        }

        func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
            Task { @MainActor in self.model.errorText = "页面进程被系统回收，点重试即可" }
        }
    }
}
