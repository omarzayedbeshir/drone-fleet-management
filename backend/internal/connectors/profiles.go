// Package connectors persists tenant-owned bindings and supervises their workers.
package connectors

import (
	"encoding/json"
	"errors"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/catalog"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Profile is trusted operator configuration. Never accept endpoints, paths or
// credentials from an API request. Live profiles must belong to one tenant.
type Profile struct {
	ID             string   `json:"id"`
	Label          string   `json:"label"`
	Source         string   `json:"source"`
	VendorLabel    string   `json:"vendor_label,omitempty"`
	Executable     string   `json:"executable,omitempty"`
	Arguments      []string `json:"arguments,omitempty"`
	Vendor         string   `json:"vendor"`
	Simulated      bool     `json:"simulated"`
	OrganisationID string   `json:"organisation_id,omitempty"`
	Endpoint       string   `json:"endpoint,omitempty"`
	CSVPath        string   `json:"csv_path,omitempty"`
	CredentialRef  string   `json:"credential_ref,omitempty"`
	QueueSize      int      `json:"queue_size,omitempty"`
	Speed          float64  `json:"speed,omitempty"`
	SystemID       uint8    `json:"system_id,omitempty"`
	ComponentID    uint8    `json:"component_id,omitempty"`
}
type ProfileSummary struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Source    string `json:"source"`
	Vendor    string `json:"vendor"`
	Simulated bool   `json:"simulated"`
}
type Profiles map[string]Profile

var profileID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func (p Profile) Validate() error {
	if p.QueueSize < 0 || p.QueueSize > 4096 {
		return errors.New("invalid profile queue capacity")
	}
	if !profileID.MatchString(p.ID) || len(p.Label) < 1 || len(p.Label) > 100 || math.IsNaN(p.Speed) || math.IsInf(p.Speed, 0) || p.Speed < 0 || p.Speed > 1000 {
		return errors.New("invalid connector profile")
	}
	if !catalog.ValidVendor(p.Vendor) {
		return errors.New("invalid profile vendor")
	}
	if !p.Simulated && !validUUID(p.OrganisationID) {
		return errors.New("live connector profiles require an organisation UUID")
	}
	if p.OrganisationID != "" && !validUUID(p.OrganisationID) {
		return errors.New("invalid profile organisation UUID")
	}
	if len(p.VendorLabel) > 100 || len(p.Arguments) > 32 {
		return errors.New("invalid module metadata")
	}
	switch p.Source {
	case "external":
		if p.Executable == "" {
			return errors.New("external profiles require an executable")
		}
		for _, arg := range p.Arguments {
			if len(arg) > 1024 {
				return errors.New("module argument too long")
			}
		}
	case "csv":
		if p.Vendor != "dji" || !p.Simulated || p.CSVPath == "" {
			return errors.New("CSV profiles require a simulated DJI-format recording")
		}
	case "dji":
		u, e := url.Parse(p.Endpoint)
		if p.Vendor != "dji" || e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(p.Simulated && u.Scheme == "http")) {
			return errors.New("DJI profiles require a clean HTTPS endpoint (HTTP allowed for simulated sources)")
		}
	case "ardupilot":
		u, e := url.Parse(p.Endpoint)
		if p.Vendor != "ardupilot" || e != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "tcp" && u.Scheme != "udp") || p.SystemID == 255 {
			return errors.New("invalid MAVLink profile")
		}
	default:
		return errors.New("unknown connector source")
	}
	return nil
}
func (p Profile) Allowed(org, vendor string) bool {
	return p.Vendor == vendor && (p.OrganisationID == "" || p.OrganisationID == org)
}
func (p Profile) Summary() ProfileSummary {
	return ProfileSummary{p.ID, p.Label, p.Source, p.Vendor, p.Simulated}
}
func LoadProfiles(path string) (Profiles, error) {
	profiles := []Profile{{ID: "demo-dji-csv", Label: "Nile Delta DJI recording", Source: "csv", Vendor: "dji", Simulated: true, CSVPath: "testdata/dji_sample.csv", Speed: 10}}
	base := "."
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, errors.New("cannot open connector profiles file")
		}
		defer f.Close()
		d := json.NewDecoder(io.LimitReader(f, 65537))
		d.DisallowUnknownFields()
		if err = d.Decode(&profiles); err != nil {
			return nil, errors.New("invalid connector profiles file")
		}
		if d.Decode(new(any)) != io.EOF {
			return nil, errors.New("profiles file must contain one JSON array")
		}
		base = filepath.Dir(path)
	}
	out := Profiles{}
	for _, p := range profiles {
		if err := p.Validate(); err != nil {
			return nil, err
		}
		if _, ok := out[p.ID]; ok {
			return nil, errors.New("duplicate connector profile ID")
		}
		if p.CSVPath != "" && !filepath.IsAbs(p.CSVPath) {
			p.CSVPath = filepath.Join(base, p.CSVPath)
		}
		if p.Executable != "" && !filepath.IsAbs(p.Executable) && (strings.ContainsRune(p.Executable, os.PathSeparator)) {
			p.Executable = filepath.Join(base, p.Executable)
		}
		out[p.ID] = p
	}
	return out, nil
}

// VendorDefinitions is metadata installation, not schema or core-code editing.
func (ps Profiles) VendorDefinitions() ([]catalog.Vendor, error) {
	out := map[string]string{}
	for _, v := range catalog.Builtins {
		out[v.Code] = v.Label
	}
	for _, p := range ps {
		label := p.VendorLabel
		if label == "" {
			if _, ok := out[p.Vendor]; ok {
				continue
			}
			label = p.Vendor
		}
		if previous, ok := out[p.Vendor]; ok && previous != label && previous != p.Vendor {
			return nil, errors.New("conflicting vendor labels")
		}
		out[p.Vendor] = label
	}
	vendors := []catalog.Vendor{}
	for code, label := range out {
		vendors = append(vendors, catalog.Vendor{Code: code, Label: label})
	}
	return vendors, nil
}
