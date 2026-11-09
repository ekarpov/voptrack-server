.PHONY: image-selection-check public-check
.PHONY: backup-recovery-completion-check recovery-assurance-check home-setup-check off-machine-backups-check guided-recovery-check recovery-set-check kit-check home-dev-links home-app-dev-links ci ci-remote release-image image home-app-dev home-app-dev-token home-app-dev-down home-quota-dev home-quota-dev-token home-quota-dev-down check recovery-check home-app-check home-dev home-dev-token home-dev-down private-console-check research-dev research-dev-token research-dev-down catalog-check backup-check dev dev-content dev-reset serve version upgrade \
	scenario-single-user-up scenario-single-user-verify scenario-single-user-down \
	scenario-multi-user-up scenario-multi-user-add-user scenario-multi-user-remove-user scenario-multi-user-verify scenario-multi-user-down \
	try try-down \
	scenario-household-up scenario-household-add-member scenario-household-verify scenario-household-down

# Voptrack runs Tessera. The server is github.com/ekarpov/tessera, pinned in
# go.mod as a tool dependency, so every target below runs exactly the version
# this repository declares rather than whatever is on the machine.

# What this repository can verify on its own: the pinned server builds and
# reports the version we asked for, and the vocabulary stays append-only.
# The protocol suites live with the protocol and run in tessera.
# The deployment image, built without any key: modules come from this
# machine's Go cache through a local proxy (deploy/modproxy.sh). Removes the
# proxy afterwards; it is a copy of what the cache already holds.
BUILD_IMAGE ?= $(if $(IMAGE),$(IMAGE),voptrack-server)

image:
	@proxy=$$(mktemp -d) && trap 'rm -rf "$$proxy"' EXIT && \
	  ./deploy/modproxy.sh "$$proxy/modules" && \
	  DOCKER_BUILDKIT=1 docker build --build-context modproxy="$$proxy/modules" \
	    -f deploy/Containerfile -t $(BUILD_IMAGE) . && \
	  docker run --rm --network none --tmpfs /var/lib/tessera --entrypoint /usr/local/bin/tesserad $(BUILD_IMAGE) -version

recovery-assurance-check:
	bash ./release/recovery-assurance/verify.sh

# Local three-mode Home/Research completion and capacity/outage matrix.
backup-recovery-completion-check:
	python3 ./release/recovery-assurance/complete.py "$${VOPTRACK_IMAGE:?select immutable image}" "$${VOPTRACK_ASSURANCE_DIR:?fresh evidence root}"

home-setup-check:
	bash ./release/home-setup/verify.sh

off-machine-backups-check:
	bash ./release/off-machine/verify.sh

guided-recovery-check:
	bash ./release/guided-recovery/verify.sh

recovery-set-check:
	bash ./release/recovery-set/verify.sh

kit-check:
	bash ./release/kit/verify.sh

check:
	python3 -m unittest discover -s ci -p 'test_distribution.py'
	python3 -m unittest discover -s ci -p 'test_ci_*.py'
	python3 -m unittest discover -s deploy -p 'test_*.py'
	python3 -m unittest discover -s specs -p 'test_*.py'
	python3 -m unittest discover -s release/recovery-set -p 'test_*.py'
	python3 -m unittest discover -s release/recovery-assurance -p 'test_*.py'
	go tool tesserad -version
	python3 specs/check-vocabulary.py --history
	go vet ./catalogue ./cmd/console ./cmd/deployctl ./internal/... ./cmd/backupcollector ./cmd/recoverykit ./cmd/recoveryset ./cmd/recovery ./cmd/backupreplicator ./cmd/homesetup
	go test ./catalogue ./cmd/console ./cmd/deployctl ./internal/... ./cmd/backupcollector ./cmd/recoverykit ./cmd/recoveryset ./cmd/recovery ./cmd/backupreplicator ./cmd/homesetup

# Prove a backup can be restored, which is the only thing that makes it one.
# Native rather than containerised: these are tesserad subcommands and need no
# proxy or image, and this runs the same binary the systemd path runs.
backup-check:
	./deploy/verify-backup.sh

# The PostgreSQL path, which deploy/ advertises and which had been stood up
# once, by hand. The server runs natively and only the database is
# containerised, so the thing under test stays separate from the thing
# supporting it. Runs the whole conformance suite on that engine and the backup
# cycle with it, including that a restore needs a DISPOSABLE database rather
# than merely a fresh one.
postgres-check:
	./deploy/verify-postgres.sh

# Parse poisoned Compose defaults without creating containers.
image-selection-check:
	python3 -m unittest discover -s ci -p 'test_image_selection.py'

# Deliberately fails until the owner resolves the publication inventory.
# Do not make it an ordinary check gate before those decisions.
public-check:
	python3 specs/check-public.py $(if $(PUBLIC_REF),--ref "$(PUBLIC_REF)",)

# The release delivery: release/compose.yaml started from a directory holding
# nothing else. Needs the image built. Removes everything it creates, volumes
# included.
release-check:
	./release/verify.sh

# The household variant, built through /admin exactly as its README tells an
# owner to: personal stores private, Family shared, the variant fixed at first
# start. Needs the image built. Removes everything it creates.
household-check:
	./release/household/verify.sh

# The Console's Home dashboards, end to end: a household built through /admin,
# members sharing one track with Home and not another, Sara signing in through
# Tessera's own consent, and every number checked exactly. Needs the image
# built. Removes everything it creates.
console-check:
	./release/console/verify.sh

# Voptrack Research's study pages: a researcher publishes a study in the
# Console, the public reads it, consent is versioned. Needs the image built.
# Home rebuilt on a new machine from the recovery kit and a backup.
recovery-check:
	./release/recovery/verify.sh

# Home without its web interface: the app does everything.
home-app-check:
	./release/home-app/verify.sh

# The Console kept on the home network: the public address serves Tessera only.
private-console-check:
	./release/private-console/verify.sh

research-check:
	./release/research/verify.sh

# The bundled track catalog, against the protocol's identifier and type rules and
# against the vocabulary. Deliberately not part of `check`: the catalog is still a
# sketch and reports the open questions its own `open` list records, so folding it
# in would leave the main gate permanently red and teach everyone to ignore it.
# It becomes part of `check` when the catalog is generated rather than sketched.
catalog-check:
	python3 specs/check-tracks.py

# The single-user scenario: Caddy for public TLS, the pinned server behind it.
# Needs VOPTRACK_STORAGE_ENCRYPTED=yes, which is your attestation that the
# volume is encrypted -- the server refuses to start without it and the flag
# encrypts nothing by itself.
scenario-single-user-up:
	./scenarios/single-user/scenario.sh up

# The reason a scenario is worth anything: Tessera's own conformance suite,
# against the running deployment, over its real URL, with TLS verified.
# Starting is not evidence.
scenario-single-user-verify:
	./scenarios/single-user/scenario.sh verify

scenario-single-user-down:
	./scenarios/single-user/scenario.sh down

# Many people, one server, one dataset each, nobody sharing. The ordinary
# self-hosted shape. One step -- creating a dataset for a new person -- is
# blocked on a Tessera change that is implemented and unreleased.
scenario-multi-user-up:
	./scenarios/multi-user/scenario.sh up

# make scenario-multi-user-add-user NAME=alice
scenario-multi-user-add-user:
	./scenarios/multi-user/scenario.sh add-user $(NAME)

# Removing somebody takes two actions in two places; neither alone works.
# make scenario-multi-user-remove-user NAME=alice
scenario-multi-user-remove-user:
	./scenarios/multi-user/scenario.sh remove-user $(NAME)

# Conformance, the administration surface, and isolation -- that one person
# cannot learn another's dataset exists, which no protocol suite covers.
scenario-multi-user-verify:
	./scenarios/multi-user/scenario.sh verify

scenario-multi-user-down:
	./scenarios/multi-user/scenario.sh down

# A household: one shared store everyone can read and write, and a personal
# store each. The shared half works; personal stores wait on the same Tessera
# change the multi-user scenario does.
scenario-household-up:
	./scenarios/household/scenario.sh up

# make scenario-household-add-member NAME=alice
scenario-household-add-member:
	./scenarios/household/scenario.sh add-member $(NAME)

# Conformance, administration, isolation, and that the shared store really is
# shared -- checked in both directions.
scenario-household-verify:
	./scenarios/household/scenario.sh verify

scenario-household-down:
	./scenarios/household/scenario.sh down

# A throwaway server with something in it, for deciding whether you want one.
# Not a deployment: localhost, a self-issued certificate, and try-down
# destroys the volume.
try:
	./scenarios/evaluation/scenario.sh up

try-down:
	./scenarios/evaluation/scenario.sh down

# Which Tessera the deployment is pinned to.
version:
	@go tool tesserad -version

# Move to a newer Tessera deliberately: make upgrade VERSION=v0.2.0
upgrade:
	@test -n "$(VERSION)" || (echo "usage: make upgrade VERSION=vX.Y.Z"; exit 1)
	go get -tool github.com/ekarpov/tessera/cmd/tesserad@$(VERSION)
	go tool tesserad -version

# Local CI, which replaced GitHub Actions on 2026-10-07: every gate against
# HEAD, the result recorded as a git note on the commit. See ci/run.sh.
ci:
	./ci/run.sh

# The same, on the shared Linux test host (ci/remote.sh, ci/shared-host.env).
ci-remote:
	./ci/remote.sh

# Build and keep the release image for the selected platform; PUSH=yes publishes it. Refuses a
# commit without a passing local CI run. make release-image IMAGE=reg/voptrack-server:v0.1.0
release-image:
	@test -n "$(IMAGE)" || (echo "usage: make release-image IMAGE=registry/voptrack-server:vX.Y.Z [PUSH=yes]"; exit 1)
	VOPTRACK_PRODUCT="$(or $(PRODUCT),standard)" ./ci/release-image.sh "$(IMAGE)" $(if $(filter yes,$(PUSH)),--push,)

# Prepare matched installation/archive downloads; never uploads website assets.
.PHONY: distribution distribution-check
distribution:
	python3 ci/distribution.py --image "$(IMAGE)" --version "$(VERSION)" --platform "$(PLATFORM)" --output "$(OUTPUT)" $(if $(filter yes,$(ARCHIVE)),--archive,) $(if $(PRODUCT),--product "$(PRODUCT)",) $(if $(filter yes,$(LOCAL)),--local,)

distribution-check:
	python3 -m unittest discover -s ci -p 'test_distribution.py'
	python3 -m unittest discover -s ci -p 'test_ci_*.py'

# Development server for the client. Synthetic data only; credentials are
# written to a private local file. Port 8081 is what the client's integration
# test expects by default.
dev:
	go tool tesserad -dev -listen 127.0.0.1:8081

# The same with attachments enabled, which the fixture needs for its media
# values and its thumbnail.
dev-content:
	go tool tesserad -dev -dev-content -listen 127.0.0.1:8081

# A Research server for the app's integration tests: the release stack on
# https://localhost:8444 with a synthetic study and one participant. Writes the
# CA and the participant's credentials to .voptrack-research-dev/. Build the
# image first (make image).
# A Home server for the app's integration tests: the release stack on
# https://localhost:8544 with a household seeded through the Console. Writes
# the CA and home.json to .voptrack-home-dev/. Build the image first.
# Home in the release shape: no web interface (VOPTRACK_CONSOLE=off), the same
# household seeded through Tessera alone, as the app does it. Beside home-dev,
# on its own port, network and files (.voptrack-home-app-dev/).
HOME_APP_DEV = VOPTRACK_HOME_DEV_CONSOLE=off VOPTRACK_HOME_DEV_PROJECT=voptrack-home-app-dev \
	VOPTRACK_HOME_DEV_STATE=$(CURDIR)/.voptrack-home-app-dev VOPTRACK_HOME_DEV_PORT=8564 \
	VOPTRACK_HOME_DEV_HTTP_PORT=8590 VOPTRACK_HOME_DEV_CONSOLE_PORT=9448 VOPTRACK_HOME_DEV_NET=10.117.52 \
	VOPTRACK_NAME="Home dev server, app only"
home-app-dev:
	$(HOME_APP_DEV) ./release/household/dev.sh up
home-app-dev-token:
	$(HOME_APP_DEV) ./release/household/dev.sh token
home-app-dev-down:
	$(HOME_APP_DEV) ./release/household/dev.sh down

# Home with a small storage quota (2 MiB), for the app's limited-storage row: a
# few records cross 80%, a few more get 507 dataset_full. Release shape, like
# home-app-dev, on its own port, network and files (.voptrack-home-quota-dev/).
HOME_QUOTA_DEV = VOPTRACK_HOME_DEV_CONSOLE=off VOPTRACK_HOME_DEV_PROJECT=voptrack-home-quota-dev \
	VOPTRACK_HOME_DEV_STATE=$(CURDIR)/.voptrack-home-quota-dev VOPTRACK_HOME_DEV_PORT=8574 \
	VOPTRACK_HOME_DEV_HTTP_PORT=8595 VOPTRACK_HOME_DEV_CONSOLE_PORT=9449 VOPTRACK_HOME_DEV_NET=10.117.53 \
	VOPTRACK_STORE_LIMIT_MB=2 VOPTRACK_NAME="Home dev server, 2 MiB quota"
home-quota-dev:
	$(HOME_QUOTA_DEV) ./release/household/dev.sh up
home-quota-dev-token:
	$(HOME_QUOTA_DEV) ./release/household/dev.sh token
home-quota-dev-down:
	$(HOME_QUOTA_DEV) ./release/household/dev.sh down

home-dev:
	./release/household/dev.sh up

# Fresh single-use join links in home.json: a member invitation and the owner's
# device link (which signs the owner's earlier devices out).
home-dev-links:
	./release/household/dev.sh links
home-app-dev-links:
	$(HOME_APP_DEV) ./release/household/dev.sh links
home-dev-token:
	./release/household/dev.sh token

home-dev-down:
	./release/household/dev.sh down

research-dev:
	./release/research/dev.sh up

research-dev-token:
	./release/research/dev.sh token

research-dev-down:
	./release/research/dev.sh down

# Stop the server first. Retires all development records and attachments, then
# seeds a replacement.
dev-reset:
	go tool tesserad -dev -dev-content -dev-reset

# The real deployment, over HTTPS, from deploy/server.json.
#
# That file is deliberately not in git: it names this deployment's issuer, TLS
# key and data directory, and a committed one is a configuration somebody runs
# unchanged. Copy the example and edit it.
serve:
	@test -f deploy/server.json || { 	  echo "deploy/server.json does not exist."; 	  echo "Create it from the example, then edit the issuer, TLS paths and data directory:"; 	  echo "    cp deploy/server.example.json deploy/server.json"; 	  echo "See docs/operating.md for the first run, including provisioning an administrator."; 	  exit 1; }
	go tool tesserad -config deploy/server.json

# Rich, additive synthetic demos for reviewing the Console. Existing app fixtures stay intact.
.PHONY: ui-dev-data ui-dev-data-check
ui-dev-data:
	python3 release/seed_ui.py

ui-dev-data-check:
	python3 release/seed_ui.py --verify

.PHONY: website-defaults-check
website-defaults-check:
	@test -n "$(VOPTRACK_IMAGE)" || { echo "Set VOPTRACK_IMAGE to a verified image ID."; exit 1; }
	VOPTRACK_IMAGE="$(VOPTRACK_IMAGE)" python3 release/console/website_defaults.py
