# Gateway PHP automation extension

This optional extension executes trusted local PHP automation through a PHP CLI
process. It is distributed as an installable `ext.package/v1` Spinner guest;
PHP is not compiled into or installed in the default Gateway host image.

The target host must separately provide a PHP CLI executable. Set
`GATEWAY_PHP_AUTOMATION_BINARY` when it is not available as `php` on `PATH`.
The extension fails closed during launch when that executable is missing.

Scripts receive a `$gateway` object with `event`, `get`, `set`, `respond`,
`forward`, and `schedule` capabilities. PHP automation is trusted local code,
not a PHP sandbox: installing this extension deliberately grants scripts the
permissions of its supervised extension process. Use JavaScript, Lua, or
HookScript for a narrower scripting surface.
