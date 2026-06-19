# Tool Search Call Arguments Normalization Design

## Goal

Prevent AINexus from returning OpenAI Responses payloads that encode
`tool_search_call.arguments` as a JSON string when Codex requires an object.

## Scope

- Normalize only output items whose `type` is `tool_search_call`.
- Convert `arguments` only when it is a string containing a valid JSON object.
- Preserve existing objects and unsupported values, including invalid JSON,
  arrays, scalars, and empty strings.
- Preserve standard `function_call.arguments` strings.
- Cover native passthrough and converted Responses output.
- Do not modify existing Codex session files.

## Architecture

Add one response-boundary normalizer in `internal/proxy`. It accepts a JSON
payload and recursively visits only the Responses containers that can carry
output items:

- a direct output item;
- `item` in `response.output_item.added` and `response.output_item.done`;
- `output` in a non-streaming Response object;
- `response.output` in `response.completed`.

When a visited item is a `tool_search_call`, the normalizer parses a
string-typed `arguments` value into `map[string]interface{}`. It returns the
original bytes when no value changes so normal traffic avoids reserialization.

Apply the normalizer after response transformation and before the final payload
is observed or written downstream. This placement covers every upstream format
without duplicating behavior across OpenAI, Claude, and Gemini transformers.

## Error Handling

Normalization is compatibility behavior, not validation. Malformed JSON or a
JSON value that is not an object remains unchanged. The proxy must continue
returning the upstream response instead of turning optional normalization into
a request failure.

## Testing

Use test-driven development.

1. Add a non-streaming proxy test whose upstream returns a string-typed
   `tool_search_call.arguments`; assert the downstream JSON contains an object.
2. Add a streaming proxy test covering `response.output_item.done` and
   `response.completed`; assert both downstream representations contain an
   object.
3. Add preservation coverage for `function_call.arguments`, invalid JSON,
   arrays, and already-object arguments.
4. Run focused proxy tests, transformer tests, and then the complete Go test
   suite.

## Success Criteria

- New Responses traffic cannot write a valid-object JSON string into a Codex
  `tool_search_call.arguments` history item through AINexus.
- Standard function-call behavior is unchanged.
- All focused and complete Go tests pass.
