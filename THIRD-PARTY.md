# Third-party software

Voptrack Server is licensed under Apache-2.0; see LICENSE. Its images also contain
Tessera, rsc.io/qr, the Go runtime and other Go modules. Their original license
and notice files, module versions, Go version and go.sum are included inside each
image at `/usr/share/voptrack/licenses`. The matching download contains the same
files in `licenses.tar.gz`. These files keep each author's terms; the Voptrack
license does not replace them.

The runtime uses Alpine Linux and its packages, including Python, OpenSSL, curl,
CA certificates and time-zone data. The image's `alpine-packages.txt` records the
exact installed versions, license names and upstream URLs. Alpine publishes its
package source recipes at https://gitlab.alpinelinux.org/alpine/aports and its
packages at https://pkgs.alpinelinux.org/. Obtain matching package sources and
license texts from the recorded upstream projects when redistributing a changed
runtime. Caddy is a separate image; retain the exact Caddy image and its notices
with an offline download. Its source is https://github.com/caddyserver/caddy.

Builds do not pass repository credentials into image layers. The collected module
list and checksum file identify the public dependencies used by the build.
