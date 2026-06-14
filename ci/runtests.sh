#!/bin/sh
# Comprehensive feature test for latchet. Run on the VM:
#   LATCHET_RUNTIME=podman sh runtests.sh
# Assumes ~/latchet (binary), ~/latchet-src (a git checkout on a branch),
# and ci/features.yml present in CWD.
set -u
LATCHET="${LATCHET:-$HOME/latchet}"
export LATCHET_RUNTIME="${LATCHET_RUNTIME:-podman}"
TMP="$(mktemp -d)"
PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); printf 'PASS  %s\n' "$1"; }
bad()  { FAIL=$((FAIL+1)); printf 'FAIL  %s\n' "$1"; }
# assert exit code: name expected-code cmd...
ec()   { n="$1"; want="$2"; shift 2; "$@" >"$TMP/out" 2>&1; got=$?; \
         [ "$got" = "$want" ] && ok "$n (exit $got)" || { bad "$n (want $want got $got)"; sed 's/^/      /' "$TMP/out"; }; }
# assert a file contains a string: name file needle
has()  { grep -qF -- "$3" "$2" && ok "$1" || { bad "$1 (missing: $3)"; }; }

echo "===== FLAGS ====="
ec "flag -version"        0 "$LATCHET" -version
ec "flag -help"           0 "$LATCHET" -help
ec "flag -validate-only"  0 "$LATCHET" -file ci/features.yml -validate-only
ec "flag -dry-run"        0 "$LATCHET" -file ci/features.yml -dry-run

echo "===== EXIT 2 (config/parse) ====="
ec "missing file"         2 "$LATCHET" -file "$TMP/nope.yml"
printf 'jobs:\n  x:\n    container: alpine:3.19\n    runs-on: ubuntu\n    steps:\n      - run: echo hi\n' >"$TMP/unknown.yml"
ec "unknown key runs-on"  2 "$LATCHET" -file "$TMP/unknown.yml" -validate-only
printf 'jobs:\n  x:\n    container: alpine:3.19\n    needs: x\n    steps:\n      - run: echo hi\n' >"$TMP/self.yml"
ec "self-needs"           2 "$LATCHET" -file "$TMP/self.yml" -validate-only
printf 'jobs:\n  a:\n    container: alpine:3.19\n    needs: b\n    steps: [{run: echo a}]\n  b:\n    container: alpine:3.19\n    needs: a\n    steps: [{run: echo b}]\n' >"$TMP/cycle.yml"
ec "cycle a<->b"          2 "$LATCHET" -file "$TMP/cycle.yml" -validate-only
printf 'jobs:\n  a:\n    container: alpine:3.19\n    steps: [{run: echo a}]\n  b:\n    container: alpine:3.19\n    inherit: a\n    steps: [{run: echo b}]\n' >"$TMP/inh.yml"
ec "inherit not in needs" 2 "$LATCHET" -file "$TMP/inh.yml" -validate-only
printf 'jobs:\n  a:\n    steps: [{run: echo a}]\n' >"$TMP/nocont.yml"
ec "missing container"    2 "$LATCHET" -file "$TMP/nocont.yml" -validate-only
printf 'jobs:\n  a:\n    container: alpine:3.19\n    steps: []\n' >"$TMP/nostep.yml"
ec "no steps"             2 "$LATCHET" -file "$TMP/nostep.yml" -validate-only
printf 'jobs:\n  a:\n    container: alpine:3.19\n    steps: [{run: ""}]\n' >"$TMP/emptyrun.yml"
ec "empty run"            2 "$LATCHET" -file "$TMP/emptyrun.yml" -validate-only

echo "===== EXIT 1 (job failure + skip propagation) ====="
printf 'jobs:\n  fails:\n    container: alpine:3.19\n    steps: [{run: exit 7}]\n  downstream:\n    container: alpine:3.19\n    needs: fails\n    steps: [{run: echo nope}]\n' >"$TMP/fail.yml"
ec "failing step -> exit 1" 1 "$LATCHET" -file "$TMP/fail.yml"
has "downstream skipped" "$TMP/out" "downstream"
grep -qF "skipped" "$TMP/out" && ok "skip propagation reported" || bad "skip propagation reported"

echo "===== EXIT 3 (infra) ====="
printf 'jobs:\n  a:\n    container: docker.io/library/latchet-no-such-image:nope\n    steps: [{run: echo hi}]\n' >"$TMP/badimg.yml"
ec "bad image -> exit 3"   3 "$LATCHET" -file "$TMP/badimg.yml"
grep -q "Error" "$TMP/out" && ok "pull error surfaces runtime diagnostic" || { bad "pull error surfaces runtime diagnostic"; sed 's/^/      /' "$TMP/out"; }

echo "===== FEATURE RUN (branch checkout) ====="
RUNLOGS="$TMP/logs-branch"
( cd "$HOME/latchet-src" && LATCHET_LOG_DIR="$RUNLOGS" "$LATCHET" -file "$OLDPWD/ci/features.yml" ) >"$TMP/frun" 2>&1
[ $? = 0 ] && ok "feature run exit 0" || { bad "feature run exit 0"; sed 's/^/      /' "$TMP/frun"; }
AL="$RUNLOGS/latest/a.log"; BL="$RUNLOGS/latest/b.log"
has "step default name 'step 1'" "$AL" "step 1"
has "env precedence: step wins"  "$AL" "PREC_STEP=step"
has "env precedence: job wins"   "$AL" "PREC_JOB=job"
has "workflow-only env"          "$AL" "ONLY_WF=wf-only"
has "job-only env"               "$AL" "ONLY_JOB=job-only"
has "builtin override via env"   "$AL" "OVERRIDE=FAKED"
has "LATCHET_WORKSPACE"          "$AL" "WS=/workspace"
has "WS assert (pwd=/workspace)" "$AL" "WS_ASSERT ok"
has "LATCHET_JOB_ID"             "$AL" "JOB=a"
has "LATCHET_GIT_URL"            "$AL" "URL=https://github.com/thowd22/latchet"
has "LATCHET_GIT_BRANCH=main"    "$AL" "BRANCH=main"
has "LATCHET_GIT_TAG empty"      "$AL" "TAG="
has "LATCHET_GIT_REF=branch"     "$AL" "REF=refs/heads/main"
grep -qE 'SHA=[0-9a-f]{40}' "$AL" && ok "LATCHET_GIT_SHA 40-hex" || bad "LATCHET_GIT_SHA 40-hex"
RID="$(readlink "$RUNLOGS/latest")"
has "LATCHET_RUN_ID matches dir" "$AL" "RUN=$RID"
has "scalar needs ran b"         "$BL" "SCALAR_NEEDS ok"

echo "===== PROVENANCE (SLSA) ====="
PROV="$RUNLOGS/latest/provenance.json"
[ -f "$PROV" ] && ok "provenance.json written" || bad "provenance.json written"
has "predicateType slsa v1"      "$PROV" "https://slsa.dev/provenance/v1"
has "in-toto statement type"     "$PROV" "https://in-toto.io/Statement/v1"
has "invocationId == run id"     "$PROV" "$RID"
has "resolved image digest pinned" "$PROV" "@sha256:"
has "builder id stamped"         "$PROV" "latchet.dev/builders/latchet@"
grep -qE '"sha256": "[0-9a-f]{64}"' "$PROV" && ok "subject sha256 present" || bad "subject sha256 present"

echo "===== FEATURE RUN (tag checkout: GIT_TAG / GIT_REF) ====="
git -C "$HOME/latchet-src" fetch -q --tags 2>/dev/null
git -C "$HOME/latchet-src" -c advice.detachedHead=false checkout -q v0.4.0
TAGLOGS="$TMP/logs-tag"
( cd "$HOME/latchet-src" && LATCHET_LOG_DIR="$TAGLOGS" "$LATCHET" -file "$OLDPWD/ci/features.yml" ) >/dev/null 2>&1
TA="$TAGLOGS/latest/a.log"
has "tag: GIT_TAG=v0.4.0"   "$TA" "TAG=v0.4.0"
has "tag: GIT_REF=tags ref" "$TA" "REF=refs/tags/v0.4.0"
has "tag: GIT_BRANCH empty"  "$TA" "BRANCH="
git -C "$HOME/latchet-src" checkout -q main

echo "===== INPUT ENV VARS ====="
# LATCHET_WORKSPACE_ROOT + LATCHET_KEEP_WORKSPACE
WSROOT="$TMP/wsroot"
LATCHET_WORKSPACE_ROOT="$WSROOT" LATCHET_KEEP_WORKSPACE=1 LATCHET_LOG_DIR="$TMP/l1" \
  "$LATCHET" -file ci/features.yml >/dev/null 2>&1
[ -d "$WSROOT" ] && [ -n "$(ls -A "$WSROOT" 2>/dev/null)" ] && ok "LATCHET_WORKSPACE_ROOT + KEEP_WORKSPACE (kept on success)" || bad "LATCHET_WORKSPACE_ROOT/KEEP_WORKSPACE"
# LATCHET_LOG_DIR
LD="$TMP/customlogs"
LATCHET_LOG_DIR="$LD" "$LATCHET" -file ci/features.yml >/dev/null 2>&1
[ -e "$LD/latest" ] && ok "LATCHET_LOG_DIR honored" || bad "LATCHET_LOG_DIR honored"
# XDG_STATE_HOME
XS="$TMP/xdg"
env -u LATCHET_LOG_DIR XDG_STATE_HOME="$XS" "$LATCHET" -file ci/features.yml >/dev/null 2>&1
[ -e "$XS/latchet/latest" ] && ok "XDG_STATE_HOME honored" || bad "XDG_STATE_HOME honored"

echo "===== -max-parallel 1 (live streaming to stdout) ====="
LATCHET_LOG_DIR="$TMP/l2" "$LATCHET" -file ci/features.yml -max-parallel 1 >"$TMP/serial" 2>&1
has "serial mode streams step output to stdout" "$TMP/serial" "WS=/workspace"

echo
echo "===== RESULT: $PASS passed, $FAIL failed ====="
rm -rf "$TMP"
[ "$FAIL" = 0 ]
