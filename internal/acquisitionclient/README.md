# Supported acquisition transport

Alpha.35 catalog, collector provisioning/revocation and exact leased retrieval;
alpha.34 operation admission/reconciliation remains person-authorized. Tokens are
per-call arguments and are never journaled. HTTPS and redirect refusal are
mandatory. The credential-free journal commits the caller-selected ID before PUT.

`complete` describes Tessera output, not local publication or external custody.
`internal/backupcollector` owns collection/publication, with no producer or key
authority. It checks HTTPS provenance, descriptors, exact lengths/digests and
metadata payload agreement. These checks do not verify its HMAC. Run Tessera's
supported verifier with the separately held key before any restore.
