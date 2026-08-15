import AppKit
import Foundation
import XCTest

final class KubeLoopUITests: XCTestCase {
    private var application: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        let environment = ProcessInfo.processInfo.environment
        let applicationPath = try XCTUnwrap(environment["KUBELOOP_UI_E2E_APP_PATH"], "KUBELOOP_UI_E2E_APP_PATH is required")
        let bundleIdentifier = try XCTUnwrap(environment["KUBELOOP_UI_E2E_BUNDLE_ID"], "KUBELOOP_UI_E2E_BUNDLE_ID is required")
        let profilePath = try XCTUnwrap(environment["KUBELOOP_UI_E2E_PROFILE_PATH"], "KUBELOOP_UI_E2E_PROFILE_PATH is required")
        let url = URL(fileURLWithPath: applicationPath)
        let opened = expectation(description: "open KubeLoop application")
        let configuration = NSWorkspace.OpenConfiguration()
        configuration.environment = ["KUBELOOP_PROFILE_PATH": profilePath]
        NSWorkspace.shared.openApplication(at: url, configuration: configuration) { _, error in
            XCTAssertNil(error)
            opened.fulfill()
        }
        wait(for: [opened], timeout: 20)
        application = XCUIApplication(bundleIdentifier: bundleIdentifier)
        application.activate()
        XCTAssertTrue(application.windows.firstMatch.waitForExistence(timeout: 20))
    }

    override func tearDownWithError() throws {
        application?.terminate()
    }

    func testRealWailsWindowExposesEveryPrimarySurface() {
        let screen = KubeLoopScreen(application: application)
        for destination in screen.primaryDestinations {
            let button = application.buttons[destination]
            XCTAssertTrue(button.waitForExistence(timeout: 10), "missing native accessibility button: \(destination)")
            button.click()
            XCTAssertTrue(application.staticTexts[destination].firstMatch.waitForExistence(timeout: 10), "navigation did not render: \(destination)")
        }
    }

    func testServerLoginCompletesThroughSystemChrome() throws {
        let environment = ProcessInfo.processInfo.environment
        let baseURL = try XCTUnwrap(environment["KUBELOOP_UI_E2E_BASE_URL"])
        let username = try XCTUnwrap(environment["KUBELOOP_UI_E2E_ADMIN_USERNAME"])
        let password = try XCTUnwrap(environment["KUBELOOP_UI_E2E_ADMIN_PASSWORD"])
        let screen = KubeLoopScreen(application: application)
        screen.open("Overview")
        let address = application.textFields.matching(NSPredicate(format: "value CONTAINS[c] 'https' OR placeholderValue CONTAINS[c] 'server'")).firstMatch
        XCTAssertTrue(address.waitForExistence(timeout: 10), "server address field is unavailable")
        address.click()
        address.typeKey("a", modifierFlags: .command)
        address.typeKey(.delete, modifierFlags: [])
        address.typeText(baseURL)
        let connect = application.buttons.matching(NSPredicate(format: "label CONTAINS[c] 'connect' OR label CONTAINS[c] 'retest'")).firstMatch
        XCTAssertTrue(connect.waitForExistence(timeout: 10), "server discovery action is unavailable")
        connect.click()
        let localLogin = application.buttons.matching(NSPredicate(format: "label CONTAINS[c] 'local account'")).firstMatch
        XCTAssertTrue(localLogin.waitForExistence(timeout: 30), "local account login is unavailable")
        localLogin.click()

        let chrome = XCUIApplication(bundleIdentifier: "com.google.Chrome")
        chrome.activate()
        XCTAssertTrue(chrome.windows.firstMatch.waitForExistence(timeout: 30), "system Chrome did not open")
        let usernameField = chrome.textFields.matching(NSPredicate(format: "label CONTAINS[c] 'username' OR placeholderValue CONTAINS[c] 'username'")).firstMatch
        XCTAssertTrue(usernameField.waitForExistence(timeout: 30), "OAuth username field is unavailable in Chrome")
        usernameField.click()
        usernameField.typeText(username)
        let passwordField = chrome.secureTextFields.matching(NSPredicate(format: "label CONTAINS[c] 'password' OR placeholderValue CONTAINS[c] 'password'")).firstMatch
        XCTAssertTrue(passwordField.waitForExistence(timeout: 10), "OAuth password field is unavailable in Chrome")
        passwordField.click()
        passwordField.typeText(password)
        let allow = chrome.buttons.matching(NSPredicate(format: "label ==[c] 'allow' OR label ==[c] 'continue'")).firstMatch
        XCTAssertTrue(allow.waitForExistence(timeout: 10), "OAuth authorization action is unavailable in Chrome")
        allow.click()

        application.activate()
        XCTAssertTrue(application.staticTexts["Signed in securely"].waitForExistence(timeout: 45), "OAuth callback did not authenticate the desktop application")

        for destination in ["Connections", "Workload", "Network", "Host Aliases", "MCP", "Overview"] {
            screen.open(destination)
            XCTAssertFalse(application.buttons["Sign in"].exists, "authentication was lost after switching to \(destination)")
        }
        XCTAssertTrue(application.staticTexts["Signed in securely"].waitForExistence(timeout: 20), "Overview did not recover the authenticated state")

        screen.open("Host Aliases")
        application.buttons["Add"].click()
        let aliasDomain = application.textFields.matching(NSPredicate(format: "placeholderValue == 'app.example.dev'")).firstMatch
        let aliasIP = application.textFields.matching(NSPredicate(format: "placeholderValue == '10.96.0.50'")).firstMatch
        XCTAssertTrue(aliasDomain.waitForExistence(timeout: 10), "host-alias domain field is unavailable")
        aliasDomain.typeText("ui-e2e.kubeloop.test")
        aliasIP.typeText("192.0.2.50")
        application.buttons["Save"].click()
        XCTAssertEqual(aliasDomain.value as? String, "ui-e2e.kubeloop.test")
        application.buttons["Clear all"].click()
        XCTAssertTrue(application.staticTexts["No host aliases yet. Add a domain and IP, then save."].waitForExistence(timeout: 20), "host aliases were not cleared")

        screen.open("Overview")

        let socks = application.buttons["SOCKS5 proxy"]
        XCTAssertTrue(socks.waitForExistence(timeout: 30), "SOCKS mode is unavailable")
        socks.click()
        XCTAssertTrue(socks.isSelected, "SOCKS mode was not selected")
        let socksPort = application.textFields["SOCKS port"]
        XCTAssertTrue(socksPort.waitForExistence(timeout: 10), "SOCKS port field is unavailable")
        let testPort = String(12_000 + ProcessInfo.processInfo.processIdentifier % 1_000)
        socksPort.click()
        socksPort.typeKey("a", modifierFlags: .command)
        socksPort.typeKey(.delete, modifierFlags: [])
        socksPort.typeText(testPort)
        application.buttons["Save"].click()
        XCTAssertEqual(socksPort.value as? String, testPort, "SOCKS port did not persist")
        connectAndWait(status: "Remote cluster proxy is ready")
        screen.open("Network")
        screen.open("Overview")
        XCTAssertTrue(application.staticTexts["Remote cluster proxy is ready"].waitForExistence(timeout: 20), "SOCKS connection was lost across tabs")
        disconnectAndWait()

        let tun = application.buttons["TUN mode"]
        XCTAssertTrue(tun.waitForExistence(timeout: 20), "TUN mode is unavailable")
        tun.click()
        XCTAssertTrue(tun.isSelected, "TUN mode was not selected")
        XCTAssertFalse(socks.isSelected, "selecting TUN incorrectly selected SOCKS")
        connectAndWait(status: "System TUN is connected", timeout: 60)
        XCTAssertTrue(tun.isSelected, "TUN selection changed while connecting")
        XCTAssertFalse(socks.isSelected, "SOCKS was visually selected during TUN connection")
        disconnectAndWait()

        screen.open("MCP")
        let enableToken = application.buttons["Enable token"]
        if enableToken.waitForExistence(timeout: 3) {
            enableToken.click()
            XCTAssertTrue(application.buttons["Disable token"].waitForExistence(timeout: 20), "MCP bearer authentication did not enable")
        }
        let enableMCP = application.buttons["Enable MCP"]
        if enableMCP.waitForExistence(timeout: 3) {
            enableMCP.click()
        }
        XCTAssertTrue(application.staticTexts["Listening"].waitForExistence(timeout: 20), "MCP server did not start")
        let endpoint = application.staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'http://127.0.0.1:' AND label ENDSWITH '/mcp'")).firstMatch
        XCTAssertTrue(endpoint.waitForExistence(timeout: 10), "MCP endpoint is unavailable")
        assertMCPRequiresBearer(endpoint.label)
        application.buttons["Disable MCP"].click()
        XCTAssertTrue(application.staticTexts["Off"].waitForExistence(timeout: 20), "MCP server did not stop")
    }

    private func connectAndWait(status: String, timeout: TimeInterval = 45) {
        let connect = application.buttons["Connect"]
        XCTAssertTrue(connect.waitForExistence(timeout: 20), "connection switch is unavailable")
        connect.click()
        XCTAssertTrue(application.staticTexts[status].waitForExistence(timeout: timeout), "connection did not reach: \(status)")
    }

    private func disconnectAndWait() {
        let disconnect = application.buttons["Disconnect"]
        XCTAssertTrue(disconnect.waitForExistence(timeout: 20), "disconnect switch is unavailable")
        disconnect.click()
        XCTAssertTrue(application.staticTexts["Data Plane disconnected"].waitForExistence(timeout: 45), "data plane did not disconnect")
    }

    private func assertMCPRequiresBearer(_ endpoint: String) {
        guard let url = URL(string: endpoint) else {
            XCTFail("invalid MCP endpoint: \(endpoint)")
            return
        }
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("application/json, text/event-stream", forHTTPHeaderField: "Accept")
        request.httpBody = Data(#"{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}"#.utf8)
        let completed = expectation(description: "unauthenticated MCP request")
        URLSession.shared.dataTask(with: request) { _, response, error in
            XCTAssertNil(error)
            XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 401)
            completed.fulfill()
        }.resume()
        wait(for: [completed], timeout: 15)
    }
}

private struct KubeLoopScreen {
    let application: XCUIApplication
    let primaryDestinations = ["Overview", "Servers", "Connections", "Workload", "Network", "Host Aliases", "MCP"]

    func open(_ destination: String) {
        let button = application.buttons[destination]
        XCTAssertTrue(button.waitForExistence(timeout: 10))
        button.click()
    }
}
