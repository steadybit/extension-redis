# Changelog

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
