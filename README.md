## Hub CLI

Hub CLI is stack composition and lifecycle tool.

    $ hub elaborate hub.yaml params.yaml -o hub.yaml.elaborate
    $ hub deploy hub.yaml.elaborate -e NAME=stage
    $ hub version
    $ hub help

## Build

Use `make` to build Hub CLI:

    $ make

Or directly with `go`:

    $ go get github.com/agilestacks/hub/cmd/hub

## Clean up

    $ make clean

## Pre-built binaries

Install [Hub CLI](https://docs.agilestacks.com/article/zrban5vpb5-install-toolbox#hub_cli):

    curl -L https://github.com/agilestacks/hub/releases/download/v1.0.0/hub.linux_amd64 -o /usr/local/bin/hub
    chmod +x /usr/local/bin/hub

There are [Linux amd64](https://github.com/agilestacks/hub/releases/download/v1.0.0/hub.linux_amd64), [Linux arm64](https://github.com/agilestacks/hub/releases/download/v1.0.0/hub.linux_arm64), and [macOS amd64](https://github.com/agilestacks/hub/releases/download/v1.0.0/hub.darwin_amd64) binaries.

### Extensions

Optionally, install extensions:

    $ hub extensions install

Hub CLI Extensions require [AWS CLI] or [Azure CLI], [kubectl], [jq], [yq v4]. Optionally install [Node.js] and NPM for `hub pull` extension. Optionally install [eksctl] for `hub ext eks` extension.

Windows users please [read on](https://docs.agilestacks.com/article/u6a9cq5yya-hub-cli-on-windows).

### macOS users

Depending on your's machine Security & Privacy settings and macOS version (10.15+), you may get an error _cannot be opened because the developer cannot be verified_. Please [read on](https://github.com/hashicorp/terraform/issues/23033#issuecomment-542302933) for a simple workaround.

Alternatively, to set a global preference to _Allow apps downloaded from: Anywhere_, execute:

    $ sudo spctl --master-disable

### Beta binaries

We also publish latest binaries to the `controlplane.stage.agilestacks.io` for [Linux amd64](https://controlplane.stage.agilestacks.io/dist/hub-cli/hub.linux_amd64), [Linux arm64](https://controlplane.stage.agilestacks.io/dist/hub-cli/hub.linux_arm64), and [macOS amd64](https://controlplane.stage.agilestacks.io/dist/hub-cli/hub.darwin_amd64).

## What's next?

Deploy [App Stack](https://github.com/agilestacks/stack-app-eks) or [Machine Learning Stack](https://github.com/agilestacks/stack-ml-eks) on AWS EKS.

More information in the [wiki](https://github.com/agilestacks/hub/wiki) and [docs](https://docs.agilestacks.com).


[AWS CLI]: https://aws.amazon.com/cli/
[Azure CLI]: https://docs.microsoft.com/en-us/cli/azure/
[kubectl]: https://kubernetes.io/docs/reference/kubectl/overview/
[eksctl]: https://eksctl.io
[jq]: https://stedolan.github.io/jq/
[yq v4]: https://github.com/mikefarah/yq
[Node.js]: https://nodejs.org
