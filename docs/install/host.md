# Prepare a new Ubuntu host

These steps install Docker on a new Ubuntu 24.04 or 26.04 server. Docker runs
Voptrack and keeps its data across restarts. Use a machine you control, with
administrator access. For another Linux system, follow Docker's own guide.

## Check the host

Use a 64-bit x86 machine for the prepared images in this release. Arrange disk
encryption for Docker's data before storing real data. The Voptrack setting only
records your confirmation; it does not encrypt anything. No capacity benchmark
has been published. Leave room for data, backups and a source build if needed.

```sh
uname -m
cat /etc/os-release
df -h /
sudo ss -ltnp | head -30
```

If Docker already runs other services, keep them. Do not uninstall packages or
change its storage settings using a fresh-host recipe. Docker-published ports can
bypass common host firewall rules; use the host provider's firewall as well.

## Install Docker on a fresh Ubuntu host

Install the tools used to download and verify files.

```sh
sudo apt-get update
sudo apt-get install -y ca-certificates curl python3 openssl
```

Download Docker’s signing key so Ubuntu can check its packages.

```sh
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc
```

Register Docker’s package source for your Ubuntu version.

```sh
. /etc/os-release
printf 'Types: deb\nURIs: https://download.docker.com/linux/ubuntu\nSuites: %s\nComponents: stable\nSigned-By: /etc/apt/keyrings/docker.asc\n' "$VERSION_CODENAME" | sudo tee /etc/apt/sources.list.d/docker.sources >/dev/null
```

Install Docker and Compose from the new package source.

```sh
sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
```

Check the platform and Compose version.

```sh
sudo docker info --format '{{.OSType}}/{{.Architecture}}'
sudo docker compose version
```

Use Compose 2.40.3 or newer. Commands in the install guides use `sudo docker` so
you do not need to join a group with host-wide Docker access.

## Choose the address

For a public server, point the domain's A record at the host. If it has an AAAA
record, IPv6 must work too. Allow incoming TCP 80 and 443 at the provider and on
the host. These ports must be free.

For an installation without a domain, reserve a stable IP address the phones
can reach and use a local certificate. For open Research recruitment, use a
public domain so participants can open the site with ordinary browser trust.
See the [IP and phone trust guide](local-home.md).

Two products on one host need separate routing and network settings.

The default Docker network is `10.117.47.0/24`. Check it does not overlap your
host, local network or VPN. Before first start, choose a free subnet and set
`VOPTRACK_PROXY_SUBNET`, `VOPTRACK_PROXY_ADDRESS` and `VOPTRACK_PROXY_POOL` if needed.

These commands follow the [Docker Ubuntu guide](https://docs.docker.com/engine/install/ubuntu/).
They do not configure a domain, encrypt a disk or change the provider's firewall.
