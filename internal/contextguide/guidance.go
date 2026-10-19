// Package contextguide owns context acquisition and revision reuse guidance.
package contextguide

const Reuse = "Call agentdock_context when the device, workspace or required rules are missing or changed. The first-page Bootstrap supports routing; use the exact next_cursor only for needed details. Reuse the delivered context_revision. After context loss use force_refresh."
const Description = "Get workspace rules, Git state, tasks and available capabilities. The first page keeps workspace and tool routes. Follow next_cursor for needed details; omitting cursor never advances an existing page sequence; unrelated work may continue. Omitted workdir inherits the conversation workspace."
