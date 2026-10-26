import CoreImage.CIFilterBuiltins
import MaktabaKit
import SwiftUI
import UIKit

/// The homes on this network (they announce themselves), the ones this TV knows, and one
/// by its address as the fallback.
struct TVHomePicker: View {
    let session: AppSession
    @State private var browser = HomeBrowser()
    @State private var address = ""
    @State private var problem: String?

    var body: some View {
        ZStack {
            LibraryBackdrop()
            ScrollView {
                VStack(alignment: .leading, spacing: 40) {
                    VStack(alignment: .leading, spacing: 8) {
                        SectionTitle("Maktaba", size: 64)
                        Text("Choose your home")
                            .font(Theme.body(32))
                            .foregroundStyle(Theme.muted)
                    }
                    VStack(alignment: .leading, spacing: 20) {
                        Text("On this network")
                            .font(Theme.body(29, weight: .semibold))
                            .foregroundStyle(Theme.goldText)
                        if browser.found.isEmpty {
                            HStack(spacing: 16) {
                                ProgressView()
                                Text("Looking for homes…").foregroundStyle(Theme.muted)
                            }
                        }
                        ForEach(browser.found) { home in
                            HomeRow(name: home.name, address: home.url.host() ?? "", systemImage: "wifi") {
                                session.use(Home(url: home.url, name: home.name))
                            }
                        }
                    }
                    let others = session.homes.filter { h in !browser.found.contains { $0.url == h.url } }
                    if !others.isEmpty {
                        VStack(alignment: .leading, spacing: 20) {
                            Text("Your homes")
                                .font(Theme.body(29, weight: .semibold))
                                .foregroundStyle(Theme.goldText)
                            ForEach(others) { home in
                                HomeRow(name: home.name, address: home.url.host() ?? "", systemImage: "house") {
                                    session.use(home)
                                }
                                .contextMenu {
                                    Button("Forget", systemImage: "trash", role: .destructive) { session.forget(home) }
                                }
                            }
                        }
                    }
                    VStack(alignment: .leading, spacing: 20) {
                        Text("Another home")
                            .font(Theme.body(29, weight: .semibold))
                            .foregroundStyle(Theme.goldText)
                        HStack(spacing: 24) {
                            TextField("Address, like 192.168.1.20:8088", text: $address)
                                .keyboardType(.URL)
                                .autocorrectionDisabled()
                                .textInputAutocapitalization(.never)
                                .onSubmit(add)
                                .frame(maxWidth: 900)
                            Button("Connect", action: add)
                                .buttonStyle(.gold)
                                .disabled(address.isEmpty)
                        }
                        if let problem {
                            Text(problem).foregroundStyle(.red)
                        }
                    }
                }
                .readingPanel(padding: 48)
                .frame(maxWidth: 1300)
                .padding(.vertical, 80)
                .frame(maxWidth: .infinity)
            }
        }
        .onAppear { browser.start() }
        .onDisappear { browser.stop() }
    }

    private func add() {
        guard let url = Home.normalize(address) else {
            problem = "That is not a home's address."
            return
        }
        problem = nil
        session.use(Home(url: url, name: url.host() ?? url.absoluteString))
    }
}

private struct HomeRow: View {
    let name: String
    let address: String
    let systemImage: String
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 24) {
                Image(systemName: systemImage).font(.system(size: 40))
                VStack(alignment: .leading, spacing: 4) {
                    Text(name).font(Theme.heading(40))
                    Text(address).font(Theme.body(26)).opacity(0.75)
                }
                Spacer()
            }
            .frame(maxWidth: .infinity)
        }
        .buttonStyle(.gold)
    }
}

/// Signing in on a TV: the TV shows a code (and a QR code of the page that approves it);
/// a person signed in on a phone or a computer approves it, and the TV is signed in as
/// them. Typing a password with the remote is the other way, and the only one on a home
/// from before codes.
struct TVSignInView: View {
    let session: AppSession
    @State private var pairing: DevicePairing?
    /// nil: not known yet; false: this home signs in by password only.
    @State private var codes: Bool?
    @State private var byPassword = false
    @State private var problem: String?

    var body: some View {
        ZStack {
            LibraryBackdrop()
            HStack(alignment: .top, spacing: 60) {
                if codes != false && !byPassword {
                    codePanel
                } else {
                    PasswordPanel(session: session)
                }
            }
            .padding(80)
        }
        .task(id: session.current?.url) { await pair() }
    }

    private var codePanel: some View {
        HStack(alignment: .center, spacing: 64) {
            VStack(alignment: .leading, spacing: 28) {
                SectionTitle("Sign in to \(session.current?.name ?? "your home")", size: 52)
                Text("On a phone or computer signed in to Maktaba, scan the code or open")
                    .font(Theme.body(30))
                    .foregroundStyle(Theme.muted)
                if let pairing, let home = session.current?.url {
                    Text(verbatim: "\(home.host() ?? "")\(home.port.map { ":\($0)" } ?? "")/pair")
                        .font(Theme.body(34, weight: .semibold))
                        .foregroundStyle(Theme.text)
                    Text("and enter")
                        .font(Theme.body(30))
                        .foregroundStyle(Theme.muted)
                    Text(pairing.code)
                        .font(.system(size: 96, weight: .bold, design: .monospaced))
                        .foregroundStyle(Theme.goldText)
                        .accessibilityLabel(pairing.code.map(String.init).joined(separator: " "))
                        .accessibilityIdentifier("pairing-code")
                    HStack(spacing: 16) {
                        ProgressView()
                        Text("Waiting for approval…").foregroundStyle(Theme.muted)
                    }
                } else {
                    ProgressView()
                }
                if let problem {
                    Text(problem).foregroundStyle(.red)
                }
                HStack(spacing: 32) {
                    Button("Use a password instead", systemImage: "keyboard") { byPassword = true }
                    Button("Choose another home", systemImage: "house") { session.switchHome() }
                }
                .buttonStyle(.gold)
                .padding(.top, 20)
            }
            if let pairing, let url = session.current.flatMap({ pairing.verifyURL(home: $0.url) }),
               let qr = QRCode.image(url.absoluteString) {
                qr.interpolation(.none)
                    .resizable()
                    .frame(width: 420, height: 420)
                    .padding(24)
                    .background(.white, in: .rect(cornerRadius: Theme.cornerRadius))
                    .accessibilityLabel("QR code for the sign-in page")
            }
        }
        .readingPanel(padding: 56)
    }

    /// Shows a code until it is approved (then signed in) or expires (then a new one).
    private func pair() async {
        guard let client = session.client else { return }
        while !Task.isCancelled {
            do {
                guard let p = try await client.startDevicePairing() else {
                    codes = false
                    return
                }
                codes = true
                pairing = p
                problem = nil
                waiting: while Date.now < p.expiresAt {
                    try await Task.sleep(for: .seconds(p.interval))
                    switch try await client.collectDevicePairing(p, label: UIDevice.current.name) {
                    case .waiting:
                        continue
                    case .approved:
                        try await session.signedInByCode()
                        return
                    case .expired:
                        break waiting
                    }
                }
            } catch is CancellationError {
                return
            } catch {
                problem = error.localizedDescription
                try? await Task.sleep(for: .seconds(5))
            }
        }
    }
}

/// Username and password with the TV's keyboard.
private struct PasswordPanel: View {
    let session: AppSession
    @State private var username = ""
    @State private var password = ""
    @State private var busy = false
    @State private var problem: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 28) {
            SectionTitle("Sign in to \(session.current?.name ?? "your home")", size: 52)
            TextField("Username", text: $username)
                .textContentType(.username)
                .autocorrectionDisabled()
                .textInputAutocapitalization(.never)
            SecureField("Password", text: $password)
                .textContentType(.password)
                .onSubmit(signIn)
            if let problem {
                Text(problem).foregroundStyle(.red)
            }
            HStack(spacing: 32) {
                Button(action: signIn) {
                    if busy { ProgressView() } else { Text("Sign in") }
                }
                .disabled(username.isEmpty || password.isEmpty || busy)
                Button("Choose another home", systemImage: "house") { session.switchHome() }
            }
            .buttonStyle(.gold)
        }
        .frame(maxWidth: 1000)
        .readingPanel(padding: 56)
    }

    private func signIn() {
        busy = true
        problem = nil
        Task {
            defer { busy = false }
            do {
                try await session.signIn(username: username, password: password)
            } catch {
                problem = error.localizedDescription
            }
        }
    }
}

enum QRCode {
    /// A QR code of `text`, one point per module (draw it with `.interpolation(.none)`).
    static func image(_ text: String) -> Image? {
        let f = CIFilter.qrCodeGenerator()
        f.message = Data(text.utf8)
        f.correctionLevel = "M"
        guard let out = f.outputImage,
              let cg = CIContext().createCGImage(out, from: out.extent) else { return nil }
        return Image(uiImage: UIImage(cgImage: cg))
    }
}
