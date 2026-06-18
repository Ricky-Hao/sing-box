# Agent instructions for this fork

This repository is a Ricky-Hao fork of `sing-box`. Keep the fork easy to rebase onto upstream `stable`: make small, intentional changes, keep upstream behavior as the default, and do not rewrite unrelated files.

## Fork maintenance rules

- Treat `rickyhao/stable` as the maintained fork branch based on upstream `stable`.
- Prefer upstream files and behavior unless a fork-specific requirement explicitly says otherwise.
- When changing GitHub Actions or release logic, reuse upstream workflow structure. Disable workflows that are not needed, but for workflows that remain enabled, only reduce outputs; roll unrelated workflow changes back to upstream.
- Keep each functional area squashable into one clear commit. Do not broad-format, reorder, or refactor unrelated code just to make a change look cleaner.
- Do not commit, tag, push, publish releases, or rewrite branch history unless explicitly asked.

## iWAN endpoint rules

- The iWAN endpoint is a fork feature and must be built with both `with_iwan` and `with_gvisor`.
- Keep `with_iwan` and `with_gvisor` wired in every relevant build surface:
  - CLI release tag files under `release/`.
  - Mobile/libbox tags in `cmd/internal/build_libbox/main.go`.
  - Include/stub registration under `include/`.
- iWAN is userspace-stack only for now. `system: true` must remain unsupported until a real system TUN implementation is intentionally designed.
- Preserve these documented semantics:
  - `address` is optional; the effective tunnel IPv4 address comes from OPENACK.
  - `allowed_ips` only provides route preference for `preferred_by`; it does not install OS routes.
  - Segment routing, OS route management, `up_script`/`down_script`, and applying OPENACK DNS to the system resolver are out of scope unless explicitly requested.
- Keep protocol behavior compatible with `iwand`: OPEN/OPENACK/OPENREJ/DATA/DATA_ENC/ECHO/CLOSE, IPFRAG reassembly, MD5 control signatures, AES-128 password TLV block, XOR data encryption, token/session handling, and TLV length semantics.
- Do not silently accept data from arbitrary UDP sources. Prefer connected UDP or explicit source validation.
- Do not change WireGuard behavior when using it as the endpoint pattern.

## Android/SFA rules

- SFA Android builds must include iWAN and gVisor in mobile libbox. If SFA reports that iWAN is not included, check `cmd/internal/build_libbox/main.go` before checking release tag files.
- SFA APK signing must use stable release signing secrets. Do not add or restore temporary/generated signing-key fallback for release APKs.
- Expected signing secrets:
  - `SFA_RELEASE_KEYSTORE`
  - `SFA_KEYSTORE_PASS`
  - `SFA_KEY_ALIAS`
  - `SFA_KEY_PASS`
- Never commit keystores, decoded signing files, generated secret files, or debug configs containing credentials.
- SFA release output is intentionally limited to `arm64-v8a`.
- For mobile iWAN troubleshooting, check MTU early. On 5G, start with iWAN MTU `1280`; if stable, try `1320` or `1360`. Avoid using `1420` as the default mobile MTU. If a `tun` inbound is used, keep `tun.mtu` aligned with the iWAN MTU.

## Release artifact policy

- Keep release assets narrow and predictable.
- Keep SFA APK output.
- Keep CLI archives only for:
  - Systems: `linux`, `windows`, `darwin`, `freebsd`.
  - Architectures: `amd64`, `arm64`.
  - Linux variants: pure Go and musl only.
- Do not build or publish glibc Linux assets.
- Do not publish SFM, deb, OpenWrt, or other upstream assets unless explicitly requested.
- Preserve macOS CLI binaries, but do not reintroduce SFM just for macOS.

## Validation rules

- For iWAN code changes, run focused tests with build tags:
  - `go test -tags with_iwan,with_gvisor ./transport/iwan ./protocol/iwan ./option`
  - `go test -tags with_iwan,with_gvisor ./include`
- For mobile/libbox tag changes, run:
  - `go test -run '^$' ./cmd/internal/build_libbox`
- Always run `gofmt` on changed Go files and `git diff --check` before reporting completion.
- For release or CI changes, verify the GitHub Actions build result and inspect the published asset list.
- For SFA iWAN inclusion issues, verify Android job logs show both:
  - `github.com/sagernet/sing-box/transport/iwan`
  - `github.com/sagernet/sing-box/protocol/iwan`
- For real iWAN smoke tests, use throwaway local debug configs and do not commit credentials or generated logs containing secrets.

