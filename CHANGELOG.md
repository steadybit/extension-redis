# Changelog

## v1.1.13

- chore(deps): bump github.com/steadybit/action-kit/go/action_kit_test
- chore(deps): bump github.com/steadybit/discovery-kit/go/discovery_kit_test
- chore(deps): bump github.com/stretchr/testify from 1.11.1 to 1.12.1

## v1.1.12

- chore(deps): bump steadybit kits and drop Go patch pin (#45)

## v1.1.11

- chore(deps): bump github.com/redis/go-redis/v9 from 9.21.0 to 9.22.0

## v1.1.10

- feat: support filtering targets out of discovery
- fix: emit connection/latency/memory/replication metrics on Start

## v1.1.9

- chore(deps): update dependencies

## v1.1.8

- Add a "Fail early" option to the connection count, latency, memory and replication checks. When enabled, the check fails as soon as its threshold is exceeded instead of waiting for the end of the step. Disabled by default, matching the previous behavior of only reporting a threshold breach at the end.
- chore(deps): bump go to 1.26.5 (#40)
- ci: skip build on .trivyignore.yml-only changes [skip ci]
- feat(checks): add fail early option (#39)
- fix(checks): use internal time control so breaches fail at the end (#42)
- fix: the connection count, latency, memory and replication checks now use internal time control so a threshold breach is reliably reported at the end of the step. Previously they used external time control without a stop handler, so the end-of-step failure was never emitted and a breach only produced a warning (the check completed successfully even though its threshold was exceeded).
- refactor: register extension index via exthttp.RegisterRevisionedHandler (#41)

## v1.1.7

- Merge pull request #31 from steadybit/feat/add-claude-workflows
- chore(deps): bump github.com/redis/go-redis/v9 from 9.20.1 to 9.21.0
- chore(deps): bump github.com/steadybit/action-kit/go/action_kit_sdk
- chore(deps): bump github.com/steadybit/discovery-kit/go/discovery_kit_sdk
- chore(deps): bump github.com/steadybit/event-kit/go/event_kit_api
- chore(deps): bump github.com/steadybit/extension-kit
- chore: silence SonarQube finding on secrets: inherit in Claude workflows
- fix: avoid duplicating the node address in cluster restore errors
- fix: report a failed stop for the maxmemory-limit attack when the cluster cannot be reached during restore, instead of silently reporting success while the target's `maxmemory` is left altered
- fix: report failed maxmemory restore when the cluster is unreachable
- fix: stop leaking Redis credentials embedded in endpoint URLs (#32)
- fix: strip credentials from Redis endpoint URLs before publishing them as target attributes/metric labels and before logging them, so a password embedded in a `redis://user:pass@host` URL is no longer exposed to the platform or logs (the full credentials remain in the endpoint configuration and are still used to connect)
- refactor: collapse maxmemory restore onto a single error channel

## v1.1.6

- chore(deps): bump github.com/steadybit/extension-kit
- chore(deps): bump golang.org/x/net to v0.55.0 (CVE-2026-39821) (#27)

## v1.1.5

- chore(deps): bump alpine from 3.23 to 3.24
- chore(deps): bump github.com/redis/go-redis/v9 from 9.19.0 to 9.20.0
- chore(deps): bump github.com/redis/go-redis/v9 from 9.20.0 to 9.20.1
- chore: update to go 1.26.4
- feat: add weekly auto patch-release workflow

## v1.1.4

- breaking: the `Pause Clients` attack now always issues `CLIENT PAUSE ... WRITE` and the `pauseMode` parameter has been removed. `CLIENT PAUSE ALL` could not be aborted early (Redis blocks `CLIENT UNPAUSE` itself under an active ALL pause) and also stalled the extension's own discovery probes for the entire duration. Pausing only writes keeps the attack fully reversible via `CLIENT UNPAUSE` and lets discovery (PING/INFO) keep running. The attack has been relabeled "Pause Write Clients".

## v1.1.3

- Support discovery group attribute via `STEADYBIT_EXTENSION_DISCOVERY_GROUP` env var (or `discovery.group` Helm value) — when set, the extension adds `steadybit.group=<value>` to every discovered target
- Update dependencies

## v1.1.2

- Bump Go to 1.26.3
- Update dependencies
- Improved action descriptions

## v1.1.1

- Bump Go to 1.25.9

- Support if-none-match for the extension list endpoint

## v1.0.1

- fix: no default value for cache key
- fix: reduce CPU and memory usage of BigKey attack
- fix: used memory in MB
- fix: client close unexpectedly
- Update alpine packages in Docker image to address CVEs
- Update dependencies

## v1.0.0

 - Initial release
