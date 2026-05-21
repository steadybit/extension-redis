# Changelog

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