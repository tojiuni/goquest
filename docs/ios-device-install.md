# iOS device deployment — `xcodebuild` vs `devicectl`

Runbook for installing the `Goquest` iOS / watchOS app (from
[`Lyckabc/goquest-ios`](https://github.com/Lyckabc/goquest-ios)) onto a
physical iPhone / Apple Watch.

**Short version**: Xcode 16's `xcodebuild ... install` does **not** push to
the device anymore — it stages the `.app` into a local
`InstallationBuildProductsLocation` directory and exits successful. To
actually land the binary on the device, run `xcrun devicectl device install
app` as a separate step.

## Symptoms

The footgun looks like:

```
$ xcodebuild -project Goquest.xcodeproj -scheme Goquest \
    -destination 'platform=iOS,id=<UDID>' \
    -configuration Debug -allowProvisioningUpdates build install
…
** BUILD SUCCEEDED **
** INSTALL SUCCEEDED **
```

Both stages report success. The phone's home screen shows nothing new. The
app icon you wanted to update is still the previous version (or absent).

Verifying with `devicectl` exposes the truth:

```
$ xcrun devicectl device info apps --device <DEVICE_ID>
Apps installed:
  (no entry for home.toji.goquest)
```

## Correct pipeline

Three discrete steps. Don't combine them.

```bash
# 1. Build (no install action). Produces a Debug-iphoneos/Goquest.app.
cd ~/Documents/dev/goquest-ios
xcodebuild \
  -project Goquest.xcodeproj \
  -scheme Goquest \
  -destination 'platform=iOS,id=<XCODEBUILD_UDID>' \
  -configuration Debug \
  -allowProvisioningUpdates \
  build

# 2. Locate the produced .app under DerivedData.
APP=$(find ~/Library/Developer/Xcode/DerivedData/Goquest-*/Build/Products/Debug-iphoneos \
        -name 'Goquest.app' -type d -maxdepth 2 | head -1)

# 3. Push to the device.
xcrun devicectl device install app --device <DEVICECTL_UUID> "$APP"
```

If you also want a clean slate (icon caches, Keychain) prepend an
uninstall:

```bash
xcrun devicectl device uninstall app --device <DEVICECTL_UUID> home.toji.goquest
```

## UDID format gotcha

The two CLIs use **different identifiers** for the same physical device.

| Tool | ID format | Example |
|------|-----------|---------|
| `xcodebuild -destination 'platform=iOS,id=…'` | Apple's **ECID** (hex) | `00008150-000641DE3A28401C` |
| `xcrun devicectl device …` | macOS **CoreDevice UUID** (RFC 4122) | `60E23CBB-E240-55DC-BEBE-DD47A76B1E6B` |

Pulling them is a one-shot each, but pull them from the right place:

```bash
# xcodebuild ECID — listed by xctrace
xcrun xctrace list devices | grep -v Simulator

# devicectl UUID — listed by devicectl
xcrun devicectl list devices
```

Mixing them gives the famously unhelpful error:

```
xcodebuild: error: Unable to find a device matching the provided destination specifier:
  { platform:iOS, id:60E23CBB-…, error:… doesn't have a known architecture. }
```

That's not a device problem; it's a wrong ID format.

## Verifying the install

```bash
$ xcrun devicectl device info apps --device <DEVICECTL_UUID> | grep -i goquest
Goquest   home.toji.goquest   1.0   1
```

If the app shows up there, the binary is on the device and the home
screen will reflect it (sometimes after re-locking + unlocking — iOS
icon cache).

## First launch

Free Apple developer signing prompts iOS with **Untrusted Developer** the
first time. Resolve once:

> Settings → General → VPN & Device Management → (your Apple ID) → Trust

After that, launches go straight through. App signed with a free
personal team also **expires after 7 days** — schedule a re-install or
move to a paid team via TestFlight if longer life is needed.

## When to use Xcode UI instead

If the deployment is interactive (debug session, looking at logs,
profiling), Xcode's Run (⌘R) is still the easiest path — it does the
right thing under the hood. The CLI pipeline above is for unattended /
scripted installs.

## Reference

- Apple docs: [Diagnose issues installing apps on devices using
  `devicectl`](https://developer.apple.com/documentation/xcode/diagnose-issues-installing-apps-on-devices-using-devicectl).
- Captured 2026-05-11 after losing several install cycles to the
  silent-success bug while shipping the Goquest logomark.
