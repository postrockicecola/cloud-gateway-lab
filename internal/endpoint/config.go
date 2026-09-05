package endpoint

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type fileConfig struct {
	Models map[string][]fileEndpoint `yaml:"models"`
}

type fileEndpoint struct {
	ID        string `yaml:"id"`
	Provider  string `yaml:"provider"`
	BaseURL   string `yaml:"base_url"`
	APIKey    string `yaml:"api_key"`
	Weight    int    `yaml:"weight"`
	Region    string `yaml:"region"`
	ModelName string `yaml:"model_name"`
	Timeout   string `yaml:"timeout"`
}

func LoadYAML(path string) ([]Endpoint, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseYAML(body)
}

func ParseYAML(body []byte) ([]Endpoint, error) {
	var cfg fileConfig
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		return nil, fmt.Errorf("parse endpoints yaml: %w", err)
	}
	var out []Endpoint
	for model, list := range cfg.Models {
		for _, raw := range list {
			ep := Endpoint{
				ID:        raw.ID,
				Provider:  ExpandEnv(raw.Provider),
				Model:     model,
				ModelName: ExpandEnv(raw.ModelName),
				BaseURL:   ExpandEnv(raw.BaseURL),
				APIKey:    ExpandEnv(raw.APIKey),
				Region:    raw.Region,
				Weight:    raw.Weight,
			}
			if raw.Timeout != "" {
				d, err := time.ParseDuration(raw.Timeout)
				if err != nil {
					return nil, fmt.Errorf("endpoint %s timeout: %w", raw.ID, err)
				}
				ep.Timeout = d
			}
			if err := Validate(ep); err != nil {
				return nil, err
			}
			out = append(out, ep)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no endpoints in config")
	}
	return out, nil
}

func Single(id, model, baseURL, apiKey string) Endpoint {
	return Endpoint{
		ID:       id,
		Provider: "openai",
		Model:    model,
		BaseURL:  baseURL,
		APIKey:   apiKey,
		Weight:   1,
		Timeout:  60 * time.Second,
	}
}
