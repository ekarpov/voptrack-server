# Native recovery regression gates

`producer-recovery.sh` builds published alpha.38 normal/devfault binaries and
uses public CLI/HTTP interfaces. It obtains a genuine export after a live effective
PUT with identity retained independently earlier. It checks fresh recovery, stale
default precedence/restart, new epoch, old administrator/scope refusal, canonical
identical retry and changed-export refusal, lock/options, invalid/initialized
states and five published interruption boundaries with pending provisioning fence.
Opaque read-only intent hashes check retry stability; corrupt/unknown/symlink
private fixtures are negative tests only. It does not prove collector rebinding,
artifact authentication, data restoration, physical power-loss durability or
custody; the extended Console gate covers the first three separately.

`prejournal.sh` is a surviving-producer synthetic export
fixture, not genuine prior readback or host-loss proof. `persistfault/` retains its
four native fsync failures and bounded acquisition publication overlap.

Version qualification: the normal and devfaults binaries must both report the
exact dependency version declared by this checkout's module, rather than a
historical hard-coded alpha.38 string. Recovery/refusal assertions are unchanged.
