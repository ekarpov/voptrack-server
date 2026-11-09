# Voptrack Server

Run your own server for the [Voptrack app](https://voptrack.app/).

**Home** gives household members private stores and lets them choose what to
share. **Research** helps a researcher describe a study, invite participants,
collect consent, monitor collection and export data. Each runs with Docker Compose.
Home's website is optional; Research uses its website for information and joining.

Both Voptrack Server and [Tessera](https://github.com/ekarpov/tessera) are public
source projects. Tessera provides storage, accounts, sharing and sign-in;
Voptrack adds these products, their websites and the installation/recovery tools.
The source pins Tessera **v0.1.0**.

## Install

Choose one path. Each guide starts with an overview, then commands you can copy.

| Path | For a person | For an agent |
| --- | --- | --- |
| Clone the repository, build and run on your host | [Source installation](docs/install-source.md) | [Source runbook](docs/install-source-agent.md) |
| Use prepared Home or Research Docker images | [Image installation](docs/installation.md) | [Image runbook](docs/install-agent.md) |

The first Voptrack v0.1.0 images and downloads are being prepared. Check
[GitHub Releases](https://github.com/ekarpov/voptrack-server/releases) for published
files before following the image path. No example download is a promise that it
is already available. [Release preparation](docs/releases/v0.1.0.md) records what
has been tested and what remains. [GitHub delivery](release/distribution/README.md)
explains how images and downloads are prepared and published.

Use a fresh Linux x86-64 host with Docker, encrypted storage, room for data and
backups, and a reachable address. Use a domain for public HTTPS or an IP with a local
certificate for Home or a private Research group. Use a public domain for open
Research recruitment. [Host setup](docs/install/host.md) and the [IP and phone
trust guide](docs/install/local-home.md) cover both paths. Choose the product and website mode before
first start. The first release does not accept old alpha or release-candidate data.

## Using it

Home opens on **Home**. Members see shared tracks, Spending and Browse records;
the owner uses **Manage Home** for invitations and backups.
[Home guide](release/household/README.md).

Research opens on **Studies**. A study has Overview, Participants, Exports and
Study settings. It shows collection counts and dates; authorized CSV/JSON exports
contain recorded values. Operations are under **Server and backups**.
[Research guide](release/research/README.md).
Neither first-release website offers Saved views.

The website acts with each signed-in person's rights. Host administrators can
read the underlying files; application privacy does not hide data from the person
controlling the host. Keep backups and matching recovery material away from the
server, with the backup key separately held. Test recovery before relying on it.
[Operations and recovery](release/README.md).

## Develop

For build tools and disposable test servers, see [Development](docs/development.md).
Client integration uses port 8081 and a token file; keep credentials private.
[Documentation](docs/README.md) covers architecture, vocabulary, integration and
checks. Agents changing this repository start with [AGENTS.md](AGENTS.md).

## License

[Apache-2.0](LICENSE). Images include dependency notices; see
[third-party software](THIRD-PARTY.md).
