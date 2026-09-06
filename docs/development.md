# Development Guide

This document covers local development, testing, and the project structure of Buildcage.

## Contents

- [Local Usage](#local-usage)
- [Testing](#testing)
- [Local Development](#local-development)
- [Formatting & Linting](#formatting--linting)
- [Viewing Logs](#viewing-logs)
- [Makefile Commands](#makefile-commands)
- [Directory Structure](#directory-structure)
- [Inspect Engine Internals](#inspect-engine-internals)
- [Explicit Engine Internals](#explicit-engine-internals)
- [Troubleshooting](#troubleshooting)

## Local Usage

You can run Buildcage locally without GitHub Actions using Docker Compose and Make.

GitHub Actions inputs are lowercase (`proxy_mode`); the environment variables for local usage are
the uppercase form of the same names (`PROXY_MODE`).

### Starting the Builder

There's one `setup_buildkit_{engine}_{mode}` target per (`universal`, `inspect`, `explicit`) x
(`audit`, `restrict`) combination:

```bash
make setup_buildkit_universal_audit
make setup_buildkit_universal_restrict
make setup_buildkit_inspect_audit
make setup_buildkit_inspect_restrict
make setup_buildkit_explicit_audit
make setup_buildkit_explicit_restrict
```

**Start with custom domains** (restrict mode only):

```bash
ALLOWED_HTTPS_RULES="github.com:443 npmjs.org:443 example.com:443" make setup_buildkit_universal_restrict
```

Each target sets `PROXY_ENGINE`, which picks the build context at image build time through
`compose.yaml`'s `build.dockerfile: docker/${PROXY_ENGINE:-universal}/Dockerfile`. The `explicit_*`
targets therefore get BuildKit's native `--proxy-network` instead of the CNI/DNS-redirect/HAProxy
stack (see [Engines](../README.md#engines)).

`transparent` is an alias for `universal` in the action's own `proxy_engine` **input** only, resolved
in TypeScript. `PROXY_ENGINE` here is a raw Compose build-context selector with no alias layer, so
it does not understand that name.

### End-to-End Workflow

```bash
# 1. Start Buildcage
make setup_buildkit_universal_audit

# 2. Build
docker buildx build --builder buildcage --progress=plain -f Dockerfile .

# 3. View report
make report_buildkit

# 4. Clean up
make clean_buildkit
```

`make report_buildkit` runs `node report/src/main.ts` with the same `COMPOSE_PROJECT_NAME`/`BUILDCAGE_BUILD_TEST_HOOKS`
override the `setup_buildkit_*`/`clean_buildkit` targets use (see the pattern rule near the top of the
Makefile). Running `node report/src/main.ts` directly, without going through `make`, won't find the
running builder container. Raw builder logs are also available via `docker compose logs builder`.

## Testing

Each `setup_buildkit_{engine}_{mode}` target has a matching
`test_integration_buildkit_{engine}_{mode}` target (start → build the matching
`test/Dockerfile.*` → verify → clean up):

```bash
make test_integration_buildkit_universal_audit
make test_integration_buildkit_universal_restrict
make test_integration_buildkit_explicit_audit
make test_integration_buildkit_explicit_restrict
make test_integration_buildkit_inspect_audit
make test_integration_buildkit_inspect_restrict
# Debian/apt build, which starts with no CA store at all
make test_integration_buildkit_inspect_debian_audit
make test_integration_buildkit_inspect_debian_restrict
# Learns rules from an inspect audit run, then enforces them unedited
make test_integration_buildkit_inspect_roundtrip
```

## Local Development

Local Usage above runs the builder image. This is about running the setup and report actions
themselves against changes that aren't published yet.

Sigstore verification requires a real, published GHCR image, so the setup action normally can't
run against an unpublished branch or local changes. This repo's own CI (`test_action` job in
`.github/workflows/test-e2e.yml`) tests the real `setup`/`report` actions end-to-end against a
locally built image instead, via a build-time-gated mechanism: `BUILDCAGE_BUILD_TEST_HOOKS=1 vp run
build` compiles `dist/main.cjs` where the `BUILDCAGE_LOCAL_IMAGE_REF` override is reachable.
The override logic lives in its own module (`src/core/lib/provenance/local-image-override.ts`), loaded
only via a dynamic `import()` gated by that build-time flag. Without the flag (i.e. every
normal/committed build), rolldown's own module-graph tree-shaking excludes that entire file from
the bundle. It is physically absent, not just unreachable. A CI check (`unit_test` job)
additionally confirms a normal build never contains a live runtime read of
`BUILDCAGE_BUILD_TEST_HOOKS` in `dist`, guarding against a future refactor silently breaking
that guarantee.

To exercise it locally:

1. Build the image: `docker compose build` (set `PROXY_ENGINE` to select the engine).
2. `BUILDCAGE_BUILD_TEST_HOOKS=1 vp run build`
3. Run it with `BUILDCAGE_LOCAL_IMAGE_REF=<image ref from step 1>` set (e.g. via `act`, or by
   invoking `node dist/main.cjs` directly with the relevant `INPUT_*` env vars). Never commit a
   `dist/main.cjs` built this way: run `vp run build` again (without the flag) before committing.

See [security.md](./security.md#verification-limitations) for more details.

## Formatting & Linting

Formatting, linting, and type-aware linting are handled by [vp (Vite+)](https://viteplus.dev/),
installed globally on your machine like `pnpm`/`corepack` rather than through `pnpm exec`:

```bash
curl -fsSL https://vite.plus | bash   # macOS/Linux
# Windows: irm https://viteplus.dev/install.ps1 | iex
```

The project pins its own toolchain version via the `vite-plus` devDependency in `package.json`
(the same way `packageManager` pins `pnpm`), and the globally installed `vp` binary detects and
delegates to that pinned version automatically, so plain `vp ...` commands are reproducible without
going through `pnpm exec`. This was verified against `vp v0.2.7` / local `vite-plus
v0.2.5`; if a much newer `vp` behaves differently, that's the version to compare against.

```bash
vp check       # format + lint + type-aware lint (read-only; what CI runs)
vp check --fix # same, but auto-fixes format/lint issues in place
vp lint --fix
vp fmt --write
```

`vp run typecheck` (`tsc`) remains the authoritative full type check; `vp check`'s type-aware
linting (via `oxlint-tsgolint`) catches a subset of type-driven issues fast but doesn't replace it.

Running `vp install` (in place of `pnpm install`) automatically sets up a pre-commit hook, via the
`prepare` script, that formats and lints your staged files (`vite.config.ts`'s `staged` config)
before each commit, auto-fixing and re-staging what it can.

## Viewing Logs

```bash
# All communication logs
docker compose logs builder

# Real-time log monitoring
docker compose logs -f builder
```

**Log format (`universal` and `explicit`):**

```
[28/Feb/2026:10:15:30 +0000] buildcage [ALLOWED] "github.com:443" -
[28/Feb/2026:10:15:31 +0000] buildcage [BLOCKED] "malicious.com:443" not-allowed
[28/Feb/2026:10:15:32 +0000] buildcage [AUDIT] "npmjs.org:80" -
```

Fields: `[timestamp] buildcage [status] "domain:port" reason`

**`inspect` reads two logs instead**, since a name CoreDNS refused never reaches HAProxy at all:

```bash
docker compose exec builder cat /var/log/haproxy/current
docker compose exec builder cat /var/log/coredns/current
```

HAProxy's log carries one line per request, oldest first, with its method, full URL, status and
size. Refusals are interleaved with the rest, since nothing here can be attributed to a `RUN` step
the way `explicit`'s per-step breakdown can:

```
✅ 00:00.512: GET https://registry.npmjs.org/express -> 200 (99.9KB)
🚫 00:01.048: DNS secret-data.attacker.example -> dns-not-allowed
🚫 00:01.390: POST https://registry.npmjs.org/express/-rev/1-abc -> not-allowed
✅ 00:02.115: TLS db.example.com:5432 -> (12.3KB)
```

Times are relative to when the proxy started. A refusal names its reason rather than a status: 403,
502 and 503 mean a rule, a name that would not resolve, and an origin that could not be reached or
verified.

Each log is an s6-log directory rather than a single file: `current` rotates into a timestamped
archive once it crosses 1MB, up to 100 archives kept. The report reads every archive, oldest first,
then `current`, so early traffic is never dropped just because a later part of the same run pushed
the log past a rotation. Reading `current` by hand, as above, only shows what has accumulated since
the most recent one.

## Makefile Commands

`make help` lists every target with its own description. The ones you type most:

| Command                                          | Description                                                            |
| ------------------------------------------------ | ---------------------------------------------------------------------- |
| `make setup_buildkit_{engine}_{mode}`            | Start a builder, for any engine x `{audit,restrict}`                   |
| `make report_buildkit`                           | Show the report for the currently running builder                      |
| `make clean_buildkit`                            | Stop and remove the builder's containers/images and the buildx builder |
| `make test_unit`                                 | Every unit test, the QuickJS ones included (needs Docker)              |
| `make test_integration_buildkit`                 | Every `test_integration_buildkit_*` target in turn                     |
| `make test_integration_buildkit_{engine}_{mode}` | One engine and mode (start, build, verify, clean up)                   |

The integration set also holds `inspect_debian_{audit,restrict}` (an apt build that starts with no
CA store), `inspect_roundtrip` (learn rules from an audit run, then enforce them unedited), and
`universal_restrict_no_traffic`.

## Directory Structure

```text
.
├── action.yml                # Setup action entry (node24 → dist/main.cjs, dist/post.cjs)
├── src/                      # Source (ESM): verify image provenance, resolve image ref, compose up
│   ├── lib/                  # Setup action's own small helpers (errors.ts)
│   └── core/                 # Code shared across actions
│       ├── lib/               # All shared library code, consolidated: acl/ (rule parsing) is
│       │                     # dual-consumed by Node and QuickJS; test/test-shim.ts is a portable
│       │                     # node:test-alike shim used by *.test.ts across the whole tree
│       │                     # (Node and QuickJS alike). Everything else is Node-only, used by the
│       │                     # setup and report actions' Node runtime and report-action.node.ts,
│       │                     # never by the QuickJS scripts: log/, report/, docker/, provenance/
│       │                     # (Sigstore, OCI registry lookups, image ref resolution, local-image
│       │                     # test-hook override), actions/
│       └── scripts/           # QuickJS entry point (convert-rule.ts), run inside the built images
│                             # (rolldown-bundled into /opt/buildcage/scripts/ at image build time;
│                             # see rolldown.scripts.config.js). test/ is a qjs test runner, types/
│                             # is the qjs:std/qjs:os ambient type declaration
├── dist/                     # Bundled output (rolldown → CommonJS); dist/qjs, dist/qjs-test,
│                             # dist/report-action are gitignored build-time scratch output, not committed
├── docker/                   # proxy_engine build contexts
│   ├── compose.action.yaml   # Runtime compose file the action itself uses (verified, digest-pinned
│   │                         # image ref), distinct from the top-level compose.yaml below
│   ├── lib/                  # write-step-summary.ts, shared by both engines' report-action.node.ts
│   ├── universal/            # proxy_engine: universal. Dockerfile + BuildKit/haproxy/dnsmasq/
│   │                         # s6-overlay config + scripts/report-action.node.ts (runs under Node
│   │                         # on the runner, `docker cp`'d out by the report action)
│   ├── inspect/               # proxy_engine: inspect. Dockerfile + HAProxy/CoreDNS/s6-overlay
│   │                         # config + buildcage-runc/ (Go module: wraps buildkit-runc to inject
│   │                         # CA trust at exec time) + scripts/report-action.node.ts
│   └── explicit/             # proxy_engine: explicit. Dockerfile + buildkit-proxy/ (Go module:
│                             # entrypoint/PID1, supervises buildkitd, injects the source policy
│                             # into Solve via a gRPC proxy) + scripts/ (gen-source-policy.ts runs
│                             # under QuickJS; report-action.node.ts runs under Node on the runner,
│                             # `docker cp`'d out by the report action. TypeScript, rolldown-bundled
│                             # at image build time)
├── test/                     # Dockerfile.*/assert-*.sh per {engine}-{mode} combination, plus the
│                             # fixture containers: test-server and test-dns per engine, and
│                             # test-server-impostor / test-udp-echo for the inspect assertions
├── compose.test-*.yaml       # Test override config, one per engine
├── report/                   # GitHub Actions report action
│   ├── action.yml            # Action entry (node24 → dist/main.cjs)
│   ├── src/                  # Source (ESM): log analysis, per-command breakdown, Job Summary output
│   └── dist/                 # Bundled output (rolldown → CommonJS)
├── docs/                     # development.md, security.md, explicit-engine.md, plus the
│                             # reference.md/rules.md/inspect-engine.md link stubs
├── compose.yaml              # Docker Compose config for local dev (dockerfile path selected by
│                             # PROXY_ENGINE; also defines the local-dev `proxy` service)
└── Makefile                  # Operational commands
```

## Inspect Engine Internals

This section covers how `proxy_engine: inspect` is implemented internally. For the user-facing
behavior, see [Inspect Proxy Engine](./security.md#inspect-proxy-engine) in Security Details.

- `PROXY_ENGINE=inspect` selects `docker/inspect/Dockerfile` at build time (see `compose.yaml`'s
  `build.dockerfile: docker/${PROXY_ENGINE:-universal}/Dockerfile`), the same mechanism `explicit`
  uses.
- **HAProxy** is the single listener. `req.ssl_hello_type` tells a TLS handshake from a plain
  request by its first bytes, so one `bind` line handles both without the config declaring per-port
  whether it's plaintext or TLS. Two HAProxy features carry the rest of the enforcement:
  `normalize-uri` (an upstream directive still marked experimental, gated behind
  `expose-experimental-directives` in `src/core/lib/acl/haproxy-config.ts`) resolves `..` in the
  path before ACLs see it, and `do-resolve` + `set-dst` resolve the requested name and rewrite the
  connection's destination to it, run only after the ACL check for that request has already passed.
- **CoreDNS** answers every query with the proxy's own address, allowed or not, using an `expr`
  plugin view compiled from the same host patterns HAProxy's own ACLs use, so what's logged as
  `allowed` matches exactly what HAProxy would actually let through:

  ```
  # Allowlisted names are logged as allowed, but answered exactly like a denied
  # one below: this resolver never gets a request any closer to a real address.
  . {
      view allowlist {
        expr name() matches '^(abc[^.]*\.amazonaws\.com|registry\.npmjs\.org)\.$'
      }
      template IN A   { answer "{{ .Name }} 60 IN A <proxy-ip>" }
      template IN AAAA { }
      log . "buildcage dns allowed name={name}"
  }
  ```

- **`buildcage-runc`** (`docker/inspect/buildcage-runc/`) wraps BuildKit's own `buildkit-runc`,
  selected via `[worker.oci] binary` in `buildkitd.toml`. For the subcommands that carry an OCI
  bundle, it sets the CA-trust environment variables (see
  [CA trust and compatibility](../README.md#ca-trust-and-compatibility)) directly, and for the CA
  itself, mirrors the step's CA store directory into a scratch copy, appends the CA there, and
  bind-mounts the copy over the step's view of the real directory for the step's duration. Once the
  real `runc` exits, that mirror is compared against its state right after the CA was added: if
  nothing else changed, the real directory was never opened for writing, so BuildKit's layer diff for
  that step is unaffected; only a step that actually changed the store gets that change synced back.
  This is what keeps a step that never touches its CA store from producing a different layer than an
  unmodified build would. Either way this happens at exec time, entirely outside LLB, so it cannot
  affect a cache key: two builds that differ only in `proxy_engine` still share cache.
- The `allowed_url_rules` compiler enumerates hosts rather than generalizing them
  (`a.example.com`/`b.example.com` never becomes `*.example.com`), because CoreDNS's own allow/deny
  view is generated from the same host patterns. Widening a host widens what's logged as allowed
  DNS-side, not only what matches HTTP-side.
- `make test_integration_buildkit_inspect_roundtrip` (see [Testing](#testing) above) runs an audit
  build, feeds its own generated `allowed_url_rules` back as `restrict`, and checks both halves:
  every request the audit saw still passes, and a path, method, host, or port it never saw is
  refused.

## Explicit Engine Internals

> [!WARNING]
> `explicit` is **deprecated**; see [Explicit Proxy Engine](./explicit-engine.md). This section is
> kept for existing maintenance only; it receives no further development.

This section covers how `proxy_engine: explicit` is implemented internally. For the user-facing
behavior (what's enforced, what's visible in the report), see
[Explicit Proxy Engine](./security.md#explicit-proxy-engine) in Security Details.

- A small statically-linked Go binary (`docker/explicit/buildkit-proxy/`) is the image's entrypoint
  (PID 1) and directly supervises the real `buildkitd` as a child process. `RUN` steps are isolated
  into their own point-to-point network namespace by `proxyNetwork = true`, built directly on
  netlink/veth rather than CNI.
- At startup, the binary: writes `/etc/resolv.conf` from `EXTERNAL_RESOLVER` if that variable is
  set (otherwise the container's own resolv.conf, e.g. Docker's embedded DNS, is left untouched);
  runs a QuickJS script that compiles `allowed_https_rules` / `allowed_http_rules` /
  `allowed_ip_rules` (the same syntax as `universal`; see
  [Rule syntax](../README.md#rule-syntax)) into a BuildKit
  [source policy](https://github.com/moby/buildkit/blob/master/docs/proxy.md); starts `buildkitd`
  with `proxyNetwork = true` bound to an internal Unix socket; and starts its own gRPC listener on
  the socket path Buildx actually connects to.
- That gRPC listener sits in front of the real `buildkitd` control socket. It intercepts only the
  `Solve` RPC to inject the compiled source policy, and transparently relays every other RPC
  (`Session`, `Status`, `DiskUsage`, etc.) to the real daemon without decoding it, so future
  BuildKit versions that add new RPCs are automatically supported.
- If the build client has already set a **static** source policy on the request (e.g. via the
  `EXPERIMENTAL_BUILDKIT_SOURCE_POLICY` environment variable, which `docker buildx build` reads
  unconditionally), buildcage **merges** it with its own policy rather than rejecting the build,
  placing its own rules last so they always have the final say for every `http(s)` source: a
  client-supplied policy can never widen access beyond `allowed_https_rules` / `allowed_http_rules` /
  `allowed_ip_rules`. For any other scheme (`docker-image://`, `git://`, etc.) buildcage's rules
  never match, so the client's rules apply unmodified: buildcage only ever governs what it was
  configured to govern. A **dynamic**, session-based policy (`docker buildx build --policy=...`,
  `docker/buildx`'s own Rego policy feature) is a separate mechanism and is left untouched; it
  applies as an additional condition alongside buildcage's (merged) policy.

## Troubleshooting

If you encounter issues, try reproducing the problem locally to get detailed logs:

1. **Check logs:**

   ```bash
   docker compose logs builder
   ```

2. **Run in audit mode** to understand your build's network behavior:

   ```bash
   make clean_buildkit
   make setup_buildkit_universal_audit
   docker buildx build --builder buildcage --no-cache -f Dockerfile .
   docker compose logs builder
   ```

3. **TLS/certificate errors under `proxy_engine: inspect`**: if a `RUN` step fails with a
   certificate error there but works fine under `universal`, the tool likely pins a certificate or
   ships its own trust store rather than reading the CA-trust environment variables Buildcage sets.
   See [CA trust and compatibility](../README.md#ca-trust-and-compatibility). The JVM is the common
   case; fall back to `universal` for it.

4. **TLS/certificate errors under `proxy_engine: explicit`**: if a `RUN` step fails with a
   certificate error there but works fine under `universal` (or without Buildcage at all), the tool
   likely bundles its own CA store instead of consulting the system one BuildKit already trusts. See
   [CA trust for tools with their own CA store](./explicit-engine.md#ca-trust-for-tools-with-their-own-ca-store)
   in the Explicit Proxy Engine doc.

5. **Open an issue** at [github.com/buildcage/docker/issues](https://github.com/buildcage/docker/issues) with:
   - Your Dockerfile
   - The audit mode report output
   - Full error messages from `docker compose logs builder`
