import Foundation

/// Composer validation shares Core's UTF-8 byte budget, not a character count.
enum WorkbenchInsertionInput {
    static let maximumBytes = 8192

    static func normalized(_ raw: String) throws -> String {
        let text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, text.utf8.count <= maximumBytes else {
            throw WorkbenchClientError.configuration(L10n.text("A supplement must contain 1–8192 UTF-8 bytes."))
        }
        return text
    }
}

/// Window-local retry identity; the authoritative queue remains in Core.
struct WorkbenchInsertionSubmissions {
    private struct Submission { let text: String; let id: String }
    private var pending = [String: Submission]()
    private let capacity: Int

    init(capacity: Int = 100) { self.capacity = max(1, capacity) }

    mutating func id(conversation: String, text: String) throws -> String {
        if let previous = pending[conversation], previous.text == text { return previous.id }
        guard pending[conversation] != nil || pending.count < capacity else {
            throw WorkbenchClientError.configuration(L10n.text("Too many unresolved submissions. Read their receipts before sending to another conversation."))
        }
        let id = "macos-\(UUID().uuidString.lowercased())"
        pending[conversation] = Submission(text: text, id: id)
        return id
    }

    mutating func observe(conversation: String, insertion: WorkbenchInsertion) {
        guard insertion.terminal,
              pending[conversation]?.id == insertion.raw.text("submission_id") else { return }
        pending.removeValue(forKey: conversation)
    }

    mutating func observe(conversation: String, page: WorkbenchInsertionPage) {
        for item in page.items { observe(conversation: conversation, insertion: item) }
    }
}
