package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials"
	ecsService "github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
)

var (
	accessKeyId     = flag.String("accessKeyId", "", "Your accessKeyId of cloud account (or set ALIYUN_ACCESS_KEY_ID)")
	accessKeySecret = flag.String("accessKeySecret", "", "Your accessKeySecret of cloud account (or set ALIYUN_ACCESS_KEY_SECRET)")
	securityToken   = flag.String("securityToken", "", "STS security token, for temporary AccessKeys (or set ALIYUN_SECURITY_TOKEN / ALIBABA_CLOUD_SECURITY_TOKEN)")
	roleArn         = flag.String("roleArn", "", "OIDC: RAM role ARN to assume (or set ALIBABA_CLOUD_ROLE_ARN)")
	oidcProviderArn = flag.String("oidcProviderArn", "", "OIDC: RAM OIDC identity provider ARN (or set ALIBABA_CLOUD_OIDC_PROVIDER_ARN)")
	oidcTokenFile   = flag.String("oidcTokenFile", "", "OIDC: path to a file holding the OIDC ID token (or set ALIBABA_CLOUD_OIDC_TOKEN_FILE)")
	roleSessionName = flag.String("roleSessionName", "", "OIDC: session name of the assumed role (or set ALIBABA_CLOUD_ROLE_SESSION_NAME; default spot-instance-advisor)")
	region          = flag.String("region", "cn-hangzhou", "The region of spot instances")
	cpu             = flag.Int("mincpu", 1, "Min cores of spot instances")
	memory          = flag.Int("minmem", 2, "Min memory of spot instances")
	maxCpu          = flag.Int("maxcpu", 32, "Max cores of spot instances ")
	maxMemory       = flag.Int("maxmem", 64, "Max memory of spot instances")
	family          = flag.String("family", "", "The spot instance family you want (e.g. ecs.n1,ecs.n2)")
	instanceType    = flag.String("instanceType", "", "Specific instance types (comma-separated, e.g. ecs.n1.small,ecs.n2.large). Takes precedence over family parameter.")
	arch            = flag.String("arch", "", "CPU architecture filter: x86_64 or arm64")
	cutoff          = flag.Int("cutoff", 2, "Discount of the spot instance prices")
	limit           = flag.Int("limit", 20, "Limit of the spot instances")
	resolution      = flag.Int("resolution", 7, "The window of price history analysis in days")
	jsonOutput      = flag.Bool("json", false, "Output results in JSON format")
)

func main() {
	flag.Parse()

	if err := validateFlags(); err != nil {
		fail("Invalid parameters", err)
	}

	credConfig, err := resolveCredentialConfig(credentialFlags{
		accessKeyID:     *accessKeyId,
		accessKeySecret: *accessKeySecret,
		securityToken:   *securityToken,
		roleArn:         *roleArn,
		oidcProviderArn: *oidcProviderArn,
		oidcTokenFile:   *oidcTokenFile,
		roleSessionName: *roleSessionName,
	})
	if errors.Is(err, errMissingCredentials) {
		fail("Missing required parameters", err)
	} else if err != nil {
		fail("Invalid credentials configuration", err)
	}

	credProvider, err := buildCredentialsProvider(credConfig)
	if err != nil {
		fail("Invalid credentials configuration", err)
	}

	client, err := newECSClient(*region, credProvider)
	if err != nil {
		fail("Failed to create ecs client", err)
	}

	metastore := NewMetaStore(client)

	if err := metastore.Initialize(*region, *jsonOutput); err != nil {
		fail("Failed to initialize metastore", err)
	}

	instanceTypes := metastore.FilterInstances(*cpu, *memory, *maxCpu, *maxMemory, *family, *instanceType, *arch, *jsonOutput)

	historyPrices, err := metastore.FetchSpotPrices(instanceTypes, *resolution, *jsonOutput)
	if err != nil {
		fail("Failed to fetch spot prices", err)
	}

	sortedInstancePrices := metastore.SpotPricesAnalysis(historyPrices, *jsonOutput)

	metastore.PrintPriceRank(sortedInstancePrices, *cutoff, *limit, *jsonOutput)
}

// newECSClient builds the ECS client that signs every request with creds.
// sdk.NewConfig() is the config NewClientWithAccessKey uses internally, so an
// AccessKey provider here behaves exactly like the pre-STS/OIDC client.
func newECSClient(region string, creds credentials.CredentialsProvider) (*ecsService.Client, error) {
	return ecsService.NewClientWithOptions(region, sdk.NewConfig(), creds)
}

// resolveCredentials picks credentials from explicit flags first, then from
// environment variables. Env support keeps the secret off the command line
// (argv / ps / shell history / CI logs) — the primary credential path on
// shared or CI hosts. Both the project's ALIYUN_* and the official
// ALIBABA_CLOUD_* names are accepted.
func resolveCredentials(flagID, flagSecret string) (string, string) {
	return firstNonEmpty(flagID,
			os.Getenv("ALIYUN_ACCESS_KEY_ID"),
			os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_ID")),
		firstNonEmpty(flagSecret,
			os.Getenv("ALIYUN_ACCESS_KEY_SECRET"),
			os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET"))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		// Return the TRIMMED value: the emptiness test uses TrimSpace, so a value
		// that is non-empty only due to surrounding whitespace must still be
		// normalized before use — otherwise a mis-pasted credential with stray
		// spaces passes the check but fails at the API with a confusing auth error.
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// validateFlags sanity-checks numeric flag ranges so that misconfiguration
// (e.g. --limit -5 or --mincpu 32 --maxcpu 1) produces a clear error instead
// of silently empty output.
func validateFlags() error {
	if *cpu <= 0 || *maxCpu <= 0 {
		return fmt.Errorf("mincpu and maxcpu must be positive (got mincpu=%d, maxcpu=%d)", *cpu, *maxCpu)
	}
	if *cpu > *maxCpu {
		return fmt.Errorf("mincpu (%d) must be <= maxcpu (%d)", *cpu, *maxCpu)
	}
	if *memory <= 0 || *maxMemory <= 0 {
		return fmt.Errorf("minmem and maxmem must be positive (got minmem=%d, maxmem=%d)", *memory, *maxMemory)
	}
	if *memory > *maxMemory {
		return fmt.Errorf("minmem (%d) must be <= maxmem (%d)", *memory, *maxMemory)
	}
	if *resolution <= 0 {
		return fmt.Errorf("resolution must be a positive number of days (got %d)", *resolution)
	}
	if *limit <= 0 {
		return fmt.Errorf("limit must be positive (got %d)", *limit)
	}
	if *cutoff < 0 {
		return fmt.Errorf("cutoff must not be negative (got %d)", *cutoff)
	}
	// cutoff == 0 is a legitimate filter ("highlight only free instances" —
	// Discount is 0 when SpotPrice is 0), so only negatives are rejected.
	return nil
}

// fail prints an error in the appropriate format (JSON or human-readable) and
// exits non-zero. It never returns, so callers need not guard the following
// statement. outputJSONError calls os.Exit itself in the JSON branch.
func fail(message string, err error) {
	if *jsonOutput {
		outputJSONError(message, err.Error())
	}
	fmt.Fprintf(os.Stderr, "Error: %s: %v\n", message, err)
	os.Exit(1)
}

// JSON 错误输出结构
type JSONError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// 输出 JSON 格式的错误信息
func outputJSONError(message, details string) {
	errorResponse := JSONError{
		Error:   message,
		Message: details,
	}

	jsonData, err := json.MarshalIndent(errorResponse, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "{\"error\":\"Failed to marshal error\",\"message\":\"%s\"}\n", err.Error())
		return
	}

	fmt.Println(string(jsonData))
	os.Exit(1)
}
