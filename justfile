# Go installed with snap lives in /snap/bin.
export PATH := env_var("PATH") + ":/snap/bin"

# List recipes
default:
    @just --list

# Install web dependencies when package.json changed
web-deps:
    cd web && ([ node_modules -nt package.json ] || npm install)

# Build the web client and the kk binary with the client embedded
build: web-deps
    cd web && npm run build
    go build -o kk ./app

# Run Vite and the server in dev mode and open the web client in Chrome
dev: web-deps
    #!/usr/bin/env bash
    set -euo pipefail
    # go:embed needs web/dist to exist even though dev mode serves the client from Vite.
    [ -n "$(ls -A web/dist 2>/dev/null)" ] || { mkdir -p web/dist && touch web/dist/.keep; }
    go build -o kk ./app
    # A server started from an older binary would keep running old code, so stop it first.
    if old=$(./kk server 2>/dev/null | sed -n 's/^pid: //p') && [ -n "$old" ]; then
        echo "stopping the running server (pid $old)"
        kill "$old"
        while kill -0 "$old" 2>/dev/null; do sleep 0.1; done
    fi
    (cd web && exec npx vite --clearScreen false) &
    vite=$!
    log=$(mktemp)
    KK_DEV=1 ./kk > >(tee "$log") 2>&1 &
    server=$!
    trap 'kill $server $vite 2>/dev/null; rm -f "$log"' EXIT
    url=""
    for _ in $(seq 100); do
        url=$(sed -n 's/^web: //p' "$log")
        [ -n "$url" ] && break
        kill -0 "$server" 2>/dev/null || exit 1
        sleep 0.1
    done
    [ -n "$url" ] || { echo "the server did not print its web address"; exit 1; }
    setsid google-chrome "$url" >/dev/null 2>&1 < /dev/null &
    wait -n "$server" "$vite"

# Type-check the web client
typecheck: web-deps
    cd web && npx tsc --noEmit

# Run the test suite
test: build
    go test ./...
