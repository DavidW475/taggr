# taggr

taggr creates semantic version tags on git hosting platforms. It reads the highest
version tag a repository already has, asks a bump source how large the next
increment should be, and creates the resulting tag on the target commit.

Platforms and bump sources are independent of each other, so the same versioning
rules apply wherever a repository is hosted.

| | Available |
|---|---|
| Platforms | `azuredevops` |
| Bump sources | `pr-labels`, `conventional-commits` |

## Install

```sh
go install github.com/DavidW475/taggr@latest
```

## Usage

```sh
taggr next               # print the version that would be released next
taggr next --tag         # the same, including the tag prefix: v1.5.0
taggr tag --dry-run      # show the full plan without changing anything
taggr tag                # create the tag
```

Inside a pipeline taggr needs no arguments: repository, commit and pull request
are read from the variables the CI system sets.

```
$ taggr tag --dry-run
platform    azuredevops (bump from pr-labels)
repository  Payments/checkout
commit      4f2a91cd0123456789abcdef0123456789abcdef
current     v1.4.2
bump        minor — label "feature" on pull request 1421 requests a minor bump
next        v1.5.0

dry run: tag v1.5.0 was not created
```

`--output json` prints the same plan machine-readably, including the current
version, the bump and the reason it was chosen.

## Configuration

Settings come from flags, from environment variables prefixed with `TAGGR_`, and
from a config file (`.taggr.yaml` in the current or home directory). Flags win
over environment variables, which win over the config file.

```yaml
platform: azuredevops
source: pr-labels

platforms:
  azuredevops:
    org_url: https://dev.azure.com/contoso
    project: Payments
    # Better supplied as TAGGR_PLATFORMS_AZUREDEVOPS_TOKEN, AZURE_DEVOPS_EXT_PAT
    # or SYSTEM_ACCESSTOKEN than written into the file.
    token: ""

sources:
  pr-labels:
    default_bump: patch
    labels:
      major: [major, breaking, breaking-change, semver:major]
      minor: [minor, feature, enhancement, semver:minor]
      patch: [patch, fix, bugfix, semver:patch]
      none: [no-release, skip-release, semver:none]

  conventional-commits:
    default_bump: none
    strip_pattern: '^Merged PR \d+: '
    types:
      minor: [feat]
      patch: [fix, perf]
      none: [chore, docs, style, refactor, test, build, ci, revert]

tag:
  prefix: v
  annotated: true
  initial_version: 0.1.0
```

Every key can be set through the environment instead; dots and dashes become
underscores:

```sh
export TAGGR_PLATFORMS_AZUREDEVOPS_ORG_URL=https://dev.azure.com/contoso
export TAGGR_SOURCES_PR_LABELS_DEFAULT_BUMP=minor
```

Select the source with `--source` or the `source` key.

### Bump from pull request labels (`pr-labels`)

The largest label wins, so a pull request labelled `fix` and `breaking-change`
produces a major bump. A label from the `none` list suppresses the release
entirely, whatever else is set. A pull request without any matching label falls
back to `default_bump`.

The labels are read from the pull request the release comes from. During a pull
request build that is the current pull request; on the branch build after the
merge taggr looks up the pull request the commit was merged by, so the labels
still decide the version.

### Bump from commit messages (`conventional-commits`)

Reads the commits between the previous version tag and the commit being released
and applies the [Conventional Commits](https://www.conventionalcommits.org)
rules:

| Commit | Bump |
|---|---|
| `feat: add pagination` | minor |
| `fix: correct the rounding` | patch |
| `feat(api)!: drop the v1 endpoint` | major |
| `fix: rework`, with a `BREAKING CHANGE:` footer | major |
| `chore: bump dependencies` | none |
| `Update readme` | ignored, does not follow the format |

The largest bump across the range wins, and a breaking change is always major, no
matter which type carries it. Commits that do not follow the format are ignored,
so the source also works in a repository that adopted the convention only
recently. When no commit requests a release, `default_bump` decides — with the
default `none` that means no tag, which is the behaviour the specification
intends. An empty range never releases, whatever the default says.

`strip_pattern` unwraps a subject before it is parsed. Its default handles Azure
DevOps squash merges, where the message reads `Merged PR 1421: feat: add
pagination`. Set it to an empty string to switch the unwrapping off.

## Azure DevOps

The token needs the **Code (read & write)** scope to create tags; **Code (read)**
is enough for `taggr next` and `taggr tag --dry-run`. In a pipeline the job's own
token works, provided it is mapped into the step:

```yaml
- script: |
    taggr tag
  displayName: Tag the release
  env:
    SYSTEM_ACCESSTOKEN: $(System.AccessToken)
    TAGGR_PLATFORMS_AZUREDEVOPS_ORG_URL: $(System.CollectionUri)
```

Repository, commit and pull request are taken from `SYSTEM_TEAMPROJECT`,
`BUILD_REPOSITORY_NAME`, `BUILD_SOURCEVERSION` and
`SYSTEM_PULLREQUEST_PULLREQUESTID`.

Capturing the version before building artefacts:

```yaml
- script: echo "##vso[task.setvariable variable=version]$(taggr next)"
  displayName: Resolve the version
```

## Adding a platform or a bump source

Both are plugin points behind an interface.

A **platform** implements `platform.Platform` — list tags, create a tag, resolve a
commit — and registers itself from an `init` function. Anything beyond that
minimum is an optional interface a caller discovers with a type assertion:

| Interface | Purpose |
|---|---|
| `platform.LabelReader` | read pull request labels, needed by `pr-labels` |
| `platform.CommitReader` | list the commits of a range, needed by `conventional-commits` |
| `platform.PullRequestResolver` | find the pull request a commit was merged by |
| `platform.EnvironmentDetector` | infer repository, commit and pull request from the CI environment |

A **bump source** implements `source.Source` and receives the active platform in
its request, so it asks for the capability it needs instead of talking to one
platform's API. That is why `pr-labels` works on every platform that can read
labels, and `conventional-commits` on every platform that can list commits.

New implementations become selectable by adding a blank import to
`internal/plugins`; nothing else in the program needs to know about them.

## Tests

```sh
go test ./...
```

Unit tests cover the version rules, the bump sources and the Azure DevOps client
against a fake of the SDK's git client.

`internal/integration` runs the whole program end to end: the real command tree
resolves its settings, opens the registered platform and bump source, and the
official Azure DevOps SDK talks HTTP to a server that emulates the API — endpoint
discovery, authentication, paging and all. Only the network is replaced, so these
tests cover what unit tests cannot: flag and configuration precedence, the wiring
between planner, platform and source, the payloads the SDK produces, and the
output a pipeline consumes. No credentials and no live organisation are needed.

## Layout

```
cmd/                          cobra commands: root, tag, next
internal/version/             semantic versions: parse, compare, bump
internal/platform/            platform interfaces and registry
internal/platform/ado/        Azure DevOps, on the official Azure DevOps Go SDK
internal/source/              bump source interfaces and registry
internal/source/prlabels/     bump from pull request labels
internal/source/conventional/ bump from conventional commit messages
internal/release/             planner: current version + bump -> next tag
internal/config/              namespaced view of the configuration
internal/plugins/             wires implementations into their registries
internal/integration/         end-to-end tests against a fake Azure DevOps API
```
