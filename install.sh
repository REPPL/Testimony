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
#   -d, --dir DIR     install directory (default: ~/.local/bin — no admin rights needed)
#   -y, --yes         non-interactive: accept dependency installs (brew if present,
#                     otherwise the local, admin-free option)
#       --no-deps     install the binary only; print dependency guidance and exit
#       --version V   install release V instead of the default
#   -h, --help        this text
#
# Trust model. The binary install downloads the platform tarball AND the release's
# published SHA256SUMS, and verifies the tarball against it (integrity — the bytes
# are exactly what the release published). When `gh` (the GitHub CLI) is available
# it ALSO runs `gh attestation verify` against the release workflow's SLSA
# build-provenance (authenticity — cryptographic proof the tarball was built by
# REPPL/Testimony's own release.yml, the strong anchor). Without gh it proceeds on
# the checksum alone and prints a note that installing gh enables provenance
# verification. No per-release hash is pinned in this script: the checksums are
# fetched from the release itself and the attestation binds them to the workflow.
# Everything installs into user-owned locations by default; sudo is never invoked.

set -eu

REPO="REPPL/Testimony"
VERSION="v0.1.0"

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

# choose "Question" "option-a" "option-b" → prints the chosen word.
choose() {
    q="$1"; a="$2"; b="$3"
    if [ "$ASSUME_YES" = 1 ]; then printf '%s' "$a"; return; fi
    if [ -r /dev/tty ] && [ -w /dev/tty ]; then
        printf '%s [%s/%s/skip] ' "$q" "$a" "$b" > /dev/tty
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
    trap 'rm -rf "$tmp"' EXIT INT TERM

    say "Downloading $tarball ..."
    fetch "$base/$tarball" "$tmp/$tarball"

    # Integrity: verify the tarball against the release's published SHA256SUMS.
    # No hash is pinned in this script — it is fetched from the release itself.
    say "Downloading SHA256SUMS ..."
    fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS"
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
    if have gh; then
        say "Verifying SLSA build-provenance attestation with gh ..."
        if gh attestation verify "$tmp/$tarball" \
               --repo "$REPO" \
               --signer-workflow "$REPO/.github/workflows/release.yml" >/dev/null 2>&1; then
            say "Provenance verified: built by $REPO/.github/workflows/release.yml"
        else
            die "attestation verification FAILED for $tarball
  gh could not confirm this tarball was built by $REPO's release workflow.
Refusing to install."
        fi
    else
        say "NOTE: 'gh' (GitHub CLI) not found — installed on the checksum alone."
        say "      Install gh to also verify SLSA build-provenance (authenticity):"
        say "        https://cli.github.com  — then re-run this installer."
    fi

    mkdir -p "$INSTALL_DIR"
    tar -xzf "$tmp/$tarball" -C "$tmp" testimony
    install -m 0755 "$tmp/testimony" "$INSTALL_DIR/testimony"
    say "Installed: $INSTALL_DIR/testimony ($("$INSTALL_DIR/testimony" version))"

    case ":$PATH:" in
        *":$INSTALL_DIR:"*) : ;;
        *) say ""
           say "NOTE: $INSTALL_DIR is not on your PATH. Add it, e.g.:"
           say "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.zshrc && exec zsh" ;;
    esac
}

# --- dependencies -----------------------------------------------------------
# transcribe needs: ffmpeg, plus one ASR engine (WhisperX preferred, whisper.cpp
# works too). demo/merge/report need nothing. Local options never require admin
# rights; brew needs a Homebrew install but not sudo on default setups.

dep_ffmpeg() {
    if have ffmpeg; then say "ffmpeg: already installed ($(command -v ffmpeg))"; return; fi
    say ""
    say "ffmpeg is required by 'testimony transcribe' (audio conversion)."
    if have brew; then
        c=$(choose "Install ffmpeg via" "brew" "local")
    else
        c=$(choose "Install ffmpeg (no Homebrew found)" "local" "local")
    fi
    case "$c" in
        brew) brew install ffmpeg ;;
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
            say "Fetching static ffmpeg build (evermeet.cx) ..."
            fetch "https://evermeet.cx/ffmpeg/info/ffmpeg/release" "$tmp2/info.json"
            u=$(sed -n 's/.*"zip":{"url":"\([^"]*\)".*/\1/p' "$tmp2/info.json" | head -1)
            [ -n "$u" ] || { err "could not parse evermeet.cx response; skipping ffmpeg"; rm -rf "$tmp2"; return; }
            fetch "$u" "$tmp2/ffmpeg.zip"
            if have gpg; then
                fetch "$u.sig" "$tmp2/ffmpeg.zip.sig"
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
            (cd "$tmp2" && unzip -q ffmpeg.zip)
            install -m 0755 "$tmp2/ffmpeg" "$INSTALL_DIR/ffmpeg"
            ;;
        linux)
            arch=$(uname -m)
            case "$arch" in x86_64) ja=amd64 ;; aarch64|arm64) ja=arm64 ;; *) err "no static ffmpeg for $arch"; rm -rf "$tmp2"; return ;; esac
            say "Fetching static ffmpeg build (johnvansickle.com) ..."
            fetch "https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-${ja}-static.tar.xz" "$tmp2/ffmpeg.tar.xz"
            fetch "https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-${ja}-static.tar.xz.md5" "$tmp2/ffmpeg.md5" || true
            if [ -s "$tmp2/ffmpeg.md5" ] && have md5sum; then
                (cd "$tmp2" && sed 's| .*ffmpeg-release.*| ffmpeg.tar.xz|' ffmpeg.md5 | md5sum -c -) \
                    || { err "ffmpeg md5 mismatch; skipping"; rm -rf "$tmp2"; return; }
                say "ffmpeg md5 verified (upstream publishes md5 only)."
            else
                say "WARNING: could not verify the static ffmpeg build; installing unverified."
            fi
            tar -xJf "$tmp2/ffmpeg.tar.xz" -C "$tmp2"
            install -m 0755 "$tmp2"/ffmpeg-*-static/ffmpeg "$INSTALL_DIR/ffmpeg"
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
                    fetch "https://astral.sh/uv/install.sh" "$uvd/uv-install.sh"
                    sh "$uvd/uv-install.sh"
                    rm -rf "$uvd"
                    # uv lands in ~/.local/bin; make it visible to this run.
                    PATH="$HOME/.local/bin:$PATH"; export PATH
                else
                    say "Skipped. Later: uv tool install whisperx   (or: pipx install whisperx)"
                    return
                fi
            fi
            uv tool install whisperx
            say "whisperx installed (user-local). First run downloads its models."
            ;;
        whisper.cpp)
            brew install whisper-cpp
            say ""
            say "whisper.cpp needs a ggml model. Download once (~1.5 GB), user-local:"
            say "  mkdir -p ~/.local/share/testimony/models && curl -fL -o ~/.local/share/testimony/models/ggml-large-v3-turbo.bin \\"
            say "    https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-turbo.bin"
            say "Then: testimony transcribe -engine whispercpp -model ~/.local/share/testimony/models/ggml-large-v3-turbo.bin ..."
            ;;
        skip)
            say "Skipped. Later: uv tool install whisperx   or   brew install whisper-cpp" ;;
    esac
}

usage() { sed -n '2,20p' "$0" 2>/dev/null || say "see script header"; }

main() {
    while [ $# -gt 0 ]; do
        case "$1" in
            -d|--dir)  INSTALL_DIR="$2"; shift 2 ;;
            -y|--yes)  ASSUME_YES=1; shift ;;
            --no-deps) NO_DEPS=1; shift ;;
            --version) VERSION="$2"; shift 2 ;;
            -h|--help) usage; exit 0 ;;
            *) die "unknown flag: $1 (try --help)" ;;
        esac
    done

    say "testimony installer — release $VERSION, install dir $INSTALL_DIR"
    install_binary

    if [ "$NO_DEPS" = 1 ]; then
        say ""
        say "Dependencies skipped (--no-deps). 'testimony transcribe' needs ffmpeg + whisperx or whisper.cpp."
        exit 0
    fi

    dep_ffmpeg
    dep_asr

    say ""
    say "Done. Try:  testimony demo    (capture a session; speak while you click)"
}

main "$@"
