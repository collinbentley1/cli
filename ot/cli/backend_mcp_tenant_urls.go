package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type backendMCPTenantURL struct {
	Slug    string
	Title   string
	Version string
	URL     string
}

type backendMCPCustomerAppsCatalog struct {
	Customers []backendMCPCustomerApp `json:"customers"`
}

type backendMCPCustomerApp struct {
	Slug     string                         `json:"slug"`
	Title    string                         `json:"title"`
	Versions []backendMCPCustomerAppVersion `json:"versions"`
}

type backendMCPCustomerAppVersion struct {
	TemplateVersion string `json:"template_version"`
	State           string `json:"state"`
}

func latestBackendMCPCustomerTenantURLs(repoRoot string, publicMCPURL string) ([]backendMCPTenantURL, error) {
	baseURL, err := publicMCPBaseURL(publicMCPURL)
	if err != nil {
		return nil, err
	}

	catalog, err := loadBackendMCPCustomerAppsCatalog(repoRoot)
	if err != nil {
		return nil, err
	}

	result := make([]backendMCPTenantURL, 0, len(catalog.Customers))
	for _, customer := range catalog.Customers {
		slug := strings.TrimSpace(customer.Slug)
		if slug == "" {
			return nil, fmt.Errorf("MCP customer app catalog contains a customer without a slug")
		}
		version, err := latestBackendMCPCustomerVersion(customer)
		if err != nil {
			return nil, err
		}
		result = append(result, backendMCPTenantURL{
			Slug:    slug,
			Title:   strings.TrimSpace(customer.Title),
			Version: version,
			URL:     fmt.Sprintf("%s/%s/%s/mcp", baseURL, slug, version),
		})
	}
	return result, nil
}

func printBackendMCPTenantURLs(repoRoot string, publicMCPURL string) {
	tenantURLs, err := latestBackendMCPCustomerTenantURLs(repoRoot, publicMCPURL)
	if err != nil {
		fmt.Printf("  customer MCPs: unavailable: %v\n", err)
		return
	}
	if len(tenantURLs) == 0 {
		return
	}
	fmt.Println("  customer MCPs:")
	for _, tenantURL := range tenantURLs {
		label := tenantURL.Slug
		if tenantURL.Title != "" {
			label = tenantURL.Title
		}
		fmt.Printf("    %s (%s): %s\n", label, tenantURL.Version, tenantURL.URL)
	}
}

func backendMCPTenantPublicURLEnvName(slug string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(slug), "-", "_")) + "_MCP_PUBLIC_URL"
}

func backendMCPTenantURLsEnvValue(tenantURLs []backendMCPTenantURL) string {
	parts := make([]string, 0, len(tenantURLs))
	for _, tenantURL := range tenantURLs {
		slug := strings.TrimSpace(tenantURL.Slug)
		publicURL := strings.TrimSpace(tenantURL.URL)
		if slug == "" || publicURL == "" {
			continue
		}
		parts = append(parts, slug+"="+publicURL)
	}
	return strings.Join(parts, "|")
}

func publicMCPBaseURL(publicMCPURL string) (string, error) {
	value := strings.TrimSpace(publicMCPURL)
	if value == "" {
		return "", fmt.Errorf("public MCP URL is empty")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("parse public MCP URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("public MCP URL must include scheme and host: %s", value)
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	path = strings.TrimSuffix(path, "/mcp")
	return parsed.Scheme + "://" + parsed.Host + path, nil
}

func loadBackendMCPCustomerAppsCatalog(repoRoot string) (backendMCPCustomerAppsCatalog, error) {
	path := filepath.Join(repoRoot, "backend", "server", "mcp_app", "customer_apps.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return backendMCPCustomerAppsCatalog{}, fmt.Errorf("read MCP customer app catalog: %w", err)
	}
	var catalog backendMCPCustomerAppsCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return backendMCPCustomerAppsCatalog{}, fmt.Errorf("parse MCP customer app catalog: %w", err)
	}
	return catalog, nil
}

func latestBackendMCPCustomerVersion(customer backendMCPCustomerApp) (string, error) {
	var latest string
	var latestKey []int
	for _, version := range customer.Versions {
		if strings.TrimSpace(version.State) == "retired" {
			continue
		}
		templateVersion := strings.TrimSpace(version.TemplateVersion)
		key, err := mcpVersionSortKey(templateVersion)
		if err != nil {
			return "", fmt.Errorf("%s has invalid MCP app version %q: %w", customer.Slug, templateVersion, err)
		}
		if latest == "" || compareMCPVersionSortKeys(key, latestKey) > 0 {
			latest = templateVersion
			latestKey = key
		}
	}
	if latest == "" {
		return "", fmt.Errorf("MCP customer app %s has no non-retired versions", customer.Slug)
	}
	return latest, nil
}

func mcpVersionSortKey(version string) ([]int, error) {
	value := strings.TrimSpace(version)
	if !strings.HasPrefix(value, "v") {
		return nil, fmt.Errorf("must start with v")
	}
	segments := strings.Split(strings.TrimPrefix(value, "v"), ".")
	if len(segments) == 0 || len(segments) > 3 {
		return nil, fmt.Errorf("must use vMAJOR, vMAJOR.MINOR, or vMAJOR.MINOR.PATCH")
	}
	key := make([]int, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			return nil, fmt.Errorf("contains an empty version segment")
		}
		if !isDecimalVersionSegment(segment) {
			return nil, fmt.Errorf("contains non-numeric segment %q", segment)
		}
		part, err := strconv.Atoi(segment)
		if err != nil {
			return nil, fmt.Errorf("contains non-numeric segment %q", segment)
		}
		key = append(key, part)
	}
	return key, nil
}

func isDecimalVersionSegment(segment string) bool {
	for _, char := range segment {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func compareMCPVersionSortKeys(left []int, right []int) int {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for i := 0; i < limit; i++ {
		if left[i] > right[i] {
			return 1
		}
		if left[i] < right[i] {
			return -1
		}
	}
	if len(left) > len(right) {
		return 1
	}
	if len(left) < len(right) {
		return -1
	}
	return 0
}
