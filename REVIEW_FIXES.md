# pve-fleeting-plugin — Fix Instructions

This document was produced by three independent deep-review passes (duplication,
correctness/quality, CVE/security) over the Go repository at
`/Users/mnefedev/Documents/myp-a_git/pve-fleeting-plugin`, run on 2026-10-17.
It is written for an engineer/model with **no prior context** on this codebase —
read the "Context" section first, then work through the tiers **in order**.

## Context — what this project is

A GitLab Runner "fleeting" plugin that autoscales CI executors by cloning a
Proxmox VE template VM. Fork of `github.com/LukasK32/fleeting-plugin-proxmox`,
now at `github.com/myplixa/pve-fleeting-plugin`. Core files, all under
`cmd/fleeting-plugin-proxmox/`:

- `plugin/instance_group.go` — the `provider.InstanceGroup` implementation
  (`Init`/`Increase`/`Decrease`/`Update`/`ConnectInfo`/`Heartbeat`/`Shutdown`).
- `plugin/instances.go` — clone/deploy/mark-for-removal logic.
- `plugin/collector.go` — background goroutine that deletes instances marked
  `-removing`.
- `plugin/network_config.go`, `plugin/resources.go`, `plugin/connector_ssh.go` —
  per-clone configuration (network/VLAN/DHCP, CPU/RAM/disks, SSH key injection).
- `plugin/naming.go` — `<base>-<shortUUID>` instance naming + lifecycle-state
  prefix matching (`hasInstanceNamePrefix`).
- `plugin/settings.go` — `Settings` struct, `CheckRequiredFields`,
  `FillWithDefaults`.
- `plugin/proxmox.go` — Proxmox API client construction, pool/VM lookups.

**This has been live-tested against a real Proxmox host this session** —
clone, network/VLAN override, DHCP injection, CPU/RAM/disk resize, SSH key
injection, and a real GitLab Runner pipeline were all verified working. Do
not refactor in ways that change externally-observable behavior of anything
not explicitly listed below. When in doubt, make the smallest change that
fixes the specific finding.

## Rules for whoever executes this

1. **One finding at a time.** After each change: `go build ./...`,
   `go vet ./...`, `go test ./cmd/... -race`. All three must stay clean
   before moving to the next finding.
2. Line numbers below are a locator, not a guarantee — they will drift as
   you apply earlier fixes. Find the code by the quoted snippet/function
   name, not by blind line-number lookup.
3. Do not touch anything under "Tier 5 — do not touch" — three independent
   reviews checked these and found them correct; changing them is far more
   likely to introduce a regression than to fix anything.
4. Items marked **[NEEDS LIVE VERIFICATION]** change behavior that was only
   ever checked against a live Proxmox host. Make the code change and get
   it building/passing unit tests, but flag it back to the user before
   considering it done — don't assume it works without a real deploy test.
5. Never commit or push. That's a separate, explicit step the user controls.
6. `pve-probe-token.json` and `cmd/liveprobe/` (a live-credential leak and
   hand-run test harness respectively) have **already been removed** and
   `.gitignore` already covers them — do not recreate them.

---

## Tier 1 — Critical correctness bugs (fix first, low risk)

### 1.1 `Increase()` reports failed deploys as successes

**File:** `plugin/instance_group.go`, inside `Increase`, the `errorGroup.Go`
closure (search for `"failed to deploy an instance"`).

**Problem:**
```go
vmid, err := ig.deployInstance(ctx, template, cloneMu)
if err != nil {
    ig.log.Error("failed to deploy an instance", "vmid", vmid, "err", err)
}
ig.log.Info("successfully deployed instance", "vmid", vmid)
succeededMu.Lock()
succeeded++
succeededMu.Unlock()
return err
```
The error is logged but the function doesn't `return` out of that block, so
execution falls through: it logs a bogus "successfully deployed" on every
failure and still increments `succeeded`. `Increase` is contractually "how
many instances were successfully requested" (see the `provider.InstanceGroup`
interface doc) — on a batch where every clone fails, this currently returns
`(5, err)` instead of `(0, err)`, and the taskscaler believes instances exist
that don't.

**Fix:**
```go
vmid, err := ig.deployInstance(ctx, template, cloneMu)
if err != nil {
    ig.log.Error("failed to deploy an instance", "vmid", vmid, "err", err)
    return err
}

ig.log.Info("successfully deployed instance", "vmid", vmid)
succeededMu.Lock()
succeeded++
succeededMu.Unlock()
return nil
```

**Verify:** add/check a unit test that calls `Increase` against a fake/mocked
failure path if the test scaffolding supports it; otherwise this is covered
by re-reading the diff carefully — the existing tests don't exercise this
path, so build+vet+test passing doesn't prove it's fixed. Treat it as code
review on your own diff.

---

### 1.2 `FillWithDefaults` sets `InstanceNetworkProtocol` twice (dead code)

**File:** `plugin/settings.go`, function `FillWithDefaults`.

**Problem:** the exact same block appears twice:
```go
if s.InstanceNetworkProtocol == "" {
    s.InstanceNetworkProtocol = DefaultInstanceNetworkProtocol
}
```
once near the top of the function, once again at the very end. The second
copy is unreachable-by-construction (the field is never cleared in between).
Harmless today, but it's exactly the kind of thing that survives review
because the duplicate is currently inert — if someone later adds logic
between the two copies expecting the field to still be empty, it'll silently
not be.

**Fix:** delete the second (trailing) copy of the block. Keep the first one.

---

### 1.3 Inconsistent task-waiting on instance rename (`deployInstance`)

**File:** `plugin/instances.go`, function `deployInstance`.

**Problem:** there are two places that rename a VM via `vm.Config(ctx, proxmox.VirtualMachineOption{Name: "name", Value: ...})`:
- The one at the end of `deployInstance` (renames to `-running` on success or
  `-removing` on failure) does **not** wait for the task:
  ```go
  _, renameErr := vm.Config(ctx, proxmox.VirtualMachineOption{
      Name:  "name",
      Value: renameToState(vm.Name, newBase),
  })
  if renameErr != nil {
      ig.log.Error("failed to rename instance", "vmid", VMID, "err", renameErr)
  }
  ```
- The one in `markInstancesForRemoval` (used by `markStaleInstancesForRemoval`
  and `Decrease`) **does** wait:
  ```go
  task, err := vm.Config(ctx, proxmox.VirtualMachineOption{...})
  if err == nil {
      err = task.Wait(ctx, proxmoxTaskWaitInterval, proxmoxTaskWaitTimeout)
  }
  ```
Same logical operation, two different reliability guarantees. The
non-waiting one is the rename that finalizes a successful deploy (the one
`Update()` depends on to ever see `StateRunning`) — if the async rename task
fails after `vm.Config` returns successfully-enqueued, nobody finds out.

**Fix:** make `deployInstance`'s rename wait for the task too, matching the
`markInstancesForRemoval` pattern:
```go
task, renameErr := vm.Config(ctx, proxmox.VirtualMachineOption{
    Name:  "name",
    Value: renameToState(vm.Name, newBase),
})
if renameErr == nil {
    renameErr = task.Wait(ctx, proxmoxTaskWaitInterval, proxmoxTaskWaitTimeout)
}
if renameErr != nil {
    ig.log.Error("failed to rename instance", "vmid", VMID, "err", renameErr)
}
```
(Variable names: match whatever the surrounding function already uses for
`task`/`err` to avoid shadowing issues — read the full function before
editing, there's an outer `err` in scope from the deploy steps above this
block.)

---

### 1.4 `renameToState` can produce an untrackable VM name

**File:** `plugin/naming.go`, function `renameToState`.

**Problem:**
```go
func renameToState(currentName, newBase string) string {
    idx := strings.LastIndex(currentName, "-")
    if idx < 0 {
        return newBase
    }
    return newBase + currentName[idx:]
}
```
When `currentName` has no `-` at all, this returns the **bare** `newBase`
(e.g. `"ci-vm-small-running"` with no UUID suffix). But
`hasInstanceNamePrefix` requires `base + "-"` as a prefix match — a bare
`newBase` with nothing after it fails every single lifecycle check
(`Update`, `Decrease`, the collector) simultaneously. The VM becomes a
permanent orphan, invisible to all three.

In practice `currentName` should always have a `-` (it was itself produced
by `newInstanceName`, which always appends `-<8 hex chars>`), so this is a
defensive-coding gap, not something that fires on the happy path. But fix it
anyway — cheap insurance against a future caller passing a raw/un-suffixed
name.

**Fix:**
```go
func renameToState(currentName, newBase string) string {
    idx := strings.LastIndex(currentName, "-")
    if idx < 0 {
        return newInstanceName(newBase)
    }
    return newBase + currentName[idx:]
}
```

**Then fix the test that currently asserts the broken behavior.** In
`plugin/naming_test.go`, `Test_renameToState` has a case named
`"no separator in current name"` asserting `renameToState("nodash", "ci-vm-small-removing") == "ci-vm-small-removing"`.
Update it to assert the new behavior instead — the result should have the
`"ci-vm-small-removing-"` prefix with some non-empty suffix:
```go
{
    name:        "no separator in current name",
    currentName: "nodash",
    newBase:     "ci-vm-small-removing",
},
```
and in the test body, for this case, assert
`require.True(t, hasInstanceNamePrefix(got, tt.newBase))` instead of an exact
string match (since the UUID is random). Look at how `Test_newInstanceName`
in the same file already does this kind of prefix assertion and follow that
pattern.

---

### 1.5 `Decrease()` doesn't take `instanceCloningMu`

**File:** `plugin/instance_group.go`, function `Decrease`.

**Problem:** The mutex comment on `instanceCloningMu` (on the `InstanceGroup`
struct definition) explains it exists because "when a new instance is
created with a recycled ID then for a brief period it will be reported from
Proxmox with old name". `Update()` takes this mutex before reading the pool
listing; `collectRemovedInstances` (in `collector.go`) takes it too. `Decrease`
does **not** — it reads `member.Name` from a pool listing and renames based
on it, with no protection against the exact race the mutex exists to guard
against. A stale listing entry for a recycled VMID could cause `Decrease` to
rename a brand-new (different) instance to `-removing`, after which the
collector deletes a live runner.

**Fix:** wrap the pool-read-and-rename body of `Decrease` with the same
`ig.instanceCloningMu.Lock(); defer ig.instanceCloningMu.Unlock()` pattern
`Update` uses, placed right after the `getProxmoxPool` error check (same
position `Update` puts it — check `Update`'s exact placement and mirror it).

**[NEEDS LIVE VERIFICATION]** — this changes lock ordering/contention.
Confirm `go test ./cmd/... -race` stays clean (it should, since you're just
adding a mutex acquisition, not changing what's protected), but there's no
way to exercise the actual recycled-VMID race in a unit test — this is a
"the fix is obviously correct by inspection" situation, flag it as such.

---

## Tier 2 — Important reliability bugs (fix after Tier 1, read carefully)

### 2.1 Key regeneration on restart breaks SSH to already-running instances

**Files:** `plugin/instance_group.go` (`Init`), `plugin/instances.go`
(`markStaleInstancesForRemoval`).

**Problem:** `Init` generates a fresh RSA key every process start when the
operator hasn't supplied `connector_config.key`. `markStaleInstancesForRemoval`
only sweeps instances whose name has the `InstanceNameCreating` prefix. An
instance already in `-running` state survives a plugin restart untouched,
keeps being reported as `StateRunning` by `Update`, and keeps getting jobs
dispatched to it — but `ConnectInfo` now hands out the **new** key, which
that VM's `authorized_keys` (populated at clone time with the **old** key)
will reject. Every job on a surviving instance fails SSH auth until GitLab
Runner eventually tears it down for other reasons (idle timeout, max_use_count).

**This only matters when the operator relies on self-generated keys** (i.e.
`connector_config.key` is unset in their `config.toml`). If they supply
their own fixed key, this doesn't apply — the key is stable across restarts.

**Fix (pick one, this needs a decision, not just mechanical editing):**

- **Option A (safer, simpler):** when `Init` self-generates a key (i.e. the
  branch where `ig.FleetingSettings.Key == nil` fires), also sweep
  `InstanceNameRunning`-prefixed instances for removal in
  `markStaleInstancesForRemoval`, not just `InstanceNameCreating`-prefixed
  ones. This means every plugin restart tears down all running instances
  when using self-generated keys — correct but potentially disruptive if the
  plugin restarts often.
- **Option B (more work, less disruptive):** persist the generated key to
  disk (next to `credentials_file_path`, or a new setting) and load it back
  on `Init` instead of generating fresh, so restarts don't invalidate
  anything. This needs careful handling of file permissions (match the
  credentials-file 0600 convention) and a decision about key rotation.

Given the scope of this document, implement **Option A** — it's the smaller,
safer change. Leave a `// TODO` comment noting Option B as a future
improvement if the operator wants restart-persistent running instances.

**[NEEDS LIVE VERIFICATION]** — this changes what gets torn down on restart.
Get it building and passing unit tests, then flag to the user to validate
against a real Proxmox host (start plugin, let an instance go to `-running`,
restart the plugin process, confirm the running instance now gets swept).

---

### 2.2 Orphaned `-creating` VMs on clone failure after the Proxmox POST succeeds

**File:** `plugin/instances.go`, function `deployInstance`.

**Problem:**
```go
VMID, task, err := ig.cloneTemplate(ctx, template, cloneMu)
if err == nil {
    ig.log.Info("Deploying new instance", "vmid", VMID)
    err = task.Wait(ctx, proxmoxTaskWaitInterval, proxmoxTaskWaitTimeout)
}
if err != nil {
    return VMID, fmt.Errorf("failed to deploy instance: %w", err)
}

vm, err := ig.getProxmoxVM(ctx, VMID)
if err != nil {
    return VMID, fmt.Errorf("failed to find newly deployed instance vmid='%d': %w", VMID, err)
}
```
Both `return` statements happen *after* Proxmox has already created the
target VM (the clone task was accepted/ran), but *before* the code path
further down that renames the VM to `-removing` on failure. The VM is left
sitting with its `-creating` name forever — the collector only matches
`-removing`-prefixed names, so it's never cleaned up, and `Update` reports it
as `StateCreating` indefinitely. The only recovery is a plugin restart
(`markStaleInstancesForRemoval` on `Init` does catch `-creating` instances).

Also: `ig.getProxmoxVM(ctx, VMID)` resolves the VMID by scanning the **pool
listing** (`getProxmoxPool` → iterate `pool.Members`). A freshly-cloned VM
that hasn't propagated into the pool listing yet returns `ErrNotFound` here,
which is the exact failure this bug orphans. Since a clone always lands on
the template's own node, use `ig.getProxmoxVMOnNode(ctx, VMID, template.Node)`
instead — it skips the pool-listing round-trip entirely and isn't subject to
listing lag. (Check `getProxmoxVMOnNode`'s signature in `plugin/proxmox.go`
before wiring this up.)

**Fix:**
1. Replace `ig.getProxmoxVM(ctx, VMID)` with
   `ig.getProxmoxVMOnNode(ctx, VMID, template.Node)` in `deployInstance`.
2. For both early-return error paths (the `task.Wait` failure and the VM-lookup
   failure), attempt to mark the instance for removal before returning, so it
   doesn't orphan. The simplest approach: wrap the existing body in the same
   pattern the end of the function already uses — on any failure from this
   point, best-effort rename to `InstanceNameRemoving` and log (not fail) if
   that rename itself fails. Concretely, restructure so VMID-exists failures
   share the same "rename to removing" tail that the rest of the function
   already has, rather than returning early with a bare error. Look at how
   the end of `deployInstance` builds `newBase` and calls `renameToState` —
   reuse that same block for these earlier failure points instead of
   duplicating logic. This may mean moving the clone+lookup steps inside the
   same `err = func() error { ... }()` closure that already wraps the
   start/configure/wait-for-agent steps, so there's exactly one
   success/failure tail instead of three separate ones.

This is a real restructuring, not a one-line fix — read the whole function
first, understand the existing closure-based error handling, and extend it
to cover the clone+lookup steps rather than bolting on more special cases.

**[NEEDS LIVE VERIFICATION]** — the actual Proxmox clone-then-rename
sequence can only be fully validated against a live host. Get the restructured
code building and passing unit tests; flag for a live test where you force a
failure mid-clone if possible (e.g. point `TemplateID` at a VM the token
doesn't have permission to clone).

---

### 2.3 `instanceCloningMu` held across the entire deploy, not just the clone

**File:** `plugin/instance_group.go`, function `Increase`.

**Problem:**
```go
ig.instanceCloningMu.Lock()
defer ig.instanceCloningMu.Unlock()

for n := 0; n < count; n++ {
    errorGroup.Go(func() error {
        vmid, err := ig.deployInstance(ctx, template, cloneMu)
        ...
```
The lock is acquired once for the whole `Increase` call and held through
**every** parallel deploy's clone + network config + resource config +
cloud-init config + `vm.Start` + `WaitForAgent` (up to
`proxmoxAgentStartTimeout`, currently 2 minutes, plus up to 5 minutes per
config task per the `proxmoxTaskWaitTimeout` constant). While held, `Update()`
cannot read any state at all (it also takes `instanceCloningMu`), so the
autoscaler is blind to instance state for potentially minutes during a scale-up,
and the collector can't reclaim anything either.

The actual race this mutex protects against is narrow: a freshly-cloned VM
briefly reporting its *old* (pool-listing-cached) name before the rename
propagates — this only concerns the clone call itself and immediately-after
pool reads, not the full deploy lifecycle. `cloneTemplate` already has its
own separate `cloneMu *sync.Mutex` parameter serializing the actual
`template.Clone` calls.

**Fix:** narrow `instanceCloningMu`'s critical section in `Increase` to just
around the clone step, not the whole deploy. This likely means moving the
lock/unlock into `deployInstance` itself (or a wrapper), scoped tightly
around `ig.cloneTemplate(...)` and nothing after it. Read how
`markStaleInstancesForRemoval` and `collectRemovedInstances` use this same
mutex (both scope it tightly around a pool read) and match that scoping
discipline — the goal is "protect pool-listing reads from seeing stale
names right after a clone", not "serialize all deploy work".

This is a meaningful behavioral change to concurrency timing — be
conservative, make the smallest change that narrows the critical section,
and re-run `go test ./cmd/... -race` after.

**[NEEDS LIVE VERIFICATION]** — concurrency timing changes should be
validated with a live multi-instance `Increase` call (the repo's existing
live-test pattern used `Increase(ctx, 3)` against the real Proxmox host
earlier this session — if you have access to that environment, repeat
something similar and confirm `Update()` is responsive during the scale-up
instead of blocked).

---

### 2.4 `Shutdown()` can't cancel in-flight collector work

**Files:** `plugin/collector.go` (`collectRemovedInstances`),
`plugin/instance_group.go` (`Shutdown`).

**Problem:** `collectRemovedInstances` builds its own context via
`context.WithTimeout(context.Background(), collectionTimeout)` (5 minutes),
and nothing outside the function holds the matching `cancel` func.
`Shutdown` signals `collectorShutdownTrigger` then blocks on
`collectorWaitGroup.Wait()` — but if the collector goroutine is mid-cycle
(stopping/deleting a VM, waiting on a Proxmox task), it can't be interrupted
and `Shutdown` blocks for however long that in-flight work takes, up to the
full 5-minute timeout. The collector's own idle-wait
(`<-time.After(collectionWaitAfterTrigger)` in the main loop) is also not
selected against the shutdown trigger in a way that lets it cancel early —
check the actual `select` in `runRemovedInstanceCollector` to confirm.

**Fix:**
1. Add a `context.CancelFunc` field to `InstanceGroup` (next to the existing
   trigger channels), created via
   `ctx, cancel := context.WithCancel(context.Background())` during `Init`
   (store both; this is the collector's lifetime context).
2. In `Shutdown`, call that `cancel()` in addition to signaling
   `collectorShutdownTrigger`.
3. In `collectRemovedInstances`, derive the per-cycle timeout context from
   the stored lifetime context instead of `context.Background()`:
   `ctx, cancel := context.WithTimeout(ig.collectorCtx, collectionTimeout)`.
   This way, canceling the lifetime context immediately cancels any
   in-flight per-cycle work too.
4. Double check the `runRemovedInstanceCollector` loop's `select` already
   exits promptly on `collectorShutdownTrigger` for the *idle* wait — if the
   `<-time.After(...)` cases aren't already inside a `select` alongside the
   shutdown trigger, that's a second, smaller bug worth fixing the same way
   (the loop already has a `select` with multiple cases per the code — verify
   `collectorShutdownTrigger` is one of them for every wait point, not just
   some).

**[NEEDS LIVE VERIFICATION]** — hard to unit-test cleanly; the fix is
reasoned-through, but shutdown timing under load should be sanity-checked
against the real runner setup on `gitlab-mkud` if convenient (restart the
`gitlab-runner` systemd service while a scale-down is in flight, confirm it
doesn't hang for minutes).

---

### 2.5 `instance_disks` can silently build an invalid Proxmox value

**File:** `plugin/settings.go` (`CheckRequiredFields`), `plugin/resources.go`
(`configureInstanceDisks`).

**Problem:** `storage` is optional per `CheckRequiredFields` (only required
"if template is a VM", per the clone-options logic in `instances.go`). But
new-disk allocation in `configureInstanceDisks` unconditionally does:
```go
Value: fmt.Sprintf("%s:%d", ig.Settings.Storage, disk.SizeGB),
```
If `Storage` is empty, this produces `":50"`, which Proxmox will reject with
an opaque error at deploy time — a config mistake that should be caught at
load time, not mid-deploy.

**Fix:** in `CheckRequiredFields`, in the existing loop over
`s.InstanceDisks` (next to the `diskKeyPattern` and `SizeGB > 0` checks),
add: if `s.Storage == ""` and there's at least one disk entry that would
need allocation (i.e. the whole `InstanceDisks` list is non-empty — you
can't know at config-validation time which specific entries are "new" vs.
"resize existing", since that depends on what's on the template, which isn't
known until deploy time), require `Storage` to be set. Simplest correct
rule: if `len(s.InstanceDisks) > 0 && s.Storage == ""`, return an
`ErrRequiredSettingMissing` for `storage`. This is slightly conservative
(it'll require `storage` even for configs where every disk entry happens to
be a pure resize of an existing disk), but that's an acceptable tradeoff for
catching the real failure mode at config-load time instead of deploy time.
Document this tradeoff in a one-line comment.

---

### 2.6 `buildNetworkOverride` silently no-ops when the template has no `net0`

**File:** `plugin/network_config.go`, functions `buildNetworkOverride` and
`configureInstanceNetwork`.

**Problem:**
```go
parts := strings.Split(current, ",")
if len(parts) == 0 || parts[0] == "" {
    return current, false
}
```
and separately, in `configureInstanceNetwork`:
```go
if vm.VirtualMachineConfig == nil {
    return nil
}
```
If the cloned template has no `net0` device at all (or `VirtualMachineConfig`
is nil for some reason), an operator-configured `instance_network_bridge` /
`instance_network_vlan_tag` / `instance_network_dhcp` is silently dropped —
no log line, no error, the VM just boots with whatever (nothing) the
template had. The operator explicitly asked for a network override; silently
ignoring it is surprising behavior that wastes debugging time when someone's
VMs don't come up with networking.

**Fix:** add a `ig.log.Warn(...)` call at both early-return points
mentioning that network/cloud-init configuration was requested but the
template has no `net0`/config to apply it to. Keep the behavior
non-fatal (don't fail the deploy over this — a template legitimately
might not have the device the operator expects, and failing deploys over
a log-worthy misconfiguration is a bigger behavior change than this
document's scope). Just make it visible instead of silent. Check whether
`ig.log` is accessible from these functions (they're methods on
`*InstanceGroup`, so it should be).

---

### 2.7 `http.Transport` built from scratch loses sane defaults

**File:** `plugin/proxmox.go`, function `getProxmoxClient`.

**Problem:**
```go
httpClient := http.Client{
    Transport: &http.Transport{
        TLSClientConfig: &tls.Config{
            InsecureSkipVerify: ig.Settings.InsecureSkipTLSVerify,
        },
    },
}
```
Building a bare `http.Transport` drops everything `http.DefaultTransport`
normally provides: `Proxy: http.ProxyFromEnvironment` (so `HTTPS_PROXY`/
`NO_PROXY` env vars are silently ignored, which matters for a plugin that
might be reached through a corporate proxy), connection/handshake timeouts,
idle connection pooling settings. The `http.Client` also has no `Timeout`
set at all, so a hung TLS handshake against an unreachable Proxmox host can
block indefinitely (bounded only by whatever context the caller happens to
pass, which isn't guaranteed everywhere).

**Fix:**
```go
transport := http.DefaultTransport.(*http.Transport).Clone()
transport.TLSClientConfig = &tls.Config{
    //nolint:gosec
    InsecureSkipVerify: ig.Settings.InsecureSkipTLSVerify,
}

httpClient := http.Client{
    Transport: transport,
}
```
Do not add a `Client.Timeout` in this pass unless you also check every
call site that might legitimately need a longer-than-default timeout (e.g.
large disk operations) — that's a separate, riskier change. The
`DefaultTransport.Clone()` swap alone is the safe, well-scoped fix.

---

## Tier 3 — Code quality / duplication cleanup (optional, do after Tier 1+2 are verified stable)

These don't fix bugs — they reduce future maintenance risk by removing
places where the same logic is copy-pasted and could drift. Each is safe in
isolation; do them one at a time with build+test in between, same as
everything else.

### 3.1 Extract a shared `task, err := X(); wait; wrap-error` helper

**Files:** `plugin/instances.go`, `plugin/collector.go`,
`plugin/network_config.go`, `plugin/resources.go`, `plugin/connector_ssh.go`.

The pattern
```go
task, err := vm.SomeCall(ctx, ...)
if err == nil {
    err = task.Wait(ctx, proxmoxTaskWaitInterval, proxmoxTaskWaitTimeout)
}
if err != nil {
    return fmt.Errorf("failed to ...: %w", err)
}
```
appears roughly 11 times across these 5 files. Add two small helpers (new
file `plugin/task.go`, or append to `plugin/proxmox.go` if you prefer fewer
files):

```go
func waitForTask(ctx context.Context, task *proxmox.Task, err error, timeout time.Duration) error {
    if err == nil {
        err = task.Wait(ctx, proxmoxTaskWaitInterval, timeout)
    }
    return err
}

func (ig *InstanceGroup) applyVMConfig(ctx context.Context, vm *proxmox.VirtualMachine, what string, options ...proxmox.VirtualMachineOption) error {
    if len(options) == 0 {
        return nil
    }
    task, err := vm.Config(ctx, options...)
    if err := waitForTask(ctx, task, err, proxmoxTaskWaitTimeout); err != nil {
        return fmt.Errorf("failed to configure instance %s: %w", what, err)
    }
    return nil
}
```

Then replace the 5 sites that are `vm.Config(ctx, options...)` +
wait + wrap (in `network_config.go`, `resources.go` ×2,
`connector_ssh.go`) with a single call to `ig.applyVMConfig(ctx, vm, "<description>", options...)`.
For the other call sites (`Start`, `Stop`, `Delete`, `Clone`, `ResizeDisk` —
not `vm.Config`), use just `waitForTask` and keep the custom error-wrap
message at each site, since those aren't identical operations.

**Do this one call site at a time**, confirming `go build && go test` after
each, rather than converting all 11 in one edit — if something doesn't fit
the helper's shape cleanly (check `instances.go`'s two rename sites
especially, after you've already fixed finding 1.3 above — they may now be
identical enough to also use `applyVMConfig`), leave it as-is rather than
forcing it.

### 3.2 Test fixture helpers (reduces duplication, no behavior change)

**File:** `plugin/settings_test.go` — replace the 8 repeated full `Settings{}`
literals in `TestSettings_checkRequiredFields` with a `validSettings(mutate func(*Settings)) Settings`
helper that starts from a known-good `Settings` and applies one mutation per
test case. See the duplication-review output for the exact shape; the
existing `sampleName`/`sampleURL`/etc. package vars stay as-is, just stop
re-declaring all of them in every test case literal.

**File:** `plugin/network_test.go` — the same `[]*proxmox.AgentNetworkIface`
literal (4-address dual-stack version) appears 3 times, and a 2-address
internal-only version appears 3 more times. Extract `dualStackIfaces()` and
`internalOnlyIfaces()` helper functions, each returning the literal once,
and call them from the test cases instead of inlining.

**File:** `plugin/proxmox_test.go` — the `os.WriteFile(path, []byte(...), 0o600)` +
`require.NoError(t, err)` pair appears 4 times; extract a
`writeCredentials(t *testing.T, path, body string)` helper.

These are mechanical, low-risk, and safe to do in one pass each (unlike 3.1,
which touches production code).

### 3.3 Extract `listInstances` / `instanceState` helpers

**Files:** `plugin/instances.go` (`markStaleInstancesForRemoval`),
`plugin/collector.go` (`collectRemovedInstances`), `plugin/instance_group.go`
(`Update`, `Decrease`).

All four functions do "fetch pool → filter to instances via
`isProxmoxResourceAnInstance` → do something based on lifecycle-prefix
matching". Extract:
```go
func (ig *InstanceGroup) listInstances(ctx context.Context) ([]proxmox.ClusterResource, error) {
    pool, err := ig.getProxmoxPool(ctx)
    if err != nil {
        return nil, err
    }
    out := make([]proxmox.ClusterResource, 0, len(pool.Members))
    for _, m := range pool.Members {
        if ig.isProxmoxResourceAnInstance(m) {
            out = append(out, m)
        }
    }
    return out, nil
}

func (ig *InstanceGroup) instanceState(name string) (provider.State, bool) {
    switch {
    case hasInstanceNamePrefix(name, ig.Settings.InstanceNameCreating):
        return provider.StateCreating, true
    case hasInstanceNamePrefix(name, ig.Settings.InstanceNameRunning):
        return provider.StateRunning, true
    case hasInstanceNamePrefix(name, ig.Settings.InstanceNameRemoving):
        return provider.StateDeleting, true
    default:
        return "", false
    }
}
```
Rewire `Update` to use both; rewire `Decrease` and
`markStaleInstancesForRemoval`/`collectRemovedInstances` to use whichever
parts apply (they don't all need the full switch — `Decrease` only checks
for `InstanceNameCreating`/`InstanceNameRemoving`, `collector.go` only checks
`InstanceNameRemoving`). **Important:** `instanceCloningMu` /
`collectInstance`'s own locking must stay exactly where it currently is
relative to the pool fetch — don't let this refactor change *when* the
mutex is held, only *how the fetch-and-filter code is written*. Re-read each
call site's current locking before and after your edit to confirm nothing
moved.

### 3.4 Smaller cleanups (do only if time remains, each is independent)

- `plugin/resources.go`, `configureCPUAndMemory`: the two pointer-deref
  checks (`if numCPUs != nil && *numCPUs > 0`, `if memoryMB != nil && *memoryMB > 0`)
  are identical in shape. A small `intOption(name string, v *int) (proxmox.VirtualMachineOption, bool)`
  helper (see duplication-review output) collapses both and will absorb
  future similar settings without a third copy.
- `plugin/instances.go`, `markInstancesForRemoval`: both the success and
  error path push `ig.instanceCollectionTrigger <- struct{}{}`. Replace with
  a single `defer func() { ig.instanceCollectionTrigger <- struct{}{} }()`
  placed once, right after the function's `errorGroup` is declared — this
  also fixes a latent issue where a future early `return` would skip the
  trigger.
- `plugin/connector_ssh.go`: two `fmt.Errorf("...", err)` / `fmt.Errorf("...")`
  calls with no format verbs referencing `err`/no args at all — these should
  be `errors.New(...)` instead (both at the two `return nil, fmt.Errorf(...)`
  sites in `getSshPubKey`/`generateSshKey` that don't actually use `%w`).
  Check each call site individually — only change the ones with zero format
  arguments; leave any that do wrap an error with `%w` alone.
- `plugin/instance_group.go`, `ConnectInfo`: the three `if info.Protocol == "" {...}`
  /`if info.Username == "" {...}`/`if info.Key == nil {...}` blocks near the
  end are dead code — `info.ConnectorConfig` is a copy of
  `ig.FleetingSettings.ConnectorConfig`, which `Init` already normalized
  (same defaulting logic runs there). This is **optional** — the dead code
  is harmless defensive duplication, not a bug, and removing it saves a few
  lines at the cost of removing a safety net if `Init`'s normalization ever
  changes. Low priority; skip if uncertain.
- Remove the redundant `member := member` / `instance := instance` loop-variable
  copies in `instance_group.go` and `instances.go` — `go.mod` declares
  `go 1.25`, where loop variables are already per-iteration (this was fixed
  language-wide in Go 1.22). These are inert leftovers from the upstream
  code predating that change. Purely cosmetic; skip if you're not confident
  distinguishing which copies are truly redundant vs. load-bearing — get this
  wrong and you reintroduce a real closure-capture bug.

---

## Tier 4 — Security / dependencies (do independently of Tier 1-3)

### 4.1 Dependency version bumps (govulncheck findings)

A full `govulncheck ./...` run found 20 reachable advisories across the Go
stdlib, `golang.org/x/net`, `golang.org/x/crypto`, and `google.golang.org/grpc`.
The stdlib ones need a toolchain bump (see 4.2); the three module ones have a
**verified-working** fix:

```bash
go get golang.org/x/net@v0.60.0 golang.org/x/crypto@v0.57.0 google.golang.org/grpc@v1.83.2
go mod tidy
```

Note: `golang.org/x/net@v0.60.0` requires `golang.org/x/crypto@v0.57.0` — if
you ask for a lower `x/crypto` version alongside it, `go get` will fail (and
`go mod tidy` will silently revert a partial bump), so run exactly the
three-module command above together, not one at a time. This was tested in
an isolated copy of the repo this session: it builds clean and
`go test ./cmd/...` passes afterward. The `go` directive in `go.mod` will
auto-bump to `go 1.26.0` as a side effect — that's expected and fine.

After this, re-run `govulncheck ./...` — it should drop from 20 findings to
14, all of which are stdlib-only (see 4.2).

### 4.2 Toolchain pin for stdlib CVEs + fix the CI/build gap

**Problem:** `go.mod` declares `go 1.25.0` with no `toolchain` directive.
`.github/workflows/build-release.yml` uses `actions/setup-go` with
`go-version-file: go.mod`, which means **released binaries are built with
whatever Go 1.25.x the GitHub Actions runner happens to have**, not a
specific patched version. `govulncheck` found 14 stdlib advisories (in
`net/http`, `crypto/tls`, `net/url`, `net/textproto`, `encoding/asn1`) fixed
in `go1.26.9`+/`go1.26.6`+/`go1.26.5`+.

**Fix:** add an explicit `toolchain` directive to `go.mod`:
```
go 1.26.0
toolchain go1.26.9
```
(adjust the exact patch version to whatever's current/available at the time
you do this — check `go install golang.org/dl/go1.26.9@latest` or the
official Go downloads page for the latest 1.26.x patch release before
hardcoding a version). This makes `go build`/`go test`/CI all use the pinned
patched toolchain automatically (Go's own toolchain-selection mechanism
downloads it if needed), closing the gap between "what govulncheck measured
locally" and "what actually ships in releases".

### 4.3 Credentials file permission check

**File:** `plugin/proxmox.go`, function `getProxmoxAPIToken` (reads
`credentials_file_path`).

**Problem:** the file is `os.Open`'d with no permission check. A
world-readable (`0644`) token file on a shared runner host is a real
privilege-escalation path (the token can create/delete/reconfigure VMs
across the whole Proxmox cluster). README documents the credentials file
format but never tells operators to `chmod 600` it.

**Fix:** after opening the file, `Stat()` it and warn (not hard-fail — avoid
breaking existing working setups) if group/other read bits are set:
```go
if info, statErr := credentialsFile.Stat(); statErr == nil {
    if info.Mode().Perm()&0o077 != 0 {
        ig.log.Warn("credentials file is readable by group/other, consider chmod 600", "path", ig.Settings.CredentialsFilePath)
    }
}
```
Place this right after the successful `os.Open` call, before the
`json.NewDecoder(...).Decode(...)` call. Also add a one-line note to the
README's "Credentials file" section telling operators to `chmod 600` the
file.

### 4.4 README: note the risk of `insecure_skip_tls_verify`

**File:** `README.md`, the `insecure_skip_tls_verify` row in the Plugin
Settings table.

The runtime already logs a `WARN` when this is enabled (confirmed correct
and sufficient as a *mechanism* — it fires once in `Init`). Add a short
caution note near the table (not necessarily in the table cell itself)
pointing operators at installing the Proxmox CA certificate into the host's
trust store as the preferred alternative to disabling verification
entirely.

### 4.5 Add a test for `proxmoxURLEncode`

**File:** `plugin/connector_ssh.go` has `proxmoxURLEncode` — confirmed
correct by two independent reviews (it percent-encodes an SSH public key the
way Proxmox's strict RFC3986-style decoder expects, fixing a real bug found
earlier this session), but it has **zero test coverage**, and it's flagged
by both reviews as the single most regression-prone function in the new
code — an innocent-looking change (e.g. swapping to `url.PathEscape`, or
dropping the `"+"`→`"%20"` replace) would silently break every cloud-init
SSH key injection without any test catching it.

**Fix:** add `plugin/connector_ssh_test.go` with a table test covering:
- An RSA public key (standard `ssh-rsa AAAA...` format).
- An ed25519 public key (`ssh-ed25519 AAAA...`).
- A key with a trailing comment (`ssh-ed25519 AAAA... user@host`).
- Assert the result, when percent-decoded with Go's standard
  `url.QueryUnescape`, round-trips back to the original (trimmed) key
  string — this is a stronger assertion than checking specific `%XX`
  sequences, since it directly validates the property that matters
  ("Proxmox can get the original key back out of this").

---

## Tier 5 — Do not touch (verified correct by multiple independent reviews)

Listed explicitly so nobody "fixes" these by mistake:

- `proxmoxURLEncode`'s actual encoding logic (`plugin/connector_ssh.go`) —
  correct for RSA/ed25519/ECDSA keys. Only add a test (4.5); don't change
  the implementation.
- `determineAddresses` / `determinePossibleAddresses`
  (`plugin/network.go`) — the IPv4/IPv6 branches look parallel but encode
  different protocol semantics; not worth generifying.
- VLAN tag range validation — already correctly enforced in
  `CheckRequiredFields` (0-4094) before `FillWithDefaults` and before any
  deploy path can reach `buildNetworkOverride`. No gap here.
- `cloneMu` placement around `template.Clone` in `instances.go` — correctly
  scoped to the actual Proxmox-side race (`cluster.NextID`).
- The `Config()` + `task.Wait()` pattern itself (not its duplication, which
  Tier 3.1 addresses) — matches go-proxmox v0.8.2's actual API contract
  everywhere it's used.
- Disk resize/allocate logic in `configureInstanceDisks` — uses **exact**
  disk-key matching against `VirtualMachineConfig.MergeDisks()`, which
  correctly handles the "resize existing vs. allocate new" split as
  documented in the README. (An earlier design iteration this session had a
  lexicographic-sort "pick the primary disk" heuristic with CD-ROM
  filtering — that approach was replaced with the current explicit
  `instance_disks` list design specifically to avoid that whole class of
  ambiguity. If you see any reference to "primary disk" auto-detection in
  old notes, it does not apply to the current code.)
- `IntOrBool(false)` / `omitempty` behavior on the `Full` clone option in
  `instances.go` — coincidentally matches Proxmox's own linked-clone default
  for templates. Correct, if slightly non-obvious; a clarifying comment
  would help but the behavior itself is right.
