# xiaomi-router-driver-2

Linux-first router bundle for running `vpn-manager` on OpenWrt/Xiaomi routers.

## What is in the repo

- Go backend in `cmd/` and `internal/`
- React frontend in `frontend/`
- Router bundle templates in `packaging/router/`
- Linux bundle build scripts in `build.*` and `package_router.*`
- Ready-to-copy router bundle in `build/router`

Subscriptions accept URI/Base64 lists, sing-box JSON, and Xray JSON configs
(including arrays with per-location `remarks`). Xray import supports VLESS over
TCP/RAW with TLS/REALITY and Hysteria2 with optional Salamander obfuscation.
XHTTP and VLESS Encryption run through native Xray, preserving nested download
settings and finalmask. Each endpoint uses one TUN engine; policy routes remain
managed by the application. Unsupported chains are excluded. Legacy
client warnings are filtered, with alternate fetch profiles used when needed.

## Build

The primary build target is the router bundle in `build/router`.

- Windows: `build.bat`
- Linux/macOS shell: `./build.sh`

## Deploy

The repository keeps the ready router bundle in `build/router`.

Local deployment credentials are intentionally not versioned.
Use `deploy_router.local.example.sh` as a template for a local ignored `deploy_router.local.sh` on macOS/Linux.
The deploy script can also read the existing Windows-style `deploy_router.local.cmd` if you already have one.

- macOS/Linux: `./deploy_router.sh`
- Manual deploy: copy the contents of `build/router` to the router and start the service there.

## Runtime binaries

The application uses Linux `openvpn`, `sing-box`, and `xray` binaries on the router.
Run `bash fetch_xray.sh` to download the pinned, checksum-verified Xray v26.11.9
Linux arm64 release before packaging. `ROUTER_XRAY_BIN` can override its path;
`VPN_MANAGER_XRAY_BIN` overrides the executable at runtime. Older update bundles
without Xray remain compatible, but native endpoints need the new engine.

They can be provided in one of these ways:

- bundled in `build/router/bin`
- passed explicitly through `ROUTER_OPENVPN_BIN` and `ROUTER_SINGBOX_BIN`
- fetched from the target router during deploy if they are missing locally
