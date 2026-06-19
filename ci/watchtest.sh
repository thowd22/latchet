#!/bin/sh
# End-to-end test for `latchet watch` against a local bare repo + podman.
# Run on the VM: sh watchtest.sh
set -u
export PATH="$HOME/.local/bin:$PATH"
export LATCHET_RUNTIME=podman
LATCHET="${LATCHET:-$HOME/latchet}"
W="$(mktemp -d)"
BARE="$W/repo.git"; WORK="$W/work"
PASS=0; FAIL=0
ok()  { PASS=$((PASS+1)); printf 'PASS  %s\n' "$1"; }
bad() { FAIL=$((FAIL+1)); printf 'FAIL  %s\n' "$1"; }

git init -q -b main --bare "$BARE"
git clone -q "$BARE" "$WORK"
git -C "$WORK" config user.email t@t.t; git -C "$WORK" config user.name test
cat > "$WORK/latchet.yml" <<'YML'
name: watched
jobs:
  hello:
    container: alpine:3.19
    steps:
      - run: echo "WATCH_RAN sha=$LATCHET_GIT_SHA"
YML
git -C "$WORK" add -A; git -C "$WORK" commit -qm init; git -C "$WORK" push -q origin main

cat > "$W/latchet-ci.yml" <<YML
watch:
  - url: file://$BARE
    branches: [main]
    tags: ["v*"]
YML

run_watch() {
  LATCHET_CONFIG="$W/latchet-ci.yml" LATCHET_WATCH_STATE="$W/state.json" \
    LATCHET_LOG_DIR="$W/logs" "$LATCHET" watch -max-parallel 1 2>&1
}

echo "=== pass 1: baseline (no fire) ==="
O="$(run_watch)"
echo "$O" | grep -q "0 run(s) fired" && ok "baseline fires nothing" || { bad "baseline fires nothing"; echo "$O"; }
echo "$O" | grep -q "WATCH_RAN" && bad "baseline must not run a container" || ok "baseline runs no container"

echo "=== pass 2: new commit on main -> fire ==="
echo change >> "$WORK/file.txt"; git -C "$WORK" add -A; git -C "$WORK" commit -qm change; git -C "$WORK" push -q origin main
O="$(run_watch)"
echo "$O" | grep -q "refs/heads/main .* running latchet.yml" && ok "branch advance fires" || { bad "branch advance fires"; echo "$O"; }
echo "$O" | grep -q "WATCH_RAN" && ok "fired run executed in a container" || { bad "fired run executed in a container"; echo "$O"; }
echo "$O" | grep -q "1 run(s) fired, 0 failed" && ok "exactly one run fired" || { bad "exactly one run fired"; echo "$O"; }

echo "=== pass 3: no change -> no fire ==="
O="$(run_watch)"
echo "$O" | grep -q "0 run(s) fired" && ok "no re-fire on unchanged ref" || { bad "no re-fire on unchanged ref"; echo "$O"; }

echo "=== pass 4: new tag v1.0.0 -> fire ==="
git -C "$WORK" tag v1.0.0; git -C "$WORK" push -q origin v1.0.0
O="$(run_watch)"
echo "$O" | grep -q "refs/tags/v1.0.0 .* running latchet.yml" && ok "new tag fires" || { bad "new tag fires"; echo "$O"; }
echo "$O" | grep -q "WATCH_RAN" && ok "tag run executed in a container" || { bad "tag run executed in a container"; echo "$O"; }

echo
echo "=== watch result: $PASS passed, $FAIL failed ==="
rm -rf "$W"
[ "$FAIL" = 0 ]
