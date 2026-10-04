# Spot Instance Advisor 使用说明

## 功能概述

Spot Instance Advisor 是一个用于分析阿里云 Spot 实例价格和可用性的工具。现在支持两种输出格式：表格格式和 JSON 格式。

## 命令行参数

### 基本参数

- `--accessKeyId`: 阿里云访问密钥 ID（或环境变量 `ALIYUN_ACCESS_KEY_ID` / `ALIBABA_CLOUD_ACCESS_KEY_ID`）
- `--accessKeySecret`: 阿里云访问密钥 Secret（或环境变量 `ALIYUN_ACCESS_KEY_SECRET` / `ALIBABA_CLOUD_ACCESS_KEY_SECRET`）
- `--securityToken`: STS 安全令牌，配合临时 AccessKey 使用（或环境变量 `ALIYUN_SECURITY_TOKEN` / `ALIBABA_CLOUD_SECURITY_TOKEN`）
- `--roleArn`: OIDC：要扮演的 RAM 角色 ARN（或环境变量 `ALIBABA_CLOUD_ROLE_ARN`）
- `--oidcProviderArn`: OIDC：RAM OIDC 身份提供商 ARN（或环境变量 `ALIBABA_CLOUD_OIDC_PROVIDER_ARN`）
- `--oidcTokenFile`: OIDC：OIDC ID Token 文件路径（或环境变量 `ALIBABA_CLOUD_OIDC_TOKEN_FILE`）
- `--roleSessionName`: OIDC：角色会话名（或环境变量 `ALIBABA_CLOUD_ROLE_SESSION_NAME`；默认 `spot-instance-advisor`）
- `--region`: 区域（默认：cn-hangzhou）

### 认证方式

支持三种凭证，原有的 AccessKey 用法保持不变：

1. **AccessKey（长期密钥）**：`--accessKeyId` + `--accessKeySecret`，或对应环境变量。
2. **STS Token（临时密钥）**：在 AccessKey 基础上再提供 `--securityToken`，或 `ALIBABA_CLOUD_ACCESS_KEY_ID` / `ALIBABA_CLOUD_ACCESS_KEY_SECRET` / `ALIBABA_CLOUD_SECURITY_TOKEN` 环境变量（`aliyun/configure-aliyun-credentials-action` 导出的就是这组变量）。环境变量里的 Token 只与同一来源的 AccessKey 配对：`ALIYUN_SECURITY_TOKEN` 对应 `ALIYUN_ACCESS_KEY_*`，`ALIBABA_CLOUD_SECURITY_TOKEN` 对应 `ALIBABA_CLOUD_ACCESS_KEY_*`；通过参数传入的 AccessKey 只会使用 `--securityToken`。
3. **OIDC（AssumeRoleWithOIDC）**：提供 `--roleArn` + `--oidcProviderArn` + `--oidcTokenFile`，或对应的 `ALIBABA_CLOUD_*` 环境变量（ACK RRSA 会自动注入这组变量）。工具用 OIDC Token 换取 RAM 角色的 STS 凭证，无需保存长期密钥。可通过 `ALIBABA_CLOUD_STS_REGION` / `ALIBABA_CLOUD_VPC_ENDPOINT_ENABLED` 指定 STS 地域或 VPC 接入点。

优先级：AccessKey 参数 > OIDC 参数 > AccessKey 环境变量（`ALIYUN_*` 优先于 `ALIBABA_CLOUD_*`）> OIDC 环境变量。同时传入 AccessKey 参数和 OIDC 参数会直接报错。GitHub Actions 的配置示例见 [README](README.md#github-actions-with-oidc)。

### 实例筛选参数

- `--mincpu`: 最小 CPU 核心数（默认：1）
- `--maxcpu`: 最大 CPU 核心数（默认：32）
- `--minmem`: 最小内存（GB）（默认：2）
- `--maxmem`: 最大内存（GB）（默认：64）
- `--family`: 实例族（例如：ecs.n1,ecs.n2）
- `--instanceType`: 指定实例类型（逗号分隔，例如：ecs.n1.small,ecs.n2.large）。如果指定此参数，将优先使用此参数，忽略 family 参数
- `--arch`: 架构过滤（x86_64 或 arm64）

### 分析参数

- `--cutoff`: 折扣阈值（默认：2）
- `--limit`: 结果数量限制（默认：20）
- `--resolution`: 价格历史分析窗口（天）（默认：7）

### 输出格式参数

- `--json`: 以 JSON 格式输出结果

## 使用示例

### 1. 基本使用（表格格式）

```bash
./spot-instance-advisor \
  --accessKeyId YOUR_ACCESS_KEY_ID \
  --accessKeySecret YOUR_ACCESS_KEY_SECRET \
  --region cn-hangzhou \
  --mincpu 2 \
  --maxcpu 8 \
  --minmem 4 \
  --maxmem 16 \
  --arch arm64
```

### 2. JSON 格式输出（纯 JSON，无摘要信息）

```bash
./spot-instance-advisor \
  --accessKeyId YOUR_ACCESS_KEY_ID \
  --accessKeySecret YOUR_ACCESS_KEY_SECRET \
  --region cn-hangzhou \
  --mincpu 2 \
  --maxcpu 8 \
  --minmem 4 \
  --maxmem 16 \
  --json
```

**注意**: 使用 `--json` 参数时，程序将：

- 只输出纯 JSON 结果，不显示任何摘要信息
- 错误时也以 JSON 格式输出错误信息
- 适合程序化处理和自动化脚本

### 3. 指定实例族

```bash
./spot-instance-advisor \
  --accessKeyId YOUR_ACCESS_KEY_ID \
  --accessKeySecret YOUR_ACCESS_KEY_SECRET \
  --family ecs.n1,ecs.n2 \
  --json
```

### 4. 指定具体实例类型

```bash
./spot-instance-advisor \
  --accessKeyId YOUR_ACCESS_KEY_ID \
  --accessKeySecret YOUR_ACCESS_KEY_SECRET \
  --instanceType ecs.n1.small,ecs.n2.large \
  --json
```

**注意**: 使用 `--instanceType` 参数时：

- 将优先使用指定的实例类型，忽略 `--family` 参数
- 支持逗号分隔的多个实例类型
- **会跳过所有其他过滤条件**（CPU、内存、架构等），直接使用指定的实例类型
- 如果指定的实例类型不存在，将被跳过

## JSON 输出格式

当使用 `--json` 参数时，输出将是格式化的 JSON 数组，每个元素包含以下字段：

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

### JSON 字段说明

- `instanceTypeId`: 实例类型 ID
- `zoneId`: 可用区 ID
- `pricePerCore`: 每核心价格
- `discount`: 折扣倍数
- `possibility`: 价格稳定性指标
- `cpuCoreCount`: CPU 核心数
- `memorySize`: 内存大小（GB）
- `instanceFamily`: 实例族
- `arch`: CPU 架构（x86_64 或 arm64）

## 构建

```bash
go build -o spot-instance-advisor .
```

## 依赖管理

项目使用 Go modules 进行依赖管理：

```bash
go mod tidy
go mod vendor
```

## JSON 错误输出格式

当使用 `--json` 参数且发生错误时，程序会输出 JSON 格式的错误信息：

```json
{
  "error": "错误类型",
  "message": "详细错误信息"
}
```

### 错误输出示例

```json
{
  "error": "Failed to initialize metastore",
  "message": "failed to DescribeInstanceTypes: SDK.ServerError\nErrorCode: InvalidAccessKeyId.NotFound\nMessage: Specified access key is not found."
}
```
