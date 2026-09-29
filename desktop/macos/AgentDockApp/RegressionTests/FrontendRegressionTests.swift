import XCTest
import AppKit
@testable import WorkbenchKit

private final class FrontendFixture: @unchecked Sendable {
    private let lock = NSLock()
    let now = WorkbenchFormatting.iso(Date())
    var phase = 0
    var payloadDelay: Double = 0
    var payloadReads = 0
    var insertionStatus = "acknowledged"
    var failInsertion = false
    var insertionRequests = [WorkbenchJSON]()
    var lastInsertion = WorkbenchJSON.null
    var pluginValid = true
    var pluginFailure = "PLUGIN_SOURCE_CHANGE_CONFIRMATION_REQUIRED"
    var pluginUpdates = [WorkbenchJSON]()
    var streamFailures = false
    var streamRequests = 0
    var restFails = false

    func access<T>(_ body: (FrontendFixture) throws -> T) rethrows -> T {
        lock.lock(); defer { lock.unlock() }
        return try body(self)
    }

    static func body(_ request: URLRequest) throws -> WorkbenchJSON {
        if let data = request.httpBody { return try WorkbenchJSON.decode(data) }
        guard let stream = request.httpBodyStream else { return .object([:]) }
        stream.open(); defer { stream.close() }
        var data = Data(), buffer = [UInt8](repeating: 0, count: 4096)
        while true {
            let count = stream.read(&buffer, maxLength: buffer.count)
            if count == 0 { break }
            if count < 0 { throw stream.streamError ?? URLError(.cannotDecodeRawData) }
            data.append(contentsOf: buffer.prefix(count))
            guard data.count <= 65536 else { throw URLError(.dataLengthExceedsMaximum) }
        }
        return try WorkbenchJSON.decode(data)
    }

    private func row(_ id: String) -> WorkbenchJSON {
        .object(["conversation_id": .string(id), "title": .string(id),
            "state": .object(["workspace_id": .string("wsp_test")]),
            "statistics": .object(["last_tool_call_at": .string(now)])])
    }

    private func call() -> WorkbenchJSON {
        .object(["call_id": .string("call_a"), "conversation_id": .string("conv_a"),
            "status": .string(phase == 0 ? "running" : "succeeded"),
            "updated_seq": .integer(Int64(phase + 1)),
            "response": phase == 0 ? .null : .object([
                "ref": .string(String(repeating: String(phase), count: 64)),
                "state": .string("complete"), "bytes": .integer(14)])])
    }

    func answer(_ request: URLRequest) throws -> (Int, String, Data, Double) {
        let body = try Self.body(request)
        return try access { state in
            let path = request.url!.path
            func json(_ value: WorkbenchJSON, _ code: Int = 200, _ delay: Double = 0) throws -> (Int, String, Data, Double) {
                (code, "application/json", try value.encodedData(), delay)
            }
            if path.hasSuffix("/stream") {
                state.streamRequests += 1
                if state.streamFailures { return try json(.object(["code": .string("TEMPORARY")]), 503) }
                return (200, "text/event-stream", Data("retry: 1000\n\n".utf8), 0)
            }
            if state.restFails { throw URLError(.cannotConnectToHost) }
            if path.hasSuffix("/execution") { return try json(.object(["server_now": .string(state.now)])) }
            if path.hasSuffix("/sidebar") {
                return try json(.object(["server_now": .string(state.now), "groups": .array([
                    .object(["workspace_id": .string("wsp_test"), "conversations": .array([state.row("conv_a"), state.row("conv_b")])])])]))
            }
            if path.hasSuffix("/calls") { return try json(.object(["calls": .array([state.call()])])) }
            if path.hasSuffix("/calls/call_a") { return try json(state.call()) }
            if path.contains("/payload/") {
                state.payloadReads += 1
                let text = state.phase == 0 ? "" : "final-result-\(state.phase)"
                return try json(.object(["text": .string(text), "next_offset": .integer(Int64(text.utf8.count)),
                    "has_more": .bool(false)]), 200, state.payloadDelay)
            }
            if path.hasSuffix("/effective") {
                return try json(.object(["effective": .object(["revision": .integer(1), "mode": .string("full")])]))
            }
            if path.hasSuffix("/insertions") {
                if request.httpMethod == "POST" {
                    state.insertionRequests.append(body)
                    state.lastInsertion = .object(["insertion_id": .string("ins_" + String(state.insertionRequests.count)),
                        "submission_id": body["submission_id"], "text": body["text"], "status": .string(state.insertionStatus)])
                    if state.failInsertion { state.failInsertion = false; throw URLError(.networkConnectionLost) }
                    return try json(.object(["insertion": state.lastInsertion]))
                }
                return try json(.object(["insertions": .array(state.lastInsertion.isNull ? [] : [state.lastInsertion])]))
            }
            if path.hasSuffix("/conversations/conv_a") { return try json(.object(["conversation": state.row("conv_a")])) }
            if path.hasSuffix("/conversations/conv_b") { return try json(.object(["conversation": state.row("conv_b")])) }
            if path.hasSuffix("/plugins/fixture") {
                return try json(.object(["plugin": .object(["name": .string("fixture"), "source": .string("old.zip")])]))
            }
            if path.hasSuffix("/plugins") {
                if body.text("action") == "validate" {
                    return try json(.object(["valid": .bool(state.pluginValid), "plugin": .object(["name": .string("fixture")]),
                        "review": .object(["source": .string("new.zip")])]))
                }
                state.pluginUpdates.append(body)
                if state.pluginFailure == "NETWORK" { throw URLError(.networkConnectionLost) }
                if !body.flag("confirmed_source_change") {
                    return try json(.object(["code": .string(state.pluginFailure), "message": .string("fixture rejection")]), 409)
                }
                return try json(.object(["ok": .bool(true)]))
            }
            return try json(.object(["code": .string("NOT_FOUND")]), 404)
        }
    }
}

final class FrontendRegressionTests: XCTestCase {
    private var sessions = [URLSession]()
    override func tearDown() {
        sessions.forEach { $0.invalidateAndCancel() }
        sessions.removeAll()
        super.tearDown()
    }

    private func client(_ fixture: FrontendFixture) -> WorkbenchAPIClient {
        RuntimeProtocol.configure { try fixture.answer($0) }
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [RuntimeProtocol.self]
        let session = URLSession(configuration: config)
        sessions.append(session)
        return WorkbenchAPIClient(session: session) {
            try WorkbenchConnection(baseURL: URL(string: "http://127.0.0.1:18765/")!, bearerToken: "frontend-test")
        }
    }

    @MainActor private func until(_ condition: () -> Bool) async throws {
        let deadline = Date().addingTimeInterval(5)
        while !condition() {
            guard Date() < deadline else { throw NSError(domain: "FrontendRegressionTests", code: 1, userInfo: [NSLocalizedDescriptionKey: "Fixture did not converge"]) }
            try await Task.sleep(nanoseconds: 10_000_000)
        }
    }

    @MainActor private func descendants(_ view: NSView) -> [NSView] {
        [view] + view.subviews.flatMap { descendants($0) }
    }

    private func insertion(_ id: String, _ status: String) -> WorkbenchInsertion {
        WorkbenchInsertion(json: .object(["insertion_id": .string("ins_fixture"), "submission_id": .string(id), "status": .string(status)]))
    }

    func testOrdinaryAndSSETimeoutsAreIndependent() {
        let normal = WorkbenchAPIClient.sessionConfiguration(stream: false)
        let stream = WorkbenchAPIClient.sessionConfiguration(stream: true)
        XCTAssertEqual(normal.timeoutIntervalForRequest, 12)
        XCTAssertEqual(normal.timeoutIntervalForResource, 20)
        XCTAssertGreaterThan(stream.timeoutIntervalForRequest, 15)
        XCTAssertGreaterThan(stream.timeoutIntervalForResource, 3600)
        for config in [normal, stream] {
            XCTAssertNil(config.httpCookieStorage)
            XCTAssertFalse(config.httpShouldSetCookies)
            XCTAssertEqual(config.connectionProxyDictionary?.count, 0)
        }
    }

    func testSSERequestKeepsLongTimeoutAndResumeCursor() async throws {
        let api = client(FrontendFixture())
        for try await _ in api.eventStream(path: "/internal/runtime/calls/stream", lastEventID: "42") {}
        let request = try XCTUnwrap(RuntimeProtocol.captured().first)
        XCTAssertEqual(request.timeoutInterval, WorkbenchAPIClient.streamIdleTimeout)
        XCTAssertEqual(request.value(forHTTPHeaderField: "Last-Event-ID"), "42")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer frontend-test")
    }

    func testTerminalInsertionCreatesNewIdentityButPendingRetryDoesNot() throws {
        for terminal in ["acknowledged", "expired", "cancelled", "failed"] {
            var cache = WorkbenchInsertionSubmissions()
            let first = try cache.id(conversation: "conv_a", text: "continue")
            cache.observe(conversation: "conv_a", insertion: insertion(first, "pending"))
            XCTAssertEqual(try cache.id(conversation: "conv_a", text: "continue"), first)
            cache.observe(conversation: "conv_a", insertion: insertion(first, terminal))
            let second = try cache.id(conversation: "conv_a", text: "continue")
            XCTAssertNotEqual(second, first)
            cache.observe(conversation: "conv_a", insertion: insertion(first, terminal))
            XCTAssertEqual(try cache.id(conversation: "conv_a", text: "continue"), second)
        }
    }

    func testInsertionCapacityNeverErasesUnresolvedIdentity() throws {
        var cache = WorkbenchInsertionSubmissions(capacity: 1)
        let first = try cache.id(conversation: "conv_a", text: "continue")
        XCTAssertThrowsError(try cache.id(conversation: "conv_b", text: "continue"))
        XCTAssertEqual(try cache.id(conversation: "conv_a", text: "continue"), first)
        cache.observe(conversation: "conv_b", insertion: insertion(first, "acknowledged"))
        XCTAssertEqual(try cache.id(conversation: "conv_a", text: "continue"), first)
    }

    func testInsertionUTF8ByteBoundaries() throws {
        for value in [String(repeating: "a", count: 8192), String(repeating: "中", count: 2730) + "ab", String(repeating: "😀", count: 2048)] {
            XCTAssertEqual(value.utf8.count, 8192)
            XCTAssertEqual(try WorkbenchInsertionInput.normalized(" \n" + value + "\n"), value)
            XCTAssertThrowsError(try WorkbenchInsertionInput.normalized(value + "a"))
        }
        XCTAssertThrowsError(try WorkbenchInsertionInput.normalized(" \n"))
        XCTAssertThrowsError(try WorkbenchInsertionInput.normalized(String(repeating: "中", count: 3000)))
    }

    func testOversizedInsertionIsRejectedBeforeHTTP() async throws {
        let api = client(FrontendFixture())
        do {
            _ = try await api.enqueueInsertion(conversationID: "conv_a", submissionID: "test", text: String(repeating: "a", count: 8193))
            XCTFail("oversized input was accepted")
        } catch {}
        XCTAssertTrue(RuntimeProtocol.captured().isEmpty)
    }

    @MainActor func testModelSameTextAfterTerminalIsANewSubmission() async throws {
        let fixture = FrontendFixture()
        let api = client(fixture)
        let model = WorkbenchViewModel(client: api)
        defer { model.stop() }
        model.start()
        try await until { model.snapshot.permission != nil }
        model.submitInsertion("continue")
        try await until { fixture.access { $0.insertionRequests.count } == 1 && !model.isOperating }
        model.submitInsertion("continue")
        try await until { fixture.access { $0.insertionRequests.count } == 2 && !model.isOperating }
        let ids = fixture.access { $0.insertionRequests.map { $0.text("submission_id") } }
        XCTAssertNotEqual(ids[0], ids[1])
    }

    @MainActor func testUnknownInsertionWriteUsesSameIdentityOnlyOnExplicitRetry() async throws {
        let fixture = FrontendFixture()
        fixture.access { $0.insertionStatus = "pending"; $0.failInsertion = true }
        let model = WorkbenchViewModel(client: client(fixture))
        defer { model.stop() }
        model.start()
        try await until { model.snapshot.permission != nil }
        model.submitInsertion("continue")
        try await until { fixture.access { $0.insertionRequests.count } == 1 && !model.isOperating }
        try await Task.sleep(nanoseconds: 100_000_000)
        XCTAssertEqual(fixture.access { $0.insertionRequests.count }, 1)
        fixture.access { $0.insertionStatus = "acknowledged" }
        model.submitInsertion("continue")
        try await until { fixture.access { $0.insertionRequests.count } == 2 && !model.isOperating }
        let ids = fixture.access { $0.insertionRequests.map { $0.text("submission_id") } }
        XCTAssertEqual(ids[0], ids[1])
    }

    @MainActor func testPayloadRevisionInvalidatesExhaustedCacheAndAllowsReload() async throws {
        let fixture = FrontendFixture()
        let tested = WorkbenchViewModel(client: client(fixture))
        defer { tested.stop() }
        tested.start()
        try await until { tested.snapshot.selectedCall != nil }
        tested.loadPayload(source: "response")
        try await until { tested.payloadSlices["response"] != nil && !tested.isReadingPayload }
        XCTAssertEqual(tested.payloadSlices["response"]?.text, "")
        fixture.access { $0.phase = 1 }
        tested.refresh()
        try await until { tested.snapshot.selectedCall?.sequence == 2 }
        XCTAssertNil(tested.payloadSlices["response"])
        XCTAssertEqual(fixture.access { $0.payloadReads }, 1, "changed output must remain lazy")
        tested.loadPayload(source: "response")
        try await until { tested.payloadSlices["response"]?.text == "final-result-1" }
        tested.loadPayload(source: "response", restart: true)
        try await until { fixture.access { $0.payloadReads } == 3 && !tested.isReadingPayload }
        XCTAssertEqual(tested.payloadSlices["response"]?.text, "final-result-1")
    }

    @MainActor func testLatePayloadCannotReplaceNewRevision() async throws {
        let fixture = FrontendFixture()
        fixture.access { $0.payloadDelay = 0.35 }
        let model = WorkbenchViewModel(client: client(fixture))
        defer { model.stop() }
        model.start()
        try await until { model.snapshot.selectedCall != nil }
        model.loadPayload(source: "response")
        try await until { fixture.access { $0.payloadReads } == 1 }
        fixture.access { $0.phase = 1; $0.payloadDelay = 0 }
        model.refresh()
        try await until { model.snapshot.selectedCall?.sequence == 2 }
        try await Task.sleep(nanoseconds: 450_000_000)
        XCTAssertNil(model.payloadSlices["response"])
        XCTAssertFalse(model.isReadingPayload)
        model.loadPayload(source: "response")
        try await until { model.payloadSlices["response"]?.text == "final-result-1" }
    }

    @MainActor func testStreamFailureRequiresRESTVerificationOfStaleness() async throws {
        let fixture = FrontendFixture()
        fixture.access { $0.streamFailures = true }
        let model = WorkbenchViewModel(client: client(fixture))
        defer { model.stop() }
        model.start()
        try await until { fixture.access { $0.streamRequests } > 0 && model.snapshot.permission != nil && !model.isRefreshing }
        XCTAssertFalse(model.snapshot.stale)
        fixture.access { $0.restFails = true }
        model.refresh()
        try await until { model.snapshot.stale }
        XCTAssertTrue(model.snapshot.stale)
    }

    @MainActor func testPermissionChoiceSurvivesRepeatedRenderAndResetsOnConversationChange() throws {
        _ = NSApplication.shared
        let model = WorkbenchViewModel(fixtureMode: true), detail = WorkbenchDetailViewController()
        _ = detail.view
        detail.render(model, selectedInsertion: nil)
        let tabs = try XCTUnwrap(descendants(detail.view).first { $0.accessibilityIdentifier() == "workbench.detail.tabs" } as? NSTabView)
        tabs.selectTabViewItem(at: 2)
        let choice = try XCTUnwrap(descendants(detail.view).first { $0.accessibilityIdentifier() == "workbench.permission.mode" } as? NSPopUpButton)
        choice.selectItem(withTitle: L10n.text("Read only"))
        NSApp.sendAction(try XCTUnwrap(choice.action), to: choice.target, from: choice)
        for _ in 0..<5 { detail.render(model, selectedInsertion: nil) }
        XCTAssertEqual(choice.titleOfSelectedItem, L10n.text("Read only"))
        model.selectConversation("conv_permissions")
        detail.render(model, selectedInsertion: nil)
        XCTAssertEqual(choice.indexOfSelectedItem, 0)
    }

    @MainActor func testSelectedInsertionDoesNotStealTabOnRender() throws {
        _ = NSApplication.shared
        let model = WorkbenchViewModel(fixtureMode: true), detail = WorkbenchDetailViewController()
        _ = detail.view
        let selected = try XCTUnwrap(model.snapshot.insertions.items.first)
        detail.render(model, selectedInsertion: selected)
        let tabs = try XCTUnwrap(descendants(detail.view).first { $0.accessibilityIdentifier() == "workbench.detail.tabs" } as? NSTabView)
        XCTAssertEqual(tabs.indexOfTabViewItem(tabs.selectedTabViewItem!), 3)
        tabs.selectTabViewItem(at: 1)
        for _ in 0..<5 { detail.render(model, selectedInsertion: selected) }
        XCTAssertEqual(tabs.indexOfTabViewItem(tabs.selectedTabViewItem!), 1)
        detail.render(model, selectedInsertion: WorkbenchInsertion(json: .object(["insertion_id": .string("ins_new")])))
        XCTAssertEqual(tabs.indexOfTabViewItem(tabs.selectedTabViewItem!), 3)
    }

    @MainActor func testReloadButtonRequestsRestartAndComposerPreservesOversizedDraft() throws {
        _ = NSApplication.shared
        let model = WorkbenchViewModel(fixtureMode: true), detail = WorkbenchDetailViewController()
        _ = detail.view
        detail.render(model, selectedInsertion: nil)
        var received: (String, Bool)?
        detail.onReadPayload = { received = ($0, $1) }
        let reload = try XCTUnwrap(descendants(detail.view).first { $0.accessibilityIdentifier() == "workbench.output.reload" } as? NSButton)
        reload.performClick(nil)
        XCTAssertEqual(received?.0, "response")
        XCTAssertEqual(received?.1, true)
        let timeline = WorkbenchTimelineViewController()
        _ = timeline.view
        let editor = try XCTUnwrap(descendants(timeline.view).first { $0.accessibilityIdentifier() == "workbench.insertion.editor" } as? NSTextView)
        let send = try XCTUnwrap(descendants(timeline.view).first { $0.accessibilityIdentifier() == "workbench.insertion.send" } as? NSButton)
        editor.isEditable = true
        editor.string = String(repeating: "a", count: 8192)
        timeline.textDidChange(Notification(name: NSText.didChangeNotification))
        XCTAssertTrue(send.isEnabled)
        editor.string += "a"
        timeline.textDidChange(Notification(name: NSText.didChangeNotification))
        XCTAssertFalse(send.isEnabled)
        XCTAssertEqual(editor.string.utf8.count, 8193)
    }

    @MainActor func testPluginRebindNeedsExplicitConfirmation() async throws {
        for accepted in [false, true] {
            let fixture = FrontendFixture()
            let api = client(fixture)
            var confirmations = 0
            do {
                _ = try await api.updateLocalPlugin(name: "fixture", source: "new.zip", confirmCandidate: { _ in true }, confirmSourceChange: { installed, candidate in
                    confirmations += 1
                    XCTAssertFalse(installed["plugin"].isNull)
                    XCTAssertTrue(candidate.flag("valid"))
                    return accepted
                })
                XCTAssertTrue(accepted)
            } catch { XCTAssertFalse(accepted) }
            XCTAssertEqual(confirmations, 1)
            let updates = fixture.access { $0.pluginUpdates }
            XCTAssertEqual(updates.count, accepted ? 2 : 1)
            XCTAssertFalse(updates[0].flag("confirmed_source_change"))
            if accepted { XCTAssertTrue(updates[1].flag("confirmed_source_change")) }
        }
    }

    @MainActor func testPluginValidationAndOtherFailuresNeverRetryWrites() async throws {
        for failure in ["INVALID", "OTHER", "NETWORK", "CANCEL"] {
            let fixture = FrontendFixture()
            fixture.access { $0.pluginValid = failure != "INVALID"; $0.pluginFailure = failure }
            do {
                _ = try await client(fixture).updateLocalPlugin(name: "fixture", source: "new.zip", confirmCandidate: { _ in failure != "CANCEL" }, confirmSourceChange: { _, _ in XCTFail("unexpected source confirmation"); return true })
                XCTFail("operation should not succeed")
            } catch {}
            XCTAssertEqual(fixture.access { $0.pluginUpdates.count }, ["INVALID", "CANCEL"].contains(failure) ? 0 : 1)
        }
    }

    func testIPv6ActualConfigurationEntryAndNonLoopbackRejection() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        let paths = AppPaths(home: directory, appBundle: directory.appendingPathComponent("Fixture.app"))
        try FileManager.default.createDirectory(at: paths.appSupport, withIntermediateDirectories: true)
        for host in ["::1", "[::1]", "::", "[::]"] {
            let env = "AGENTDOCK_HOST='\(host)'\nAGENTDOCK_PORT='18765'\nAGENTDOCK_AUTH_TOKEN='ipv6-fixture'\n"
            try Data(env.utf8).write(to: paths.environment)
            let configuration = try XCTUnwrap(ServiceConfiguration.load(from: paths.environment))
            XCTAssertEqual(configuration.healthURL?.absoluteString, "http://[::1]:18765/healthz")
            XCTAssertEqual(try WorkbenchConnection(paths: paths).baseURL.absoluteString, "http://[::1]:18765/")
        }
        for host in ["::2", "[2001:db8::1]", "example.com"] {
            let env = "AGENTDOCK_HOST='\(host)'\nAGENTDOCK_PORT='18765'\nAGENTDOCK_AUTH_TOKEN='ipv6-fixture'\n"
            try Data(env.utf8).write(to: paths.environment)
            XCTAssertThrowsError(try WorkbenchConnection(paths: paths))
        }
        XCTAssertEqual(ServiceConfiguration.httpEndpoint(host: "127.0.0.1", port: 18765, path: "/healthz")?.absoluteString, "http://127.0.0.1:18765/healthz")
        XCTAssertNil(ServiceConfiguration.httpEndpoint(host: "::1", port: -1, path: "/"))
    }
}
