# WebRTC Port Isolation — Track C Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `play.py --livestream 2` (and `train.py`) render correctly in the browser viewer at `http://10.61.3.125/viewer/` with `hostNetwork=false` and pod-0/-1 coexisting on `ws-node074`.

**Architecture:** Constrain Kit WebRTC to bind UDP in a known window per pod (pod-0: 30998–31097, pod-1: 31098–31197), then expose that exact window as `hostPort` mappings so the host-candidate ICE entries Kit advertises are actually reachable from the browser. No Isaac Lab / Kit native source changes.

**Tech Stack:** Kubernetes (k0s), Isaac Sim 6.0 Kit livestream extensions, HAMi vGPU, `isaac-launchable` deployment manifests, coturn (unused in this plan, belongs to fallback Track D).

**Spec:** `docs/superpowers/specs/2026-04-24-webrtc-port-isolation-design.md`

**Pre-requisite decision gate (Phase 1):** If Task 3 shows the Kit portRange setting does not exist or does not constrain bind ports, STOP this plan and create a new plan for Track D (TURN relay). Do not proceed to Phase 2.

---

## File Structure

**Will modify:**
- `k8s/isaac-sim/deployment-0.yaml` — add 100× hostPort entries (30998–31097/UDP), env vars `ISAACSIM_WEBRTC_PORT_MIN/MAX`
- `k8s/isaac-sim/deployment-1.yaml` — add 100× hostPort entries (31098–31197/UDP), env vars, switch signal hostPort to 49101
- `k8s/base/configmaps.yaml` — extend `runheadless-script-0` and `runheadless-script-1` to forward the portRange env vars as Kit flags
- `k8s/base/services.yaml` — either widen NodePort Services to the same 100-port window or delete the `isaac-launchable-{0,1}-media` NodePort Services (hostPort makes them redundant)

**Will create:**
- `scripts/gen-webrtc-ports.sh` — small generator producing 100 hostPort YAML fragments for a given `base` (called once per manifest to avoid hand-editing 100 lines)
- `docs/superpowers/plans/notes/2026-04-24-probe-results.md` — log of Phase 1 probe results (Kit portRange key, observed bind range, max concurrent UDP count)

**Will read (no edit):**
- `/isaac-sim/extscache/omni.kit.livestream.webrtc-10.1.2*/config/extension.toml` (inside pod)
- `/isaac-sim/extscache/omni.kit.livestream.webrtc-10.1.2*/docs/` (inside pod)
- `/isaac-sim/apps/isaacsim.exp.full.streaming.kit` (inside pod, reference `[settings.exts."omni.kit.livestream.app"]`)

---

## Phase 1 — Probe

### Task 1: Find the Kit WebRTC portRange setting key

**Files:**
- Read (in pod): `/isaac-sim/extscache/omni.kit.livestream.webrtc-10.1.2+110.0.0.lx64.r.cp312/config/extension.toml`
- Read (in pod): `/isaac-sim/extscache/omni.kit.livestream.app-10.1.0+110.0.0.lx64.r.cp312/config/extension.toml`
- Read (in pod, via `strings`): the native `.plugin.so` for both extensions
- Write (locally): `docs/superpowers/plans/notes/2026-04-24-probe-results.md`

- [ ] **Step 1: Open a shell in the running pod-0 vscode container**

```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o name" | head -1 | cut -d/ -f2)
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- bash -c 'ls /isaac-sim/extscache/ | grep livestream'"
```

Expected output includes `omni.kit.livestream.app-10.1.0+…` and `omni.kit.livestream.webrtc-10.1.2+…`.

- [ ] **Step 2: Dump the two extension.toml files**

```bash
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- bash -c '
  cat /isaac-sim/extscache/omni.kit.livestream.webrtc-10.1.2+110.0.0.lx64.r.cp312/config/extension.toml
  echo === APP ===
  cat /isaac-sim/extscache/omni.kit.livestream.app-10.1.0+110.0.0.lx64.r.cp312/config/extension.toml
'"
```

Scan the output for any setting name that contains `port`, `range`, `min`, `max`, `bind`, `ephemeral`.

- [ ] **Step 3: Scan the native plugins with `strings` for port-range setting keys**

```bash
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- bash -c '
  for f in /isaac-sim/extscache/omni.kit.livestream.*/bin/*.plugin.so; do
    echo === \$f ===
    strings -n 10 \$f | grep -iE \"port.{0,6}(range|min|max)|udp.{0,4}(port|bind)|primaryStream\" | head -30
  done
'"
```

Expected: one or more candidate setting keys like `/exts/omni.kit.livestream.webrtc/portRange/min`, `/exts/omni.kit.livestream.app/primaryStream/portRangeMin`, or similar. Record every plausible candidate.

- [ ] **Step 4: Record candidates locally**

```bash
mkdir -p /Users/xiilab/git/isaac-launchable/docs/superpowers/plans/notes
cat > /Users/xiilab/git/isaac-launchable/docs/superpowers/plans/notes/2026-04-24-probe-results.md <<'EOF'
# Probe results (2026-04-24)

## Task 1: candidate port-range setting keys
- <key-1>
- <key-2>
- ...

## Task 3 outcome
(fill after Task 3)
EOF
```

Fill in every candidate key from Step 2/3 output under "candidate port-range setting keys". Do not guess — only include strings that actually appeared.

- [ ] **Step 5: Commit the note**

```bash
cd /Users/xiilab/git/isaac-launchable
git add docs/superpowers/plans/notes/2026-04-24-probe-results.md
git commit -m "docs: record Kit livestream setting key probe"
```

---

### Task 2: Prepare the probe environment (temporary hostNetwork rollout)

**Files:**
- Modify (live cluster only, NOT committed): `deployment/isaac-launchable-0` via `kubectl patch`
- Scale: `deployment/isaac-launchable-1` to 0

- [ ] **Step 1: Scale pod-1 to 0 so hostPort does not conflict**

```bash
ssh root@10.61.3.75 'k0s kubectl scale deployment/isaac-launchable-1 -n isaac-launchable --replicas=0'
```

Expected: `deployment.apps/isaac-launchable-1 scaled`

- [ ] **Step 2: Patch pod-0 to hostNetwork=true and remove sidecar container ports**

```bash
ssh root@10.61.3.75 '
k0s kubectl patch deployment/isaac-launchable-0 -n isaac-launchable --type=strategic \
  --patch "{\"spec\":{\"template\":{\"spec\":{\"hostNetwork\":true}}}}"
k0s kubectl patch deployment/isaac-launchable-0 -n isaac-launchable --type=json -p="[
  {\"op\":\"remove\",\"path\":\"/spec/template/spec/containers/1/ports\"},
  {\"op\":\"remove\",\"path\":\"/spec/template/spec/containers/2/ports\"}
]"
'
```

Expected: two `deployment.apps/isaac-launchable-0 patched` messages. (Sidecar port removal is required because hostNetwork=true promotes `containerPort` to a host-namespace bind; 80 conflicts with nginx-ingress-controller and 5173 may conflict elsewhere.)

- [ ] **Step 3: Wait for pod-0 rollout to complete (vscode container is enough)**

```bash
ssh root@10.61.3.75 '
  until k0s kubectl get pod -n isaac-launchable -l instance=pod-0 --no-headers 2>/dev/null \
        | awk "{print \$2}" | grep -qE "^(1|2|3)/3"; do sleep 3; done
  k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o wide
'
```

Expected: pod Running, `IP` column shows `10.61.3.74` (node IP, not a `10.244.x.x` pod IP). nginx/web-viewer may be CrashLoopBackOff — that is expected and does not affect the probe.

- [ ] **Step 4: Commit nothing — this patch is intentionally not stored in git**

(The probe environment is disposable and will be reverted in Task 4.)

---

### Task 3: Measure whether the portRange key constrains Kit UDP binds

**Files:**
- Run (in pod): `play.py --livestream 2 --kit_args ...`
- Observe (in pod): `ss -ulnp`
- Append to local: `docs/superpowers/plans/notes/2026-04-24-probe-results.md`

- [ ] **Step 1: Capture the baseline UDP set (before Kit launches)**

```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o name" | head -1 | cut -d/ -f2)
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- ss -uln" \
  | awk 'NR>1 {print $5}' | sort -u > /tmp/udp_before.txt
wc -l /tmp/udp_before.txt
```

Expected: a non-empty list of UDP listen endpoints (DNS 53, coturn 3478, etc.).

- [ ] **Step 2: Launch play.py with the portRange flag candidate from Task 1**

Replace `<key>` with the most specific candidate you recorded (e.g. `/exts/omni.kit.livestream.webrtc/portRange/min`).

```bash
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -i -- bash -s" <<'REMOTE'
cd /workspace/isaaclab
nohup ./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/play.py \
  --task Isaac-Ant-v0 --num_envs 4 --livestream 2 \
  --kit_args "--/exts/omni.kit.livestream.app/primaryStream/publicIp=10.61.3.74 \
              --/exts/omni.kit.livestream.app/primaryStream/signalPort=49100 \
              --/exts/omni.kit.livestream.app/primaryStream/streamPort=30998 \
              --/<key>/min=30998 \
              --/<key>/max=31097 \
              --merge-config=/isaac-sim/config/open_endpoint.toml" \
  >/tmp/probe_play.log 2>&1 &
echo PID=$!
REMOTE
```

Expected: a PID is printed. Do not wait here; move to Step 3.

- [ ] **Step 3: Wait for Simulation App Startup Complete**

```bash
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- bash -c '
  until grep -q \"Simulation App Startup Complete\" /tmp/probe_play.log; do sleep 3; done
  echo OK
'"
```

Expected: `OK` printed after ~15–30 seconds.

- [ ] **Step 4: Snapshot UDP listeners after Kit is up**

```bash
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- ss -uln" \
  | awk 'NR>1 {print $5}' | sort -u > /tmp/udp_after.txt
comm -13 /tmp/udp_before.txt /tmp/udp_after.txt > /tmp/udp_new.txt
cat /tmp/udp_new.txt
```

Expected: a short list of UDP endpoints that appeared after Kit started. These are the ports that must be in the 30998–31097 window for the probe to be a success.

- [ ] **Step 5: Judge the probe result and record**

Classify the new ports:
- **PASS:** every IPv4 entry is within `0.0.0.0:30998`–`0.0.0.0:31097` (IPv6 noise on `::` can be ignored if the IPv4 set is constrained).
- **FAIL:** any IPv4 entry outside the window, or no new entries at all (Kit didn't start streaming).

Append to notes:

```bash
cat >> /Users/xiilab/git/isaac-launchable/docs/superpowers/plans/notes/2026-04-24-probe-results.md <<'EOF'

## Task 3 outcome
- portRange candidate key used: /<key>/min, /<key>/max
- ports newly bound by Kit (IPv4 only):
  $(cat /tmp/udp_new.txt | grep -E "^0\.0\.0\.0" )
- verdict: PASS | FAIL
EOF
```

- [ ] **Step 6: Commit notes**

```bash
cd /Users/xiilab/git/isaac-launchable
git add docs/superpowers/plans/notes/2026-04-24-probe-results.md
git commit -m "docs: record Task 3 probe outcome"
```

- [ ] **Step 7: Decision gate**

If verdict is **FAIL**:
1. Kill the probe play.py: `ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- bash -c 'pkill -TERM -f isaaclab.sh'"`
2. Run Task 4 (revert)
3. STOP this plan. Inform the user "Track C is not viable; Track D (TURN relay) plan needed" — do not proceed to Task 5.

If verdict is **PASS**, proceed to Task 4 to clean up the probe environment, then Phase 2.

---

### Task 4: Revert the probe environment

**Files:**
- `k8s/isaac-sim/deployment-0.yaml` (re-apply from git)
- `deployment/isaac-launchable-1` (scale back to 1)

- [ ] **Step 1: Kill the probe play.py**

```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o name" | head -1 | cut -d/ -f2)
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -- bash -c '
  pgrep -f python.sh | xargs -r kill -TERM 2>/dev/null
  sleep 3
  pgrep -af python.sh | head
'"
```

Expected: no output after the echo — all python.sh processes gone.

- [ ] **Step 2: Re-apply the original deployment-0 manifest**

```bash
ssh root@10.61.3.75 "k0s kubectl apply -f -" \
  < /Users/xiilab/git/isaac-launchable/k8s/isaac-sim/deployment-0.yaml
```

Expected: `deployment.apps/isaac-launchable-0 configured`

- [ ] **Step 3: Scale pod-1 back to 1**

```bash
ssh root@10.61.3.75 'k0s kubectl scale deployment/isaac-launchable-1 -n isaac-launchable --replicas=1'
```

Expected: `deployment.apps/isaac-launchable-1 scaled`

- [ ] **Step 4: Wait for both pods to be Ready**

```bash
ssh root@10.61.3.75 '
  until k0s kubectl get pod -n isaac-launchable -l app=isaac-launchable --no-headers 2>/dev/null \
        | awk "{print \$2}" | grep -cE "^3/3" | grep -q 2; do sleep 5; done
  k0s kubectl get pod -n isaac-launchable -l app=isaac-launchable
'
```

Expected: two pods, both `3/3 Running`.

- [ ] **Step 5: Commit nothing** — no git state changed by this task.

---

## Phase 2 — Track C Implementation

**Pre-condition:** Task 3 verdict was **PASS**. `docs/superpowers/plans/notes/2026-04-24-probe-results.md` contains the exact Kit portRange setting key (hereafter `<portRangeKey>`).

### Task 5: Create the hostPort generator helper script

**Files:**
- Create: `scripts/gen-webrtc-ports.sh`

- [ ] **Step 1: Write the generator script**

```bash
cat > /Users/xiilab/git/isaac-launchable/scripts/gen-webrtc-ports.sh <<'EOF'
#!/bin/bash
# Emit 100 hostPort/containerPort YAML fragments for a contiguous UDP range.
# Usage: ./scripts/gen-webrtc-ports.sh <base-port>
# Example: ./scripts/gen-webrtc-ports.sh 30998
set -e
base="${1:?base port required}"
count="${2:-100}"
for i in $(seq 0 $((count - 1))); do
  p=$((base + i))
  cat <<YAML
        - name: wrtc-$p
          containerPort: $p
          hostPort: $p
          protocol: UDP
YAML
done
EOF
chmod +x /Users/xiilab/git/isaac-launchable/scripts/gen-webrtc-ports.sh
```

- [ ] **Step 2: Smoke-test the generator**

```bash
/Users/xiilab/git/isaac-launchable/scripts/gen-webrtc-ports.sh 30998 3
```

Expected output (exact):

```
        - name: wrtc-30998
          containerPort: 30998
          hostPort: 30998
          protocol: UDP
        - name: wrtc-30999
          containerPort: 30999
          hostPort: 30999
          protocol: UDP
        - name: wrtc-31000
          containerPort: 31000
          hostPort: 31000
          protocol: UDP
```

- [ ] **Step 3: Commit the helper**

```bash
cd /Users/xiilab/git/isaac-launchable
git add scripts/gen-webrtc-ports.sh
git commit -m "feat(scripts): add gen-webrtc-ports helper"
```

---

### Task 6: Extend `deployment-0.yaml` with the 100-port hostPort window and portRange env

**Files:**
- Modify: `k8s/isaac-sim/deployment-0.yaml`

- [ ] **Step 1: Add env vars (next to `ISAACSIM_STREAM_PORT`)**

In `k8s/isaac-sim/deployment-0.yaml`, inside `spec.template.spec.containers[0].env` (the `vscode` container), **after** the existing `ISAACSIM_SIGNAL_PORT` block, insert:

```yaml
        - name: ISAACSIM_WEBRTC_PORT_MIN
          value: "30998"
        - name: ISAACSIM_WEBRTC_PORT_MAX
          value: "31097"
```

- [ ] **Step 2: Replace the single `webrtc-media` port entry with a 100-entry window**

Locate the block:

```yaml
        - name: webrtc-media
          containerPort: 30998
          hostPort: 30998
          protocol: UDP
```

Remove it. In its place, run the generator and paste the output:

```bash
cd /Users/xiilab/git/isaac-launchable
./scripts/gen-webrtc-ports.sh 30998 100
```

Paste those 400 lines (100 × 4-line blocks) exactly where the single `webrtc-media` entry used to be, keeping the preceding `- name: vscode ... containerPort: 8080` and the following `- name: webrtc-signal ... containerPort: 49100 ... hostPort: 49100 ... protocol: TCP` intact.

- [ ] **Step 3: Verify the YAML is syntactically valid**

```bash
cd /Users/xiilab/git/isaac-launchable
python3 -c "import yaml,sys; list(yaml.safe_load_all(open('k8s/isaac-sim/deployment-0.yaml'))); print('OK')"
```

Expected: `OK`

- [ ] **Step 4: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add k8s/isaac-sim/deployment-0.yaml
git commit -m "feat(k8s): pod-0 expose UDP 30998-31097 window for Kit WebRTC

Kit WebRTC binds multiple ephemeral UDP ports per livestream session.
Single hostPort 30998/UDP was insufficient, trapping the other ports
inside the pod network namespace and breaking video track negotiation.

Pair with ISAACSIM_WEBRTC_PORT_MIN/MAX so runheadless.sh forwards the
window to Kit via --kit_args."
```

---

### Task 7: Extend `deployment-1.yaml` analogously, with a disjoint window and distinct signal hostPort

**Files:**
- Modify: `k8s/isaac-sim/deployment-1.yaml`

- [ ] **Step 1: Update env block**

In `spec.template.spec.containers[0].env` of pod-1, change `ISAACSIM_STREAM_PORT` to `31098` and add the window env vars:

```yaml
        - name: ISAACSIM_STREAM_PORT
          value: "31098"
        - name: ISAACSIM_SIGNAL_PORT
          value: "49101"
        - name: ISAACSIM_WEBRTC_PORT_MIN
          value: "31098"
        - name: ISAACSIM_WEBRTC_PORT_MAX
          value: "31197"
```

- [ ] **Step 2: Replace the single `webrtc-media` port (currently containerPort only) with the 100-entry window + TCP hostPort for signal**

Remove:

```yaml
        - name: webrtc-media
          containerPort: 30999
          protocol: UDP
        - name: webrtc-signal
          containerPort: 49100
          protocol: TCP
```

Insert instead:

```bash
./scripts/gen-webrtc-ports.sh 31098 100
```

Paste output, followed by:

```yaml
        - name: webrtc-signal
          containerPort: 49101
          hostPort: 49101
          protocol: TCP
```

(pod-1 now also uses hostPort for signal, distinct from pod-0's 49100.)

- [ ] **Step 3: Validate YAML**

```bash
python3 -c "import yaml,sys; list(yaml.safe_load_all(open('k8s/isaac-sim/deployment-1.yaml'))); print('OK')"
```

- [ ] **Step 4: Update `nginx-config-1` ConfigMap to proxy signal to 49101**

Pod-1's nginx sidecar fronts signal traffic on container port 80 and proxies internally to the Kit signal TCP port. Because we moved pod-1's signal from 49100 to 49101, the sidecar proxy target must change too.

Read the existing ConfigMap:

```bash
grep -n "49100\|signal\|proxy_pass" /Users/xiilab/git/isaac-launchable/k8s/base/configmaps.yaml | head -40
```

Locate the `nginx-config-1` ConfigMap block (it will contain an `nginx.conf` key with one or more `proxy_pass` directives pointing at `127.0.0.1:49100` or `localhost:49100`). Replace every `49100` inside the `nginx-config-1` block (only that block — leave `nginx-config-0` untouched) with `49101`.

If the ConfigMap contains a helper env or server-name line with "49100", update it too, but **do not** touch references that belong to pod-0's `nginx-config-0`.

- [ ] **Step 5: Validate YAML again after editing**

```bash
python3 -c "import yaml,sys; list(yaml.safe_load_all(open('k8s/base/configmaps.yaml'))); print('OK')"
```

- [ ] **Step 6: Commit both files together**

```bash
cd /Users/xiilab/git/isaac-launchable
git add k8s/isaac-sim/deployment-1.yaml k8s/base/configmaps.yaml
git commit -m "feat(k8s): pod-1 expose UDP 31098-31197 window + signal 49101

Matches pod-0 structure with a disjoint UDP window and a distinct
signal TCP port so both pods can coexist on ws-node074 without
hostPort conflicts. nginx-config-1 sidecar proxy target updated
from 49100 to 49101 so the viewer signal path still terminates at
Kit inside pod-1."
```

---

### Task 8: Update `runheadless-script-0` and `-1` ConfigMaps to forward portRange flags

**Files:**
- Modify: `k8s/base/configmaps.yaml`

- [ ] **Step 1: Add portRange flag injection in the runheadless bash**

Inside the `runheadless-script-0` ConfigMap, locate the line:

```sh
[ -n "${ISAACSIM_STREAM_PORT}" ] && EXTRA_FLAGS="${EXTRA_FLAGS} --/exts/omni.kit.livestream.app/primaryStream/streamPort=${ISAACSIM_STREAM_PORT}"
```

Add **immediately after**:

```sh
[ -n "${ISAACSIM_WEBRTC_PORT_MIN}" ] && EXTRA_FLAGS="${EXTRA_FLAGS} --/<portRangeKey>/min=${ISAACSIM_WEBRTC_PORT_MIN}"
[ -n "${ISAACSIM_WEBRTC_PORT_MAX}" ] && EXTRA_FLAGS="${EXTRA_FLAGS} --/<portRangeKey>/max=${ISAACSIM_WEBRTC_PORT_MAX}"
```

Replace `<portRangeKey>` with the exact key confirmed in Task 3 (from the probe notes).

- [ ] **Step 2: Apply the same change to `runheadless-script-1`**

Same two lines, inserted at the same position.

- [ ] **Step 3: Validate YAML**

```bash
python3 -c "import yaml; list(yaml.safe_load_all(open('k8s/base/configmaps.yaml'))); print('OK')"
```

- [ ] **Step 4: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add k8s/base/configmaps.yaml
git commit -m "feat(configmap): forward ISAACSIM_WEBRTC_PORT_MIN/MAX to Kit

runheadless.sh now translates the per-pod UDP window env into Kit
livestream portRange settings, pinning Kit's ephemeral UDP binds
to the hostPort window defined in each pod's deployment manifest."
```

---

### Task 9: Retire or widen the NodePort media Services

**Files:**
- Modify: `k8s/base/services.yaml`

- [ ] **Step 1: Decide policy** — with hostPort windows now directly exposing UDP on the node IP, the `isaac-launchable-{0,1}-media` NodePort Services are redundant. Remove them to avoid double-exposure and port-reservation conflicts.

In `k8s/base/services.yaml`, delete the two Service objects named `isaac-launchable-0-media` and `isaac-launchable-1-media` (lines 66–103, the two blocks with `type: NodePort`, `ports.name: webrtc-media`). Keep `kit-streaming-media` untouched (different workload).

- [ ] **Step 2: Validate YAML**

```bash
python3 -c "import yaml; list(yaml.safe_load_all(open('k8s/base/services.yaml'))); print('OK')"
```

- [ ] **Step 3: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add k8s/base/services.yaml
git commit -m "refactor(k8s): drop redundant pod-0/-1 media NodePort Services

hostPort windows in deployment-0/-1.yaml now expose the same UDP
ports directly. Keeping both would double-reserve 30998/30999 and
interfere with the new 100-port window allocation."
```

---

### Task 10: Roll out the new configuration and verify Kit binds inside the window

**Files:**
- Apply: all committed k8s manifests

- [ ] **Step 1: Apply all manifests**

```bash
cd /Users/xiilab/git/isaac-launchable
ssh root@10.61.3.75 "k0s kubectl apply -f -" < k8s/base/configmaps.yaml
ssh root@10.61.3.75 "k0s kubectl apply -f -" < k8s/base/services.yaml
ssh root@10.61.3.75 "k0s kubectl apply -f -" < k8s/isaac-sim/deployment-0.yaml
ssh root@10.61.3.75 "k0s kubectl apply -f -" < k8s/isaac-sim/deployment-1.yaml
```

Expected: each apply reports `configured`.

- [ ] **Step 2: Roll out pod-0 and pod-1**

```bash
ssh root@10.61.3.75 '
  k0s kubectl rollout restart deployment/isaac-launchable-0 -n isaac-launchable
  k0s kubectl rollout restart deployment/isaac-launchable-1 -n isaac-launchable
  k0s kubectl rollout status  deployment/isaac-launchable-0 -n isaac-launchable --timeout=5m
  k0s kubectl rollout status  deployment/isaac-launchable-1 -n isaac-launchable --timeout=5m
'
```

Expected: both `deployment … successfully rolled out`.

- [ ] **Step 3: Confirm the hostPort registrations on the node**

```bash
ssh root@10.61.3.75 '
  k0s kubectl get pod -n isaac-launchable -l app=isaac-launchable \
    -o jsonpath="{range .items[*]}{.metadata.name}{\": \"}{.spec.containers[0].ports[?(@.protocol==\"UDP\")].hostPort}{\"\\n\"}{end}"
' | head
```

Expected: pod-0 line lists 100 hostPorts (30998 … 31097), pod-1 line lists 100 (31098 … 31197).

- [ ] **Step 4: Launch `quadrupeds.py` in pod-0 and verify Kit binds inside the window**

```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o name" | head -1 | cut -d/ -f2)
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -i -- bash -s" <<'REMOTE'
nohup /isaac-sim/runheadless.sh >/tmp/rh_verify.log 2>&1 &
until grep -q "app ready" /tmp/rh_verify.log; do sleep 3; done
ss -uln | awk 'NR>1 && $5 ~ /^0\.0\.0\.0:[0-9]+$/ {split($5,a,":"); p=a[2]+0; if (p>=30998 && p<=31097) print "IN_WINDOW:", $5}'
REMOTE
```

Expected: one or more `IN_WINDOW:` lines pointing to ports in 30998–31097. **If any new UDP port outside the window appears, increase the window size (e.g. 200) in all three files and redo Task 6/7/10.**

- [ ] **Step 5: Commit nothing** — verification only.

---

### Task 11: End-to-end validation in the browser

**Files:**
- None (manual browser-side verification)

- [ ] **Step 1: `quadrupeds.py` regression check**

Open `http://10.61.3.125/viewer/` in Chrome. In pod-0 terminal, `pkill -TERM -f runheadless` to stop the previous, then run `/isaac-sim/runheadless.sh` manually and wait for `app ready`.

Expected: the browser viewport shows the quadrupeds scene within ~20 s.

- [ ] **Step 2: `play.py --livestream 2` — the actual target**

Stop the runheadless Kit (`pkill -TERM -f runheadless`) and launch `play.py` instead:

```bash
POD=$(ssh root@10.61.3.75 "k0s kubectl get pod -n isaac-launchable -l instance=pod-0 -o name" | head -1 | cut -d/ -f2)
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -i -- bash -s" <<'REMOTE'
cd /workspace/isaaclab
nohup ./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/play.py \
  --task Isaac-Ant-v0 --num_envs 4 --livestream 2 \
  >/tmp/play_final.log 2>&1 &
echo PID=$!
REMOTE
```

Refresh the browser tab.

Expected: `chrome://webrtc-internals` shows `inbound-rtp (kind=video, codec=H264)`; the viewport renders four Ant robots walking. Session stays open ≥ 30 s without `SERVER_DISCONNECTED`.

- [ ] **Step 3: pod-1 coexistence check**

Use pod-1's viewer URL (distinct signal hostPort 49101 — either a separate ingress rule or a direct `?signalPort=49101` param depending on how web-viewer is configured). Verify pod-1's browser tab shows its own Kit, and pod-0's tab is not disturbed.

- [ ] **Step 4: `train.py` smoke check**

```bash
ssh root@10.61.3.75 "k0s kubectl exec -n isaac-launchable $POD -c vscode -i -- bash -s" <<'REMOTE'
cd /workspace/isaaclab
pkill -TERM -f python.sh; sleep 5
nohup ./isaaclab.sh -p scripts/reinforcement_learning/rsl_rl/train.py \
  --task Isaac-Ant-v0 --num_envs 64 --livestream 2 --max_iterations 5 \
  >/tmp/train_final.log 2>&1 &
REMOTE
```

Expected: browser renders training scene (many envs) at interactive FPS; no `SERVER_DISCONNECTED`.

- [ ] **Step 5: If any step fails, stop and escalate** — do not "add error handling" guesswork. Capture the failing log and the webrtc-internals snapshot into the probe notes, then pause for human review.

---

### Task 12: Update memory and correct the upstream GitHub issue

**Files:**
- Modify: `/Users/xiilab/.claude/projects/-Users-xiilab-git-HAMi/memory/project_isaac_lab_livestream_status.md`
- Post comment: Isaac Lab issue #5364 (via `gh`)

- [ ] **Step 1: Append resolution notes to memory**

Add a new section at the end of `project_isaac_lab_livestream_status.md`:

```markdown
## 2026-04-24 오전: 진짜 원인 규명 + Track C 해결

**Root cause**: Isaac Lab / Kit 버그 아님. `hostNetwork=false` k8s pod 에서 Kit WebRTC 가 다수의 ephemeral UDP 포트를 bind 하는데 `hostPort` 매핑은 30998 단일 → 외부 도달 불가 → SDP answer 에 video track 포함돼도 connectivity-check 실패 → `SERVER_DISCONNECTED`.

**Evidence**: `hostNetwork=true` 재현 실험에서 `ss -ulnp` 가 4+ 개의 ephemeral UDP (37879, 38674, 47750, 49770 …) 관찰. `primaryStream.streamPort=30998` 은 advertise 값일 뿐 실제 bind 아님.

**Fix**: 각 파드에 UDP 100-port window 할당 (pod-0: 30998–31097, pod-1: 31098–31197) + Kit `<portRangeKey>` 설정으로 bind 제한 + `hostPort` 로 전체 window 노출. `deployment-0/-1.yaml`, `configmaps.yaml`, `services.yaml` 수정. Isaac Lab 소스는 손대지 않음.

**Upstream issues update**: #5364 는 Isaac Lab 버그가 아니라 하부 인프라 설정 이슈였으므로 maintainer 에게 close 요청. #5362 (deadlock) / #5363 (obs_groups) 는 별개 이슈로 유지.
```

- [ ] **Step 2: Post a corrective comment on Isaac Lab #5364**

Save the following to `/tmp/5364_correction.md`:

```markdown
## Follow-up 2026-04-24: root cause is k8s pod networking, not Isaac Lab

Further investigation confirmed that the missing `inbound-rtp (kind=video)` is caused by Kubernetes pod network isolation, not by any Isaac Lab or Kit code bug:

- With `hostNetwork: true` on the pod, `play.py --livestream 2` renders correctly in the browser viewer. Same Isaac Lab version, same Kit experience, only the network namespace differs.
- `ss -ulnp` inside the pod shows Kit WebRTC binds 4+ ephemeral UDP ports in the 32768–60999 range. The `primaryStream.streamPort=30998` setting is an ICE-candidate advertise value, not a bind constraint, so a single `hostPort: 30998/UDP` mapping leaves the other bound ports trapped inside the pod network namespace. All host ICE candidates then fail their connectivity checks and the server tears down the session after ~51 s.
- The fix, implemented in our deployment, is to constrain Kit's bind range via the livestream portRange setting and expose the whole window as `hostPort` mappings. Isaac Lab and Kit require no changes.

Please consider closing this issue; the earlier attempts matrix I posted described a symptom that was downstream of a network-layer misconfiguration on our side. Thanks.
```

Then post:

```bash
gh issue comment 5364 --repo isaac-sim/IsaacLab --body-file /tmp/5364_correction.md
rm /tmp/5364_correction.md
```

Expected: a new comment URL is printed.

- [ ] **Step 3: Commit the memory update**

```bash
cd /Users/xiilab/.claude/projects/-Users-xiilab-git-HAMi/memory
git add project_isaac_lab_livestream_status.md
git commit -m "memory: record 2026-04-24 Track C resolution" 2>/dev/null || true
```

(The memory directory may or may not be a git repo; `|| true` avoids a plan-level hard fail if it is not.)

---

### Task 13: Tag the repo and write a short operator note

**Files:**
- Modify: `README.md` (top-level)

- [ ] **Step 1: Add an operator section in README.md**

Append at the end of `/Users/xiilab/git/isaac-launchable/README.md`:

```markdown
## Operating notes — WebRTC port window

Each Isaac Sim pod advertises a contiguous UDP window for Kit WebRTC
bindings. The window is exposed via `hostPort`, and Kit is constrained
to bind only inside it through `ISAACSIM_WEBRTC_PORT_MIN/MAX` (forwarded
to Kit by `runheadless.sh`).

| pod | UDP window | signal TCP |
|---|---|---|
| isaac-launchable-0 | 30998–31097 (100 ports) | 49100 |
| isaac-launchable-1 | 31098–31197 (100 ports) | 49101 |

If you add a third pod, pick a disjoint window (e.g. 31198–31297) and a
distinct signal port (49102). If `ss -uln` inside a pod ever shows Kit
UDP binds outside the configured window, increase the window size in
both the deployment manifest (`./scripts/gen-webrtc-ports.sh <base> <N>`)
and the env vars.
```

- [ ] **Step 2: Commit**

```bash
cd /Users/xiilab/git/isaac-launchable
git add README.md
git commit -m "docs(readme): document WebRTC port window operating contract"
```

---

## Done criteria

All five must be true:

1. `chrome://webrtc-internals` shows `inbound-rtp (kind=video, codec=H264)` within 10 s of opening `http://10.61.3.125/viewer/` while pod-0 runs `play.py --livestream 2`.
2. Same for pod-1 in a second browser tab, simultaneously.
3. Session uptime ≥ 30 s without `SERVER_DISCONNECTED`.
4. `train.py --livestream 2 --max_iterations 5` renders live and completes without stream drop.
5. `git log --oneline` in `~/git/isaac-launchable` shows commits from Tasks 5, 6, 7, 8, 9, 13 (Task 1's probe note is optional, Task 12's memory commit is in a different repo).

If any of the five fails, do not declare completion — fall back to Track D planning.
