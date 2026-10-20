import XCTest
import Foundation
@testable import WorkbenchKit

final class RuntimeProtocol: URLProtocol {
    static let lock = NSLock()
    static var handler: ((URLRequest) throws -> (Int, String, Data, Double))?
    static var requests = [URLRequest]()
    private var work: DispatchWorkItem?
    static func configure(_ block: @escaping (URLRequest) throws -> (Int, String, Data, Double)) {
        lock.lock(); defer { lock.unlock() }; handler = block; requests = []
    }
    static func captured() -> [URLRequest] { lock.lock(); defer { lock.unlock() }; return requests }
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        Self.lock.lock(); Self.requests.append(request); let handler = Self.handler; Self.lock.unlock()
        do {
            guard let handler else { throw URLError(.cannotConnectToHost) }
            let (code, mime, body, delay) = try handler(request)
            let operation = DispatchWorkItem { [weak self] in
                guard let self else { return }
                let response = HTTPURLResponse(url: self.request.url!, statusCode: code, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": mime])!
                self.client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
                self.client?.urlProtocol(self, didLoad: body)
                self.client?.urlProtocolDidFinishLoading(self)
            }
            work = operation
            DispatchQueue.global().asyncAfter(deadline: .now() + delay, execute: operation)
        } catch { client?.urlProtocol(self, didFailWithError: error) }
    }
    override func stopLoading() { work?.cancel(); work = nil }
}

final class TransportTests: XCTestCase {
    private var previousLanguage: UILanguagePreference = .system
    override func setUp() {
        super.setUp()
        previousLanguage = L10n.languagePreference()
        L10n.setLanguagePreference(.simplifiedChinese)
    }
    override func tearDown() {
        L10n.setLanguagePreference(previousLanguage)
        super.tearDown()
    }

    private func client(limit: Int = 8 * 1024 * 1024) -> WorkbenchAPIClient {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [RuntimeProtocol.self]
        let session = URLSession(configuration: configuration)
        return WorkbenchAPIClient(session: session, maximumResponseBytes: limit) {
            try WorkbenchConnection(baseURL: URL(string: "http://127.0.0.1:18765")!, bearerToken: "isolated-test-token")
        }
    }
    private func answer(_ value: WorkbenchJSON, status: Int = 200, delay: Double = 0) throws -> (Int, String, Data, Double) {
        (status, "application/json", try value.encodedData(), delay)
    }
    private func requestBody(_ request: URLRequest) throws -> Data {
        if let body = request.httpBody { return body }
        guard let stream = request.httpBodyStream else {
            throw URLError(.cannotDecodeContentData)
        }
        stream.open()
        defer { stream.close() }
        let capacity = 4096
        let buffer = UnsafeMutablePointer<UInt8>.allocate(capacity: capacity)
        defer { buffer.deallocate() }
        var data = Data()
        while true {
            let count = stream.read(buffer, maxLength: capacity)
            if count < 0 { throw stream.streamError ?? URLError(.cannotDecodeContentData) }
            if count == 0 { break }
            data.append(buffer, count: count)
        }
        return data
    }
    func testLoopbackBoundaryAndIDValidation() throws {
        for value in ["https://127.0.0.1:18765", "http://example.com", "http://user@localhost", "http://localhost/?secret=x"] {
            XCTAssertThrowsError(try WorkbenchConnection(baseURL: URL(string: value)!, bearerToken: "fixture"))
        }
        let api = client()
        for id in ["", ".", "..", "a/b", "a%2fb", "a\\b"] { XCTAssertThrowsError(try api.encodedPathComponent(id)) }
    }
    func testAuthenticatedQueryAndLazyPayload() async throws {
        RuntimeProtocol.configure { request in
            (200, "application/json", Data(#"{"calls":[],"latest_seq":8}"#.utf8), 0)
        }
        let api = client()
        _ = try await api.calls(conversationID: "conv_1")
        let request = try XCTUnwrap(RuntimeProtocol.captured().first)
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer isolated-test-token")
        XCTAssertFalse(request.url!.absoluteString.contains("isolated-test-token"))
        XCTAssertFalse(request.url!.absoluteString.contains("include_output=true"))
        XCTAssertTrue(request.url!.absoluteString.contains("top_level=true"))
        XCTAssertFalse(RuntimeProtocol.captured().contains { $0.url!.path.contains("payload") })
    }
    func testOversizedAndInvalidResponsesDoNotBecomeEmptyLists() async throws {
        RuntimeProtocol.configure { _ in (200, "application/json", Data(repeating: 32, count: 2048), 0) }
        do { _ = try await client(limit: 1024).overview(); XCTFail("expected bound") }
        catch let error as WorkbenchClientError { XCTAssertEqual(error, .responseTooLarge(limit: 1024)) }
        RuntimeProtocol.configure { _ in (200, "text/html", Data("error page".utf8), 0) }
        do { _ = try await client().overview(); XCTFail("expected MIME rejection") }
        catch { XCTAssertTrue(error.localizedDescription.contains("非 JSON")) }
    }
    func test403AndConflictAreNotRetried() async throws {
        for status in [403, 409] {
            RuntimeProtocol.configure { _ in (status, "application/json", Data(#"{"ok":false,"code":"POLICY_CONFLICT","error":"revision changed"}"#.utf8), 0) }
            do { _ = try await client().updatePermission(.object(["expected_revision": .integer(1)])); XCTFail("expected failure") }
            catch { XCTAssertTrue(error.localizedDescription.contains("revision changed")) }
            XCTAssertEqual(RuntimeProtocol.captured().count, 1)
        }
    }
    func testSSECursorAndBoundedStreamParsing() async throws {
        RuntimeProtocol.configure { _ in (200, "text/event-stream", Data("id: 9\nevent: call\ndata: {\"call_id\":\"call_9\"}\n\n".utf8), 0) }
        let api = client()
        var events = [WorkbenchStreamEvent]()
        for try await event in api.eventStream(path: "/internal/runtime/calls/stream", lastEventID: "8") { events.append(event) }
        XCTAssertEqual(events.map(\.id), ["9"])
        XCTAssertEqual(RuntimeProtocol.captured().first?.value(forHTTPHeaderField: "Last-Event-ID"), "8")
    }
    @MainActor func testOfflineSnapshotAndSelectionGeneration() async throws {
        let now = WorkbenchFormatting.iso(Date())
        let rows: [WorkbenchJSON] = ["conv_a", "conv_b"].map { .object(["conversation_id": .string($0), "title": .string($0),
            "statistics": .object(["last_tool_call_at": .string(now)])]) }
        RuntimeProtocol.configure { request in
            let path = request.url!.path
            if path.hasSuffix("sidebar") {
                return try self.answer(.object(["server_now": .string(now), "groups": .array([
                    .object(["workspace_id": .string("wsp_test"), "conversations": .array(rows)])])]))
            }
            if path.hasSuffix("/execution") { return try self.answer(.object(["server_now": .string(now)])) }
            if path.contains("/conversations/conv_a") && !path.hasSuffix("insertions") {
                return try self.answer(.object(["conversation": rows[0]]), delay: 0.35)
            }
            if path.contains("/conversations/conv_b") && !path.hasSuffix("insertions") { return try self.answer(.object(["conversation": rows[1]])) }
            if path.hasSuffix("/calls") { return try self.answer(.object(["calls": .array([])])) }
            if path.hasSuffix("insertions") { return try self.answer(.object(["insertions": .array([])])) }
            if path.hasSuffix("effective") { return try self.answer(.object(["effective": .object(["revision": .integer(1), "mode": .string("rules")])])) }
            return try self.answer(.object(["ok": .bool(false)]), status: 404)
        }
        let model = WorkbenchViewModel(client: client())
        model.start()
        for _ in 0..<100 where model.snapshot.sidebar.groups.isEmpty { try await Task.sleep(nanoseconds: 10_000_000) }
        XCTAssertEqual(model.snapshot.sidebar.groups.count, 1)
        model.selectConversation("conv_b")
        try await Task.sleep(nanoseconds: 500_000_000)
        XCTAssertEqual(model.selectedConversationID, "conv_b")
        XCTAssertEqual(model.snapshot.selectedConversation?.title, "conv_b")
        XCTAssertFalse(RuntimeProtocol.captured().contains { $0.url!.path.contains("payload") })
        RuntimeProtocol.configure { _ in throw URLError(.cannotConnectToHost) }
        model.refresh()
        try await Task.sleep(nanoseconds: 100_000_000)
        XCTAssertTrue(model.snapshot.stale)
        XCTAssertEqual(model.snapshot.sidebar.groups.count, 1)
        model.stop()
    }
    func testSourcePayloadRoute() async throws {
        RuntimeProtocol.configure { request in
            XCTAssertTrue(request.url?.path.hasSuffix("/internal/runtime/calls/call_1/payload/source") == true)
            return try self.answer(.object([
                "text": .string("business output"),
                "next_offset": .integer(15),
                "has_more": .bool(false),
            ]))
        }
        let result = try await client().callPayload("call_1", source: "source")
        XCTAssertEqual(result.text("text"), "business output")
    }

    @MainActor func testPluginInstallUsesValidatedReviewToken() async throws {
        RuntimeProtocol.configure { request in
            let body = try WorkbenchJSON.decode(try self.requestBody(request))
            switch body.text("action") {
            case "validate":
                return try self.answer(.object([
                    "ok": .bool(true),
                    "valid": .bool(true),
                    "review": .object(["review_token": .string("review-install")]),
                ]))
            case "install":
                XCTAssertEqual(body.text("review_token"), "review-install")
                XCTAssertTrue(body["confirmed"].isNull)
                return try self.answer(.object(["ok": .bool(true), "plugin": .object(["name": .string("example")])]))
            default:
                return try self.answer(.object(["ok": .bool(false), "error": .string("unexpected action")]), status: 400)
            }
        }
        let result = try await client().installLocalPlugin(source: "/tmp/example") { candidate in
            candidate["review"].text("review_token") == "review-install"
        }
        XCTAssertEqual(result["plugin"].text("name"), "example")
        XCTAssertEqual(RuntimeProtocol.captured().count, 2)
    }

    @MainActor func testPluginUpdateReusesReviewTokenForConfirmedSourceChange() async throws {
        var updateAttempts = 0
        RuntimeProtocol.configure { request in
            if request.httpMethod == "GET" {
                return try self.answer(.object(["plugin": .object(["name": .string("example")])]))
            }
            let body = try WorkbenchJSON.decode(try self.requestBody(request))
            switch body.text("action") {
            case "validate":
                return try self.answer(.object([
                    "ok": .bool(true),
                    "valid": .bool(true),
                    "review": .object(["review_token": .string("review-update")]),
                ]))
            case "update":
                updateAttempts += 1
                XCTAssertEqual(body.text("review_token"), "review-update")
                XCTAssertTrue(body["confirmed"].isNull)
                if updateAttempts == 1 {
                    XCTAssertFalse(body.flag("confirmed_source_change"))
                    return try self.answer(.object([
                        "ok": .bool(false),
                        "code": .string("PLUGIN_SOURCE_CHANGE_CONFIRMATION_REQUIRED"),
                        "error": .string("source changed"),
                    ]), status: 409)
                }
                XCTAssertTrue(body.flag("confirmed_source_change"))
                return try self.answer(.object(["ok": .bool(true), "plugin": .object(["name": .string("example")])]))
            default:
                return try self.answer(.object(["ok": .bool(false), "error": .string("unexpected action")]), status: 400)
            }
        }
        let result = try await client().updateLocalPlugin(
            name: "example",
            source: "/tmp/example",
            confirmCandidate: { _ in true },
            confirmSourceChange: { installed, candidate in
                installed["plugin"].text("name") == "example"
                    && candidate["review"].text("review_token") == "review-update"
            }
        )
        XCTAssertEqual(result["plugin"].text("name"), "example")
        XCTAssertEqual(updateAttempts, 2)
        XCTAssertEqual(RuntimeProtocol.captured().count, 4)
    }

    @MainActor func testAutomaticCallDetailAndPartialRefreshRetention() async throws {
        let now = WorkbenchFormatting.iso(Date())
        let conversation: WorkbenchJSON = .object([
            "conversation_id": .string("conv_1"),
            "title": .string("conversation"),
            "state": .object([
                "workspace_id": .string("wsp_1"),
                "active_task_id": .string("task_1"),
                "active_task_thread_id": .string("main"),
            ]),
            "statistics": .object(["last_tool_call_at": .string(now)]),
        ])
        func configure(phase: Int) {
            RuntimeProtocol.configure { request in
                let path = request.url!.path
                if path.hasSuffix("/execution/sidebar") {
                    return try self.answer(.object([
                        "server_now": .string(now),
                        "latest_seq": .integer(1),
                        "groups": .array([.object([
                            "workspace_id": .string("wsp_1"),
                            "conversations": .array([conversation]),
                        ])]),
                    ]))
                }
                if path.hasSuffix("/execution") { return try self.answer(.object(["server_now": .string(now)])) }
                if path.hasSuffix("/calls/call_1") {
                    return try self.answer(.object([
                        "call_id": .string("call_1"),
                        "conversation_id": .string("conv_1"),
                        "updated_seq": .integer(1),
                        "status": .string("succeeded"),
                        "summary": .string("full detail"),
                    ]))
                }
                if path.hasSuffix("/calls") {
                    return try self.answer(.object([
                        "calls": .array([.object([
                            "call_id": .string("call_1"),
                            "conversation_id": .string("conv_1"),
                            "updated_seq": .integer(1),
                            "status": .string("succeeded"),
                        ])]),
                        "latest_seq": .integer(1),
                    ]))
                }
                if path.hasSuffix("/conversations/conv_1") { return try self.answer(.object(["conversation": conversation])) }
                if path.hasSuffix("/permissions/effective") {
                    if phase == 1 { return try self.answer(.object(["code": .string("PERMISSION_UNAVAILABLE"), "error": .string("offline")]), status: 503) }
                    return try self.answer(.object(["effective": .object(["revision": .integer(1), "mode": .string("rules")])]))
                }
                if path.hasSuffix("/conversations/conv_1/insertions") {
                    if phase == 1 { return try self.answer(.object(["code": .string("INSERTION_UNAVAILABLE"), "error": .string("offline")]), status: 503) }
                    return try self.answer(.object(["insertions": .array([.object([
                        "insertion_id": .string("ins_1"),
                        "status": .string("queued"),
                    ])])]))
                }
                if path.hasSuffix("/tasks/task_1/threads") { return try self.answer(.object(["threads": .array([])])) }
                if path.hasSuffix("/tasks/task_1") {
                    if phase == 1 { return try self.answer(.object(["code": .string("TASK_UNAVAILABLE"), "error": .string("offline")]), status: 503) }
                    return try self.answer(.object(["task": .object([
                        "task_id": .string("task_1"),
                        "title": .string("task"),
                        "status": .string(phase == 2 ? "completed" : "running"),
                    ])]))
                }
                return try self.answer(.object(["ok": .bool(false), "error": .string("not found")]), status: 404)
            }
        }

        let model = WorkbenchViewModel(client: client())
        configure(phase: 0)
        model.refresh()
        for _ in 0..<200 where model.snapshot.selectedCall?.summary != "full detail" {
            try await Task.sleep(nanoseconds: 10_000_000)
        }
        XCTAssertEqual(model.snapshot.selectedCall?.summary, "full detail")
        XCTAssertEqual(model.snapshot.task?.status, "running")
        XCTAssertEqual(model.snapshot.permission?.mode, "rules")
        XCTAssertEqual(model.snapshot.insertions.items.map(\.id), ["ins_1"])

        configure(phase: 1)
        model.refresh(reason: "poll")
        for _ in 0..<200 where model.isRefreshing { try await Task.sleep(nanoseconds: 10_000_000) }
        try await Task.sleep(nanoseconds: 100_000_000)
        XCTAssertEqual(model.snapshot.task?.status, "running")
        XCTAssertEqual(model.snapshot.permission?.mode, "rules")
        XCTAssertEqual(model.snapshot.insertions.items.map(\.id), ["ins_1"])

        configure(phase: 2)
        model.refresh(reason: "poll")
        for _ in 0..<200 where model.snapshot.task?.status != "completed" {
            try await Task.sleep(nanoseconds: 10_000_000)
        }
        XCTAssertEqual(model.snapshot.task?.status, "completed")
        model.stop()
    }

}
