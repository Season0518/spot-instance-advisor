package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials"
	ecsService "github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
)

// credentialEnvVars are every env var credential resolution reads.
var credentialEnvVars = []string{
	"ALIYUN_ACCESS_KEY_ID", "ALIYUN_ACCESS_KEY_SECRET", "ALIYUN_SECURITY_TOKEN",
	"ALIBABA_CLOUD_ACCESS_KEY_ID", "ALIBABA_CLOUD_ACCESS_KEY_SECRET", "ALIBABA_CLOUD_SECURITY_TOKEN",
	"ALIBABA_CLOUD_ROLE_ARN", "ALIBABA_CLOUD_OIDC_PROVIDER_ARN", "ALIBABA_CLOUD_OIDC_TOKEN_FILE",
	"ALIBABA_CLOUD_ROLE_SESSION_NAME",
}

// clearCredentialEnv blanks every credential env var for the test so the
// host environment (e.g. a CI runner with real credentials) cannot leak in.
func clearCredentialEnv(t *testing.T) {
	t.Helper()
	for _, k := range credentialEnvVars {
		t.Setenv(k, "")
	}
}

// TestResolveCredentialConfig locks in the credential selection order and,
// above all, that every pre-STS/OIDC AccessKey invocation resolves exactly
// as before.
func TestResolveCredentialConfig(t *testing.T) {
	oidcFlags := credentialFlags{
		roleArn:         "acs:ram::123:role/flag-role",
		oidcProviderArn: "acs:ram::123:oidc-provider/flag-idp",
		oidcTokenFile:   "/flag/token",
	}
	oidcEnv := map[string]string{
		"ALIBABA_CLOUD_ROLE_ARN":          "acs:ram::123:role/env-role",
		"ALIBABA_CLOUD_OIDC_PROVIDER_ARN": "acs:ram::123:oidc-provider/env-idp",
		"ALIBABA_CLOUD_OIDC_TOKEN_FILE":   "/env/token",
	}
	merge := func(ms ...map[string]string) map[string]string {
		out := map[string]string{}
		for _, m := range ms {
			for k, v := range m {
				out[k] = v
			}
		}
		return out
	}
	aliyunAK := map[string]string{"ALIYUN_ACCESS_KEY_ID": "aliyunID", "ALIYUN_ACCESS_KEY_SECRET": "aliyunSecret"}
	officialAK := map[string]string{"ALIBABA_CLOUD_ACCESS_KEY_ID": "STS.officialID", "ALIBABA_CLOUD_ACCESS_KEY_SECRET": "officialSecret"}
	officialToken := map[string]string{"ALIBABA_CLOUD_SECURITY_TOKEN": "officialToken"}

	tests := []struct {
		name  string
		flags credentialFlags
		env   map[string]string
		want  credentialConfig
	}{
		// --- AccessKey: unchanged pre-STS/OIDC behavior ---
		{
			name:  "AK flags",
			flags: credentialFlags{accessKeyID: "flagID", accessKeySecret: "flagSecret"},
			want:  credentialConfig{mode: credModeAccessKey, accessKeyID: "flagID", accessKeySecret: "flagSecret"},
		},
		{
			name: "ALIYUN_* AK env",
			env:  aliyunAK,
			want: credentialConfig{mode: credModeAccessKey, accessKeyID: "aliyunID", accessKeySecret: "aliyunSecret"},
		},
		{
			name: "ALIBABA_CLOUD_* AK env without token",
			env:  map[string]string{"ALIBABA_CLOUD_ACCESS_KEY_ID": "officialID", "ALIBABA_CLOUD_ACCESS_KEY_SECRET": "officialSecret"},
			want: credentialConfig{mode: credModeAccessKey, accessKeyID: "officialID", accessKeySecret: "officialSecret"},
		},
		{
			name:  "whitespace-only AK flags fall through to env",
			flags: credentialFlags{accessKeyID: "  ", accessKeySecret: "\t"},
			env:   aliyunAK,
			want:  credentialConfig{mode: credModeAccessKey, accessKeyID: "aliyunID", accessKeySecret: "aliyunSecret"},
		},
		{
			name:  "AK flags ignore an env token issued for another key",
			flags: credentialFlags{accessKeyID: "flagID", accessKeySecret: "flagSecret"},
			env:   merge(officialAK, officialToken),
			want:  credentialConfig{mode: credModeAccessKey, accessKeyID: "flagID", accessKeySecret: "flagSecret"},
		},
		{
			name: "ALIYUN_* AK ignores the ALIBABA_CLOUD_* token",
			env:  merge(aliyunAK, officialAK, officialToken),
			want: credentialConfig{mode: credModeAccessKey, accessKeyID: "aliyunID", accessKeySecret: "aliyunSecret"},
		},
		{
			name: "env AK beats env OIDC",
			env:  merge(aliyunAK, oidcEnv),
			want: credentialConfig{mode: credModeAccessKey, accessKeyID: "aliyunID", accessKeySecret: "aliyunSecret"},
		},

		// --- STS ---
		{
			name:  "AK flags + securityToken flag",
			flags: credentialFlags{accessKeyID: "STS.flagID", accessKeySecret: "flagSecret", securityToken: " flagToken "},
			want:  credentialConfig{mode: credModeSTS, accessKeyID: "STS.flagID", accessKeySecret: "flagSecret", securityToken: "flagToken"},
		},
		{
			name: "ALIYUN_* AK + ALIYUN_SECURITY_TOKEN",
			env:  merge(aliyunAK, map[string]string{"ALIYUN_SECURITY_TOKEN": "aliyunToken"}),
			want: credentialConfig{mode: credModeSTS, accessKeyID: "aliyunID", accessKeySecret: "aliyunSecret", securityToken: "aliyunToken"},
		},
		{
			name: "ALIBABA_CLOUD_* AK + token (configure-aliyun-credentials-action)",
			env:  merge(officialAK, officialToken),
			want: credentialConfig{mode: credModeSTS, accessKeyID: "STS.officialID", accessKeySecret: "officialSecret", securityToken: "officialToken"},
		},
		{
			name:  "explicit securityToken flag pairs with env AK",
			flags: credentialFlags{securityToken: "flagToken"},
			env:   aliyunAK,
			want:  credentialConfig{mode: credModeSTS, accessKeyID: "aliyunID", accessKeySecret: "aliyunSecret", securityToken: "flagToken"},
		},

		// --- OIDC ---
		{
			name:  "OIDC flags",
			flags: oidcFlags,
			want: credentialConfig{mode: credModeOIDC, roleArn: oidcFlags.roleArn, oidcProviderArn: oidcFlags.oidcProviderArn,
				oidcTokenFile: oidcFlags.oidcTokenFile, roleSessionName: defaultRoleSessionName},
		},
		{
			name:  "OIDC flags beat env AK",
			flags: oidcFlags,
			env:   merge(aliyunAK, officialAK, officialToken),
			want: credentialConfig{mode: credModeOIDC, roleArn: oidcFlags.roleArn, oidcProviderArn: oidcFlags.oidcProviderArn,
				oidcTokenFile: oidcFlags.oidcTokenFile, roleSessionName: defaultRoleSessionName},
		},
		{
			name:  "OIDC flag overrides one env field",
			flags: credentialFlags{roleArn: "acs:ram::123:role/flag-role", roleSessionName: "my-session"},
			env:   oidcEnv,
			want: credentialConfig{mode: credModeOIDC, roleArn: "acs:ram::123:role/flag-role", oidcProviderArn: oidcEnv["ALIBABA_CLOUD_OIDC_PROVIDER_ARN"],
				oidcTokenFile: oidcEnv["ALIBABA_CLOUD_OIDC_TOKEN_FILE"], roleSessionName: "my-session"},
		},
		{
			name: "OIDC env (ACK RRSA)",
			env:  merge(oidcEnv, map[string]string{"ALIBABA_CLOUD_ROLE_SESSION_NAME": "env-session"}),
			want: credentialConfig{mode: credModeOIDC, roleArn: oidcEnv["ALIBABA_CLOUD_ROLE_ARN"], oidcProviderArn: oidcEnv["ALIBABA_CLOUD_OIDC_PROVIDER_ARN"],
				oidcTokenFile: oidcEnv["ALIBABA_CLOUD_OIDC_TOKEN_FILE"], roleSessionName: "env-session"},
		},
		{
			name: "half an env AK pair falls through to OIDC env",
			env:  merge(map[string]string{"ALIYUN_ACCESS_KEY_ID": "aliyunID"}, oidcEnv),
			want: credentialConfig{mode: credModeOIDC, roleArn: oidcEnv["ALIBABA_CLOUD_ROLE_ARN"], oidcProviderArn: oidcEnv["ALIBABA_CLOUD_OIDC_PROVIDER_ARN"],
				oidcTokenFile: oidcEnv["ALIBABA_CLOUD_OIDC_TOKEN_FILE"], roleSessionName: defaultRoleSessionName},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCredentialEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			got, err := resolveCredentialConfig(tt.flags)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestResolveCredentialConfigErrors(t *testing.T) {
	tests := []struct {
		name        string
		flags       credentialFlags
		env         map[string]string
		wantMissing bool   // errors.Is(err, errMissingCredentials)
		wantInErr   string // substring of the error message
	}{
		{
			name:        "nothing configured",
			wantMissing: true,
			wantInErr:   "accessKeyId and accessKeySecret are required",
		},
		{
			name:        "security token alone",
			flags:       credentialFlags{securityToken: "token"},
			wantMissing: true,
		},
		{
			name:        "half an AK pair on the command line does not switch to env OIDC",
			flags:       credentialFlags{accessKeyID: "flagID"},
			env:         map[string]string{"ALIBABA_CLOUD_ROLE_ARN": "r", "ALIBABA_CLOUD_OIDC_PROVIDER_ARN": "p", "ALIBABA_CLOUD_OIDC_TOKEN_FILE": "f"},
			wantMissing: true,
		},
		{
			name:      "AK flags combined with OIDC flags",
			flags:     credentialFlags{accessKeyID: "id", accessKeySecret: "secret", roleArn: "r"},
			wantInErr: "cannot be combined",
		},
		{
			name:      "security token flag combined with OIDC flags",
			flags:     credentialFlags{securityToken: "token", roleArn: "r", oidcProviderArn: "p", oidcTokenFile: "f"},
			wantInErr: "cannot be combined",
		},
		{
			name:        "security token flag alone does not switch to env OIDC",
			flags:       credentialFlags{securityToken: "token"},
			env:         map[string]string{"ALIBABA_CLOUD_ROLE_ARN": "r", "ALIBABA_CLOUD_OIDC_PROVIDER_ARN": "p", "ALIBABA_CLOUD_OIDC_TOKEN_FILE": "f"},
			wantMissing: true,
		},
		{
			name:      "incomplete OIDC flags",
			flags:     credentialFlags{roleArn: "r"},
			wantInErr: "missing --oidcProviderArn (ALIBABA_CLOUD_OIDC_PROVIDER_ARN), --oidcTokenFile (ALIBABA_CLOUD_OIDC_TOKEN_FILE)",
		},
		{
			name:      "incomplete OIDC env",
			env:       map[string]string{"ALIBABA_CLOUD_OIDC_TOKEN_FILE": "f"},
			wantInErr: "missing --roleArn (ALIBABA_CLOUD_ROLE_ARN), --oidcProviderArn (ALIBABA_CLOUD_OIDC_PROVIDER_ARN)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCredentialEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			_, err := resolveCredentialConfig(tt.flags)
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := errors.Is(err, errMissingCredentials); got != tt.wantMissing {
				t.Errorf("errors.Is(err, errMissingCredentials) = %v, want %v (err: %v)", got, tt.wantMissing, err)
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("error %q does not contain %q", err, tt.wantInErr)
			}
		})
	}
}

func TestBuildCredentialsProviderStatic(t *testing.T) {
	tests := []struct {
		name      string
		cfg       credentialConfig
		wantToken string
		wantName  string
	}{
		{"access key", credentialConfig{mode: credModeAccessKey, accessKeyID: "id", accessKeySecret: "secret"}, "", "static_ak"},
		{"sts", credentialConfig{mode: credModeSTS, accessKeyID: "id", accessKeySecret: "secret", securityToken: "token"}, "token", "static_sts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := buildCredentialsProvider(tt.cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			cc, err := p.GetCredentials()
			if err != nil {
				t.Fatalf("GetCredentials: %v", err)
			}
			if cc.AccessKeyId != "id" || cc.AccessKeySecret != "secret" || cc.SecurityToken != tt.wantToken {
				t.Errorf("got %+v", cc)
			}
			if p.GetProviderName() != tt.wantName {
				t.Errorf("provider name = %q, want %q", p.GetProviderName(), tt.wantName)
			}
		})
	}
}

func TestBuildCredentialsProviderErrors(t *testing.T) {
	missingFile := credentialConfig{mode: credModeOIDC, roleArn: "r", oidcProviderArn: "p",
		oidcTokenFile: filepath.Join(t.TempDir(), "absent"), roleSessionName: defaultRoleSessionName}
	if _, err := buildCredentialsProvider(missingFile); err == nil || !strings.Contains(err.Error(), "OIDC token file") {
		t.Errorf("missing OIDC token file: got err %v", err)
	}
	if _, err := buildCredentialsProvider(credentialConfig{mode: "bogus"}); err == nil {
		t.Error("unknown mode: expected an error")
	}
}

// fakeSTS is a TLS server standing in for sts.aliyuncs.com. The SDK's OIDC
// provider always dials https://, so the test trusts the server's
// certificate through http.DefaultTransport (which the SDK clones).
type fakeSTS struct {
	*httptest.Server
	mu   sync.Mutex
	form map[string]string
}

func newFakeSTS(t *testing.T) *fakeSTS {
	t.Helper()
	f := &fakeSTS{}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.form = map[string]string{}
		for k := range r.Form {
			f.form[k] = r.Form.Get(k)
		}
		f.mu.Unlock()
		expiration := time.Now().UTC().Add(time.Hour).Format("2006-01-02T15:04:05Z")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"RequestId":"req","Credentials":{"AccessKeyId":"STS.oidcID","AccessKeySecret":"oidcSecret",` +
			`"SecurityToken":"oidcToken","Expiration":"` + expiration + `"}}`))
	}))
	t.Cleanup(f.Close)

	pool := x509.NewCertPool()
	pool.AddCert(f.Certificate())
	transport := http.DefaultTransport.(*http.Transport)
	oldTLS := transport.TLSClientConfig
	transport.TLSClientConfig = &tls.Config{RootCAs: pool}
	oldOverride := stsEndpointOverride
	stsEndpointOverride = strings.TrimPrefix(f.URL, "https://")
	t.Cleanup(func() {
		transport.TLSClientConfig = oldTLS
		stsEndpointOverride = oldOverride
	})
	return f
}

func (f *fakeSTS) lastForm() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.form
}

func writeTokenFile(t *testing.T, token string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "oidc-token")
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestOIDCProviderAssumesRole checks the OIDC provider exchanges the token
// file for STS credentials through AssumeRoleWithOIDC.
func TestOIDCProviderAssumesRole(t *testing.T) {
	sts := newFakeSTS(t)
	cfg := credentialConfig{
		mode:            credModeOIDC,
		roleArn:         "acs:ram::123:role/ci",
		oidcProviderArn: "acs:ram::123:oidc-provider/github",
		oidcTokenFile:   writeTokenFile(t, "id-token-jwt"),
		roleSessionName: defaultRoleSessionName,
	}
	p, err := buildCredentialsProvider(cfg)
	if err != nil {
		t.Fatalf("buildCredentialsProvider: %v", err)
	}
	cc, err := p.GetCredentials()
	if err != nil {
		t.Fatalf("GetCredentials: %v", err)
	}
	if cc.AccessKeyId != "STS.oidcID" || cc.AccessKeySecret != "oidcSecret" || cc.SecurityToken != "oidcToken" {
		t.Errorf("unexpected credentials %+v", cc)
	}

	form := sts.lastForm()
	want := map[string]string{
		"Action":          "AssumeRoleWithOIDC",
		"RoleArn":         cfg.roleArn,
		"OIDCProviderArn": cfg.oidcProviderArn,
		"OIDCToken":       "id-token-jwt",
		"RoleSessionName": defaultRoleSessionName,
	}
	for k, v := range want {
		if form[k] != v {
			t.Errorf("STS request %s = %q, want %q", k, form[k], v)
		}
	}
}

// fakeECS is a plain-HTTP stand-in for the ECS endpoint that records the
// request parameters the SDK signed.
type fakeECS struct {
	*httptest.Server
	mu     sync.Mutex
	params map[string]string
}

func newFakeECS(t *testing.T) *fakeECS {
	t.Helper()
	// The SDK reads proxy env vars on every request; keep loopback traffic
	// off any proxy configured on the host.
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		t.Setenv(k, "")
	}
	f := &fakeECS{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.params = map[string]string{}
		for k := range r.Form {
			f.params[k] = r.Form.Get(k)
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"RequestId":"req","InstanceTypes":{"InstanceType":[]}}`))
	}))
	t.Cleanup(f.Close)
	return f
}

// call sends one DescribeInstanceTypes through client to the fake endpoint
// and returns the parameters the server received.
func (f *fakeECS) call(t *testing.T, client *ecsService.Client) map[string]string {
	t.Helper()
	req := ecsService.CreateDescribeInstanceTypesRequest()
	req.Scheme = "http"
	req.Domain = strings.TrimPrefix(f.URL, "http://")
	if _, err := client.DescribeInstanceTypes(req); err != nil {
		t.Fatalf("DescribeInstanceTypes: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.params
}

// TestECSClientSignsWithResolvedCredentials drives a real ECS client (as
// main builds it) against a local endpoint and checks which credentials end
// up on the wire for each mode.
func TestECSClientSignsWithResolvedCredentials(t *testing.T) {
	ecs := newFakeECS(t)

	newClient := func(t *testing.T, p credentials.CredentialsProvider) *ecsService.Client {
		t.Helper()
		client, err := newECSClient("cn-hangzhou", p)
		if err != nil {
			t.Fatalf("newECSClient: %v", err)
		}
		return client
	}

	t.Run("access key sends no security token", func(t *testing.T) {
		params := ecs.call(t, newClient(t, credentials.NewStaticAKCredentialsProvider("akID", "akSecret")))
		if params["AccessKeyId"] != "akID" {
			t.Errorf("AccessKeyId = %q, want akID", params["AccessKeyId"])
		}
		if _, ok := params["SecurityToken"]; ok {
			t.Errorf("unexpected SecurityToken %q on an AccessKey request", params["SecurityToken"])
		}
	})

	t.Run("sts sends the security token", func(t *testing.T) {
		params := ecs.call(t, newClient(t, credentials.NewStaticSTSCredentialsProvider("STS.id", "secret", "stsToken")))
		if params["AccessKeyId"] != "STS.id" || params["SecurityToken"] != "stsToken" {
			t.Errorf("AccessKeyId = %q, SecurityToken = %q", params["AccessKeyId"], params["SecurityToken"])
		}
	})

	t.Run("oidc sends the assumed-role credentials", func(t *testing.T) {
		newFakeSTS(t)
		p, err := buildCredentialsProvider(credentialConfig{
			mode:            credModeOIDC,
			roleArn:         "acs:ram::123:role/ci",
			oidcProviderArn: "acs:ram::123:oidc-provider/github",
			oidcTokenFile:   writeTokenFile(t, "id-token-jwt"),
			roleSessionName: defaultRoleSessionName,
		})
		if err != nil {
			t.Fatalf("buildCredentialsProvider: %v", err)
		}
		params := ecs.call(t, newClient(t, p))
		if params["AccessKeyId"] != "STS.oidcID" || params["SecurityToken"] != "oidcToken" {
			t.Errorf("AccessKeyId = %q, SecurityToken = %q", params["AccessKeyId"], params["SecurityToken"])
		}
	})
}
