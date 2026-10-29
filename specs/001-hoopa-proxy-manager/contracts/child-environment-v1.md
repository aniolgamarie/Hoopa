# Child proxy environment v1

Root implementation decision for T052/T055, 2026-10-29. This defines the bounded pure environment module for the next US2 tests; it does not claim application launch, terminal handling, init, metering or another platform is delivered. The module performs no filesystem, network, process, parent-environment or system-setting mutation. T139 remains the prerequisite for executing the next task milestone.

## Modes and declared names

The declared names are `http_proxy`, `https_proxy`, `all_proxy`, `ftp_proxy`, and `no_proxy`, compared without ASCII case sensitivity. Every case variant and duplicate of a declared name is removed from a managed/direct child's copy. Unrelated variables, including application-specific configuration, remain byte-for-byte and in their original relative order. This finite set is not a claim to override every application's own proxy mechanism. Parent input slices and `os.Environ` are never modified.

- `inherit`: preserve the supplied parent environment exactly, including order and duplicates; do not apply profile bypass or require an entry. Operating-system/process-library treatment of duplicate keys is outside this pure-copy assertion.
- `direct`: remove the five declared names, add no proxy or bypass variables, and require no entry. This does not bypass an existing TUN, VPN, system capture or application-local proxy.
- `managed`: remove the five names, then append deterministic pairs `http_proxy`, `HTTP_PROXY`, `https_proxy`, `HTTPS_PROXY`, `all_proxy`, `ALL_PROXY`, `no_proxy`, `NO_PROXY`. HTTP/HTTPS values use the authenticated `http://` URL of the bound mixed entry. ALL values use `socks5h://` for the same entry. Userinfo must be URL-escaped with the standard URL constructor, never concatenated unescaped. FTP variables are cleared without adding an FTP-specific promise. Both NO_PROXY values are exactly the explicit bypass tokens joined by commas; default/empty bypass produces empty values, with no inherited or implicit localhost exclusion.

Managed input must have a nonzero immutable `config.AppBinding` for the same valid app ID. The binding codec supplies the finite private loopback mixed-entry shape and credential validation. The caller must separately prove this binding belongs to the committed, currently usable entry before launch; constructing an environment alone proves neither provenance nor reachability and cannot start/reload/probe the core. Direct/inherit ignore any supplied binding.

`metering=true` with direct/inherit is an invalid combination. Managed metering is explicitly unsupported in this P1 module; it must fail before constructing a child environment. A later approved metering integration may extend this boundary. The field is never silently ignored or reduced to unmetered forwarding.

## Explicit bypass tokens

The environment module accepts a finite token grammar for managed mode: `*`; an ASCII DNS hostname (optionally beginning with a single dot); a canonical literal IPv4/IPv6 address accepted by `net/netip`; or an IP prefix accepted by `net/netip`. Host labels are 1–63 ASCII letters/digits/hyphens, cannot begin/end with hyphen, and the hostname without the optional leading dot is at most 253 bytes. A terminal dot, URL scheme/path, port suffix, brackets, comma, whitespace, control character, empty token and wildcard other than the entire `*` token are rejected. IPv6 colons and prefix slashes are accepted only through the IP parsers. IPv6 zones are not supported. IP/prefix tokens must equal the parser's canonical `String()` representation; prefixes need not be masked to their network address. Domains preserve caller case, token ordering and duplicates; no implicit normalization or DNS lookup occurs.

At most 256 tokens and at most 16 KiB of joined bypass text are accepted. Nil/empty lists are equivalent to no bypass. These limits and grammar constrain this initial child-environment profile, not the syntax of every external client's NO_PROXY implementation. Actual matching remains the target application's behavior; Hoopa does not promise identical hostname/CIDR semantics across clients and does not insert routing rules from these tokens. Direct/inherit do not consume or validate bypass metadata, because those modes respectively clear or preserve the parent setting.

## Pure Go boundary and privacy

The initial Linux API is:

```go
func BuildEnvironment(parent []string, app config.App, binding *config.AppBinding) (Environment, error)
func (e Environment) SensitiveValues() ([]string, error)
func (e Environment) Changes() []EnvironmentChange

type EnvironmentChange struct {
    Name string
    Action string // "remove" or "set"; never values
}
```

`Environment` owns its slices, exposes detached copies only through the deliberate `SensitiveValues` escape hatch, and redacts ordinary string/Go-string/JSON formatting. A zero value cannot be consumed as a valid child environment. All output/error formatting must omit parent values, usernames, passwords and entry URLs. `Changes` contains only removed key names in parent order followed by the deterministic generated set names; repeated removed names remain separate, and values are never returned. Inherit has no changes. The command preview may later combine this safe projection with separate entry/bypass metadata, but must never format or persist the sensitive environment.

A valid empty environment returns a non-nil empty slice from `SensitiveValues`, including when the input parent slice is nil. This preserves the exact list of zero entries while avoiding `exec.Cmd.Env == nil` being interpreted as permission to inherit. It does not make the zero-value `Environment` valid.

Invalid mode/app ID, NUL-containing parent entries, and managed bypass/binding errors return constant `ErrEnvironment`; incompatible metering returns `ErrMeteringMode`; managed metering returns `ErrMeteringUnsupported`. Failure returns a zero environment. Non-NUL opaque parent entries lacking `=` are preserved when unrelated; a bare declared proxy name is removed in managed/direct just like its valued form. No additional parent-environment grammar or hidden defaults are invented.

## T052 acceptance definitions

Tests precede implementation and cover mixed-case/duplicate proxy conflicts, exact parent preservation, generated HTTP/SOCKS URL round-trip with reserved credential characters, explicit and default bypass, three modes, mode/metering refusal, absent/mismatched/zero binding, invalid bypass tokens/bounds, unconsumed bypass in direct/inherit, input/output aliasing and redacted formatting. They compare the real parent environment before/after as well as supplied slices. Because this module is pure and never invokes a system tool, no unit test may claim a real managed connection, terminal behavior or full system-proxy audit. T053/T058 supply those separate integration observations.
