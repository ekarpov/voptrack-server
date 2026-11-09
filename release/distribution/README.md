# Deliver Home and Research through GitHub

Use GitHub Packages to hold the two Docker images and GitHub Releases to hold
matching installation files. A user can pull an image or download a file that
contains both Voptrack and Caddy. Both source repositories are public; users do
not need a GitHub account. This guide prepares a release by hand. It does not
enable GitHub Actions or publish anything automatically.

## Release contents

| Item | Name |
| --- | --- |
| Home image | `ghcr.io/ekarpov/voptrack-home:v0.1.1` |
| Research image | `ghcr.io/ekarpov/voptrack-research:v0.1.1` |
| Small download, per product | `voptrack-<home or research>-v0.1.1-linux-amd64-install.tar.gz` |
| Offline download, per product | `voptrack-<home or research>-v0.1.1-linux-amd64-offline.tar.gz` |
| Combined download checksums | `SHA256SUMS` |

The images contain the same tested programs and separate product defaults. Each
bundle fixes its product and exact image. It includes human and agent guides,
source/version information, checksums and dependency notices. The offline file
also contains both Compose images. Its loader checks the loaded files, writes a
no-pull Compose file and preserves an image-identity receipt.

Only Linux x86-64 is selected for this first preparation. Do not advertise ARM
until it has native runtime and fresh-install evidence. A multi-platform compile
alone is insufficient.

## 1. Select and qualify the source

Review the exact public tree, reachable history, notices and binary contents.
Keep development history and private artifacts out of the public push. Run
`make public-check`; its locations need review and are not automatic failures or
approvals. Do not rewrite append-only specifications to silence catalog questions.

The final squash commit on `main` must have its own passing full CI note. Run:

```sh
make ci-remote
git notes --ref=ci show HEAD
```

Use a clean tree with all 23 named gates passing. Do not copy another commit's
note. No demo may be used for qualification. Public DNS/TLS, real-device tests,
independent recovery custody and fresh-host guide acceptance remain separate.

## 2. Build and keep the two images

On the qualified clean commit, with Docker, Buildx and public Go modules available:

```sh
version=v0.1.1
VOPTRACK_PLATFORMS=linux/amd64 make release-image PRODUCT=home IMAGE="ghcr.io/ekarpov/voptrack-home:$version"
VOPTRACK_PLATFORMS=linux/amd64 make release-image PRODUCT=research IMAGE="ghcr.io/ekarpov/voptrack-research:$version"
docker image inspect "ghcr.io/ekarpov/voptrack-home:$version" --format '{{.Id}}'
docker image inspect "ghcr.io/ekarpov/voptrack-research:$version" --format '{{.Id}}'
```

The commands now load usable images into Docker, rather than discarding the build.
They attach source, version and product labels and verify the built Tessera version.
No publication occurs. Inspect `/usr/share/voptrack/licenses` in each image and
retain the notices with downloads. Do not reuse an image left after a failed build.

Prepare local offline downloads in fresh directories outside the checkout:

```sh
out=$(mktemp -d)
make distribution PRODUCT=home LOCAL=yes ARCHIVE=yes VERSION="$version" PLATFORM=linux/amd64 IMAGE="ghcr.io/ekarpov/voptrack-home:$version" OUTPUT="$out/home"
make distribution PRODUCT=research LOCAL=yes ARCHIVE=yes VERSION="$version" PLATFORM=linux/amd64 IMAGE="ghcr.io/ekarpov/voptrack-research:$version" OUTPUT="$out/research"
printf 'Prepared files: %s\n' "$out"
```

These local downloads are preparation artifacts. They contain no invented registry
digest and generate only the offline delivery path. Qualify each product through
its actual prepared image and matched Compose file before publication.

## 3. Test the installation guides

On fresh authorized Linux hosts, follow each human and agent guide literally:

- Clone the public source at the selected commit, build and start Home, then
  repeat with Research using a separate fresh installation.
- Load each prepared offline download without registry access and start with
  `compose.offline.yaml`. Check that no image pull occurs.
- After publication below, repeat the small-download path with an empty Docker
  credential configuration. Anonymous access must work for both packages.

Check product selection, discovery, HTTPS, owner sign-in, member/participant
access, actual app connection, service/host restarts and an isolated recovery
restore. Record source, image, platform, commands, outcomes and unrun checks.
Wrong architecture, corrupt files or a failed load must not reset existing data.
Do not run destructive tests against review or real-user fixtures.

## 4. Publish only after release approval

Making the repository public does not automatically make its image packages
public. Use a publishing credential with `write:packages`, supplied through
Docker's private credential mechanism. Never put a token in a command argument,
tracked file, image layer or chat. The operator can use a private token file:

```sh
inspections=$(mktemp -d)
docker image inspect "ghcr.io/ekarpov/voptrack-home:$version" > "$inspections/home-before.json"
docker image inspect "ghcr.io/ekarpov/voptrack-research:$version" > "$inspections/research-before.json"
read -r -p 'Private token file path: ' token_file
docker login ghcr.io --username ekarpov --password-stdin < "$token_file"
docker push "ghcr.io/ekarpov/voptrack-home:$version"
docker push "ghcr.io/ekarpov/voptrack-research:$version"
```

Push the exact tested local images; do not rebuild them during publication. In
GitHub's package settings, connect each package to this repository and set its
visibility to Public. Record each registry digest returned by the push. A registry
digest is not the local image ID; compare runtime contents after pulling it.
Keep version tags unchanged after release.

Prepare the registry bundles with those exact digests:

```sh
read -r -p 'Home digest, sha256:...: ' home_digest
read -r -p 'Research digest, sha256:...: ' research_digest
docker pull "ghcr.io/ekarpov/voptrack-home@$home_digest"
docker pull "ghcr.io/ekarpov/voptrack-research@$research_digest"
docker image inspect "ghcr.io/ekarpov/voptrack-home@$home_digest" > "$inspections/home-pulled.json"
docker image inspect "ghcr.io/ekarpov/voptrack-research@$research_digest" > "$inspections/research-pulled.json"
python3 release/recovery-set/image_identity.py "$inspections/home-before.json" "$inspections/home-pulled.json"
python3 release/recovery-set/image_identity.py "$inspections/research-before.json" "$inspections/research-pulled.json"
published=$(mktemp -d)
make distribution PRODUCT=home ARCHIVE=yes VERSION="$version" PLATFORM=linux/amd64 IMAGE="ghcr.io/ekarpov/voptrack-home@$home_digest" OUTPUT="$published/home"
make distribution PRODUCT=research ARCHIVE=yes VERSION="$version" PLATFORM=linux/amd64 IMAGE="ghcr.io/ekarpov/voptrack-research@$research_digest" OUTPUT="$published/research"
cat "$published/home/SHA256SUMS" "$published/research/SHA256SUMS" > "$published/SHA256SUMS"
```

The bundle builder checks source/version/product labels. Review the exact
inspections and license archive. Keep each file under GitHub's release-asset
size limit. Re-run the guide checks against the published digest-based downloads.

Create an annotated `v0.1.1` tag on the qualified source and a draft GitHub Release
only after owner approval. Attach the four archives and combined SHA256SUMS.
Include the public test report, exact source, Tessera pin, image digests, supported
platform, installation links and known limits. Publish the release after download
checks pass. Do not push historical development branches, tags or CI notes as
part of the public release. A website may link to the same GitHub files later;
website hosting is not required by either installation path.

## References and limits

[GitHub's container registry guide](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
describes package visibility, anonymous pulls and publishing credentials.
[GitHub Releases](https://docs.github.com/en/repositories/releasing-projects-on-github/about-releases)
provides tagged release notes and downloadable assets. Source and image publication
are separate from deploying servers. App-facing upgrades require app confirmation,
at least two weeks' notice, verified recovery material and release of any active
freeze. Frozen demos remain on rc.4.

Packaging tests use simulated image responses for refusal cases; they do not
prove a public registry or host can run the server. Actual-image and fresh-host
results must be recorded separately. Dependency notices in the image and bundle
must remain with redistributed binaries; see [third-party software](../../THIRD-PARTY.md).
