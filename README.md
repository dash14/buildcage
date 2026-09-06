# Buildcage for Docker

![Buildcage](./assets/banner.png)

[![GitHub](https://img.shields.io/badge/GitHub-buildcage%2Fdocker-blue?logo=github)](https://github.com/buildcage/docker)
[![Marketplace](https://img.shields.io/badge/marketplace-Buildcage%20for%20Docker-blue?logo=github)](https://github.com/marketplace/actions/buildcage-for-docker)
![version](https://img.shields.io/github/v/release/buildcage/docker)
![build](https://img.shields.io/github/actions/workflow/status/buildcage/docker/docker-publish.yml)
![test](https://img.shields.io/github/actions/workflow/status/buildcage/docker/test-e2e.yml?label=test)
![license](https://img.shields.io/github/license/buildcage/docker)

GitHub Action that restricts where `docker build` can connect. Every `RUN` step runs behind an
allowlist you write by HTTP method and URL, not only by hostname, so a build can be allowed to fetch
a package from a registry without being allowed to publish one to it.

Your Dockerfile does not change, and neither does BuildKit. Buildcage starts a builder inside the
job, routes the build's traffic through it, and leaves nothing in the image layers. Run once in
[`audit`](#operation-modes) mode and the report hands you the allowlist to paste back in. Everything
runs inside your GitHub Actions job: no agent, no external service.

See [buildcage.github.io](https://buildcage.github.io/) for what it does and why. To isolate a
workflow `run:` step rather than a Docker build, use
[Buildcage for `run:` Steps](https://github.com/buildcage/isolated-run).

## Contents

- [Usage](#usage)
- [Inputs](#inputs)
- [Operation modes](#operation-modes)
- [Rule syntax](#rule-syntax)
- [Engines](#engines)
- [CA trust and compatibility](#ca-trust-and-compatibility)
- [Report action](#report-action)
- [GitHub's native egress firewall](#githubs-native-egress-firewall)
- [Scope](#scope)
- [Documentation](#documentation)

## Usage

Buildcage starts a BuildKit builder in your job. Point Docker Buildx at it as a remote driver and
build as usual. Run once in [`audit`](#operation-modes) mode to collect what the build reaches, then
switch to `restrict`.

Two engines decide how closely that traffic is examined:

- **`inspect`** terminates TLS and reads the request, so a rule can name a method and a URL. It
  injects a CA into each `RUN` step as the step starts; neither the CA nor the variables that trust
  it are left in the image layers.
- **`universal`** never decrypts, so it also works where a CA cannot be injected, such as a tool
  that pins a certificate. Not decrypting means a rule reaches only as far as a host and a port.

The steps below use `inspect`. [Engines](#engines) compares the two in full.

### 1. Find out what the build reaches

```yaml
- name: Start Buildcage in audit mode
  uses: buildcage/docker@9db933f44e0dd4821ad7eea6f58f3b7bfd2f2db5 # v3.1.6
  with:
    proxy_mode: audit # Log every destination, block nothing
    proxy_engine: inspect # Record the method and URL of every request

- name: Set up Docker Buildx
  uses: docker/setup-buildx-action@37fe631027851001ddb9b187196cc803df7f5f0e # v4.3.0
  with:
    driver: remote
    endpoint: docker-container://buildcage

- name: Build
  uses: docker/build-push-action@53b7df96c91f9c12dcc8a07bcb9ccacbed38856a # v7.3.0
  with:
    context: .

- name: Show Buildcage report
  if: always()
  uses: buildcage/docker/report@9db933f44e0dd4821ad7eea6f58f3b7bfd2f2db5 # v3.1.6
```

The [report action](#report-action) writes every destination the build contacted to the Job Summary:

<img src="assets/report-inspect-audit-mode.png" alt="Outbound Traffic Report - audit mode" width="556">

Its **Switch to restrict mode** section holds the allowlist, already written out from what the build
actually did.

### 2. Enforce the allowlist

Paste that allowlist into the setup step and switch the mode:

```yaml
- name: Start Buildcage in restrict mode
  uses: buildcage/docker@9db933f44e0dd4821ad7eea6f58f3b7bfd2f2db5 # v3.1.6
  with:
    proxy_mode: restrict
    proxy_engine: inspect
    allowed_url_rules: |
      GET http://deb.debian.org/**
      GET https://registry.npmjs.org/**
      POST https://registry.npmjs.org/-/npm/v1/security/advisories/bulk
```

Each rule names the methods it permits, so this one lets npm fetch packages without letting it
publish any: a `POST` to the same host is refused, as is every host not listed. Whatever is refused
is listed under **Blocked Hosts** with the reason, and **Communication details** names the full URL
of every request, allowed or refused:

<img src="assets/report-inspect-restrict-mode.png" alt="Outbound Traffic Report - restrict mode" width="556">

A blocked connection fails the job at the report step, so a build that starts reaching somewhere new
doesn't pass unnoticed. Pass `fail_on_blocked: false` to that step to report without failing, or list
destinations you expect to stay blocked in `known_blocked_rules`.

Nothing else in the workflow changes. Buildcage injects a CA and the environment variables that trust
it when a `RUN` step starts, so the build sees an ordinary HTTPS connection and the Dockerfile stays
as it is.

### Example workflows

Each pair builds the same Dockerfile with and without rules:
`inspect` on an apt and npm build ([audit](.github/workflows/example-inspect-audit.yml) ·
[restrict](.github/workflows/example-inspect-restrict.yml)), `universal` on a Maven build
([audit](.github/workflows/example-universal-audit.yml) ·
[restrict](.github/workflows/example-universal-restrict.yml)).

### Notes

- The Buildx `endpoint` must match the `builder_name` input (default: `buildcage`).
- Multi-stage Dockerfiles work unchanged. Buildcage doesn't fork or patch BuildKit, it only wires up
  how build traffic is routed.
- Private registries are ordinary hosts: add the domain like any other.
- One registry often needs several domains. PyPI, for example, uses both `pypi.org` and
  `files.pythonhosted.org`. The audit report lists every one of them, so start from that.
- The generated allowlist covers only what the engine decrypted. `allow_tls_rules` and
  `allowed_ip_rules` come back exactly as the audit run was configured with them.
- If something in the build pins a certificate or carries its own trust store (the JVM is the usual
  case), use `proxy_engine: universal` instead. See [Engines](#engines).

## Inputs

Every input is optional.

| Input          | Default     | Description                                                           |
| -------------- | ----------- | --------------------------------------------------------------------- |
| `builder_name` | `buildcage` | Name of the builder container. The Buildx `endpoint` has to match it. |
| `proxy_mode`   | `restrict`  | `audit` or `restrict`. See [Operation modes](#operation-modes).       |
| `proxy_engine` | `universal` | `inspect` or `universal`. See [Engines](#engines).                    |

### Rule inputs

All of these are empty by default. Which ones apply depends on the engine:

| Input                 | `inspect` | `universal` | What one rule matches                                                               |
| --------------------- | :-------: | :---------: | ----------------------------------------------------------------------------------- |
| `allowed_url_rules`   |    ✅     |      -      | A method and a URL: `GET https://registry.npmjs.org/**`                             |
| `allowed_https_rules` |    ✅     |     ✅      | A host and port reached over HTTPS: `registry.npmjs.org:443`                        |
| `allowed_http_rules`  |    ✅     |     ✅      | A host and port reached over plain HTTP: `deb.debian.org:80`                        |
| `allowed_ip_rules`    |    ✅     |     ✅      | An address and port, for connections made without DNS: `192.168.1.1:443`            |
| `allow_tls_rules`     |    ✅     |      -      | A TLS destination to pass through undecrypted, judged on SNI: `db.example.com:5432` |
| `known_blocked_rules` |    ✅     |     ✅      | A host expected to be blocked, so it doesn't fail the [report](#report-action)      |

Setting a rule the engine can't act on is caught before the build starts: `restrict` fails, since a
rule that looks like it protects the build but cannot be enforced is worse than none, and `audit`
warns and ignores it.

See [Rule syntax](#rule-syntax) for the grammar. The deprecated `explicit` engine takes the same
host rules as `universal`; see [Explicit Proxy Engine](./docs/explicit-engine.md).

## Operation modes

| `proxy_mode` | What it does                                                      | When to use it                                             |
| ------------ | ----------------------------------------------------------------- | ---------------------------------------------------------- |
| `audit`      | Logs every destination the build reaches and blocks nothing       | First setup, adding a dependency, investigating a failure  |
| `restrict`   | Allows only what the rules match, blocks and logs everything else | Everyday builds, CI/CD pipelines, security-critical builds |

`audit` allows what the active engine can classify. A connection it cannot classify, such as an HTTP
request carrying no `Host` header, is still refused, on each engine's own terms (see
[Engines](#engines)).

If you forget a domain the build needs, `restrict` blocks it and the report step fails with the
destination named, which is why it is worth running `audit` first.

## Rule syntax

`allowed_url_rules` and `allow_tls_rules` need `proxy_engine: inspect`. The host rules work with
either engine. The inputs are additive: a connection is allowed when any rule in any of them
matches.

### URL rules: `allowed_url_rules`

A rule is a method list, a space, then a URL pattern. Because a rule contains a space, this input is
newline-separated. The method is required, so a rule always states what it permits. A blank line, or
a line starting with `#`, is ignored, which helps once the list gets long.

```yaml
allowed_url_rules: |
  # npm installs
  GET https://registry.npmjs.org/@myorg/**
  GET|HEAD https://example.com/public/*

  # internal write access
  POST,PUT https://api.internal.example.com/v1/*
  * https://internal.example.com
```

Methods are separated by `|` or `,`, and `*` means any method. The port may be left out when it is
the scheme's default, and a pattern with no path allows any path on that host.

| Pattern | In a domain                                       | In a path                     |
| ------- | ------------------------------------------------- | ----------------------------- |
| `**`    | crosses dots                                      | crosses `/`                   |
| `*`     | one or more, not crossing a dot                   | one or more, not crossing `/` |
| `?`     | one character                                     | one character                 |
| `~`     | raw regex, split into a host half and a path half |                               |

A `~` rule is split at the first `/` after `://` rather than applied to the whole URL: everything
before that `/` is matched against the host, everything from it onward against the path. So
`~^https://example\.com/pub/.*$` becomes a host match on `example\.com` and a path match on
`/pub/.*$`. The host half's port pattern can be any regex (`example\.com:(443|8443)`,
`example\.com:\d+`), matched against the connection's own `host:port`. Leave it out and the rule
matches the scheme's default port only, 443 for `https` and 80 for `http`; there is no implicit
any-port, so write `example\.com:.*` to allow more.

A wildcard may sit among literal text, in a domain label or a path segment: `abc*.amazonaws.com`,
`/pkg-*/**`. A path or method never narrows what a wildcard _host_ resolves. See
[Inspect Proxy Engine](./docs/security.md#inspect-proxy-engine) for why, and for how to write a host
pattern that doesn't widen more than intended.

A rule may name an address rather than a name. Nothing is loosened by that: the rules still match
against the `Host` header and still decide, and an address reached this way stays inspected, so
method and path rules apply to it. Over HTTPS the origin's certificate has to be valid for the
address, which needs an IP SAN, so in practice an address is a plaintext or a passthrough
destination.

### Host rules: `allowed_https_rules`, `allowed_http_rules`, `allowed_ip_rules`, `known_blocked_rules`

These four share one syntax. Rules are separated by whitespace, so one per line reads best. A host
rule is equivalent to a URL rule with any method and any path.

#### Wildcards

| Pattern | Matches                                                     | Example                                                                  |
| ------- | ----------------------------------------------------------- | ------------------------------------------------------------------------ |
| `*`     | One or more characters **excluding** dots (single label)    | `*.example.com` matches `sub.example.com` but not `deep.sub.example.com` |
| `**`    | One or more characters **including** dots (multiple labels) | `**.example.com` matches `sub.example.com` and `deep.sub.example.com`    |
| `?`     | A single character excluding dots                           | `exampl?.com` matches `example.com`, `examplx.com`                       |

A label that contains `*` has to be exactly `*` or `**`. `abc*.example.com` is rejected here; only
`allowed_url_rules` takes a wildcard in the middle of a label.

#### Ports

A port is required on every rule.

| Rule                 | Matches                                                       |
| -------------------- | ------------------------------------------------------------- |
| `example.com:443`    | `example.com` on port 443 only                                |
| `*.example.com:8443` | Any single-level subdomain of `example.com` on port 8443 only |
| `example.com:*`      | `example.com` on any port                                     |

### IP addresses: `allowed_ip_rules`

Connections made straight to an address never go through DNS, so they are allowed separately from
any domain. IPv4 only, and what a rule may hold depends on the engine:

| Engine      | A rule can be                                               | It cannot be       |
| ----------- | ----------------------------------------------------------- | ------------------ |
| `inspect`   | An address, a CIDR block (`10.0.0.0/8:443`), or a `~` regex | A wildcard pattern |
| `universal` | An address, a wildcard, or a `~` regex                      | A CIDR block       |

Either way the connection is tunnelled without inspection: once an `ip:port` pair is allowed, any
TCP-based protocol can use that path. Prefer a domain rule where the destination has a stable name.

### TLS passthrough: `allow_tls_rules`

For TLS traffic that isn't HTTPS. The SNI and port are checked and the connection passes through
undecrypted, so the build validates the origin's own certificate:

```yaml
allow_tls_rules: |
  db.example.com:5432
```

### Regular expressions

Prefix a rule with `~` to use a regular expression. A host rule's pattern is matched against
`domain:port` as one expression, so the port is part of the pattern and can be a regex itself. It
cannot be left out: either engine refuses a `~` host rule with no `:` in it, since what the pattern
is matched against always carries the port.

| Rule                              | Effect                                                     |
| --------------------------------- | ---------------------------------------------------------- |
| `~^example\.com:443$`             | Matches `example.com` on port 443 only                     |
| `~^example\.com:\d+$`             | Matches `example.com` on any port                          |
| `~^.*\.example\.com:(443\|8443)$` | Matches any subdomain of `example.com` on port 443 or 8443 |
| `~^192\.168\.1\.\d+:80$`          | Matches a range of IP addresses (in `allowed_ip_rules`)    |

In `allowed_url_rules` a `~` expression covers the URL, and is split into a host half and a path
half as described above. A rule the split cannot handle, one with no `/` after `://`, is refused
with an error naming what to write instead.

## Engines

`proxy_engine` selects how Buildcage sees the build's traffic.

|                                               | `inspect`<br>terminates TLS, checks method and URL          | `universal`<br>reads the SNI only, checks host and port |
| --------------------------------------------- | ----------------------------------------------------------- | ------------------------------------------------------- |
| A rule can say                                | `GET\|HEAD https://registry.npmjs.org/**`                   | `registry.npmjs.org:443`                                |
| Allow a fetch, refuse a publish, same host    | ✅                                                          | -                                                       |
| The report shows                              | Every request with its full URL                             | Host and port                                           |
| Domain fronting (allowed SNI, another `Host`) | Refused, the real `Host` is what rules match                | Not visible                                             |
| The build's TLS                               | Terminated and re-signed with a CA generated for that build | Untouched                                               |
| Certificate pinning, or the JVM's own store   | -                                                           | ✅                                                      |
| Traffic as a JSON artifact                    | ✅                                                          | -                                                       |

Both intercept at the network level, so a tool that ignores `HTTP_PROXY` is covered either way, and
both apply to `RUN` steps. `FROM` is buildkitd's own traffic and stays outside.

Start with `inspect`, and fall back to `universal` when something in the build won't accept the
injected CA; see [CA trust and compatibility](#ca-trust-and-compatibility). `universal` is the
default value of `proxy_engine`, so `inspect` has to be set explicitly. `transparent` is accepted as
an alias for `universal`, the name it had before `inspect` existed.

`proxy_engine: explicit` is BuildKit's own native `--proxy-network`. It still works but is
deprecated and receives no further development; see
[Explicit Proxy Engine](./docs/explicit-engine.md) if you already depend on it, most commonly for
its BuildKit-native SLSA provenance integration.

For the architecture and threat model behind each engine, see
[Security Details](./docs/security.md). For implementation internals, see the
[Development Guide](./docs/development.md).

## CA trust and compatibility

This section is about `proxy_engine: inspect`, which terminates TLS and re-signs it with a CA
generated for the build, so the build has to trust that CA. If a variable below is already set, by
the base image or by the Dockerfile, Buildcage appends the CA to whatever file it already points at
rather than redirecting the variable elsewhere. Otherwise, where it points depends on whether the
step has a system CA store:

| Variable              | Read by                                                                         | If unset, with a store                           | If unset, with no store     |
| --------------------- | ------------------------------------------------------------------------------- | ------------------------------------------------ | --------------------------- |
| `NODE_EXTRA_CA_CERTS` | Node.js                                                                         | Additive: pointed at a file holding only this CA | same, store or no store     |
| `DENO_CERT`           | Deno                                                                            | Additive: pointed at a file holding only this CA | same, store or no store     |
| `CURL_CA_BUNDLE`      | curl                                                                            | Left unset; curl already reads the system store  | proxy-CA-only fallback file |
| `REQUESTS_CA_BUNDLE`  | Python `requests`                                                               | Replaces the bundle: pointed at the system store | proxy-CA-only fallback file |
| `PIP_CERT`            | pip                                                                             | Replaces the bundle: pointed at the system store | proxy-CA-only fallback file |
| `SSL_CERT_FILE`       | OpenSSL, and anything reading it (Go, Ruby, wget, Rust's `rustls-native-certs`) | Replaces the bundle: pointed at the system store | proxy-CA-only fallback file |

**Not supported under `inspect`:** a tool that pins a certificate, or ships its own trust store
instead of reading the variables above. The JVM (Java, Kotlin, Scala) is the common case, since it
only reads its own `cacerts` file. Use `proxy_engine: universal` for those.

**No system CA store** (`scratch`, distroless, or `debian:*-slim` before `ca-certificates` is
installed): every variable above still gets set, but only to a dedicated file trusting the proxy's
own CA, with no public roots. That is enough for ordinary HTTPS, since `inspect` re-signs all of it
with the same CA. It is not enough for an `allow_tls_rules` or `allowed_ip_rules` passthrough, which
presents its own real certificate and needs a real store already in place to verify against. This is
decided once, from the rootfs as the step begins, so installing `ca-certificates` partway through a
step doesn't help a passthrough connection made later in that same step:

```dockerfile
RUN apt-get install -y ca-certificates && \
    curl https://internal.example.com/pkg.tgz -o pkg.tgz   # still fails: CURL_CA_BUNDLE was already
                                                              # fixed to the proxy-CA-only fallback
                                                              # before apt-get ran
```

```dockerfile
RUN apt-get install -y ca-certificates
RUN curl https://internal.example.com/pkg.tgz -o pkg.tgz   # this step starts with a store, so
                                                              # CURL_CA_BUNDLE points at it instead
                                                              # of the proxy-CA-only fallback
```

**The CA store's directory is a mount point for the step's duration**, so removing or renaming the
directory itself (not files inside it) fails instead of succeeding:

```dockerfile
RUN rm -rf /etc/ssl/certs        # fails: the directory is a mount point and can't be removed itself
RUN rm -rf /etc/ssl/certs/*      # fine: removing what's inside it works normally
```

The same applies to a Dockerfile-chosen custom path (an already-set CA-trust variable pointing
somewhere of its own), unless its directory is unexpectedly large (more than 20 MiB or 512 files), in
which case injection is skipped for that variable only, the same graceful degradation as when no CA
bundle is found at all.

See [Inspect Proxy Engine](./docs/security.md#inspect-proxy-engine) for the threat model and attack
resistance.

## Report action

`buildcage/docker/report` reads the builder's communication log, writes the Job Summary, and
optionally fails the job when blocked connections are found.

```yaml
- name: Show Buildcage report
  if: always()
  uses: buildcage/docker/report@9db933f44e0dd4821ad7eea6f58f3b7bfd2f2db5 # v3.1.6
```

Every input is optional.

| Input                             | Default     | Description                                                                                   |
| --------------------------------- | ----------- | --------------------------------------------------------------------------------------------- |
| `builder_name`                    | `buildcage` | Name of the builder container                                                                 |
| `fail_on_blocked`                 | `true`      | Fail the step if blocked connections are detected (restrict mode only; ignored in audit mode) |
| `upload_traffic_artifact`         | `false`     | Upload the observed traffic as a JSON artifact named `buildcage-traffic`, `inspect` only      |
| `traffic_artifact_retention_days` | empty       | How long to keep that artifact, in days; empty uses the repository's own default              |

In restrict mode the step fails when blocked connections are detected, failing the workflow with it.
In audit mode, blocked connections (protocol errors, for instance) are reported but never fail the
step.

If some blocked connections are expected, say a known-noisy dependency, or a domain you are
deliberately keeping off the allowlist to confirm it stays blocked, list them in the setup action's
`known_blocked_rules` input. When every blocked connection matches, the step no longer fails even
with `fail_on_blocked: true`, and a `::notice::` is emitted instead of `::error::`; any unmatched
blocked connection still fails the step. Once `known_blocked_rules` is set, the Blocked Hosts table
gains an **Expected** column (✅) marking the matched rows.

### Traffic artifact

`upload_traffic_artifact: true` uploads the same timeline as a `traffic.json` inside an artifact
named `buildcage-traffic` (`buildcage-traffic-<builder_name>` when the builder is not the default
one). It carries name lookups that only resolved as well, which is how a too-wide rule being probed
shows up. `universal` never sees a method or a URL, so this input only does anything under
`inspect`.

| Field         | Always | Notes                                                  |
| ------------- | ------ | ------------------------------------------------------ |
| `time`        | yes    | ISO 8601 UTC                                           |
| `elapsed`     |        | since the proxy started, fixed `HH:MM:SS.mmm`          |
| `action`      | yes    | `allow`, `block`, or `audit` when nothing was enforced |
| `protocol`    | yes    | `https`, `http`, `tls`, `tcp`, `dns`                   |
| `host`        | yes    | the name asked for, or the address when there was none |
| `port`        |        | absent for `dns`, which connects to nothing            |
| `method`      |        | `http` and `https` only                                |
| `url`         |        | `http` and `https` only                                |
| `status`      |        | only when something answered                           |
| `bytes`       |        | absent for a refusal and for `dns`                     |
| `reason`      |        | only when `action` is `block`                          |
| `destination` |        | the address it actually resolved to; absent for `dns`  |

A field is absent because it does not apply, never because it was zero: a refusal has no status
because nothing answered, and a passthrough none because nothing was decrypted. Filter on `action`.
The artifact is uploaded even when the build fails, since a failing run is when it is most wanted.

```json
[
  {
    "time": "2026-09-02T04:11:07.512Z",
    "elapsed": "00:00:00.512",
    "action": "allow",
    "protocol": "https",
    "host": "registry.npmjs.org",
    "port": 443,
    "method": "GET",
    "url": "https://registry.npmjs.org/express",
    "status": 200,
    "bytes": 102300,
    "destination": "104.16.0.35"
  },
  {
    "time": "2026-09-02T04:11:08.048Z",
    "elapsed": "00:00:01.048",
    "action": "block",
    "protocol": "dns",
    "host": "secret-data.attacker.example",
    "reason": "dns-not-allowed"
  },
  {
    "time": "2026-09-02T04:11:08.390Z",
    "elapsed": "00:00:01.390",
    "action": "block",
    "protocol": "https",
    "host": "registry.npmjs.org",
    "port": 443,
    "method": "POST",
    "url": "https://registry.npmjs.org/express/-rev/1-abc",
    "reason": "not-allowed"
  }
]
```

## GitHub's native egress firewall

GitHub is building an egress firewall directly into Actions runners
([technical preview](https://github.com/github-early-access/actions-native-egress-firewall) as of
August 2026): opt a job into a firewall-enabled runner image and every step's traffic is observed at
the runner boundary. Today that is audit only, with enforcement not yet available, and it applies to
the whole job at once.

Buildcage sits at a different layer and works alongside it: allowlists are scoped per `docker build`
rather than per job, rules can name a method and a URL rather than only a host, and enforcement (not
just audit) is available now.

## Scope

Buildcage controls _where_ your build can connect, not _what code_ it runs. A malicious package
delivered through an allowed domain still runs. Treat it as one layer in a defense-in-depth
strategy, a last line of defense so that if something slips through your other measures, at least it
can't call home.

An allowlist also cannot stop anything leaving through a service you had to allow anyway. That is a
structural limit. What it does stop is traffic to a destination that is not on the list, and
infrastructure an attacker set up is normally not on it, because the build has no reason to reach
it. That is also the hardest kind of leak to find afterwards.

An allowlist generated from an audit run already blocks every destination the audit did not record.
Whether to go further depends on what the build has access to:
[Hardening](./docs/security.md#hardening) is what to look at when it holds credentials, personal
data, or source you do not publish. For the full threat model, see
[Security Details](./docs/security.md).

## Documentation

| Doc                                                | What's in it                                                      |
| -------------------------------------------------- | ----------------------------------------------------------------- |
| [Security Details](./docs/security.md)             | Architecture and threat model for every engine, attack resistance |
| [Development Guide](./docs/development.md)         | Local usage, testing, logs, and implementation internals          |
| [Explicit Proxy Engine](./docs/explicit-engine.md) | The deprecated `proxy_engine: explicit` in full                   |

## Contributing

Contributions are welcome! Please feel free to submit issues or pull requests at [github.com/buildcage/docker](https://github.com/buildcage/docker).

## Show Your Support

Knowing that this project is useful to others gives me the motivation to keep working on it.
If you find Buildcage helpful, please consider giving it a star ⭐ on GitHub!

## Disclaimer

This software is provided "as is", without warranty of any kind, express or implied. The authors and contributors are not liable for any damages, losses, or security incidents arising from the use of this software. Use at your own risk.

## License

The Buildcage source code is licensed under the MIT License. See [LICENSE](./LICENSE) file for details.

The Docker image includes third-party components under their own licenses (GPL, Apache 2.0, ISC, etc.). See [THIRD_PARTY_LICENSES](./THIRD_PARTY_LICENSES) for the full list.
