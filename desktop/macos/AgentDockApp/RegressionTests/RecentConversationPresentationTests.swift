import XCTest
@testable import WorkbenchKit

final class RecentConversationPresentationTests: XCTestCase {
    private let now = Date(timeIntervalSince1970: 1_800_000_000)

    private func row(live: Bool, age: TimeInterval = 1, extra: [String: WorkbenchJSON] = [:]) -> WorkbenchConversation {
        var value: [String: WorkbenchJSON] = [
            "conversation_id": .string("conv_recent"), "source": .string("ChatGPT"),
            "in_flight": .bool(live),
            "last_interaction_at": .string(WorkbenchFormatting.iso(now.addingTimeInterval(-age))),
            "statistics": .object(["running": .integer(live ? 10 : 0)])
        ]
        value.merge(extra) { _, new in new }
        return WorkbenchConversation(json: .object(value), serverNow: now)
    }

    func testExecutionDoesNotChangeConversationText() {
        let idle = row(live: false)
        let live = row(live: true)
        XCTAssertEqual(live.stateText, idle.stateText)
        XCTAssertEqual(live.metadataText, idle.metadataText)
        XCTAssertTrue(live.inFlight)
        XCTAssertTrue(live.recentlyActive)
        var expired = row(live: true, age: 119)
        expired.advancePresentation(serverNow: now.addingTimeInterval(1))
        XCTAssertFalse(expired.recentlyActive)
        XCTAssertTrue(expired.inFlight)
    }

    func testInactiveManagementStatesCannotBeReactivatedByClock() {
        for key in ["terminated_at", "archived_at", "trashed_at"] {
            var item = row(live: true, extra: [key: .string(WorkbenchFormatting.iso(now))])
            XCTAssertFalse(item.recentlyActive)
            item.advancePresentation(serverNow: now.addingTimeInterval(1))
            XCTAssertFalse(item.recentlyActive)
        }
        var unknown = row(live: true, extra: ["is_unattributed": .bool(true)])
        unknown.advancePresentation(serverNow: now)
        XCTAssertFalse(unknown.recentlyActive)
    }
}
