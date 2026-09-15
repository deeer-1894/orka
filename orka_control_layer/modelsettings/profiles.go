package modelsettings

import (
	"encoding/json"
	"errors"
	"github.com/orka-oss/orka_core/modelprofile"
	"strings"
	"time"
)

// Check is evidence from a bounded provider exchange; empty is unverified.
type Check struct {
	Verified  bool      `json:"verified"`
	CheckedAt time.Time `json:"checked_at,omitzero"`
	Error     string    `json:"error,omitempty"`
}
type Verification struct {
	Text   Check `json:"text"`
	Vision Check `json:"vision"`
	Tools  Check `json:"tools"`
}

type Profiles struct {
	Profiles        []Config `json:"profiles"`
	ActiveProfileID string   `json:"active_profile_id"`
}
type PublicProfiles struct {
	Profiles        []PublicConfig `json:"profiles"`
	ActiveProfileID string         `json:"active_profile_id"`
}

func (p Profiles) Public() PublicProfiles {
	out := PublicProfiles{Profiles: []PublicConfig{}, ActiveProfileID: p.ActiveProfileID}
	for _, c := range p.Profiles {
		out.Profiles = append(out.Profiles, c.Public())
	}
	return out
}
func (c Config) String() string   { b, _ := json.Marshal(c.Public()); return string(b) }
func (c Config) GoString() string { return c.String() }

type diskProfile struct {
	Config
	Key string `json:"api_key,omitempty"`
}

func profilesFromDisk(d diskConfig, exists bool) (Profiles, error) {
	p := Profiles{Profiles: []Config{}, ActiveProfileID: d.ActiveProfileID}
	if !exists {
		return p, nil
	}
	if d.Profiles == nil {
		d.Config.Model = d.LegacyModel
		d.Config.Policies = migrateLegacyPolicies(d.Config)
		d.Config.ID, d.Config.Name = "legacy", "Legacy connection"
		d.Profiles = []diskProfile{{Config: d.Config, Key: d.Key}}
		p.ActiveProfileID = "legacy"
	}
	for _, item := range d.Profiles {
		c, err := normalize(item.Config)
		if err != nil {
			return Profiles{}, ErrStorage
		}
		c.APIKey = item.Key
		p.Profiles = append(p.Profiles, c)
	}
	if err := validateProfiles(p); err != nil {
		return Profiles{}, ErrStorage
	}
	return p, nil
}
func profilesToDisk(p Profiles) diskConfig {
	d := diskConfig{Profiles: []diskProfile{}, ActiveProfileID: p.ActiveProfileID}
	for _, c := range p.Profiles {
		d.Profiles = append(d.Profiles, diskProfile{Config: c, Key: c.APIKey})
	}
	return d
}
func validateProfiles(p Profiles) error {
	if len(p.Profiles) > 64 {
		return errors.New("too many profiles")
	}
	seen := map[string]bool{}
	for _, c := range p.Profiles {
		if c.ID == "deployment" {
			return errors.New("profile ID deployment is reserved for server configuration")
		}
		if c.ID == "" || !validText(c.ID, 128) || strings.TrimSpace(c.ID) != c.ID || seen[c.ID] || strings.TrimSpace(c.Name) == "" || !validText(c.Name, 128) {
			return errors.New("profiles require unique IDs and names")
		}
		seen[c.ID] = true
	}
	if len(p.Profiles) > 0 && !seen[p.ActiveProfileID] || len(p.Profiles) == 0 && p.ActiveProfileID != "" {
		return errors.New("active_profile_id must identify a profile")
	}
	return nil
}
func retainedVerification(prior, c Config) map[string]Verification {
	out := map[string]Verification{}
	if prior.BaseURL == c.BaseURL && prior.Protocol == c.Protocol && prior.APIKey == c.APIKey {
		for _, m := range c.Models {
			if v, ok := prior.Verified[m]; ok {
				out[m] = v
			}
		}
	}
	return out
}
func (s *Store) GetProfiles(owner string) (Profiles, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, exists, err := s.read(owner)
	if err != nil {
		return Profiles{}, err
	}
	return profilesFromDisk(d, exists)
}

// SaveProfiles replaces the owned collection atomically. Keys are write-only;
// omitted keys are retained only for the same ID, protocol and normalized URL.
// Verification is server-owned and invalidated by connection/model changes.
func (s *Store) SaveProfiles(owner string, p Profiles, keys map[string]string) (Profiles, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, exists, err := s.read(owner)
	if err != nil {
		return Profiles{}, err
	}
	prior, err := profilesFromDisk(d, exists)
	if err != nil {
		return Profiles{}, err
	}
	if err = validateProfiles(p); err != nil {
		return Profiles{}, err
	}
	old := map[string]Config{}
	for _, c := range prior.Profiles {
		old[c.ID] = c
	}
	out := Profiles{Profiles: []Config{}, ActiveProfileID: p.ActiveProfileID}
	for _, raw := range p.Profiles {
		c, err := normalize(raw)
		if err != nil {
			return Profiles{}, err
		}
		key := strings.TrimSpace(keys[c.ID])
		if !validText(key, 8192) {
			return Profiles{}, errors.New("invalid api_key")
		}
		prev := old[c.ID]
		c.Policies = inheritedPolicies(prev, c)
		c.APIKey = key
		if key == "" && prev.BaseURL == c.BaseURL && prev.Protocol == c.Protocol {
			c.APIKey = prev.APIKey
		}
		c.Verified = retainedVerification(prev, c)
		out.Profiles = append(out.Profiles, c)
	}
	if err = s.write(owner, profilesToDisk(out)); err != nil {
		return Profiles{}, err
	}
	return out, nil
}

func clonePolicies(p map[string]modelprofile.CallPolicy) map[string]modelprofile.CallPolicy {
	if p == nil {
		return nil
	}
	out := make(map[string]modelprofile.CallPolicy, len(p))
	for name, v := range p {
		out[name] = v
	}
	return out
}

// Only the legacy single-connection reader materializes the exact policies
// measured before profiles existed. New named connections never use this table.
func migrateLegacyPolicies(c Config) map[string]modelprofile.CallPolicy {
	out := clonePolicies(c.Policies)
	if out == nil {
		out = map[string]modelprofile.CallPolicy{}
	}
	for _, name := range OrderedModels(c.Model, c.Models) {
		if _, explicit := out[name]; explicit {
			continue
		}
		switch name {
		case "glm-5.3":
			out[name] = modelprofile.CallPolicy{FirstMaxTokens: 16384, MaxTokens: 16384, TimeoutSeconds: 300, ReasoningEffort: "low"}
		case "glm-5.3-flash":
			out[name] = modelprofile.CallPolicy{FirstMaxTokens: 8192, MaxTokens: 8192, TimeoutSeconds: 300, ReasoningEffort: "low"}
		case "deepseek-v4-pro", "deepseek-v4-flash":
			out[name] = modelprofile.CallPolicy{ReasoningEffort: "low"}
		}
	}
	return out
}

// Omitted optional policies preserve existing tuning for still-listed models;
// an explicit empty object clears it. This keeps old settings clients safe.
func inheritedPolicies(prior, c Config) map[string]modelprofile.CallPolicy {
	if c.Policies != nil {
		return c.Policies
	}
	if prior.Policies == nil {
		return nil
	}
	out := map[string]modelprofile.CallPolicy{}
	for _, name := range c.Models {
		if p, ok := prior.Policies[name]; ok {
			out[name] = p
		}
	}
	return out
}
