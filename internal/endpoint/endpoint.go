package endpoint

import (
	"fmt"
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
	ID        string
	Provider  string
	Model     string
	ModelName string
	BaseURL   string
	APIKey    string
	Region    string
	Weight    int
	Timeout   time.Duration
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
