# Backend gaps tracked by the console

The console only renders capabilities represented by the Go OpenAPI contract. These are intentional boundaries, not mocked UI:

| Capability | Current contract | Console behavior | Recommendation |
| --- | --- | --- | --- |
| Service dependency graph | `GET/PUT /v1/projects/{projectID}/service-layout` stores resource positions only. | Services canvas renders supported resource nodes and persists `x/y`; it does not draw synthetic edges. | Add an explicit service relationship schema and endpoint when dependency ownership is defined. |
| Source editor | Function and site deployment APIs accept archives; the contract does not return source files or an editor session. | Function configuration explains the boundary and offers archive deployment; no fake Monaco editor is shown. | Add a source/artifact browsing contract if in-console editing is desired. |
| Unified deployment feed | Deployments are scoped to Functions and Sites. | The project Deployments page links to those real resource views. | Add a project deployment index if cross-resource chronology is required. |
| Trace filters/spans | Trace list APIs expose cursor/limit and root HTTP traces, not status/service filters or span trees. | Trace tables show returned root traces and use the API cursor model; no invented span hierarchy or filter semantics. | Extend the trace schema with filter parameters and span relationships. |
| Messaging writes | Current messaging endpoints expose provider/topic/message metadata; no write mutation is presented by this contract. | Messaging is a safe metadata view. | Add explicit provider/topic/message mutation schemas before adding forms. |

When a backend capability is added, regenerate the client with `npm run api:generate` before changing feature code.
