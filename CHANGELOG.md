# Changelog

## Unreleased

- fix: skip Redis instance/database discovery for endpoints with an active `Pause Clients` attack in `ALL` mode. `CLIENT PAUSE ALL` is server-wide and exempts no client, so the extension's own discovery connection was timing out; affected endpoints now serve the last successful discovery result until the pause expires or is stopped.

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