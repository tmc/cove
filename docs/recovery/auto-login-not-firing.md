# Auto-login watchdog not firing

Use this when a macOS VM was provisioned successfully but the next headed boot
stops at the login screen and the user agent does not connect until the
password is typed manually.

## Expected Flow

`cove provision -apply` writes the disk-side auto-login files:

- `/private/etc/kcpassword`
- `/Library/Preferences/com.apple.loginwindow.plist`

It also writes a host-side watchdog cache:

```text
~/.vz/vms/<vm>/autologin.json
```

On the next `cove run -gui`, cove reads that cache and registers the
login-screen watchdog. If macOS shows the lock screen instead of completing
auto-login, the watchdog detects it, types the cached password, and waits for
the desktop.

## Older VMs

VMs provisioned by older cove builds may have the disk-side files but no
host-side `autologin.json`. Seed the cache once, then run the VM again:

```sh
umask 077
cat > "$HOME/.vz/vms/default/autologin.json" <<'JSON'
{"Username":"myuser","Password":"mypassword"}
JSON
chmod 600 "$HOME/.vz/vms/default/autologin.json"
cove -vm default run -gui
```

Do not edit `kcpassword` or `loginwindow.plist` for this recovery. If those
disk-side files are missing or have the wrong owner, run `cove doctor --fix` or
re-run provisioning instead.
