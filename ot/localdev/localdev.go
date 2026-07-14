package localdev

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/collinbentley1/cli/ot/run"
	"golang.org/x/sys/unix"
)

const (
	profileVersion       = 2
	registryVersion      = 1
	activeEnv            = "OT_WORKTREE_ISOLATION_ACTIVE"
	instanceIDEnv        = "OT_LOCAL_INSTANCE_ID"
	instanceRootEnv      = "OT_LOCAL_INSTANCE_ROOT"
	instanceProfileEnv   = "OT_LOCAL_INSTANCE_PROFILE"
	isolationModeEnv     = "OT_WORKTREE_ISOLATION"
	registryDirEnv       = "OT_LOCALDEV_REGISTRY_DIR"
	forceReallocateEnv   = "OT_WORKTREE_REASSIGN_PORTS"
	portBlockBase        = 20000
	portBlockSize        = 20
	maxPortBlockOffset   = 2000
	defaultInfisicalHost = "127.0.0.1"
)

type Profile struct {
	Version    int        `json:"version"`
	RepoRoot   string     `json:"repo_root"`
	InstanceID string     `json:"instance_id"`
	Offset     int        `json:"offset"`
	Ports      Ports      `json:"ports"`
	Containers Containers `json:"containers"`
	Secrets    Secrets    `json:"secrets"`
	CreatedAt  string     `json:"created_at"`
	UpdatedAt  string     `json:"updated_at"`
}

type Ports struct {
	Router     int `json:"router"`
	Marketing  int `json:"marketing"`
	AuthWeb    int `json:"auth_web"`
	AppWeb     int `json:"app_web"`
	AuthAPI    int `json:"auth_api"`
	Backend    int `json:"backend"`
	CRM        int `json:"crm"`
	CRMMCP     int `json:"crm_mcp"`
	Docs       int `json:"docs"`
	AppDB      int `json:"app_db"`
	CRMDB      int `json:"crm_db"`
	WordPress  int `json:"wordpress"`
	WPTests    int `json:"wordpress_tests"`
	WPDatabase int `json:"wordpress_database"`
}

type Containers struct {
	AppDB string `json:"app_db"`
	CRMDB string `json:"crm_db"`
}

type Secrets struct {
	AppDBPassword string `json:"app_db_password"`
	CRMDBPassword string `json:"crm_db_password"`
}

type registryFile struct {
	Version int                      `json:"version"`
	Repos   map[string]registryEntry `json:"repos"`
}

type registryEntry struct {
	InstanceID string `json:"instance_id"`
	Offset     int    `json:"offset"`
	UpdatedAt  string `json:"updated_at"`
}

func Ensure(repoRoot string) (*Profile, error) {
	if !Enabled(repoRoot) {
		return nil, nil
	}

	profilePath := filepath.Join(repoRoot, ".ot", "local-dev-profile.json")
	if strings.TrimSpace(os.Getenv(forceReallocateEnv)) == "" {
		if profile, err := readProfile(profilePath); err == nil && profile.validFor(repoRoot) {
			_ = os.Chmod(profilePath, 0o600)
			return profile, nil
		}
	}

	profile, err := allocateProfile(repoRoot)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(profilePath), 0o755); err != nil {
		return nil, fmt.Errorf("create local profile dir: %w", err)
	}
	if err := writeProfile(profilePath, profile); err != nil {
		return nil, err
	}
	return profile, nil
}

func Apply(repoRoot string) (*Profile, error) {
	profile, err := Ensure(repoRoot)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		_ = os.Unsetenv(activeEnv)
		return nil, nil
	}
	for key, value := range profile.Env() {
		if err := os.Setenv(key, value); err != nil {
			return nil, fmt.Errorf("set %s: %w", key, err)
		}
	}
	return profile, nil
}

func Active() bool {
	return strings.TrimSpace(os.Getenv(activeEnv)) == "1"
}

func Enabled(repoRoot string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(isolationModeEnv))) {
	case "0", "false", "off", "no":
		return false
	case "1", "true", "on", "yes":
		return true
	}
	return isLinkedGitWorktree(repoRoot)
}

func (p *Profile) Env() map[string]string {
	backendURL := localURL(defaultInfisicalHost, p.Ports.Backend)
	authAPIURL := localURL(defaultInfisicalHost, p.Ports.AuthAPI)
	routerOrigin := localhostURL(p.Ports.Router)
	authWebOrigin := localhostURL(p.Ports.AuthWeb)
	crmOrigin := localURL(defaultInfisicalHost, p.Ports.CRM)
	frontendOrigins := csvUnique([]string{
		routerOrigin,
		localURL(defaultInfisicalHost, p.Ports.Router),
		localhostURL(p.Ports.Marketing),
		localURL(defaultInfisicalHost, p.Ports.Marketing),
		authWebOrigin,
		localURL(defaultInfisicalHost, p.Ports.AuthWeb),
		localhostURL(p.Ports.AppWeb),
		localURL(defaultInfisicalHost, p.Ports.AppWeb),
		localhostURL(p.Ports.Docs),
		localURL(defaultInfisicalHost, p.Ports.Docs),
		crmOrigin,
	})

	appDBPassword := p.Secrets.AppDBPassword
	crmDBPassword := p.Secrets.CRMDBPassword

	return map[string]string{
		activeEnv:                             "1",
		instanceIDEnv:                         p.InstanceID,
		instanceRootEnv:                       p.RepoRoot,
		instanceProfileEnv:                    filepath.Join(p.RepoRoot, ".ot", "local-dev-profile.json"),
		"OT_PUBLIC_PATH_PREFIX":               "/_ot/" + p.InstanceID,
		"APP_PORT":                            strconv.Itoa(p.Ports.Backend),
		"AUTH_PORT":                           strconv.Itoa(p.Ports.AuthAPI),
		"LOCAL_ROUTER_PORT":                   strconv.Itoa(p.Ports.Router),
		"MARKETING_PORT":                      strconv.Itoa(p.Ports.Marketing),
		"AUTH_WEB_PORT":                       strconv.Itoa(p.Ports.AuthWeb),
		"APP_WEB_PORT":                        strconv.Itoa(p.Ports.AppWeb),
		"DOCS_PORT":                           strconv.Itoa(p.Ports.Docs),
		"CRM_PORT":                            strconv.Itoa(p.Ports.CRM),
		"CRM_MCP_PORT":                        strconv.Itoa(p.Ports.CRMMCP),
		"WP_ENV_PORT":                         strconv.Itoa(p.Ports.WordPress),
		"WP_ENV_TESTS_PORT":                   strconv.Itoa(p.Ports.WPTests),
		"WP_ENV_MYSQL_PORT":                   strconv.Itoa(p.Ports.WPDatabase),
		"APP_DB_PORT":                         strconv.Itoa(p.Ports.AppDB),
		"CRM_DB_PORT":                         strconv.Itoa(p.Ports.CRMDB),
		"APP_DB_CONTAINER":                    p.Containers.AppDB,
		"CRM_DB_CONTAINER":                    p.Containers.CRMDB,
		"LOCAL_PRIMARY_APP_PGSQL_INIT_DB_URL": postgresURL("postgres", appDBPassword, defaultInfisicalHost, p.Ports.AppDB, "postgres", "postgresql"),
		"LOCAL_PRIMARY_APP_PGSQL_DB_URL":      postgresURL("postgres", appDBPassword, defaultInfisicalHost, p.Ports.AppDB, "app", "postgresql"),
		"LOCAL_PRIMARY_CRM_PGSQL_INIT_DB_URL": postgresURL("crm", crmDBPassword, defaultInfisicalHost, p.Ports.CRMDB, "postgres", "postgresql"),
		"LOCAL_PRIMARY_CRM_PGSQL_DB_URL":      postgresURL("crm", crmDBPassword, defaultInfisicalHost, p.Ports.CRMDB, "crm", "postgresql+psycopg"),
		"NEXT_PUBLIC_API_BASE_URL":            backendURL,
		"NEXT_PUBLIC_AUTH_BASE_URL":           authAPIURL,
		"NEXT_PUBLIC_SITE_URL":                routerOrigin,
		"HARNESS_BACKEND_URL":                 backendURL,
		"AUTH_BASE_URL":                       authAPIURL,
		"FRONTEND_BASE_URL":                   authWebOrigin,
		"WORKOS_REDIRECT_URI":                 fmt.Sprintf("%s/api/auth/oauth/callback", authAPIURL),
		"AUTH_COOKIE_SECURE":                  "false",
		"CORS_ALLOW_ORIGINS":                  frontendOrigins,
		"AUTH_ALLOWED_RETURN_TO_ORIGINS":      csvUnique([]string{routerOrigin, localURL(defaultInfisicalHost, p.Ports.Router), authWebOrigin, localURL(defaultInfisicalHost, p.Ports.AuthWeb), crmOrigin}),
		"CRM_PUBLIC_BASE_URL":                 crmOrigin,
		"CRM_MCP_PUBLIC_BASE_URL":             crmOrigin + "/mcp",
		"CRM_GOOGLE_OAUTH_REDIRECT_URI":       crmOrigin + "/api/google/connect/callback",
		"CRM_MICROSOFT_OAUTH_REDIRECT_URI":    crmOrigin + "/api/microsoft/connect/callback",
	}
}

// dynamicBackendEnvKeys are env vars that the backend stack recomputes from
// scratch every run (currently the Tailscale-funnel MCP URLs in
// “backendStackExtraEnv“). They must never be re-exported from the caller's
// shell — overmind injects the freshly-computed values when it spawns each
// procfile entry, but the procfile prefix produced by “ShellExportPrefixFromEnv“
// is prepended INSIDE the spawned bash, so a stale value from the caller's
// shell would otherwise overwrite the fresh one and the web/worker would
// advertise old tunnel URLs.
// Per-tenant "<SLUG>_MCP_PUBLIC_URL" vars are also dynamic, but they are
// computed from the backend's tenant catalog at runtime and never appear in
// knownEnvKeys, so they are excluded from re-export without being listed here.
func dynamicBackendEnvKeys() map[string]bool {
	return map[string]bool{
		"MCP_PUBLIC_URL":                       true,
		"OT_BACKEND_MCP_TUNNEL_HOST":           true,
		"OT_BACKEND_MCP_TUNNEL_PUBLIC_URL":     true,
		"OT_BACKEND_MCP_TUNNEL_PATH":           true,
		"OT_BACKEND_MCP_TUNNEL_ASSET_BASE_URL": true,
		"OT_BACKEND_MCP_TUNNEL_TENANT_URLS":    true,
		"OT_WIDGET_STATIC_ASSET_BASE_URL":      true,
		"OT_WIDGET_RESOURCE_DOMAINS":           true,
	}
}

func ShellExportLinesFromEnv() []string {
	return ShellExportLinesFromEnvExcept(nil)
}

func ShellExportPrefixFromEnv() string {
	lines := ShellExportLinesFromEnv()
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "; ") + "; "
}

func WrapSpecWithShellExports(spec run.Spec) run.Spec {
	lines := ShellExportLinesFromEnvExcept(spec.Env)
	if len(lines) == 0 {
		return spec
	}
	parts := append([]string{spec.Program}, spec.Args...)
	command := strings.Join(append(lines, "exec "+shellJoin(parts)), "\n")
	return run.Spec{
		Program: "bash",
		Args:    []string{"-lc", command},
		Dir:     spec.Dir,
		Env:     spec.Env,
	}
}

func ShellExportLinesFromEnvExcept(overrides map[string]string) []string {
	if !Active() {
		return nil
	}
	skip := dynamicBackendEnvKeys()
	for key := range overrides {
		skip[key] = true
	}
	lines := make([]string, 0, len(knownEnvKeys()))
	for _, key := range knownEnvKeys() {
		if skip[key] {
			continue
		}
		value, ok := os.LookupEnv(key)
		if !ok {
			continue
		}
		lines = append(lines, "export "+key+"="+shellQuote(value))
	}
	return lines
}

func knownEnvKeys() []string {
	return []string{
		activeEnv,
		instanceIDEnv,
		instanceRootEnv,
		instanceProfileEnv,
		"OT_PUBLIC_PATH_PREFIX",
		"APP_PORT",
		"AUTH_PORT",
		"LOCAL_ROUTER_PORT",
		"MARKETING_PORT",
		"AUTH_WEB_PORT",
		"APP_WEB_PORT",
		"DOCS_PORT",
		"CRM_PORT",
		"CRM_MCP_PORT",
		"WP_ENV_PORT",
		"WP_ENV_TESTS_PORT",
		"WP_ENV_MYSQL_PORT",
		"APP_DB_PORT",
		"CRM_DB_PORT",
		"APP_DB_CONTAINER",
		"CRM_DB_CONTAINER",
		"LOCAL_PRIMARY_APP_PGSQL_INIT_DB_URL",
		"LOCAL_PRIMARY_APP_PGSQL_DB_URL",
		"LOCAL_PRIMARY_CRM_PGSQL_INIT_DB_URL",
		"LOCAL_PRIMARY_CRM_PGSQL_DB_URL",
		"DB_URL",
		"NEXT_PUBLIC_API_BASE_URL",
		"NEXT_PUBLIC_AUTH_BASE_URL",
		"NEXT_PUBLIC_SITE_URL",
		"HARNESS_BACKEND_URL",
		"HARNESS_MCP_TARGETS_JSON",
		"HARNESS_TARGET",
		"HARNESS_TARGET_VERSION",
		"MCP_PUBLIC_URL",
		"OT_BACKEND_MCP_TUNNEL_HOST",
		"OT_BACKEND_MCP_TUNNEL_PUBLIC_URL",
		"OT_BACKEND_MCP_TUNNEL_ASSET_BASE_URL",
		"OT_BACKEND_MCP_TUNNEL_TENANT_URLS",
		"OT_WIDGET_STATIC_ASSET_BASE_URL",
		"OT_WIDGET_RESOURCE_DOMAINS",
		"AUTH_BASE_URL",
		"FRONTEND_BASE_URL",
		"WORKOS_REDIRECT_URI",
		"AUTH_COOKIE_SECURE",
		"CORS_ALLOW_ORIGINS",
		"AUTH_ALLOWED_RETURN_TO_ORIGINS",
		"CRM_PUBLIC_BASE_URL",
		"CRM_MCP_PUBLIC_BASE_URL",
		"CRM_GOOGLE_OAUTH_REDIRECT_URI",
		"CRM_MICROSOFT_OAUTH_REDIRECT_URI",
	}
}

func allocateProfile(repoRoot string) (*Profile, error) {
	regDir, err := registryDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(regDir, 0o755); err != nil {
		return nil, fmt.Errorf("create local profile registry dir: %w", err)
	}

	lockPath := filepath.Join(regDir, "profiles.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open local profile registry lock: %w", err)
	}
	defer func() { _ = lock.Close() }()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return nil, fmt.Errorf("flock local profile registry: %w", err)
	}
	defer func() { _ = unix.Flock(int(lock.Fd()), unix.LOCK_UN) }()

	registryPath := filepath.Join(regDir, "profiles.json")
	registry := readRegistry(registryPath)
	pruneRegistry(registry)

	if entry, ok := registry.Repos[repoRoot]; ok && strings.TrimSpace(os.Getenv(forceReallocateEnv)) == "" {
		profile, err := newProfile(repoRoot, entry.InstanceID, entry.Offset)
		if err != nil {
			return nil, err
		}
		registry.Repos[repoRoot] = registryEntry{
			InstanceID: profile.InstanceID,
			Offset:     profile.Offset,
			UpdatedAt:  profile.UpdatedAt,
		}
		if err := writeRegistry(registryPath, registry); err != nil {
			return nil, err
		}
		return profile, nil
	}

	instanceID := instanceIDForRepo(repoRoot)
	usedOffsets := map[int]bool{}
	for root, entry := range registry.Repos {
		if root == repoRoot {
			continue
		}
		usedOffsets[entry.Offset] = true
	}

	start := preferredOffset(repoRoot)
	for i := range maxPortBlockOffset {
		offset := (start + i) % maxPortBlockOffset
		if usedOffsets[offset] {
			continue
		}
		ports := portsForOffset(offset)
		if !portsAvailable(ports) {
			continue
		}
		profile, err := newProfile(repoRoot, instanceID, offset)
		if err != nil {
			return nil, err
		}
		registry.Repos[repoRoot] = registryEntry{
			InstanceID: profile.InstanceID,
			Offset:     profile.Offset,
			UpdatedAt:  profile.UpdatedAt,
		}
		if err := writeRegistry(registryPath, registry); err != nil {
			return nil, err
		}
		return profile, nil
	}
	return nil, fmt.Errorf("could not allocate a free local port block for %s", repoRoot)
}

func newProfile(repoRoot, instanceID string, offset int) (*Profile, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	ports := portsForOffset(offset)
	secrets, err := newSecrets()
	if err != nil {
		return nil, err
	}
	return &Profile{
		Version:    profileVersion,
		RepoRoot:   repoRoot,
		InstanceID: instanceID,
		Offset:     offset,
		Ports:      ports,
		Containers: Containers{
			AppDB: "app-postgres-" + instanceID,
			CRMDB: "crm-postgres-" + instanceID,
		},
		Secrets:   secrets,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func newSecrets() (Secrets, error) {
	appPassword, err := randomLocalSecret()
	if err != nil {
		return Secrets{}, fmt.Errorf("generate local application database password: %w", err)
	}
	crmPassword, err := randomLocalSecret()
	if err != nil {
		return Secrets{}, fmt.Errorf("generate CRM local database password: %w", err)
	}
	return Secrets{
		AppDBPassword: appPassword,
		CRMDBPassword: crmPassword,
	}, nil
}

func randomLocalSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func portsForOffset(offset int) Ports {
	base := portBlockBase + offset*portBlockSize
	return Ports{
		Router:     base,
		Marketing:  base + 1,
		AuthWeb:    base + 2,
		AppWeb:     base + 3,
		AuthAPI:    base + 4,
		Backend:    base + 5,
		CRM:        base + 6,
		CRMMCP:     base + 7,
		Docs:       base + 8,
		AppDB:      base + 9,
		CRMDB:      base + 10,
		WordPress:  base + 11,
		WPTests:    base + 12,
		WPDatabase: base + 13,
	}
}

func (p *Profile) validFor(repoRoot string) bool {
	return p != nil &&
		p.Version == profileVersion &&
		p.RepoRoot == repoRoot &&
		p.InstanceID != "" &&
		p.Offset >= 0 &&
		p.Offset < maxPortBlockOffset &&
		p.Secrets.AppDBPassword != "" &&
		p.Secrets.CRMDBPassword != ""
}

func readProfile(path string) (*Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var profile Profile
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

func writeProfile(path string, profile *Profile) error {
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return fmt.Errorf("encode local profile: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

func readRegistry(path string) registryFile {
	registry := registryFile{
		Version: registryVersion,
		Repos:   map[string]registryEntry{},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return registry
	}
	if err := json.Unmarshal(data, &registry); err != nil {
		return registryFile{Version: registryVersion, Repos: map[string]registryEntry{}}
	}
	if registry.Version != registryVersion || registry.Repos == nil {
		return registryFile{Version: registryVersion, Repos: map[string]registryEntry{}}
	}
	return registry
}

func writeRegistry(path string, registry registryFile) error {
	registry.Version = registryVersion
	if registry.Repos == nil {
		registry.Repos = map[string]registryEntry{}
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return fmt.Errorf("encode local profile registry: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func pruneRegistry(registry registryFile) {
	for repoRoot := range registry.Repos {
		if _, err := os.Stat(repoRoot); os.IsNotExist(err) {
			delete(registry.Repos, repoRoot)
		}
	}
}

func registryDir() (string, error) {
	if override := strings.TrimSpace(os.Getenv(registryDirEnv)); override != "" {
		return override, nil
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache dir: %w", err)
	}
	return filepath.Join(cacheDir, "ot", "localdev"), nil
}

func isLinkedGitWorktree(repoRoot string) bool {
	data, err := os.ReadFile(filepath.Join(repoRoot, ".git"))
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(data)), "gitdir:")
}

func instanceIDForRepo(repoRoot string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(repoRoot)))
	return hex.EncodeToString(sum[:])[:10]
}

func preferredOffset(repoRoot string) int {
	sum := sha256.Sum256([]byte(filepath.Clean(repoRoot)))
	value := int(sum[10])<<8 | int(sum[11])
	return value % maxPortBlockOffset
}

func portsAvailable(ports Ports) bool {
	for _, port := range ports.list() {
		if !portAvailable(port) {
			return false
		}
	}
	return true
}

func portAvailable(port int) bool {
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

func (p Ports) list() []int {
	return []int{
		p.Router,
		p.Marketing,
		p.AuthWeb,
		p.AppWeb,
		p.AuthAPI,
		p.Backend,
		p.CRM,
		p.CRMMCP,
		p.Docs,
		p.AppDB,
		p.CRMDB,
		p.WordPress,
		p.WPTests,
		p.WPDatabase,
	}
}

func localURL(host string, port int) string {
	return fmt.Sprintf("http://%s:%d", host, port)
}

func postgresURL(user string, password string, host string, port int, database string, scheme string) string {
	parsed := url.URL{
		Scheme: scheme,
		User:   url.UserPassword(user, password),
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
		Path:   "/" + database,
	}
	return parsed.String()
}

func localhostURL(port int) string {
	return localURL("localhost", port)
}

func csvUnique(values []string) string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.TrimSpace(value)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
	}
	return strings.Join(out, ",")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func shellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func DebugString(profile *Profile) string {
	if profile == nil {
		return ""
	}
	entries := profile.Env()
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+"="+entries[key])
	}
	return strings.Join(lines, "\n")
}
