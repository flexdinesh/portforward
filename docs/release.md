# Releases

Stable releases are SemVer Git tags on `main`, published only by the manual
**Release** workflow. Every push to `main` runs lightweight CI; development
versions install directly from `main`.

## Stable releases

Required repository secret:

- `HOMEBREW_TAP_TOKEN`: fine-grained token with contents write and pull request write access to `flexdinesh/homebrew-tap`.

Add it under repository **Settings → Secrets and variables → Actions**. The
workflow checks that it exists before creating a tag or publishing artifacts.
GitHub supplies `GITHUB_TOKEN` automatically for this repository's release.

1. Merge release-ready code to `main`.
2. Run the **Release** workflow. It requires no inputs and checks out the latest `main` when the job starts.
3. The workflow selects the next patch version, verifies the repository, then publishes the tag and a stable GitHub Release marked **Latest** with GoReleaser.
4. It generates `Formula/portforward.rb` and opens or updates a pull request against `flexdinesh/homebrew-tap`.
5. Merge the tap pull request after its Homebrew checks pass.

Each release publishes `checksums.txt` and four archives, where `<version>` omits the leading `v`:

- `portforward_<version>_darwin_amd64.tar.gz`
- `portforward_<version>_darwin_arm64.tar.gz`
- `portforward_<version>_linux_amd64.tar.gz`
- `portforward_<version>_linux_arm64.tar.gz`

Each archive contains the native `portforward` binary and README.

The tap branch is deterministic per version, such as `portforward-v0.1.2`. Rerunning a release whose tag still points to current `main` reuses the published artifacts and updates the same branch and pull request. Published artifacts are not rebuilt or replaced. The workflow publishes the GitHub Release before updating the tap, so rerunning it can repair a failed tap update.

The tap repository owns Homebrew style, strict audit, install, and formula test checks before merge.

## Development releases

The **CI** workflow checks formatting and builds pushes to `main` and pull
requests in one read-only job. Full validation runs locally in the pre-push
hook; manual stable releases retain full validation.
Both workflows use the tools and tasks in `mise.toml`: routine CI runs `ci`,
and stable releases run `release:check` before the `release` publishing task.

Development installs use `main` directly, without a separate branch or
publishing job. They do not wait for CI or change the stable GitHub **Latest**
release or Go's `@latest`.

`go install github.com/flexdinesh/portforward/cmd/portforward@main` resolves the branch to a
Go pseudo-version, or a stable version if that commit also has a release tag.
Go module proxies may briefly cache branch lookups. To bypass that cache:

```bash
GOPROXY=direct go install github.com/flexdinesh/portforward/cmd/portforward@main
```

## Version series

`.release-version` contains the active `major.minor` release series. For example, `0.1` selects `v0.1.2` when `v0.1.1` is the latest release, then `v0.1.3`, and so on. A rerun from the same commit reuses its existing tag and release.

To begin a new minor or major series, change `.release-version` in the repo. Changing it to `0.2` makes the next release `v0.2.0`; changing it to `1.0` makes the next release `v1.0.0`. Later releases continue incrementing that series' patch number.

## Installing

```bash
# Latest release.
go install github.com/flexdinesh/portforward/cmd/portforward@latest

# Specific release.
go install github.com/flexdinesh/portforward/cmd/portforward@v0.1.0

# Latest development version from main.
go install github.com/flexdinesh/portforward/cmd/portforward@main
```

## Version Output

Go installs report the resolved module version: a stable tag or a development
pseudo-version. Downloaded release binaries get the version from the release
tag through GoReleaser linker flags.

```bash
portforward --version
```

Do not create a moving `latest` tag. Go already resolves `@latest` to the newest
SemVer tag.
