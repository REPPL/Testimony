#!/bin/sh
# testimony installer — https://github.com/REPPL/Testimony
#
# Usage (one line):
#   curl -fsSL https://raw.githubusercontent.com/REPPL/Testimony/main/install.sh | sh
#
# Prefer to inspect first (recommended):
#   curl -fsSLO https://raw.githubusercontent.com/REPPL/Testimony/main/install.sh
#   less install.sh && sh install.sh
#
# Passing flags through a pipe:
#   curl -fsSL .../install.sh | sh -s -- --yes --dir "$HOME/bin"
#
# Flags:
#   -d, --dir DIR     install directory (default: ~/.local/bin, or $TESTIMONY_INSTALL_DIR
#                     when set — no admin rights needed)
#   -y, --yes         non-interactive: accept each dependency's default install
#                     choice (ffmpeg: brew if present, otherwise the local,
#                     admin-free option; ASR: whisperx via uv, always — see
#                     dep_asr for why)
#       --no-deps     install the binary only; print dependency guidance and exit
#       --version V   install release V instead of the default
#   -h, --help        this text
#
# Trust model. The binary install downloads the platform tarball AND the release's
# published SHA256SUMS, and verifies the tarball against it (integrity — the bytes
# are exactly what the release published). When an AUTHENTICATED `gh` (the GitHub
# CLI) is available it ALSO runs `gh attestation verify` against the release
# workflow's SLSA build-provenance (authenticity — cryptographic proof the tarball
# was built by REPPL/Testimony's own release.yml, the strong anchor). A gh that
# cannot attempt the verification — absent, too old to know attestations, or
# unable to establish its authentication (which an unreachable API also causes
# at the auth check) — means the install proceeds on the checksum alone, with
# a note saying so. Once the verification is attempted, any failure refuses
# the install, fail closed: a rejection, and equally a verification that
# could not complete, because mid-verification failure cannot be told apart
# from an attacker blocking it. Re-run when connectivity returns.
# No per-release hash is pinned in this script: the checksums are fetched from
# the release itself and the attestation binds them to the workflow.
# Everything installs into user-owned locations by default; sudo is never invoked.

set -eu

REPO="REPPL/Testimony"
VERSION="v0.4.0"

# Pinned OpenPGP fingerprint of the evermeet.cx ffmpeg publisher key
# (Helmut K. C. Tessarek, key id 0x476C4B611A660874). The local-macOS ffmpeg
# path verifies the build's signature against THIS key only — never any key the
# signature happens to name — so an attacker-signed substitute build is refused.
EVERMEET_FPR="20F6EA3E0CFD6B4C53447A73476C4B611A660874"

INSTALL_DIR="${TESTIMONY_INSTALL_DIR:-$HOME/.local/bin}"
ASSUME_YES=0
NO_DEPS=0

say()  { printf '%s\n' "$*"; }
err()  { printf 'install.sh: %s\n' "$*" >&2; }
die()  { err "$*"; exit 1; }

have() { command -v "$1" >/dev/null 2>&1; }

# Prompt via the terminal even when stdin is the pipe. Returns 0 for yes.
# With --yes, always yes. With no terminal at all, always the safe default (no).
ask() {
    q="$1"
    [ "$ASSUME_YES" = 1 ] && return 0
    if [ -r /dev/tty ] && [ -w /dev/tty ]; then
        printf '%s [y/N] ' "$q" > /dev/tty
        IFS= read -r reply < /dev/tty || reply=""
        case "$reply" in [yY]|[yY][eE][sS]) return 0 ;; *) return 1 ;; esac
    fi
    return 1
}

# choose "Question" "option-a" "option-b" → prints the chosen word. When both
# options are the same word there is only one choice to offer, so it is rendered
# (and matched) once: [local/skip], never [local/local/skip].
choose() {
    q="$1"; a="$2"; b="$3"
    if [ "$ASSUME_YES" = 1 ]; then printf '%s' "$a"; return; fi
    if [ "$a" = "$b" ]; then opts="$a"; else opts="$a/$b"; fi
    if [ -r /dev/tty ] && [ -w /dev/tty ]; then
        printf '%s [%s/skip] ' "$q" "$opts" > /dev/tty
        IFS= read -r reply < /dev/tty || reply=""
        case "$reply" in
            "$a") printf '%s' "$a" ;;
            "$b") printf '%s' "$b" ;;
            *)    printf 'skip' ;;
        esac
    else
        printf 'skip'
    fi
}

fetch() { # fetch URL FILE
    if have curl; then curl -fsSL -o "$2" "$1"
    elif have wget; then wget -qO "$2" "$1"
    else die "need curl or wget"
    fi
}

sha256_of() {
    if have shasum; then shasum -a 256 "$1" | awk '{print $1}'
    elif have sha256sum; then sha256sum "$1" | awk '{print $1}'
    else die "need shasum or sha256sum to verify the download"
    fi
}

platform() {
    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    arch=$(uname -m)
    case "$os" in darwin|linux) : ;; *) die "unsupported OS: $os (build from source: go build ./cmd/testimony)" ;; esac
    case "$arch" in
        arm64|aarch64) arch=arm64 ;;
        x86_64|amd64)  arch=amd64 ;;
        *) die "unsupported architecture: $arch" ;;
    esac
    printf '%s_%s' "$os" "$arch"
}

install_binary() {
    plat=$(platform)
    tarball="testimony_${VERSION}_${plat}.tar.gz"
    base="https://github.com/$REPO/releases/download/$VERSION"

    tmp=$(mktemp -d "${TMPDIR:-/tmp}/testimony-install.XXXXXX")
    # Both traps sweep every mktemp dir the script can hold ($tmp here; $tmp2,
    # $gnupg, $uvd in the dependency stage — unset expansions vanish), so an
    # interrupt mid-dependency leaks nothing. A caught INT/TERM must also STOP
    # the script: a trap that only cleans up returns control after the handler,
    # so Ctrl+C at a dependency prompt read was swallowed into the safe-default
    # answer and the run carried on.
    trap 'rm -rf ${tmp:+"$tmp"} ${tmp2:+"$tmp2"} ${gnupg:+"$gnupg"} ${uvd:+"$uvd"} ${staged:+"$staged"}' EXIT
    trap 'rm -rf ${tmp:+"$tmp"} ${tmp2:+"$tmp2"} ${gnupg:+"$gnupg"} ${uvd:+"$uvd"} ${staged:+"$staged"}; trap - EXIT; exit 130' INT TERM

    say "Downloading $tarball ..."
    # A bad --version (or a platform the release never published) surfaces from
    # curl/wget as a bare 404 in the middle of "Downloading". Name the actual
    # cause instead. Still fail-closed: die exits non-zero, nothing is retried.
    fetch "$base/$tarball" "$tmp/$tarball" || die "could not download $tarball
  Release \"$VERSION\" — or its $plat asset — was not found at
    $base/$tarball
  Release tags are of the form vX.Y.Z (note the leading 'v').
  Releases: https://github.com/$REPO/releases"

    # Integrity: verify the tarball against the release's published SHA256SUMS.
    # No hash is pinned in this script — it is fetched from the release itself.
    say "Downloading SHA256SUMS ..."
    fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || die "could not download SHA256SUMS from $base"
    want=$(awk -v f="$tarball" '$2 == f {print $1}' "$tmp/SHA256SUMS")
    [ -n "$want" ] || die "no entry for $tarball in SHA256SUMS"
    got=$(sha256_of "$tmp/$tarball")
    [ "$got" = "$want" ] || die "SHA-256 mismatch for $tarball
  expected: $want
  got:      $got
Refusing to install."
    say "SHA-256 verified against the release's SHA256SUMS: $got"

    # Authenticity: when the GitHub CLI is available, verify the tarball's SLSA
    # build-provenance attestation — cryptographic proof it was built by this
    # repo's release workflow, not merely that its bytes match a fetched manifest
    # (which an attacker who replaced BOTH the tarball and SHA256SUMS could forge).
    # --signer-workflow binds acceptance to release.yml specifically, not any
    # workflow in the repo. Without gh, proceed on the checksum with a printed note.
    #
    # A gh that CANNOT ATTEMPT the verification is the checksum-only case, not a
    # provenance failure: `gh attestation verify` refuses to run unauthenticated
    # (exit 4, before any verification), and a gh predating the attestation
    # command or --signer-workflow fails the same way — treating those as
    # "attestation FAILED" made a freshly `brew install`ed gh strictly worse
    # than no gh at all, refusing a tarball whose checksum had just verified.
    # Once the verification is attempted, every failure refuses the install,
    # fail closed — a rejected attestation, and equally a verification that
    # fails mid-way (network, API outage): at that point an inconclusive
    # answer cannot be told apart from one an attacker is blocking. The auth
    # check above draws the attempt boundary: a gh whose authentication
    # cannot be established — which an unreachable API also causes — is the
    # checksum-fallback case, with a note naming it. gh's own output is shown
    # instead of being swallowed.
    if have gh && ! gh auth status --hostname github.com >/dev/null 2>&1; then
        say "NOTE: 'gh' is installed but not authenticated — installed on the checksum alone."
        say "      'gh attestation verify' needs an authenticated gh; run 'gh auth login'"
        say "      (or set GH_TOKEN) and re-run to also verify SLSA build-provenance."
    elif have gh; then
        say "Verifying SLSA build-provenance attestation with gh ..."
        if gh attestation verify "$tmp/$tarball" \
               --repo "$REPO" \
               --signer-workflow "$REPO/.github/workflows/release.yml" >"$tmp/attest.log" 2>&1; then
            say "Provenance verified: built by $REPO/.github/workflows/release.yml"
        elif grep -qiE 'unknown (command|flag)' "$tmp/attest.log"; then
            say "NOTE: this gh cannot verify attestations (it lacks 'attestation verify"
            say "      --signer-workflow') — installed on the checksum alone. Update gh"
            say "      (https://cli.github.com) and re-run to also verify build provenance."
        else
            sed 's/^/  gh: /' "$tmp/attest.log" >&2
            die "attestation verification FAILED for $tarball
  gh could not confirm this tarball was built by $REPO's release workflow.
Refusing to install."
        fi
    else
        say "NOTE: 'gh' (GitHub CLI) not found — installed on the checksum alone."
        say "      Install gh to also verify SLSA build-provenance (authenticity):"
        say "        https://cli.github.com  — then re-run this installer."
    fi

    tar -xzf "$tmp/$tarball" -C "$tmp" testimony
    # Prove the binary runs and is the release it claims BEFORE it replaces
    # anything: a failing command substitution inside say's argument does not
    # trip `set -e`, so an unrunnable binary (a wrong-platform asset)
    # previously replaced the installed one and printed "Installed: ... ()"
    # at exit 0. The probe runs from a staged copy inside INSTALL_DIR — the
    # download's temp directory may sit on a noexec mount (a hardened /tmp,
    # a noexec TMPDIR), where executing "$tmp/testimony" fails for a
    # perfectly good binary — and only the verified copy is renamed onto the
    # final name, so a refusal leaves any previously installed binary
    # untouched. Releases predating the version stamp (v0.1.0) report
    # "testimony dev" and are refused here; every release since prints its
    # own tag.
    mkdir -p "$INSTALL_DIR"
    staged="$INSTALL_DIR/.testimony.staged.$$"
    install -m 0755 "$tmp/testimony" "$staged"
    installed_version="$("$staged" version)" || { rm -f "$staged"; die "the release binary failed to run from $INSTALL_DIR (a wrong-platform asset, or a noexec mount?): $tarball"; }
    [ "$installed_version" = "testimony $VERSION" ] || { rm -f "$staged"; die "the release binary reports \"$installed_version\", expected \"testimony $VERSION\"; refusing to install it"; }
    mv -f "$staged" "$INSTALL_DIR/testimony"
    say "Installed: $INSTALL_DIR/testimony ($installed_version)"

    case ":$PATH:" in
        *":$INSTALL_DIR:"*) : ;;
        *) say ""
           say "NOTE: $INSTALL_DIR is not on your PATH. Add it, e.g.:"
           say "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.zshrc && exec zsh" ;;
    esac
}

# --- dependencies -----------------------------------------------------------
# record's capture needs ffmpeg (so does transcribe -audio's conversion; a bare
# transcribe needs none), and transcribe needs one ASR engine (WhisperX
# preferred, whisper.cpp works too). demo/merge/report need nothing. Local
# options never require admin rights; brew needs a Homebrew install but not
# sudo on default setups.

dep_ffmpeg() {
    if have ffmpeg; then say "ffmpeg: already installed ($(command -v ffmpeg))"; return; fi
    say ""
    say "ffmpeg is required by 'testimony record' (audio/screen capture) and by"
    say "'testimony transcribe -audio' (converting an external recording)."
    if have brew; then
        c=$(choose "Install ffmpeg via" "brew" "local")
    else
        c=$(choose "Install ffmpeg (no Homebrew found)" "local" "local")
    fi
    case "$c" in
        brew) brew install ffmpeg || err "brew install ffmpeg failed; skipping ffmpeg (later: brew install ffmpeg)" ;;
        local) install_ffmpeg_local ;;
        skip) say "Skipped. Later: brew install ffmpeg  (or re-run this installer)" ;;
    esac
}

install_ffmpeg_local() {
    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    mkdir -p "$INSTALL_DIR"
    tmp2=$(mktemp -d "${TMPDIR:-/tmp}/testimony-ffmpeg.XXXXXX")
    case "$os" in
        darwin)
            # evermeet.cx publishes a GPG signature (.sig) per build; verify it
            # against the PINNED publisher key ($EVERMEET_FPR) when gpg is
            # available, and refuse on a bad or wrong-key signature.
            # Every fetch/unpack below is guarded with the err-skip-return
            # convention the parse failure already uses: ffmpeg is an OPTIONAL
            # dependency, and under `set -eu` an unguarded failure aborted the
            # whole installer with the child's raw exit code — skipping the ASR
            # step and the closing guidance, and leaking $tmp2 (the EXIT trap
            # covers only install_binary's $tmp).
            say "Fetching static ffmpeg build (evermeet.cx) ..."
            fetch "https://evermeet.cx/ffmpeg/info/ffmpeg/release" "$tmp2/info.json" \
                || { err "could not reach evermeet.cx; skipping ffmpeg"; rm -rf "$tmp2"; return; }
            u=$(sed -n 's/.*"zip":{"url":"\([^"]*\)".*/\1/p' "$tmp2/info.json" | head -1)
            [ -n "$u" ] || { err "could not parse evermeet.cx response; skipping ffmpeg"; rm -rf "$tmp2"; return; }
            fetch "$u" "$tmp2/ffmpeg.zip" \
                || { err "ffmpeg download failed; skipping ffmpeg"; rm -rf "$tmp2"; return; }
            if have gpg; then
                fetch "$u.sig" "$tmp2/ffmpeg.zip.sig" \
                    || { err "could not fetch the ffmpeg signature; refusing this unverifiable build"; rm -rf "$tmp2"; return; }
                # Import ONLY the pinned publisher key into a throwaway keyring,
                # then verify against it. --auto-key-retrieve is never used: it
                # would fetch whatever key the (attacker-supplied) signature
                # names and accept a build signed by that key. We also assert the
                # good signature's VALIDSIG carries the pinned fingerprint, so a
                # signature made by any other key is rejected. Fail closed.
                gnupg=$(mktemp -d "${TMPDIR:-/tmp}/testimony-gpg.XXXXXX")
                status=$(GNUPGHOME="$gnupg" gpg --batch --no-auto-key-retrieve \
                             --keyserver hkps://keys.openpgp.org \
                             --recv-keys "$EVERMEET_FPR" >/dev/null 2>&1 \
                         && GNUPGHOME="$gnupg" gpg --batch --no-auto-key-retrieve --status-fd 1 \
                             --verify "$tmp2/ffmpeg.zip.sig" "$tmp2/ffmpeg.zip" 2>/dev/null) || true
                rm -rf "$gnupg"
                if printf '%s\n' "$status" | grep -q "VALIDSIG.*$EVERMEET_FPR"; then
                    say "ffmpeg GPG signature verified (pinned evermeet key $EVERMEET_FPR)."
                else
                    err "ffmpeg GPG signature verification FAILED (not signed by the pinned evermeet key); refusing this build."
                    rm -rf "$tmp2"; return
                fi
            else
                say "WARNING: gpg not found — installing this ffmpeg build unverified"
                say "         (its signature is at $u.sig)."
            fi
            (cd "$tmp2" && unzip -q ffmpeg.zip) \
                || { err "could not unpack ffmpeg; skipping ffmpeg"; rm -rf "$tmp2"; return; }
            install -m 0755 "$tmp2/ffmpeg" "$INSTALL_DIR/ffmpeg" \
                || { err "could not install ffmpeg into $INSTALL_DIR; skipping ffmpeg"; rm -rf "$tmp2"; return; }
            ;;
        linux)
            arch=$(uname -m)
            case "$arch" in x86_64) ja=amd64 ;; aarch64|arm64) ja=arm64 ;; *) err "no static ffmpeg for $arch"; rm -rf "$tmp2"; return ;; esac
            say "Fetching static ffmpeg build (johnvansickle.com) ..."
            fetch "https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-${ja}-static.tar.xz" "$tmp2/ffmpeg.tar.xz" \
                || { err "ffmpeg download failed; skipping ffmpeg"; rm -rf "$tmp2"; return; }
            fetch "https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-${ja}-static.tar.xz.md5" "$tmp2/ffmpeg.md5" || true
            if [ -s "$tmp2/ffmpeg.md5" ] && have md5sum; then
                (cd "$tmp2" && sed 's| .*ffmpeg-release.*| ffmpeg.tar.xz|' ffmpeg.md5 | md5sum -c -) \
                    || { err "ffmpeg md5 mismatch; skipping"; rm -rf "$tmp2"; return; }
                say "ffmpeg md5 verified (upstream publishes md5 only)."
            else
                say "WARNING: could not verify the static ffmpeg build; installing unverified."
            fi
            tar -xJf "$tmp2/ffmpeg.tar.xz" -C "$tmp2" \
                || { err "could not unpack ffmpeg; skipping ffmpeg"; rm -rf "$tmp2"; return; }
            install -m 0755 "$tmp2"/ffmpeg-*-static/ffmpeg "$INSTALL_DIR/ffmpeg" \
                || { err "could not install ffmpeg into $INSTALL_DIR; skipping ffmpeg"; rm -rf "$tmp2"; return; }
            ;;
    esac
    rm -rf "$tmp2"
    say "Installed: $INSTALL_DIR/ffmpeg (user-local, no admin rights needed)"
}

dep_asr() {
    if have whisperx; then say "ASR: whisperx already installed"; return; fi
    if have whisper-cli; then say "ASR: whisper.cpp already installed"; return; fi
    say ""
    say "'testimony transcribe' needs one local ASR engine:"
    say "  whisperx    — word-level timestamps (preferred; Python, installs user-local via uv)"
    say "  whisper.cpp — segment-level (Homebrew; also needs a ggml model file)"
    if have brew; then
        c=$(choose "Install" "whisperx" "whisper.cpp")
    else
        c=$(choose "Install (no Homebrew found)" "whisperx" "whisperx")
    fi
    case "$c" in
        whisperx)
            if ! have uv; then
                if ask "whisperx installs via uv (user-local, no admin). Install uv first (astral.sh installer)?"; then
                    # Download+execute inside a private mktemp -d, not a fixed
                    # /tmp/uv-install.sh: a predictable, world-writable path lets
                    # a local attacker on a shared host pre-plant a symlink or win
                    # the write→exec race and run their own code as the user.
                    uvd=$(mktemp -d "${TMPDIR:-/tmp}/testimony-uv.XXXXXX")
                    fetch "https://astral.sh/uv/install.sh" "$uvd/uv-install.sh" \
                        || { err "could not download the uv installer; skipping whisperx (later: uv tool install whisperx)"; rm -rf "$uvd"; return; }
                    sh "$uvd/uv-install.sh" \
                        || { err "uv installation failed; skipping whisperx (later: uv tool install whisperx)"; rm -rf "$uvd"; return; }
                    rm -rf "$uvd"
                    # uv lands in ~/.local/bin; make it visible to this run.
                    PATH="$HOME/.local/bin:$PATH"; export PATH
                else
                    say "Skipped. Later: uv tool install whisperx   (or: pipx install whisperx)"
                    return
                fi
            fi
            uv tool install whisperx \
                || { err "whisperx installation failed; later: uv tool install whisperx (or: pipx install whisperx)"; return; }
            say "whisperx installed (user-local). First run downloads its models."
            ;;
        whisper.cpp)
            brew install whisper-cpp \
                || { err "brew install whisper-cpp failed; later: brew install whisper-cpp"; return; }
            say ""
            say "whisper.cpp needs a ggml model. Download once (~1.5 GB), user-local,"
            say "into a directory '-model NAME' searches:"
            say "  mkdir -p ~/.cache/whisper.cpp && curl -fL -o ~/.cache/whisper.cpp/ggml-large-v3-turbo.bin \\"
            say "    https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-turbo.bin"
            say "Then: testimony transcribe -engine whispercpp ...   (-model large-v3-turbo is the default)"
            ;;
        skip)
            say "Skipped. Later: uv tool install whisperx   or   brew install whisper-cpp" ;;
    esac
}

# The help text is embedded rather than sed-extracted from "$0": through the
# documented pipe invocation $0 is the shell's own argv[0] ("sh", or a path
# like /bin/sh whose bytes sed would happily print), not this script.
usage() {
    cat <<'EOF'
testimony installer — https://github.com/REPPL/Testimony

Usage (one line):
  curl -fsSL https://raw.githubusercontent.com/REPPL/Testimony/main/install.sh | sh

Passing flags through a pipe:
  curl -fsSL .../install.sh | sh -s -- --yes --dir "$HOME/bin"

Flags:
  -d, --dir DIR     install directory (default: ~/.local/bin, or $TESTIMONY_INSTALL_DIR
                    when set — no admin rights needed)
  -y, --yes         non-interactive: accept each dependency's default install
                    choice (ffmpeg: brew if present, otherwise the local,
                    admin-free option; ASR: whisperx via uv, always)
      --no-deps     install the binary only; print dependency guidance and exit
      --version V   install release V instead of the default
  -h, --help        this text
EOF
}

main() {
    while [ $# -gt 0 ]; do
        case "$1" in
            -d|--dir)  [ $# -ge 2 ] || die "--dir needs a value (try --help)"; INSTALL_DIR="$2"; shift 2 ;;
            -y|--yes)  ASSUME_YES=1; shift ;;
            --no-deps) NO_DEPS=1; shift ;;
            --version) [ $# -ge 2 ] || die "--version needs a value (try --help)"; VERSION="$2"; shift 2 ;;
            -h|--help) usage; exit 0 ;;
            *) die "unknown flag: $1 (try --help)" ;;
        esac
    done

    say "testimony installer — release $VERSION, install dir $INSTALL_DIR"
    install_binary

    if [ "$NO_DEPS" = 1 ]; then
        say ""
        say "Dependencies skipped (--no-deps). 'testimony record' needs ffmpeg; 'testimony transcribe' needs whisperx or whisper.cpp (and ffmpeg with -audio)."
        exit 0
    fi

    dep_ffmpeg
    dep_asr

    say ""
    say "Done. Try:  testimony demo    (capture a session; speak while you click)"
}

main "$@"
