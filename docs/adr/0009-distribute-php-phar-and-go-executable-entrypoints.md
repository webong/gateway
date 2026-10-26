---
status: accepted
---

# Distribute PHP archives and compiled Go binaries through Ext

Gateway keeps `ext.package/v1` as the common release and installation boundary, with each artifact declared and hashed in the package manifest. A Planner companion may use a PHP CLI source entrypoint or a self-contained PHAR, both implementing `php-jsonline` over `ext.plugin/v1`; Spinner uses a compiled Go process executable declared as the `spinner` entrypoint with matching OS and architecture metadata. Each plane independently verifies and reconciles only its own entrypoint from the combined package. We do not introduce a Go archive loader: Go `.a` archives are build/link inputs, not runtime programs, and compiling or linking them inside Spinner would add toolchain and ABI coupling. The outer Ext archive remains language-neutral so one release can coordinate the Go and PHP artifacts.
