package huburl

import (
	"fmt"
	"net/url"
	"strings"
)

const (
	DefaultSelfHosted = "http://localhost:8080"
	cloudHTTPBase     = "https://hub.amurg.ai"
	cloudWSURL        = "wss://hub.amurg.ai/ws/runtime"
)

// Endpoints describes the canonical hub endpoints derived from a single user input.
type Endpoints struct {
	DisplayURL string
	HTTPBase   string
	WSURL      string
}

// Cloud returns the fixed Amurg Cloud endpoints.
func Cloud() Endpoints {
	return Endpoints{
		DisplayURL: cloudHTTPBase,
		HTTPBase:   cloudHTTPBase,
		WSURL:      cloudWSURL,
	}
}

// Parse normalizes a hub URL entered by the user.
//
// Accepted inputs include:
//   - http://localhost:8080
//   - https://hub.example.com/amurg
//   - ws://localhost:8080/ws/runtime
//   - localhost:8080
func Parse(input string) (Endpoints, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		raw = DefaultSelfHosted
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return Endpoints{}, fmt.Errorf("parse hub URL: %w", err)
	}
	if u.Scheme == "" {
		return Endpoints{}, fmt.Errorf("hub URL must include a scheme")
	}
	if u.Host == "" {
		return Endpoints{}, fmt.Errorf("hub URL must include a host")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return Endpoints{}, fmt.Errorf("hub URL must not include a query string or fragment")
	}

	switch u.Scheme {
	case "http", "https":
		return fromHTTP(u), nil
	case "ws", "wss":
		return fromWS(u), nil
	default:
		return Endpoints{}, fmt.Errorf("hub URL must use http, https, ws, or wss")
	}
}

func fromHTTP(u *url.URL) Endpoints {
	basePath := stripWSRuntimeSuffix(normalizePath(u.Path))
	wsScheme := "ws"
	if u.Scheme == "https" {
		wsScheme = "wss"
	}
	return Endpoints{
		DisplayURL: buildURL(u, u.Scheme, basePath),
		HTTPBase:   buildURL(u, u.Scheme, basePath),
		WSURL:      buildURL(u, wsScheme, runtimePath(basePath)),
	}
}

func fromWS(u *url.URL) Endpoints {
	basePath := stripWSRuntimeSuffix(normalizePath(u.Path))
	httpScheme := "http"
	if u.Scheme == "wss" {
		httpScheme = "https"
	}
	return Endpoints{
		DisplayURL: buildURL(u, httpScheme, basePath),
		HTTPBase:   buildURL(u, httpScheme, basePath),
		WSURL:      buildURL(u, u.Scheme, runtimePath(basePath)),
	}
}

func buildURL(template *url.URL, scheme, path string) string {
	v := *template
	v.Scheme = scheme
	v.Path = path
	v.RawPath = ""
	v.RawQuery = ""
	v.Fragment = ""
	return v.String()
}

func normalizePath(path string) string {
	if path == "" || path == "/" {
		return ""
	}
	return strings.TrimSuffix(path, "/")
}

func stripWSRuntimeSuffix(path string) string {
	if path == "/ws/runtime" {
		return ""
	}
	if strings.HasSuffix(path, "/ws/runtime") {
		return normalizePath(strings.TrimSuffix(path, "/ws/runtime"))
	}
	return path
}

func runtimePath(basePath string) string {
	if basePath == "" {
		return "/ws/runtime"
	}
	return basePath + "/ws/runtime"
}
