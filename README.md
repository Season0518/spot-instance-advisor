# Spot Instance Advisor

[![Go Version](https://img.shields.io/badge/Go-1.25.2-blue.svg)](https://golang.org/)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go Modules](https://img.shields.io/badge/Go%20Modules-Enabled-blue.svg)](go.mod)

A command-line tool for analyzing Alibaba Cloud Spot instance prices and availability. This tool helps you find the most cost-effective Spot instances based on historical price data and availability patterns.

## Features

- 🔍 **Instance Filtering**: Filter instances by CPU, memory, and instance family
- 📊 **Price Analysis**: Analyze historical Spot prices with customizable time windows
- 💰 **Cost Optimization**: Find instances with the best price-to-performance ratios
- 📈 **Availability Insights**: Get insights into instance availability patterns
- 🎯 **Multiple Output Formats**: Support for both human-readable tables and JSON output
- ⚡ **Fast & Efficient**: Optimized for quick analysis of large instance catalogs

## Quick Start

### Prerequisites

- Go 1.25.2 or later
- Alibaba Cloud account with ECS API access

### Installation

```bash
# Clone the repository
git clone <repository-url>
cd spot-instance-advisor

# Build the binary (outputs to dist/spot-instance-advisor-OS-ARCH)
make build

# Or build directly with Go (not recommended for releases)
go build -o dist/spot-instance-advisor-$(go env GOOS)-$(go env GOARCH) .
```

### Basic Usage

```bash
# Basic usage with table output
./spot-instance-advisor \
  --accessKeyId YOUR_ACCESS_KEY_ID \
  --accessKeySecret YOUR_ACCESS_KEY_SECRET \
  --region cn-hangzhou

# JSON output for programmatic use
./spot-instance-advisor \
  --accessKeyId YOUR_ACCESS_KEY_ID \
  --accessKeySecret YOUR_ACCESS_KEY_SECRET \
  --region cn-hangzhou \
  --json
```

## Command Line Options

### Authentication

- `--accessKeyId`: Your Alibaba Cloud Access Key ID (or `ALIYUN_ACCESS_KEY_ID` / `ALIBABA_CLOUD_ACCESS_KEY_ID`)
- `--accessKeySecret`: Your Alibaba Cloud Access Key Secret (or `ALIYUN_ACCESS_KEY_SECRET` / `ALIBABA_CLOUD_ACCESS_KEY_SECRET`)
- `--securityToken`: STS security token, for temporary AccessKeys (or `ALIYUN_SECURITY_TOKEN` / `ALIBABA_CLOUD_SECURITY_TOKEN`)
- `--roleArn`: OIDC: RAM role ARN to assume (or `ALIBABA_CLOUD_ROLE_ARN`)
- `--oidcProviderArn`: OIDC: RAM OIDC identity provider ARN (or `ALIBABA_CLOUD_OIDC_PROVIDER_ARN`)
- `--oidcTokenFile`: OIDC: path to a file holding the OIDC ID token (or `ALIBABA_CLOUD_OIDC_TOKEN_FILE`)
- `--roleSessionName`: OIDC: session name of the assumed role (or `ALIBABA_CLOUD_ROLE_SESSION_NAME`; default `spot-instance-advisor`)
- `--region`: Target region (default: cn-hangzhou)

See [Authentication Methods](#authentication-methods) for how these combine.

### Instance Filtering

- `--mincpu`: Minimum CPU cores (default: 1)
- `--maxcpu`: Maximum CPU cores (default: 32)
- `--minmem`: Minimum memory in GB (default: 2)
- `--maxmem`: Maximum memory in GB (default: 64)
- `--family`: Instance family filter (e.g., ecs.n1,ecs.n2)
- `--instanceType`: Specific instance types (comma-separated, e.g., ecs.n1.small,ecs.n2.large). Takes precedence over family parameter.
- `--arch`: CPU architecture filter: x86_64 or arm64

### Analysis Parameters

- `--cutoff`: Discount threshold (default: 2)
- `--limit`: Maximum number of results (default: 20)
- `--resolution`: Price history analysis window in days (default: 7)

### Output Format

- `--json`: Output results in JSON format

## Authentication Methods

Three credential types are supported. The AccessKey usage is unchanged, so existing scripts keep working.

### AccessKey (long-term)

```bash
./spot-instance-advisor --accessKeyId YOUR_KEY --accessKeySecret YOUR_SECRET

# or keep the secret off the command line
export ALIYUN_ACCESS_KEY_ID=YOUR_KEY
export ALIYUN_ACCESS_KEY_SECRET=YOUR_SECRET
./spot-instance-advisor
```

### STS token (temporary AccessKey)

Add the security token that came with the temporary key:

```bash
./spot-instance-advisor \
  --accessKeyId STS.xxx \
  --accessKeySecret xxx \
  --securityToken xxx

# or
export ALIBABA_CLOUD_ACCESS_KEY_ID=STS.xxx
export ALIBABA_CLOUD_ACCESS_KEY_SECRET=xxx
export ALIBABA_CLOUD_SECURITY_TOKEN=xxx
./spot-instance-advisor
```

A security token from the environment is only used with the AccessKey from the same source: `ALIYUN_SECURITY_TOKEN` with `ALIYUN_ACCESS_KEY_*`, and `ALIBABA_CLOUD_SECURITY_TOKEN` with `ALIBABA_CLOUD_ACCESS_KEY_*`. An AccessKey passed as flags only uses `--securityToken`. This way, a long-term key never gets paired with a token that an earlier CI step exported.

### OIDC (AssumeRoleWithOIDC)

The tool exchanges an OIDC ID token for STS credentials of a RAM role, so no long-lived secret is stored anywhere. It needs the role ARN, the RAM OIDC identity provider ARN, and a file containing the ID token:

```bash
./spot-instance-advisor \
  --roleArn acs:ram::<account-id>:role/<role-name> \
  --oidcProviderArn acs:ram::<account-id>:oidc-provider/<provider-name> \
  --oidcTokenFile /path/to/oidc-token
```

The same settings can come from the standard `ALIBABA_CLOUD_ROLE_ARN`, `ALIBABA_CLOUD_OIDC_PROVIDER_ARN` and `ALIBABA_CLOUD_OIDC_TOKEN_FILE` env vars. Those are the variables ACK RRSA injects into pods, so the tool works there with no flags. The STS endpoint defaults to `sts.aliyuncs.com`. Set `ALIBABA_CLOUD_STS_REGION` to use a regional endpoint, and `ALIBABA_CLOUD_VPC_ENDPOINT_ENABLED=true` for its VPC variant.

### Credential precedence

1. `--accessKeyId` / `--accessKeySecret` flags (AccessKey, or STS with `--securityToken`)
2. `--roleArn` / `--oidcProviderArn` / `--oidcTokenFile` flags (OIDC; a field not given as a flag falls back to its env var)
3. `ALIYUN_ACCESS_KEY_*`, then `ALIBABA_CLOUD_ACCESS_KEY_*` env vars (AccessKey, or STS with the matching security token)
4. `ALIBABA_CLOUD_ROLE_ARN` / `ALIBABA_CLOUD_OIDC_PROVIDER_ARN` / `ALIBABA_CLOUD_OIDC_TOKEN_FILE` env vars (OIDC)

Combining AccessKey flags with OIDC flags is rejected as ambiguous.

### GitHub Actions with OIDC

For example, this lets the job that provisions a spot CI runner pick an instance type without storing an AccessKey in repository secrets. First, create a RAM OIDC identity provider for `https://token.actions.githubusercontent.com` and a RAM role that trusts it. The role needs read access to `ecs:DescribeInstanceTypes`, `ecs:DescribeAvailableResource` and `ecs:DescribeSpotPriceHistory`.

Option A: let [`aliyun/configure-aliyun-credentials-action`](https://github.com/aliyun/configure-aliyun-credentials-action) do the exchange. It exports `ALIBABA_CLOUD_ACCESS_KEY_ID` / `ALIBABA_CLOUD_ACCESS_KEY_SECRET` / `ALIBABA_CLOUD_SECURITY_TOKEN`, which the tool picks up as STS credentials:

```yaml
permissions:
  id-token: write
  contents: read

steps:
  - uses: aliyun/configure-aliyun-credentials-action@v1
    with:
      role-to-assume: acs:ram::<account-id>:role/<role-name>
      oidc-provider-arn: acs:ram::<account-id>:oidc-provider/<provider-name>
  - run: ./spot-instance-advisor --region cn-hangzhou --mincpu 4 --maxcpu 8 --json
```

Option B: have the tool do the exchange itself, from a token file:

```yaml
permissions:
  id-token: write
  contents: read

steps:
  - name: Fetch GitHub OIDC token
    run: |
      curl -sSf -H "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
        "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=sts.aliyuncs.com" | jq -r .value > "$RUNNER_TEMP/oidc-token"
  - run: ./spot-instance-advisor --region cn-hangzhou --mincpu 4 --maxcpu 8 --json
    env:
      ALIBABA_CLOUD_ROLE_ARN: acs:ram::<account-id>:role/<role-name>
      ALIBABA_CLOUD_OIDC_PROVIDER_ARN: acs:ram::<account-id>:oidc-provider/<provider-name>
      ALIBABA_CLOUD_OIDC_TOKEN_FILE: ${{ runner.temp }}/oidc-token
```

The `audience` must match a Client ID configured on the RAM OIDC identity provider. GitHub ID tokens are short-lived, so fetch the token right before running the tool.

## Development

### Project Structure

```tree
spot-instance-advisor/
├── main.go          # Main application entry point
├── credentials.go   # Credential selection (AccessKey / STS / OIDC)
├── meta.go          # Metadata and instance management
├── sort.go          # Price analysis and sorting logic
├── go.mod           # Go module dependencies
├── go.sum           # Dependency checksums
├── Makefile         # Build automation
└── README.md        # This file
```

### Building and Testing

```bash
# Install dependencies
make deps

# Run tests
make test

# Build the application
make build

# Build for Linux
make build-linux

# Run with coverage
make test-coverage

# Clean build artifacts
make clean

# Update dependencies
make deps-update
```

### Dependencies

This project uses modern Go modules for dependency management:

- **github.com/aliyun/alibaba-cloud-sdk-go**: Alibaba Cloud SDK for Go
- **github.com/fatih/color**: Terminal color output
- **github.com/sirupsen/logrus**: Structured logging

## JSON Output Format

When using the `--json` flag, the tool outputs structured JSON data:

```json
[
  {
    "instanceTypeId": "ecs.n1.small",
    "zoneId": "cn-hangzhou-a",
    "pricePerCore": 0.1234,
    "discount": 2.5,
    "possibility": 0.8,
    "cpuCoreCount": 1,
    "memorySize": 2.0,
    "instanceFamily": "ecs.n1",
    "arch": "x86_64"
  }
]
```

### JSON Field Descriptions

- `instanceTypeId`: Instance type identifier
- `zoneId`: Availability zone identifier
- `pricePerCore`: Price per CPU core
- `discount`: Discount multiplier compared to on-demand pricing
- `possibility`: Price stability indicator
- `cpuCoreCount`: Number of CPU cores
- `memorySize`: Memory size in GB
- `instanceFamily`: Instance family identifier
- `arch`: CPU architecture (x86_64 or arm64)

## Examples

### Find Small Instances with Good Discounts

```bash
./spot-instance-advisor \
  --accessKeyId YOUR_KEY \
  --accessKeySecret YOUR_SECRET \
  --mincpu 1 \
  --maxcpu 4 \
  --minmem 2 \
  --maxmem 8 \
  --cutoff 3 \
  --json
```

### Analyze Specific Instance Family

```bash
./spot-instance-advisor \
  --accessKeyId YOUR_KEY \
  --accessKeySecret YOUR_SECRET \
  --family ecs.n1,ecs.n2 \
  --arch x86_64 \
  --limit 10 \
  --json
```

### Analyze Specific Instance Types

```bash
./spot-instance-advisor \
  --accessKeyId YOUR_KEY \
  --accessKeySecret YOUR_SECRET \
  --instanceType ecs.n1.small,ecs.n2.large \
  --arch x86_64 \
  --limit 10 \
  --json
```

**Note**: When using `--instanceType`:

- The specified instance types take precedence over `--family` parameter
- Supports comma-separated multiple instance types
- **All other filters (CPU, memory, architecture) will be skipped**, directly using the specified instance types
- Invalid or non-existent instance types will be skipped

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

This project is licensed under the Apache License, Version 2.0 - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- Alibaba Cloud for providing the ECS API
- The Go community for excellent tooling and libraries
