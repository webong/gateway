# Proxy 0.2.0 registry migrations

Verbatim copies of `database/migrations/*.php` from `webong/proxy` tag `v0.2.0`.
`tests/Feature/ProxyRegistryUpgradeTest.php` runs them to build a registry as
Proxy 0.2.0 created it, so the Proxy 0.3.1 upgrade can be tested against real
legacy rows. Do not edit them or update them to a later Proxy release: their
value is that they stay exactly what 0.2.0 shipped.
