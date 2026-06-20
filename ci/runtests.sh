#!/bin/sh
# Comprehensive feature test for latchet. Run on the VM:
#   LATCHET_RUNTIME=podman sh runtests.sh
# Assumes ~/latchet (binary), ~/latchet-src (a git checkout on a branch),
# and ci/features.yml present in CWD.
set -u
LATCHET="${LATCHET:-$HOME/latchet}"
export LATCHET_RUNTIME="${LATCHET_RUNTIME:-podman}"
export PATH="$HOME/.local/bin:$PATH"   # so latchet can find cosign
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
[ ! -f "$PROV.bundle" ] && ok "unsigned by default (no LATCHET_COSIGN_KEY)" || bad "unsigned by default (no LATCHET_COSIGN_KEY)"

echo "===== PROVENANCE SIGNING (cosign) ====="
if command -v cosign >/dev/null 2>&1; then
  KEYDIR="$TMP/keys"; mkdir -p "$KEYDIR"
  ( cd "$KEYDIR" && COSIGN_PASSWORD="" cosign generate-key-pair >/dev/null 2>&1 )
  SLOGS="$TMP/logs-sign"
  COSIGN_PASSWORD="" LATCHET_COSIGN_KEY="$KEYDIR/cosign.key" LATCHET_LOG_DIR="$SLOGS" \
    "$LATCHET" -file ci/features.yml >"$TMP/signrun" 2>&1
  SPROV="$SLOGS/latest/provenance.json"
  [ -f "$SPROV.bundle" ] && ok "provenance.json.bundle written" || { bad "provenance.json.bundle written"; sed 's/^/      /' "$TMP/signrun"; }
  grep -qF "provenance signed" "$TMP/signrun" && ok "signing logged" || bad "signing logged"
  if cosign verify-blob --key "$KEYDIR/cosign.pub" --bundle "$SPROV.bundle" --insecure-ignore-tlog=true "$SPROV" >/dev/null 2>&1; then
    ok "cosign verify-blob succeeds"
  else
    bad "cosign verify-blob succeeds"
  fi
  printf 'tampered\n' >> "$SPROV"   # corrupt the signed file
  if cosign verify-blob --key "$KEYDIR/cosign.pub" --bundle "$SPROV.bundle" --insecure-ignore-tlog=true "$SPROV" >/dev/null 2>&1; then
    bad "tamper detected (verify must fail on modified file)"
  else
    ok "tamper detected (verify fails on modified file)"
  fi
else
  echo "SKIP  cosign not installed (signing checks skipped)"
fi

echo "===== VERIFY (re-derive & compare) ====="
VLOGS="$TMP/logs-verifyrun"
LATCHET_LOG_DIR="$VLOGS" "$LATCHET" -file ci/verify-demo.yml >/dev/null 2>&1
VPROV="$VLOGS/latest/provenance.json"
[ -f "$VPROV" ] && ok "verify: demo run produced provenance" || bad "verify: demo run produced provenance"

# Faithful re-run: both lax and strict verify.
ec "verify lax (faithful) -> 0"    0 env LATCHET_LOG_DIR="$TMP/v1" "$LATCHET" verify --file ci/verify-demo.yml "$VPROV"
ec "verify strict (faithful) -> 0" 0 env LATCHET_LOG_DIR="$TMP/v2" "$LATCHET" verify --strict --file ci/verify-demo.yml "$VPROV"
has "verify reports VERIFIED" "$TMP/out" "VERIFIED"
[ -f "$TMP/v1/latest/verification.json" ] && ok "verification.json written" || bad "verification.json written"

# Tampered subject digest: strict fails, lax tolerates content drift.
ZERO=0000000000000000000000000000000000000000000000000000000000000000
sed "0,/\"sha256\": \"[0-9a-f]\{64\}\"/s//\"sha256\": \"$ZERO\"/" "$VPROV" > "$TMP/prov-baddigest.json"
ec "verify strict (tampered digest) -> 1" 1 env LATCHET_LOG_DIR="$TMP/v3" "$LATCHET" verify --strict --file ci/verify-demo.yml "$TMP/prov-baddigest.json"
ec "verify lax (tampered digest) -> 0"    0 env LATCHET_LOG_DIR="$TMP/v4" "$LATCHET" verify --file ci/verify-demo.yml "$TMP/prov-baddigest.json"

# Tampered subject name: lax fails (a claimed artifact is never reproduced).
sed 's#gen/artifact.txt#gen/ghost.txt#' "$VPROV" > "$TMP/prov-badname.json"
ec "verify lax (missing subject) -> 1" 1 env LATCHET_LOG_DIR="$TMP/v5" "$LATCHET" verify --file ci/verify-demo.yml "$TMP/prov-badname.json"

# Workflow file differs from the manifest's recorded SHA: verification fails.
cp ci/verify-demo.yml "$TMP/wf-mod.yml"; printf '# tampered recipe\n' >> "$TMP/wf-mod.yml"
ec "verify (workflow SHA mismatch) -> 1" 1 env LATCHET_LOG_DIR="$TMP/v6" "$LATCHET" verify --file "$TMP/wf-mod.yml" "$VPROV"

echo "===== GLOBAL CONFIG ====="
GCFG="$TMP/latchet-ci.yml"
cat > "$GCFG" <<YML
runtime: podman
max_parallel: 1
env:
  GLOBAL_DEFAULT: from-config
  PREC: config-level
YML
GCLOGS="$TMP/logs-gc"
# runtime comes from config (LATCHET_RUNTIME unset); global env default injected;
# the workflow's own PREC overrides the config default.
env -u LATCHET_RUNTIME LATCHET_CONFIG="$GCFG" LATCHET_LOG_DIR="$GCLOGS" "$LATCHET" -file ci/gc-demo.yml >/dev/null 2>&1
GA="$GCLOGS/latest/a.log"
[ -f "$GA" ] && ok "runtime from config (ran without LATCHET_RUNTIME)" || bad "runtime from config (ran without LATCHET_RUNTIME)"
has "global default env injected"       "$GA" "GLOBAL_DEFAULT=from-config"
has "workflow env overrides config env" "$GA" "PREC=workflow-level"
printf 'bogus_key: 1\n' > "$TMP/badcfg.yml"
ec "bad global config -> exit 2" 2 env LATCHET_CONFIG="$TMP/badcfg.yml" "$LATCHET" -file ci/gc-demo.yml

echo "===== LOCATION + CONDITIONALS ====="
# Default location is "local" -> else branch runs, prod/staging skipped.
CLLOGS="$TMP/logs-cond-local"
env -u LATCHET_LOCATION LATCHET_LOG_DIR="$CLLOGS" "$LATCHET" -file ci/cond-demo.yml >/dev/null 2>&1
CL="$CLLOGS/latest/deploy.log"
has "default LATCHET_LOCATION is local" "$CL" "LOC=local"
has "local: else branch runs"           "$CL" "BRANCH=none"
grep -qF "BRANCH=prod" "$CL" && bad "local: if branch skipped" || ok "local: if branch skipped"
has "local: skipped step logged"        "$CL" "prod -> skipped (if condition false)"
# location = server -> if branch runs, elif + else skipped.
CSLOGS="$TMP/logs-cond-server"
LATCHET_LOCATION=server LATCHET_LOG_DIR="$CSLOGS" "$LATCHET" -file ci/cond-demo.yml >/dev/null 2>&1
CS="$CSLOGS/latest/deploy.log"
has "server: LATCHET_LOCATION injected"  "$CS" "LOC=server"
has "server: if branch runs (prod)"      "$CS" "BRANCH=prod"
grep -qF "BRANCH=staging" "$CS" && bad "server: elif skipped" || ok "server: elif skipped"
grep -qF "BRANCH=none" "$CS" && bad "server: else skipped" || ok "server: else skipped"

echo "===== JOB CONDITIONALS ====="
env -u LATCHET_LOCATION LATCHET_LOG_DIR="$TMP/jc-local" "$LATCHET" -file ci/jobcond-demo.yml >"$TMP/jc-local.out" 2>&1
has "job if false -> job skipped"        "$TMP/jc-local.out" "deploy -> skipped (if condition false)"
has "skip propagates to dependent"       "$TMP/jc-local.out" "after-deploy -> skipped (deploy skipped)"
has "unconditional job still runs"       "$TMP/jc-local.out" "always               success"
LATCHET_LOCATION=server LATCHET_LOG_DIR="$TMP/jc-server" "$LATCHET" -file ci/jobcond-demo.yml >"$TMP/jc-server.out" 2>&1
has "job if true -> job + dependent run" "$TMP/jc-server.out" "after-deploy         success"

echo "===== STEP OUTPUTS ====="
OLOGS="$TMP/logs-output"
LATCHET_LOG_DIR="$OLOGS" "$LATCHET" -file ci/output-demo.yml >/dev/null 2>&1
OL="$OLOGS/latest/build.log"
has "output passes to a later step"     "$OL" "using VERSION=1.2.3 ARTIFACT=app-abc"
has "output usable in an if: condition" "$OL" "COND_SAW_OUTPUT=yes"
grep -qF "SHOULD_NOT_RUN" "$OL" && bad "false output condition skipped" || ok "false output condition skipped"
grep -qF '"build/.latchet' "$OLOGS/latest/provenance.json" && bad ".latchet kept out of provenance subjects" || ok ".latchet kept out of provenance subjects"
# cross-job: producer outputs: -> consumer needs:
XLOGS="$TMP/logs-crossjob"
LATCHET_LOG_DIR="$XLOGS" "$LATCHET" -file ci/crossjob-demo.yml >/dev/null 2>&1
XC="$XLOGS/latest/consumer.log"
has "declared outputs reach the dependent job" "$XC" "consumer VERSION=2.0.0 ARTIFACT=app-xyz"
has "undeclared value does not cross"          "$XC" "INTERNAL=[]"
grep -qF "not-exported" "$XC" && bad "undeclared output value did not leak" || ok "undeclared output value did not leak"

echo "===== SECRET MASKING ====="
SECRET="s3cr3t-$(echo abcXYZ | tr a-z A-Z)0123456789"   # fixed, distinctive value
SLEN=${#SECRET}
SECLOGS="$TMP/logs-secret"
MY_SECRET="$SECRET" LATCHET_LOG_DIR="$SECLOGS" "$LATCHET" -file ci/secret-demo.yml >/dev/null 2>&1
SL="$SECLOGS/latest/s.log"
has "secret injected into step (PRESENT)"        "$SL" "PRESENT"
has "container saw real value (LEN correct)"     "$SL" "LEN=$SLEN"
has "secret value masked in log (VALUE=***)"     "$SL" "VALUE=***"
grep -qF "$SECRET" "$SL" && bad "raw secret absent from log" || ok "raw secret absent from log"
SPROVF="$SECLOGS/latest/provenance.json"
grep -qF "$SECRET" "$SPROVF" && bad "raw secret absent from provenance" || ok "raw secret absent from provenance"
grep -qF '"MY_SECRET": "***"' "$SPROVF" && ok "secret redacted in provenance (***)" || bad "secret redacted in provenance (***)"

echo "===== DETERMINISM HELPERS ====="
DLOGS="$TMP/logs-det"
LATCHET_LOG_DIR="$DLOGS" "$LATCHET" -file ci/deterministic.yml >/dev/null 2>&1
DL="$DLOGS/latest/det.log"; PL="$DLOGS/latest/plain.log"
grep -qE 'DET SOURCE_DATE_EPOCH=[0-9]+' "$DL" && ok "deterministic: SOURCE_DATE_EPOCH set" || bad "deterministic: SOURCE_DATE_EPOCH set"
has "deterministic: TZ/LC_ALL/LANG set" "$DL" "DET TZ=UTC LC_ALL=C LANG=C"
has "plain job: helpers NOT injected"   "$PL" "PLAIN TZ= LC_ALL= LANG="
FLOGS="$TMP/logs-detforce"
LATCHET_DETERMINISTIC=1 LATCHET_LOG_DIR="$FLOGS" "$LATCHET" -file ci/deterministic.yml >/dev/null 2>&1
has "LATCHET_DETERMINISTIC=1 forces plain job" "$FLOGS/latest/plain.log" "PLAIN TZ=UTC LC_ALL=C LANG=C"

echo "===== VERIFY --key (manifest signature) ====="
if command -v cosign >/dev/null 2>&1; then
  KD="$TMP/vkeys"; mkdir -p "$KD"; ( cd "$KD" && COSIGN_PASSWORD="" cosign generate-key-pair >/dev/null 2>&1 )
  SVLOGS="$TMP/logs-signedrun"
  COSIGN_PASSWORD="" LATCHET_COSIGN_KEY="$KD/cosign.key" LATCHET_LOG_DIR="$SVLOGS" "$LATCHET" -file ci/verify-demo.yml >/dev/null 2>&1
  SVPROV="$SVLOGS/latest/provenance.json"
  [ -f "$SVPROV.bundle" ] && ok "verify --key: signed run produced bundle" || bad "verify --key: signed run produced bundle"
  ec "verify --key (valid sig) -> 0" 0 env LATCHET_LOG_DIR="$TMP/sv1" "$LATCHET" verify --key "$KD/cosign.pub" --file ci/verify-demo.yml "$SVPROV"
  has "verify reports signature verified" "$TMP/out" "signature: verified"
  cp "$SVPROV" "$TMP/svtamper.json"; cp "$SVPROV.bundle" "$TMP/svtamper.json.bundle"; printf ' ' >> "$TMP/svtamper.json"
  ec "verify --key (tampered manifest) -> 1" 1 env LATCHET_LOG_DIR="$TMP/sv2" "$LATCHET" verify --key "$KD/cosign.pub" --file ci/verify-demo.yml "$TMP/svtamper.json"
  has "verify reports signature did not verify" "$TMP/out" "signature did not verify"
else
  echo "SKIP  cosign not installed (verify --key checks skipped)"
fi

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
