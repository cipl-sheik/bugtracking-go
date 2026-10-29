package bugtrack

import (
	"fmt"
	"net/url"
	"strings"
)

func resolveOptions(opts Options) (endpoint, apiKey string, err error) {
	if dsn := strings.TrimSpace(opts.DSN); dsn != "" {
		return parseDSN(dsn)
	}
	endpoint = strings.TrimSpace(opts.Endpoint)
	apiKey = strings.TrimSpace(opts.APIKey)
	if endpoint == "" && apiKey == "" {
		return "", "", fmt.Errorf("bugtrack: DSN or both Endpoint and APIKey are required")
	}
	endpoint, err = parseEndpoint(endpoint)
	if err != nil {
		return "", "", err
	}
	if endpoint == "" {
		return "", "", fmt.Errorf("bugtrack: Endpoint is required")
	}
	apiKey, err = parseKey(apiKey)
	if err != nil {
		return "", "", err
	}
	return endpoint, apiKey, nil
}

func parseEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("bugtrack: Endpoint must be an absolute http or https URL")
	}
	if u.User != nil {
		return "", fmt.Errorf("bugtrack: put the API key in APIKey, not in the Endpoint URL")
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("bugtrack: Endpoint must be the API origin without a path")
	}
	return u.Scheme + "://" + u.Host, nil
}

// parseDSN accepts the DSN shapes produced by the BugTrack API.
// The project id in the path is not used. The key alone selects the project.
func parseDSN(raw string) (endpoint, apiKey string, err error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User == nil {
		return "", "", fmt.Errorf("bugtrack: invalid DSN")
	}
	user := strings.TrimSpace(u.User.Username())
	pass, hasPass := u.User.Password()
	pass = strings.TrimSpace(pass)
	if user == "" {
		return "", "", fmt.Errorf("bugtrack: invalid DSN")
	}
	if hasPass && pass != "" && !strings.HasPrefix(user, "btk_") {
		apiKey, err = parseKey("btk_" + user + "_" + pass)
	} else {
		apiKey, err = parseKey(user)
	}
	if err != nil {
		return "", "", fmt.Errorf("bugtrack: invalid DSN")
	}
	return u.Scheme + "://" + u.Host, apiKey, nil
}

func parseKey(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("bugtrack: malformed API key")
	}
	parts := strings.Split(raw, "_")
	if len(parts) != 3 || parts[0] != "btk" || !isHex(parts[1]) || !isHex(parts[2]) {
		return "", fmt.Errorf("bugtrack: malformed API key")
	}
	return "btk_" + parts[1] + "_" + parts[2], nil
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}
