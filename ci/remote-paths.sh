# Sourced by the remote CI and recovery launchers after their private settings.
ci_root=${VOPTRACK_CI_ROOT:?set VOPTRACK_CI_ROOT to a private directory on the remote host}
ci_lock=${VOPTRACK_CI_LOCK:-$ci_root/heavy.lock}
for remote_path in "$ci_root" "$ci_lock"; do
  if [[ ! "$remote_path" =~ ^(/[A-Za-z0-9_.-]+)+$ || "$remote_path/" =~ /\.{1,2}/ ]]; then
    echo 'Remote CI paths must be absolute and contain only ordinary path segments.' >&2
    exit 2
  fi
done
