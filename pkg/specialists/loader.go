package specialists

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SpecialistInfo holds metadata about a specialist.
type SpecialistInfo struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description"`
}

// SpecialistLoader discovers and loads specialist definitions from workspace/specialists/.
type SpecialistLoader struct {
	specialistsDir string
}

// NewSpecialistLoader creates a loader that scans workspace/specialists/.
func NewSpecialistLoader(workspace string) *SpecialistLoader {
	return &SpecialistLoader{
		specialistsDir: filepath.Join(workspace, "specialists"),
	}
}

// ListSpecialists scans for all specialist directories containing SPECIALIST.md.
func (sl *SpecialistLoader) ListSpecialists() []SpecialistInfo {
	var specialists []SpecialistInfo

	dirs, err := os.ReadDir(sl.specialistsDir)
	if err != nil {
		return specialists
	}

	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		specFile := filepath.Join(sl.specialistsDir, dir.Name(), "SPECIALIST.md")
		if _, err := os.Stat(specFile); err != nil {
			continue
		}

		info := SpecialistInfo{
			Name: dir.Name(),
			Path: specFile,
		}
		if meta := sl.getMetadata(specFile); meta != nil {
			info.Description = meta.Description
		}
		specialists = append(specialists, info)
	}

	return specialists
}

// LoadSpecialist reads a specialist's persona (SPECIALIST.md with frontmatter stripped).
func (sl *SpecialistLoader) LoadSpecialist(name string) (string, bool) {
	specFile := filepath.Join(sl.specialistsDir, name, "SPECIALIST.md")
	content, err := os.ReadFile(specFile)
	if err != nil {
		return "", false
	}
	return stripFrontmatter(string(content)), true
}

// GetMetadata returns parsed frontmatter metadata for a specialist.
func (sl *SpecialistLoader) GetMetadata(name string) *SpecialistInfo {
	specFile := filepath.Join(sl.specialistsDir, name, "SPECIALIST.md")
	if _, err := os.Stat(specFile); err != nil {
		return nil
	}
	meta := sl.getMetadata(specFile)
	if meta == nil {
		return &SpecialistInfo{Name: name, Path: specFile}
	}
	meta.Path = specFile
	return meta
}

// Exists checks whether a specialist with the given name exists.
func (sl *SpecialistLoader) Exists(name string) bool {
	specFile := filepath.Join(sl.specialistsDir, name, "SPECIALIST.md")
	_, err := os.Stat(specFile)
	return err == nil
}

// Dir returns the base specialists directory path.
func (sl *SpecialistLoader) Dir() string {
	return sl.specialistsDir
}

// BuildSpecialistsSummary returns an XML summary of all specialists for the system prompt.
func (sl *SpecialistLoader) BuildSpecialistsSummary() string {
	all := sl.ListSpecialists()
	if len(all) == 0 {
		return ""
	}

	var lines []string
	lines = append(lines, "<specialists>")
	for _, s := range all {
		lines = append(lines, "  <specialist>")
		lines = append(lines, fmt.Sprintf("    <name>%s</name>", escapeXML(s.Name)))
		lines = append(lines, fmt.Sprintf("    <description>%s</description>", escapeXML(s.Description)))
		lines = append(lines, "  </specialist>")
	}
	lines = append(lines, "</specialists>")

	return strings.Join(lines, "\n")
}

// --- internal helpers ---

func (sl *SpecialistLoader) getMetadata(path string) *SpecialistInfo {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	fm := extractFrontmatter(string(content))
	if fm == "" {
		return &SpecialistInfo{Name: filepath.Base(filepath.Dir(path))}
	}

	// Try JSON first
	var jsonMeta struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(fm), &jsonMeta); err == nil {
		return &SpecialistInfo{
			Name:        jsonMeta.Name,
			Description: jsonMeta.Description,
		}
	}

	// Fall back to simple YAML
	yamlMeta := parseSimpleYAML(fm)
	return &SpecialistInfo{
		Name:        yamlMeta["name"],
		Description: yamlMeta["description"],
	}
}

var frontmatterRe = regexp.MustCompile(`(?s)^---\n(.*)\n---`)
var frontmatterStripRe = regexp.MustCompile(`(?s)^---\n.*?\n---\n`)

func extractFrontmatter(content string) string {
	match := frontmatterRe.FindStringSubmatch(content)
	if len(match) > 1 {
		return match[1]
	}
	return ""
}

func stripFrontmatter(content string) string {
	return frontmatterStripRe.ReplaceAllString(content, "")
}

func parseSimpleYAML(content string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			value = strings.Trim(value, "\"'")
			result[key] = value
		}
	}
	return result
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
