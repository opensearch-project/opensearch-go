- [Developer Guide](#developer-guide)
  - [Getting Started](#getting-started)
    - [Git Clone OpenSearch Go Client Repository](#git-clone-opensearch-go-client-repository)
    - [Install Prerequisites](#install-prerequisites)
      - [Go 1.24](#go-124)
      - [Docker](#docker)
      - [Windows](#windows)
    - [Unit Testing](#unit-testing)
    - [Integration Testing](#integration-testing)
    - [Composing an OpenSearch Docker Container](#composing-an-opensearch-docker-container)
      - [Execute integration tests from your terminal](#execute-integration-tests-from-your-terminal)
  - [Advanced Cluster Configuration](#advanced-cluster-configuration)
    - [Cluster Scaling](#cluster-scaling)
    - [Heterogeneous Clusters](#heterogeneous-clusters)
      - [CPU Limits](#cpu-limits)
      - [Node Roles](#node-roles)
      - [Combining Overrides](#combining-overrides)
      - [Resetting to Defaults](#resetting-to-defaults)
    - [Testing Specific OpenSearch Versions](#testing-specific-opensearch-versions)
    - [Cluster Status and Troubleshooting](#cluster-status-and-troubleshooting)
  - [Lint](#lint)
    - [Markdown lint](#markdown-lint)
    - [Go lint](#go-lint)
  - [Coverage](#coverage)
  - [Use an Editor](#use-an-editor)
    - [GoLand](#goland)
    - [Vim](#vim)

# Developer Guide

So you want to contribute code to the OpenSearch Go Client? Excellent! We're glad you're here. Here's what you need to do:

## Getting Started

### Git Clone OpenSearch Go Client Repository

Fork [opensearch-project/opensearch-go](https://github.com/opensearch-project/opensearch-go) and clone locally, e.g. `git clone https://github.com/[your username]/opensearch-go.git`.

### Install Prerequisites

#### Go 1.24

OpenSearch Go Client builds using [Go](https://go.dev/doc/install) 1.24 at a minimum.

#### Docker

[Docker](https://docs.docker.com/get-docker/) is required for building some OpenSearch artifacts and executing integration tests.

#### Windows

To build the project on Windows, use [WSL2](https://learn.microsoft.com/en-us/windows/wsl/install), the compatibility layer for running Linux applications.

Install `make`

```
sudo apt install make
```

### Unit Testing

Go has a simple tool for running tests, and we simplified it further by creating this make command:

```
make test-unit
```

Individual unit tests can be run with the following command:

```
cd folder-path/to/test;
go test -v -run TestName;
```

### Integration Testing

In order to test opensearch-go client, you need a running OpenSearch cluster. You can use Docker to accomplish this. The [Docker Compose file](.ci/opensearch/docker-compose.yml) supports the ability to run integration tests for the project in local environments. If you have not installed docker-compose, you can install it from this [link](https://docs.docker.com/compose/install/).

### Composing an OpenSearch Docker Container

Ensure that Docker is installed on your local machine. You can check by running `docker --version`. Next, navigate to your local opensearch-go repository. Run the following command to build and start the OpenSearch docker container.

```
make cluster.build cluster.start
```

This command will start the OpenSearch container using the `docker-compose.yaml` configuration file. During the build process, the necessary dependencies and files will be downloaded, which may take some time depending on your internet connection and system resources.

Once the container is built and running, you can open a web browser and navigate to localhost:9200 to access the OpenSearch docker container.

In order to differentiate unit tests from integration tests, Go has a built-in mechanism for allowing you to logically separate your tests with [build tags](https://pkg.go.dev/cmd/go#hdr-Build_constraints). The build tag needs to be placed as close to the top of the file as possible, and must have a blank line beneath it. Hence, create all integration tests with build tag 'integration'.

#### Execute integration tests from your terminal

1. Run below command to start containers. By default, it will launch latest OpenSearch cluster.
   ```
   make cluster.build cluster.start
   ```
2. Run all integration tests.
   ```
   make test-integ race=true
   ```
3. Stop and clean containers.
   ```
   make cluster.stop cluster.clean
   ```

## Advanced Cluster Configuration

By default, `make cluster.start` launches a 3-node cluster where every node has the same resources and roles (`cluster_manager,data,ingest`). The targets below let you customize the cluster for testing weighted round-robin routing, role-based request routing, and other behavior that only surfaces with non-uniform nodes.

### Cluster Scaling

Scale the running cluster to a different number of nodes without rebuilding:

```
make cluster.scale.1    # Single-node cluster
make cluster.scale.2    # 2-node cluster
make cluster.scale.3    # Full 3-node cluster (default)
```

### Heterogeneous Clusters

Override files let you change CPU limits and node roles independently. Each target writes a Docker Compose override file under `.ci/opensearch/` that is automatically merged with `docker-compose.yml` on the next `cluster.build` or `cluster.start`. The override files are not checked into source control.

The override file paths are:

| Override | File |
|----------|------|
| CPU limits | `.ci/opensearch/docker-compose.cpu-override.yml` |
| Node roles | `.ci/opensearch/docker-compose.roles-override.yml` |

These are standard Docker Compose files. You can hand-edit them for custom configurations (e.g., different CPU ratios or role combinations not covered by the Make targets), or remove individual files to selectively reset one dimension while keeping the other:

```
# Remove only the CPU override, keep roles
rm .ci/opensearch/docker-compose.cpu-override.yml

# Remove only the roles override, keep CPU limits
rm .ci/opensearch/docker-compose.roles-override.yml
```

#### CPU Limits

Set per-node CPU limits so the client's weighted round-robin allocates proportional traffic. The `deploy.resources.limits.cpus` value is reported by each node via `GET /_nodes/os` as `allocated_processors`, which the client uses to compute connection weights.

```
# Balanced weights [1,1,2]
make cluster.heterogeneous.cpu.1    # node1=2, node2=2, node3=4 CPUs

# Skewed weights [1,2,4]
make cluster.heterogeneous.cpu.2    # node1=1, node2=2, node3=4 CPUs
```

#### Node Roles

Assign different roles to each node so the client's role-based routing policy can direct requests to the correct nodes (e.g., bulk requests to ingest-capable nodes, search requests to data nodes):

```
make cluster.heterogeneous.roles    # node1=cluster_manager+ingest, node2=data+ingest, node3=data
```

#### Combining Overrides

CPU and role overrides are independent files and can be combined. Set both, then rebuild:

```
make cluster.heterogeneous.cpu.1 cluster.heterogeneous.roles
make cluster.stop cluster.clean cluster.build cluster.start
```

Verify the resulting configuration:

```
curl -sk 'https://admin:myStrongPassword123%21@localhost:9200/_nodes/http,os?pretty' \
  | jq '.nodes[] | {name, roles: .roles, processors: .os.allocated_processors}'
```

#### Resetting to Defaults

Remove all override files and return to the default homogeneous 3-node cluster:

```
make cluster.homogeneous
make cluster.stop cluster.clean cluster.build cluster.start
```

### Testing Specific OpenSearch Versions

The cluster supports any published OpenSearch Docker image version. Always clean before switching versions to avoid stale data or cached images:

```
make cluster.stop
OPENSEARCH_VERSION=2.19.1 make cluster.clean cluster.build cluster.start
make test-integ
```

Set `SECURE_INTEGRATION=false` to disable TLS and basic auth:

```
SECURE_INTEGRATION=false OPENSEARCH_VERSION=2.19.1 make cluster.clean cluster.build cluster.start
```

### Cluster Status and Troubleshooting

Use `make cluster.status` to display cluster health, node info, Docker container state, and index/shard details. It auto-detects whether the cluster is running in secure or insecure mode.

```
make cluster.status
```

## Lint

To keep all the code in a certain uniform format, it was decided to use some writing rules. If you wrote something wrong, it's okay, you can simply run the script to check the necessary files, and optionally format the content. But keep in mind that all these checks are repeated on the pipeline, so it's better to check locally.

### Markdown lint

To check the markdown files, run the following command:

```
make lint.markdown
```

### Go lint

To check all go files, run the following command:

```
make linters
```

## Coverage

To get the repository test coverage, run the following command:

For the results to be display in your terminal:

```
make coverage
```

For the results to be display in your browser:

```
make coverage-html
```

## Use an Editor

### GoLand

You can import the OpenSearch project into GoLand as follows:

1. Select **File | Open**
2. In the subsequent dialog navigate to the ~/go/src/opensearch-go and click **Open**

After you have opened your project, you need to specify the location of the Go SDK. You can either specify a local path to the SDK or download it. To set the Go SDK, navigate to **Go | GOROOT** and set accordingly.

### Vim

To improve your vim experience with Go, you might want to check out [fatih/vim-go](https://github.com/fatih/vim-go). For example it correctly formats the file and validates it on save.
