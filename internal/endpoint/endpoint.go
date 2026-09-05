package endpoint

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type Health int

const (
	Healthy Health = iota
	Unhealthy
)

func (h Health) String() string {
	if h == Unhealthy {
		return "UNHEALTHY"
	}
	return "HEALTHY"
}

type Endpoint struct {
	ID         string
	Provider   string
	Model      string
	ModelName  string
	BaseURL    string
	APIKey     string
	Region     string
	Weight     int
	Timeout    time.Duration
	APIVersion string
}

func (e Endpoint) UpstreamModel(requested string) string {
	if e.ModelName != "" {
		return e.ModelName
	}
	return requested
}

func ExpandEnv(value string) string {
	return os.ExpandEnv(strings.TrimSpace(value))
}

const (
	DefaultAzureAPIVersion     = "2024-02-15-preview"
	DefaultAnthropicAPIVersion = "2023-06-01"
)

func (e Endpoint) Kind() string {
	return strings.ToLower(strings.TrimSpace(e.Provider))
}

func (e Endpoint) AzureAPIVersion() string {
	if e.APIVersion != "" {
		return e.APIVersion
	}
	return DefaultAzureAPIVersion
}

func (e Endpoint) AnthropicVersion() string {
	if e.APIVersion != "" {
		return e.APIVersion
	}
	return DefaultAnthropicAPIVersion
}

func (e Endpoint) ProbeURL() string {
	base := strings.TrimRight(e.BaseURL, "/")
	switch e.Kind() {
	case "azure":
		return base + "/openai/models?api-version=" + e.AzureAPIVersion()
	case "claude", "anthropic":
		if strings.HasSuffix(base, "/v1") {
			return base + "/models"
		}
		return base + "/v1/models"
	default:
		return base + "/models"
	}
}

func (e Endpoint) SetAuth(req *http.Request) {
	if e.APIKey == "" || req == nil {
		return
	}
	switch e.Kind() {
	case "azure":
		req.Header.Set("api-key", e.APIKey)
	case "claude", "anthropic":
		req.Header.Set("x-api-key", e.APIKey)
		req.Header.Set("anthropic-version", e.AnthropicVersion())
	default:
		req.Header.Set("Authorization", "Bearer "+e.APIKey)
	}
}

func Validate(e Endpoint) error {
	if e.ID == "" {
		return fmt.Errorf("endpoint id is required")
	}
	if e.BaseURL == "" {
		return fmt.Errorf("endpoint %s: base_url is required", e.ID)
	}
	if e.Model == "" {
		return fmt.Errorf("endpoint %s: model is required", e.ID)
	}
	if e.Weight < 0 {
		return fmt.Errorf("endpoint %s: weight must be >= 0", e.ID)
	}
	return nil
}
