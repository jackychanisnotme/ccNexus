# Codex Upstream WebSocket Bridge Design

## Goal

Prevent Codex Token Pool streams from ending during pending custom-tool calls by using the official ChatGPT Responses WebSocket transport upstream while preserving AINexus's existing HTTP/SSE client interface.

## Architecture

Only streaming `/v1/responses` requests routed to a `codex_token_pool` endpoint are eligible. AINexus keeps the downstream response as SSE, opens `wss://chatgpt.com/backend-api/codex/responses` with the selected credential, converts the normalized Responses payload into a `response.create` text frame, and wraps each upstream JSON event as an SSE `data:` event.

The bridge is a focused proxy component rather than part of the generic transformer. It owns WebSocket dialing, request-frame encoding, event forwarding, terminal-event validation, and protocol-error classification. Existing request normalization, credential selection, statistics, rate-limit capture, and downstream stream-session logic remain authoritative.

## Data Flow

1. The normal proxy path validates and normalizes the incoming Responses request, selects a Codex credential, and prepares official Codex headers.
2. For an eligible streaming request, the bridge changes the upstream scheme to `wss`, uses the `/backend-api/codex/responses` path, and performs the WebSocket handshake.
3. The normalized JSON body is copied into a new object with `type: response.create`; `store=false`, `stream=true`, and `instructions` continue to be enforced by existing normalization.
4. Each upstream text frame is validated as JSON, inspected for rate-limit and terminal events, encoded as `data: <json>\n\n`, and passed through the existing downstream stream session.
5. `response.completed` is the only successful terminal event. Upstream error frames, binary frames, close frames, EOF, cancellation, and idle timeout return classified errors.

## Compatibility And Fallback

- Existing client URLs, database rows, credentials, endpoint ordering, and SSE response format do not change.
- WebSocket is attempted only for Codex Token Pool streaming Responses requests.
- A handshake response that proves the endpoint does not support WebSocket may fall back once to the existing HTTP/SSE path before any downstream semantic bytes are written.
- Authentication, quota, policy, transient network, and post-upgrade failures are not silently converted to HTTP success. They use existing retry, credential rotation, and endpoint failover rules where safe.
- Non-streaming requests, compact requests, non-Codex endpoints, and older clients continue through existing paths.
- The implementation is original and uses the BSD-licensed `github.com/gorilla/websocket` dependency already present in the module graph; no AGPL source is copied from `new-api`.

## Error Handling

- A stream ending with `custom_tool_pending` remains incomplete and must never enter the tolerant-success branch.
- Once semantic bytes reach the downstream client, AINexus does not replay the request on another credential or endpoint.
- Handshake and close diagnostics include endpoint, credential ID, status/close code, completion state, and whether fallback was attempted, without logging tokens or request contents.
- Context cancellation closes both sides promptly. Read deadlines are refreshed on frames and use the existing stream idle-timeout policy.

## Testing

- Request-frame tests verify `type: response.create` and preservation of native Responses fields.
- Bridge tests use a local WebSocket server to verify headers, large custom-tool deltas, SSE framing, and successful `response.completed` termination.
- Failure tests cover close-before-completion, `custom_tool_pending`, upstream error events, malformed/binary frames, cancellation, and unsupported-handshake fallback.
- Routing tests prove that only eligible Codex Token Pool streaming requests use the bridge and that all existing request classes remain on their current paths.
- Run focused proxy tests, then `go test ./... -count=1`.
