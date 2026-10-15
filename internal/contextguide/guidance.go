// Package contextguide owns context acquisition and revision reuse guidance.
package contextguide

const Reuse = "Call agentdock_context when the device, workspace or required rules are missing or changed. Reuse a complete context_revision; continue an incomplete snapshot with its exact next_cursor. After context loss use force_refresh."
const Description = "Get workspace rules, Git state, tasks and available capabilities. Reuse context_revision after complete delivery; follow next_cursor for an incomplete snapshot. Omitted workdir inherits the conversation workspace."
