package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials"
)

// Credential modes. buildCredentialsProvider maps each to the matching
// credentials provider in the Aliyun SDK.
const (
	credModeAccessKey = "access_key"    // long-term AccessKey pair
	credModeSTS       = "sts"           // temporary AccessKey pair + SecurityToken
	credModeOIDC      = "oidc_role_arn" // AssumeRoleWithOIDC (GitHub Actions, ACK RRSA, ...)
)

// defaultRoleSessionName is the RoleSessionName used when assuming a role via
// OIDC unless overridden. It shows up in ActionTrail, so it names the tool
// rather than the SDK's random "aliyun-go-sdk-<ts>" default.
const defaultRoleSessionName = "spot-instance-advisor"

// stsEndpointOverride replaces the STS endpoint the OIDC provider calls.
// Tests set it to point AssumeRoleWithOIDC at a local server; production
// leaves it empty so the SDK picks the endpoint (sts.aliyuncs.com, or the
// regional / VPC endpoint from ALIBABA_CLOUD_STS_REGION and
// ALIBABA_CLOUD_VPC_ENDPOINT_ENABLED).
var stsEndpointOverride string

// errMissingCredentials marks "no credentials configured at all", so main can
// keep the pre-STS/OIDC "Missing required parameters" error heading.
var errMissingCredentials = errors.New("accessKeyId and accessKeySecret are required (pass as flags or set ALIYUN_ACCESS_KEY_ID / ALIYUN_ACCESS_KEY_SECRET env vars), " +
	"or configure OIDC via --roleArn / --oidcProviderArn / --oidcTokenFile (or ALIBABA_CLOUD_ROLE_ARN / ALIBABA_CLOUD_OIDC_PROVIDER_ARN / ALIBABA_CLOUD_OIDC_TOKEN_FILE env vars)")

// credentialFlags holds the raw credential-related flag values.
type credentialFlags struct {
	accessKeyID     string
	accessKeySecret string
	securityToken   string
	roleArn         string
	oidcProviderArn string
	oidcTokenFile   string
	roleSessionName string
}

// credentialConfig is the resolved credential selection: mode plus the
// fields that mode needs.
type credentialConfig struct {
	mode            string
	accessKeyID     string
	accessKeySecret string
	securityToken   string
	roleArn         string
	oidcProviderArn string
	oidcTokenFile   string
	roleSessionName string
}

// resolveCredentialConfig picks the credential mode. The AccessKey lookup is
// exactly the pre-STS/OIDC behavior (resolveCredentials), so every existing
// AK invocation keeps working unchanged. The order is:
//
//  1. --accessKeyId / --accessKeySecret flags: AccessKey, or STS when a
//     security token is also set.
//  2. --roleArn / --oidcProviderArn / --oidcTokenFile flags: OIDC. A missing
//     field falls back to its ALIBABA_CLOUD_* env var.
//  3. ALIYUN_* then ALIBABA_CLOUD_* AccessKey env vars: AccessKey, or STS when
//     a security token is also set. Short-lived credentials exported by
//     aliyun/configure-aliyun-credentials-action land here.
//  4. ALIBABA_CLOUD_ROLE_ARN / ALIBABA_CLOUD_OIDC_PROVIDER_ARN /
//     ALIBABA_CLOUD_OIDC_TOKEN_FILE env vars: OIDC (e.g. ACK RRSA).
//
// Explicit flags therefore beat env vars, and among env vars an AccessKey
// beats OIDC, the same order the SDK's default credential chain uses.
func resolveCredentialConfig(f credentialFlags) (credentialConfig, error) {
	// --securityToken only makes sense with an AccessKey, so it counts as an
	// AccessKey flag: combined with OIDC flags it is a conflict, not ignored.
	akFlagSet := firstNonEmpty(f.accessKeyID, f.accessKeySecret, f.securityToken) != ""
	oidcFlagSet := firstNonEmpty(f.roleArn, f.oidcProviderArn, f.oidcTokenFile) != ""
	if akFlagSet && oidcFlagSet {
		return credentialConfig{}, fmt.Errorf("--accessKeyId/--accessKeySecret/--securityToken cannot be combined with --roleArn/--oidcProviderArn/--oidcTokenFile; pick one credential type")
	}

	if !oidcFlagSet {
		id, secret := resolveCredentials(f.accessKeyID, f.accessKeySecret)
		if id != "" && secret != "" {
			cfg := credentialConfig{
				mode:            credModeAccessKey,
				accessKeyID:     id,
				accessKeySecret: secret,
				securityToken:   resolveSecurityToken(f.accessKeyID, f.securityToken),
			}
			if cfg.securityToken != "" {
				cfg.mode = credModeSTS
			}
			return cfg, nil
		}
		if akFlagSet {
			// AccessKey flags given but no complete pair: report that rather
			// than silently switching to OIDC from the environment.
			return credentialConfig{}, errMissingCredentials
		}
	}

	cfg := credentialConfig{
		mode:            credModeOIDC,
		roleArn:         firstNonEmpty(f.roleArn, os.Getenv("ALIBABA_CLOUD_ROLE_ARN")),
		oidcProviderArn: firstNonEmpty(f.oidcProviderArn, os.Getenv("ALIBABA_CLOUD_OIDC_PROVIDER_ARN")),
		oidcTokenFile:   firstNonEmpty(f.oidcTokenFile, os.Getenv("ALIBABA_CLOUD_OIDC_TOKEN_FILE")),
		roleSessionName: firstNonEmpty(f.roleSessionName, os.Getenv("ALIBABA_CLOUD_ROLE_SESSION_NAME"), defaultRoleSessionName),
	}
	if cfg.roleArn == "" && cfg.oidcProviderArn == "" && cfg.oidcTokenFile == "" {
		return credentialConfig{}, errMissingCredentials
	}
	var missing []string
	if cfg.roleArn == "" {
		missing = append(missing, "--roleArn (ALIBABA_CLOUD_ROLE_ARN)")
	}
	if cfg.oidcProviderArn == "" {
		missing = append(missing, "--oidcProviderArn (ALIBABA_CLOUD_OIDC_PROVIDER_ARN)")
	}
	if cfg.oidcTokenFile == "" {
		missing = append(missing, "--oidcTokenFile (ALIBABA_CLOUD_OIDC_TOKEN_FILE)")
	}
	if len(missing) > 0 {
		return credentialConfig{}, fmt.Errorf("incomplete OIDC configuration, missing %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}

// resolveSecurityToken returns the STS security token to pair with the
// resolved AccessKey. An explicit --securityToken always wins. Otherwise the
// token must come from the same place as the AccessKey ID, because an STS
// token is only valid with the temporary key issued alongside it: a
// long-term key passed via --accessKeyId must not pick up an
// ALIBABA_CLOUD_SECURITY_TOKEN that an earlier CI step exported.
func resolveSecurityToken(flagID, flagToken string) string {
	if token := strings.TrimSpace(flagToken); token != "" {
		return token
	}
	switch {
	case strings.TrimSpace(flagID) != "":
		return ""
	case strings.TrimSpace(os.Getenv("ALIYUN_ACCESS_KEY_ID")) != "":
		return strings.TrimSpace(os.Getenv("ALIYUN_SECURITY_TOKEN"))
	default:
		return strings.TrimSpace(os.Getenv("ALIBABA_CLOUD_SECURITY_TOKEN"))
	}
}

// buildCredentialsProvider turns a resolved credentialConfig into the SDK
// credentials provider the ECS client signs requests with. The AccessKey mode
// yields the same StaticAKCredentialsProvider that NewClientWithAccessKey
// creates internally.
func buildCredentialsProvider(cfg credentialConfig) (credentials.CredentialsProvider, error) {
	switch cfg.mode {
	case credModeAccessKey:
		return credentials.NewStaticAKCredentialsProvider(cfg.accessKeyID, cfg.accessKeySecret), nil
	case credModeSTS:
		return credentials.NewStaticSTSCredentialsProvider(cfg.accessKeyID, cfg.accessKeySecret, cfg.securityToken), nil
	case credModeOIDC:
		// Check the token file up front: the SDK only reads it on the first
		// API call, where a typo'd path surfaces as a confusing
		// "failed to DescribeInstanceTypes: open ..." error.
		if _, err := os.Stat(cfg.oidcTokenFile); err != nil {
			return nil, fmt.Errorf("cannot read OIDC token file: %v", err)
		}
		builder := credentials.NewOIDCCredentialsProviderBuilder().
			WithRoleArn(cfg.roleArn).
			WithOIDCProviderARN(cfg.oidcProviderArn).
			WithOIDCTokenFilePath(cfg.oidcTokenFile).
			WithRoleSessionName(cfg.roleSessionName)
		if stsEndpointOverride != "" {
			builder = builder.WithSTSEndpoint(stsEndpointOverride)
		}
		provider, err := builder.Build()
		if err != nil {
			return nil, err
		}
		return provider, nil
	default:
		return nil, fmt.Errorf("unknown credential mode %q", cfg.mode)
	}
}
