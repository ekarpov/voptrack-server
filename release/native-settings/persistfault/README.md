# Test-only native journal persistence fault supervisor

This candidate-only prototype starts an unchanged child beneath a seccomp
USER_NOTIF filter for fsync/fdatasync. It continues all calls until a task-owned
control file contains `fail N NONCE`. It injects EIO into the Nth sync whose fd
symlink metadata matches EXACT_JOURNAL_DIRECTORY/.journal-*. Changing NONCE resets
the counter. It never opens, reads, copies, or edits the journal. It emits only
readiness and an injection ordinal, never paths, credentials or contents.

Build: `cc -Wall -Wextra -Werror -O2 -static notify.c -o notify`.
Invocation: `notify CONTROL EXACT_JOURNAL_DIRECTORY COMMAND ARGS...`.

Kernel: 6.18.54.1-microsoft-standard-WSL2, actions_avail includes user_notif.
Compiled successfully on 2026-10-17. Both minimal probes failed before child exec:

- WSL: notify /tmp/nonexistent-fault-mode /tmp /bin/true
- Disposable container: docker run --rm --name alpha37-persistfault-probe
  --network none --cap-drop ALL --security-opt no-new-privileges:true --read-only
  -v CANDIDATE/release/native-settings/persistfault/notify:/persistfault:ro
  --entrypoint /persistfault
  sha256:5f31d874d8105a04f44d257dc0bb68297eec85187c58ffdb800251c66fd0d2a8
  /no-mode /tmp /bin/true

Both report: `seccomp USER_NOTIF unavailable: Device or resource busy` (EBUSY).
The exact cause is not established. No global security configuration, host
privilege, existing deployment or upstream source was changed. Container --rm
removed the probe container. No native persistence-failure proof was executed.

## Working process-scoped alternative

`trace.c` / `trace` uses unprivileged PTRACE_TRACEME for its own child,
PTRACE_O_TRACECLONE/FORK/VFORK for Go threads/children, PTRACE_GET_SYSCALL_INFO
for entry/exit, and PTRACE_O_EXITKILL. On a matching sync entry it suppresses that
one syscall and returns EIO at exit. No host configuration, capability or container
security relaxation is needed. SIGTERM/SIGINT forward to the task child group.
This test-only implementation is Linux x86_64.

Build: `cc -Wall -Wextra -Werror -Wno-misleading-indentation -O2 -static trace.c -o trace`.
Invocation: `trace CONTROL EXACT_JOURNAL_DIRECTORY COMMAND ARGS...`.

Successful 2026-10-17 selftests:
- Parent traces own child and exits correctly, in WSL and restricted container.
- Synthetic unrelated-file fsync continues; second .journal-* sync gets EIO;
  third continues. Static sync-probe checks return/errno, then removes its files.
- Pass-through mode completes all synthetic syncs.
- Exact ordinal-2 test passes inside named --rm container, no network/caps,
  no-new-privileges, read-only rootfs/bind, task-only writable tmpfs.
- C sources compile with warnings as errors; Python gate helper compiles.

The native_persistence_checks.py helper has not yet been run against Tessera.
Selftests do not establish native persistence coverage. The worker must run and
review it before claiming native admission/effective-write proof.

Scenario: isolated quiet producer, scheduling disabled through public settings;
target admission (ordinal 1) and effective-write (ordinal 2) separately; require
observed injection plus public settings_unavailable/null authoritative policy;
restart without fault and reconcile the exact same request identity/payload.
Private .journal naming is shared among atomicJSON writes, so quiescence and
public settings-specific observations are required; an arbitrary fsync error
proves nothing about settings persistence. Never invent a failed request outcome
after an uncertain response. Physical power-loss durability is outside this probe.

## Stage-specific directory-sync extension

Control `fail-dir N NONCE` selects fsync/fdatasync whose fd symlink metadata equals
the exact acquisition root directory. `fail N NONCE` still selects only its
.journal-* temp files. No private file contents are accessed.

Published alpha.37 atomicJSON syncs the temp file, closes it, renames it, then
syncs the directory (internal/acquisition/manager.go:315–338). A directory-sync
failure therefore occurs after rename: an admission 503 is uncertain even when
that same request is found and becomes effective on restart. The helper now runs
admission/effective faults at both stages and always reconciles the identical
request ID/payload. Only the pre-rename temp admission asserts absent request and
unchanged acknowledged policy after restart. Directory recovery accepts supported
public retained readback or absence, then replays that exact binding.

The main worker reported the original two temp-file cases passing against actual
alpha.37 image 435b0cd7 (alpha37-kit-persistence.log). The extension is a later tree;
that result does not cover directory cases. Strict C/Python compilation and a
synthetic own-child directory test pass: temp-file sync excluded, only second
exact-directory fsync returns EIO, third directory sync continues. Actual-image
four-case results must be recorded separately. These syscall EIO probes do not
simulate physical power loss or establish crash-storage durability.

## Deterministic acquisition publication-boundary pause

`pause-stage NONCE` parks only the traced thread entering fsync/fdatasync on an
exact acquisition-root/stage-* directory (directory metadata verified; no nested
path). Other traced threads continue. `release NONCE` resumes that syscall;
automatic release after 90 seconds emits a timeout marker and invalidates the
fixture proof. A test-only VOPTRACK_FAULT_PAUSE_MS can shorten, never extend, this
bound for synthetic selfchecks.

Published alpha.37 acquisition/manager.go:543 has returned from capture before
stage sync at :589; manager publication mutex is acquired at :597. Standalone
server.go:356–364 finishes both backup capture and retirement evidence before
returning. SQL evidence life/journal deferred locks have unwound. Consequently
this proves acquisition still running at the publication boundary; it does not
prove overlap throughout snapshot capture or every acquisition phase.

Synthetic multithread tests pass: explicit release, automatic timeout and an
independent traced thread advancing while the stage thread is parked. Strict C
compilation and Python wrapper compilation pass. The producer_stage_checks
context wrapper requires explicit device maintenance barriers only around its
intentional test-owned producer restarts: drain current device calls, recreate,
wait for healthy public observation, resume. Device workers remain running for
all measured staged-publication windows; errors there are neither suppressed nor
retried into success. Cleanup restores the normal entrypoint and original policy
through public commands.
